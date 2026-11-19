package policy_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

const reviewRuleBody = "    family: assessment-currency\n    assessmentCurrency: approval\n"

func TestAssessmentCurrencyRuleCompilesToTheReviewFamily(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule(reviewRuleBody, "REV-X-001")))
	wantCodes(t, &res)

	if len(res.Plans) != 1 {
		t.Fatalf("plans = %+v", res.Plans)
	}

	plan := res.Plans[0]
	if plan.Predicate.Family != policy.FamilyAssessmentCurrency || plan.Predicate.AssessmentCurrency != "approval" ||
		plan.Family != "assessment-currency" {
		t.Errorf("predicate = %+v, family = %q", plan.Predicate, plan.Family)
	}

	if plan.Predicate.Family.EdgeFindings() {
		t.Error("the assessment-currency family has edge findings")
	}

	if len(plan.Subjects) != 0 || len(plan.Targets) != 0 || len(plan.Traversal.EdgeKinds) != 0 {
		t.Errorf("subjects = %v, targets = %v, traversal = %+v", plan.Subjects, plan.Targets, plan.Traversal)
	}
}

func TestAssessmentCurrencyRuleAcceptsEveryCheck(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"approval", "disposition", "capability", "governance"} {
		res := compileOne(t, pack(rule("    assessmentCurrency: "+name+"\n")))
		wantCodes(t, &res)

		if len(res.Plans) != 1 || res.Plans[0].Predicate.AssessmentCurrency != name {
			t.Errorf("%s: plans = %+v", name, res.Plans)
		}
	}
}

func TestAssessmentCurrencyRuleWithAnUnknownValueIsAnError(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    assessmentCurrency: freshness\n")))
	wantCodes(t, &res, "CO-POL-001")

	if len(res.Plans) != 0 {
		t.Errorf("plans = %+v", res.Plans)
	}
}

func TestAssessmentCurrencyRuleRejectsASubjectAndAnotherPredicate(t *testing.T) {
	t.Parallel()

	withSubject := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    assessmentCurrency: approval\n")))
	wantCodes(t, &withSubject, "CO-POL-001")

	other := compileOne(t, pack(rule("    assessmentCurrency: approval\n    observedRequired: {}\n")))
	wantCodes(t, &other, "CO-POL-001")
}
