package evidence_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// recordsFixture writes the provenance and record files and gives the configuration that declares them.
func recordsFixture(t *testing.T, root string) *workspace.Assurance {
	t.Helper()

	for _, name := range []string{"shop.provenance.sigstore.json", "malformed.provenance.json"} {
		copyAs(t, root, "provenance/"+name, "build/attestations/"+name)
	}

	// The fake cosign refuses this one by name, as it would a statement changed after signing.
	copyAs(t, root, "provenance/modified.provenance.sigstore.json", "build/attestations/refuse.provenance.sigstore.json")

	workload := map[string]string{"namespace": "shop", "kind": "Deployment", "name": "api"}
	sameDigest := "sha256:" + strings.Repeat("a", 64)
	deployment := map[string]any{
		"environment": "production", "cluster": "prod-cluster", "workload": workload,
		"container": "api", "digest": sameDigest, "outcome": "succeeded",
		"time": now.Add(-time.Hour).Format(time.RFC3339), "deployer": "ci",
	}
	observation := map[string]any{
		"environment": "production", "cluster": "prod-cluster", "workload": workload,
		"container": "api", "digest": sameDigest, "time": now.Add(-time.Minute).Format(time.RFC3339),
	}

	writeFile(t, root, "build/records/deploy-1.json", bundle(t, bareStatement(t, statement.PredicateDeploymentRecord, deployment, strings.Repeat("a", 64))))
	writeFile(t, root, "build/records/observed-1.json", bundle(t, bareStatement(t, statement.PredicateWorkloadObservation, observation, strings.Repeat("a", 64))))
	// A signed record whose subject is not the digest its predicate states: the loader admits it exactly as
	// provenance.NewRecord does, as an integrity failure.
	writeFile(t, root, "build/records/deploy-3.json", bundle(t, bareStatement(t, statement.PredicateDeploymentRecord, deployment, strings.Repeat("b", 64))))
	// A statement of the wrong predicate type in the deployment glob is malformed for that kind.
	writeFile(t, root, "build/records/deploy-2.json", bare(t, observation))

	return &workspace.Assurance{
		Producers:  []workspace.Producer{producer("ci", workspace.ProducerClaimBuildProvenance, workspace.ProducerClaimDeploymentRecord, workspace.ProducerClaimWorkloadObservation)},
		Provenance: &workspace.AssuranceProvenance{Statements: []string{"build/attestations/*.json"}},
		Records:    &workspace.AssuranceRecords{Deployments: []string{"build/records/deploy-*.json"}, Observations: []string{"build/records/observed-*.json"}},
	}
}

// expectProvenance checks the provenance statements that were kept and their signatures.
func expectProvenance(t *testing.T, got *evidence.Evidence) {
	t.Helper()

	provenance := make([]string, 0, len(got.Provenance))
	signatures := map[string]statement.Signature{}

	for i := range got.Provenance {
		provenance = append(provenance, got.Provenance[i].Loaded.Path)
		signatures[got.Provenance[i].Loaded.Path] = got.Provenance[i].Verification.Signature
	}

	wantFiles := []string{"build/attestations/refuse.provenance.sigstore.json", "build/attestations/shop.provenance.sigstore.json"}
	if !slices.Equal(provenance, wantFiles) {
		t.Errorf("provenance statements = %v, want %v", provenance, wantFiles)
	}

	if signatures["build/attestations/shop.provenance.sigstore.json"] != statement.SignatureVerified ||
		signatures["build/attestations/refuse.provenance.sigstore.json"] != statement.SignatureFailed {
		t.Errorf("signatures = %v", signatures)
	}
}

// expectDeployments checks the deployment records and the one that contradicts its subject.
func expectDeployments(t *testing.T, got *evidence.Evidence) {
	t.Helper()

	deployments := map[string]assurance.Admission{}
	for i := range got.Deployments {
		deployments[got.Deployments[i].Loaded.Path] = got.Deployments[i].Admission
	}

	deployed := len(got.Deployments) == 2 && got.Deployments[0].Verification.Signature == statement.SignatureVerified &&
		deployments["build/records/deploy-1.json"].Admissible
	expectThat(t, deployed, "deployments", got.Deployments)

	contradicted := deployments["build/records/deploy-3.json"]
	expectThat(t, !contradicted.Admissible && slices.Contains(contradicted.Details, assurance.DetailIntegrityFailure),
		"a record whose subject is not its stated digest (want integrity-failure)", contradicted)
}

// warnedPaths is the set of source paths that a diagnostic of the code names.
func warnedPaths(list []diag.Diagnostic, code string) map[string]bool {
	warned := map[string]bool{}

	for i := range list {
		if list[i].Code == code && list[i].Source != nil {
			warned[list[i].Source.URI] = true
		}
	}

	return warned
}

// TestProvenanceAndRecordsAreLoadedAndVerified: the statements go to the lineage evaluators with their verification;
// a malformed file among valid ones is a warning and no entry.
func TestProvenanceAndRecordsAreLoadedAndVerified(t *testing.T) {
	t.Parallel()

	exe, _ := fakeCosign(t)
	root := t.TempDir()
	cfg := recordsFixture(t, root)

	got := mustLoad(t, context.Background(), options(root, cfg, exe))

	expectProvenance(t, got)
	expectDeployments(t, got)

	if len(got.Observations) != 1 || !got.Observations[0].Admission.Admissible {
		t.Errorf("observations = %+v", got.Observations)
	}

	warned := warnedPaths(got.Diagnostics, diag.PRV009)

	for _, path := range []string{"build/attestations/malformed.provenance.json", "build/attestations/refuse.provenance.sigstore.json", "build/records/deploy-2.json"} {
		if !warned[path] {
			t.Errorf("no CO-PRV-009 for %s; warned for %v", path, warned)
		}
	}

	// The redacted form of every statement that was read is kept for the bundle.
	expectEqual(t, "persisted statements", len(got.Persisted), 5)
}

// TestScenarioErrorsAreConfigurationErrors: every problem comes back in one ConfigError, and no program starts before
// them.
func TestScenarioErrorsAreConfigurationErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	copyAs(t, root, "quality/unknown-element.yaml", "quality/a.yaml")
	copyAs(t, root, "quality/unknown-rule.yaml", "quality/b.yaml")

	cfg := &workspace.Assurance{
		Rendering: rendering(helmSource("production")),
		Quality:   &workspace.AssuranceQuality{Scenarios: []string{"quality/*.yaml"}},
	}
	opts := options(root, cfg, "")
	opts.Known = known()
	opts.Run = forbiddenRun(t)
	opts.Locate = func(string) (string, error) {
		t.Error("a renderer was looked up")

		return "", os.ErrNotExist
	}

	got, err := evidence.Load(context.Background(), opts)

	var config *evidence.ConfigError
	if !errors.As(err, &config) || len(config.Errors) < 2 || config.ExitCode() != diag.InvalidInput {
		t.Fatalf("error = %v, want a ConfigError with at least two errors", err)
	}

	var first *diag.Error
	if !errors.As(err, &first) || first.Diagnostic.Code != diag.QUA002 {
		t.Errorf("first error = %v, want CO-QUA-002", first)
	}

	expectNoEvidence(t, got, "a configuration error")
}

// measurementsFixture writes a measurement of a scenario and one that names no scenario, and gives the configuration.
func measurementsFixture(t *testing.T, root string) *workspace.Assurance {
	t.Helper()

	copyAs(t, root, "quality/valid.yaml", "quality/valid.yaml")

	measurement := map[string]any{
		"scenario": "QS-FAILOVER", "metric": "failover-seconds", "value": 12, "unit": "s", "threshold": 30, "comparison": "lte",
		"testConditions": map[string]string{"load": "peak"}, "revision": revision, "environment": "production", "outcome": "passed",
		"evaluator": map[string]string{"name": "chaos", "version": "1.0.0"},
		"window":    map[string]string{"start": now.Add(-2 * time.Hour).Format(time.RFC3339), "end": now.Add(-time.Hour).Format(time.RFC3339)},
	}
	writeFile(t, root, "build/measurements/failover.json", bundle(t, bareStatement(t, statement.PredicateMeasurement, measurement, "aa")))
	// A measurement that names no scenario cannot be assigned: CO-QUA-003.
	delete(measurement, "scenario")
	writeFile(t, root, "build/measurements/orphan.json", bundle(t, bareStatement(t, statement.PredicateMeasurement, measurement, "bb")))

	return &workspace.Assurance{
		Producers: []workspace.Producer{producer("ci", workspace.ProducerClaimMeasurement)},
		Quality:   &workspace.AssuranceQuality{Scenarios: []string{"quality/*.yaml"}, Measurements: []string{"build/measurements/*.json"}},
	}
}

func TestScenariosAndMeasurementsAreLoaded(t *testing.T) {
	t.Parallel()

	exe, _ := fakeCosign(t)
	root := t.TempDir()
	opts := options(root, measurementsFixture(t, root), exe)
	opts.Known = known()

	got := mustLoad(t, context.Background(), opts)

	if got.Scenarios == nil || len(got.Scenarios.Scenarios) != 3 {
		t.Fatalf("scenarios = %+v", got.Scenarios)
	}

	measured := len(got.Measurements) == 1 && got.Measurements[0].Predicate.Scenario == "QS-FAILOVER" &&
		got.Measurements[0].Verification.Signature == statement.SignatureVerified
	expectThat(t, measured, "measurements", got.Measurements)
	expectCode(t, got.Diagnostics, diag.QUA003)

	// Only the readable measurement is a line of the results; its admission is the one of the statement alone.
	summarized := len(got.Statements) == 1 && got.Statements[0].Kind == evidence.KindMeasurement && got.Statements[0].Admissible
	expectThat(t, summarized, "statements", got.Statements)
}

// TestLoadIsDeterministic: two loads of the same inputs give equal evidence, with a render, a plan, signed results
// and measurements, scenarios, and the warnings they produce.
func TestLoadIsDeterministic(t *testing.T) {
	t.Parallel()

	exe, _ := fakeCosign(t)
	root := t.TempDir()
	copyAs(t, root, "plain", "deploy/plain")
	copyAs(t, root, "plans/terraform-1.2-create.json", "build/plans/prod.json")
	copyAs(t, root, "quality/valid.yaml", "quality/valid.yaml")

	cfg := signedResults(t, root, map[string]map[string]any{
		"a.json":      checkResult("unit-tests", "passed", "production"),
		"b.json":      checkResult("unit-tests", "failed", "production"),
		"refuse.json": checkResult("lint", "passed", "production"),
		"c.json":      checkResult("lint", "passed", "staging"),
	})
	cfg.Rendering = rendering(plainSource("staging", "deploy/plain"), plainSource("production", "deploy/plain"))
	cfg.Plans = []workspace.AssurancePlan{{Environment: "production", Tool: "terraform", File: "build/plans/prod.json"}}
	cfg.Provenance = &workspace.AssuranceProvenance{Statements: []string{"build/attestations/*.json"}}
	cfg.Quality = &workspace.AssuranceQuality{Scenarios: []string{"quality/*.yaml"}}

	load := func() *evidence.Evidence {
		opts := options(root, cfg, exe)
		opts.Known = known()

		return mustLoad(t, context.Background(), opts)
	}

	first, second := load(), load()

	if !reflect.DeepEqual(first, second) {
		t.Error("two loads of the same inputs differ")
	}

	// The lists are sorted in UTF-16 order whatever the order of the declaration and of the files.
	if !slices.IsSortedFunc(first.Statements, func(a, b evidence.StatementSummary) int { return strings.Compare(a.Path, b.Path) }) {
		t.Errorf("statements are not sorted: %+v", first.Statements)
	}

	if first.Renders[0].Environment != "production" || first.Renders[1].Environment != "staging" {
		t.Errorf("renders are not sorted by environment")
	}

	if !slices.IsSortedFunc(first.Diagnostics, func(a, b diag.Diagnostic) int { return strings.Compare(a.Code, b.Code) }) {
		t.Errorf("diagnostics are not sorted: %v", codes(first.Diagnostics))
	}
}
