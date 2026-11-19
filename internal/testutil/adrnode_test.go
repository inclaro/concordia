package testutil_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// adrProps are the seven properties of a CBM 0.12.1 `ADR` node that the adapter keeps (O12).
var adrProps = []string{"adr_id", "title", "status", "status_text", "date", "deciders", "superseded_by"}

// infraADRGraph holds the two decisions and the `SUPERSEDES` edge of test/fixtures/cbm-recordings/infra.json.
func infraADRGraph() *testutil.FakeGraph {
	older := testutil.ADRNode("117", "docs/adr/0001-use-self-hosted-postgres.md", map[string]string{
		"adr_id": "ADR-1", "title": "ADR-0001: Use self-hosted Postgres", "status": "superseded",
		"status_text": "Superseded by ADR-0002", "date": "2026-01-10", "deciders": "Platform Team",
		"superseded_by": "0002-use-managed-postgres.md",
	})
	newer := testutil.ADRNode("111", "docs/adr/0002-use-managed-postgres.md", map[string]string{
		"adr_id": "ADR-2", "title": "ADR-0002: Use managed Postgres", "status": "accepted",
		"status_text": "Accepted", "date": "2026-03-02", "deciders": "Platform Team",
	})
	edge := testutil.NewEdge(&newer, &older, "SUPERSEDES", map[string]string{"via": "markdown", "syntax": "supersedes", "tier": "exact", "line": "7", "count": "1"})

	return testutil.NewFakeGraph("p", []testutil.FakeNode{older, newer}, []testutil.FakeEdge{edge})
}

func fetchDecisions(t *testing.T, g *testutil.FakeGraph) (observation.EvidenceQueryResult, observation.Capabilities) {
	t.Helper()

	client := observation.NewFakeClient(g.Handler, "")
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: g.Project})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"SUPERSEDES"})
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	return res, provider.Capabilities(t.Context())
}

func TestADRNode(t *testing.T) {
	node := testutil.ADRNode("5", "docs/adr/0005-x.md", map[string]string{"adr_id": "ADR-5"})

	if node.Label != "ADR" || node.QN != "docs.adr.0005-x.__adr__" || node.Path != "docs/adr/0005-x.md" || node.Line != 1 || node.Props["adr_id"] != "ADR-5" {
		t.Fatalf("node = %+v", node)
	}
}

func TestReplayedADRNodeCarriesAllSevenProperties(t *testing.T) {
	want := map[string]string{
		"adr_id": "ADR-7", "title": "ADR-0007: Managed", "status": "superseded", "status_text": "Superseded by ADR-0009",
		"date": "2026-04-01", "deciders": "Ada, Bob", "superseded_by": "0009-next.md",
	}
	node := testutil.ADRNode("1", "docs/adr/0007-managed.md", want)
	other := testutil.ADRNode("2", "docs/adr/0009-next.md", map[string]string{"adr_id": "ADR-9"})
	edge := testutil.NewEdge(&other, &node, "SUPERSEDES", map[string]string{"syntax": "supersedes", "via": "markdown"})

	res, _ := fetchDecisions(t, testutil.NewFakeGraph("p", []testutil.FakeNode{node, other}, []testutil.FakeEdge{edge}))

	got := res.Nodes["cbm:p:1"]
	if got.Label != "ADR" || got.FilePath != "docs/adr/0007-managed.md" {
		t.Fatalf("node = %+v", got)
	}

	for _, name := range adrProps {
		if got.Properties[name] != want[name] {
			t.Fatalf("property %s = %q, want %q", name, got.Properties[name], want[name])
		}
	}
}

func TestInfraDecisionsGiveALinkFromADR2ToADR1(t *testing.T) {
	res, caps := fetchDecisions(t, infraADRGraph())
	ev := adr.BuildEvidence(&res, caps, nil)

	if !ev.Supported || len(ev.Decisions) != 2 {
		t.Fatalf("evidence = %+v", ev)
	}

	labels := map[string]string{}
	for i := range ev.Decisions {
		labels[ev.Decisions[i].Path] = ev.Decisions[i].Label
	}

	want := []adr.SupersedesEdge{{From: "docs/adr/0002-use-managed-postgres.md", To: "docs/adr/0001-use-self-hosted-postgres.md", Line: 7}}
	if !slices.Equal(ev.Supersedes, want) {
		t.Fatalf("supersedes = %+v, want %+v", ev.Supersedes, want)
	}

	if labels[want[0].From] != "ADR-2" || labels[want[0].To] != "ADR-1" {
		t.Fatalf("labels = %v", labels)
	}
}
