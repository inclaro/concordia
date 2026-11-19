package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

const (
	traceSecret   = "CONCORDIA-SEED-env-value-5d1e"
	traceManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  containers:
    - name: app
      image: registry.example.com/payments/api:1.2.0
      env:
        - name: DB_PASSWORD
          value: ` + traceSecret + `
        - name: MODE
          value: fast
`
)

// traceSubject is the trace of orders-service in an environment with a workload at the given location.
func traceSubject(env string, workload deployment.Location) deployment.SubjectTrace {
	return deployment.SubjectTrace{
		ArchitectureID: "orders-service",
		Environment:    env,
		Level:          deployment.LevelDeclared,
		Status:         deployment.StatusPartial,
		Steps: []deployment.StepTrace{
			{
				Step: deployment.StepService, Role: observation.RoleDeclaredIntent, Resolution: observation.ResolutionExact,
				Entities: []deployment.Entity{{Kind: deployment.KindService, Identity: "orders-service", Locations: []deployment.Location{}}},
			},
			{
				Step: deployment.StepWorkload, Role: observation.RoleParsedSourceFact, Resolution: observation.ResolutionExplicitlyBound,
				Entities: []deployment.Entity{{Kind: deployment.KindWorkload, Identity: env + "/Deployment/api", Locations: []deployment.Location{workload}}},
			},
			{
				Step: deployment.StepBuildDefinition, Role: observation.RoleInferredAssociation, Resolution: observation.ResolutionUnresolved,
				Gaps: []deployment.Gap{{Reason: deployment.ReasonCoverageIncomplete, Detail: "no build command names this image", LossModes: []string{"build-link-needs-build-command"}}},
			},
		},
		Rules: []deployment.RuleLink{{RuleID: "INFRA-TRC-001", ObligationID: "ob-1", Verdict: "pass"}},
	}
}

// traceDir is a directory with a results file that holds a trace of orders-service in production and staging and
// the manifest with the secret. The workload of production is at line 11, the line of the secret value.
func traceDir(t *testing.T, mutate func(*deployment.Trace)) string {
	t.Helper()

	run := testutil.SampleRun()
	run.DeploymentTrace = deployment.Trace{
		Status: deployment.StatusConfigured,
		Levels: deployment.Levels(),
		Environments: []deployment.EnvironmentTrace{
			{Name: "production", Cluster: "prod-eu", Coverage: deployment.CoverageState{Status: observation.CoverageComplete}, Subjects: []deployment.SubjectTrace{traceSubject("production", deployment.Location{Path: "k8s/prod/deployment.yaml", Line: 11})}},
			{Name: "staging", Coverage: deployment.CoverageState{Status: observation.CoveragePartial}, Subjects: []deployment.SubjectTrace{traceSubject("staging", deployment.Location{Path: "k8s/staging/deployment.yaml", Line: 2})}},
		},
		Summary: deployment.Summary{ByStatus: map[deployment.Status]int{deployment.StatusPartial: 2}, Subjects: 2, Environments: 2},
	}

	if mutate != nil {
		mutate(&run.DeploymentTrace)
	}

	text, err := canonjson.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": string(text), "k8s/prod/deployment.yaml": traceManifest})

	return dir
}

func trace(t *testing.T, dir string, args ...string) result {
	t.Helper()

	return runIn(t, dir, append([]string{"trace"}, args...)...)
}

func TestTraceShowsTheFiveStepsOfAService(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json", "--environment", "production")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout,
		"Deployment trace: orders-service in production (cluster prod-eu)",
		"Evidence: declared configuration only. Not traced: deployment execution and runtime.",
		"1. service [declared intent, exact]",
		"2. workload [parsed source fact, explicitly bound]",
		"at k8s/prod/deployment.yaml:11",
		"gap: no build command names this image (build-link-needs-build-command)",
		"INFRA-TRC-001 pass",
	)

	if strings.Contains(res.Stdout, "staging") {
		t.Errorf("output has the environment that was not selected:\n%s", res.Stdout)
	}

	if strings.Contains(res.Stdout, traceSecret) || strings.Contains(res.Stdout, "| ") {
		t.Errorf("output shows source lines without --source:\n%s", res.Stdout)
	}
}

func TestTraceShowsEveryEnvironmentByDefault(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json")
	wantContains(t, res.Stdout, "orders-service in production", "orders-service in staging")
}

func TestTraceRepeatsTheEnvironmentOption(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json", "--environment", "production", "--environment", "staging")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout, "orders-service in production", "orders-service in staging")
}

func TestTraceRejectsAnUnknownNode(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "ghost-service", "--results", "results.json")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, want 2", res.Code)
	}

	wantContains(t, res.Stderr, "CO-WRK-010", "ghost-service")
}

func TestTraceRejectsAnUndeclaredEnvironment(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json", "--environment", "qa")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, want 2", res.Code)
	}

	wantContains(t, res.Stderr, "CO-WRK-017", "qa")
}

func TestTraceWithNoEnvironmentDeclaredExitsWithTheEnvironmentCode(t *testing.T) {
	t.Parallel()

	dir := traceDir(t, func(tr *deployment.Trace) {
		*tr = deployment.Trace{Status: deployment.StatusNotConfigured, Levels: deployment.Levels(), Environments: []deployment.EnvironmentTrace{}}
	})

	res := trace(t, dir, "orders-service", "--results", "results.json")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, want 2", res.Code)
	}

	wantContains(t, res.Stderr, "CO-WRK-017", "environment-specific assurance needs a declared environment")
}

// traceJSONSummary is the part of the JSON trace document that the tests compare.
type traceJSONSummary struct {
	architectureID string
	environment    string
	level          string
	cluster        string
	traces         int
	steps          int
}

// decodeTraceJSON summarizes the JSON output of concordia trace: the document and its first trace.
func decodeTraceJSON(t *testing.T, stdout string) traceJSONSummary {
	t.Helper()

	var doc struct {
		ArchitectureID string `json:"architectureId"`
		Traces         []struct {
			Environment string `json:"environment"`
			Level       string `json:"level"`
			Cluster     string `json:"cluster"`
			Steps       []any  `json:"steps"`
		} `json:"traces"`
	}

	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}

	summary := traceJSONSummary{architectureID: doc.ArchitectureID, traces: len(doc.Traces)}
	if len(doc.Traces) > 0 {
		first := doc.Traces[0]
		summary.environment, summary.level, summary.cluster, summary.steps = first.Environment, first.Level, first.Cluster, len(first.Steps)
	}

	return summary
}

func TestTraceWritesJSON(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json", "--environment", "production", "--format", "json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	want := traceJSONSummary{architectureID: "orders-service", traces: 1, environment: "production", level: "declared", cluster: "prod-eu", steps: 3}
	if got := decodeTraceJSON(t, res.Stdout); got != want {
		t.Errorf("document = %+v, want %+v", got, want)
	}
}

func TestTraceWorksOffline(t *testing.T) {
	t.Parallel()

	res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json", "--offline")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout, "orders-service in production")
}

func TestTraceSourceRedactsASecretValue(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"pretty", "json"} {
		res := trace(t, traceDir(t, nil), "orders-service", "--results", "results.json", "--environment", "production", "--source", "--format", format)
		if res.Code != 0 {
			t.Fatalf("%s: exit code = %d, stderr = %q", format, res.Code, res.Stderr)
		}

		if strings.Contains(res.Stdout, traceSecret) {
			t.Errorf("%s: output shows the secret value:\n%s", format, res.Stdout)
		}

		wantContains(t, res.Stdout, "[REDACTED", "value: fast")
		wantContains(t, res.Stderr, "CO-REP-005")
	}
}

func TestTraceSourceDoesNotReadOutsideTheWorkspace(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "ws")

	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(parent, "outside.yaml"), []byte("a: 1\nb: OUTSIDE-CONTENT\nc: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := traceDir(t, func(tr *deployment.Trace) {
		tr.Environments[0].Subjects[0].Steps[1].Entities[0].Locations = []deployment.Location{{Path: "../outside.yaml", Line: 2}}
	})

	text, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(filepath.Join(root, "results.json"), text, 0o600); err != nil {
		t.Fatal(err)
	}

	res := trace(t, root, "orders-service", "--results", "results.json", "--environment", "production", "--source")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if strings.Contains(res.Stdout, "OUTSIDE-CONTENT") {
		t.Errorf("output shows a file outside the workspace:\n%s", res.Stdout)
	}

	wantContains(t, res.Stdout, "at ../outside.yaml:2")
	wantContains(t, res.Stderr, "CO-REP-006")
}
