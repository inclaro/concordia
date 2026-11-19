package conformance

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// tarjan holds the state of one strongly connected components search.
type tarjan struct {
	adj     map[string][]string
	t       *traversal
	index   map[string]int
	low     map[string]int
	onStack map[string]bool
	stack   []string
	out     [][]string
	counter int
}

// sccs gives the strongly connected components with 2 or more nodes (Tarjan), each sorted.
// A traversal that reaches its limit stops: the components found so far are valid, the
// others are missing, and trav.stopped() says so.
func sccs(adj map[string][]string, trav *traversal) [][]string {
	state := &tarjan{adj: adj, t: trav, index: map[string]int{}, low: map[string]int{}, onStack: map[string]bool{}}

	keys := make([]string, 0, len(adj))
	for k := range adj {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	for _, node := range keys {
		if trav.stopped() {
			break
		}

		if _, seen := state.index[node]; !seen {
			state.visit(node)
		}
	}

	return state.out
}

func (tj *tarjan) visit(node string) {
	if !tj.t.visit() {
		return
	}

	tj.index[node] = tj.counter
	tj.low[node] = tj.counter
	tj.counter++

	tj.stack = append(tj.stack, node)
	tj.onStack[node] = true

	for _, next := range tj.adj[node] {
		if _, seen := tj.index[next]; !seen {
			tj.visit(next)

			if tj.t.stopped() {
				return
			}

			tj.low[node] = min(tj.low[node], tj.low[next])
		} else if tj.onStack[next] {
			tj.low[node] = min(tj.low[node], tj.index[next])
		}
	}

	if tj.low[node] == tj.index[node] {
		tj.popComponent(node)
	}
}

// popComponent takes the component of root off the stack and keeps it when it has two or more nodes.
func (tj *tarjan) popComponent(root string) {
	var comp []string

	for {
		top := tj.stack[len(tj.stack)-1]
		tj.stack = tj.stack[:len(tj.stack)-1]
		tj.onStack[top] = false
		comp = append(comp, top)

		if top == root {
			break
		}
	}

	if len(comp) >= minCycleLength {
		slices.SortFunc(comp, canonjson.CompareUTF16)
		tj.out = append(tj.out, comp)
	}
}

// minCycleLength is the smallest number of nodes in a cycle.
const minCycleLength = 2

func buildAdj(pairs map[string]*BoundEdge) map[string][]string {
	sets := map[string]map[string]struct{}{}

	for k := range pairs {
		src, dst := splitPair(k)
		ensureAdjSet(sets, src)
		ensureAdjSet(sets, dst)

		sets[src][dst] = struct{}{}
	}

	adj := make(map[string][]string, len(sets))

	for k, set := range sets {
		adj[k] = sortedAdjList(set)
	}

	return adj
}

// ensureAdjSet makes the successor set of node when it is not there yet.
func ensureAdjSet(sets map[string]map[string]struct{}, node string) {
	if sets[node] == nil {
		sets[node] = map[string]struct{}{}
	}
}

// sortedAdjList gives the members of one successor set in UTF-16 order.
func sortedAdjList(set map[string]struct{}) []string {
	list := make([]string, 0, len(set))
	for next := range set {
		list = append(list, next)
	}

	slices.SortFunc(list, canonjson.CompareUTF16)

	return list
}

// cycleSearch is the state of one deterministic DFS for a cycle through start.
type cycleSearch struct {
	start   string
	members map[string]struct{}
	adj     map[string][]string
	trav    *traversal
	seen    map[string]struct{}
	path    []string
}

// findCycle gives a simple cycle through the smallest node of a component, found with a deterministic DFS.
// A traversal that reaches its limit gives the start alone: no cycle is claimed.
func findCycle(start string, members map[string]struct{}, adj map[string][]string, trav *traversal) []string {
	search := &cycleSearch{
		start:   start,
		members: members,
		adj:     adj,
		trav:    trav,
		seen:    map[string]struct{}{start: {}},
		path:    []string{start},
	}

	if !search.dfs(start) && trav.stopped() {
		return []string{start}
	}

	return search.path
}

// dfs extends the path from node and reports whether it closed a cycle back to the start.
func (cs *cycleSearch) dfs(node string) bool {
	if !cs.trav.visit() {
		return false
	}

	for _, next := range cs.adj[node] {
		if cs.step(next) {
			return true
		}
	}

	return false
}

// step follows the edge to next: true when it closes the cycle or a deeper path does.
func (cs *cycleSearch) step(next string) bool {
	if !hasKey(cs.members, next) {
		return false
	}

	if next == cs.start {
		return true
	}

	if hasKey(cs.seen, next) {
		return false
	}

	cs.seen[next] = struct{}{}

	cs.path = append(cs.path, next)
	if cs.dfs(next) {
		return true
	}

	cs.path = cs.path[:len(cs.path)-1]

	return false
}

// cycleGraphs gives, for each ordered pair of subjects, the first edge of all evidence
// and the first edge at or above the confidence threshold.
func (r *planRun) cycleGraphs() (all, strong map[string]*BoundEdge) {
	all = map[string]*BoundEdge{}
	strong = map[string]*BoundEdge{}

	for _, bound := range r.bound {
		for _, p := range edgePairs(bound) {
			if !hasKey(r.subjects, p[0]) || !hasKey(r.subjects, p[1]) {
				continue
			}

			k := pairKey(p[0], p[1])

			keepFirstEdge(all, k, bound)

			if bound.Edge.Confidence.Rank() >= r.rankFor(bound.Edge.Kind) {
				keepFirstEdge(strong, k, bound)
			}
		}
	}

	return all, strong
}

// keepFirstEdge records bound under k unless an earlier edge sorts first.
func keepFirstEdge(m map[string]*BoundEdge, k string, bound *BoundEdge) {
	if cur, ok := m[k]; !ok || compareEdges(bound, cur) < 0 {
		m[k] = bound
	}
}

func (r *planRun) cycleCandidates() []candidate {
	all, strong := r.cycleGraphs()
	strongAdj := buildAdj(strong)
	strongTrav := r.newTraversal()
	strongSccs := sccs(strongAdj, strongTrav)

	r.noteTraversal(strongTrav)
	r.countSubThresholdCycles(all, strongSccs)

	out := make([]candidate, 0, len(strongSccs))

	for _, ids := range strongSccs {
		cycleTrav := r.newTraversal()
		cyc := findCycle(ids[0], stringSet(ids), strongAdj, cycleTrav)

		r.noteTraversal(cycleTrav)

		if c, ok := r.cycleCandidate(ids, cyc, strong); ok {
			out = append(out, c)
		}
	}

	return out
}

// countSubThresholdCycles counts the components of all evidence that no strong component equals.
func (r *planRun) countSubThresholdCycles(all map[string]*BoundEdge, strongSccs [][]string) {
	strongKeys := map[string]struct{}{}

	for _, c := range strongSccs {
		strongKeys[strings.Join(c, ",")] = struct{}{}
	}

	allTrav := r.newTraversal()

	for _, c := range sccs(buildAdj(all), allTrav) {
		if !hasKey(strongKeys, strings.Join(c, ",")) {
			r.coverage.SubThresholdEvidence++
		}
	}

	r.noteTraversal(allTrav)
}

// cycleCandidate makes the finding candidate of one component and its cycle.
func (r *planRun) cycleCandidate(ids, cyc []string, strong map[string]*BoundEdge) (candidate, bool) {
	edges := cycleEdges(cyc, strong)
	if len(edges) == 0 {
		return candidate{}, false
	}

	steps := make([]EvidenceStep, 0, len(edges)*stepsPerEdge)
	extra := make([]Location, 0, len(edges))

	for i, bound := range edges {
		steps = append(steps, edgeSteps(bound)...)

		if i > 0 {
			extra = append(extra, edgeLocation(bound))
		}
	}

	loc := edgeLocation(edges[0])

	return candidate{
		fingerprint:    CycleFingerprint(r.plan.RuleID, ids),
		kind:           KindCycle,
		sourceID:       ids[0],
		targetID:       strings.Join(ids, ","),
		message:        "dependency cycle: " + strings.Join(append(slices.Clone(cyc), cyc[0]), " -> "),
		edge:           edges[0],
		evidencePath:   steps,
		location:       &loc,
		extraLocations: extra,
	}, true
}

// cycleEdges gives the strong edge of each consecutive pair of the cycle that has one.
func cycleEdges(cyc []string, strong map[string]*BoundEdge) []*BoundEdge {
	edges := make([]*BoundEdge, 0, len(cyc))

	for i, id := range cyc {
		if bound := strong[pairKey(id, cyc[(i+1)%len(cyc)])]; bound != nil {
			edges = append(edges, bound)
		}
	}

	return edges
}

// stepsPerEdge is the number of evidence steps of one edge: node, edge, node.
const stepsPerEdge = 3
