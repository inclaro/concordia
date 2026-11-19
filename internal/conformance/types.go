// Package conformance evaluates policy rules against observed evidence, reconciles
// the declared architecture with the observed one, and builds the check result.
package conformance

import (
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// RuleVerdict is the verdict of one rule.
type RuleVerdict string

// Rule verdicts.
const (
	VerdictPass          RuleVerdict = "pass"
	VerdictViolation     RuleVerdict = "violation"
	VerdictUnknown       RuleVerdict = "unknown"
	VerdictNotApplicable RuleVerdict = "not-applicable"
	VerdictError         RuleVerdict = "error"
)

// FindingKind is the kind of a finding.
type FindingKind string

// Finding kinds.
const (
	KindForbiddenDependency FindingKind = "forbidden-dependency"
	KindLayerViolation      FindingKind = "layer-violation"
	KindUndeclared          FindingKind = "undeclared"
	KindContradiction       FindingKind = "contradiction"
	KindUnobserved          FindingKind = "unobserved"
	KindCycle               FindingKind = "cycle"
	KindForbiddenPath       FindingKind = "forbidden-path"
	KindDisallowedDep       FindingKind = "disallowed-dependency"
	KindPeerDependency      FindingKind = "peer-dependency"
	KindNonPublicAPI        FindingKind = "non-public-api"
	KindOwnershipViolation  FindingKind = "ownership-violation"
	KindBudgetExceeded      FindingKind = "budget-exceeded"
	KindMissingEdge         FindingKind = "missing-edge"
	KindMissingEntity       FindingKind = "missing-entity"
	KindBindingGap          FindingKind = "binding-gap"
)

// ObservedRef names the observed node behind one end of a finding.
type ObservedRef struct {
	QualifiedName string `json:"qualifiedName"`
	FilePath      string `json:"filePath"`
	Label         string `json:"label"`
}

// EdgeRef describes an observed edge without its endpoints.
type EdgeRef struct {
	Kind             observation.CanonicalEdgeKind `json:"kind"`
	NativeKind       string                        `json:"nativeKind"`
	Confidence       observation.Confidence        `json:"confidence"`
	ResolutionMethod string                        `json:"resolutionMethod"`
}

// Location is a place in a file. Line is 0 (left out in JSON) when unknown.
type Location struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// StepEdge is the edge of an evidence step.
type StepEdge struct {
	Location *Location `json:"location,omitempty"`
	// Identity is set when the fact has an environment, an occurrence, an image
	// reference, or a lossy mark.
	Identity         *observation.EdgeIdentity     `json:"identity,omitempty"`
	Kind             observation.CanonicalEdgeKind `json:"kind"`
	NativeKind       string                        `json:"nativeKind"`
	Confidence       observation.Confidence        `json:"confidence"`
	ResolutionMethod string                        `json:"resolutionMethod"`
	From             string                        `json:"from"`
	To               string                        `json:"to"`
}

// EvidenceStep is one step of a finding evidence path: an observed node or an
// edge. Exactly one of the two fields is set.
type EvidenceStep struct {
	Node *observation.ObservedNode `json:"node,omitempty"`
	Edge *StepEdge                 `json:"edge,omitempty"`
}

// Endpoint is one end of a finding.
type Endpoint struct {
	Observed       *ObservedRef `json:"observed,omitempty"`
	ArchitectureID string       `json:"architectureId"`
}

// Finding is one violation.
type Finding struct {
	Owner          *string   `json:"owner"`
	DispositionRef *string   `json:"dispositionRef"`
	Edge           *EdgeRef  `json:"edge,omitempty"`
	Location       *Location `json:"location,omitempty"`
	// Decision is the `decision` block of an `adrGovernance` finding (design D7 of add-adr-governance). nil for
	// every other finding, so their JSON is unchanged.
	Decision    *FindingDecision       `json:"decision,omitempty"`
	Fingerprint string                 `json:"fingerprint"`
	RuleID      string                 `json:"ruleId"`
	Severity    policy.RuleSeverity    `json:"severity"`
	Kind        FindingKind            `json:"kind"`
	Message     string                 `json:"message"`
	Disposition exceptions.Disposition `json:"disposition"`
	Source      Endpoint               `json:"source"`
	Target      Endpoint               `json:"target"`
	// EvidencePath is never nil.
	EvidencePath []EvidenceStep `json:"evidencePath"`
	// RelatedLocations are sorted, unique, without the main location, never nil.
	RelatedLocations []Location `json:"relatedLocations"`
	// NextAction says what a person does next, for example "resolve binding" for a binding gap. Empty when none.
	NextAction string `json:"nextAction,omitempty"`
	// ObligationID is the ID of the obligation of a typed rule (see BuildObligations). Empty for the other families.
	ObligationID string `json:"obligationId,omitempty"`
	// Subject is the CALM ID that the obligation of a typed rule is about.
	Subject string `json:"subject,omitempty"`
	// Environment is the declared environment of the obligation. Empty when it has none.
	Environment string `json:"environment,omitempty"`
	// Result is the verdict of the obligation result that the finding reports: `violation`, or `unknown`
	// for a binding gap. Empty for the findings of the other families (their findings are violations).
	Result RuleVerdict `json:"result,omitempty"`
	// Occurrence is the occurrence discriminator of the result (for example a container name). Empty when none.
	Occurrence string `json:"occurrence,omitempty"`
	// Enforcement is the enforcement mode of the finding: the mode of its rule in its environment
	// (EnforcementConfig.Resolve). Only `mandatory` blocks; `shadow` findings stay out of the reports.
	Enforcement policy.Enforcement `json:"enforcement,omitempty"`
	// Reason is the closed unknown reason (see UnknownReason) of a finding with Result `unknown`. Empty for the others.
	Reason UnknownReason `json:"reason,omitempty"`
	// Expected is the condition that the rule requires (OUT-02). Never empty.
	Expected string `json:"expected"`
	// Observed is the observed evidence or the gap that blocks a definite result (OUT-02). Never empty.
	Observed string `json:"observed"`
	// Delta is the difference between the declared and the observed architecture that the finding relates to
	// (the reconciliation entry of its pair). null when the finding has no pair.
	Delta *FindingDelta `json:"delta"`
	// ProviderLimits names the capability profile and the loss modes of the families that the finding depends on.
	ProviderLimits ProviderLimits `json:"providerLimits"`
	// Area is the report area of the finding (see Areas).
	Area Area `json:"area"`
	// Related lists the decisions, views, and assessments that the finding relates to. It is `[]` when none: the
	// later changes that own those results fill it.
	Related []RelatedRef `json:"related"`
}

// Finding kinds of typed obligations. KindObligationViolation is an occurrence that is a violation: a forbidden,
// not permitted, or unexpected-direction relation. KindObligationAbsent is a required relation that is absent
// with complete qualified coverage. KindObligationUnknown is an obligation with no definite result: the finding shows
// the reason, the missing evidence or coverage, and the next action (OUT-02). It never gates by itself.
const (
	KindObligationViolation FindingKind = "obligation-violation"
	KindObligationAbsent    FindingKind = "obligation-absent"
	KindObligationUnknown   FindingKind = "obligation-unknown"
)

// Reason says why a rule is unknown or not applicable.
type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EdgeCoverage is the evidence state of one edge kind of a rule.
type EdgeCoverage struct {
	MinimumConfidence observation.Confidence `json:"minimumConfidence"`
	// Observed counts the candidate edges of the kind at or above the minimum confidence.
	Observed int `json:"observed"`
	// BelowThreshold counts the candidate edges of the kind below the minimum confidence.
	BelowThreshold int `json:"belowThreshold"`
	// Supported is false when the provider does not support the edge kind.
	Supported bool `json:"supported"`
}

// Coverage is the coverage of one rule.
type Coverage struct {
	// EdgeKinds has one entry for each edge kind that the rule traverses.
	EdgeKinds map[observation.CanonicalEdgeKind]EdgeCoverage `json:"edgeKinds"`
	// SkipReasons gives the reason of each skip count that is not zero. Never nil.
	SkipReasons map[string]string `json:"skipReasons"`
	// Predicates are the coverage records of the edge-kind families that the rule traverses, in the projects that the rule depends on. Never nil.
	Predicates []observation.PredicateCoverage `json:"predicates"`
	Files      observation.FileCoverage        `json:"files"`
	// Skipped counts the candidate edges that the rule left out, by reason. The edges stay in the evidence.
	Skipped SkipCounts `json:"skipped"`
	// Required is the required file ratio of the rule.
	Required float64 `json:"required"`
	// UnboundEndpoints counts candidate edges with an endpoint that has no binding.
	UnboundEndpoints int `json:"unboundEndpoints"`
	// UnresolvedReferences counts the `ambiguous-usage` edges that start in a subject.
	UnresolvedReferences int `json:"unresolvedReferences"`
	// SubThresholdEvidence counts edges that matched the predicate but are below the confidence threshold.
	SubThresholdEvidence int `json:"subThresholdEvidence"`
	// TraversalIncomplete is true when a cycle or path traversal stopped at the traversal limit. Such a rule never passes.
	TraversalIncomplete bool `json:"traversalIncomplete"`
}

// RuleResult is the result of one rule.
type RuleResult struct {
	Coverage *Coverage `json:"coverage,omitempty"`
	// SupersededBy is set when an explicit precedence list placed another rule above this one for an obligation
	// (the winner of the first superseded obligation; each obligation lists its own).
	SupersededBy *policy.Supersession `json:"supersededBy,omitempty"`
	Policy       policy.PackRef       `json:"policy"`
	// ChangedScope is "reduced" or "full" when the run is limited with --changed.
	ChangedScope string                 `json:"changedScope,omitempty"`
	Severity     policy.RuleSeverity    `json:"severity"`
	Family       policy.PredicateFamily `json:"family"`
	Verdict      RuleVerdict            `json:"verdict"`
	Name         string                 `json:"name"`
	// Enforcement is the enforcement mode of the rule (EnforcementConfig.Resolve). For a typed rule it is the
	// strongest mode of its obligations; the gates read the mode of each obligation.
	Enforcement policy.Enforcement `json:"enforcement,omitempty"`
	RuleID      string             `json:"ruleId"`
	Reasons     []Reason           `json:"reasons"`
	Subjects    []string           `json:"subjects"`
	Targets     []string           `json:"targets"`
	// Obligations are the obligation results of a typed rule, sorted by ID. Every result stays visible, whatever the
	// verdict of the rule is.
	Obligations  []ObligationResult `json:"obligations,omitempty"`
	Plan         policy.RulePlan    `json:"plan"`
	FindingCount int                `json:"findingCount"`
}

// Classification is the class of a reconciled CALM pair.
type Classification string

// Reconciliation classes.
const (
	ClassConforming    Classification = "conforming"
	ClassUndeclared    Classification = "undeclared"
	ClassUnobserved    Classification = "unobserved"
	ClassContradiction Classification = "contradiction"
	ClassUnknown       Classification = "unknown"
)

// ReconciliationEntry is the state of one directed CALM pair.
type ReconciliationEntry struct {
	From           string         `json:"from"`
	To             string         `json:"to"`
	RelationshipID string         `json:"relationshipId,omitempty"`
	Classification Classification `json:"classification"`
	// ObservedKinds are sorted, never nil.
	ObservedKinds []observation.CanonicalEdgeKind `json:"observedKinds"`
	EdgeCount     int                             `json:"edgeCount"`
}
