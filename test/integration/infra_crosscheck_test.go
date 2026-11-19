package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// The whole check on the infra fixture, with declared environments: the manifest cross-check, the index
// result of the recording, and the predicate coverage of O1, O4, O5, O6 and O11 in one run. The seeded
// secrets of the fixture must not reach any gap, log, diagnostic, or report file.

const infraEnvironmentsText = `environments:
  - id: production
    sources: ["k8s/prod/**", "charts/**", "scripts/**"]
  - id: staging
    sources: ["k8s/staging/**"]
  - id: edge
    sources: ["deploy/**"]
`

type infraRun struct {
	run   *conformance.Run
	rec   observation.Recording
	dir   string
	logs  *bytes.Buffer
	files []string
}

// runInfra checks the infra fixture with the recording (or the one with `!deploy/`). With generation set,
// the index result of the recording is written first, as `concordia index` does.
func runInfra(t *testing.T, recordingFile string, writeIndex bool, edits ...func(*conformance.CheckOptions)) *infraRun {
	t.Helper()

	w := newInfraWorkspace(t, recordingFile, deploysWorkspaceFiles(deploysWorkspace+infraEnvironmentsText))
	dir, rec, files := w.dir, w.rec, w.files

	logs := &bytes.Buffer{}
	check := func() *conformance.Run {
		run, checkErr := conformance.RunCheck(context.Background(), w.checkOptions(logger.New(logger.Options{Out: logs, RunID: "run", Verbose: true, Trace: true}), edits...))
		if checkErr != nil {
			t.Fatalf("check failed: %v", checkErr)
		}

		return run
	}

	run := check()

	if writeIndex {
		snap := replayInfra(t, recordingFile)
		record := snap.index.Record(infraProject, run.Run.GraphGeneration, rec.HeadSHA)

		if err := conformance.WriteIndexRecord(dir, conformance.IndexRecordFile, &record); err != nil {
			t.Fatal(err)
		}

		run = check()
	}

	return &infraRun{run: run, rec: rec, dir: dir, logs: logs, files: files}
}

func predicateRecord(t *testing.T, run *conformance.Run, predicate string, scope observation.CoverageScope) *observation.PredicateCoverage {
	t.Helper()

	for i := range run.Coverage.Predicates {
		rec := &run.Coverage.Predicates[i]
		if rec.Predicate == predicate && rec.Scope == scope {
			return rec
		}
	}

	t.Fatalf("no record %s in %+v", predicate, scope)

	return nil
}

func gapsOf(rec *observation.PredicateCoverage, mode string) []observation.CoverageGap {
	for _, m := range rec.LossModes {
		if m.ID == mode {
			return m.Gaps
		}
	}

	return nil
}

func TestInfraCheckRecordsTheLossModesOfTheRecording(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true)
	production := observation.CoverageScope{Environment: "production"}

	checkInfraEdgeLossModes(t, got.run)
	checkInfraOccurrenceLossModes(t, got.run, production)
	checkInfraSecretReferenceGaps(t, got.run, production)
	checkInfraSelectorGaps(t, got.run, production)
	checkInfraTemplateGaps(t, got.run, production)
	checkInfraProjectScopeGaps(t, got.run)
}

// O1: the edge environment source is under the default-excluded deploy/ directory.
func checkInfraEdgeLossModes(t *testing.T, run *conformance.Run) {
	t.Helper()

	edge := predicateRecord(t, run, "uses-image", observation.CoverageScope{Environment: "edge"})
	if edge.Status != observation.CoveragePartial || len(gapsOf(edge, observation.LossDefaultExcludedDirectory)) != 1 {
		t.Errorf("edge uses-image = %+v", edge)
	}

	if !slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.OBS020 }) {
		t.Errorf("no CO-OBS-020: %+v", run.Diagnostics)
	}
}

// O4: the `app` container of the production Deployment is lost.
func checkInfraOccurrenceLossModes(t *testing.T, run *conformance.Run, production observation.CoverageScope) {
	t.Helper()

	occurrences := predicateRecord(t, run, observation.FamilyContainerOccurrenceVersions, production)
	merged := gapsOf(occurrences, observation.LossMergedOccurrences)

	if occurrences.Status != observation.CoveragePartial || !anyInfraGap(merged, isInfraMergedAppContainerGap) {
		t.Errorf("container-occurrence-versions = %+v", occurrences)
	}
}

// anyInfraGap reports whether the predicate holds for one of the gaps.
func anyInfraGap(gaps []observation.CoverageGap, pred func(g *observation.CoverageGap) bool) bool {
	for i := range gaps {
		if pred(&gaps[i]) {
			return true
		}
	}

	return false
}

func isInfraMergedAppContainerGap(g *observation.CoverageGap) bool {
	return g.Container == "app" && g.Path == infraProdDeploy && g.Workload == "Deployment/api"
}

// O5: references to declarations that no manifest has.
func checkInfraSecretReferenceGaps(t *testing.T, run *conformance.Run, production observation.CoverageScope) {
	t.Helper()

	secrets := predicateRecord(t, run, "references-secret", production)
	if !anyInfraGap(gapsOf(secrets, observation.LossTargetNotDeclared), isInfraMissingSecretGap) {
		t.Errorf("references-secret = %+v", secrets)
	}
}

func isInfraMissingSecretGap(g *observation.CoverageGap) bool {
	return g.ReferenceMode == "env" && g.Target == "missing-secret" && g.TargetKind == "Secret"
}

// O6: empty and expression selectors in the NetworkPolicies.
func checkInfraSelectorGaps(t *testing.T, run *conformance.Run, production observation.CoverageScope) {
	t.Helper()

	selects := predicateRecord(t, run, "policy-selects-workload", production)
	for _, mode := range []string{observation.LossEmptySelector, observation.LossUnsupportedSelectorExpression} {
		if gaps := gapsOf(selects, mode); len(gaps) == 0 || gaps[0].Path != "k8s/prod/networkpolicy.yaml" {
			t.Errorf("policy-selects-workload %s = %+v", mode, gaps)
		}
	}
}

// O11: the Helm template is not rendered; the cross-check skips it.
func checkInfraTemplateGaps(t *testing.T, run *conformance.Run, production observation.CoverageScope) {
	t.Helper()

	const template = "charts/api/templates/deployment.yaml"

	templates := predicateRecord(t, run, "rendered-templates", production)
	if !slices.ContainsFunc(gapsOf(templates, observation.LossUnrenderedTemplate), func(g observation.CoverageGap) bool { return g.Path == template }) {
		t.Errorf("rendered-templates = %+v", templates)
	}

	crossCheck := run.Coverage.CrossCheck
	if !slices.Contains(crossCheck.FilesSkipped, template) || crossCheck.FilesChecked == 0 {
		t.Errorf("cross-check = %+v", crossCheck)
	}
}

// O1 raster image and the partly parsed template, in the project scope.
func checkInfraProjectScopeGaps(t *testing.T, run *conformance.Run) {
	t.Helper()

	project := observation.CoverageScope{Project: infraProject}

	diagrams := predicateRecord(t, run, "diagram-identity", project)
	if len(gapsOf(diagrams, observation.LossRasterNotIndexed)) != 1 {
		t.Errorf("diagram-identity = %+v", diagrams)
	}

	index := predicateRecord(t, run, observation.FamilyIndexResult, project)
	if gaps := gapsOf(index, observation.LossParsePartial); len(gaps) != 1 || gaps[0].Line != 1 {
		t.Errorf("index-result = %+v", index)
	}
}

func TestInfraCheckWithTheDirectoryReincluded(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraReincludeFile, true)

	edge := predicateRecord(t, got.run, "uses-image", observation.CoverageScope{Environment: "edge"})
	if len(gapsOf(edge, observation.LossDefaultExcludedDirectory)) != 0 {
		t.Errorf("edge uses-image = %+v", edge)
	}
}

func TestInfraCheckWithoutAnIndexResultIsUnknown(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, false)

	for _, env := range []string{"production", "staging", "edge"} {
		rec := predicateRecord(t, got.run, "uses-image", observation.CoverageScope{Environment: env})
		if rec.Status != observation.CoverageUnknown || !slices.Contains(rec.Reasons, "index result unavailable") {
			t.Errorf("%s uses-image = %+v", env, rec)
		}
	}
}

// infraSeedEntry is one seeded secret or environment value of the infra fixture.
type infraSeedEntry struct {
	Path  string `json:"path"`
	Class string `json:"class"`
	Value string `json:"value"`
}

func readInfraSeedEntries(t *testing.T) []infraSeedEntry {
	t.Helper()

	var seeds []infraSeedEntry
	if err := json.Unmarshal(mustRead(t, infraSeedsFile), &seeds); err != nil {
		t.Fatal(err)
	}

	return seeds
}

// requireNoInfraSeeds reports every output that holds a seeded value, in clear text or in base64.
func requireNoInfraSeeds(t *testing.T, outputs map[string][]byte, seeds []infraSeedEntry) {
	t.Helper()

	for name, data := range outputs {
		for _, seed := range seeds {
			for _, needle := range []string{seed.Value, base64.StdEncoding.EncodeToString([]byte(seed.Value))} {
				if bytes.Contains(data, []byte(needle)) {
					t.Errorf("%s contains the seed %s of %s", name, seed.Class, seed.Path)
				}
			}
		}
	}
}

// collectInfraFiles reads every file under root into outputs, keyed by keyOf(path).
func collectInfraFiles(t *testing.T, root, what string, keyOf func(path string) string, outputs map[string][]byte) {
	t.Helper()

	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			outputs[keyOf(p)] = mustRead(t, p)
		}

		return walkErr
	})
	if err != nil {
		t.Fatalf("walk the %s: %v", what, err)
	}
}

// No seeded secret or environment value reaches a gap, a log, a diagnostic, or a written report (EVD-04, A38).
func TestInfraCheckKeepsSeededValuesOutOfEveryOutput(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true)
	seeds := readInfraSeedEntries(t)

	canonical, err := conformance.CanonicalJSON(got.run)
	if err != nil {
		t.Fatal(err)
	}

	outputs := map[string][]byte{"logs": got.logs.Bytes(), "run": canonical}

	collectInfraFiles(t, filepath.Join(got.dir, "build"), "written files", func(p string) string { return p }, outputs)

	if len(outputs) < 4 {
		t.Fatalf("expected the written run files, got %d outputs", len(outputs))
	}

	requireNoInfraSeeds(t, outputs, seeds)
}

// exportInfraBundle runs `export bundle` with the graph and the source snippets on the checked workspace and
// returns what it wrote to stdout and stderr.
func exportInfraBundle(t *testing.T, got *infraRun) (stdout, stderr []byte) {
	t.Helper()

	var outBuf, errBuf bytes.Buffer

	stdio := cli.IO{Stdin: strings.NewReader(""), Stdout: &outBuf, Stderr: &errBuf, Env: map[string]string{"CONCORDIA_NOW": fixedNow}, Cwd: got.dir}
	deps := cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client {
			return observation.NewReplayClient(got.rec)
		},
		NewGit: func(string) workspace.GitPort { return &workspace.FakeGit{Rev: got.rec.HeadSHA, Files: got.files} },
	}

	code := cli.Run(t.Context(), []string{"--verbose", "--trace", "export", "bundle", "--include-graph", "--include-source-snippets", "violations"}, &stdio, deps, cli.BuildInfo{Version: "test"})
	if code > 1 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}

	return outBuf.Bytes(), errBuf.Bytes()
}

// infraBundleParts tells which parts of the exported bundle the run produced.
type infraBundleParts struct {
	gaps, snippet, graph bool
}

func (p *infraBundleParts) observe(name string, data []byte) {
	p.gaps = p.gaps || (strings.HasPrefix(name, "bundle/") && bytes.Contains(data, []byte(observation.LossMergedOccurrences)))
	p.snippet = p.snippet || strings.HasPrefix(name, "bundle/snippets/")
	p.graph = p.graph || strings.HasPrefix(name, "bundle/graph/")
}

// The bundle of the same run, with the graph and the source snippets: no file of it holds a seeded value,
// in clear text or in base64. The run has the cross-check gaps, and the snippets come from the files that
// hold the seeds (the violation of CO-DEP-001 is in scripts/deploy.sh).
func TestInfraExportBundleKeepsSeededValuesOut(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true)
	stdout, stderr := exportInfraBundle(t, got)

	extractDir := t.TempDir()
	if _, err := bundle.Open(filepath.Join(got.dir, "build", "concordia", "conformance.tar.gz"), extractDir); err != nil {
		t.Fatalf("open the bundle: %v", err)
	}

	outputs := map[string][]byte{"stdout": stdout, "stderr": stderr}

	collectInfraFiles(t, extractDir, "bundle", func(p string) string {
		rel, _ := filepath.Rel(extractDir, p)

		return "bundle/" + filepath.ToSlash(rel)
	}, outputs)

	var parts infraBundleParts
	for name, data := range outputs {
		parts.observe(name, data)
	}

	requireNoInfraSeeds(t, outputs, readInfraSeedEntries(t))

	if !parts.gaps || !parts.snippet || !parts.graph {
		t.Errorf("the bundle lacks the cross-check gaps (%v), a snippet (%v), or the graph (%v); %d files", parts.gaps, parts.snippet, parts.graph, len(outputs))
	}
}
