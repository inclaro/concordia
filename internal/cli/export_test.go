package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
)

// exportTestWantBundleFiles checks that the manifest lists every path.
func exportTestWantBundleFiles(t *testing.T, manifest *bundle.Manifest, paths ...string) {
	t.Helper()

	have := map[string]bool{}
	for _, f := range manifest.Files {
		have[f.Path] = true
	}

	for _, want := range paths {
		if !have[want] {
			t.Errorf("bundle lacks %s: %v", want, manifest.Files)
		}
	}
}

func TestExportBundleWritesTheArchive(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "export", "bundle", "--include-graph", "--include-source-snippets", "violations")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	out := exportTestBundlePath(s)
	if !strings.HasPrefix(res.Stdout, out+" sha256:") {
		t.Fatalf("stdout = %q", res.Stdout)
	}

	manifest, err := bundle.Open(out, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if env := manifest.Provenance.Environments; env.Selected == nil || env.Excluded == nil {
		t.Errorf("provenance environments: %+v", env)
	}

	exportTestWantBundleFiles(t, manifest, "results/results.json", "results/results.sarif", "inputs/concordia.yaml", "graph/evidence.json")
}

func TestExportBundleSignsWithCosign(t *testing.T) {
	t.Parallel()

	exportTestSkipWithoutShell(t)

	s := newChkSetup(t, nil, nil)
	script := filepath.Join(t.TempDir(), "cosign")

	exportTestWriteScript(t, script, "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = \"--bundle\" ]; then echo sig > \"$2\"; fi; shift; done\n")

	s.cfg.Env["COSIGN"] = script

	res := s.run(t, "export", "bundle", "--sign", "--sign-key", "k")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	sig := exportTestBundlePath(s) + ".sigstore.json"
	if _, err := os.Stat(sig); err != nil {
		t.Fatalf("no signature file: %v", err)
	}

	if !strings.Contains(res.Stdout, sig) {
		t.Errorf("stdout lacks the signature path: %q", res.Stdout)
	}
}

func TestExportBundleSignNeedsCosign(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "export", "bundle", "--sign", "--sign-key", "k")
	if res.Code != 5 || !strings.Contains(res.Stderr, "CO-WRK-014") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// exportTestIncompleteCommands are the commands that read a run and refuse an incomplete one.
func exportTestIncompleteCommands(results string) [][]string {
	return [][]string{
		{"export", "bundle"},
		{"explain", "CO-BND-001"},
		{"explain", "CO-BND-001", "--results", results},
		{"diff", "--from", results, "--to", results},
		{"diff", "--from", results, "--to", "worktree"},
	}
}

func TestIncompleteRunIsRefused(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	if res := s.run(t, "check"); res.Code > 1 {
		t.Fatalf("check: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	results := filepath.Join("build", "concordia", "results.json")

	for _, args := range exportTestIncompleteCommands(results) {
		if res := s.run(t, args...); strings.Contains(res.Stderr, "CO-REP-004") {
			t.Fatalf("%v before: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}

	markIncomplete(t, s.dir)

	for _, args := range exportTestIncompleteCommands(results) {
		res := s.run(t, args...)
		if res.Code != 2 || !strings.Contains(res.Stderr, "CO-REP-004") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}
}

func TestInterruptedCheckGivesExit130(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	s.cfg.Ctx = ctx

	if res := s.run(t, "check"); res.Code != 130 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	paths, _ := filepath.Glob(filepath.Join(s.dir, ".concordia", "runs", "*", "manifest.json"))
	for _, p := range paths {
		if text, _ := os.ReadFile(p); strings.Contains(string(text), `"complete"`) {
			t.Errorf("%s says complete", p)
		}
	}
}
