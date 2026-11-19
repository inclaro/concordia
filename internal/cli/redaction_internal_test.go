package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/redact"
)

// redactionExpectEqual reports an error when got differs from want.
func redactionExpectEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// redactionSecretEdge is an edge with property values that are secrets without the shape of a token and with one token.
func redactionSecretEdge() *observation.EvidenceEdge {
	return &observation.EvidenceEdge{
		ID: "e1", From: "cbm:p:1", To: "cbm:p:2", Kind: observation.KindCalls, NativeKind: "CALLS",
		Properties: map[string]string{
			"api_token": "hunter2-not-token-shaped",
			"note":      "uses ghp_CONCORDIASEEDshelltoken0000000000000 here",
			"via":       "env",
		},
		Facets: map[string]string{"family": "configured-connection"},
	}
}

// redactionEvidenceRun is a run whose evidence has the given edges and no nodes.
func redactionEvidenceRun(edges ...*observation.EvidenceEdge) *conformance.Run {
	return &conformance.Run{Evidence: &observation.EvidenceQueryResult{
		Nodes: map[string]observation.ObservedNode{},
		Edges: edges,
	}}
}

// redactionEdgeProperties decodes the properties of the only edge of the graph.
func redactionEdgeProperties(t *testing.T, data []byte) map[string]string {
	t.Helper()

	var doc struct {
		Edges []struct {
			Properties map[string]string `json:"properties"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Edges) != 1 {
		t.Fatalf("err = %v, graph: %s", err, data)
	}

	return doc.Edges[0].Properties
}

func TestGraphJSONRedactsEdgePropertyValuesAndRecordsThem(t *testing.T) {
	t.Parallel()

	edge := redactionSecretEdge()

	data, records, err := graphJSON(redactionEvidenceRun(edge))
	if err != nil {
		t.Fatal(err)
	}

	text := string(data)
	for _, secret := range []string{"hunter2", "CONCORDIASEED"} {
		if strings.Contains(text, secret) {
			t.Errorf("the graph has %q: %s", secret, text)
		}
	}

	props := redactionEdgeProperties(t, data)
	for key, want := range map[string]string{
		"api_token": "[REDACTED:secret-data]",
		"note":      "uses [REDACTED:token] here",
		"via":       "env",
	} {
		if props[key] != want {
			t.Errorf("properties = %v", props)
		}
	}

	if len(records) != 2 {
		t.Fatalf("records = %+v", records)
	}

	for _, r := range records {
		redactionExpectEqual(t, "record output", r.Output, "graph/evidence.json")
		redactionExpectEqual(t, "record file", r.File, "")
		redactionExpectEqual(t, "record line", r.Line, 0)

		if r.Property == "" {
			t.Errorf("record = %+v", r)
		}
	}

	if edge.Properties["api_token"] != "hunter2-not-token-shaped" {
		t.Error("the run was changed")
	}
}

func TestManifestRedactionsCountsAndSortsWithoutValues(t *testing.T) {
	t.Parallel()

	records := []redact.Record{
		{Output: "snippets/b.txt", File: "x.tfvars", Property: "db_password", Class: redact.ClassSecretData, Line: 4},
		{Output: "graph/evidence.json", Property: "api_token", Class: redact.ClassSecretData},
		{Output: "graph/evidence.json", Property: "api_token", Class: redact.ClassSecretData},
	}

	got := manifestRedactions(records)
	if len(got) != 2 {
		t.Fatalf("redactions = %+v", got)
	}

	redactionExpectEqual(t, "first path", got[0].Path, "graph/evidence.json")
	redactionExpectEqual(t, "first count", got[0].Count, 2)
	redactionExpectEqual(t, "second path", got[1].Path, "snippets/b.txt")
	redactionExpectEqual(t, "second line", got[1].Line, 4)
	redactionExpectEqual(t, "second count", got[1].Count, 1)

	if len(manifestRedactions(nil)) != 0 {
		t.Error("nil records give entries")
	}
}

type redactionDerivation struct {
	Rule    string   `json:"rule"`
	Inputs  []string `json:"inputs"`
	Version int      `json:"version"`
}

type redactionDerivedEdge struct {
	Derivation *redactionDerivation `json:"derivation"`
	ID         string               `json:"id"`
}

// redactionExpectDerivedFact checks the derivation record of the placement fact.
func redactionExpectDerivedFact(t *testing.T, d *redactionDerivation) {
	t.Helper()

	if d == nil {
		t.Fatalf("derived fact = %+v", d)
	}

	redactionExpectEqual(t, "rule", d.Rule, "placement-from-namespace")
	redactionExpectEqual(t, "version", d.Version, 1)

	if len(d.Inputs) != 1 || d.Inputs[0] != "cbm:p:1" {
		t.Errorf("derived fact = %+v", d)
	}
}

// A derived fact keeps its derivation record in the evidence export; a native fact has none.
func TestGraphJSONKeepsTheDerivationRecordOfADerivedFact(t *testing.T) {
	t.Parallel()

	derived := &observation.EvidenceEdge{
		ID: "e1", From: "cbm:p:1", To: "synth:namespace:production/payments", Kind: observation.KindPlacedIn,
		Properties: map[string]string{}, Environment: "production",
		Derivation: &observation.Derivation{Rule: observation.PlacementRule, Version: observation.PlacementRuleVersion, Inputs: []string{"cbm:p:1"}},
	}
	native := &observation.EvidenceEdge{ID: "e2", From: "cbm:p:1", To: "cbm:p:2", Kind: observation.KindCalls, NativeKind: "CALLS", Properties: map[string]string{}}

	data, _, err := graphJSON(redactionEvidenceRun(derived, native))
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Edges []redactionDerivedEdge `json:"edges"`
	}

	if err = json.Unmarshal(data, &doc); err != nil || len(doc.Edges) != 2 {
		t.Fatalf("err = %v, graph: %s", err, data)
	}

	for _, e := range doc.Edges {
		if e.ID == "e1" {
			redactionExpectDerivedFact(t, e.Derivation)

			continue
		}

		if e.Derivation != nil {
			t.Errorf("native fact has a derivation: %+v", e.Derivation)
		}
	}
}
