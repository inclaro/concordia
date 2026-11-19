package conformance

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// The fetch asks for the supersession edges, the mentions and the file references when ADR governance is
// active (a register, or a selected governance rule), and for none of them otherwise.
func TestRequiredNativeKindsForADRGovernance(t *testing.T) {
	t.Parallel()

	governance := []policy.RulePlan{{RuleID: "GOV-ADR-001", Predicate: policy.Predicate{Family: policy.FamilyDecisionGovernance}}}
	register := &workspace.Workspace{ADR: &workspace.ADR{Files: []string{"docs/adr/register.yaml"}}}

	for name, tc := range map[string]struct {
		wspace *workspace.Workspace
		plans  []policy.RulePlan
		want   bool
	}{
		"nothing":             {wspace: &workspace.Workspace{}, want: false},
		"an adr key, no file": {wspace: &workspace.Workspace{ADR: &workspace.ADR{}}, want: false},
		"a register":          {wspace: register, want: true},
		"the pack":            {wspace: &workspace.Workspace{}, plans: governance, want: true},
	} {
		got := requiredNativeKinds(tc.wspace, tc.plans)
		for _, native := range []string{"SUPERSEDES", "MENTIONS", "REFERENCES_FILE"} {
			if slices.Contains(got, native) != tc.want {
				t.Errorf("%s: kinds %v: %s present = %t, want %t", name, got, native, !tc.want, tc.want)
			}
		}
	}
}

// A governance rule reports in `adrGovernance`, and the predicate dispatch never evaluates it.
func TestGovernanceFamilyIsSkippedByEvaluate(t *testing.T) {
	t.Parallel()

	if got := AreaOfFamily(policy.FamilyDecisionGovernance); got != AreaADRGovernance {
		t.Errorf("area = %s", got)
	}

	if policy.FamilyDecisionGovernance.EdgeFindings() {
		t.Error("a governance rule has no edge findings to reduce to the changed files")
	}

	rules := []RuleResult{{RuleID: "GOV-ADR-004", Family: policy.FamilyDecisionGovernance, Verdict: VerdictViolation, FindingCount: 1}}
	findings := []Finding{{RuleID: "GOV-ADR-004", Location: &Location{Path: "docs/adr/0007.md"}}}

	out, kept := reduceToChanged(rules, findings, map[string]struct{}{"other.go": {}})
	if len(kept) != 1 || out[0].ChangedScope != "full" || out[0].Verdict != VerdictViolation {
		t.Errorf("rule %+v kept %+v", out[0], kept)
	}

	if got := fullScopeRules(out); !slices.Equal(got, []string{"GOV-ADR-004"}) {
		t.Errorf("full scope %v", got)
	}

	r := &planRun{plan: &policy.RulePlan{Predicate: policy.Predicate{Family: policy.FamilyDecisionGovernance}}}
	if cands, err := r.candidates(); err != nil || cands != nil {
		t.Errorf("candidates %v, %v", cands, err)
	}
}
