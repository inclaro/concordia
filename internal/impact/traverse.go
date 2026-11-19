package impact

import (
	"slices"
	"time"
)

// Default traversal limits, used for a limit that is not set. The numbers follow design D9 and are
// tuned from the benchmark.
const (
	DefaultMaxDepth   = 64
	DefaultMaxVisited = 100000
	DefaultMaxSeconds = 120
)

// Limits are the traversal limits (`assessments.analysisLimits`). A zero or negative value means
// the default.
type Limits struct {
	MaxDepth   int `json:"maxDepth"`
	MaxVisited int `json:"maxVisited"`
	MaxSeconds int `json:"maxSeconds"`
}

// WithDefaults replaces each unset limit by its default.
func (l Limits) WithDefaults() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}

	if l.MaxVisited <= 0 {
		l.MaxVisited = DefaultMaxVisited
	}

	if l.MaxSeconds <= 0 {
		l.MaxSeconds = DefaultMaxSeconds
	}

	return l
}

// Limit names, as `Incomplete.Limit` and the diagnostic `CO-ASM-008` show them.
const (
	LimitDepth   = "maxDepth"
	LimitVisited = "maxVisited"
	LimitSeconds = "maxSeconds"
)

// Incomplete says that a limit stopped the traversal. It is never silent: the caller reports the
// diagnostic `CO-ASM-008` and turns the currency of every assessment that the traversal did not
// reach into `unknown` with the reason `limit-exceeded` (see Result.Reach).
type Incomplete struct {
	// Limit is LimitDepth, LimitVisited, or LimitSeconds.
	Limit string `json:"limit"`
	// Configured is the value of the limit that stopped the traversal.
	Configured int `json:"configured"`
	// Visited is the number of nodes visited when the traversal stopped.
	Visited int `json:"visited"`
}

// Seed is a changed item that a traversal starts from.
type Seed struct {
	Node NodeID     `json:"node"`
	Op   ChangeKind `json:"change"`
}

// Trigger says that a changed item reaches a target node. All paths to the same target are merged
// into one trigger that keeps all of the evidence.
type Trigger struct {
	// Target is the reached node, for example an assessment.
	Target NodeID `json:"target"`
	// Path is the shortest dependency path from a changed item to the target, both included. Ties
	// break by sorted identity, so the path is the same on every run.
	Path []NodeID `json:"path"`
	// Changed are all the changed items that reach the target, sorted.
	Changed []Seed `json:"changed"`
	// Evidence are all nodes on any path from a changed item to the target (assumptions
	// included), the changed items among them, the target not. Sorted.
	Evidence []NodeID `json:"evidence"`
}

// Cycle is a set of nodes that depend on each other. A traversal visits each of them once.
// The caller reports it as the informational diagnostic `CO-ASM-009`.
type Cycle struct {
	Nodes []NodeID `json:"nodes"`
}

// Options configure Traverse.
type Options struct {
	// Now gives the current time for maxSeconds. Nil means time.Now. Tests inject a fake clock.
	Now func() time.Time
	// TargetTypes are the node types that get a trigger when reached. Empty means every type
	// except item and assumption.
	TargetTypes []NodeType
	// Limits are the traversal limits. Unset values take the defaults.
	Limits Limits
}

// Reach is what a traversal says about a target node.
type Reach string

// Reach values.
const (
	// ReachAffected: a changed item reaches the node. It has a trigger when its type is a target type.
	ReachAffected Reach = "affected"
	// ReachUnaffected: the traversal completed and did not reach the node.
	ReachUnaffected Reach = "unaffected"
	// ReachUnknown: a limit stopped the traversal before the node was reached or excluded. The
	// currency is `unknown` with the reason `limit-exceeded`.
	ReachUnknown Reach = "unknown"
)

// Result is the outcome of Traverse.
type Result struct {
	visited map[NodeID]bool
	// Incomplete is set when a limit stopped the traversal.
	Incomplete *Incomplete `json:"incomplete,omitempty"`
	// Visited are the nodes that the traversal visited, sorted.
	Visited []NodeID `json:"visited"`
	// Triggers are the merged triggers of the target nodes, sorted by target.
	Triggers []Trigger `json:"triggers"`
	// Cycles are the dependency cycles among the visited nodes, sorted.
	Cycles []Cycle `json:"cycles"`
}

// Reach says whether the target is affected, unaffected, or unknown because of a limit.
func (r *Result) Reach(target NodeID) Reach {
	switch {
	case r.visited[target]:
		return ReachAffected
	case r.Incomplete != nil:
		return ReachUnknown
	default:
		return ReachUnaffected
	}
}

// traversal is the state of one Traverse call.
type traversal struct {
	now     func() time.Time
	start   time.Time
	visited map[NodeID]bool
	parent  map[NodeID]NodeID
	seeds   map[NodeID]Seed
	// succ and pred hold every edge between visited nodes that the traversal followed.
	succ map[NodeID][]NodeID
	pred map[NodeID][]NodeID
	// incomplete is the first limit that was hit; stopped says to end the search now.
	incomplete *Incomplete
	targets    []NodeType
	limits     Limits
	stopped    bool
}

func newTraversal(opts Options) *traversal {
	run := &traversal{
		now:     opts.Now,
		targets: opts.TargetTypes,
		limits:  opts.Limits.WithDefaults(),
		visited: map[NodeID]bool{},
		parent:  map[NodeID]NodeID{},
		seeds:   map[NodeID]Seed{},
		succ:    map[NodeID][]NodeID{},
		pred:    map[NodeID][]NodeID{},
	}

	if run.now == nil {
		run.now = time.Now
	}

	if len(run.targets) == 0 {
		run.targets = []NodeType{NodeAssessment, NodeView, NodeControl, NodeQualityScenario}
	}

	run.start = run.now()

	return run
}

// Traverse runs a breadth-first search from the seeds along dependent edges: from a changed item to
// the assumptions, assessments, and consumers that depend on it, in files that did or did not
// change. The visited set is keyed by (identity, version); each pair is visited once, so a cycle
// ends without an error and is returned in Result.Cycles.
//
// Every level is expanded in sorted node order, so a node's parent is the smallest-sorted node of
// the previous level that reaches it, and the shortest path to it is deterministic.
//
// A limit stops the traversal and sets Result.Incomplete; nothing is truncated silently.
func (g *Graph) Traverse(seeds []Seed, opts Options) Result {
	run := newTraversal(opts)
	frontier := run.seed(seeds)

	for depth := 0; len(frontier) > 0 && !run.stopped; depth++ {
		frontier = run.expandLevel(g, frontier, depth)
	}

	return run.result(g, run.targets)
}

// expandLevel visits the dependents of every node of the frontier and gives the next frontier, sorted.
func (run *traversal) expandLevel(g *Graph, frontier []NodeID, depth int) []NodeID {
	var next []NodeID

	for _, node := range frontier {
		if run.now().Sub(run.start) > time.Duration(run.limits.MaxSeconds)*time.Second {
			run.stop(LimitSeconds, run.limits.MaxSeconds)

			break
		}

		next = run.expandNode(node, sortedKeys(g.dependents[node]), depth, next)
		if run.stopped {
			break
		}
	}

	slices.SortFunc(next, CompareNodeID)

	return next
}

// expandNode records the edges from node to its dependents and visits the new ones.
func (run *traversal) expandNode(node NodeID, dependents []NodeID, depth int, next []NodeID) []NodeID {
	for _, dep := range dependents {
		if !run.visited[dep] {
			if depth >= run.limits.MaxDepth {
				run.mark(LimitDepth, run.limits.MaxDepth)

				continue
			}

			if len(run.visited) >= run.limits.MaxVisited {
				run.stop(LimitVisited, run.limits.MaxVisited)

				break
			}

			run.visited[dep] = true
			run.parent[dep] = node
			next = append(next, dep)
		}

		run.succ[node] = append(run.succ[node], dep)
		run.pred[dep] = append(run.pred[dep], node)
	}

	return next
}

// seed visits the seeds in sorted order and gives them as the first frontier.
func (run *traversal) seed(seeds []Seed) []NodeID {
	var frontier []NodeID

	for _, seed := range sortedSeeds(seeds) {
		if _, dup := run.seeds[seed.Node]; dup {
			continue
		}

		if len(run.visited) >= run.limits.MaxVisited {
			run.stop(LimitVisited, run.limits.MaxVisited)

			break
		}

		run.seeds[seed.Node] = seed
		run.visited[seed.Node] = true
		frontier = append(frontier, seed.Node)
	}

	return frontier
}

// mark records the first limit that was hit without ending the search (the depth limit only
// leaves out deeper nodes).
func (run *traversal) mark(limit string, configured int) {
	if run.incomplete == nil {
		run.incomplete = &Incomplete{Limit: limit, Configured: configured}
	}
}

// stop records the limit and ends the search.
func (run *traversal) stop(limit string, configured int) {
	run.mark(limit, configured)
	run.stopped = true
}

func sortedSeeds(seeds []Seed) []Seed {
	out := slices.Clone(seeds)
	slices.SortFunc(out, func(a, b Seed) int { return CompareNodeID(a.Node, b.Node) })

	return out
}

func (run *traversal) result(graph *Graph, targets []NodeType) Result {
	res := Result{
		Visited:    make([]NodeID, 0, len(run.visited)),
		Triggers:   []Trigger{},
		Cycles:     run.cycles(),
		Incomplete: run.incomplete,
		visited:    run.visited,
	}

	for id := range run.visited {
		res.Visited = append(res.Visited, id)
	}

	slices.SortFunc(res.Visited, CompareNodeID)

	if res.Incomplete != nil {
		res.Incomplete.Visited = len(res.Visited)
	}

	for _, id := range res.Visited {
		if slices.Contains(targets, graph.Type(id)) {
			res.Triggers = append(res.Triggers, run.trigger(id))
		}
	}

	return res
}

// trigger builds the merged trigger of a visited target.
func (run *traversal) trigger(target NodeID) Trigger {
	trig := Trigger{Target: target, Path: []NodeID{}, Changed: []Seed{}, Evidence: []NodeID{}}

	for node := target; ; {
		trig.Path = append(trig.Path, node)

		parent, ok := run.parent[node]
		if !ok {
			break
		}

		node = parent
	}

	slices.Reverse(trig.Path)

	// Every node that reaches the target through followed edges is evidence.
	seen := map[NodeID]bool{target: true}
	stack := []NodeID{target}

	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		for _, p := range run.pred[node] {
			if !seen[p] {
				seen[p] = true
				stack = append(stack, p)
			}
		}
	}

	for node := range seen {
		if node != target {
			trig.Evidence = append(trig.Evidence, node)
		}

		if seed, ok := run.seeds[node]; ok {
			trig.Changed = append(trig.Changed, seed)
		}
	}

	slices.SortFunc(trig.Evidence, CompareNodeID)
	slices.SortFunc(trig.Changed, func(a, b Seed) int { return CompareNodeID(a.Node, b.Node) })

	return trig
}

// cycles finds the strongly connected components with more than one node, or one node with an
// edge to itself, among the visited nodes.
func (run *traversal) cycles() []Cycle {
	scc := &tarjan{
		succ:    run.succ,
		index:   map[NodeID]int{},
		low:     map[NodeID]int{},
		onStack: map[NodeID]bool{},
		out:     []Cycle{},
	}

	nodes := make([]NodeID, 0, len(run.visited))
	for id := range run.visited {
		nodes = append(nodes, id)
	}

	slices.SortFunc(nodes, CompareNodeID)

	for _, node := range nodes {
		if _, seen := scc.index[node]; !seen {
			scc.visit(node)
		}
	}

	slices.SortFunc(scc.out, func(a, b Cycle) int { return CompareNodeID(a.Nodes[0], b.Nodes[0]) })

	return scc.out
}

// tarjan is the state of Tarjan's strongly-connected-components search.
type tarjan struct {
	succ    map[NodeID][]NodeID
	index   map[NodeID]int
	low     map[NodeID]int
	onStack map[NodeID]bool
	stack   []NodeID
	out     []Cycle
	next    int
}

func (scc *tarjan) visit(node NodeID) {
	scc.index[node], scc.low[node] = scc.next, scc.next
	scc.next++

	scc.stack = append(scc.stack, node)
	scc.onStack[node] = true

	succ := slices.Clone(scc.succ[node])
	slices.SortFunc(succ, CompareNodeID)

	for _, other := range succ {
		if _, seen := scc.index[other]; !seen {
			scc.visit(other)

			scc.low[node] = min(scc.low[node], scc.low[other])
		} else if scc.onStack[other] {
			scc.low[node] = min(scc.low[node], scc.index[other])
		}
	}

	if scc.low[node] != scc.index[node] {
		return
	}

	var comp []NodeID

	for {
		other := scc.stack[len(scc.stack)-1]
		scc.stack = scc.stack[:len(scc.stack)-1]
		scc.onStack[other] = false

		comp = append(comp, other)

		if other == node {
			break
		}
	}

	if len(comp) > 1 || slices.Contains(scc.succ[node], node) {
		slices.SortFunc(comp, CompareNodeID)
		scc.out = append(scc.out, Cycle{Nodes: comp})
	}
}
