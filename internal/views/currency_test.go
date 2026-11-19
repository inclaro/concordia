package views_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// currencyFixture is the standard test view with its committed revision, ready for repeated evaluation.
type currencyFixture struct {
	state *conformance.ViewInput
	git   *workspace.FakeGit
}

func newCurrencyFixture(t *testing.T) *currencyFixture {
	t.Helper()

	in := testInputs(t, "HTTPS", containerView("payments-container"))

	return &currencyFixture{
		git: committedRevision(t, in, &in.Workspace.Views[0]),
		state: &conformance.ViewInput{
			Workspace: in.Workspace, Reconcile: in.Observed.Reconcile, Evidence: in.Observed.Evidence, Bindings: in.Bindings,
			Policies: in.Policies, Coverage: in.Observed.Coverage, Reconciliation: in.Reconciliation,
		},
	}
}

// evaluate runs the view evaluation of the fixture with the given currency.
func (f *currencyFixture) evaluate(t *testing.T, currency *assessment.Evaluation) *conformance.ViewEvaluation {
	t.Helper()

	out, err := views.Evaluate(t.Context(), &conformance.ViewEvalInput{
		State: f.state, Git: f.git, ToolVersion: "test", OnUnknown: policy.UnknownFail, Currency: currency,
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// evaluationCovering gives an evaluation of three assessments: two cover the subject and one covers another.
func evaluationCovering(subject string) *assessment.Evaluation {
	approval := &assessment.ApprovalInfo{ID: "ASM-EDGE-v1-1", Decision: assessment.DecisionApproved, Verified: true}

	return &assessment.Evaluation{Assessments: []assessment.Result{
		{
			ID: "ASM-EDGE", Version: 1, Type: assessment.TypeSecurity, Latest: true, Subjects: []string{subject},
			Currency: assessment.Currency{State: assessment.CurrencyCurrent, Reasons: []string{}, Approval: approval},
		},
		{
			ID: "ASM-LATENCY", Version: 2, Type: assessment.TypeQuality, Latest: true, Subjects: []string{subject},
			Currency: assessment.Currency{State: assessment.CurrencyReviewRequired, Reasons: []string{assessment.DetailNoApproval}},
		},
		{
			ID: "ASM-OTHER", Version: 1, Type: assessment.TypeSecurity, Latest: true, Subjects: []string{"elsewhere"},
			Currency: assessment.Currency{State: assessment.CurrencyRevoked, Reasons: []string{assessment.DetailRevoked}},
		},
	}}
}

func isExpectedEdgeEntry(entry *conformance.ViewAssessment) bool {
	return entry.ID == "ASM-EDGE" && entry.Version == 1 && entry.Type == "security" && entry.State == "current" &&
		entry.Approval == "ASM-EDGE-v1-1"
}

func isExpectedLatencyEntry(entry *conformance.ViewAssessment) bool {
	return entry.ID == "ASM-LATENCY" && entry.Type == "quality" && slices.Equal(entry.Reasons, []string{assessment.DetailNoApproval})
}

// Scenario: View lists matching assessments.
func TestAViewListsTheAssessmentsThatCoverItsSubject(t *testing.T) {
	t.Parallel()

	fixture := newCurrencyFixture(t)
	plain := fixture.evaluate(t, nil)

	depicted := slices.Concat(plain.Views[0].Depicts.Elements, plain.Views[0].Depicts.Relationships)
	if len(depicted) == 0 {
		t.Fatal("the test view depicts nothing")
	}

	evaluation := evaluationCovering(depicted[0])
	got := fixture.evaluate(t, evaluation).Views[0].Assessment

	if got.State != "review-required" || len(got.Assessments) != 2 {
		t.Fatalf("assessment currency = %+v", got)
	}

	if first := &got.Assessments[0]; !isExpectedEdgeEntry(first) {
		t.Errorf("first = %+v", *first)
	}

	if second := &got.Assessments[1]; !isExpectedLatencyEntry(second) {
		t.Errorf("second = %+v", *second)
	}

	// An evaluation that covers nothing the view depicts leaves the entry not-evaluated, and the verdicts do not change.
	evaluation.Assessments = evaluation.Assessments[2:]
	again := fixture.evaluate(t, evaluation)

	if again.Views[0].Assessment.State != conformance.AssessmentCurrencyNotEvaluated || len(again.Views[0].Assessment.Assessments) != 0 {
		t.Errorf("assessment currency = %+v", again.Views[0].Assessment)
	}

	if again.Views[0].Presence.Status != plain.Views[0].Presence.Status || again.Views[0].Freshness.Status != plain.Views[0].Freshness.Status {
		t.Errorf("the checks changed with the currency: %+v vs %+v", again.Views[0], plain.Views[0])
	}
}

// evaluateViews runs the view evaluation of the standard test view with the given currency.
func evaluateViews(t *testing.T, currency *assessment.Evaluation) *conformance.ViewEvaluation {
	t.Helper()

	return newCurrencyFixture(t).evaluate(t, currency)
}

// requireViewsNotEvaluated checks that every view is `not-evaluated` and lists no assessment.
func requireViewsNotEvaluated(t *testing.T, out *conformance.ViewEvaluation) {
	t.Helper()

	for i := range out.Views {
		if got := out.Views[i].Assessment; got.State != conformance.AssessmentCurrencyNotEvaluated || len(got.Assessments) != 0 {
			t.Errorf("view %s: assessment currency = %+v", out.Views[i].ID, got)
		}
	}
}

// requireSameVerdicts checks that two evaluations have the same rules, findings and rule verdicts.
func requireSameVerdicts(t *testing.T, a, b *conformance.ViewEvaluation) {
	t.Helper()

	if len(a.Rules) != len(b.Rules) || len(a.Findings) != len(b.Findings) {
		t.Fatalf("rules %d/%d, findings %d/%d", len(a.Rules), len(b.Rules), len(a.Findings), len(b.Findings))
	}

	for i := range a.Rules {
		if a.Rules[i].Verdict != b.Rules[i].Verdict {
			t.Errorf("rule %s: verdict %s vs %s", a.Rules[i].RuleID, a.Rules[i].Verdict, b.Rules[i].Verdict)
		}
	}
}

// Scenario: No assessment capability.
// Without an evaluation every view is `not-evaluated` with no assessment, and the rule verdicts and findings are those
// of a run whose evaluation covers none of the views.
func TestWithoutAssessmentsEveryViewIsNotEvaluated(t *testing.T) {
	t.Parallel()

	plain := evaluateViews(t, nil)
	requireViewsNotEvaluated(t, plain)

	requireSameVerdicts(t, plain, evaluateViews(t, &assessment.Evaluation{}))
}

// requireStableInterfaceValues checks that a view has the same semantic digest and depicted subject in two runs.
func requireStableInterfaceValues(t *testing.T, a, b *conformance.ViewResult) {
	t.Helper()

	if a.SemanticDigest == nil || b.SemanticDigest == nil || *a.SemanticDigest != *b.SemanticDigest {
		t.Errorf("view %s: semantic digest %v vs %v", a.ID, a.SemanticDigest, b.SemanticDigest)
	}

	if !slices.Equal(a.Depicts.Elements, b.Depicts.Elements) || !slices.Equal(a.Depicts.Relationships, b.Depicts.Relationships) {
		t.Errorf("view %s: depicts %+v vs %+v", a.ID, a.Depicts, b.Depicts)
	}
}

// Scenario: Interface values are stable.
// Two runs over an unchanged topology give the same semantic digest and the same depicted subject.
func TestViewInterfaceValuesAreStableBetweenRuns(t *testing.T) {
	t.Parallel()

	first, second := evaluateViews(t, nil), evaluateViews(t, nil)

	if len(first.Views) == 0 || len(first.Views) != len(second.Views) {
		t.Fatalf("views %d/%d", len(first.Views), len(second.Views))
	}

	for i := range first.Views {
		requireStableInterfaceValues(t, &first.Views[i], &second.Views[i])
	}
}
