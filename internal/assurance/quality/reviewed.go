package quality

import (
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
)

// EvaluateReviewed gives the reviewed result of a scenario (design D21). evaluated says whether the run evaluated
// assessments at all (the review evaluation is not nil, which is the case exactly when the workspace has an
// `assessments` key), and currency is `CurrencyFor(<scenario id>)` of that evaluation. The function reads what the
// evaluation of the run already decided and evaluates nothing itself, so two evaluations cannot disagree.
//
//   - Not evaluated: `unknown` with `reviewed-mode-not-available` and the detail that the workspace has no
//     `assessments` key. Never a pass.
//   - A `quality` assessment that is `current` and approved is decided by its outcome: `satisfactory` is `pass`;
//     `satisfactory-with-conditions` is `pass` with the detail "satisfactory with conditions" (SubjectCurrency does
//     not carry the conditions); `unsatisfactory` is `violation`; `inconclusive` is `unknown` with
//     `no-evidence-supplied` and the detail "inconclusive".
//   - A quality assessment whose approval is a rejection: `violation`. A rejection normally makes the currency
//     state `review-required` with the reason `approval rejected`, because no approval applies; it is still a
//     decision about this version of the assessment and not a missing review, so it counts as a rejection both
//     then and when the state is `current`.
//   - Anything else (`review-required`, `expired`, `revoked`, `unknown`, not approved, or no quality assessment):
//     `unknown` with the currency state as the detail. The reason is the one that the currency reports for an
//     `unknown` state, and `no-evidence-supplied` otherwise, because no approval counts as evidence.
//
// When several quality assessments cover the scenario, the worst wins: a rejection, then an approved
// unsatisfactory assessment, then an approved inconclusive one, then a pass. So one approved assessment cannot
// hide a rejection or a bad outcome of another, while an assessment that is not current and approved never lowers
// a pass. Assessments of other types never count: a security assessment does not review a quality scenario.
func EvaluateReviewed(sc *Scenario, evaluated bool, currency []assessment.SubjectCurrency) Result {
	res := Result{Scenario: sc.ID, Mode: ModeReviewed, Verdict: VerdictUnknown, Details: []string{}}

	if !evaluated {
		res.Reason = ReasonReviewedModeNotAvailable
		res.Details = append(res.Details, DetailNoAssessmentsKey)

		return res
	}

	var conditions []string

	byBucket := map[bucket][]string{}
	unknownReason := ""

	for i := range currency {
		cur := &currency[i]
		if cur.Type != assessment.TypeQuality {
			continue
		}

		which, detail := classify(cur)
		byBucket[which] = append(byBucket[which], cur.ID)

		switch {
		case which == bucketPassed && detail != "":
			conditions = append(conditions, detail)
		case detail != "":
			res.Details = append(res.Details, detail)
		}

		if which == bucketOther && cur.State == assessment.CurrencyUnknown && unknownReason == "" {
			unknownReason = cur.UnknownReason
		}
	}

	rejected, unsatisfactory := byBucket[bucketRejected], byBucket[bucketUnsatisfactory]
	inconclusive, passed := byBucket[bucketInconclusive], byBucket[bucketPassed]

	// Worst wins: a rejection, then an approved unsatisfactory assessment (both violations), then an approved
	// inconclusive one, then a pass. An assessment that is not current and approved never lowers a pass.
	switch {
	case len(rejected) > 0:
		res.Verdict = VerdictViolation
		res.Evidence = rejected
	case len(unsatisfactory) > 0:
		res.Verdict = VerdictViolation
		res.Evidence = unsatisfactory
	case len(inconclusive) > 0:
		res.Reason = ReasonNoEvidenceSupplied
		res.Evidence = inconclusive
	case len(passed) > 0:
		res.Verdict = VerdictPass
		res.Evidence = passed
		res.Details = conditions

		if res.Details == nil {
			res.Details = []string{}
		}
	default:
		res.Reason = ReasonNoEvidenceSupplied
		if unknownReason != "" {
			res.Reason = unknownReason
		}

		if len(res.Details) == 0 {
			res.Details = append(res.Details, "no quality assessment covers the scenario")
		}
	}

	return res
}

// rejection says whether the assessment version was rejected by an approval record.
func rejection(cur *assessment.SubjectCurrency) bool {
	if cur.ReviewState != assessment.StateRejected {
		return false
	}

	return cur.State == assessment.CurrencyCurrent || slices.Contains(cur.Reasons, assessment.DetailApprovalRejected)
}

// notApprovedHint says that a current assessment is not approved, because the currency state alone ("current")
// would read as good news.
func notApprovedHint(cur *assessment.SubjectCurrency) string {
	if cur.State == assessment.CurrencyCurrent {
		return " (not approved: " + string(cur.ReviewState) + ")"
	}

	return ""
}

// bucket is the group that an assessment falls into when the verdict is decided.
type bucket int

const (
	// bucketOther is an assessment that is not current and approved, and not rejected.
	bucketOther bucket = iota
	bucketRejected
	bucketUnsatisfactory
	bucketInconclusive
	bucketPassed
)

// classify gives the bucket of a quality assessment and the detail to report for it, "" when there is none. An
// approval says that a reviewer accepted the assessment, and the outcome says what the assessment concludes: for a
// current approved assessment the outcome decides. SubjectCurrency does not carry the conditions of an assessment,
// so `satisfactory-with-conditions` is reported by name.
func classify(cur *assessment.SubjectCurrency) (which bucket, detail string) {
	switch {
	case rejection(cur):
		return bucketRejected, cur.ID + ": rejected"
	case cur.State != assessment.CurrencyCurrent || cur.ReviewState != assessment.StateApproved:
		return bucketOther, cur.ID + ": " + string(cur.State) + notApprovedHint(cur)
	}

	switch cur.Outcome {
	case assessment.OutcomeSatisfactory:
		return bucketPassed, ""
	case assessment.OutcomeSatisfactoryWithConditions:
		return bucketPassed, cur.ID + ": satisfactory with conditions"
	case assessment.OutcomeUnsatisfactory:
		return bucketUnsatisfactory, cur.ID + ": unsatisfactory"
	case assessment.OutcomeInconclusive:
	}

	// An outcome that this package does not know is not a conclusion.
	return bucketInconclusive, cur.ID + ": inconclusive"
}
