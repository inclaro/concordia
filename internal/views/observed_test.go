package views_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
)

// pair finds the relationships from one element to another.
func pair(p *views.Projection, from, to string) []views.ProjectionRelationship {
	var out []views.ProjectionRelationship

	for _, r := range p.Relationships {
		if r.From == from && r.To == to {
			out = append(out, r)
		}
	}

	return out
}

// assertPairCount checks the number of relationships from one element to another, and gives them.
func assertPairCount(t *testing.T, p *views.Projection, from, to string, want int) []views.ProjectionRelationship {
	t.Helper()

	rels := pair(p, from, to)
	if len(rels) != want {
		t.Errorf("%s -> %s = %+v, want %d relationship(s)", from, to, rels, want)
	}

	return rels
}

// assertExplicitNulls checks that no relationship has a protocol, an interface or a context, and that all are forward.
func assertExplicitNulls(t *testing.T, rows []views.ProjectionRelationship) {
	t.Helper()

	for i := range rows {
		r := &rows[i]
		assertNilPtr(t, "protocol", r.Protocol)
		assertNilPtr(t, "interface", r.Interface)
		assertNilPtr(t, "context", r.Context)
		assertEq(t, "direction", r.Direction, views.DirectionForward)
	}
}

func TestObservedProjectionRollsUpByBindingAndContainment(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	view := containerView("payments-container")
	got := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	requireEq(t, "basis", got.Basis, views.BasisObserved)
	requireEq(t, "contract", got.Contract, views.ProjectionContract)

	// web -> orders (twice: web1 -> ord1 and web2 -> handler, which rolls up to its container) merge into one.
	for _, r := range assertPairCount(t, got, "web-ui", "orders-service", 1) {
		assertEq(t, "kind", r.Kind, "calls")
	}

	// The handler component calls the database: the relationship is between the containers.
	assertPairCount(t, got, "orders-service", "payments-db", 1)

	// ord1 -> ord2 is inside one container: no self edge.
	assertPairCount(t, got, "orders-service", "orders-service", 0)

	// Protocol, interface and context are explicit nulls here.
	assertExplicitNulls(t, got.Relationships)
}

func TestObservedProjectionKeepsSelfEdgesWhenAsked(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	view := containerView("payments-container")
	view.Projection = &views.ProjectionRules{SelfEdges: "keep"}
	got := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	assertPairCount(t, got, "orders-service", "orders-service", 1)
}

func TestObservedProjectionSeparateParallelEdges(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	edges = append(edges, call{kind: "IMPORTS", from: "web1", to: "ord1", confidence: "0.95"})

	view := containerView("payments-container")
	view.Projection = &views.ProjectionRules{ParallelEdges: "separate"}

	got := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	rels := pair(got, "web-ui", "orders-service")
	if len(rels) != 2 {
		t.Fatalf("web-ui -> orders-service = %+v, want two relationships (calls and imports)", rels)
	}

	relKinds := []string{rels[0].Kind, rels[1].Kind}
	slices.Sort(relKinds)
	assertSliceEq(t, "kinds", relKinds, []string{"calls", "imports"})

	// With the default `merge`, the two calls that roll up to the same pair are one relationship per kind.
	merged := containerView("payments-container")
	assertPairCount(t, observed(t, &merged, codeEvidence(t, fns, edges, projectionBindings())), "web-ui", "orders-service", 2)
}

func TestObservedProjectionTwoCandidateElements(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	fns = append(fns, fn{"shared1", "services/shared/s.go"})
	edges = append(edges, calls("web1", "shared1"))

	// The shared code is bound to two containers by non-exclusive bindings of the same tier.
	bindings := append(projectionBindings(),
		rootBinding("b-shared-orders", "orders-service", "services/shared/**", false),
		rootBinding("b-shared-db", "payments-db", "services/shared/**", false),
	)

	view := containerView("payments-container")
	got := observed(t, &view, codeEvidence(t, fns, edges, bindings))

	if len(got.Ambiguous) != 1 {
		t.Fatalf("ambiguous = %+v, want the shared entity", got.Ambiguous)
	}

	a := got.Ambiguous[0]
	assertEq(t, "ambiguous entity", a.Entity, "pkg.shared1")
	assertSliceEq(t, "ambiguous candidates", a.Candidates, []string{"orders-service", "payments-db"})

	// No relationship of the ambiguous entity: web-ui still reaches orders-service through ord1 only.
	assertPairCount(t, got, "web-ui", "payments-db", 0)
}

// assertUnbound checks the class of the unbound coverage of a projection and the elements that it touches.
func assertUnbound(t *testing.T, u *views.UnboundCoverage, class string, elements []string) {
	t.Helper()

	if u == nil {
		t.Errorf("unbound = nil, want %s touching %v", class, elements)

		return
	}

	assertEq(t, "unbound class", u.Class, class)
	assertSliceEq(t, "unbound elements", u.Elements, elements)
}

// projectionDigest gives the digest of a projection.
func projectionDigest(t *testing.T, p *views.Projection) string {
	t.Helper()

	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}

	return digest
}

func TestObservedProjectionUnboundEndsAreCoverageNotRelationships(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	// A function of no binding: the orders service calls it.
	fns = append(fns, fn{"stray", "tools/stray.go"})
	edges = append(edges, calls("ord1", "stray"))

	view := containerView("payments-container")
	with := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	fns0, edges0 := codeGraph()
	without := observed(t, &view, codeEvidence(t, fns0, edges0, projectionBindings()))

	if len(with.Relationships) != len(without.Relationships) {
		t.Errorf("the unbound edge became a relationship: %+v", with.Relationships)
	}

	assertUnbound(t, with.Coverage.Unbound, views.UnboundSome, []string{"orders-service"})
	assertUnbound(t, without.Coverage.Unbound, views.UnboundNone, []string{})

	if projectionDigest(t, with) == projectionDigest(t, without) {
		t.Error("a new unbound dependency of an element of the view did not change the observed projection")
	}
}

func TestObservedProjectionLostCoverage(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	view := containerView("payments-container")

	// The binding of the database is removed.
	bindings := slices.DeleteFunc(projectionBindings(), func(b binding.Binding) bool { return b.ArchitectureRef == "payments-db" })
	in := codeEvidence(t, fns, edges, bindings)
	got := views.ObserveProjection(&view, &views.ObserveInput{Reconcile: in, Bindings: bindings})

	assertSliceEq(t, "lost", got.Coverage.Lost, []views.LostCoverage{{Kind: views.LostBinding, Subject: "payments-db"}})

	// The element is not shown as observed.
	for _, e := range got.Elements {
		if e.ID == "payments-db" {
			t.Error("payments-db has no observed entity and is still an observed element")
		}
	}
}

func TestObservedProjectionEdgeFamilyWithoutSupportIsLost(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	view := containerView("payments-container")
	view.Projection = &views.ProjectionRules{EdgeKinds: []string{"calls", "imports"}}

	in := codeEvidence(t, fns, edges, projectionBindings())
	in.SupportedCanonicalKinds = []observation.CanonicalEdgeKind{observation.KindCalls}

	got := views.ObserveProjection(&view, &views.ObserveInput{Reconcile: in, Bindings: projectionBindings()})

	assertSliceEq(t, "lost", got.Coverage.Lost, []views.LostCoverage{{Kind: views.LostEdgeKind, Subject: "imports"}})
}

func TestObservedProjectionMinimumConfidenceFilters(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	edges = append(edges, call{kind: "CALLS", from: "db1", to: "web1", confidence: "0.28"})

	view := containerView("payments-container")
	low := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	// A heuristic edge does not pass the default minimum confidence.
	assertPairCount(t, low, "payments-db", "web-ui", 0)

	view.Comparison = &views.Comparison{MinimumConfidence: "heuristic"}
	got := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	// The heuristic edge passes at the minimum confidence heuristic.
	assertPairCount(t, got, "payments-db", "web-ui", 1)
}

func TestObservedProjectionOutsideTheScopeIsLeftOut(t *testing.T) {
	t.Parallel()

	fns, edges := codeGraph()
	view := containerView("payments-container")
	view.Scope = []string{"web-ui"}

	got := observed(t, &view, codeEvidence(t, fns, edges, projectionBindings()))

	// A relationship between two elements outside the scope is not in the projection; one that crosses it is.
	assertPairCount(t, got, "orders-service", "payments-db", 0)
	assertPairCount(t, got, "web-ui", "orders-service", 1)
}
