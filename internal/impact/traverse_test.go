package impact_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

func asm(id string) impact.NodeID  { return impact.NodeID{Identity: id, Version: "1"} }
func asmt(id string) impact.NodeID { return impact.NodeID{Identity: id, Version: "2"} }

// edgeSpec is dependent -> dependency.
type edgeSpec struct{ dependent, dependency impact.NodeID }

func build(types map[impact.NodeID]impact.NodeType, edges []edgeSpec) *impact.Graph {
	g := impact.NewGraph()
	for id, t := range types {
		g.AddNode(id, t)
	}

	for _, e := range edges {
		g.AddDependency(e.dependent, e.dependency, impact.EdgeDependsOn)
	}

	return g
}

func seedOf(ids ...impact.NodeID) []impact.Seed {
	out := make([]impact.Seed, 0, len(ids))
	for _, id := range ids {
		out = append(out, impact.Seed{Node: id, Op: impact.ChangeModified})
	}

	return out
}

func triggerFor(t *testing.T, r *impact.Result, target impact.NodeID) impact.Trigger {
	t.Helper()

	for _, trig := range r.Triggers {
		if trig.Target == target {
			return trig
		}
	}

	t.Fatalf("no trigger for %s in %+v", target, r.Triggers)

	return impact.Trigger{}
}

// fullAffected is a full evaluation: an assessment is affected when its dependency closure holds a
// changed item. It does not use Traverse.
func fullAffected(g *impact.Graph, changed []impact.NodeID, targets ...impact.NodeID) []impact.NodeID {
	var out []impact.NodeID

	for _, target := range targets {
		for _, dep := range g.Closure(target) {
			if slices.Contains(changed, dep) {
				out = append(out, target)

				break
			}
		}

		if slices.Contains(changed, target) {
			out = append(out, target)
		}
	}

	slices.SortFunc(out, impact.CompareNodeID)

	return out
}

// targetsOf gives the target of each trigger, in order.
func targetsOf(res *impact.Result) []impact.NodeID {
	targets := make([]impact.NodeID, 0, len(res.Triggers))
	for _, trig := range res.Triggers {
		targets = append(targets, trig.Target)
	}

	return targets
}

// Scenario: A24 assessment file unchanged, ingress changed.
func TestA24UnchangedAssessmentReachedFromChangedIngress(t *testing.T) {
	ingress := impact.ItemNodeID(impact.KindPublicEntryPoint, "prod/ingress/api")
	otherItem := impact.ItemNodeID(impact.KindProtocol, "other")
	a1, a2 := asm("ASM-EDGE-1"), asm("ASM-OTHER-1")
	edge, other, third := asmt("ASM-EDGE"), asmt("ASM-OTHER"), asmt("ASM-THIRD")

	g := build(map[impact.NodeID]impact.NodeType{
		a1: impact.NodeAssumption, a2: impact.NodeAssumption, edge: impact.NodeAssessment, other: impact.NodeAssessment, third: impact.NodeAssessment,
	}, []edgeSpec{
		{a1, ingress}, {edge, a1},
		{a2, otherItem}, {other, a2},
		{third, a1}, {third, a2},
	})

	baseline := []impact.Entry{entry(impact.KindPublicEntryPoint, "prod/ingress/api", "sha256:1"), entry(impact.KindProtocol, "other", "sha256:o")}
	current := []impact.Entry{entry(impact.KindPublicEntryPoint, "prod/ingress/api", "sha256:2"), entry(impact.KindProtocol, "other", "sha256:o")}
	diff := impact.Diff(baseline, current, nil)

	res := g.Traverse(diff.Seeds(), impact.Options{})

	requireComplete(t, &res)
	checkSlice(t, "incremental", targetsOf(&res), fullAffected(g, []impact.NodeID{ingress}, edge, other, third))
	checkSlice(t, "path", triggerFor(t, &res, edge).Path, []impact.NodeID{ingress, a1, edge})
	checkEq(t, "reach other", res.Reach(other), impact.ReachUnaffected)
	checkEq(t, "reach edge", res.Reach(edge), impact.ReachAffected)
}

// randomLayers is a random layered graph: items at the bottom, assumptions above, assessments on top.
type randomLayers struct {
	graph   *impact.Graph
	changed []impact.NodeID
	tops    []impact.NodeID
}

// addNodes adds count nodes of one type to types and returns their ids.
func addNodes(types map[impact.NodeID]impact.NodeType, count int, id func(string) impact.NodeID, prefix string, nodeType impact.NodeType) []impact.NodeID {
	nodes := make([]impact.NodeID, 0, count)

	for i := range count {
		n := id(fmt.Sprintf("%s%d", prefix, i))
		types[n] = nodeType

		nodes = append(nodes, n)
	}

	return nodes
}

// randomEdges gives an edge from each dependent to each dependency with the chance of one in oneIn.
func randomEdges(rng *rand.Rand, dependents, dependencies []impact.NodeID, oneIn int) []edgeSpec {
	var edges []edgeSpec

	for _, dependent := range dependents {
		for _, dependency := range dependencies {
			if rng.IntN(oneIn) == 0 {
				edges = append(edges, edgeSpec{dependent, dependency})
			}
		}
	}

	return edges
}

// randomPick selects each node with the chance of one in oneIn.
func randomPick(rng *rand.Rand, nodes []impact.NodeID, oneIn int) []impact.NodeID {
	var picked []impact.NodeID

	for _, n := range nodes {
		if rng.IntN(oneIn) == 0 {
			picked = append(picked, n)
		}
	}

	return picked
}

// newRandomLayers builds a random layered graph with random changes among its items.
func newRandomLayers(rng *rand.Rand) randomLayers {
	types := map[impact.NodeID]impact.NodeType{}

	items := make([]impact.NodeID, 0, 12)
	for i := range 12 {
		items = append(items, impact.ItemNodeID(impact.KindProtocol, fmt.Sprintf("i%d", i)))
	}

	mids := addNodes(types, 8, asm, "m", impact.NodeAssumption)
	tops := addNodes(types, 6, asmt, "t", impact.NodeAssessment)

	edges := randomEdges(rng, mids, items, 4)
	edges = append(edges, randomEdges(rng, tops, mids, 3)...)
	changed := randomPick(rng, items, 5)

	return randomLayers{graph: build(types, edges), changed: changed, tops: tops}
}

// Incremental equals full on a random layered graph with random changes.
func TestIncrementalEqualsFullOnRandomGraphs(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))

	for round := range 25 {
		layers := newRandomLayers(rng)
		res := layers.graph.Traverse(seedOf(layers.changed...), impact.Options{})
		got := targetsOf(&res)

		want := fullAffected(layers.graph, layers.changed, layers.tops...)
		if !slices.Equal(got, want) {
			t.Fatalf("round %d: incremental %v != full %v", round, got, want)
		}
	}
}

// fileDigester digests a file by looking it up in files. An empty path is an error.
func fileDigester(files map[string]string) func(string) (string, bool, error) {
	return func(path string) (digest string, found bool, err error) {
		if path == "" {
			return "", false, errors.New("empty path")
		}

		digest, found = files[path]

		return digest, found, nil
	}
}

// sealEntries seals the items and gives their entries; the test stops on an error.
func sealEntries(t *testing.T, c impact.Contract, items []impact.Item, digester func(string) (string, bool, error)) []impact.Entry {
	t.Helper()

	sealed, err := c.SealAll(items, digester)
	mustOK(t, err)

	out := make([]impact.Entry, 0, len(sealed))
	for i := range sealed {
		out = append(out, sealed[i].Entry())
	}

	return out
}

// assertOnlyImplementationModified checks that the diff holds one modified implementation and
// nothing added or removed.
func assertOnlyImplementationModified(t *testing.T, diff *impact.DiffResult) {
	t.Helper()

	requireEq(t, "modified", len(diff.Modified), 1)
	checkEq(t, "modified kind", diff.Modified[0].Kind, impact.KindImplementation)
	checkEq(t, "added and removed", len(diff.Added)+len(diff.Removed), 0)
}

// Scenario: A22 authorization code changes, topology does not.
func TestA22CodeChangeReachesAssessmentNotTopologyView(t *testing.T) {
	c := impact.V1()
	files := map[string]string{"auth/policy.go": "sha256:a"}
	digester := fileDigester(files)

	items := []impact.Item{
		routeItem(c, "api.example.com"),
		c.NewItem(impact.KindImplementation, "authz", impact.Facets{"subject": {"authorization"}}, []string{"auth/policy.go"}, nil),
	}

	baseline := sealEntries(t, c, items, digester)
	files["auth/policy.go"] = "sha256:b"
	diff := impact.Diff(baseline, sealEntries(t, c, items, digester), nil)
	assertOnlyImplementationModified(t, &diff)

	route := impact.ItemNodeID(impact.KindPublicEntryPoint, "prod/ingress/api")
	authz := impact.ItemNodeID(impact.KindImplementation, "authz")
	a1 := asm("ASM-AUTHZ-1")
	review, view := asmt("ASM-AUTHZ"), impact.NodeID{Identity: "view/context", Version: "1"}

	g := build(map[impact.NodeID]impact.NodeType{a1: impact.NodeAssumption, review: impact.NodeAssessment, view: impact.NodeView},
		[]edgeSpec{{a1, authz}, {a1, route}, {review, a1}, {view, route}})

	res := g.Traverse(diff.Seeds(), impact.Options{})

	checkEq(t, "review reach", res.Reach(review), impact.ReachAffected)
	checkEq(t, "view reach", res.Reach(view), impact.ReachUnaffected)
}

// assertSelfLoopIsACycle checks that a node that depends on itself is one cycle of one node.
func assertSelfLoopIsACycle(t *testing.T, item, node impact.NodeID) {
	t.Helper()

	loop := build(map[impact.NodeID]impact.NodeType{node: impact.NodeAssessment}, []edgeSpec{{node, item}, {node, node}})
	got := loop.Traverse(seedOf(item), impact.Options{}).Cycles

	requireEq(t, "self loop cycles", len(got), 1)
	checkEq(t, "self loop cycle nodes", len(got[0].Nodes), 1)
}

// assertDiamondIsNoCycle checks that two paths to one node are not a cycle.
func assertDiamondIsNoCycle(t *testing.T, item, node impact.NodeID) {
	t.Helper()

	dia := build(map[impact.NodeID]impact.NodeType{node: impact.NodeAssessment},
		[]edgeSpec{{asm("x"), item}, {asm("y"), item}, {node, asm("x")}, {node, asm("y")}})

	checkEq(t, "diamond cycles", len(dia.Traverse(seedOf(item), impact.Options{}).Cycles), 0)
}

// Scenario: A30 cyclic dependencies.
func TestA30CycleTerminatesOncePerPair(t *testing.T) {
	item := impact.ItemNodeID(impact.KindProtocol, "p")
	a, b, mid := asmt("A"), asmt("B"), asm("ASM-1")

	// A depends on the assumption, the assumption on B, B on A, and A on the item.
	g := build(map[impact.NodeID]impact.NodeType{a: impact.NodeAssessment, b: impact.NodeAssessment, mid: impact.NodeAssumption},
		[]edgeSpec{{a, mid}, {mid, b}, {b, a}, {a, item}})

	res := g.Traverse(seedOf(item), impact.Options{})

	requireComplete(t, &res)
	checkEq(t, "visited", len(res.Visited), 4)
	requireEq(t, "cycles", len(res.Cycles), 1)
	checkSlice(t, "cycle nodes", res.Cycles[0].Nodes, []impact.NodeID{a, mid, b})
	checkEq(t, "triggers", len(res.Triggers), 2)

	// A self loop is a cycle as well; an acyclic graph has none.
	assertSelfLoopIsACycle(t, item, a)
	assertDiamondIsNoCycle(t, item, a)
}

// Scenario: SEC-07 two paths to one assessment.
func TestTwoPathsGiveOneTriggerWithAllEvidence(t *testing.T) {
	route := impact.ItemNodeID(impact.KindPublicEntryPoint, "prod/ingress/api")
	other := impact.ItemNodeID(impact.KindProtocol, "tls")
	a1, a2 := asm("ASM-A"), asm("ASM-B")
	target := asmt("ASM-EDGE")

	g := build(map[impact.NodeID]impact.NodeType{a1: impact.NodeAssumption, a2: impact.NodeAssumption, target: impact.NodeAssessment},
		[]edgeSpec{{a1, route}, {a2, route}, {a2, other}, {target, a1}, {target, a2}})

	res := g.Traverse(seedOf(route, other), impact.Options{})

	requireEq(t, "triggers", len(res.Triggers), 1)

	trig := res.Triggers[0]
	checkSlice(t, "path", trig.Path, []impact.NodeID{route, a1, target})
	checkSlice(t, "evidence", trig.Evidence, []impact.NodeID{a1, a2, other, route})
	requireEq(t, "changed", len(trig.Changed), 2)
	checkEq(t, "first changed", trig.Changed[0].Node, other)
	checkEq(t, "second changed", trig.Changed[1].Node, route)
}

// parallelPaths is a route that reaches a target through each of several equal assumptions.
type parallelPaths struct {
	route, target impact.NodeID
	mids          []impact.NodeID
}

// shuffledPath gives the path to the target when the edges are added in a random order.
func (p *parallelPaths) shuffledPath(t *testing.T, seed uint64) []impact.NodeID {
	t.Helper()

	edges := make([]edgeSpec, 0, 2*len(p.mids))
	types := map[impact.NodeID]impact.NodeType{p.target: impact.NodeAssessment}

	for _, m := range p.mids {
		edges = append(edges, edgeSpec{m, p.route}, edgeSpec{p.target, m})
		types[m] = impact.NodeAssumption
	}

	rng := rand.New(rand.NewPCG(seed, 3))
	rng.Shuffle(len(edges), func(a, b int) { edges[a], edges[b] = edges[b], edges[a] })

	res := build(types, edges).Traverse(seedOf(p.route), impact.Options{})

	return triggerFor(t, &res, p.target).Path
}

// Scenario: Path is stable.
func TestEqualLengthPathsChooseTheSamePath(t *testing.T) {
	paths := parallelPaths{
		route:  impact.ItemNodeID(impact.KindPublicEntryPoint, "r"),
		target: asmt("T"),
		mids:   []impact.NodeID{asm("m3"), asm("m1"), asm("m2"), asm("m0")},
	}

	var first []impact.NodeID

	for i := range 20 {
		path := paths.shuffledPath(t, uint64(i))

		if first == nil {
			first = path
		}

		if !slices.Equal(path, first) {
			t.Fatalf("run %d: path %v differs from %v", i, path, first)
		}
	}

	checkSlice(t, "path, want the smallest identity", first, []impact.NodeID{paths.route, asm("m0"), paths.target})
}

func TestShortestPathBeatsLongerPath(t *testing.T) {
	route := impact.ItemNodeID(impact.KindPublicEntryPoint, "r")
	long1, long2 := asm("a1"), asm("a2")
	short := asm("z9")
	target := asmt("T")

	g := build(map[impact.NodeID]impact.NodeType{long1: impact.NodeAssumption, long2: impact.NodeAssumption, short: impact.NodeAssumption, target: impact.NodeAssessment},
		[]edgeSpec{{long1, route}, {long2, long1}, {target, long2}, {short, route}, {target, short}})

	res := g.Traverse(seedOf(route), impact.Options{})

	checkSlice(t, "path", triggerFor(t, &res, target).Path, []impact.NodeID{route, short, target})
}

func TestChangedAssessmentIsItsOwnTrigger(t *testing.T) {
	target := asmt("T")
	g := build(map[impact.NodeID]impact.NodeType{target: impact.NodeAssessment}, nil)

	res := g.Traverse(seedOf(target), impact.Options{})
	trig := triggerFor(t, &res, target)

	checkSlice(t, "path", trig.Path, []impact.NodeID{target})
	checkEq(t, "evidence", len(trig.Evidence), 0)
	checkEq(t, "changed", len(trig.Changed), 1)
}

func TestConsumerNodesAreTargetsAndTargetTypesFilter(t *testing.T) {
	item := impact.ItemNodeID(impact.KindPlacement, "p")
	v, c := impact.NodeID{Identity: "view/x"}, impact.NodeID{Identity: "ctl/y"}

	g := build(map[impact.NodeID]impact.NodeType{v: impact.NodeView, c: impact.NodeControl}, []edgeSpec{{v, item}, {c, item}})

	res := g.Traverse(seedOf(item), impact.Options{})
	checkEq(t, "triggers", len(res.Triggers), 2)

	filtered := g.Traverse(seedOf(item), impact.Options{TargetTypes: []impact.NodeType{impact.NodeView}})
	requireEq(t, "filtered triggers", len(filtered.Triggers), 1)
	checkEq(t, "filtered target", filtered.Triggers[0].Target, v)
}

func TestGraphBasics(t *testing.T) {
	g := impact.NewGraph()
	a, b := asm("a"), asm("b")

	g.AddDependency(a, b, impact.EdgeUsesEvidence)
	g.AddDependency(a, b, impact.EdgeAssesses)
	g.AddDependency(a, b, impact.EdgeConsumes)

	got, ok := g.Edge(a, b)
	checkTrue(t, ok, "the edge is missing")
	checkEq(t, "edge type, want the smallest text", got, impact.EdgeAssesses)
	checkEq(t, "default type", g.Type(a), impact.NodeItem)
	checkSlice(t, "nodes", g.Nodes(), []impact.NodeID{a, b})
	checkEq(t, "NodeID.String", a.String(), "a@1")
	checkEq(t, "NodeID.String without a version", (impact.NodeID{Identity: "x"}).String(), "x")

	closure := g.Closure(b)
	checkTrue(t, closure != nil, "closure is nil, want empty non-nil")
	checkEq(t, "closure size", len(closure), 0)
}

// requireComplete stops the test when a limit stopped the traversal.
func requireComplete(t *testing.T, res *impact.Result) {
	t.Helper()

	if res.Incomplete != nil {
		t.Fatalf("incomplete: %+v", res.Incomplete)
	}
}
