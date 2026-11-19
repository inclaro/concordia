package observation_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

func crossFixture() *observation.EvidenceQueryResult {
	nodes := map[string]observation.ObservedNode{
		"cbm:app:1":  {ObservedID: "cbm:app:1", Label: "Function", QualifiedName: "src.a.call", FilePath: "src/a.ts"},
		"cbm:app:2":  {ObservedID: "cbm:app:2", Label: "Route", QualifiedName: "pay.src.handler", FilePath: "src/h.ts"},
		"cbm:app:3":  {ObservedID: "cbm:app:3", Label: "Route", QualifiedName: "pay.src.unknown", FilePath: "src/h.ts"},
		"cbm:app:4":  {ObservedID: "cbm:app:4", Label: "Route", QualifiedName: "pay.nothing", FilePath: "src/zzz.ts"},
		"cbm:app:5":  {ObservedID: "cbm:app:5", Label: "Function", QualifiedName: "other.thing"},
		"cbm:pay:10": {ObservedID: "cbm:pay:10", Label: "Function", QualifiedName: "src.handler", FilePath: "@pay/src/h.ts"},
		"cbm:pay:11": {ObservedID: "cbm:pay:11", Label: "File", QualifiedName: "src.h.__file__", FilePath: "@pay/src/h.ts"},
	}

	edge := func(id, to string) *observation.EvidenceEdge {
		return &observation.EvidenceEdge{ID: id, From: "cbm:app:1", To: to, NativeKind: "CROSS_HTTP_CALLS"}
	}

	return &observation.EvidenceQueryResult{
		Nodes: nodes,
		Edges: []*observation.EvidenceEdge{
			edge("qn", "cbm:app:2"), edge("file", "cbm:app:3"), edge("drop", "cbm:app:4"), edge("keep", "cbm:app:5"),
			{ID: "plain", From: "cbm:app:1", To: "cbm:app:5", NativeKind: "IMPORTS"},
		},
	}
}

func TestResolveCrossProject(t *testing.T) {
	t.Parallel()

	res := crossFixture()

	if dropped := observation.ResolveCrossProject(res, []string{"app", "pay"}); dropped != 1 {
		t.Fatalf("dropped = %d", dropped)
	}

	got := map[string]string{}
	for _, e := range res.Edges {
		got[e.ID] = e.To
	}

	want := map[string]string{"qn": "cbm:pay:10", "file": "cbm:pay:11", "keep": "cbm:app:5", "plain": "cbm:app:5"}
	if len(got) != len(want) {
		t.Fatalf("edges = %v", got)
	}

	for id, to := range want {
		if got[id] != to {
			t.Errorf("edge %s -> %s, want %s", id, got[id], to)
		}
	}
}
