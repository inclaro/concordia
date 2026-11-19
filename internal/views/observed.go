package views

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/observation"
)

// DefaultMinimumConfidence is the confidence that an observed edge needs when the view sets no
// `comparison.minimumConfidence` and the caller gives none.
const DefaultMinimumConfidence = observation.ConfidenceSyntactic

// ObserveInput is the input of ObserveProjection.
type ObserveInput struct {
	// Reconcile is the input of the reconciliation of the run: the model, the binding set, the evidence and
	// the exclusions. The endpoints of an edge are resolved by conformance.ScanEdges, the scan of Reconcile.
	Reconcile *conformance.ReconcileInput
	// MinimumConfidence applies when the view sets none. Empty means DefaultMinimumConfidence.
	MinimumConfidence observation.Confidence
	// Bindings are the bindings of the run (their projection hints decide the C4 level of a node).
	Bindings []binding.Binding
}

// ObserveProjection builds the observed projection of a view (design D5): the observed edges of the kinds of
// the view, rolled up by binding and containment to the elements at the abstraction of the view.
//
//   - An edge with both ends in one element is dropped (`selfEdges: omit`), so an internal refactor leaves the
//     projection unchanged.
//   - Edges merge by (from, to, kind, protocol); `parallelEdges: separate` keeps the native kind, environment,
//     reference role and occurrence apart.
//   - An entity that merges into more than one element is `ambiguous` with its candidates, and none of its edges
//     is in the projection.
//   - An edge with one unbound end is not a relationship. The elements of the view that it touches are in
//     `coverage.unbound`, and no count enters the projection.
//   - A necessary element without any bound entity, and an edge kind of the view that the provider does not
//     support, are in `coverage.lost`.
//
// A view with an environment keeps the edges of that environment and the ones that have none.
func ObserveProjection(view *Definition, in *ObserveInput) *Projection {
	rin := in.Reconcile
	lay := newLayout(view, rin.Model, in.Bindings)

	wanted, lost := observedKinds(view, rin.SupportedCanonicalKinds)
	scan := conformance.ScanEdges(rin, wanted)
	run := &observedRun{
		view: view, lay: lay, set: &rin.BindingSet, separate: view.EffectiveParallelEdges() == "separate",
		keepSelf: view.EffectiveSelfEdges() != "omit", minimum: minimumConfidence(view, in.MinimumConfidence),
		rows: map[string]*ProjectionRelationship{}, ambiguous: map[string]map[string]bool{},
		unbound: map[string]bool{}, ends: map[string]bool{},
	}

	for _, bound := range scan.Bound {
		run.edge(bound)
	}

	for _, half := range scan.HalfBound {
		run.halfBound(half)
	}

	bound := run.boundElements()
	elements := map[string]string{}

	for id := range lay.inScope {
		if bound[id] {
			elements[id] = ""
		}
	}

	for id := range run.ends {
		elements[id] = ""
	}

	required := lay.required(view, run.betweenDeclared())
	for _, id := range required.Elements {
		if lay.byID[id] != nil && !bound[id] {
			lost = append(lost, LostCoverage{Kind: LostBinding, Subject: id})
		}
	}

	sortLost(lost)

	rows := make([]ProjectionRelationship, 0, len(run.rows))
	for _, row := range run.rows {
		rows = append(rows, *row)
	}

	sortRelationships(rows)

	unbound := UnboundCoverage{Class: UnboundNone, Elements: sortedKeys(run.unbound)}
	if len(unbound.Elements) > 0 {
		unbound.Class = UnboundSome
	}

	return &Projection{
		ObservedPart:  &ObservedPart{Ambiguous: run.ambiguousEntities()},
		Contract:      ProjectionContract,
		Basis:         BasisObserved,
		View:          viewOf(view),
		Elements:      lay.elementRows(view, elements),
		Relationships: rows,
		Depicts:       Subject{Elements: sortedKeys(elements), Relationships: []string{}},
		Coverage:      ProjectionCoverage{Required: required, Lost: nonNil(lost), Unbound: &unbound},
		Missing:       []string{MissingRelationshipContext, MissingRelationshipInterface},
	}
}

// observedKinds gives the edge kinds to scan (nil: the code kinds of the reconciliation) and the lost coverage
// of the kinds that the provider does not support. A view that names no kind never loses coverage here: the
// reconciliation scan takes the code kinds that the provider gives.
func observedKinds(view *Definition, supported []observation.CanonicalEdgeKind) (map[observation.CanonicalEdgeKind]struct{}, []LostCoverage) {
	if view.Projection == nil || len(view.Projection.EdgeKinds) == 0 {
		return nil, nil
	}

	wanted := map[observation.CanonicalEdgeKind]struct{}{}

	var lost []LostCoverage

	for _, token := range view.Projection.EdgeKinds {
		kinds, ok := edgekind.Resolve(token)
		if !ok {
			continue
		}

		for _, kind := range kinds {
			wanted[kind] = struct{}{}

			if len(supported) > 0 && !slices.Contains(supported, kind) {
				lost = append(lost, LostCoverage{Kind: LostEdgeKind, Subject: string(kind)})
			}
		}
	}

	return wanted, lost
}

func minimumConfidence(view *Definition, fallback observation.Confidence) observation.Confidence {
	if view.Comparison != nil && view.Comparison.MinimumConfidence != "" {
		return observation.Confidence(view.Comparison.MinimumConfidence)
	}

	if fallback != "" {
		return fallback
	}

	return DefaultMinimumConfidence
}

func sortLost(lost []LostCoverage) {
	slices.SortFunc(lost, func(a, b LostCoverage) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}

		return strings.Compare(a.Subject, b.Subject)
	})
}

// observedRun is the state of one ObserveProjection call.
type observedRun struct {
	view *Definition
	lay  *layout
	set  *binding.Set
	// rows are the relationships by key.
	rows map[string]*ProjectionRelationship
	// ambiguous are the candidates of each ambiguous entity.
	ambiguous map[string]map[string]bool
	// unbound are the elements of the view that an unbound edge touches.
	unbound map[string]bool
	// ends are the elements that a relationship of the projection has as an end.
	ends     map[string]bool
	minimum  observation.Confidence
	separate bool
	keepSelf bool
}

// keeps says whether the edge counts for the view: its confidence reaches the minimum, and it belongs to the
// environment of the view (an edge without an environment belongs to every one).
func (r *observedRun) keeps(edge *observation.EvidenceEdge) bool {
	if edge.Confidence.Rank() < r.minimum.Rank() {
		return false
	}

	return r.view.Environment == "" || edge.Environment == "" || edge.Environment == r.view.Environment
}

// elementsOf gives the elements that the CALM IDs of an observed entity show as (sorted, distinct). IDs with no C4
// element are left out.
func (r *observedRun) elementsOf(ids []string) []string {
	set := map[string]bool{}

	for _, id := range ids {
		if up, ok := r.lay.rollUp(id); ok {
			set[up] = true
		}
	}

	return sortedKeys(set)
}

// recordAmbiguous records an entity that merges into more than one element, with the candidates.
func (r *observedRun) recordAmbiguous(node *observation.ObservedNode, elements []string) {
	entity := entityName(node)
	if r.ambiguous[entity] == nil {
		r.ambiguous[entity] = map[string]bool{}
	}

	for _, e := range elements {
		r.ambiguous[entity][e] = true
	}
}

func entityName(node *observation.ObservedNode) string {
	switch {
	case node.QualifiedName != "":
		return node.QualifiedName
	case node.FilePath != "":
		return node.FilePath
	}

	return node.ObservedID
}

func (r *observedRun) edge(bound *conformance.BoundEdge) {
	if !r.keeps(bound.Edge) {
		return
	}

	fromElements, toElements := r.elementsOf(bound.FromIDs), r.elementsOf(bound.ToIDs)

	// An edge outside the scope neither shows nor makes an entity ambiguous for this view.
	if !slices.ContainsFunc(slices.Concat(fromElements, toElements), func(e string) bool { return r.lay.inScope[e] }) {
		return
	}

	if len(fromElements) > 1 {
		r.recordAmbiguous(&bound.From, fromElements)
	}

	if len(toElements) > 1 {
		r.recordAmbiguous(&bound.To, toElements)
	}

	if len(fromElements) != 1 || len(toElements) != 1 {
		return
	}

	from, dest := fromElements[0], toElements[0]
	if from == dest && !r.keepSelf {
		return
	}

	protocol := observation.EdgeKeyOf(bound.Edge).Operation
	row := ProjectionRelationship{
		From: from, To: dest, Kind: string(bound.Edge.Kind), Direction: DirectionForward, Protocol: stringPtr(protocol),
	}

	if r.separate {
		row.Discriminator = discriminator(bound.Edge)
	}

	key := relationshipKey(&row)
	if _, seen := r.rows[key]; !seen {
		r.rows[key] = &row
	}

	r.ends[from], r.ends[dest] = true, true
}

// discriminator tells parallel edges of one kind and protocol apart.
func discriminator(edge *observation.EvidenceEdge) string {
	key := observation.EdgeKeyOf(edge)

	return strings.Join([]string{edge.NativeKind, key.Environment, key.Reference, key.Occurrence}, "/")
}

// halfBound records the elements of the view that an edge with one unbound end touches.
func (r *observedRun) halfBound(bound *conformance.BoundEdge) {
	if !r.keeps(bound.Edge) {
		return
	}

	ids := bound.FromIDs
	if len(ids) == 0 {
		ids = bound.ToIDs
	}

	for _, element := range r.elementsOf(ids) {
		if r.lay.inScope[element] {
			r.unbound[element] = true
		}
	}
}

// boundElements gives the elements that at least one observed entity is bound to, with their ancestors. An
// entity that merges into more than one element evidences none of them.
func (r *observedRun) boundElements() map[string]bool {
	bound := map[string]bool{}

	for _, ids := range r.set.ByObserved {
		elements := r.elementsOf(ids)
		if len(elements) != 1 {
			continue
		}

		for x := r.lay.byID[elements[0]]; x != nil; x = r.lay.parentOf(x) {
			bound[x.ID] = true
		}
	}

	return bound
}

// betweenDeclared gives the declared relationships between elements of the scope, the necessary coverage that
// the view does not narrow.
func (r *observedRun) betweenDeclared() map[string]bool {
	_, between, _ := r.lay.declaredRelationships(r.view)

	return between
}

func (r *observedRun) ambiguousEntities() []AmbiguousEntity {
	out := make([]AmbiguousEntity, 0, len(r.ambiguous))

	for _, entity := range sortedKeys(r.ambiguous) {
		out = append(out, AmbiguousEntity{Entity: entity, Candidates: sortedKeys(r.ambiguous[entity])})
	}

	return out
}
