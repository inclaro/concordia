package adr

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

func (b *builder) find(x int) int {
	for b.dsu[x] != x {
		b.dsu[x] = b.dsu[b.dsu[x]]
		x = b.dsu[x]
	}

	return x
}

func (b *builder) union(x, y int) {
	rx, ry := b.find(x), b.find(y)
	if rx == ry {
		return
	}

	// The smaller index becomes the root so that the result does not depend on the order of the unions.
	if rx > ry {
		rx, ry = ry, rx
	}

	b.dsu[ry] = rx
}

// findCycles runs an iterative Tarjan SCC over the successor-to-predecessor links; every SCC with more than one
// member and every self-loop is a cycle.
func (b *builder) findCycles() {
	t := newTarjan(b.preds)
	for root := range b.preds {
		t.visit(root)
	}

	for _, members := range t.sccs {
		if len(members) > 1 || slices.Contains(b.preds[members[0]], members[0]) {
			b.raw = append(b.raw, Anomaly{Kind: ReasonCycle, Paths: b.pathsOfIndexes(members)})
		}
	}
}

// tarjan is the state of an iterative Tarjan SCC run: an explicit work stack replaces the call stack, so the
// size of the graph cannot overflow it.
type tarjan struct {
	adj     [][]int
	index   []int
	low     []int
	onStack []bool
	stack   []int
	work    []tarjanFrame
	sccs    [][]int
	counter int
}

type tarjanFrame struct{ v, next int }

func newTarjan(adj [][]int) *tarjan {
	t := &tarjan{adj: adj, index: make([]int, len(adj)), low: make([]int, len(adj)), onStack: make([]bool, len(adj))}
	for i := range t.index {
		t.index[i] = -1
	}

	return t
}

func (t *tarjan) enter(v int) {
	t.index[v], t.low[v] = t.counter, t.counter
	t.counter++
	t.stack = append(t.stack, v)
	t.onStack[v] = true
	t.work = append(t.work, tarjanFrame{v: v})
}

// visit explores the component of root.
func (t *tarjan) visit(root int) {
	if t.index[root] != -1 {
		return
	}

	t.enter(root)

	for len(t.work) > 0 {
		top := &t.work[len(t.work)-1]
		cur := top.v

		if top.next < len(t.adj[cur]) {
			w := t.adj[cur][top.next]
			top.next++

			switch {
			case t.index[w] == -1:
				t.enter(w)
			case t.onStack[w]:
				t.low[cur] = min(t.low[cur], t.index[w])
			}

			continue
		}

		t.work = t.work[:len(t.work)-1]
		if len(t.work) > 0 {
			parent := t.work[len(t.work)-1].v
			t.low[parent] = min(t.low[parent], t.low[cur])
		}

		if t.low[cur] == t.index[cur] {
			t.pop(cur)
		}
	}
}

// pop closes the SCC that cur roots.
func (t *tarjan) pop(v int) {
	var members []int

	for {
		w := t.stack[len(t.stack)-1]
		t.stack = t.stack[:len(t.stack)-1]
		t.onStack[w] = false

		members = append(members, w)
		if w == v {
			break
		}
	}

	t.sccs = append(t.sccs, members)
}

func (b *builder) pathsOfIndexes(idx []int) []string {
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, b.nodes[i].Path)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// findCompeting reports each decision with two or more non-pending successors where, for some pair, neither
// reaches the other through successor links.
func (b *builder) findCompeting() {
	for p := range b.nodes {
		involved := b.incomparable(b.liveSuccessors(p))
		if len(involved) == 0 {
			continue
		}

		b.raw = append(b.raw, Anomaly{Kind: ReasonCompetingSuccessors, Paths: b.pathsOfIndexes(append([]int{p}, involved...))})
	}
}

// liveSuccessors are the successors of a decision that are not pending.
func (b *builder) liveSuccessors(p int) []int {
	var live []int

	for _, s := range b.succs[p] {
		if b.nodes[s].Class != ClassPending {
			live = append(live, s)
		}
	}

	return live
}

// incomparable gives the decisions that are in a pair where neither reaches the other.
func (b *builder) incomparable(live []int) []int {
	var out []int

	for i := range live {
		for j := i + 1; j < len(live); j++ {
			if b.reaches(live[i], live[j]) || b.reaches(live[j], live[i]) {
				continue
			}

			out = append(out, live[i], live[j])
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// reaches says whether `to` is a successor of `from`, directly or through successors.
func (b *builder) reaches(from, to int) bool {
	seen := map[int]bool{from: true}
	queue := []int{from}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]

		for _, s := range b.succs[v] {
			if s == to {
				return true
			}

			if !seen[s] {
				seen[s] = true

				queue = append(queue, s)
			}
		}
	}

	return false
}
