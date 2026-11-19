package conformance

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

func TestReduceToChangedKeepsFullScopeRules(t *testing.T) {
	t.Parallel()

	rules := []RuleResult{
		{RuleID: "R1", Family: policy.FamilyBoundary, Verdict: VerdictViolation, FindingCount: 2},
		{RuleID: "R2", Family: policy.FamilyAcyclic, Verdict: VerdictPass},
		{RuleID: "R3", Family: policy.FamilyDeclaredOnly, Verdict: VerdictViolation, FindingCount: 1},
		{RuleID: "R4", Family: policy.FamilyDeclaredOnly, Verdict: VerdictViolation, FindingCount: 1, Reasons: []Reason{{Code: "CO-CON-021"}}},
	}
	findings := []Finding{
		{RuleID: "R1", Location: &Location{Path: "a.go", Line: 1}},
		{RuleID: "R1", Location: &Location{Path: "b.go", Line: 1}},
		{RuleID: "R3", Location: &Location{Path: "b.go"}},
		{RuleID: "R4", Location: &Location{Path: "b.go"}},
	}

	out, kept := reduceToChanged(rules, findings, map[string]struct{}{"a.go": {}})

	if len(kept) != 1 || kept[0].Location.Path != "a.go" {
		t.Fatalf("kept %+v", kept)
	}

	if out[0].ChangedScope != "reduced" || out[0].FindingCount != 1 || out[0].Verdict != VerdictViolation {
		t.Errorf("R1 %+v", out[0])
	}

	if out[1].ChangedScope != "full" {
		t.Errorf("R2 %+v", out[1])
	}

	if out[2].Verdict != VerdictPass || out[2].FindingCount != 0 {
		t.Errorf("R3 %+v", out[2])
	}

	if out[3].Verdict != VerdictUnknown {
		t.Errorf("R4 %+v", out[3])
	}

	if got := fullScopeRules(out); !slices.Equal(got, []string{"R2"}) {
		t.Errorf("full %v", got)
	}
}

func TestReduceToChangedMatchesEvidenceAndRelated(t *testing.T) {
	t.Parallel()

	f := Finding{
		RuleID: "R", Location: &Location{Path: "x.go"}, RelatedLocations: []Location{{Path: "r.go"}},
		EvidencePath: []EvidenceStep{{Edge: &StepEdge{Location: &Location{Path: "e.go"}}}},
	}

	for _, p := range []string{"x.go", "r.go", "e.go"} {
		if !touchesChanged(&f, map[string]struct{}{p: {}}) {
			t.Errorf("%s not touched", p)
		}
	}

	if touchesChanged(&f, map[string]struct{}{"z.go": {}}) {
		t.Error("z.go touched")
	}
}
