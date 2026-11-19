package statement_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/statement"
)

func deploymentRecord() map[string]any {
	return map[string]any{
		"environment": "production",
		"cluster":     "prod-eu-1",
		"workload":    map[string]any{"namespace": "shop", "kind": "Deployment", "name": "api"},
		"container":   "api",
		"image":       "registry.example/shop@sha256:aaa",
		"digest":      "sha256:aaa",
		"outcome":     "succeeded",
		"time":        "2026-10-30T12:00:00Z",
		"deployer":    "ci-deploy",
	}
}

func workloadObservation() map[string]any {
	return map[string]any{
		"environment": "production",
		"context":     "prod-eu-1",
		"workload":    map[string]any{"namespace": "shop", "kind": "Deployment", "name": "api"},
		"container":   "api",
		"digest":      "sha256:aaa",
		"time":        "2026-10-31T11:00:00Z",
	}
}

// without copies the predicate and removes the field at the dotted path.
func without(predicate map[string]any, path string) map[string]any {
	out := maps.Clone(predicate)

	head, rest, nested := cutPath(path)
	if !nested {
		delete(out, head)

		return out
	}

	nestedFields, _ := out[head].(map[string]any)
	child := maps.Clone(nestedFields)
	delete(child, rest)
	out[head] = child

	return out
}

func cutPath(path string) (head, rest string, nested bool) {
	for i := range len(path) {
		if path[i] == '.' {
			return path[:i], path[i+1:], true
		}
	}

	return path, "", false
}

// with copies the predicate and sets one top-level field.
func with(predicate map[string]any, key string, value any) map[string]any {
	out := maps.Clone(predicate)
	out[key] = value

	return out
}

// mustParseDeployment reads a deployment record predicate and stops the test on an error.
func mustParseDeployment(t *testing.T, predicate map[string]any) statement.DeploymentRecord {
	t.Helper()

	loaded := mustParse(t, "r.json", statementJSON(t, statement.PredicateDeploymentRecord, predicate))

	got, err := statement.ParseDeploymentRecord(&loaded.Statement)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// mustParseObservation reads a workload observation predicate and stops the test on an error.
func mustParseObservation(t *testing.T, predicate map[string]any) statement.WorkloadObservation {
	t.Helper()

	loaded := mustParse(t, "o.json", statementJSON(t, statement.PredicateWorkloadObservation, predicate))

	got, err := statement.ParseWorkloadObservation(&loaded.Statement)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// mustParseMeasurement reads a measurement predicate and stops the test on an error.
func mustParseMeasurement(t *testing.T, predicate map[string]any) statement.Measurement {
	t.Helper()

	loaded := mustParse(t, "m.json", statementJSON(t, statement.PredicateMeasurement, predicate))

	got, err := statement.ParseMeasurement(&loaded.Statement)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// mustParsePlan reads a plan predicate and stops the test on an error.
func mustParsePlan(t *testing.T, predicate map[string]any) statement.Plan {
	t.Helper()

	loaded := mustParse(t, "p.json", statementJSON(t, statement.PredicatePlan, predicate))

	got, err := statement.ParsePlan(&loaded.Statement)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestParseDeploymentRecordListsMissingFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		predicate map[string]any
		missing   []string
	}{
		{"complete", deploymentRecord(), nil},
		{"no environment", without(deploymentRecord(), "environment"), []string{"environment"}},
		{"no cluster and no context", without(deploymentRecord(), "cluster"), []string{"cluster"}},
		{"no workload name", without(deploymentRecord(), "workload.name"), []string{"workload.name"}},
		{"no container", without(deploymentRecord(), "container"), []string{"container"}},
		{"no digest", without(deploymentRecord(), "digest"), []string{"digest"}},
		{"digest is a tag", with(deploymentRecord(), "digest", "1.4"), []string{"digest"}},
		{"no outcome", without(deploymentRecord(), "outcome"), []string{"outcome"}},
		{"no time", without(deploymentRecord(), "time"), []string{"time"}},
		{"empty deployer", with(deploymentRecord(), "deployer", ""), []string{"deployer"}},
		{"several", without(without(deploymentRecord(), "deployer"), "environment"), []string{"environment", "deployer"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expectDeepEqual(t, "missing", mustParseDeployment(t, tt.predicate).Missing, tt.missing)
		})
	}
}

func TestParseDeploymentRecordReadsTheFields(t *testing.T) {
	t.Parallel()

	got := mustParseDeployment(t, deploymentRecord())

	expectEqual(t, "environment", got.Environment, "production")
	expectEqual(t, "cluster", got.Cluster, "prod-eu-1")
	expectEqual(t, "workload name", got.Workload.Name, "api")
	expectEqual(t, "workload namespace", got.Workload.Namespace, "shop")
	expectEqual(t, "workload kind", got.Workload.Kind, "Deployment")
	expectEqual(t, "container", got.Container, "api")
	expectEqual(t, "digest", got.Digest, "sha256:aaa")
	expectEqual(t, "outcome", got.Outcome, statement.DeploymentSucceeded)
	expectEqual(t, "deployer", got.Deployer, "ci-deploy")
	expectEqual(t, "time is zero", got.Time.IsZero(), false)
	expectEqual(t, "invalid outcome", got.InvalidOutcome(), false)
}

func TestDeploymentOutcomes(t *testing.T) {
	t.Parallel()

	for outcome, invalid := range map[string]bool{"succeeded": false, "failed": false, "rolled-back": true, "SUCCEEDED": true, "": false} {
		got := mustParseDeployment(t, with(deploymentRecord(), "outcome", outcome))

		expectEqual(t, "invalid outcome of "+outcome, got.InvalidOutcome(), invalid)
	}
}

func TestParseWorkloadObservationListsMissingFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		predicate map[string]any
		missing   []string
	}{
		{"complete with a context", workloadObservation(), nil},
		{"no environment", without(workloadObservation(), "environment"), []string{"environment"}},
		{"no cluster and no context", without(workloadObservation(), "context"), []string{"cluster"}},
		{"no namespace", without(workloadObservation(), "workload.namespace"), []string{"workload.namespace"}},
		{"no kind", without(workloadObservation(), "workload.kind"), []string{"workload.kind"}},
		{"no container", without(workloadObservation(), "container"), []string{"container"}},
		{"no digest", without(workloadObservation(), "digest"), []string{"digest"}},
		{"no time", without(workloadObservation(), "time"), []string{"time"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expectDeepEqual(t, "missing", mustParseObservation(t, tt.predicate).Missing, tt.missing)
		})
	}
}

func TestPredicateReadersRejectWrongTypesAndShapes(t *testing.T) {
	t.Parallel()

	record := mustParse(t, "r.json", statementJSON(t, statement.PredicateDeploymentRecord, deploymentRecord()))
	badTime := deploymentRecord()
	badTime["time"] = "yesterday"
	badShape := deploymentRecord()
	badShape["workload"] = "api"

	if _, err := statement.ParseWorkloadObservation(&record.Statement); err == nil {
		t.Error("a deployment record is not a workload observation")
	}

	for name, predicate := range map[string]map[string]any{"time that is not a time": badTime, "workload that is not an object": badShape} {
		loaded := mustParse(t, "r.json", statementJSON(t, statement.PredicateDeploymentRecord, predicate))
		if _, err := statement.ParseDeploymentRecord(&loaded.Statement); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	notObject := mustParse(t, "p.json", statementJSON(t, statement.PredicatePlan, []string{"x"}))
	if _, err := statement.ParsePlan(&notObject.Statement); err == nil {
		t.Error("a predicate that is not an object must be an error")
	}
}

func measurement() map[string]any {
	return map[string]any{
		"scenario": "failover", "metric": "failover-seconds", "value": 42, "unit": "s", "threshold": 30, "comparison": "lte",
		"testConditions": "3 nodes, 1000 rps", "revision": "abc", "environment": "production",
		"evaluator": map[string]any{"name": "k6", "version": "1.0"}, "outcome": "passed",
		"window": map[string]any{"start": "2026-10-30T10:00:00Z", "end": "2026-10-30T11:00:00Z"},
	}
}

func TestParseMeasurement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		predicate map[string]any
		missing   []string
		failedRun bool
		invalid   bool
	}{
		{"complete", measurement(), nil, false, false},
		{"a value of zero is present", with(measurement(), "value", 0), nil, false, false},
		{"no test conditions", without(measurement(), "testConditions"), []string{"testConditions"}, false, false},
		{"no value", without(measurement(), "value"), []string{"value"}, false, false},
		{"no evaluator version", without(measurement(), "evaluator.version"), []string{"evaluator.version"}, false, false},
		{"no window end", without(measurement(), "window.end"), []string{"window.end"}, false, false},
		{"failed run", with(measurement(), "outcome", "failed"), nil, true, false},
		{"errored run", with(measurement(), "outcome", "errored"), nil, true, false},
		{"invalid outcome", with(measurement(), "outcome", "weird"), nil, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mustParseMeasurement(t, tt.predicate)

			expectDeepEqual(t, "missing", got.Missing, tt.missing)
			expectEqual(t, "failed run", got.FailedRun(), tt.failedRun)
			expectEqual(t, "invalid outcome", got.InvalidOutcome(), tt.invalid)
			expectEqual(t, "time is zero", got.Time().IsZero(), slices.Contains(tt.missing, "window.end"))
		})
	}
}

func TestParsePlan(t *testing.T) {
	t.Parallel()

	full := map[string]any{"environment": "production", "revision": "abc", "tool": "terraform", "time": "2026-10-30T10:00:00Z"}

	for name, tt := range map[string]struct {
		predicate map[string]any
		missing   []string
	}{
		"complete":    {full, nil},
		"no revision": {without(full, "revision"), []string{"revision"}},
		"nothing":     {map[string]any{}, []string{"environment", "revision", "time"}},
	} {
		expectDeepEqual(t, name+": missing", mustParsePlan(t, tt.predicate).Missing, tt.missing)
	}
}
