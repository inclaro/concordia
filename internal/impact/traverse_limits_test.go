package impact_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/impact"
)

// chain builds item -> n1 -> ... -> n(len) where the last node is an assessment.
func chain(length int) (graph *impact.Graph, item, last impact.NodeID) {
	item = impact.ItemNodeID(impact.KindProtocol, "p")
	types := map[impact.NodeID]impact.NodeType{}
	prev := item
	edges := make([]edgeSpec, 0, length)

	for i := 1; i <= length; i++ {
		n := asm(fmt.Sprintf("n%04d", i))
		types[n] = impact.NodeAssumption
		edges = append(edges, edgeSpec{n, prev})
		prev, last = n, n
	}

	types[last] = impact.NodeAssessment

	return build(types, edges), item, last
}

// traverseLimited runs a traversal from the seeds with the limits.
func traverseLimited(g *impact.Graph, seeds []impact.Seed, limits impact.Limits) impact.Result {
	return g.Traverse(seeds, impact.Options{Limits: limits})
}

// requireIncomplete stops the test unless a limit stopped the traversal, and gives that stop.
func requireIncomplete(t *testing.T, res *impact.Result) *impact.Incomplete {
	t.Helper()

	if res.Incomplete == nil {
		t.Fatal("incomplete = nil")
	}

	return res.Incomplete
}

// Scenario: A30 traversal limit reached.
// Scenario: Limit raised.
func TestLimitDepthGivesUnknownAndRaisingGivesComparison(t *testing.T) {
	g, item, target := chain(10)

	res := traverseLimited(g, seedOf(item), impact.Limits{MaxDepth: 5})
	stop := requireIncomplete(t, &res)
	requireEq(t, "limit", stop.Limit, impact.LimitDepth)
	requireEq(t, "configured", stop.Configured, 5)
	checkEq(t, "reach", res.Reach(target), impact.ReachUnknown)
	checkEq(t, "triggers of a stopped traversal", len(res.Triggers), 0)

	res = traverseLimited(g, seedOf(item), impact.Limits{MaxDepth: 10})
	requireComplete(t, &res)
	checkEq(t, "raised limit reach", res.Reach(target), impact.ReachAffected)
	checkEq(t, "raised limit triggers", len(res.Triggers), 1)

	// Depth exactly at the closure size is complete; one less is not.
	res = traverseLimited(g, seedOf(item), impact.Limits{MaxDepth: 9})
	checkTrue(t, res.Incomplete != nil, "depth 9 of a 10-edge chain must be incomplete")
}

func TestLimitVisited(t *testing.T) {
	g, item, target := chain(10)

	res := traverseLimited(g, seedOf(item), impact.Limits{MaxVisited: 4})
	stop := requireIncomplete(t, &res)
	requireEq(t, "limit", stop.Limit, impact.LimitVisited)
	requireEq(t, "visited at the stop", stop.Visited, 4)
	requireEq(t, "visited nodes", len(res.Visited), 4)
	checkEq(t, "reach", res.Reach(target), impact.ReachUnknown)

	// 11 nodes in the closure: a limit of 11 completes.
	res = traverseLimited(g, seedOf(item), impact.Limits{MaxVisited: 11})
	requireComplete(t, &res)

	// Seeds count against the limit as well.
	many := seedOf(impact.ItemNodeID(impact.KindProtocol, "a"), impact.ItemNodeID(impact.KindProtocol, "b"), impact.ItemNodeID(impact.KindProtocol, "c"))
	res = traverseLimited(g, many, impact.Limits{MaxVisited: 2})
	checkEq(t, "seed overflow limit", limitOf(&res), impact.LimitVisited)
}

// limitOf gives the limit that stopped the traversal, or an empty text when none did.
func limitOf(res *impact.Result) string {
	if res.Incomplete == nil {
		return ""
	}

	return res.Incomplete.Limit
}

func TestLimitSecondsUsesInjectedClock(t *testing.T) {
	g, item, target := chain(10)

	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	tick := func() time.Time {
		now = now.Add(time.Second)

		return now
	}

	res := g.Traverse(seedOf(item), impact.Options{Limits: impact.Limits{MaxSeconds: 3}, Now: tick})
	stop := requireIncomplete(t, &res)
	requireEq(t, "limit", stop.Limit, impact.LimitSeconds)
	requireEq(t, "configured", stop.Configured, 3)
	checkEq(t, "reach", res.Reach(target), impact.ReachUnknown)

	// A frozen clock never reaches the limit.
	frozen := func() time.Time { return now }
	res = g.Traverse(seedOf(item), impact.Options{Limits: impact.Limits{MaxSeconds: 3}, Now: frozen})
	requireComplete(t, &res)
}

func TestDefaultLimits(t *testing.T) {
	got := impact.Limits{}.WithDefaults()
	checkEq(t, "default depth", got.MaxDepth, impact.DefaultMaxDepth)
	checkEq(t, "default visited", got.MaxVisited, impact.DefaultMaxVisited)
	checkEq(t, "default seconds", got.MaxSeconds, impact.DefaultMaxSeconds)

	set := impact.Limits{MaxDepth: 3, MaxVisited: 4, MaxSeconds: 5}
	checkEq(t, "set limits", set.WithDefaults(), set)

	// The default depth is deep enough for a long chain.
	g, item, _ := chain(impact.DefaultMaxDepth)
	res := g.Traverse(seedOf(item), impact.Options{})
	requireComplete(t, &res)
}
