package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
)

const indexCoverage = `{"project":"concordia-fixture-infra","files":{"covered":1,"total":1}}`

// Task 5.6: the bundle carries the `deploymentTrace` section in results/results.json and the coverage records in
// coverage/*.json, and `export verify` accepts it.
func TestBundleCarriesTheDeploymentTraceAndTheCoverageRecords(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	// The replay client has no recording of `index_repository`, so the coverage file that `index` writes is written here.
	if err := os.MkdirAll(filepath.Join(d.w.dir, "build", "concordia"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d.w.dir, "build", "concordia", "coverage.json"), []byte(indexCoverage), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"check"}, {"export", "bundle"}} {
		if res := d.cli(t, args...); res.code != 0 && args[0] != "check" {
			t.Fatalf("%s: exit code %d, stderr %q", strings.Join(args, " "), res.code, res.stderr)
		}
	}

	archive := filepath.Join(d.w.dir, "build", "concordia", "conformance.tar.gz")
	extract := t.TempDir()

	if _, err := bundle.Open(archive, extract); err != nil {
		t.Fatalf("open the bundle: %v", err)
	}

	var results struct {
		DeploymentTrace struct {
			Status       string `json:"status"`
			Environments []struct {
				Name     string `json:"name"`
				Subjects []struct {
					ArchitectureID string `json:"architectureId"`
				} `json:"subjects"`
			} `json:"environments"`
			Summary struct {
				Subjects int `json:"subjects"`
			} `json:"summary"`
		} `json:"deploymentTrace"`
	}

	if err := json.Unmarshal(mustRead(t, filepath.Join(extract, "results", "results.json")), &results); err != nil {
		t.Fatal(err)
	}

	section := results.DeploymentTrace
	if section.Status != "configured" || len(section.Environments) != 2 || section.Summary.Subjects != 4 {
		t.Errorf("deploymentTrace in the bundle = %+v", section)
	}

	// The coverage records of the run are in results/results.json; the coverage file of `index` is copied as it is.
	if !strings.Contains(string(mustRead(t, filepath.Join(extract, "results", "results.json"))), "deployment-workload-set") {
		t.Error("results/results.json has no deployment-workload-set coverage record")
	}

	got := mustRead(t, filepath.Join(extract, "coverage", "coverage.json"))
	if string(got) != indexCoverage {
		t.Errorf("coverage/coverage.json = %q, want %q", got, indexCoverage)
	}

	if verify := d.cli(t, "export", "verify", archive); verify.code != 0 {
		t.Errorf("export verify exit code %d, stderr %q", verify.code, verify.stderr)
	}

	if _, err := os.Stat(archive); err != nil {
		t.Error(err)
	}
}
