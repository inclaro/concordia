package conformance

import "slices"

// UnknownReason is the reason of an `unknown` result. The set is closed: every
// unknown result of an obligation has exactly one of these, and `results.json`
// shows the identifier (typed-obligations spec, "Results use a closed verdict set").
type UnknownReason string

// The closed set of unknown reasons.
const (
	// ReasonCoverageIncomplete: the coverage of a needed predicate in the scope is not `complete`
	// (partial, unknown, failed scope, or a loss mode applies). It also names a scope that failed.
	ReasonCoverageIncomplete UnknownReason = "coverage-incomplete"
	// ReasonUnsupportedFamily: the provider profile does not support the evidence family that the claim needs (CO-CON-031).
	ReasonUnsupportedFamily UnknownReason = "unsupported-family"
	// ReasonCapabilityRegression: a family that the lock profile supported is not supported in this run.
	ReasonCapabilityRegression UnknownReason = "capability-regression"
	// ReasonEnvironmentUnresolved: the environment of a fact or an obligation is not resolved.
	ReasonEnvironmentUnresolved UnknownReason = "environment-unresolved"
	// ReasonAmbiguousBinding: several non-exclusive bindings map one observed entity to different CALM elements.
	ReasonAmbiguousBinding UnknownReason = "ambiguous-binding"
	// ReasonBindingGap: an endpoint of a related fact has no binding (next action: resolve binding).
	ReasonBindingGap UnknownReason = "binding-gap"
	// ReasonEvidenceConflict: sources with the same subject and role disagree and no source wins.
	ReasonEvidenceConflict UnknownReason = "evidence-conflict"
	// ReasonSelectorPropertyUnknown: a selector needs a property that is missing for the fact or entity.
	ReasonSelectorPropertyUnknown UnknownReason = "selector-property-unknown"
	// ReasonLimitExceeded: a configured resource limit was exceeded (CO-CON-030).
	ReasonLimitExceeded UnknownReason = "limit-exceeded"

	// The reasons of review currency (add-review-currency) follow.
	// ReasonTargetNotDeclared: a committed reference names a target that the provider did not declare.
	ReasonTargetNotDeclared UnknownReason = "target-not-declared"
	// ReasonTrustAnchorUnreadable: the trust anchor is missing, shallow, or not an ancestor of HEAD (CO-ASM-007).
	ReasonTrustAnchorUnreadable UnknownReason = "trust-anchor-unreadable"
	// ReasonVerificationUnavailable: signature verification cannot run, for example keyless with `--offline` (CO-ASM-011).
	ReasonVerificationUnavailable UnknownReason = "verification-unavailable"
	// ReasonReferenceUnresolved: an implementation or verification path is missing at the revision.
	ReasonReferenceUnresolved UnknownReason = "reference-unresolved"

	// The reasons of assurance adapters (add-assurance-adapters) follow. Admissibility details such as
	// `different-revision` or `stale` are not reasons: they are listed as detail of the one reason.
	// ReasonClaimNotEnabled: a rule needs a claim that no loaded contract enables (not in the claim registry).
	ReasonClaimNotEnabled UnknownReason = "claim-not-enabled"
	// ReasonNoEvidenceSupplied: no statement or result was supplied for the check, or every one is inadmissible.
	ReasonNoEvidenceSupplied UnknownReason = "no-evidence-supplied"
	// ReasonNoEvaluatorAvailable: an effective-policy claim has no evaluator enabled for the environment.
	ReasonNoEvaluatorAvailable UnknownReason = "no-evaluator-available"
	// ReasonRenderedEvidenceMissing: the scope has no complete rendered-level evidence.
	ReasonRenderedEvidenceMissing UnknownReason = "rendered-evidence-missing"
	// ReasonEnforcementNotDeclared: an evaluator result needs an enforcement assumption that the workspace does not declare.
	ReasonEnforcementNotDeclared UnknownReason = "enforcement-not-declared"
	// ReasonReviewedModeNotAvailable: a reviewed quality scenario is evaluated in a workspace without `assessments`.
	ReasonReviewedModeNotAvailable UnknownReason = "reviewed-mode-not-available"
)

// unknownReasons is the closed set, in the order of the spec.
var unknownReasons = []UnknownReason{
	ReasonCoverageIncomplete, ReasonUnsupportedFamily, ReasonCapabilityRegression, ReasonEnvironmentUnresolved,
	ReasonAmbiguousBinding, ReasonBindingGap, ReasonEvidenceConflict, ReasonSelectorPropertyUnknown, ReasonLimitExceeded,
	// add-review-currency
	ReasonTargetNotDeclared, ReasonTrustAnchorUnreadable, ReasonVerificationUnavailable, ReasonReferenceUnresolved,
	// add-assurance-adapters
	ReasonClaimNotEnabled, ReasonNoEvidenceSupplied, ReasonNoEvaluatorAvailable, ReasonRenderedEvidenceMissing,
	ReasonEnforcementNotDeclared, ReasonReviewedModeNotAvailable,
}

// UnknownReasons gives the closed set of unknown reasons.
func UnknownReasons() []UnknownReason { return slices.Clone(unknownReasons) }

// Valid says whether the reason is in the closed set.
func (r UnknownReason) Valid() bool { return slices.Contains(unknownReasons, r) }

// String gives the identifier.
func (r UnknownReason) String() string { return string(r) }
