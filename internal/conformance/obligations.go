package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// Placement is the CALM intent behind a placement obligation: the model declares the subject deployed in
// a container that the rule names as its target. The direction is the one of the CALM `deployed-in`
// relationship: each listed node (the `targets` of the RelationshipDeclaration) is deployed in the container.
// The subject is the placed node and the rule target is the container; a relation that places the container
// in the subject never satisfies the obligation and is evaluated as an unexpected placement.
type Placement struct {
	// RelationshipID is the CALM `deployed-in` relationship.
	RelationshipID string `json:"relationshipId"`
	// Container is the CALM ID of the container.
	Container string `json:"container"`
}

// Obligation is one obligation of a typed rule: what a rule needs, forbids, or allows for one subject in one
// environment. It comes from CALM intent, the rule scope, and the workspace environments. It never depends on
// the observed graph, so an empty or partial graph does not remove it.
type Obligation struct {
	// Placement is set for a placement obligation (see Placement). nil for the others.
	Placement *Placement `json:"placement,omitempty"`
	// ID is the first 16 hexadecimal characters of the SHA-256 of the rule ID, the subject ID, the environment,
	// and the relation key (policy.RelationKey), separated by the byte 0x1f.
	ID     string `json:"id"`
	RuleID string `json:"ruleId"`
	// Subject is the CALM ID of the subject.
	Subject string `json:"subject"`
	// Environment is the declared environment of the obligation. Empty when the workspace declares none and the rule scope names none.
	Environment string                 `json:"environment,omitempty"`
	Type        policy.ObligationType  `json:"type"`
	Scope       policy.ObligationScope `json:"scope"`
	Relation    policy.RelationSpec    `json:"relation"`
	// Bindings are the IDs of the active bindings that name the subject, sorted. It lets a result say what it relied on.
	Bindings []string `json:"bindings"`
	// EnvironmentUndeclared is true when the rule scope names an environment that the workspace does not declare:
	// the scope cannot be resolved, so the obligation is `unknown` and never `not-applicable`.
	EnvironmentUndeclared bool `json:"environmentUndeclared,omitempty"`
}

// Ref gives the form of the obligation that policy conflict detection uses.
func (o *Obligation) Ref(plan *policy.RulePlan) policy.ObligationRef {
	ref := policy.ObligationRef{
		ObligationID: o.ID, RuleID: o.RuleID, Subject: o.Subject, Environment: o.Environment, Relation: o.Relation, Type: o.Type,
	}

	if plan != nil && plan.Obligation != nil {
		ref.Applies = plan.Obligation.Applies
	}

	return ref
}

const obligationIDLength = 16

// obligationID gives the stable identity of an obligation.
func obligationID(ruleID, subject, environment string, relation *policy.RelationSpec) string {
	sum := digest.SHA256HexString(strings.Join([]string{ruleID, subject, environment, policy.RelationKey(relation)}, "\x1f"))

	return sum[:obligationIDLength]
}

// BuildObligations makes the obligations of a typed rule from the declared intent (the subjects of the rule, the
// `deployed-in` relationships of the model), the rule scope, and the workspace environments. The observed graph
// is not an input. Each subject gets one obligation for each environment of the rule:
//
//   - the rule scope names no environment: every declared environment (none declared: one obligation with no environment);
//   - the rule scope names environments: each of them. A named environment that the workspace does not declare keeps
//     its obligation, marked EnvironmentUndeclared;
//   - selected (the run environments, `check --environment`): an environment that is not selected gets no obligation.
//     nil selects all.
//
// The result is sorted by subject, then environment; the same inputs give the same IDs and the same order.
// A plan that is not typed gives nil.
func BuildObligations(plan *policy.RulePlan, model *intent.ArchitectureModel, wspace *workspace.Workspace, bindings *binding.Loaded, selected *RunEnvironments) []Obligation {
	spec := plan.Obligation
	if spec == nil || plan.Predicate.Family != policy.FamilyTyped {
		return nil
	}

	envs := obligationEnvironments(spec, wspace, selected)
	out := make([]Obligation, 0, len(plan.Subjects)*len(envs))

	for _, subject := range plan.Subjects {
		out = append(out, obligationsOfSubject(plan, spec, subject, envs, model, bindings)...)
	}

	slices.SortStableFunc(out, func(a, b Obligation) int {
		return cmp.Or(canonjson.CompareUTF16(a.Subject, b.Subject), canonjson.CompareUTF16(a.Environment, b.Environment))
	})

	return out
}

// obligationsOfSubject gives the obligations of one subject, one for each environment.
func obligationsOfSubject(
	plan *policy.RulePlan, spec *policy.ObligationSpec, subject string, envs []obligationEnv,
	model *intent.ArchitectureModel, bindings *binding.Loaded,
) []Obligation {
	// A target that the rule takes from the model (`deployedIn`) is, for each subject, the containers of the
	// relationships that list it. The ID stays that of the rule relation, so that it does not depend on the model.
	relation := spec.Relation
	if relation.TargetMode != policy.TargetExplicit {
		relation.Target = containersOf(model, subject)
	}

	out := make([]Obligation, 0, len(envs))

	for _, env := range envs {
		out = append(out, Obligation{
			ID: obligationID(plan.RuleID, subject, env.id, &spec.Relation), RuleID: plan.RuleID, Subject: subject, Environment: env.id,
			Type: spec.Type, Scope: spec.Scope, Relation: relation,
			Placement: placementOf(model, subject, relation.Target), Bindings: bindingIDsOf(bindings, subject),
			EnvironmentUndeclared: env.undeclared,
		})
	}

	return out
}

type obligationEnv struct {
	id         string
	undeclared bool
}

// obligationEnvironments gives the environments of a rule, in declaration order for declared ones.
func obligationEnvironments(spec *policy.ObligationSpec, wspace *workspace.Workspace, selected *RunEnvironments) []obligationEnv {
	declared := declaredEnvironmentIDs(wspace)
	envs := scopedObligationEnvs(spec.Scope.Environments, declared)

	if selected == nil {
		return envs
	}

	return slices.DeleteFunc(envs, func(e obligationEnv) bool { return isExcludedEnvironment(e, selected) })
}

// declaredEnvironmentIDs lists the environments that the workspace declares, in declaration order.
func declaredEnvironmentIDs(wspace *workspace.Workspace) []string {
	if wspace == nil {
		return nil
	}

	declared := make([]string, 0, len(wspace.Environments))
	for i := range wspace.Environments {
		declared = append(declared, wspace.Environments[i].ID)
	}

	return declared
}

// scopedObligationEnvs gives the environments that the scope of a rule names, else the declared ones, else one with no
// environment.
func scopedObligationEnvs(scoped, declared []string) []obligationEnv {
	envs := make([]obligationEnv, 0, len(declared)+len(scoped)+1)

	switch {
	case len(scoped) == 0 && len(declared) == 0:
		envs = append(envs, obligationEnv{})
	case len(scoped) == 0:
		for _, id := range declared {
			envs = append(envs, obligationEnv{id: id})
		}
	default:
		for _, id := range scoped {
			envs = append(envs, obligationEnv{id: id, undeclared: !slices.Contains(declared, id)})
		}
	}

	return envs
}

// isExcludedEnvironment says whether `check --environment` leaves the environment out. A scope name that is not
// declared is kept: it cannot be excluded by an id that is not declared.
func isExcludedEnvironment(env obligationEnv, selected *RunEnvironments) bool {
	return !env.undeclared && env.id != "" && !slices.Contains(selected.Selected, env.id)
}

// containersOf gives the sorted CALM IDs of the containers of the `deployed-in` relationships that list the subject.
// Empty (never nil) when the model declares no placement for the subject.
func containersOf(model *intent.ArchitectureModel, subject string) []string {
	containers := []string{}

	if model == nil {
		return containers
	}

	for i := range model.Relationships {
		if rel := &model.Relationships[i]; deploysSubjectInContainer(rel, subject) {
			containers = append(containers, rel.Container)
		}
	}

	slices.SortFunc(containers, canonjson.CompareUTF16)

	return slices.Compact(containers)
}

// deploysSubjectInContainer says whether the relationship is a `deployed-in` one that names a container and lists the
// subject.
func deploysSubjectInContainer(rel *intent.RelationshipDeclaration, subject string) bool {
	if rel.Kind != intent.RelDeployedIn || rel.Container == "" {
		return false
	}

	return slices.Contains(rel.Targets, subject)
}

// placementOf gives the placement intent when the model declares the subject deployed in one of the targets.
func placementOf(model *intent.ArchitectureModel, subject string, targets []string) *Placement {
	if model == nil || len(targets) == 0 {
		return nil
	}

	for i := range model.Relationships {
		rel := &model.Relationships[i]

		if placesSubjectIn(rel, subject, targets) {
			return &Placement{RelationshipID: rel.ID, Container: rel.Container}
		}
	}

	return nil
}

// placesSubjectIn says whether the relationship deploys the subject in one of the targets.
func placesSubjectIn(rel *intent.RelationshipDeclaration, subject string, targets []string) bool {
	if rel.Kind != intent.RelDeployedIn || !slices.Contains(rel.Targets, subject) {
		return false
	}

	return slices.Contains(targets, rel.Container)
}

// bindingIDsOf gives the sorted IDs of the active bindings of a CALM node. nil bindings give an empty list.
func bindingIDsOf(loaded *binding.Loaded, subject string) []string {
	ids := []string{}

	if loaded == nil {
		return ids
	}

	for i := range loaded.Bindings {
		b := &loaded.Bindings[i]
		if b.ArchitectureRef == subject && b.Status != binding.StatusProposed {
			ids = append(ids, b.ID)
		}
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	return slices.Compact(ids)
}

// AbsenceInput is what the absence rule needs to decide about a missing necessary item.
type AbsenceInput struct {
	// Coverage are the predicate records that the claim depends on, in the scope of the claim
	// (see CoverageOf). No record means that nothing is known about the extraction.
	Coverage []observation.PredicateCoverage
	// Breaches are the limits that the run went above. The claim depends on them.
	Breaches []LimitBreach
	// Supported says that the provider supports the observation (its family is supported or partial in the profile).
	Supported bool
	// InScope says that the observation is in the scope of the claim.
	InScope bool
	// AnalysisOK says that the analysis of the scope succeeded (no failed scope).
	AnalysisOK bool
}

// AbsenceResult is the verdict of a missing necessary item.
type AbsenceResult struct {
	// Verdict is `violation` or `unknown`; never `pass`.
	Verdict RuleVerdict
	// Reason is the unknown reason. Empty for a violation.
	Reason UnknownReason
	// Detail says what blocks a definite absence. Empty for a violation.
	Detail string
	// Blocking are the coverage records that are not `complete`.
	Blocking []observation.PredicateCoverage
}

// EvaluateAbsence applies the absence rule: a missing necessary item is a
// `violation` only if the provider supports the observation, the observation is
// in scope, the analysis succeeded, and the coverage of every needed predicate
// in the scope is `complete`. Otherwise it is `unknown` with the reason that
// names what is missing. The order of the reasons is: limit exceeded, family
// unsupported, coverage incomplete.
func EvaluateAbsence(input *AbsenceInput) AbsenceResult {
	if len(input.Breaches) > 0 {
		b := input.Breaches[0]

		return absenceUnknown(ReasonLimitExceeded, fmt.Sprintf("%s is configured as %d, observed %d", b.Limit, b.Configured, b.Observed), nil)
	}

	if !input.Supported {
		return absenceUnknown(ReasonUnsupportedFamily, "the provider does not support the observation", nil)
	}

	blocking := incompleteCoverage(input.Coverage)

	if i := slices.IndexFunc(blocking, func(p observation.PredicateCoverage) bool { return p.Status == observation.CoverageUnsupported }); i >= 0 {
		return absenceUnknown(ReasonUnsupportedFamily, blockingDetail(blocking[i:i+1]), blocking)
	}

	if unknown, blocked := scopeAbsenceUnknown(input, blocking); blocked {
		return unknown
	}

	return AbsenceResult{Verdict: VerdictViolation}
}

func absenceUnknown(reason UnknownReason, detail string, blocking []observation.PredicateCoverage) AbsenceResult {
	return AbsenceResult{Verdict: VerdictUnknown, Reason: reason, Detail: detail, Blocking: blocking}
}

// incompleteCoverage gives the records that are not `complete`, in their order.
func incompleteCoverage(records []observation.PredicateCoverage) []observation.PredicateCoverage {
	var blocking []observation.PredicateCoverage

	for i := range records {
		if !records[i].Complete() {
			blocking = append(blocking, records[i])
		}
	}

	return blocking
}

// scopeAbsenceUnknown gives the unknown verdict that the scope, the analysis, or the coverage of an absence gives.
// The second result is false when nothing blocks a definite absence.
func scopeAbsenceUnknown(input *AbsenceInput, blocking []observation.PredicateCoverage) (AbsenceResult, bool) {
	switch {
	case !input.InScope:
		return absenceUnknown(ReasonCoverageIncomplete, "the observation is outside the scope of the claim", blocking), true
	case !input.AnalysisOK:
		return absenceUnknown(ReasonCoverageIncomplete, "the analysis of the scope did not succeed", blocking), true
	case len(input.Coverage) == 0:
		return absenceUnknown(ReasonCoverageIncomplete, "no coverage record for the scope", nil), true
	case len(blocking) > 0:
		return absenceUnknown(ReasonCoverageIncomplete, blockingDetail(blocking), blocking), true
	}

	return AbsenceResult{}, false
}

// blockingDetail describes the first record that is not complete: predicate, status, scope, and loss modes.
func blockingDetail(records []observation.PredicateCoverage) string {
	rec := &records[0]

	scope := rec.Scope.Project
	if scope == "" {
		scope = rec.Scope.Environment
	}

	detail := fmt.Sprintf("%s is %s in %s", rec.Predicate, rec.Status, scope)

	if len(rec.LossModes) > 0 {
		modes := make([]string, len(rec.LossModes))
		for i, m := range rec.LossModes {
			modes[i] = m.ID
		}

		detail += " (" + strings.Join(modes, ", ") + ")"
	} else if len(rec.Reasons) > 0 {
		detail += " (" + rec.Reasons[0] + ")"
	}

	return detail
}

// CoverageOf gives the records of the predicates (family names) in one scope, in the order of the predicates.
// A predicate without a record is left out, so that a missing record reads as "no coverage".
func CoverageOf(records []observation.PredicateCoverage, predicates []string, scope observation.CoverageScope) []observation.PredicateCoverage {
	var out []observation.PredicateCoverage

	for _, name := range predicates {
		if i := slices.IndexFunc(records, func(r observation.PredicateCoverage) bool { return r.Predicate == name && r.Scope == scope }); i >= 0 {
			out = append(out, records[i])
		}
	}

	return out
}
