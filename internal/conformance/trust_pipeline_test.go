package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
)

// trustArch is the demo architecture with trust values on the two connected nodes and a
// classification on their relationship. dims overrides the billing node's dimensions.
func trustArch(t *testing.T, billingDims map[string]any) string {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(archText(nil)), &doc); err != nil {
		t.Fatal(err)
	}

	nodes, _ := doc["nodes"].([]any)

	billing, _ := nodes[0].(map[string]any)
	ext, _ := billing["concordia"].(map[string]any)
	ext["trust"] = map[string]any{"dimensions": billingDims}

	fulfillment, _ := nodes[1].(map[string]any)
	fulfillment["concordia"] = map[string]any{"trust": map[string]any{"dimensions": map[string]any{"zone": "internal", "account": "a2"}}}

	rels, _ := doc["relationships"].([]any)
	rel, _ := rels[0].(map[string]any)
	relExt, _ := rel["concordia"].(map[string]any)
	relExt["trust"] = map[string]any{"classification": "restricted"}

	return mustJSON(doc)
}

const trustWorkspaceText = "trust: {classifications: [public, internal, restricted]}\n"

func trustSetup(t *testing.T, dims map[string]any) *setup {
	t.Helper()

	return newSetup(t, map[string]string{"architecture/a.calm.json": trustArch(t, dims)}, &setupOpts{ws: wsText(trustWorkspaceText)})
}

func TestRunWithoutTrustHasNoTrustKey(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	if run.Trust != nil {
		t.Fatalf("trust = %+v", run.Trust)
	}

	var tree map[string]any
	if err := json.Unmarshal(canonicalOf(t, run), &tree); err != nil {
		t.Fatal(err)
	}

	if _, present := tree["trust"]; present {
		t.Fatal("results.json has a trust key although the workspace declares no trust")
	}
}

func TestCrossingsInResults(t *testing.T) {
	t.Parallel()

	s := trustSetup(t, map[string]any{"zone": "internet", "account": "a1"})
	run := s.mustCheck(t, nil)
	text := canonicalOf(t, run)

	if errs := conformance.ValidateResultJSON(text); len(errs) > 0 {
		t.Fatalf("results.json does not validate: %v", errs)
	}

	var tree struct {
		SchemaVersion string `json:"schemaVersion"`
		Trust         struct {
			Crossings []map[string]any `json:"crossings"`
		} `json:"trust"`
	}
	if err := json.Unmarshal(text, &tree); err != nil {
		t.Fatal(err)
	}

	// Scenario: Crossings in results (the schema version stays 1.1).
	if tree.SchemaVersion != "1.1" {
		t.Errorf("schemaVersion = %s", tree.SchemaVersion)
	}

	byDimension := map[string]map[string]any{}

	for _, c := range tree.Trust.Crossings {
		if dimension, _ := c["dimension"].(string); c["flow"] == "billing-to-fulfillment" {
			byDimension[dimension] = c
		}
	}

	if len(byDimension) != 5 {
		t.Fatalf("crossings = %+v", tree.Trust.Crossings)
	}

	zone := byDimension["zone"]
	if zone["state"] != "crossing" || zone["from"] != "internet" || zone["to"] != "internal" || zone["direction"] != "internet → internal" || zone["classification"] != "restricted" {
		t.Errorf("zone = %+v", zone)
	}

	if byDimension["account"]["state"] != "crossing" || byDimension["region"]["state"] != "unknown" {
		t.Errorf("account = %+v, region = %+v", byDimension["account"], byDimension["region"])
	}

	// Sorted by dimension within the flow.
	if tree.Trust.Crossings[0]["dimension"] != "account" {
		t.Errorf("first crossing = %+v", tree.Trust.Crossings[0])
	}
}

func TestCrossingsAreStable(t *testing.T) {
	t.Parallel()

	s := trustSetup(t, map[string]any{"zone": "internet"})
	first := s.mustCheck(t, nil)
	second := s.mustCheck(t, nil)

	a, errA := json.Marshal(first.Trust)
	b, errB := json.Marshal(second.Trust)

	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}

	if !bytes.Equal(a, b) || first.Summary.ResultDigest != second.Summary.ResultDigest {
		t.Fatalf("crossings differ between runs:\n%s\n%s", a, b)
	}
}

func TestUndeclaredDimensionFailsTheCheckWithExit2(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 undeclared dimension name.
	de := trustSetup(t, map[string]any{"firewall": "x"}).failureOf(t, nil)
	if de.Diagnostic.Code != diag.SEC001 || de.ExitCode != diag.InvalidInput {
		t.Fatalf("error = %+v", de)
	}
}

func TestCheckDoesNotWriteTheModelOrBindingFiles(t *testing.T) {
	t.Parallel()

	s := trustSetup(t, map[string]any{"zone": "internet"})
	files := []string{"architecture/a.calm.json", "bindings/concordia.bindings.yaml", "concordia.yaml"}

	sum := func() map[string]string {
		out := map[string]string{}

		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(s.dir, f))
			if err != nil {
				t.Fatal(err)
			}

			out[f] = digest.OfBytes(data)
		}

		return out
	}

	before := sum()

	s.mustCheck(t, nil)

	// Scenario: Check does not write the model.
	for f, h := range sum() {
		if before[f] != h {
			t.Errorf("%s changed during check", f)
		}
	}
}
