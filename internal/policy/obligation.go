package policy

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/observation"
)

// ObligationType is the predicate type of a typed rule.
type ObligationType string

// Obligation types.
const (
	ObligationRequired  ObligationType = "required"
	ObligationForbidden ObligationType = "forbidden"
	ObligationAllowed   ObligationType = "allowed"
)

// Multiplicity says whether one subject satisfies the rule for the others.
type Multiplicity string

// Multiplicities. A forAll rule is evaluated for each subject; one satisfying
// subject never satisfies the others. An exists rule passes if at least one
// admissible subject satisfies the predicate.
const (
	MultiplicityExists Multiplicity = "exists"
	MultiplicityForAll Multiplicity = "forAll"
)

// RelationDirection is the direction of the relation of an obligation, from
// the obligation subject. `from-*` means the evidence edge leaves the entity, `to-*`
// that it enters the entity. `*-subject` is the observed entity that the
// subject is bound to; `*-workload` is the workload that realizes the subject;
// `from-image` is the image that an `image` binding maps to the subject.
type RelationDirection string

// Relation directions.
const (
	DirectionFromSubject  RelationDirection = "from-subject"
	DirectionToSubject    RelationDirection = "to-subject"
	DirectionFromWorkload RelationDirection = "from-workload"
	DirectionToWorkload   RelationDirection = "to-workload"
	// DirectionFromImage anchors on the Image entities that an active `image` binding maps to the subject.
	// The evidence fact leaves the entity. An image has no environment, so the environment of the obligation does not exclude it.
	DirectionFromImage RelationDirection = "from-image"
)

// Enforcement is the enforcement mode of a rule.
type Enforcement string

// Enforcement modes. The empty value means that the rule does not declare one:
// the effective mode comes from the enforcement resolution (`check.enforcement`,
// then this declaration, then the default).
const (
	EnforcementMandatory Enforcement = "mandatory"
	EnforcementAdvisory  Enforcement = "advisory"
	EnforcementShadow    Enforcement = "shadow"
)

// Completeness is the coverage completeness that a typed rule needs before an
// absence or a pass counts.
type Completeness string

// Completeness values, from the weakest need to the strongest.
const (
	// CompletenessAny needs no coverage statement.
	CompletenessAny Completeness = "any"
	// CompletenessPartial accepts partial coverage.
	CompletenessPartial Completeness = "partial"
	// CompletenessComplete needs complete coverage of the predicate.
	CompletenessComplete Completeness = "complete"
)

// Rank gives 0 for any, 1 for partial and 2 for complete. An unknown value has rank 0.
func (c Completeness) Rank() int {
	switch c {
	case CompletenessPartial:
		return 1
	case CompletenessComplete:
		return 2
	case CompletenessAny:
		return 0
	}

	return 0
}

// Defaults of the obligation block and of the typed `evidence` keys.
const (
	// DefaultMultiplicity applies when `obligation.multiplicity` is not set.
	DefaultMultiplicity = MultiplicityForAll
	// DefaultMinimumResolution applies when `evidence.minimumResolution` is not
	// set. An `inferred`, `ambiguous` or `unresolved` fact never gives a pass by default.
	DefaultMinimumResolution = observation.ResolutionExplicitlyBound
	// DefaultCompleteness applies when `evidence.completeness` is not set.
	DefaultCompleteness = CompletenessComplete
)

// DefaultEvidenceRoles are the roles that apply when `evidence.roles` is not
// set: what the sources state and what a running system showed. Declared
// intent, inferred associations, measurements and reviews are not evidence of
// a deployed fact by default.
func DefaultEvidenceRoles() []observation.EvidenceRole {
	return []observation.EvidenceRole{observation.RoleParsedSourceFact, observation.RoleRuntimeObservation}
}

// TargetMode says what the relation target stands for. The zero value is an explicit target: Target is the
// CALM IDs that the rule names. The other two values come from the selector key `deployedIn`, and Target is
// then the set that BuildObligations fills for each obligation from the model.
type TargetMode string

// Target modes.
const (
	// TargetExplicit: the target is the list that the rule names (the zero value).
	TargetExplicit TargetMode = ""
	// TargetDeclaredContainers is `deployedIn: true`: the containers of the `deployed-in` relationships that list the subject.
	TargetDeclaredContainers TargetMode = "declared-containers"
	// TargetOtherHostingTargets is `deployedIn: false`: every hosting target that is bound to a CALM element
	// other than the containers of the `deployed-in` relationships that list the subject.
	TargetOtherHostingTargets TargetMode = "other-hosting-targets"
)

// RelationSpec is the relation that an obligation requires, forbids or allows.
// Kind is the evidence kind. Target is the set of CALM IDs that the relation
// ends at (sorted, never nil); it is empty when the rule names no target, and
// in a description of a rule that has no architecture model. With a TargetMode
// other than TargetExplicit the rule names no ID: the compiled spec has an empty
// Target, and each obligation has the containers of its subject (empty when the
// model declares no placement for the subject, see Obligation).
type RelationSpec struct {
	Kind       observation.CanonicalEdgeKind `json:"kind"`
	Direction  RelationDirection             `json:"direction"`
	TargetMode TargetMode                    `json:"targetMode,omitempty"`
	Target     []string                      `json:"target"`
}

// ObligationScope limits where an obligation applies. An empty list sets no limit.
type ObligationScope struct {
	Environments []string `json:"environments"`
	Repositories []string `json:"repositories"`
}

// ObligationSpec is the obligation contract of a typed rule (PRD POL-02):
// applicability, predicate type, relation and direction, scope, multiplicity,
// permitted evidence roles, minimum resolution and necessary completeness.
// Severity is the rule severity; the enforcement is RulePlan.Enforcement. The
// minimum confidence and `onUnknown` stay in RulePlan.EvidenceRequirements.
//
// The evaluation of a typed rule belongs to the obligations of the
// conformance package: the predicate evaluators of the other families never
// see a typed rule.
type ObligationSpec struct {
	// Applies is the selector that says which facts and entities the rule
	// applies to. nil applies to all. See SelectorExpr.Eval for unknown.
	Applies           *SelectorExpr          `json:"applies,omitempty"`
	Type              ObligationType         `json:"type"`
	Multiplicity      Multiplicity           `json:"multiplicity"`
	MinimumResolution observation.Resolution `json:"minimumResolution"`
	Completeness      Completeness           `json:"completeness"`
	// PassCompleteness is the completeness that a pass needs, while Completeness stays the one that an absence
	// needs. Empty when the rule does not set it: use PassNeed.
	PassCompleteness Completeness `json:"passCompleteness,omitempty"`
	// Roles are the evidence roles that the rule accepts. Sorted, never empty.
	Roles    []observation.EvidenceRole `json:"roles"`
	Relation RelationSpec               `json:"relation"`
	Scope    ObligationScope            `json:"scope"`
}

// PassNeed gives the completeness that a pass needs: PassCompleteness, or Completeness when the rule sets none.
func (o *ObligationSpec) PassNeed() Completeness {
	return cmp.Or(o.PassCompleteness, o.Completeness)
}

// Declaration gives the one-line description of the contract that `policy
// verify` shows, for example:
//
//	required forAll from-workload uses-image scope=production roles=parsed-source-fact minimumResolution=exact completeness=complete
//
// A target that the rule takes from the model adds `deployedIn=true` or `deployedIn=false` after the kind, and a rule that
// sets `passCompleteness` adds it after `completeness`.
func (o *ObligationSpec) Declaration() string {
	roles := make([]string, len(o.Roles))
	for i, r := range o.Roles {
		roles[i] = string(r)
	}

	parts := []string{string(o.Type), string(o.Multiplicity), string(o.Relation.Direction), string(o.Relation.Kind)}

	switch o.Relation.TargetMode {
	case TargetDeclaredContainers:
		parts = append(parts, "deployedIn=true")
	case TargetOtherHostingTargets:
		parts = append(parts, "deployedIn=false")
	case TargetExplicit:
	}

	parts = append(parts,
		"scope="+o.Scope.String(),
		"roles="+strings.Join(roles, ","),
		"minimumResolution="+string(o.MinimumResolution),
		"completeness="+string(o.Completeness),
	)

	if o.PassCompleteness != "" {
		parts = append(parts, "passCompleteness="+string(o.PassCompleteness))
	}

	if o.Applies != nil {
		parts = append(parts, "applies="+o.Applies.String())
	}

	return strings.Join(parts, " ")
}

// String shows the scope: the environments, then `repositories=...`; `any` when unlimited.
func (s ObligationScope) String() string {
	var parts []string

	if len(s.Environments) > 0 {
		parts = append(parts, strings.Join(s.Environments, ","))
	}

	if len(s.Repositories) > 0 {
		parts = append(parts, "repositories="+strings.Join(s.Repositories, ","))
	}

	if len(parts) == 0 {
		return "any"
	}

	return strings.Join(parts, ";")
}

// Precedence is the versioned precedence list of a policy pack. A rule listed
// earlier has priority over a rule listed later. Conflict detection uses it;
// the compiler only reads and keeps it.
type Precedence struct {
	Order   []string `json:"order"`
	Version int      `json:"version"`
}

// Rank gives the position of the rule in Order, or -1 when it is not listed.
func (p *Precedence) Rank(ruleID string) int {
	if p == nil {
		return -1
	}

	return slices.Index(p.Order, ruleID)
}
