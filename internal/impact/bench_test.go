package impact_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
)

// The benchmark of full and incremental evaluation (design D16, task 10.4):
//
//	go test -run xxx -bench Impact -benchmem -benchtime 10x -count 5 ./internal/impact/
//
// Every iteration moves the digest of one shared item (the item alternates between two states), so an incremental
// iteration evaluates the assessments that depend on the item and reuses the others. The benchmark reports ns/op and
// B/op, the size of the observed graph (entities and edges) and of the assessment graph, and the size of the dependency
// closures. It fails when the two modes give different results for the same inputs.

// benchmarkCommand is the command that docs/explanation/review-currency.md names and that its numbers come from.
const benchmarkCommand = "go test -run xxx -bench Impact -benchmem -benchtime 10x -count 5 ./internal/impact/"

const (
	// generatedFiles and generatedFanout size the generated graph: about files*fanout observed edges.
	generatedFiles  = 3000
	generatedFanout = 4
)

func BenchmarkImpact(b *testing.B) {
	graphs := []*observed{commerceGraph(b), generatedGraph(b, generatedFiles, generatedFanout)}

	for _, g := range graphs {
		set := buildSet(b, g)

		for _, mode := range []string{"full", "incremental"} {
			b.Run(g.name+"/"+mode, func(b *testing.B) { benchmarkMode(b, set, mode == "incremental") })
		}
	}
}

func benchmarkMode(b *testing.B, set *benchSet, incremental bool) {
	b.Helper()

	set.change("")

	var cache *assessment.Incremental

	if incremental {
		cache = cacheOf(b)

		// The run that fills the cache: the first run of a CI job that has no cache yet.
		set.evaluate(b, cache)
	}

	states := []string{"changed", ""}

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		set.change(states[i%len(states)])

		set.evaluate(b, cache)
	}

	b.StopTimer()

	// The metrics and the comparison of the modes are those of one change from the restored state, whatever iteration
	// ended the loop.
	set.change(states[1])
	set.evaluate(b, cache)

	set.change(states[0])

	last := set.evaluate(b, cache)

	// The other mode on the same inputs must give the same result.
	if incremental {
		sameResult(b, set.graph.name+" incremental", last, set.evaluate(b, nil))
	} else {
		sameResult(b, set.graph.name+" full", set.evaluate(b, cacheOf(b)), last)
	}

	size := sizesOf(last)

	b.ReportMetric(float64(set.graph.entities), "graph-entities")
	b.ReportMetric(float64(set.graph.links), "graph-edges")
	b.ReportMetric(float64(len(set.graph.edges)), "items")
	b.ReportMetric(float64(set.assessments), "assessments")
	b.ReportMetric(float64(set.nodes), "dependency-nodes")
	b.ReportMetric(float64(size.closureTotal), "closure-total")
	b.ReportMetric(float64(size.closureMax), "closure-max")
	b.ReportMetric(float64(size.visitedTotal), "visited-total")
	b.ReportMetric(float64(size.visitedMax), "visited-max")

	if last.Incremental != nil {
		b.ReportMetric(float64(last.Incremental.Hits), "hits/op")
		b.ReportMetric(float64(last.Incremental.Misses), "misses/op")
		b.ReportMetric(float64(last.Incremental.ChangedItems), "changed-items/op")
	}
}
