package policy

import (
	"regexp"
	"strconv"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// Rule shapes: the predicate part of a compiled rule.

// shape is the predicate part of a compiled rule.
type shape struct {
	// obligation is the contract of a typed rule.
	obligation *ObligationSpec
	// direction is the traversal direction. "" means outbound.
	direction string
	subjects  []string
	targets   []string
	layers    [][]string
	edgeKinds []observation.CanonicalEdgeKind
	predicate Predicate
	// maxDepth is the maximum path length. 0 means 1.
	maxDepth int
}

// shaper compiles the predicate of one kind of rule.
type shaper func(rule *rawRule, base string) (shape, *diag.Diagnostic)

// familySpec says what subjectShape starts: the family, the edges with the pointer of their key, and the pointer
// of the target key for a family with a target.
type familySpec struct {
	family        PredicateFamily
	edgesPointer  string
	targetPointer string
	edges         []string
}

func (c *compiler) shapeOf(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	if build := c.shaperOf(rule); build != nil {
		return build(rule, base)
	}

	return c.simpleShape(rule, base)
}

// shaperOf picks the method that compiles the predicate of the rule. It gives nil for a rule that
// simpleShape compiles: `declaredOnly`, `observedRequired`, and `require` with only `edges`.
func (c *compiler) shaperOf(rule *rawRule) shaper {
	switch {
	case rule.ViewCurrency != nil:
		return c.viewCurrencyShape
	case rule.Decisions != nil:
		return c.decisionsShape
	case rule.AssessmentCurrency != nil:
		return c.assessmentCurrencyShape
	case rule.Obligation != nil:
		return c.obligationShape
	case rule.Forbid != nil:
		return c.forbidShape
	case rule.LayerOrder != nil:
		return c.layerShape
	case rule.DenyPath != nil, rule.AllowOnly != nil, rule.Independent != nil, rule.PublicAPIOnly != nil,
		rule.Ownership != nil, rule.Budget != nil:
		return c.pathAndPolicyShape
	}

	return c.requireShaper(rule)
}

// requireShaper picks the method for the `require` predicates.
func (c *compiler) requireShaper(rule *rawRule) shaper {
	req := rule.Require
	if req == nil {
		return c.noRequireShaper(rule)
	}

	switch {
	case req.EffectiveAccess != nil:
		return c.effectiveAccessShape
	case req.CheckResult != nil:
		return c.checkResultShape
	case req.Claim != nil:
		return c.claimShape
	case req.Edge != nil:
		return c.requireEdgeShape
	case req.Entity != nil:
		return c.requireEntityShape
	}

	return nil
}

// noRequireShaper picks the method for a rule without `require`. A rule with no predicate at all has none to compile.
func (c *compiler) noRequireShaper(rule *rawRule) shaper {
	if rule.DeclaredOnly == nil && rule.ObservedRequired == nil {
		return c.noPredicateShape
	}

	return nil
}

func (c *compiler) viewCurrencyShape(_ *rawRule, base string) (shape, *diag.Diagnostic) {
	problem := diag.New(diag.POL001, diag.Detail(base+" rule is a view-currency rule: `check` evaluates the rules of the views pack "+
		"from the declared views, so do not list builtin:views in policies.packs"), c.at(base))

	return shape{}, &problem
}

func (c *compiler) noPredicateShape(_ *rawRule, base string) (shape, *diag.Diagnostic) {
	problem := diag.New(diag.POL001, diag.Detail(base+" rule has no predicate"), c.at(base))

	return shape{}, &problem
}

// simpleShape compiles `declaredOnly`, `observedRequired` and `require.edges` (the acyclic family).
func (c *compiler) simpleShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	var (
		built   shape
		problem *diag.Diagnostic
	)

	if built.subjects, problem = c.selectorAt(rule.Subject, base+"/subject"); problem != nil {
		return built, problem
	}

	built.targets = []string{}

	switch {
	case rule.DeclaredOnly != nil:
		built.predicate = Predicate{Family: FamilyDeclaredOnly}
		built.edgeKinds, problem = c.resolveEdges(rule.DeclaredOnly.Edges, base+"/declaredOnly/edges")
	case rule.ObservedRequired != nil:
		built.predicate = Predicate{Family: FamilyObservedRequired}
		// No edges in the rule: the evaluator allows every supported evidence kind of the relationship.
		built.edgeKinds = c.observableKinds()
	default:
		built.predicate = Predicate{Family: FamilyAcyclic, CollapseBy: CollapseByArchitectureBinding}
		built.edgeKinds, problem = c.resolveEdges(rule.Require.Edges, base+"/require/edges")
	}

	return built, problem
}

// observableKinds gives the code edge kinds that the provider supports, sorted.
func (c *compiler) observableKinds() []observation.CanonicalEdgeKind {
	var kinds []observation.CanonicalEdgeKind

	for _, kind := range c.caps.SupportedCanonicalKinds {
		if c.isObservable(kind) {
			kinds = append(kinds, kind)
		}
	}

	return uniqSorted(kinds)
}

// isObservable says whether the kind is a code relationship that the provider supports. Containment and definition are not.
func (c *compiler) isObservable(kind observation.CanonicalEdgeKind) bool {
	if kind == observation.KindContains || kind == observation.KindDefines {
		return false
	}

	return observation.IsCodeKind(kind) && c.kindSupported(kind)
}

func (c *compiler) forbidShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	var result shape

	if t := rule.Forbid.Traversal; t != nil && *t != "direct" {
		problem := diag.New(diag.POL002,
			diag.Detail("traversal '"+*t+"' is not supported; use denyPath for transitive paths"), c.at(base+"/forbid/traversal"))

		return result, &problem
	}

	var problem *diag.Diagnostic

	if result.subjects, problem = c.selectorAt(rule.Subject, base+"/subject"); problem != nil {
		return result, problem
	}

	if result.targets, problem = c.selectorAt(rule.Forbid.Target, base+"/forbid/target"); problem != nil {
		return result, problem
	}

	clauses := allowClausesOf(rule)

	if unknown := c.knownIDs(clauses.calmNodes, base+"/allow/target/calmNodes"); unknown != nil {
		return result, unknown
	}

	allowKinds, problem := c.resolveEdges(clauses.whenEdges, base+"/allow/when/any")
	if problem != nil {
		return result, problem
	}

	result.predicate = Predicate{Family: FamilyBoundary, Allow: &BoundaryAllow{
		TargetSourceRoots: uniqSorted(clauses.sourceRoots),
		TargetCalmNodes:   uniqSorted(clauses.calmNodes),
		EdgeKinds:         allowKinds,
		TargetTags:        uniqSorted(clauses.tags),
	}}
	result.edgeKinds, problem = c.resolveEdges(rule.Forbid.Edges, base+"/forbid/edges")

	return result, problem
}

// allowClauses are the raw lists of the `allow` clause of a forbid rule.
type allowClauses struct {
	calmNodes, sourceRoots, tags, whenEdges []string
}

func allowClausesOf(rule *rawRule) allowClauses {
	var out allowClauses

	if rule.Allow == nil {
		return out
	}

	if rule.Allow.Target != nil {
		out.calmNodes, out.sourceRoots = rule.Allow.Target.CalmNodes, rule.Allow.Target.SourceRoots
	}

	if rule.Allow.When == nil {
		return out
	}

	for _, when := range rule.Allow.When.Any {
		if when.Edge != nil {
			out.whenEdges = append(out.whenEdges, *when.Edge)
		}

		if when.TargetTag != nil {
			out.tags = append(out.tags, *when.TargetTag)
		}
	}

	return out
}

func (c *compiler) layerShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	var built shape

	for k, layer := range rule.LayerOrder.Layers {
		ids, problem := c.selectorAt(layer, base+"/layerOrder/layers/"+strconv.Itoa(k))
		if problem != nil {
			return built, problem
		}

		built.layers = append(built.layers, ids)
		built.subjects = append(built.subjects, ids...)
	}

	built.subjects = uniqSorted(built.subjects)
	built.targets = built.subjects
	built.predicate = Predicate{Family: FamilyLayer}

	var problem *diag.Diagnostic

	built.edgeKinds, problem = c.resolveEdges(rule.LayerOrder.Edges, base+"/layerOrder/edges")

	return built, problem
}

// subjectShape starts the shape of a family: the subject, the edge kinds, and no target.
func (c *compiler) subjectShape(rule *rawRule, base string, spec familySpec) (shape, *diag.Diagnostic) {
	built := shape{predicate: Predicate{Family: spec.family}, targets: []string{}}

	var problem *diag.Diagnostic

	if built.subjects, problem = c.selectorAt(rule.Subject, base+"/subject"); problem != nil {
		return built, problem
	}

	if spec.edges != nil {
		built.edgeKinds, problem = c.resolveEdges(spec.edges, base+spec.edgesPointer)
	}

	return built, problem
}

// targetedShape is subjectShape for a family with a target: the target selector comes after the subject and the edges.
func (c *compiler) targetedShape(rule *rawRule, base string, spec familySpec, target rawSelector) (shape, *diag.Diagnostic) {
	built, problem := c.subjectShape(rule, base, spec)
	if problem != nil {
		return built, problem
	}

	built.targets, problem = c.selectorAt(target, base+spec.targetPointer)

	return built, problem
}

// pathAndPolicyShape compiles denyPath, allowOnly, independent, publicApiOnly, ownership and budget.
func (c *compiler) pathAndPolicyShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	switch {
	case rule.DenyPath != nil:
		return c.denyPathShape(rule, base)
	case rule.AllowOnly != nil:
		return c.allowOnlyShape(rule, base)
	case rule.Independent != nil:
		return c.subjectShape(rule, base,
			familySpec{family: FamilyIndependent, edgesPointer: "/independent/edges", edges: rule.Independent.Edges})
	case rule.PublicAPIOnly != nil:
		return c.publicAPIShape(rule, base)
	case rule.Ownership != nil:
		return c.ownershipShape(rule, base)
	}

	return c.budgetShape(rule, base)
}

func (c *compiler) denyPathShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	p := rule.DenyPath

	built, problem := c.targetedShape(rule, base, familySpec{FamilyDenyPath, "/denyPath/edges", "/denyPath/target", p.Edges}, p.Target)
	built.maxDepth = defaultMaxDepth

	if p.MaxDepth != nil {
		built.maxDepth = *p.MaxDepth
	}

	return built, problem
}

func (c *compiler) allowOnlyShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	p := rule.AllowOnly

	return c.targetedShape(rule, base, familySpec{FamilyAllowOnly, "/allowOnly/edges", "/allowOnly/targets", p.Edges}, p.Targets)
}

func (c *compiler) ownershipShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	p := rule.Ownership

	built, problem := c.targetedShape(rule, base, familySpec{FamilyOwnership, "/ownership/edges", "/ownership/owners", p.Edges}, p.Owners)
	built.direction = directionInbound

	return built, problem
}

func (c *compiler) publicAPIShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	spec := rule.PublicAPIOnly

	built, problem := c.subjectShape(rule, base,
		familySpec{family: FamilyPublicAPIOnly, edgesPointer: "/publicApiOnly/edges", edges: spec.Edges})
	if problem != nil {
		return built, problem
	}

	built.predicate.APISourceRoots = uniqSorted(spec.API.SourceRoots)

	if spec.Target != nil {
		built.targets, problem = c.selectorAt(spec.Target, base+"/publicApiOnly/target")

		return built, problem
	}

	built.targets = make([]string, 0, len(c.index.ids))
	for id := range c.index.ids {
		built.targets = append(built.targets, id)
	}

	built.targets = uniqSorted(built.targets)

	return built, nil
}

func (c *compiler) budgetShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	budget := rule.Budget

	built, problem := c.subjectShape(rule, base, familySpec{family: FamilyBudget, edgesPointer: "/budget/edges", edges: budget.Edges})
	if problem != nil {
		return built, problem
	}

	built.predicate.Budget = &Budget{Measure: budget.Measure, Max: budget.Max}

	switch budget.Measure {
	case BudgetForbiddenEdges:
		if budget.Target == nil {
			d := diag.New(diag.POL001, diag.Detail("budget.target is required for measure forbidden-edges"), c.at(base+"/budget"))

			return built, &d
		}

		built.targets, problem = c.selectorAt(budget.Target, base+"/budget/target")
	default:
		if budget.Target != nil {
			d := diag.New(diag.POL001, diag.Detail("budget.target is not allowed for measure cycles"), c.at(base+"/budget/target"))

			return built, &d
		}

		built.predicate.CollapseBy = CollapseByArchitectureBinding
	}

	return built, problem
}

// requireEdgeShape compiles `require.edge`.
func (c *compiler) requireEdgeShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	e := rule.Require.Edge

	return c.targetedShape(rule, base, familySpec{FamilyRequireEdge, "/require/edge/edges", "/require/edge/target", e.Edges}, e.Target)
}

// requireEntityShape compiles `require.entity`.
func (c *compiler) requireEntityShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	entity := rule.Require.Entity

	built, problem := c.subjectShape(rule, base, familySpec{family: FamilyRequireEntity})
	if problem != nil {
		return built, problem
	}

	if entity.NamePattern != "" {
		if _, err := regexp.Compile(entity.NamePattern); err != nil {
			d := diag.New(diag.POL001, diag.Detail("require.entity.namePattern: "+err.Error()), c.at(base+"/require/entity/namePattern"))

			return built, &d
		}
	}

	built.predicate.EntityLabels = uniqSorted(entity.Labels)
	built.predicate.NamePattern = entity.NamePattern

	return built, nil
}
