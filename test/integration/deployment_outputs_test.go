package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/reporting"
)

const sarifSchemaFile = "../schemas/sarif-schema-2.1.0.json"

func sarifSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()

	reg := jsonschema.NewRegistry()

	id, err := reg.Add(mustRead(t, sarifSchemaFile))
	if err != nil {
		t.Fatal(err)
	}

	schema, err := reg.Compile(id)
	if err != nil {
		t.Fatal(err)
	}

	return schema
}

// sarifResultOf is the part of a SARIF result that the test reads.
type sarifResultOf struct {
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	RuleID              string            `json:"ruleId"`
	Locations           []struct {
		PhysicalLocation struct {
			Region *struct {
				StartLine int `json:"startLine"`
			} `json:"region"`
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

// stagingPlacementFinding gives the last INFRA-PLC-001 finding of the worker service in staging, or nil.
func stagingPlacementFinding(findings []conformance.Finding) *conformance.Finding {
	var placement *conformance.Finding

	for i := range findings {
		if isStagingWorkerPlacement(&findings[i]) {
			placement = &findings[i]
		}
	}

	return placement
}

func isStagingWorkerPlacement(f *conformance.Finding) bool {
	if f.RuleID != "INFRA-PLC-001" {
		return false
	}

	return f.Subject == "worker-service" && f.Environment == "staging"
}

// pointsToStagingWorkerManifest reports whether the finding has a line in the staging worker manifest.
func pointsToStagingWorkerManifest(f *conformance.Finding) bool {
	if f == nil || f.Location == nil {
		return false
	}

	return f.Location.Path == "k8s/staging/worker.yaml" && f.Location.Line >= 1
}

// sarifPointsTo reports whether the SARIF result is the placement rule at the line of the finding.
func sarifPointsTo(r *sarifResultOf, placement *conformance.Finding) bool {
	if r.RuleID != "INFRA-PLC-001" {
		return false
	}

	loc := r.Locations[0].PhysicalLocation
	if loc.ArtifactLocation.URI != placement.Location.Path || loc.Region == nil {
		return false
	}

	return loc.Region.StartLine == placement.Location.Line
}

func checkPlacementInSARIF(t *testing.T, run *conformance.Run, placement *conformance.Finding, want string) {
	t.Helper()

	out, err := reporting.RenderSARIF(run)
	if err != nil {
		t.Fatal(err)
	}

	if errs := sarifSchema(t).ValidateJSON(out); len(errs) > 0 {
		t.Fatalf("SARIF schema errors: %v", errs)
	}

	var doc struct {
		Runs []struct {
			Results []sarifResultOf `json:"results"`
		} `json:"runs"`
	}

	if err = json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	found := false

	for _, r := range doc.Runs[0].Results {
		if r.PartialFingerprints["concordia/v1"] != placement.Fingerprint {
			continue
		}

		found = true

		if !sarifPointsTo(&r, placement) {
			t.Errorf("SARIF result = %+v, want %s", r, want)
		}
	}

	if !found {
		t.Errorf("SARIF has no result for the placement finding %s", placement.Fingerprint)
	}
}

func checkPlacementInJUnit(t *testing.T, run *conformance.Run, want string) {
	t.Helper()

	xml := reporting.RenderJUnit(run)
	if !strings.Contains(xml, "INFRA-PLC-001") || !strings.Contains(xml, want) {
		t.Errorf("JUnit output lacks INFRA-PLC-001 or %s", want)
	}
}

func checkPlacementInPretty(t *testing.T, run *conformance.Run, want string) {
	t.Helper()

	text := reporting.RenderPretty(run, reporting.PrettyOptions{})
	for _, wantText := range []string{"INFRA-PLC-001", want, "Deployment trace:", "Subjects: 4 in 2 environment(s)"} {
		if !strings.Contains(text, wantText) {
			t.Errorf("pretty output lacks %q:\n%s", wantText, text)
		}
	}
}

// Task 5.4 (OUT-01): the findings of the `infrastructure` rules reach SARIF, JUnit, and the pretty output through the
// typed finding path, with the manifest file and line of the fact.
func TestInfrastructureFindingsPointToTheManifestInEveryFormat(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	placement := stagingPlacementFinding(d.run.Findings)
	if !pointsToStagingWorkerManifest(placement) {
		t.Fatalf("placement finding = %+v, want a location in k8s/staging/worker.yaml with a line", placement)
	}

	want := fmt.Sprintf("%s:%d", placement.Location.Path, placement.Location.Line)

	t.Run("SARIF", func(t *testing.T) {
		t.Parallel()
		checkPlacementInSARIF(t, d.run, placement, want)
	})

	t.Run("JUnit", func(t *testing.T) {
		t.Parallel()
		checkPlacementInJUnit(t, d.run, want)
	})

	t.Run("pretty", func(t *testing.T) {
		t.Parallel()
		checkPlacementInPretty(t, d.run, want)
	})
}
