package reporting

import (
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

// Only a finding about rendered documents falls back to the declared rendering source of its environment. A
// check-result finding of the same environment has no rendered source, so SARIF never points it at the chart.
func TestOnlyRenderedFindingsAreLocatedAtTheRenderingSource(t *testing.T) {
	t.Parallel()

	claimRule := conformance.RuleResult{RuleID: "ASR-REN-001", Plan: policy.RulePlan{Predicate: policy.Predicate{
		Family: policy.FamilyAssuranceClaim, AssuranceClaim: &policy.ClaimRequirement{ID: "complete-resource-set"},
	}}}
	checkRule := conformance.RuleResult{RuleID: "CHK-001", Plan: policy.RulePlan{Predicate: policy.Predicate{
		Family: policy.FamilyCheckResult, CheckResult: &policy.CheckResultRequirement{Check: "unit-tests"},
	}}}

	run := &conformance.Run{
		Rules:     []conformance.RuleResult{claimRule, checkRule},
		Assurance: &conformance.AssuranceReport{Rendered: []conformance.RenderedEntry{{Environment: "production", Source: "deploy/chart"}}},
	}

	rendered := primaryLocation(run, &conformance.Finding{RuleID: "ASR-REN-001", Environment: "production"})
	if rendered == nil || rendered.Path != "deploy/chart" {
		t.Errorf("rendered finding location = %+v, want deploy/chart", rendered)
	}

	if check := primaryLocation(run, &conformance.Finding{RuleID: "CHK-001", Environment: "production"}); check != nil {
		t.Errorf("check-result finding location = %+v, want none", check)
	}
}
