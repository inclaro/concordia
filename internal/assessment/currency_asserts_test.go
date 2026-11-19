package assessment_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/impact"
	wsp "github.com/inclaro/concordia/internal/workspace"
)

// currencyAssertNotApproved checks that an assessment without a verified approval is never approved and that its
// verdict is a violation that says so.
func currencyAssertNotApproved(t *testing.T, res *assessment.Result) {
	t.Helper()

	if res.State != assessment.StateReviewRequired && res.State != assessment.StateReadyForReview {
		t.Errorf("review state = %s, must never be approved", res.State)
	}

	verdict := res.Currency.Verdict()
	if verdict.Result != assessment.VerdictViolation || !strings.Contains(verdict.Detail, "not an approval") {
		t.Errorf("verdict = %+v", verdict)
	}
}

// currencyAssertEmptyBasis checks the one trigger of a subject that is compared against an empty basis.
func currencyAssertEmptyBasis(t *testing.T, res *assessment.Result) {
	t.Helper()

	if len(res.Currency.Triggers) != 1 || res.Currency.Triggers[0].Action != "get review" {
		t.Errorf("triggers = %+v, want one trigger against an empty basis", res.Currency.Triggers)
	}
}

// currencyAssertRequiredUnapproved checks the required entry of the edge assessment when no approval applies.
func currencyAssertRequiredUnapproved(t *testing.T, ev *assessment.Evaluation) {
	t.Helper()

	if len(ev.Required) != 1 {
		t.Fatalf("required = %+v", ev.Required)
	}

	if ev.Required[0].Currency.State != assessment.CurrencyReviewRequired || ev.Required[0].Version != 1 {
		t.Errorf("required = %+v", ev.Required)
	}
}

// currencyAssertApproval checks the approval that a current result names.
func currencyAssertApproval(t *testing.T, cur *assessment.Currency, id, mechanism string) {
	t.Helper()

	info := cur.Approval
	if info == nil {
		t.Errorf("approval = %+v", info)

		return
	}

	if info.ID != id || info.Mechanism != mechanism {
		t.Errorf("approval = %+v", info)
	}
}

// currencyAssertAddedExposure checks a trigger for an added item of the class exposure.
func currencyAssertAddedExposure(t *testing.T, trig *impact.ReviewTrigger, dependency string) {
	t.Helper()

	recordWant(t, "trigger class", trig.Class, impact.ClassExposure)
	recordWant(t, "trigger change", trig.Change, impact.ChangeAdded)
	recordWant(t, "trigger dependency", trig.Dependency, dependency)
	recordWant(t, "trigger assessment", trig.Assessment, "ASM-EDGE@1")
}

// currencyAssertRequiredNotCurrent checks that the one required entry is still in the evaluation and not current.
func currencyAssertRequiredNotCurrent(t *testing.T, ev *assessment.Evaluation, id string) {
	t.Helper()

	if len(ev.Required) != 1 {
		t.Fatalf("required = %+v", ev.Required)
	}

	if ev.Required[0].ID != id || ev.Required[0].Currency.State == assessment.CurrencyCurrent {
		t.Fatalf("required = %+v", ev.Required)
	}
}

// currencyAssertRemoval checks that the item is a removal that no disposition accounts for, and the trigger of
// the class removal.
func currencyAssertRemoval(t *testing.T, cur *assessment.Currency, identity string) {
	t.Helper()

	if len(cur.Undisposed) != 1 || cur.Undisposed[0].Identity != identity {
		t.Errorf("undisposed = %+v, triggers = %+v", cur.Undisposed, cur.Triggers)

		return
	}

	if len(cur.Triggers) == 0 || cur.Triggers[0].Class != impact.ClassRemoval {
		t.Errorf("undisposed = %+v, triggers = %+v", cur.Undisposed, cur.Triggers)
	}
}

// currencyAssertIncrementalBinding checks that an approval that binds a later version names the earlier one.
func currencyAssertIncrementalBinding(t *testing.T, second, first *assessment.Approval) {
	t.Helper()

	if second.Previous == nil || second.Delta == nil {
		t.Fatalf("binding gave previous = %+v, delta = %+v", second.Previous, second.Delta)
	}

	if second.Previous.Approval != first.ID {
		t.Fatalf("binding gave previous = %+v, delta = %+v", second.Previous, second.Delta)
	}
}

// currencyRequiredByID gives the required result with the id, nil when there is none.
func currencyRequiredByID(ev *assessment.Evaluation, id string) *assessment.RequiredResult {
	var got *assessment.RequiredResult

	for i := range ev.Required {
		if ev.Required[i].ID == id {
			got = &ev.Required[i]
		}
	}

	return got
}

// currencyAssertRequiredWithoutDocument checks a required entry whose assessment no document declares.
func currencyAssertRequiredWithoutDocument(t *testing.T, got *assessment.RequiredResult) {
	t.Helper()

	if got == nil {
		t.Fatalf("required = %+v", got)
	}

	if got.Currency.State != assessment.CurrencyReviewRequired || got.Version != 0 {
		t.Fatalf("required = %+v", got)
	}

	if !slices.Contains(got.Currency.Reasons, assessment.DetailNoApproval) {
		t.Fatalf("required = %+v", got)
	}
}

// currencyUnknownRequirement gives a required entry for the assessment ASM-GONE that no document declares.
func currencyUnknownRequirement() wsp.RequiredAssessment {
	return wsp.RequiredAssessment{ID: "data", Assessment: "ASM-GONE", Type: "security", Environment: "production", Authority: "security-review"}
}

// currencySingle gives the one result of the list for a subject, and stops the test when there are not exactly one.
func currencySingle(t *testing.T, list []assessment.SubjectCurrency, subject string) *assessment.SubjectCurrency {
	t.Helper()

	if len(list) != 1 {
		t.Fatalf("CurrencyFor(%s) = %+v", subject, list)
	}

	return &list[0]
}

// currencyAssertEdgeView checks the view of the edge assessment for the subject api-gateway.
func currencyAssertEdgeView(t *testing.T, view *assessment.SubjectCurrency, ap *assessment.Approval) {
	t.Helper()

	recordWant(t, "edge id", view.ID, "ASM-EDGE")
	recordWant(t, "edge state", view.State, assessment.CurrencyCurrent)
	recordWant(t, "edge approval", view.Approval, ap.ID)
	recordWant(t, "edge type", view.Type, assessment.TypeSecurity)
}

// currencyAssertQualityView checks the view of the quality assessment for the subject checkout.
func currencyAssertQualityView(t *testing.T, view *assessment.SubjectCurrency) {
	t.Helper()

	recordWant(t, "quality id", view.ID, "ASM-LATENCY")
	recordWant(t, "quality type", view.Type, assessment.TypeQuality)
	recordWant(t, "quality state", view.State, assessment.CurrencyReviewRequired)
	recordWant(t, "quality reason", slices.Contains(view.Reasons, assessment.DetailNoApproval), true)
}

// currencyQualityInput gives the input of an evaluation over the edge and the quality assessment, with the fact
// source that answers both.
func currencyQualityInput(t *testing.T) (*assessment.EvalInput, *fakeFacts) {
	t.Helper()

	files := edgeFiles()
	files["assessments/ASM-LATENCY/v1.yaml"] = qualityAssessment

	set, err := loadEdge(t, files)
	if err != nil {
		t.Fatal(err)
	}

	scenario := impact.V1().NewItem(impact.KindQualityScenario, "QS-LATENCY-1", impact.Facets{"scenario": {"QS-LATENCY-1"}}, nil, nil)
	facts := newFacts(entryPoint("k8s:ingress/api", "api.example.com"))
	facts.matches[impact.KindQualityScenario] = found(scenario)

	return &assessment.EvalInput{Set: set, Verifier: verifiedAll{}, Facts: facts, Now: func() time.Time { return atEval }}, facts
}

// currencyAssertNoViews checks that a subject that no assessment covers, and a nil evaluation, give an empty list.
func currencyAssertNoViews(t *testing.T, ev *assessment.Evaluation) {
	t.Helper()

	if got := ev.CurrencyFor("unrelated"); got == nil || len(got) != 0 {
		t.Errorf("CurrencyFor(unrelated) = %#v, want an empty list", got)
	}

	var none *assessment.Evaluation
	if got := none.CurrencyFor("api-gateway"); len(got) != 0 {
		t.Errorf("nil evaluation = %+v", got)
	}
}

// currencyAssertViewUnchanged checks that the evaluation still gives the view of the subject api-gateway that it
// gave before.
func currencyAssertViewUnchanged(t *testing.T, ev *assessment.Evaluation, before *assessment.SubjectCurrency) {
	t.Helper()

	again := ev.CurrencyFor("api-gateway")
	if len(again) == 0 {
		t.Fatal("CurrencyFor(api-gateway) is empty after the facts changed")
	}

	if !slices.Equal(again[0].Reasons, before.Reasons) || again[0].State != before.State {
		t.Errorf("CurrencyFor changed after the facts changed: %+v", again)
	}
}
