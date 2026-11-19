package conformance

import (
	"strings"

	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/policy"
)

// Finding reasons of the review rules, and the kind prefix of their findings.
const (
	reviewKindPrefix = "assessment-"

	reasonReviewRequired = "review-required"
	reasonExpired        = "expired"
	reasonRevoked        = "revoked"
	reasonNoApproval     = "no-approval"
	reasonUndisposed     = "undisposed-removal"
	reasonRelaxation     = "relaxation"
)

// NextActionGetReview is the next action of a review finding that a person settles by getting a review.
const NextActionGetReview = "get review"

// countFindings is the key of the finding count of the area.
const (
	countFindings      = "findings"
	countNotApplicable = "notApplicable"
)

// reviewAnchorSubject is the subject of the governance result that the trust anchor gives.
const reviewAnchorSubject = "trust-anchor"

// reviewSpec is one review finding before the common fields are set.
type reviewSpec struct {
	// Result is `violation` or `unknown`.
	Result RuleVerdict
	// Reason is the finding reason of a violation, or the closed unknown reason of an unknown.
	Reason string
	// Source and Target are the fingerprint inputs.
	Source, Target string
	// Subject is the assessment id.
	Subject     string
	Environment string
	// Key identifies the obligation within the rule (a required entry id, an item, or a setting).
	Key                         string
	Message, Expected, Observed string
	Location                    *Location
	NextAction                  string
	Related                     []string
}

func reviewObligationID(ruleID, subject, environment, key string) string {
	const length = 16

	return digest.SHA256HexString(strings.Join([]string{ruleID, subject, environment, key}, "\x1f"))[:length]
}

// reviewFindingFingerprint is the fingerprint of a review finding: the rule, the source (the required entry id, the
// subject), the target, and the canonical edge kind `review:<reason>`. The assessment version and the file are not
// inputs, so a finding survives a new version of the same assessment.
func reviewFindingFingerprint(ruleID, source, target, reason string) string {
	return Fingerprint(Parts{RuleID: ruleID, SourceArchitectureID: source, TargetArchitectureID: target, CanonicalEdgeKind: "review:" + reason})
}

func (c *checker) reviewFinding(plan *policy.RulePlan, spec *reviewSpec) Finding {
	related := make([]RelatedRef, 0, len(spec.Related))
	for _, id := range sortedUniqueStrings(spec.Related) {
		related = append(related, RelatedRef{Kind: "assessment", ID: id})
	}

	finding := Finding{
		Fingerprint:      reviewFindingFingerprint(plan.RuleID, spec.Source, spec.Target, spec.Reason),
		RuleID:           plan.RuleID,
		Severity:         plan.Severity,
		Kind:             FindingKind(reviewKindPrefix + spec.Reason),
		Message:          spec.Message,
		Source:           Endpoint{ArchitectureID: spec.Source},
		Target:           Endpoint{ArchitectureID: spec.Target},
		EvidencePath:     []EvidenceStep{},
		RelatedLocations: []Location{},
		Location:         spec.Location,
		NextAction:       spec.NextAction,
		ObligationID:     reviewObligationID(plan.RuleID, spec.Subject, spec.Environment, spec.Key),
		Subject:          spec.Subject,
		Environment:      spec.Environment,
		Result:           spec.Result,
		Expected:         spec.Expected,
		Observed:         spec.Observed,
		ProviderLimits:   ProviderLimits{Families: []FamilyLimit{}},
		Area:             AreaAssessmentCurrency,
		Related:          related,
	}

	if plan.Owner != "" {
		owner := plan.Owner
		finding.Owner = &owner
	}

	if spec.Result == VerdictUnknown {
		finding.Reason = closedReviewReason(spec.Reason)
	}

	return finding
}

// closedReviewReason gives the closed unknown reason; a value outside the set is `coverage-incomplete`.
func closedReviewReason(reason string) UnknownReason {
	if r := UnknownReason(reason); r.Valid() {
		return r
	}

	return ReasonCoverageIncomplete
}

// nextActionOfReview gives the next action of a review result: `get review` for a violation, else the action of the
// closed reason.
func nextActionOfReview(result RuleVerdict, reason string) string {
	if result == VerdictUnknown {
		return nextActionFor(closedReviewReason(reason))
	}

	return NextActionGetReview
}

func assessmentLocation(file string, line int) *Location {
	if file == "" {
		return nil
	}

	return &Location{Path: file, Line: line}
}
