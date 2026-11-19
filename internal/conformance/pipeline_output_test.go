package conformance_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestInterruptedRunKeepsManifestIncomplete(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	opts := &conformance.CheckOptions{Env: s.env, Deps: s.deps}
	render := opts.Deps.RenderReport
	opts.Deps.RenderReport = func(f workspace.ReportFormat, r *conformance.Run) ([]byte, error) {
		cancel() // the run is stopped before its files are done.

		return render(f, r)
	}

	if _, err := conformance.RunCheck(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	runs, err := filepath.Glob(filepath.Join(s.dir, ".concordia", "runs", "*", "manifest.json"))
	if err != nil || len(runs) != 1 {
		t.Fatalf("manifests %v, err %v", runs, err)
	}

	var m struct{ Status string }
	readJSON(t, runs[0], &m)

	if m.Status != conformance.ManifestIncomplete {
		t.Errorf("status = %q, want incomplete", m.Status)
	}

	if err := conformance.RequireCompleteRun(s.dir, filepath.Base(filepath.Dir(runs[0]))); err == nil {
		t.Error("an incomplete run was accepted")
	}
}

func TestCompleteRunManifestStatus(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.SkipReports = false })

	var m struct{ Status string }
	readJSON(t, filepath.Join(s.dir, ".concordia", "runs", run.Run.ID, "manifest.json"), &m)

	if m.Status != conformance.ManifestComplete {
		t.Errorf("status = %q, want complete", m.Status)
	}

	if err := conformance.RequireCompleteRun(s.dir, run.Run.ID); err != nil {
		t.Error(err)
	}
}

func TestWritesCanonicalJSONManifestAndEvidenceFiles(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		o.SkipReports = false
		o.EvidenceDir = "build/evidence"
	})

	want, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	if got := testutil.ReadFile(t, filepath.Join(s.dir, "build", "concordia", "results.json")); got != string(want) {
		t.Error("results.json is not the canonical JSON of the run")
	}

	assertManifestKeys(t, s.dir, run.Run.ID)
	assertEvidenceFile(t, s.dir, &run.Findings[0])
	assertBuildLayout(t, s.dir)
}

func assertManifestKeys(t *testing.T, dir, runID string) {
	t.Helper()

	var manifest map[string]any
	readJSON(t, filepath.Join(dir, ".concordia", "runs", runID, "manifest.json"), &manifest)

	keys := make([]string, 0, len(manifest))
	for k := range manifest {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	if !slices.Equal(keys, []string{"inputs", "observability", "run", "status", "summary", "toolchain"}) {
		t.Errorf("manifest keys %v", keys)
	}
}

func assertEvidenceFile(t *testing.T, dir string, finding *conformance.Finding) {
	t.Helper()

	fp := strings.TrimPrefix(finding.Fingerprint, "sha256:")

	var evidence struct {
		Finding struct{ Fingerprint string } `json:"finding"`
		Rule    struct{ RuleID string }      `json:"rule"`
	}

	readJSON(t, filepath.Join(dir, "build", "evidence", fp+".json"), &evidence)

	if evidence.Finding.Fingerprint != finding.Fingerprint || evidence.Rule.RuleID != "CO-BND-001" {
		t.Errorf("evidence %+v", evidence)
	}
}

func assertBuildLayout(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if !slices.Equal(names, []string{"concordia", "evidence"}) {
		t.Errorf("build entries %v", names)
	}

	tmp, _ := filepath.Glob(filepath.Join(dir, "build", "concordia", "*.tmp"))
	if len(tmp) != 0 {
		t.Errorf("temporary files %v", tmp)
	}
}

func TestWritesEveryReportOutputAndTheJSONWhenNotConfigured(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, &setupOpts{ws: wsText(
		"report:\n  outputs:\n    - {format: sarif, path: out/r.sarif}\n    - {format: junit, path: out/r.xml}\n    - {format: pretty, path: out/r.txt}\n")})
	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.SkipReports = false })

	for rel, format := range map[string]string{"out/r.sarif": "sarif", "out/r.xml": "junit", "out/r.txt": "pretty"} {
		if got := testutil.ReadFile(t, filepath.Join(s.dir, filepath.FromSlash(rel))); got != "stub "+format+"\n" {
			t.Errorf("%s: %q", rel, got)
		}
	}

	want, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	if got := testutil.ReadFile(t, filepath.Join(s.dir, "build", "concordia", "results.json")); got != string(want) {
		t.Error("results.json differs from the canonical JSON")
	}
}

func TestSkipReportsWritesNothing(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	s.mustCheck(t, nil)

	for _, rel := range []string{"build", ".concordia"} {
		if _, err := os.Stat(filepath.Join(s.dir, rel)); err == nil {
			t.Errorf("%s exists", rel)
		}
	}
}
