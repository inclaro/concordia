package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// patternURL is the URL of a CALM pattern that the architecture names as its `$schema`; the URL mapping maps it to a
// workspace file.
const patternURL = "https://example.org/patterns/demo.json"

// mappedPatternFiles make the architecture of the check workspace depend on its URL mapping: the `$schema` of the
// CALM document is a pattern URL that the mapping maps to a file of the workspace. The mapping also has an entry
// outside the workspace, one for a missing file, and an absolute entry whose path, joined to the mapping directory,
// would name a workspace file.
func mappedPatternFiles() map[string]string {
	return map[string]string{
		"concordia.yaml": strings.Replace(chkWorkspaceText(""), "architecture: {source: architecture/a.calm.json}",
			"architecture: {source: architecture/a.calm.json, urlMapping: architecture/url-mapping.json}", 1),
		"architecture/a.calm.json": strings.Replace(chkArch(), "https://calm.finos.org/release/1.2/meta/calm.json", patternURL, 1),
		"architecture/url-mapping.json": `{"` + patternURL + `": "patterns/demo.json",
  "https://example.org/shared.json": "../../shared.json",
  "https://example.org/absolute.json": "/absolute/abs.json",
  "https://example.org/missing.json": "patterns/missing.json"}`,
		"architecture/patterns/demo.json": `{"$schema": "https://calm.finos.org/release/1.2/meta/calm.json", "$id": "` +
			patternURL + `", "type": "object"}`,
		"architecture/absolute/abs.json": `{"description": "not the file of the absolute entry"}`,
	}
}

// exportTestRun runs the command and stops the test when its exit code is above maxCode.
func exportTestRun(t *testing.T, s *chkSetup, maxCode int, args ...string) result {
	t.Helper()

	res := s.run(t, args...)
	if res.Code > maxCode {
		t.Fatalf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
	}

	return res
}

// exportTestWantNoFile reports each file of the manifest whose path contains one of the parts.
func exportTestWantNoFile(t *testing.T, manifest *bundle.Manifest, parts ...string) {
	t.Helper()

	for _, f := range manifest.Files {
		if slices.ContainsFunc(parts, func(part string) bool { return strings.Contains(f.Path, part) }) {
			t.Errorf("bundle holds %s, which is not a file of the workspace", f.Path)
		}
	}
}

// A bundle holds the URL mapping and the mapped files of the workspace, so `diff` loads the architecture of the
// bundle with the mapping: a CALM document whose pattern is URL-mapped loads on the bundle side.
func TestExportBundleCarriesTheURLMappingForDiff(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, mappedPatternFiles(), nil)
	exportTestRun(t, s, 1, "check")
	exportTestRun(t, s, 0, "export", "bundle")

	manifest, err := bundle.Open(exportTestBundlePath(s), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	exportTestWantBundleFiles(t, manifest, "inputs/architecture/url-mapping.json", "inputs/architecture/patterns/demo.json")
	exportTestWantNoFile(t, manifest, "shared.json", "missing.json", "abs.json")

	res := exportTestRun(t, s, 0, "diff", "--from", exportTestBundlePath(s), "--to", "worktree", "--kind", "intent", "--no-color")
	if strings.Contains(res.Stderr, "CO-INT-012") {
		t.Fatalf("diff from the bundle: stderr = %q", res.Stderr)
	}
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

// exportTestRefused runs each command and reports whether it refused the run with CO-REP-004 and exit code 2.
func exportTestRefused(t *testing.T, s *chkSetup, commands [][]string, want bool) {
	t.Helper()

	for _, args := range commands {
		res := s.run(t, args...)
		mentions := strings.Contains(res.Stderr, "CO-REP-004")

		ok := !mentions
		if want {
			ok = mentions && res.Code == 2
		}

		if !ok {
			t.Errorf("%v (refusal wanted: %v): exit code = %d, stderr = %q", args, want, res.Code, res.Stderr)
		}
	}
}

func TestIncompleteRunIsRefused(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)
	exportTestRun(t, s, 1, "check")

	commands := exportTestIncompleteCommands(filepath.Join("build", "concordia", "results.json"))
	exportTestRefused(t, s, commands, false)

	markIncomplete(t, s.dir)
	exportTestRefused(t, s, commands, true)
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
