package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

func strategyModel() *intent.ArchitectureModel {
	return &intent.ArchitectureModel{Nodes: []intent.NodeDeclaration{{ID: "billing", Name: "Billing"}}}
}

func strategySet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}

	return m
}

func findMatch(cands []candidate, want *candidateMatch) *candidate {
	for i := range cands {
		if cands[i].Match == *want {
			return &cands[i]
		}
	}

	return nil
}

func TestParseStrategiesAcceptsAllAndRejectsOthers(t *testing.T) {
	got, err := parseStrategies("path,package,build,route,cluster,deployment")
	if err != nil || len(got) != 6 {
		t.Fatalf("got %v, %v", got, err)
	}

	_, err = parseStrategies("path,nope")
	if err == nil || !strings.Contains(err.Error(), "--strategy accepts only 'path', 'package', 'build', 'route', 'cluster' and 'deployment', got 'path,nope'") {
		t.Fatalf("error = %v", err)
	}
}

func TestDiscoverBuildStrategy(t *testing.T) {
	files := []string{"svc/billing/BUILD.bazel", "svc/billing/a.go", "other/BUILD", "BUILD"}
	nodes := []observation.ObservedNode{{ObservedID: "cbm:p:1", Label: "File", FilePath: "svc/billing/a.go"}}

	cands := discoverCandidates(strategyModel(), files, nodes, nil, nil, strategySet(strategyBuild))

	c := findMatch(cands, &candidateMatch{BuildTarget: "//svc/billing/..."})
	if c == nil {
		t.Fatalf("no build candidate: %+v", cands)
	}

	if c.MatchedObservedCount != 1 || !strings.Contains(c.Reasons[0], "build package 'svc/billing' name equals the unique-id of 'billing'") {
		t.Fatalf("candidate = %+v", c)
	}

	if len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}
}

func TestDiscoverRouteStrategy(t *testing.T) {
	nodes := []observation.ObservedNode{
		{ObservedID: "cbm:p:1", Label: "Route", QualifiedName: "GET:/api/billing/invoices", FilePath: "src/web/routes.go"},
		{ObservedID: "cbm:p:2", Label: "Route", QualifiedName: "GET:/api/other", FilePath: "src/other/routes.go"},
		{ObservedID: "cbm:p:3", Label: "File", FilePath: "src/web/routes.go"},
	}

	cands := discoverCandidates(strategyModel(), []string{"src/web/routes.go"}, nodes, nil, nil, strategySet(strategyRoute))

	c := findMatch(cands, &candidateMatch{SourceRoot: "src/web/**"})
	if c == nil || len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}

	if c.Score != packageScore || c.Reasons[0] != "route 'GET:/api/billing/invoices' has the segment 'billing'" {
		t.Fatalf("candidate = %+v", c)
	}
}

func TestDiscoverClusterStrategy(t *testing.T) {
	links := []fileLink{
		{From: "src/billing/api/a.go", To: "src/billing/core/b.go"},
		{From: "src/billing/core/b.go", To: "src/billing/db/c.go"},
		{From: "src/ui/x.go", To: "src/lib/y.go"},
	}
	files := []string{"src/billing/api/a.go", "src/billing/core/b.go", "src/billing/db/c.go"}

	cands := discoverCandidates(strategyModel(), files, nil, links, nil, strategySet(strategyCluster))

	c := findMatch(cands, &candidateMatch{SourceRoot: "src/billing/**"})
	if c == nil || len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}

	if c.Reasons[0] != "import cluster under 'src/billing' matches the slug 'billing'" {
		t.Fatalf("reason = %q", c.Reasons[0])
	}

	if got := clusterPrefixes(links); !slices.Equal(got, []string{"src", "src/billing"}) && !slices.Equal(got, []string{"src/billing", "src"}) {
		t.Fatalf("prefixes = %v", got)
	}
}
