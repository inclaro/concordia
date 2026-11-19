package integration_test

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

const adrFixtureDir = "../fixtures/adr"

// TestADRFixtureModelValidates runs `concordia model validate` on the decision fixture (test/fixtures/adr): the
// CALM model has the nodes orders-service and orders-db (with the interface sql) and the relationship orders-to-db.
func TestADRFixtureModelValidates(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(adrFixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer

	stdio := cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Env: map[string]string{}, Cwd: dir}

	code := cli.Run(t.Context(), []string{"model", "validate"}, &stdio, cli.Deps{}, cli.BuildInfo{Version: "test"})
	if code != 0 {
		t.Fatalf("model validate exit code = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
}

// TestADRFixtureHasOneFilePerVariant checks that the variant files match the variants table: one file for each
// decision of internal/adr/testdata/variants.yaml, and no other file below docs/adr/variants.
func TestADRFixtureHasOneFilePerVariant(t *testing.T) {
	files := map[string]bool{}

	err := filepath.WalkDir(filepath.Join(adrFixtureDir, "docs", "adr", "variants"), func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		rel, relErr := filepath.Rel(adrFixtureDir, p)
		if relErr != nil {
			return relErr
		}

		files[filepath.ToSlash(rel)] = true

		return nil
	})
	if err != nil {
		t.Fatalf("walk the variants: %v", err)
	}

	graph, _ := replayADR(t)

	listed := 0

	for _, n := range graph.NodesOf("ADR") {
		if !strings.HasPrefix(n.FilePath, adrDir+"variants/") {
			continue
		}

		listed++

		if !files[n.FilePath] {
			t.Errorf("the recording has a decision for %s, which is not a fixture file", n.FilePath)
		}
	}

	if listed != len(files) {
		t.Errorf("the recording has %d variant decisions and the fixture has %d variant files", listed, len(files))
	}
}

// TestADRFixtureCheckA12 runs the whole `check` pipeline on the decision fixture against the recording: ADR-0003 is
// committed, ADR-0007 supersedes it, so the governance rules GOV-ADR-002 and GOV-ADR-003 fail. It is the regression
// test for the decision evidence reaching the evaluation: the recording is made with `indexMode: full`, the only
// mode in which CBM 0.12.1 extracts Markdown (ADR nodes, `SUPERSEDES` edges); with `fast` or `moderate` no ADR node
// exists and the presence is `unverifiable (extraction-incomplete)`.
func TestADRFixtureCheckA12(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(adrFixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	var (
		files   []string
		content = map[string]string{}
	)

	err := fs.WalkDir(os.DirFS(adrFixtureDir), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		data, readErr := os.ReadFile(filepath.Join(adrFixtureDir, filepath.FromSlash(p)))
		files = append(files, p)
		content[p] = string(data)

		return readErr
	})
	if err != nil {
		t.Fatalf("list the fixture: %v", err)
	}

	client, err := observation.LoadReplayClient(adrRecordingFile)
	if err != nil {
		t.Fatalf("load the recording: %v", err)
	}

	rev := client.HeadSHA()

	run, err := conformance.RunCheck(t.Context(), &conformance.CheckOptions{
		Env: conformance.Env{
			Logger: logger.New(logger.Options{Out: io.Discard, RunID: "run", Quiet: true}), RunID: runID,
			WorkspacePath: filepath.Join(dir, workspaceFile), Cwd: dir, ToolVersion: "test",
		},
		Deps: conformance.Deps{
			CreateClient: func(*workspace.Workspace, *logger.Logger) observation.Client { return client },
			CreateGit: func(string) workspace.GitPort {
				return &workspace.FakeGit{Rev: rev, Files: files, FilesAt: map[string]map[string]string{rev: content}}
			},
			EvaluateDecisions: adr.Evaluate,
		},
		SkipReports: true,
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if run.DecisionSummary == nil || len(run.DecisionSummary.Decisions) != 1 {
		t.Fatalf("decision summary = %+v", run.DecisionSummary)
	}

	entry := run.DecisionSummary.Decisions[0]
	if entry.Presence.State != string(adr.PresenceCommitted) || entry.Applicability.State != string(adr.ApplicabilitySuperseded) ||
		entry.Status != "accepted" || !slices.Equal(entry.Successors, []string{"ADR-7"}) {
		t.Fatalf("ADR-0003 = %+v", entry)
	}

	verdicts := verdictsOf(run)
	for rule, want := range map[string]conformance.RuleVerdict{"GOV-ADR-001": "pass", "GOV-ADR-002": "violation", "GOV-ADR-003": "violation"} {
		if verdicts[rule] != want {
			t.Errorf("%s = %q, want %q (all: %v)", rule, verdicts[rule], want, verdicts)
		}
	}
}
