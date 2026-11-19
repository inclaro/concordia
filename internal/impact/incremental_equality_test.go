package impact_test

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/impact"
)

// Scenario: A32 two modes equal.
// Scenario: Benchmark detects divergence (the comparison that the benchmark runs, asserted without the benchmark).
//
// The two modes evaluate the same inputs on the pilot graph and on a generated graph, through a change of one shared
// item and back, and give the same currency, triggers, paths, digests, and counters. The incremental mode reuses the
// assessments that do not depend on the changed item.
func TestIncrementalEqualsFullOnBothGraphs(t *testing.T) {
	t.Parallel()

	graphs := map[string]*observed{
		"commerce":  commerceGraph(t),
		"generated": generatedGraph(t, 600, 3),
	}

	for name, g := range graphs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			set := buildSet(t, g)
			cache := cacheOf(t)

			// The run that fills the cache.
			cold := set.evaluate(t, cache)
			sameResult(t, name+" cold", cold, set.evaluate(t, nil))

			if cold.Incremental.Hits != 0 || cold.Incremental.Misses != set.assessments {
				t.Fatalf("cold run: %+v", cold.Incremental)
			}

			for _, state := range []string{"changed", "", "changed again"} {
				set.change(state)

				incremental := set.evaluate(t, cache)
				sameResult(t, name+" "+state, incremental, set.evaluate(t, nil))
				assertOneClusterReevaluated(t, state, incremental.Incremental, set)
				assertCurrencyMoved(t, state, incremental, set)
			}

			// Nothing changed since the last run: everything is a hit, and the result is the same.
			again := set.evaluate(t, cache)
			sameResult(t, name+" unchanged", again, set.evaluate(t, nil))

			if again.Incremental.Misses != 0 || again.Incremental.ChangedItems != 0 {
				t.Errorf("unchanged run: %+v", again.Incremental)
			}
		})
	}
}

// assertOneClusterReevaluated checks the cache report of a run after one shared item changed.
func assertOneClusterReevaluated(t *testing.T, state string, stats *assessment.IncrementalReport, set *benchSet) {
	t.Helper()

	if stats.Status != impact.CacheLoaded || stats.ChangedItems != 1 {
		t.Errorf("%s: cache = %+v, want a loaded cache and one changed item", state, stats)
	}

	// The changed item is shared by the assessments of one cluster: only they are evaluated again.
	if !missesFitOneCluster(stats, set.assessments) {
		t.Errorf("%s: hits %d, misses %d of %d assessments", state, stats.Hits, stats.Misses, set.assessments)
	}
}

func missesFitOneCluster(stats *assessment.IncrementalReport, assessments int) bool {
	if stats.Misses < 1 || stats.Misses > clusterSize {
		return false
	}

	return stats.Hits+stats.Misses == assessments
}

// assertCurrencyMoved checks that a changed item makes the assessments of its cluster review-required and leaves the
// others current: the equality above is not the equality of two empty results.
func assertCurrencyMoved(t *testing.T, state string, ev *assessment.Evaluation, set *benchSet) {
	t.Helper()

	reviewRequired := countReviewRequired(ev)

	if state == "" {
		// The item is back to what the approvals bind.
		if reviewRequired != 0 {
			t.Errorf("%d assessments are review-required after the item was restored", reviewRequired)
		}

		return
	}

	if reviewOutsideOneCluster(reviewRequired, set.assessments) {
		t.Errorf("%s: %d of %d assessments are review-required", state, reviewRequired, set.assessments)
	}
}

func countReviewRequired(ev *assessment.Evaluation) int {
	reviewRequired := 0

	for i := range ev.Assessments {
		if ev.Assessments[i].Currency.State == assessment.CurrencyReviewRequired {
			reviewRequired++
		}
	}

	return reviewRequired
}

// reviewOutsideOneCluster says whether the number of review-required assessments is not that of
// one changed cluster.
func reviewOutsideOneCluster(reviewRequired, total int) bool {
	if reviewRequired < 1 || reviewRequired > clusterSize {
		return true
	}

	return reviewRequired == total && total > clusterSize
}

// Scenario: Benchmark records values (the sizes in docs/explanation/review-currency.md are those of the graphs).
func TestBenchmarkDocumentMatchesTheGraphs(t *testing.T) {
	t.Parallel()

	doc := readDoc(t)

	_, section, found := strings.Cut(doc, "## Benchmark")
	if !found {
		t.Fatal("section `Benchmark` missing")
	}

	if !strings.Contains(section, "\n"+benchmarkCommand+"\n") {
		t.Errorf("the Benchmark section does not show the command %q", benchmarkCommand)
	}

	documented := documentedBenchmarkRows(t, section)
	graphs := map[string]*observed{"commerce": commerceGraph(t), "generated": generatedGraph(t, generatedFiles, generatedFanout)}

	for name, g := range graphs {
		set := buildSet(t, g)
		set.change("changed")

		size := sizesOf(set.evaluate(t, nil))
		got := []int{g.entities, g.links, len(g.edges), set.assessments, set.nodes, size.closureTotal, size.closureMax, size.visitedMax}

		if want := documented[name]; !slices.Equal(got, want) {
			t.Errorf("%s: sizes %v, the document says %v (entities, edges, items, assessments, nodes, closure total, closure max, visited max)", name, got, want)
		}
	}
}

var benchmarkRowPattern = regexp.MustCompile("(?m)^\\| `(commerce|generated)` \\| (\\d+) \\| (\\d+) \\| (\\d+) \\| (\\d+) \\| (\\d+) \\| (\\d+) \\| (\\d+) \\| (\\d+) \\|$")

// documentedBenchmarkRows reads the numbers of each graph from the rows of the Benchmark section.
func documentedBenchmarkRows(t *testing.T, section string) map[string][]int {
	t.Helper()

	documented := map[string][]int{}

	for _, m := range benchmarkRowPattern.FindAllStringSubmatch(section, -1) {
		numbers := make([]int, 0, len(m)-2)

		for _, text := range m[2:] {
			n, err := strconv.Atoi(text)
			if err != nil {
				t.Fatal(err)
			}

			numbers = append(numbers, n)
		}

		documented[m[1]] = numbers
	}

	return documented
}
