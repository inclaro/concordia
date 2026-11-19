package packs_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

type governanceRule struct {
	id         string
	severity   policy.RuleSeverity
	obligation string
}

// adrGovernanceRules are the rules of the adr-governance pack in file order. A rule ID is never reused or renumbered.
var adrGovernanceRules = []governanceRule{
	{"GOV-ADR-001", policy.SeverityError, "presence"},
	{"GOV-ADR-002", policy.SeverityError, "applicability"},
	{"GOV-ADR-003", policy.SeverityError, "justification"},
	{"GOV-ADR-004", policy.SeverityError, "enforcement"},
	{"GOV-ADR-005", policy.SeverityError, "history"},
	{"GOV-ADR-006", policy.SeverityWarning, "scope"},
}

// isGovernancePlan reports whether the plan is the governance rule that want describes.
func isGovernancePlan(plan *policy.RulePlan, want governanceRule) bool {
	return allTrue(
		plan.RuleID == want.id,
		plan.Severity == want.severity,
		plan.Predicate.Family == policy.FamilyDecisionGovernance,
		plan.Predicate.Decisions == want.obligation,
		plan.Family == string(policy.FamilyDecisionGovernance),
		hasDocs(plan),
	)
}

func TestADRGovernancePackCompilesToGovernanceRules(t *testing.T) {
	t.Parallel()

	result := compileCleanPack(t, "adr-governance")

	requireRuleCounts(t, len(result.Plans), len(result.DeclaredRules), len(adrGovernanceRules))

	for i, want := range adrGovernanceRules {
		plan := &result.Plans[i]
		if !isGovernancePlan(plan, want) {
			t.Errorf("plan %d = %+v, want %s %s %s", i, plan, want.id, want.severity, want.obligation)
		}

		requirePlanPack(t, plan, "adr-governance")
		requireNoEnforcement(t, plan)
	}
}

type reviewRule struct {
	id    string
	check string
}

// reviewCurrencyRules are the rules of the review-currency pack sorted by ID, as the plans are. A rule ID is never reused or renumbered.
var reviewCurrencyRules = []reviewRule{
	{"REV-APR-001", "approval"},
	{"REV-CAP-001", "capability"},
	{"REV-DSP-001", "disposition"},
	{"REV-GOV-001", "governance"},
}

// isAssessmentPlan reports whether the plan is the assessment rule that want describes.
func isAssessmentPlan(plan *policy.RulePlan, want reviewRule) bool {
	return allTrue(
		plan.RuleID == want.id,
		plan.Severity == policy.SeverityError,
		plan.Predicate.Family == policy.FamilyAssessmentCurrency,
		plan.Predicate.AssessmentCurrency == want.check,
		plan.Family == string(policy.FamilyAssessmentCurrency),
		hasDocs(plan),
	)
}

func TestReviewCurrencyPackCompilesToAssessmentRules(t *testing.T) {
	t.Parallel()

	result := compileCleanPack(t, "review-currency")

	requireRuleCounts(t, len(result.Plans), len(result.DeclaredRules), len(reviewCurrencyRules))

	for i, want := range reviewCurrencyRules {
		plan := &result.Plans[i]
		if !isAssessmentPlan(plan, want) {
			t.Errorf("plan %d = %+v, want %s %s", i, plan, want.id, want.check)
		}

		requirePlanPack(t, plan, "review-currency")
		requireNoEnforcement(t, plan)
	}
}
