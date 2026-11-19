package conformance

import "github.com/inclaro/concordia/internal/impact"

// The review-currency sections of `results.json` (design D13). All three are additive and left out when the run
// does not evaluate assessments, so the result of such a run is the one of before (schemaVersion stays 1.1). They
// are plain data: the checker makes them from the assessment.Evaluation of the run.

// AssessmentCurrencyReport is the top-level `assessmentCurrency` object: one entry for each assessment version and
// one for each required assessment, with the currency of the version and the triggers that make it need review.
type AssessmentCurrencyReport struct {
	// EvaluatedAt is the evaluation clock (ISO 8601 UTC): expiry is judged at it. It is volatile and not part of the
	// result digest.
	EvaluatedAt string `json:"evaluatedAt"`
	// ContractVersion is the digest contract that the run compared under.
	ContractVersion string `json:"contractVersion"`
	// CapabilityProfile is the id of the profile that supplied the evidence; empty without a fact source.
	CapabilityProfile string `json:"capabilityProfile"`
	// Assessments are the versions, sorted by id and version. Never nil.
	Assessments []AssessmentEntry `json:"assessments"`
	// Required are the required entries, sorted by id. Never nil.
	Required []RequiredEntry `json:"required"`
}

// AssessmentEntry is the currency of one assessment version.
type AssessmentEntry struct {
	// Approval is the verified approval that the currency rests on. nil when none applies.
	Approval *CurrencyApproval `json:"approval"`
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Title    string            `json:"title,omitempty"`
	// Environment is the declared environment of the version.
	Environment string `json:"environment"`
	Outcome     string `json:"outcome"`
	// State is the derived review state with the currency overlay: review-required, expired, and revoked replace
	// approved.
	State string `json:"state"`
	// Currency is `current`, `review-required`, `expired`, `revoked`, or `unknown`.
	Currency string `json:"currency"`
	// UnknownReason is the one closed reason of an `unknown` currency. Empty otherwise.
	UnknownReason string `json:"unknownReason,omitempty"`
	// Basis is the earlier-version approval that the comparison used because this version has none.
	Basis  string `json:"basis,omitempty"`
	File   string `json:"file"`
	Digest string `json:"digest"`
	// Reasons are the short details of a review-required, expired, or revoked currency. Never nil.
	Reasons []string `json:"reasons"`
	// Details are the sentences that explain the reasons. Never nil.
	Details []string `json:"details"`
	// Causes are the causes of an unknown currency. Never nil.
	Causes []CurrencyCause `json:"causes"`
	// NotApplied are the decision records of the version that failed verification, with the reason. Never nil.
	NotApplied []CurrencyNotApplied `json:"notApplied"`
	// Triggers are the reasons for review, sorted. Never nil.
	Triggers []impact.ReviewTrigger `json:"triggers"`
	// Required are the ids of the required entries that resolve to this version. Never nil.
	Required []string `json:"required"`
	// Analysis says how complete the comparison was.
	Analysis CurrencyAnalysis `json:"analysis"`
	Version  int              `json:"version"`
	Line     int              `json:"line,omitempty"`
	// Latest says that this is the highest version of its id.
	Latest bool `json:"latest"`
}

// RequiredEntry is the currency of one required assessment entry.
type RequiredEntry struct {
	ID          string `json:"id"`
	Assessment  string `json:"assessment"`
	Type        string `json:"type"`
	Environment string `json:"environment"`
	Authority   string `json:"authority"`
	// Currency is the currency of the version.
	Currency      string `json:"currency"`
	UnknownReason string `json:"unknownReason,omitempty"`
	// Verdict is the verdict that REV-APR-001 gives this entry: `pass`, `violation`, or `unknown`.
	Verdict RuleVerdict `json:"verdict"`
	Reasons []string    `json:"reasons"`
	// Version is the assessment version that the entry was evaluated against; 0 when no document exists.
	Version int `json:"version"`
}

// CurrencyApproval is the approval that a currency rests on.
type CurrencyApproval struct {
	ID        string `json:"id"`
	Decision  string `json:"decision"`
	Reviewer  string `json:"reviewer"`
	Authority string `json:"authority"`
	Mechanism string `json:"mechanism,omitempty"`
	DecidedAt string `json:"decidedAt"`
	// ExpiresAt is empty when the approval does not expire.
	ExpiresAt string `json:"expiresAt,omitempty"`
	// Reason is empty for an approval that counts and says why not for one that does not.
	Reason     string   `json:"reason,omitempty"`
	Conditions []string `json:"conditions"`
	Verified   bool     `json:"verified"`
}

// CurrencyCause is one cause of an unknown currency.
type CurrencyCause struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// CurrencyNotApplied is a decision record that failed verification and counts for nothing.
type CurrencyNotApplied struct {
	Record    string `json:"record"`
	Kind      string `json:"kind"`
	Authority string `json:"authority,omitempty"`
	Mechanism string `json:"mechanism,omitempty"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
}

// CurrencyAnalysis says how complete the comparison of one assessment version was.
type CurrencyAnalysis struct {
	Incomplete  *impact.Incomplete `json:"incomplete,omitempty"`
	Visited     int                `json:"visited"`
	ClosureSize int                `json:"closureSize"`
	Complete    bool               `json:"complete"`
	// Widened says that a selector could not be resolved and the full declared scope was compared.
	Widened bool `json:"widened"`
}

// GovernanceReport is the top-level `governance` object: the relaxations of the review configuration against the
// trust anchor and the dispositions of removed subjects.
type GovernanceReport struct {
	// Relaxations are the differences between the trust anchor and the candidate that loosen the review rules, the
	// enforcement, or an authority, with the governance disposition (REV-GOV-001). Never nil.
	Relaxations []GovernanceChange `json:"relaxations"`
	// Dispositions are the removed subjects with their disposition state (REV-DSP-001). Never nil.
	Dispositions []DispositionEntry `json:"dispositions"`
}

// GovernanceChange is one difference between the trust anchor and the candidate.
type GovernanceChange struct {
	// Kind is `rule`, `enforcement`, or `authority`.
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	// Change is `added`, `removed`, or `modified`.
	Change string `json:"change"`
	// Comparison is `stricter`, `equal`, `looser`, or `incomparable`.
	Comparison string `json:"comparison"`
	Digest     string `json:"digest"`
	Detail     string `json:"detail"`
	// Relaxation says that the change needs a governance disposition.
	Relaxation bool `json:"relaxation"`
}

// DispositionEntry is one removed or narrowed subject of an assessment and whether a verified disposition covers it.
type DispositionEntry struct {
	Assessment string `json:"assessment"`
	Kind       string `json:"kind"`
	Identity   string `json:"identity"`
	// State is `disposed` or `undisposed`.
	State string `json:"state"`
	// Disposition is `removed-intentionally`, `retired`, or `moved`; empty when undisposed.
	Disposition string `json:"disposition,omitempty"`
	MovedTo     string `json:"movedTo,omitempty"`
}

// AnalysisReport is the top-level `analysis` object: how the evaluation ran and how complete it was.
type AnalysisReport struct {
	// Incomplete is the first limit that stopped an analysis. nil when none did.
	Incomplete *impact.Incomplete `json:"incomplete,omitempty"`
	// Mode is `full` or `incremental`.
	Mode string `json:"mode"`
	// Visited is the number of graph nodes that the traversals visited.
	Visited int `json:"visited"`
	// ClosureSize is the sum of the dependency closures of the assessments.
	ClosureSize int `json:"closureSize"`
	// Invalidations is the number of approvals that a change of policy, authority, evaluator, normalization, or
	// digest contract made review-required.
	Invalidations int `json:"invalidations"`
	// CacheHits and CacheMisses are the counters of the incremental cache. Zero in a full run.
	CacheHits   int `json:"cacheHits"`
	CacheMisses int `json:"cacheMisses"`
	// IncompleteAnalyses is the number of assessment versions whose comparison could not run to the end (a limit or a
	// cause stopped it).
	IncompleteAnalyses int `json:"incompleteAnalyses"`
	// Complete is false when a limit or a cause stopped a comparison.
	Complete bool `json:"complete"`
}

// Analysis modes.
const (
	AnalysisFull        = "full"
	AnalysisIncremental = "incremental"
)
