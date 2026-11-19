package quality_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/assurance/quality"
)

func reviewedScenario() *quality.Scenario {
	return &quality.Scenario{ID: "QS-R", Mode: quality.ModeReviewed, Area: quality.AreaOperability}
}

// currency builds the SubjectCurrency that CurrencyFor gives for one quality assessment with the outcome
// satisfactory.
func currency(id string, state assessment.CurrencyState, review assessment.State, reasons ...string) assessment.SubjectCurrency {
	return assessment.SubjectCurrency{
		ID: id, Type: assessment.TypeQuality, State: state, ReviewState: review, Reasons: reasons, Required: []string{}, Version: 1,
		Outcome: assessment.OutcomeSatisfactory,
	}
}

// approved builds a current approved quality assessment with the outcome.
func approved(id string, outcome assessment.Outcome) assessment.SubjectCurrency {
	cur := currency(id, assessment.CurrencyCurrent, assessment.StateApproved)
	cur.Outcome = outcome

	return cur
}

// reviewedCase is one case of TestEvaluateReviewed.
type reviewedCase struct {
	name     string
	input    []assessment.SubjectCurrency
	verdict  quality.Verdict
	reason   string
	detail   string
	evidence []string
}

func reviewedCases() []reviewedCase {
	return []reviewedCase{
		{"current and approved", []assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyCurrent, assessment.StateApproved)},
			quality.VerdictPass, "", "", []string{"QA-1"}},
		{"approved with conditions", []assessment.SubjectCurrency{approved("QA-1", assessment.OutcomeSatisfactoryWithConditions)},
			quality.VerdictPass, "", "QA-1: satisfactory with conditions", []string{"QA-1"}},
		{"approved but unsatisfactory", []assessment.SubjectCurrency{approved("QA-1", assessment.OutcomeUnsatisfactory)},
			quality.VerdictViolation, "", "QA-1: unsatisfactory", []string{"QA-1"}},
		{"approved but inconclusive", []assessment.SubjectCurrency{approved("QA-1", assessment.OutcomeInconclusive)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: inconclusive", []string{"QA-1"}},
		{"satisfactory approval does not hide an unsatisfactory one", []assessment.SubjectCurrency{
			approved("QA-1", assessment.OutcomeSatisfactory), approved("QA-2", assessment.OutcomeUnsatisfactory),
		}, quality.VerdictViolation, "", "QA-2: unsatisfactory", []string{"QA-2"}},
		{"satisfactory approval does not hide an inconclusive one", []assessment.SubjectCurrency{
			approved("QA-1", assessment.OutcomeSatisfactory), approved("QA-2", assessment.OutcomeInconclusive),
		}, quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-2: inconclusive", []string{"QA-2"}},
		{"rejection beats an unsatisfactory approval", []assessment.SubjectCurrency{
			approved("QA-1", assessment.OutcomeUnsatisfactory), currency("QA-2", assessment.CurrencyCurrent, assessment.StateRejected),
		}, quality.VerdictViolation, "", "QA-2: rejected", []string{"QA-2"}},
		{"review required",
			[]assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyReviewRequired, assessment.StateReviewRequired, assessment.DetailSubjectDigestMismatch)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: review-required", nil},
		{"expired", []assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyExpired, assessment.StateExpired, assessment.DetailExpired)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: expired", nil},
		{"revoked", []assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyRevoked, assessment.StateRevoked, assessment.DetailRevoked)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: revoked", nil},
		{"unknown keeps the currency reason", []assessment.SubjectCurrency{{
			ID: "QA-1", Type: assessment.TypeQuality, State: assessment.CurrencyUnknown, ReviewState: assessment.StateApproved,
			UnknownReason: assessment.ReasonCoverageIncomplete,
		}}, quality.VerdictUnknown, assessment.ReasonCoverageIncomplete, "QA-1: unknown", nil},
		{"draft is not approved", []assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyReviewRequired, assessment.StateDraft, assessment.DetailNoApproval)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: review-required", nil},
		{"current but not approved", []assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyCurrent, assessment.StateReadyForReview)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: current (not approved: ready-for-review)", nil},
		{"current rejected", []assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyCurrent, assessment.StateRejected)},
			quality.VerdictViolation, "", "QA-1: rejected", []string{"QA-1"}},
		{"rejection as the evaluation reports it",
			[]assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyReviewRequired, assessment.StateRejected, assessment.DetailApprovalRejected)},
			quality.VerdictViolation, "", "QA-1: rejected", []string{"QA-1"}},
		{"rejected state without a rejection reason stays unknown",
			[]assessment.SubjectCurrency{currency("QA-1", assessment.CurrencyReviewRequired, assessment.StateRejected, assessment.DetailSubjectDigestMismatch)},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "QA-1: review-required", nil},
		{"no assessment covers the scenario", []assessment.SubjectCurrency{},
			quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "no quality assessment covers the scenario", nil},
		{"a security assessment never counts", []assessment.SubjectCurrency{{
			ID: "SA-1", Type: assessment.TypeSecurity, State: assessment.CurrencyCurrent, ReviewState: assessment.StateApproved,
		}}, quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, "no quality assessment covers the scenario", nil},
		{"one approved assessment does not hide a rejection", []assessment.SubjectCurrency{
			currency("QA-1", assessment.CurrencyCurrent, assessment.StateApproved),
			currency("QA-2", assessment.CurrencyCurrent, assessment.StateRejected),
		}, quality.VerdictViolation, "", "QA-2: rejected", []string{"QA-2"}},
		{"an approved assessment passes beside a stale one", []assessment.SubjectCurrency{
			currency("QA-1", assessment.CurrencyReviewRequired, assessment.StateReviewRequired),
			currency("QA-2", assessment.CurrencyCurrent, assessment.StateApproved),
		}, quality.VerdictPass, "", "", []string{"QA-2"}},
	}
}

func TestEvaluateReviewed(t *testing.T) {
	t.Parallel()

	for _, tt := range reviewedCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := quality.EvaluateReviewed(reviewedScenario(), true, tt.input)

			if got.Verdict != tt.verdict || got.Reason != tt.reason {
				t.Fatalf("verdict=%s reason=%q, want %s %q (details %v)", got.Verdict, got.Reason, tt.verdict, tt.reason, got.Details)
			}

			expectLoaded(t, "mode", got.Mode, quality.ModeReviewed)
			expectReviewedDetail(t, got.Details, tt.detail)
			expectReviewedEvidence(t, got.Evidence, tt.evidence)
		})
	}
}

// expectReviewedDetail requires no details when want is empty, and else that the last detail is want.
func expectReviewedDetail(t *testing.T, details []string, want string) {
	t.Helper()

	if want == "" {
		if len(details) != 0 {
			t.Errorf("details = %v, want none", details)
		}

		return
	}

	if len(details) == 0 || details[len(details)-1] != want {
		t.Errorf("details = %v, want last %q", details, want)
	}
}

// expectReviewedEvidence requires as many evidence items as wanted, and the first one to be the wanted first.
func expectReviewedEvidence(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("evidence = %v, want %v", got, want)

		return
	}

	if len(want) > 0 && got[0] != want[0] {
		t.Errorf("evidence = %v, want %v", got, want)
	}
}

func TestEvaluateReviewedWithoutAssessments(t *testing.T) {
	t.Parallel()

	// A nil evaluation gives CurrencyFor an empty list, and even an approved entry cannot make a pass when the run
	// evaluated no assessments.
	var evaluation *assessment.Evaluation

	for _, input := range [][]assessment.SubjectCurrency{
		evaluation.CurrencyFor("QS-R"),
		{currency("QA-1", assessment.CurrencyCurrent, assessment.StateApproved)},
	} {
		got := quality.EvaluateReviewed(reviewedScenario(), false, input)

		if got.Verdict != quality.VerdictUnknown || got.Reason != quality.ReasonReviewedModeNotAvailable {
			t.Fatalf("verdict=%s reason=%q", got.Verdict, got.Reason)
		}

		if len(got.Details) != 1 || got.Details[0] != "the workspace has no assessments key" {
			t.Errorf("details = %v", got.Details)
		}

		if got.Satisfies(reviewedScenario()) {
			t.Error("a reviewed scenario is satisfied without assessments")
		}
	}
}

func TestReviewedModeLeavesOtherModesUnaffected(t *testing.T) {
	t.Parallel()

	structural := &quality.Scenario{ID: "QS-S", Mode: quality.ModeStructural, Criterion: quality.Criterion{Rules: []string{"a"}}}

	got := quality.EvaluateStructural(structural, map[string]quality.RuleResult{"a": {Verdict: quality.VerdictPass}})
	if got.Verdict != quality.VerdictPass {
		t.Errorf("structural scenario with no assessments = %s, want pass", got.Verdict)
	}
}
