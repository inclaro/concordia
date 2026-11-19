package adr_test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
)

func fingerprintsOf(out *conformance.DecisionEvaluation) []string {
	list := make([]string, 0, len(out.Findings))
	for i := range out.Findings {
		list = append(list, out.Findings[i].Fingerprint)
	}

	slices.Sort(list)

	return list
}

// Scenario: Stable identity across non-semantic edits.
// Scenario: ADR file is renamed.
// ADR body or file rename: the fingerprints of the findings stay.
func TestFindingFingerprintsSurviveBodyEditsAndRenames(t *testing.T) {
	t.Parallel()

	base := a12World(t, a12Before).evaluate()

	renamed := newWorld(t, "docs/adr/archive/0003-moved.md", adr7Path)
	renamed.addADR("1", "docs/adr/archive/0003-moved.md", "ADR-3", "ADR-0003: A new title after a body edit", "accepted", "Accepted")
	renamed.addADR("2", adr7Path, "ADR-7", "ADR-0007: Managed database", "accepted", "Accepted")
	renamed.supersede("2", "1")
	renamed.register = a12Before

	got := renamed.evaluate()
	if len(base.Findings) != 2 || !slices.Equal(fingerprintsOf(base), fingerprintsOf(got)) {
		t.Errorf("fingerprints %v, after rename and title edit %v", fingerprintsOf(base), fingerprintsOf(got))
	}

	if findingsOf(got, adr.RuleApplicability)[0].Location.Path != "docs/adr/archive/0003-moved.md" {
		t.Errorf("location did not follow the rename: %+v", findingsOf(got, adr.RuleApplicability)[0].Location)
	}
}

// A status change that changes the reason changes the fingerprint.
func TestFindingFingerprintChangesWithTheReason(t *testing.T) {
	t.Parallel()

	proposed := newWorld(t, adr3Path)
	proposed.addADR("1", adr3Path, "ADR-3", "ADR-0003: Orders", "proposed", "Proposed")
	proposed.register = a12Before

	superseded := a12World(t, a12Before)

	inactive := findingsOf(proposed.evaluate(), adr.RuleApplicability)
	replaced := findingsOf(superseded.evaluate(), adr.RuleApplicability)

	if len(inactive) != 1 || len(replaced) != 1 {
		t.Fatalf("inactive %+v, superseded %+v", inactive, replaced)
	}

	kinds := [2]conformance.FindingKind{inactive[0].Kind, replaced[0].Kind}
	if kinds != [2]conformance.FindingKind{"adr-inactive", "adr-superseded"} || inactive[0].Fingerprint == replaced[0].Fingerprint {
		t.Errorf("inactive %+v, superseded %+v", inactive, replaced)
	}

	// Same decision, same reason, run twice on different temp directories: same identity.
	again := findingsOf(a12World(t, a12Before).evaluate(), adr.RuleApplicability)[0]
	if again.Fingerprint != replaced[0].Fingerprint {
		t.Error("fingerprint depends on the run")
	}
}

// The fingerprint reads no path, line, title or digest: it is the plain fingerprint of rule, source, targets, kind.
func TestFindingFingerprintInputs(t *testing.T) {
	t.Parallel()

	const target = "ADR-7,CORE-DB-001,calm:node:orders-db"

	got := findingsOf(a12World(t, a12Before).evaluate(), adr.RuleApplicability)[0]
	want := conformance.Fingerprint(conformance.Parts{
		RuleID: "GOV-ADR-002", SourceArchitectureID: "ADR-3", TargetArchitectureID: target,
		CanonicalEdgeKind: "adr:superseded",
	})

	if gotIdentity, wantIdentity := [3]string{got.Fingerprint, got.Source.ArchitectureID, got.Target.ArchitectureID}, [3]string{want, "ADR-3", target}; gotIdentity != wantIdentity {
		t.Errorf("fingerprint %s (source %q target %q), want %s", got.Fingerprint, got.Source.ArchitectureID, got.Target.ArchitectureID, want)
	}

	// Target order does not matter.
	if adr.FindingFingerprint("GOV-ADR-002", "ADR-3", []string{"calm:node:orders-db", "CORE-DB-001", "ADR-7"}, "superseded") != want {
		t.Error("target order changes the fingerprint")
	}
}

// goldenFingerprintVector is one entry of test/golden/fingerprints.json.
type goldenFingerprintVector struct {
	Input struct {
		RuleID string `json:"ruleId"`
		Source string `json:"sourceArchitectureId"`
		Target string `json:"targetArchitectureId"`
		Kind   string `json:"canonicalEdgeKind"`
		Path   string `json:"normalizedRepositoryPath"`
	} `json:"input"`
	Result string `json:"result"`
}

func loadGoldenFingerprintVectors(t *testing.T) []goldenFingerprintVector {
	t.Helper()

	data, err := os.ReadFile("../../test/golden/fingerprints.json")
	if err != nil {
		t.Fatal(err)
	}

	var vectors []goldenFingerprintVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}

	return vectors
}

// adrVectorReason returns the reason of an ADR vector: the kind without its `adr:` prefix, and whether the vector is one.
func adrVectorReason(kind, path string) (string, bool) {
	reason, ok := strings.CutPrefix(kind, "adr:")

	return reason, ok && path == ""
}

// The ADR vectors of test/golden/fingerprints.json equal FindingFingerprint.
func TestFindingFingerprintGoldenVectors(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, v := range loadGoldenFingerprintVectors(t) {
		if !strings.HasPrefix(v.Input.RuleID, "GOV-ADR-") {
			continue
		}

		reason, ok := adrVectorReason(v.Input.Kind, v.Input.Path)
		if !ok {
			t.Errorf("vector %+v is not an ADR vector", v)

			continue
		}

		seen[reason] = true

		if got := adr.FindingFingerprint(v.Input.RuleID, v.Input.Source, strings.Split(v.Input.Target, ","), reason); got != v.Result {
			t.Errorf("%s %s: got %s, want %s", v.Input.RuleID, reason, got, v.Result)
		}
	}

	for _, reason := range adr.Reasons {
		if !seen[string(reason)] {
			t.Errorf("no golden vector for the reason %s", reason)
		}
	}
}

// Next actions of design D7 and of the spec scenario "Next actions by obligation".
func TestNextActionOfReasons(t *testing.T) {
	t.Parallel()

	want := map[adr.Reason]string{
		adr.ReasonIndexOnly: adr.NextActionSupplyEvidence, adr.ReasonAbsent: adr.NextActionSupplyEvidence,
		adr.ReasonPathMismatch: adr.NextActionSupplyEvidence, adr.ReasonTriggerUnsatisfied: adr.NextActionSupplyEvidence,
		adr.ReasonSuperseded: adr.NextActionGetReview, adr.ReasonInactive: adr.NextActionGetReview,
		adr.ReasonHistoricalLink: adr.NextActionGetReview, adr.ReasonRuleUnjustified: adr.NextActionGetReview,
		adr.ReasonEnforcementGap: adr.NextActionCorrectIntent, adr.ReasonAdvisoryOnly: adr.NextActionCorrectIntent,
		adr.ReasonCycle: adr.NextActionCorrectIntent, adr.ReasonMissingTarget: adr.NextActionCorrectIntent,
		adr.ReasonDuplicateID: adr.NextActionCorrectIntent, adr.ReasonContradictoryStatus: adr.NextActionCorrectIntent,
		adr.ReasonCompetingSuccessors: adr.NextActionCorrectIntent,
	}

	for _, reason := range adr.Reasons {
		if got := adr.NextActionOf(string(reason)); got != want[reason] {
			t.Errorf("%s: %q, want %q", reason, got, want[reason])
		}
	}

	for _, reason := range []string{adr.EvidenceNoCapability, adr.EvidenceNoRevision, adr.EvidenceExtractionIncomplete} {
		if got := adr.NextActionOf(reason); got != adr.NextActionFixAnalysis {
			t.Errorf("%s: %q, want fix analysis configuration", reason, got)
		}
	}
}

// absentFindingShape is the comparable part of a finding that the location scenarios assert on.
type absentFindingShape struct {
	Kind       conformance.FindingKind
	NextAction string
	Path       string
	Title      string
	HasLine    bool
}

// Presence of an unregistered file has the register as its place; a recognized ADR has its own path and line.
func TestFindingLocation(t *testing.T) {
	t.Parallel()

	assertAbsentFindingLocation(t)
	assertIndexOnlyFindingLocation(t)
}

func assertAbsentFindingLocation(t *testing.T) {
	t.Helper()

	w := newWorld(t)
	w.register = a12Before

	absent := findingsOf(w.evaluate(), adr.RulePresence)
	if len(absent) != 1 {
		t.Fatalf("absent finding = %+v", absent)
	}

	got := absentFindingShape{
		Kind: absent[0].Kind, NextAction: absent[0].NextAction, Path: absent[0].Location.Path, HasLine: absent[0].Location.Line != 0,
		Title: absent[0].Decision.Title,
	}

	want := absentFindingShape{Kind: "adr-absent", NextAction: adr.NextActionSupplyEvidence, Path: "gov/decisions.yaml", HasLine: true}
	if got != want {
		t.Errorf("absent finding = %+v", absent)
	}
}

func assertIndexOnlyFindingLocation(t *testing.T) {
	t.Helper()

	idx := newWorld(t) // the decision is indexed but not tracked
	idx.addADR("1", adr3Path, "ADR-3", "ADR-0003: Orders", "accepted", "Accepted")
	idx.register = a12Before

	only := findingsOf(idx.evaluate(), adr.RulePresence)
	if len(only) != 1 {
		t.Fatalf("index-only finding = %+v", only)
	}

	got := absentFindingShape{Kind: only[0].Kind, Path: only[0].Location.Path, Title: only[0].Decision.Title}
	if want := (absentFindingShape{Kind: "adr-index-only", Path: adr3Path, Title: "ADR-0003: Orders"}); got != want {
		t.Errorf("index-only finding = %+v", only)
	}

	if only[0].Expected == "" || only[0].Observed == "" {
		t.Errorf("finding fields = %+v", only[0])
	}

	if !slices.Equal(only[0].Related, []conformance.RelatedRef{{Kind: "decision", ID: "ADR-3"}}) {
		t.Errorf("finding related = %+v", only[0].Related)
	}
}
