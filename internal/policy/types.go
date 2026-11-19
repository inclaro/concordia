// Package policy compiles policy packs into rule plans and applies the CALM
// control configurations to them.
package policy

import (
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// UnknownDisposition says what an unknown rule result becomes.
type UnknownDisposition string

// Unknown dispositions.
const (
	UnknownFail   UnknownDisposition = "fail"
	UnknownWarn   UnknownDisposition = "warn"
	UnknownIgnore UnknownDisposition = "ignore"
)

// RuleSeverity is the severity of a rule.
type RuleSeverity string

// Rule severities.
const (
	SeverityError   RuleSeverity = "error"
	SeverityWarning RuleSeverity = "warning"
	SeverityInfo    RuleSeverity = "info"
)

// EvidenceRequirements are the evidence settings of one rule after all overrides.
// MinimumCoverage is the required file coverage ratio, 0..1.
type EvidenceRequirements struct {
	// EdgeKindConfidence sets a minimum confidence for single edge kinds. It
	// replaces MinimumConfidence for these kinds.
	EdgeKindConfidence map[observation.CanonicalEdgeKind]observation.Confidence `json:"edgeKindConfidence,omitempty"`
	// MinimumByteCoverage is the required byte coverage ratio. nil means no requirement.
	MinimumByteCoverage *float64 `json:"minimumByteCoverage,omitempty"`
	// MinimumLanguageCoverage is the required ratio of each language. nil means MinimumCoverage.
	MinimumLanguageCoverage *float64               `json:"minimumLanguageCoverage,omitempty"`
	MinimumConfidence       observation.Confidence `json:"minimumConfidence"`
	OnUnknown               UnknownDisposition     `json:"onUnknown"`
	MinimumCoverage         float64                `json:"minimumCoverage"`
}

// MinimumConfidenceFor gives the minimum confidence of edges of kind.
func (e *EvidenceRequirements) MinimumConfidenceFor(kind observation.CanonicalEdgeKind) observation.Confidence {
	if c, ok := e.EdgeKindConfidence[kind]; ok {
		return c
	}

	return e.MinimumConfidence
}

// LanguageThreshold gives the required coverage ratio of each language.
func (e *EvidenceRequirements) LanguageThreshold() float64 {
	if e.MinimumLanguageCoverage != nil {
		return *e.MinimumLanguageCoverage
	}

	return e.MinimumCoverage
}

// BoundaryAllow is the `allow` clause of a `forbid` rule. All lists are
// sorted, unique and not nil.
type BoundaryAllow struct {
	// TargetSourceRoots are globs matched against the target observed file path.
	TargetSourceRoots []string `json:"targetSourceRoots"`
	// TargetCalmNodes: the edge is allowed when its target binds to one of these CALM IDs.
	TargetCalmNodes []string `json:"targetCalmNodes"`
	// EdgeKinds: the edge is allowed when its canonical kind is in this list.
	EdgeKinds []observation.CanonicalEdgeKind `json:"edgeKinds"`
	// TargetTags: the edge is allowed when a target CALM node has one of these tags.
	TargetTags []string `json:"targetTags"`
}

// PredicateFamily is the family of a rule predicate.
type PredicateFamily string

// Predicate families.
const (
	FamilyBoundary         PredicateFamily = "boundary"
	FamilyLayer            PredicateFamily = "layer"
	FamilyDeclaredOnly     PredicateFamily = "declared-only"
	FamilyObservedRequired PredicateFamily = "observed-required"
	FamilyAcyclic          PredicateFamily = "acyclic"
	FamilyDenyPath         PredicateFamily = "deny-path"
	FamilyAllowOnly        PredicateFamily = "allow-only"
	FamilyIndependent      PredicateFamily = "independent"
	FamilyPublicAPIOnly    PredicateFamily = "public-api-only"
	FamilyOwnership        PredicateFamily = "ownership"
	FamilyBudget           PredicateFamily = "budget"
	FamilyRequireEdge      PredicateFamily = "require-edge"
	FamilyRequireEntity    PredicateFamily = "require-entity"
	// FamilyTyped is a rule with an `obligation` block. The predicate evaluators
	// never evaluate it: the obligations of the conformance package do.
	FamilyTyped PredicateFamily = "typed"
	// FamilyView is a rule of the `views` pack. `check` fills its results from the declared views
	// (VIEW-PRES-001, VIEW-FRESH-001, VIEW-CORR-001); the policy compiler refuses a rule of this family.
	FamilyView PredicateFamily = "view-currency"
	// FamilyDecisionGovernance is a rule of the `adr-governance` pack (a rule with a `decisions` key). `check`
	// fills its results from the decision register; the predicate evaluators never evaluate it.
	FamilyDecisionGovernance PredicateFamily = "decision-governance"
	// FamilyAssessmentCurrency is a rule of the `review-currency` pack (a rule with an `assessmentCurrency` key).
	// `check` fills its results from the assessments of the workspace; the predicate evaluators never evaluate it.
	// Its findings are not edges, so `check --changed` never reduces them.
	FamilyAssessmentCurrency PredicateFamily = "assessment-currency"
	// FamilyEffectiveAccess is a rule with `require.effectiveAccess`: the effective network or authorization result
	// between two sets of workloads must equal the expectation. Its results come from the assurance evaluators, not
	// from the predicate evaluators. It is advisory and has no edge findings, so `check --changed` never reduces it.
	FamilyEffectiveAccess PredicateFamily = "effective-access"
	// FamilyCheckResult is a rule with `require.checkResult`: a named check must have an admissible result. Its results
	// come from the admitted check results, not from the predicate evaluators. It is advisory and has no edge
	// findings, so `check --changed` never reduces it.
	FamilyCheckResult PredicateFamily = "check-result"
	// FamilyAssuranceClaim is a rule with `require.claim`: a registered assurance claim must hold for every subject of
	// the claim (an environment, a container occurrence). The claim registry decides at evaluation time whether the
	// claim exists, so a claim that no contract enables is `unknown`, not a compile error. It is advisory and has no
	// edge findings, so `check --changed` never reduces it.
	FamilyAssuranceClaim PredicateFamily = "assurance-claim"
)

// MaturityAdvisory is the maturity of the families that the assurance adapters introduced: their verdicts are reported
// and can fail a run through the rule severity, but the family contract is not yet stable.
const MaturityAdvisory = "advisory"

// Dimensions of `require.effectiveAccess` and the outcomes that each accepts.
const (
	DimensionNetwork       = "network"
	DimensionAuthorization = "authorization"
)

// Expected outcomes of `require.effectiveAccess`.
const (
	ExpectDenied     = "denied"
	ExpectAllowed    = "allowed"
	ExpectNotGranted = "not-granted"
	ExpectGranted    = "granted"
)

// EdgeFindings says whether the findings of the family are edges (or paths of edges).
// Only the findings of these families are reduced to the changed files.
func (f PredicateFamily) EdgeFindings() bool {
	switch f {
	case FamilyBoundary, FamilyLayer, FamilyDeclaredOnly, FamilyDenyPath, FamilyAllowOnly,
		FamilyIndependent, FamilyPublicAPIOnly, FamilyOwnership:
		return true
	case FamilyObservedRequired, FamilyAcyclic, FamilyBudget, FamilyRequireEdge, FamilyRequireEntity, FamilyTyped, FamilyView,
		FamilyDecisionGovernance, FamilyAssessmentCurrency, FamilyEffectiveAccess, FamilyCheckResult, FamilyAssuranceClaim:
		return false
	}

	return false
}

// CollapseByArchitectureBinding is the only collapse mode of an acyclic predicate.
const CollapseByArchitectureBinding = "architectureBinding"

// Aggregation modes of a rule. They say which findings of edge families are one finding.
const (
	AggregationEdge             = "edge"
	AggregationFile             = "file"
	AggregationSymbol           = "symbol"
	AggregationArchitecturePair = "architecture-pair"
)

// Budget measures.
const (
	BudgetForbiddenEdges = "forbidden-edges"
	BudgetCycles         = "cycles"
)

// Budget is the `budget` clause of a rule: the most findings that the measure may have.
type Budget struct {
	Measure string `json:"measure"`
	Max     int    `json:"max"`
}

// Predicate is the compiled predicate of a rule.
type Predicate struct {
	// Allow is set for the boundary family.
	Allow *BoundaryAllow `json:"allow,omitempty"`
	// Budget is set for the budget family.
	Budget *Budget         `json:"budget,omitempty"`
	Family PredicateFamily `json:"family"`
	// CollapseBy is set for the acyclic family and for a budget with the measure cycles.
	CollapseBy string `json:"collapseBy,omitempty"`
	// Decisions is the obligation name of a decision-governance rule: presence, applicability, justification,
	// enforcement, history or scope.
	Decisions string `json:"decisions,omitempty"`
	// AssessmentCurrency is the check of an assessment-currency rule: approval, disposition, capability or governance.
	AssessmentCurrency string `json:"assessmentCurrency,omitempty"`
	// EffectiveAccess is set for the effective-access family.
	EffectiveAccess *EffectiveAccess `json:"effectiveAccess,omitempty"`
	// CheckResult is set for the check-result family.
	CheckResult *CheckResultRequirement `json:"checkResult,omitempty"`
	// AssuranceClaim is set for the assurance-claim family.
	AssuranceClaim *ClaimRequirement `json:"assuranceClaim,omitempty"`
	// Maturity is "advisory" for the effective-access, check-result, and assurance-claim families, empty for the others.
	Maturity string `json:"maturity,omitempty"`
	// NamePattern is the optional RE2 pattern of the qualified name of a require-entity rule.
	NamePattern string `json:"namePattern,omitempty"`
	// APISourceRoots are the globs of the public API files of a public-api-only rule.
	APISourceRoots []string `json:"apiSourceRoots,omitempty"`
	// EntityLabels are the node labels of a require-entity rule.
	EntityLabels []string `json:"entityLabels,omitempty"`
}

// EffectiveAccess is the `require.effectiveAccess` clause of a rule. The rule Subjects are the resolved `from` CALM IDs
// and the Targets the resolved `to` CALM IDs.
type EffectiveAccess struct {
	// Dimension is DimensionNetwork or DimensionAuthorization.
	Dimension string `json:"dimension"`
	// Expect is ExpectDenied or ExpectAllowed for the network dimension, ExpectNotGranted or ExpectGranted for authorization.
	Expect string `json:"expect"`
	// Ports are the ports of the pair that the rule is about, sorted. Empty: every port.
	Ports []int `json:"ports,omitempty"`
}

// CheckResultRequirement is the `require.checkResult` clause of a rule. The rule Subjects are the resolved `subject`
// CALM IDs (none when the rule names no subject).
type CheckResultRequirement struct {
	// Check is the check identifier.
	Check string `json:"check"`
	// Environment is the environment of the result. Empty: the environment of the run.
	Environment string `json:"environment,omitempty"`
}

// ClaimRequirement is the `require.claim` clause of a rule. The rule has no Subjects: the claim names its own subjects
// (the environments or container occurrences that its evidence covers).
type ClaimRequirement struct {
	// ID is the claim identifier (kebab-case). It is looked up in the claim registry at evaluation time.
	ID string `json:"id"`
	// Environment is the environment of the claim. Empty: every environment that the run evaluates.
	Environment string `json:"environment,omitempty"`
}

// Traversal says which edges a rule follows. Direction is "outbound", or "inbound" for an
// ownership rule.
type Traversal struct {
	Direction    string                          `json:"direction"`
	EdgeKinds    []observation.CanonicalEdgeKind `json:"edgeKinds"`
	MinimumDepth int                             `json:"minimumDepth"`
	MaximumDepth int                             `json:"maximumDepth"`
}

// PackRef names the policy pack of a rule.
type PackRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// RulePlan is one compiled rule.
//
// Subjects are the resolved subject CALM IDs, sorted (for `layer` rules: the union of
// all layers). Layers are the CALM IDs of each layer of a `layer` rule, in order, each
// sorted. Targets are the resolved target CALM IDs, sorted. What they mean depends on
// the family:
//
//   - boundary: the forbidden targets. layer: same as subjects.
//   - deny-path: the targets that no path from a subject may reach.
//   - allow-only: the allowed targets.
//   - public-api-only: the targets whose API is public (explicit selector, else all CALM nodes).
//   - ownership: the owners (the nodes that may access the subjects).
//   - budget with forbidden-edges: the targets of the forbidden edges. Other budgets: none.
//   - require-edge: the targets that each subject must reach.
//   - declared-only, observed-required, acyclic, independent, require-entity: none.
//
// UnsupportedEdgeKinds are the kinds of Traversal.EdgeKinds that the provider does not
// support. Aggregation is one of edge, file, symbol and architecture-pair.
type RulePlan struct {
	Predicate            Predicate                       `json:"predicate"`
	Pack                 PackRef                         `json:"pack"`
	Remediation          string                          `json:"remediation,omitempty"`
	Rationale            string                          `json:"rationale,omitempty"`
	Owner                string                          `json:"owner,omitempty"`
	RuleID               string                          `json:"ruleId"`
	Severity             RuleSeverity                    `json:"severity"`
	Name                 string                          `json:"name"`
	Aggregation          string                          `json:"aggregation"`
	EvidenceRequirements EvidenceRequirements            `json:"evidenceRequirements"`
	Subjects             []string                        `json:"subjects"`
	Targets              []string                        `json:"targets"`
	Layers               [][]string                      `json:"layers,omitempty"`
	UnsupportedEdgeKinds []observation.CanonicalEdgeKind `json:"unsupportedEdgeKinds"`
	// Family is the rule family that enforcement is set for (`rule.family`). "" when not declared.
	Family string `json:"family,omitempty"`
	// Enforcement is the mode that the rule declares. "" when not declared:
	// the effective mode comes from the enforcement resolution.
	Enforcement Enforcement `json:"enforcement,omitempty"`
	// Obligation is the obligation contract of a typed rule (Predicate.Family is
	// FamilyTyped). nil for the other families. For a typed rule, Subjects are the
	// CALM IDs of the rule `subject` and Targets those of `obligation.relation.target`.
	Obligation *ObligationSpec `json:"obligation,omitempty"`
	Traversal  Traversal       `json:"traversal"`
}

// PolicyFile is the text of one policy file.
type PolicyFile struct {
	// Overrides are the rule overrides of the workspace for this file, keyed by
	// rule ID. nil sets nothing. Only a pack of `policies.packs` has overrides.
	Overrides map[string]workspace.RuleOverride
	// Path is shown in diagnostics and in PackRefs: "/" path relative to the workspace root.
	// A built-in pack has the path `builtin:<name>`.
	Path string
	Text string
	// Use is the `use` value of `policies.packs` that gave this file. "" for a file of `policies.files`.
	Use string
}

// PolicyPackRef describes one compiled policy file.
type PolicyPackRef struct {
	// Precedence is the versioned `precedence` list of the pack. nil when the pack declares none.
	Precedence *Precedence `json:"precedence,omitempty"`
	ID         string      `json:"id"`
	Version    string      `json:"version"`
	Path       string      `json:"path"`
	// Use is the `use` value when the file came from `policies.packs`.
	Use string `json:"use,omitempty"`
	// Digest is `sha256:<hex>` of the file bytes.
	Digest string `json:"digest"`
}

// PartialEvidence is a set of overrides. An empty or nil field is not set.
type PartialEvidence struct {
	MinimumConfidence observation.Confidence
	MinimumCoverage   *float64
	OnUnknown         UnknownDisposition
}

// CompileOverrides change the compile result.
type CompileOverrides struct {
	// CLI holds the flags. Highest precedence.
	CLI PartialEvidence
	// Workspace holds the workspace `check` values (`check.unknown` is OnUnknown).
	Workspace PartialEvidence
	// RuleIDs (`--rule <id>`) keep only these plans. nil keeps all. An unknown ID gives CO-WRK-010.
	RuleIDs []string
	// KnownCheckIDs are the check identifiers that the result sources define, for example the `check` of the
	// check-result statements that `assurance.verification.results` matches. The compiler adds the `check-ids` of the
	// controls of the architecture; a `require.checkResult` that names any other check gives CO-POL-005.
	KnownCheckIDs []string
}

// ProviderCaps are the capabilities of the observation provider that matter to policies.
type ProviderCaps struct {
	// Profile identifies the capability profile of the provider build (nil: none applies).
	Profile *observation.ProfileRef
	// Families is the support of each profile family, after the profile was
	// compared with the graph schema (CO-OBS-017).
	Families                map[string]observation.FamilyState
	SupportedCanonicalKinds []observation.CanonicalEdgeKind
}
