package conformance_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/conformance"
)

func TestViewAssessmentCurrencyAggregatesTheMostSevereState(t *testing.T) {
	t.Parallel()

	result := func(id string, state assessment.CurrencyState, unknown string, subjects ...string) assessment.Result {
		return assessment.Result{
			ID: id, Version: 1, Type: assessment.TypeSecurity, Latest: true, Subjects: subjects,
			Currency: assessment.Currency{State: state, UnknownReason: unknown, Reasons: []string{}},
		}
	}

	ev := &assessment.Evaluation{Assessments: []assessment.Result{
		result("A", assessment.CurrencyCurrent, "", "node-1"),
		result("B", assessment.CurrencyUnknown, "unsupported-family", "node-1"),
		result("C", assessment.CurrencyExpired, "", "rel-1"),
	}}

	if got := conformance.ViewAssessmentCurrencyOf(nil, []string{"node-1"}); got.State != conformance.AssessmentCurrencyNotEvaluated || got.Assessments != nil {
		t.Errorf("nil evaluation: %+v", got)
	}

	got := conformance.ViewAssessmentCurrencyOf(ev, []string{"node-1"})
	if got.State != "unknown" || len(got.Assessments) != 2 || got.Assessments[1].Reasons[0] != "unsupported-family" {
		t.Errorf("node-1: %+v", got)
	}

	// Two subjects that name the same assessment list it once.
	both := conformance.ViewAssessmentCurrencyOf(ev, []string{"node-1", "rel-1", "node-1"})
	if both.State != "expired" || len(both.Assessments) != 3 {
		t.Errorf("both: %+v", both)
	}

	if none := conformance.ViewAssessmentCurrencyOf(ev, []string{"nowhere"}); none.State != conformance.AssessmentCurrencyNotEvaluated {
		t.Errorf("none: %+v", none)
	}
}
