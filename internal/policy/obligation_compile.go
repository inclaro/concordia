package policy

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/diag"
)

// typedEvidenceKeys are the `evidence` keys that only a rule with an obligation block may set.
var typedEvidenceKeys = []string{"roles", "minimumResolution", "completeness", "passCompleteness"}

// deployedInKey is the selector key that stands for the declared deployment topology: the nodes that a
// `deployed-in` relationship lists (as the subject), or their containers and the other hosting targets (as the relation target).
const deployedInKey = "deployedIn"

// deployedInSelector says whether a selector of an obligation block is the `deployedIn` form, and its value. The key is
// valid only as the whole selector: next to another key it gives CO-POL-002. The schema has already checked that the value is a boolean.
func (c *compiler) deployedInSelector(sel rawSelector, pointer string) (value, found bool, problem *diag.Diagnostic) {
	raw, ok := sel[deployedInKey]
	if !ok {
		return false, false, nil
	}

	if len(sel) > 1 {
		return false, true, c.selectorProblem("selector key '"+deployedInKey+"' cannot be combined with another key; it is the whole selector", pointer)
	}

	value, _ = raw.(bool)

	return value, true, nil
}

// subjectSelector compiles the subject of an obligation block. `{deployedIn: true}` selects the nodes that a
// `deployed-in` relationship lists as deployed nodes; any other form is a selector of the legacy keys.
func (c *compiler) subjectSelector(sel rawSelector, pointer string) ([]string, *diag.Diagnostic) {
	value, found, problem := c.deployedInSelector(sel, pointer)

	switch {
	case problem != nil:
		return nil, problem
	case !found:
		return c.selectorAt(sel, pointer)
	case !value:
		return nil, c.selectorProblem("selector key '"+deployedInKey+"' is valid as the subject only with the value true", pointer)
	}

	return slices.Clone(c.index.deployed), nil
}

// relationTarget compiles the target of the relation of an obligation block: the CALM IDs of a selector, or, for
// `{deployedIn: true}` and `{deployedIn: false}`, no ID and the target mode that BuildObligations resolves for each subject.
func (c *compiler) relationTarget(sel rawSelector, pointer string) ([]string, TargetMode, *diag.Diagnostic) {
	value, found, problem := c.deployedInSelector(sel, pointer)

	switch {
	case problem != nil:
		return nil, TargetExplicit, problem
	case !found:
		ids, d := c.selectorAt(sel, pointer)

		return ids, TargetExplicit, d
	case value:
		return []string{}, TargetDeclaredContainers, nil
	}

	return []string{}, TargetOtherHostingTargets, nil
}

// obligationShape compiles the `obligation` block of a typed rule: the subject, the
// relation with its edge kind and target, and the contract (ObligationSpec).
// The relation kind is the only edge kind of the rule.
func (c *compiler) obligationShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	block := rule.Obligation
	built := shape{predicate: Predicate{Family: FamilyTyped}, targets: []string{}}

	var (
		problem *diag.Diagnostic
		mode    TargetMode
	)

	if built.subjects, problem = c.subjectSelector(rule.Subject, base+"/subject"); problem != nil {
		return built, problem
	}

	if built.edgeKinds, problem = c.resolveEdges([]string{block.Relation.Kind}, base+"/obligation/relation/kind"); problem != nil {
		return built, problem
	}

	// The relation kind is the only edge kind of the rule: a token that stands for several kinds is ambiguous.
	if len(built.edgeKinds) != 1 {
		return built, c.edgeKindProblem(block.Relation.Kind, base+"/obligation/relation/kind")
	}

	if block.Relation.Target != nil {
		if built.targets, mode, problem = c.relationTarget(block.Relation.Target, base+"/obligation/relation/target"); problem != nil {
			return built, problem
		}
	}

	spec := &ObligationSpec{
		Type: block.Type,
		Relation: RelationSpec{
			Kind: built.edgeKinds[0], Direction: block.Relation.Direction, Target: nonNil(built.targets), TargetMode: mode,
		},
		Scope:             ObligationScope{Environments: []string{}, Repositories: []string{}},
		Multiplicity:      cmp.Or(block.Multiplicity, DefaultMultiplicity),
		Roles:             DefaultEvidenceRoles(),
		MinimumResolution: DefaultMinimumResolution,
		Completeness:      DefaultCompleteness,
	}

	if block.Scope != nil {
		spec.Scope.Environments = uniqSorted(block.Scope.Environments)
		spec.Scope.Repositories = uniqSorted(block.Scope.Repositories)
	}

	if ev := rule.Evidence; ev != nil {
		if len(ev.Roles) > 0 {
			spec.Roles = uniqSorted(ev.Roles)
		}

		spec.MinimumResolution = cmp.Or(ev.MinimumResolution, spec.MinimumResolution)
		spec.Completeness = cmp.Or(ev.Completeness, spec.Completeness)
		spec.PassCompleteness = ev.PassCompleteness
	}

	if block.Applies != nil {
		if spec.Applies, problem = c.selectorExpr(block.Applies, base+"/obligation/applies", nil); problem != nil {
			return built, problem
		}
	}

	if block.Relation.Direction == DirectionToSubject || block.Relation.Direction == DirectionToWorkload {
		built.direction = directionInbound
	}

	built.obligation = spec

	return built, nil
}
