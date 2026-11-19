package adr_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
)

const adrReferencePage = "../../docs/reference/adr-governance.md"

// docTable gives the first column of the rows of every table of the page whose header starts with prefix, with
// the backticks removed.
func docTable(t *testing.T, prefix string) []string {
	t.Helper()

	page, err := os.ReadFile(adrReferencePage)
	if err != nil {
		t.Fatal(err)
	}

	var (
		out   []string
		inTab bool
	)

	for _, line := range strings.Split(string(page), "\n") {
		switch {
		case strings.HasPrefix(line, prefix):
			inTab = true
		case inTab && strings.HasPrefix(line, "|---"):
		case inTab && strings.HasPrefix(line, "|"):
			cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
			out = append(out, strings.Trim(strings.TrimSpace(cell), "`"))
		default:
			inTab = false
		}
	}

	return out
}

func TestDocumentedReasonsEqualTheConstants(t *testing.T) {
	known := map[string]bool{
		adr.EvidenceNoRevision: true, adr.EvidenceNoCapability: true, adr.EvidenceExtractionIncomplete: true,
		adr.EvidenceNotCommitted: true, string(adr.PresenceUncommittedEdit): true, string(adr.PresenceNotRecognized): true,
	}
	for _, r := range adr.Reasons {
		known[string(r)] = true
	}

	documented := docTable(t, "| Reason |")
	if len(documented) == 0 {
		t.Fatal("the page has no reason table")
	}

	for _, r := range documented {
		if !known[r] {
			t.Errorf("documented reason %q is not a constant of internal/adr", r)
		}
	}

	// Every finding reason is documented.
	for _, want := range adr.Reasons {
		if !slices.Contains(documented, string(want)) {
			t.Errorf("reason %q is not documented", want)
		}
	}

	// So is every evidence reason of an unknown result.
	for _, want := range []string{
		adr.EvidenceNoRevision, adr.EvidenceNoCapability, adr.EvidenceExtractionIncomplete,
		string(adr.PresenceUncommittedEdit), string(adr.PresenceNotRecognized),
	} {
		if !slices.Contains(documented, want) {
			t.Errorf("reason %q is not documented", want)
		}
	}
}

func TestDocumentedObligationsEqualTheConstants(t *testing.T) {
	want := []string{
		adr.RulePresence, adr.RuleApplicability, adr.RuleJustification, adr.RuleEnforcement, adr.RuleHistory, adr.RuleScope,
	}

	if got := docTable(t, "| Rule | Obligation |"); !slices.Equal(got, want) {
		t.Errorf("documented rules = %v, want %v", got, want)
	}
}

func TestDocumentedPresenceStatesEqualTheConstants(t *testing.T) {
	want := []string{
		string(adr.PresenceCommitted), string(adr.PresenceIndexOnly), string(adr.PresenceUncommittedEdit),
		string(adr.PresenceAbsent), string(adr.PresenceNotRecognized), string(adr.PresencePathMismatch),
		string(adr.PresenceUnverifiable),
	}

	if got := docTable(t, "| State | Result |"); !slices.Equal(got, want) {
		t.Fatalf("documented presence states = %v, want %v", got, want)
	}
}

func TestDocumentedApplicabilityStatesEqualTheConstants(t *testing.T) {
	want := []string{
		string(adr.ApplicabilityActive), string(adr.ApplicabilityInactive), string(adr.ApplicabilitySuperseded),
		string(adr.ApplicabilityAmbiguous), string(adr.ApplicabilityUnknown),
	}

	if got := docTable(t, "| State | Reason |"); !slices.Equal(got, want) {
		t.Fatalf("documented applicability states = %v, want %v", got, want)
	}
}
