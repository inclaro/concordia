package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/trust"
)

// trustExplainDir is the sample results file with crossings for the flow of the CO-BND-001 findings.
func trustExplainDir(t *testing.T, withTrust bool) string {
	t.Helper()

	run := testutil.SampleRun()

	if withTrust {
		var finding *conformance.Finding

		for i := range run.Findings {
			if run.Findings[i].RuleID == "CO-BND-001" {
				finding = &run.Findings[i]

				break
			}
		}

		vocab := trust.NewVocabulary(nil, nil)
		crossings := trust.Crossings(vocab, &trust.Flow{
			ID: "billing-to-fulfillment", Kind: trust.FlowRelationship, Classification: "restricted",
			Source:      trust.Endpoint{ID: finding.Source.ArchitectureID, Trust: trust.Declaration{Dimensions: map[string]string{"zone": "internet", "account": "a1"}}},
			Destination: trust.Endpoint{ID: finding.Target.ArchitectureID, Trust: trust.Declaration{Dimensions: map[string]string{"zone": "internal", "account": "a1"}}},
		})
		crossings = append(crossings, trust.Crossings(vocab, &trust.Flow{
			ID: "unrelated", Source: trust.Endpoint{ID: "x"}, Destination: trust.Endpoint{ID: "y"},
		})...)
		trust.Sort(crossings)
		run.Trust = &conformance.TrustResult{Crossings: crossings}
	}

	text, err := canonjson.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": string(text)})

	return dir
}

func TestExplainShowsTheCrossingsOfTheFlowOfAFinding(t *testing.T) {
	t.Parallel()

	res := explain(t, trustExplainDir(t, true), "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive)
	if res.Code != 0 {
		t.Fatalf("exit code = %d\n%s", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout,
		"Trust crossings: billing-to-fulfillment (relationship), data classification restricted",
		"zone: internet → internal (crossing)",
		"account: a1 → a1 (same)",
		"region: unknown → unknown (unknown)",
	)

	if strings.Contains(res.Stdout, "unrelated") {
		t.Errorf("a flow that the finding does not involve is shown:\n%s", res.Stdout)
	}
}

func TestExplainWithoutTrustShowsNoCrossings(t *testing.T) {
	t.Parallel()

	res := explain(t, trustExplainDir(t, false), "CO-BND-001", "--results", "results.json")
	if strings.Contains(res.Stdout, "Trust crossings") {
		t.Errorf("output shows crossings without a trust section:\n%s", res.Stdout)
	}

	js := explain(t, trustExplainDir(t, false), "CO-BND-001", "--results", "results.json", "--format", "json")

	var doc map[string]any
	if err := json.Unmarshal([]byte(js.Stdout), &doc); err != nil {
		t.Fatal(err)
	}

	if _, present := doc["trust"]; present {
		t.Error("explain JSON has a trust key without crossings")
	}
}

func TestExplainJSONHasTheCrossingsOfTheSelectedFindings(t *testing.T) {
	t.Parallel()

	res := explain(t, trustExplainDir(t, true), "CO-BND-001", "--results", "results.json", "--format", "json")

	var doc struct {
		SchemaVersion string `json:"schemaVersion"`
		Trust         struct {
			Crossings []trust.Crossing `json:"crossings"`
		} `json:"trust"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, res.Stdout)
	}

	if doc.SchemaVersion != "1.1" || len(doc.Trust.Crossings) != 5 {
		t.Fatalf("doc = %+v", doc)
	}

	for _, c := range doc.Trust.Crossings {
		if c.Flow != "billing-to-fulfillment" {
			t.Errorf("crossing = %+v", c)
		}
	}
}
