package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// factOutcome says what a fact is for an obligation.
type factOutcome int

const (
	// outSkip: the fact is not about this obligation (another target, another rule, another obligation of the rule).
	outSkip factOutcome = iota
	// outValid: the fact is admissible evidence of the relation.
	outValid
	// outProblem: the fact is of the relation but cannot decide (selector, binding, resolution, environment).
	outProblem
	// outMismatch: the fact has an evidence role that the rule does not permit.
	outMismatch
	// outSeparate: the fact is evaluated separately (another environment) and never counts here.
	outSeparate
	// outInapplicable: the applicability selector of the rule is false for the fact: the rule does not apply to it.
	outInapplicable
)

// scanned is the facts of one anchor, by outcome.
type scanned struct {
	valid    []*BoundEdge
	problems []OccurrenceResult
	mismatch []OccurrenceResult
	separate []OccurrenceResult
}

// record keeps one assessed fact under its outcome. A fact that is skipped or inapplicable is not kept.
func (s *scanned) record(bound *BoundEdge, occ *OccurrenceResult, outcome factOutcome) {
	switch outcome {
	case outValid:
		s.valid = append(s.valid, bound)
	case outProblem:
		s.problems = append(s.problems, *occ)
	case outMismatch:
		s.mismatch = append(s.mismatch, *occ)
	case outSeparate:
		s.separate = append(s.separate, *occ)
	case outSkip, outInapplicable:
	}
}

func (r *obligationRun) outbound() bool {
	d := r.ob.Relation.Direction

	return d == policy.DirectionFromSubject || d == policy.DirectionFromWorkload || d == policy.DirectionFromImage
}

func (r *obligationRun) workloadDirection() bool {
	d := r.ob.Relation.Direction

	return d == policy.DirectionFromWorkload || d == policy.DirectionToWorkload
}

// imageAnchor says whether the observed entity is an image that an `image` binding maps to the subject: an `Image` node
// or a synthesized image entity. A binding of another match kind that bound it does not make it one.
func (r *obligationRun) imageAnchor(node *observation.ObservedNode) bool {
	if node.Label != observation.LabelImage {
		return false
	}

	used := r.e.input.BindingSet.UsedBy[node.ObservedID]

	return len(used) == 0 || slices.ContainsFunc(used, func(id string) bool {
		b := r.e.bindingsByID[id]

		return b == nil || slices.ContainsFunc(b.Match, func(m binding.Match) bool { return m.Kind == binding.MatchImage })
	})
}

// anchorsOf gives the observed entities of the subject: those in the environment of the obligation (or with no
// environment), and those of other environments. A `*-workload` direction takes the workloads. The direction
// `from-image` takes the images that an `image` binding maps to the subject, in every environment: an image entity has no
// environment (a digest entity has the one of the fact that names it, which does not exclude it). The repository scope
// limits all of them.
func (r *obligationRun) anchorsOf() (inEnv, others []string) {
	image := r.ob.Relation.Direction == policy.DirectionFromImage

	for _, observedID := range r.e.anchors[r.ob.Subject] {
		node, ok := r.e.nodes[observedID]
		if !ok || !r.anchorInScope(&node, image) {
			continue
		}

		if image || r.inObligationEnvironment(&node) {
			inEnv = append(inEnv, observedID)
		} else {
			others = append(others, observedID)
		}
	}

	return inEnv, others
}

// anchorInScope says whether the entity is of the kind that the direction of the relation anchors on, and is in the
// repositories that the rule limits.
func (r *obligationRun) anchorInScope(node *observation.ObservedNode, image bool) bool {
	return r.anchorEligible(node, image) && !r.outsideRepositoryScope(node)
}

// anchorEligible says whether the entity is of the kind that the direction of the relation anchors on: a workload for
// a `*-workload` direction, an image that an `image` binding maps to the subject for `from-image`.
func (r *obligationRun) anchorEligible(node *observation.ObservedNode, image bool) bool {
	if r.workloadDirection() && !isWorkload(node) {
		return false
	}

	return !image || r.imageAnchor(node)
}

// outsideRepositoryScope says whether the rule limits the repositories and the entity is in none of them.
func (r *obligationRun) outsideRepositoryScope(node *observation.ObservedNode) bool {
	repos := r.spec.Scope.Repositories

	return len(repos) > 0 && !slices.Contains(repos, r.repositoryOf(node))
}

// inObligationEnvironment says whether the entity is in the environment of the obligation, has none, or has one that is
// not resolved.
func (r *obligationRun) inObligationEnvironment(node *observation.ObservedNode) bool {
	env := r.ob.Environment

	return env == "" || node.Environment == "" || node.Environment == env || node.Environment == observation.EnvironmentUnresolved
}

// covers says whether the rule has an obligation for the environment: the facts of that environment are
// evaluated by that obligation and are left out of this one.
func (r *obligationRun) covers(env string) bool {
	if scoped := r.ob.Scope.Environments; len(scoped) > 0 {
		return slices.Contains(scoped, env)
	}

	return slices.Contains(r.e.declared, env)
}

// facts gives the facts of the relation kind that start (or, when reversed, end) at the anchor, sorted.
func (r *obligationRun) facts(anchor string, reverse bool) []*BoundEdge {
	slot := factSlot{kind: r.ob.Relation.Kind, id: anchor}
	edges := r.e.to[slot]

	if r.outbound() != reverse {
		edges = r.e.from[slot]
	}

	return r.bind(edges)
}

// bind gives the facts with their endpoints and the bindings of the endpoints, sorted. A fact with an endpoint that the
// graph does not have, or in an excluded file, is left out.
func (r *obligationRun) bind(edges []*observation.EvidenceEdge) []*BoundEdge {
	out := make([]*BoundEdge, 0, len(edges))

	for _, edge := range edges {
		from, dest, ok := r.admittedEndpoints(edge)
		if !ok {
			continue
		}

		out = append(out, &BoundEdge{
			Edge: edge, From: from, To: dest,
			FromIDs: r.e.input.BindingSet.ByObserved[edge.From], ToIDs: r.e.input.BindingSet.ByObserved[edge.To],
		})
	}

	sortFacts(out)

	return out
}

// admittedEndpoints gives the entities at the ends of a fact. It says false when the graph lacks one of them or one is
// in an excluded file.
func (r *obligationRun) admittedEndpoints(edge *observation.EvidenceEdge) (from, dest observation.ObservedNode, ok bool) {
	from, okFrom := r.e.nodes[edge.From]
	dest, okTo := r.e.nodes[edge.To]

	if !okFrom || !okTo {
		return from, dest, false
	}

	return from, dest, !r.inExcludedFile(&from) && !r.inExcludedFile(&dest)
}

func (r *obligationRun) inExcludedFile(node *observation.ObservedNode) bool {
	return node.FilePath != "" && r.e.excluded(node.FilePath)
}

// otherEnd gives the endpoint of the fact that is not the anchor side: the target of the relation.
func (r *obligationRun) otherEnd(edge *observation.EvidenceEdge, reverse bool) string {
	if r.outbound() != reverse {
		return edge.To
	}

	return edge.From
}

// scan assesses the facts of one anchor.
func (r *obligationRun) scan(anchor string) scanned {
	if occ := r.anchorProblem(anchor); occ != nil {
		return scanned{problems: []OccurrenceResult{*occ}}
	}

	var seen scanned

	for _, bound := range r.facts(anchor, false) {
		occ, outcome := r.assess(anchor, bound)
		if outcome == outInapplicable {
			r.sawInapplicable = true
		}

		seen.record(bound, &occ, outcome)
	}

	return seen
}

// anchorProblem gives the result of an anchor whose bindings are ambiguous or whose declarations compete: no fact of
// it can decide. It gives nil for any other anchor.
func (r *obligationRun) anchorProblem(anchor string) *OccurrenceResult {
	if ids := r.e.input.BindingSet.Ambiguous[anchor]; len(ids) > 0 {
		occ := r.entity(OccurrenceBindingGap, VerdictUnknown, ReasonAmbiguousBinding,
			"non-exclusive bindings map the entity to different CALM elements ("+joinSorted(ids)+"); no binding wins by order of declaration")
		r.locate(&occ, anchor)

		return &occ
	}

	conflict := r.e.conflicted[anchor]
	if conflict == nil {
		return nil
	}

	sources := make([]Location, len(conflict.Locations))
	for i, loc := range conflict.Locations {
		sources[i] = Location{Path: loc.Path, Line: loc.Line}
	}

	slices.SortFunc(sources, compareLocations)

	occ := r.entity(OccurrenceConflict, VerdictUnknown, ReasonEvidenceConflict,
		fmt.Sprintf("competing declarations of %s in %s: they are not two instances and no declaration wins", conflict.ID.String(), locationList(sources)))
	occ.Sources = sources
	r.locate(&occ, anchor)

	return &occ
}

// locate sets the anchor and its source location on an occurrence that has no fact.
func (r *obligationRun) locate(occ *OccurrenceResult, anchor string) {
	occ.Anchor = anchor
	occ.Bindings = r.e.bindingRefs(anchor)

	if node, ok := r.e.nodes[anchor]; ok && node.FilePath != "" {
		occ.Location = &Location{Path: node.FilePath, Line: node.StartLine}
	}
}

// assess decides what one fact is for the obligation. The checks run in this order: environment, target
// binding, applicability selector, evidence role, minimum resolution.
func (r *obligationRun) assess(anchor string, bound *BoundEdge) (OccurrenceResult, factOutcome) {
	if occ, outcome, done := r.assessEnvironment(anchor, bound); done {
		return occ, outcome
	}

	if occ, outcome, done := r.assessTarget(anchor, bound, r.otherEnd(bound.Edge, false)); done {
		return occ, outcome
	}

	return r.eligible(anchor, bound)
}

// eligible runs the last three checks of assess on a fact: the applicability selector, the evidence role, and the
// minimum resolution. It gives outValid when the fact passes all three.
func (r *obligationRun) eligible(anchor string, bound *BoundEdge) (OccurrenceResult, factOutcome) {
	if ev := r.spec.Applies.Eval(r.contextFor(anchor, bound)); ev.Value != policy.TriTrue {
		if ev.Value == policy.TriFalse {
			return OccurrenceResult{}, outInapplicable
		}

		return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictUnknown, ReasonSelectorPropertyUnknown,
			"selector property unknown: "+propertyList(ev.Unknown)), outProblem
	}

	if !slices.Contains(r.spec.Roles, bound.Edge.Role) {
		return r.occurrenceOf(anchor, bound, OccurrenceRoleMismatch, VerdictUnknown, ReasonCoverageIncomplete, r.roleDetail(bound.Edge.Role)), outMismatch
	}

	if reason, detail, low := r.resolutionProblem(bound.Edge); low {
		return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictUnknown, reason, detail), outProblem
	}

	return OccurrenceResult{}, outValid
}

func propertyList(props []policy.Property) string {
	names := make([]string, len(props))
	for i := range props {
		names[i] = props[i].String()
	}

	return strings.Join(names, ", ")
}

// roleDetail names the missing role. A declared fact is not a runtime fact: a difference between them is drift, never a pass.
func (r *obligationRun) roleDetail(have observation.EvidenceRole) string {
	roles := make([]string, len(r.spec.Roles))
	for i, role := range r.spec.Roles {
		roles[i] = string(role)
	}

	detail := fmt.Sprintf("missing evidence role: the rule permits %s, the fact has the role %s", strings.Join(roles, ", "), roleName(have))

	if have == observation.RoleParsedSourceFact && slices.Contains(r.spec.Roles, observation.RoleRuntimeObservation) {
		detail += "; a declared fact is not a runtime fact, so a difference between them is drift and never a pass"
	}

	return detail
}

func roleName(role observation.EvidenceRole) string {
	if role == "" {
		return "none"
	}

	return string(role)
}

// resolutionProblem says whether the fact is resolved below the minimum resolution of the rule. A fact with no
// resolution (a provider that does not state it) is not tested.
func (r *obligationRun) resolutionProblem(edge *observation.EvidenceEdge) (reason UnknownReason, detail string, low bool) {
	if edge.Resolution == "" || edge.Resolution.Rank() >= r.spec.MinimumResolution.Rank() {
		return "", "", false
	}

	detail = fmt.Sprintf("the fact has the resolution %s; the rule needs at least %s", edge.Resolution, r.spec.MinimumResolution)

	switch edge.Resolution {
	case observation.ResolutionAmbiguous:
		return ReasonAmbiguousBinding, detail, true
	case observation.ResolutionUnresolved, observation.ResolutionUnbound:
		return ReasonBindingGap, detail, true
	case observation.ResolutionExact, observation.ResolutionExplicitlyBound, observation.ResolutionInferred:
	}

	return ReasonCoverageIncomplete, detail, true
}

// assessEnvironment applies the environment of the obligation to a fact. A fact with no environment that an
// environment owns (an infrastructure or runtime fact) is unknown; a fact of an environment that another
// obligation of the rule covers is left to that obligation; a fact of an uncovered environment is evaluated separately.
// A build lineage fact (observation.IsLineageKind) is evaluated in every environment, whatever environment it has.
func (r *obligationRun) assessEnvironment(anchor string, bound *BoundEdge) (OccurrenceResult, factOutcome, bool) {
	env := r.ob.Environment
	// A lineage fact connects an image and a build definition, which no environment owns: it is evidence in every environment.
	if env == "" || observation.IsLineageKind(bound.Edge.Kind) {
		return OccurrenceResult{}, outValid, false
	}

	have := r.factEnvironment(anchor, bound)

	switch {
	case have == env:
		return OccurrenceResult{}, outValid, false
	case have == "" || have == observation.EnvironmentUnresolved:
		return r.unresolvedEnvironment(anchor, bound)
	case r.covers(have):
		return OccurrenceResult{}, outSkip, true
	}

	occ := r.occurrenceOf(anchor, bound, OccurrenceWrongEnvironment, VerdictNotApplicable, "",
		fmt.Sprintf("the fact is in the environment %s, which the rule does not cover; the obligation is for %s and the fact never satisfies it", have, env))
	occ.Decisive = false

	return occ, outSeparate, true
}

// unresolvedEnvironment gives the unknown result of an infrastructure or runtime fact whose environment is not
// resolved. Any other fact of no environment is kept.
func (r *obligationRun) unresolvedEnvironment(anchor string, bound *BoundEdge) (OccurrenceResult, factOutcome, bool) {
	if !observation.IsInfrastructureKind(bound.Edge.Kind) && bound.Edge.Role != observation.RoleRuntimeObservation {
		return OccurrenceResult{}, outValid, false
	}

	return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictUnknown, ReasonEnvironmentUnresolved,
		"the environment of the fact is not resolved; the obligation is for "+r.ob.Environment), outProblem, true
}

// assessTarget checks the other endpoint of the fact against the target of the relation. An endpoint that no
// binding maps keeps its fact and gives a binding gap. For the target `deployedIn: false` the relation target is the set of
// declared containers and the fact needs a bound endpoint outside it: an endpoint in the set is not evaluated.
func (r *obligationRun) assessTarget(anchor string, bound *BoundEdge, other string) (OccurrenceResult, factOutcome, bool) {
	targets := r.ob.Relation.Target
	negated := r.ob.Relation.TargetMode == policy.TargetOtherHostingTargets

	if len(targets) == 0 && !negated {
		return OccurrenceResult{}, outValid, false
	}

	ids := r.e.input.BindingSet.ByObserved[other]

	switch {
	case len(ids) == 0:
		return r.targetBindingGap(anchor, bound, other), outProblem, true
	case len(r.e.input.BindingSet.Ambiguous[other]) > 0:
		return r.occurrenceOf(anchor, bound, OccurrenceBindingGap, VerdictUnknown, ReasonAmbiguousBinding,
			"non-exclusive bindings map the target to different CALM elements ("+joinSorted(r.e.input.BindingSet.Ambiguous[other])+")"), outProblem, true
	case slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(targets, id) }) == negated:
		// Not in the set of an explicit target, or in the declared containers of `deployedIn: false`.
		return OccurrenceResult{}, outSkip, true
	}

	return OccurrenceResult{}, outValid, false
}

// targetBindingGap gives the binding gap of a target endpoint that no binding maps to a CALM element.
func (r *obligationRun) targetBindingGap(anchor string, bound *BoundEdge, other string) OccurrenceResult {
	node := r.e.nodes[other]

	occ := r.occurrenceOf(anchor, bound, OccurrenceBindingGap, VerdictUnknown, ReasonBindingGap,
		fmt.Sprintf("no binding maps %s %s to a CALM element: the rule needs it to decide on the %s fact", node.Label, node.QualifiedName, bound.Edge.Kind))

	if node.FilePath != "" {
		occ.Location = &Location{Path: node.FilePath, Line: node.StartLine}
	}

	return occ
}
