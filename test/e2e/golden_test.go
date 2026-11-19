package e2e_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The golden contract test. It runs the scenarios that test/golden/capture-ts.sh
// recorded from the TypeScript tool, with the Go binary and the real CBM, and
// compares the results with test/golden/commerce/<scenario>/:
//
//   - exit codes: equal.
//   - results.json, coverage.json, the baseline file and the manifest: parsed;
//     the keys run, toolchain and inputs are removed; every sha256 value is
//     replaced by "sha256:X", the hash of a graph generation and a git commit id
//     are masked (see normalize). The golden fingerprints come from the old GU-
//     rule IDs, and the golden repositories had the old names, so these values differ.
//   - waivers.yaml, the binding candidates and the init YAML and JSON files: parsed values equal.
//     The init architecture and lock files name CALM 1.3, not the recorded 1.2: init writes
//     the latest supported CALM release.
//   - workspace.dsl: text equal after the sha256 replacement.
//   - SARIF: valid against the schema; same multiset of (ruleId, level, baselineState, uri).
//   - JUnit: same test cases with the same failure, error and skipped counts.
//   - stdout and stderr: not compared (the layout can change).
//
// The CBM project names are the names of the capture script. A graph generation
// and a CBM qualified name contain the project name, so equal names are needed
// for equal data.
const (
	goldenRoot       = "../golden/commerce"
	deploymentRoot   = "../golden/deployment"
	goldenNow        = "2026-06-01T00:00:00Z"
	goldenCheck      = "golden-check"
	goldenBrownfield = "golden-brownfield"
)

// scenario records what one scenario of the golden test did.
type scenario struct {
	t     *testing.T
	repo  *repo
	codes map[string]int
	files map[string]string
	// root is the golden directory that holds the directory of the scenario (goldenRoot by default).
	root string
	name string
	// last is the result of the latest step.
	last cliResult
	n    int
}

func newScenario(t *testing.T, name string, r *repo) *scenario {
	t.Helper()

	return &scenario{t: t, repo: r, root: goldenRoot, name: name, codes: map[string]int{}, files: map[string]string{}}
}

// stepKey is the file name prefix of the current step.
func (s *scenario) stepKey(suffix string) string {
	return fmt.Sprintf("%02d-%s", s.n, suffix)
}

// step runs one command in the repository and records the exit code.
func (s *scenario) step(env []string, name string, args ...string) {
	s.t.Helper()

	s.n++

	res := s.repo.cli(append([]string{"CONCORDIA_NOW=" + goldenNow}, env...), args...)
	s.codes[s.stepKey(name)] = res.code
	s.last = res

	if res.code != 0 {
		s.t.Logf("step %s: exit %d, stderr: %s", s.stepKey(name), res.code, shorten(res.stderr))
	}
}

// grab records the files that a command wrote.
func (s *scenario) grab(rels ...string) {
	s.t.Helper()

	for _, rel := range rels {
		data, err := os.ReadFile(s.repo.path(rel))
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			s.t.Fatal(err)
		}

		s.files[s.stepKey(strings.ReplaceAll(rel, "/", "_"))] = string(data)
	}
}

func (s *scenario) grabManifests() {
	s.t.Helper()

	manifests, err := filepath.Glob(s.repo.path(".concordia/runs/*/manifest.json"))
	if err != nil {
		s.t.Fatal(err)
	}

	for _, m := range manifests {
		data, err := os.ReadFile(m)
		if err != nil {
			s.t.Fatal(err)
		}

		s.files[s.stepKey("manifest.json")] = string(data)
	}
}

// snapshot records all files of the repository, without .git.
func (s *scenario) snapshot() {
	s.t.Helper()

	err := filepath.WalkDir(s.repo.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		rel, err := filepath.Rel(s.repo.dir, p)
		if err != nil {
			return err
		}

		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		s.files[s.stepKey(strings.ReplaceAll(filepath.ToSlash(rel), "/", "_"))] = string(data)

		return nil
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

// updateGoldenEnv makes the golden tests rewrite the golden directory instead of failing:
// a golden file is rewritten only when the comparison of the test fails, so a file that still
// matches keeps its recorded bytes. Review the diff of test/golden/commerce afterwards.
const updateGoldenEnv = "CONCORDIA_UPDATE_GOLDEN"

// tester is what the comparison helpers need of a test, so that update mode can run them
// against a recorder that only notes a failure.
type tester interface {
	Helper()
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// recorder is a tester that notes failures instead of reporting them.
type recorder struct {
	t      *testing.T
	failed bool
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(string, ...any) { r.failed = true }

func (r *recorder) Fatal(args ...any) { r.t.Fatal(args...) }

func (r *recorder) Fatalf(format string, args ...any) { r.t.Fatalf(format, args...) }

// writeGolden writes one golden file and creates its directory.
func (s *scenario) writeGolden(path, text string) {
	s.t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		s.t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		s.t.Fatal(err)
	}

	s.t.Logf("updated %s", path)
}

// removeGolden removes one golden file that the tool no longer writes.
func (s *scenario) removeGolden(path string) {
	s.t.Helper()

	if err := os.Remove(path); err != nil {
		s.t.Fatal(err)
	}

	s.t.Logf("removed %s", path)
}

// update rewrites the exit codes and files of the golden directory that differ from the
// run, adds the ones that the golden directory lacks and removes the ones that the tool no
// longer writes.
func (s *scenario) update(dir string) {
	s.t.Helper()

	s.updateCodes(dir)
	s.updateFiles(filepath.Join(dir, "files"))
}

// updateCodes rewrites the golden exit codes of the run and removes the ones of steps that no longer exist.
func (s *scenario) updateCodes(dir string) {
	s.t.Helper()

	for key, code := range s.codes {
		path := filepath.Join(dir, key+".code")

		if data, err := os.ReadFile(path); err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(code) {
			s.writeGolden(path, strconv.Itoa(code)+"\n")
		}
	}

	s.removeStaleCodes(dir)
}

// removeStaleCodes removes the golden exit codes of steps that no longer exist.
func (s *scenario) removeStaleCodes(dir string) {
	s.t.Helper()

	stale, err := filepath.Glob(filepath.Join(dir, "*.code"))
	if err != nil {
		s.t.Fatal(err)
	}

	for _, path := range stale {
		if _, ok := s.codes[strings.TrimSuffix(filepath.Base(path), ".code")]; !ok {
			s.removeGolden(path)
		}
	}
}

// updateFiles rewrites the golden files that differ from the run, adds the missing ones and removes the stale ones.
func (s *scenario) updateFiles(filesDir string) {
	s.t.Helper()

	entries, err := os.ReadDir(filesDir)
	if err != nil && !os.IsNotExist(err) {
		s.t.Fatal(err)
	}

	for _, e := range entries {
		s.updateFile(filepath.Join(filesDir, e.Name()), e.Name())
	}

	for name, got := range s.files {
		if path := filepath.Join(filesDir, name); !fileExists(path) {
			s.writeGolden(path, got)
		}
	}
}

// updateFile rewrites one golden file when it differs from the run and removes it when the run lacks it.
func (s *scenario) updateFile(path, name string) {
	s.t.Helper()

	got, ok := s.files[name]
	if !ok {
		s.removeGolden(path)

		return
	}

	probe := &recorder{t: s.t}
	compareFile(probe, name, readGolden(s.t, path), got)

	if probe.failed {
		s.writeGolden(path, got)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// compare checks the recorded exit codes and files against the golden directory.
func (s *scenario) compare() {
	s.t.Helper()

	dir := filepath.Join(s.root, s.name)

	if os.Getenv(updateGoldenEnv) != "" {
		s.update(dir)

		return
	}

	s.compareCodes(dir)
	s.compareFiles(dir)
}

// compareCodes checks the recorded exit codes against the golden *.code files.
func (s *scenario) compareCodes(dir string) {
	s.t.Helper()

	goldenCodes, err := filepath.Glob(filepath.Join(dir, "*.code"))
	if err != nil {
		s.t.Fatal(err)
	}

	seen := map[string]bool{}

	for _, p := range goldenCodes {
		key := strings.TrimSuffix(filepath.Base(p), ".code")
		seen[key] = true

		s.compareCode(key, p)
	}

	s.t.Logf("%s: compared %d exit codes and %d files", s.name, len(goldenCodes), len(s.files))

	for key := range s.codes {
		if !seen[key] {
			s.t.Errorf("step %s: no golden exit code", key)
		}
	}
}

// compareCode checks the exit code of one step against its golden file p.
func (s *scenario) compareCode(key, p string) {
	s.t.Helper()

	want, err := strconv.Atoi(strings.TrimSpace(readGolden(s.t, p)))
	if err != nil {
		s.t.Fatalf("%s: %v", p, err)
	}

	got, ok := s.codes[key]
	if !ok {
		s.t.Errorf("step %s: not run", key)

		return
	}

	if got != want {
		s.t.Errorf("step %s: exit code %d, golden %d", key, got, want)
	}
}

// compareFiles checks the recorded files against the golden files directory.
func (s *scenario) compareFiles(dir string) {
	s.t.Helper()

	entries, err := os.ReadDir(filepath.Join(dir, "files"))
	if err != nil && !os.IsNotExist(err) {
		s.t.Fatal(err)
	}

	goldenFiles := map[string]bool{}

	for _, e := range entries {
		goldenFiles[e.Name()] = true

		s.compareGoldenFile(filepath.Join(dir, "files", e.Name()), e.Name())
	}

	s.reportUnexpectedFiles(goldenFiles)
}

// compareGoldenFile checks the recorded file name against its golden file at path.
func (s *scenario) compareGoldenFile(path, name string) {
	s.t.Helper()

	got, ok := s.files[name]
	if !ok {
		s.t.Errorf("file %s: the Go tool did not write it", name)

		return
	}

	compareFile(s.t, name, readGolden(s.t, path), got)
}

// reportUnexpectedFiles fails for each recorded file that has no golden file.
func (s *scenario) reportUnexpectedFiles(goldenFiles map[string]bool) {
	s.t.Helper()

	for _, name := range slices.Sorted(maps.Keys(s.files)) {
		if !goldenFiles[name] {
			s.t.Errorf("file %s: the Go tool wrote it, but it is not in the golden files", name)
		}
	}
}

func TestGoldenCheck(t *testing.T) {
	requireE2E(t)

	r := newRepo(t, repoSpec{source: examplesDir, oldName: "commerce-example", project: goldenCheck, dirName: "check"})
	s := newScenario(t, "check", r)

	s.step(nil, "index", "index")
	s.grab("build/concordia/coverage.json")
	s.step(nil, "check-json", "check", "--format", "json")
	s.grab("build/concordia/results.json", "build/concordia/coverage.json")
	s.grabManifests()
	s.step(nil, "check-sarif", "check", "--format", "sarif", "--output", "build/concordia/results.sarif")
	s.grab("build/concordia/results.sarif")
	s.step(nil, "check-junit", "check", "--format", "junit", "--output", "build/concordia/results.xml")
	s.grab("build/concordia/results.xml")
	s.step(nil, "explain", "explain", "CO-BND-001")
	s.step(nil, "check-rule", "check", "--rule", "CO-COV-001")
	s.grab("build/concordia/results.json")
	s.step(nil, "render", "render", "--format", "dsl")
	s.grab("build/concordia/structurizr/workspace.dsl")
	s.step(nil, "model-validate", "model", "validate")
	s.step(nil, "bind-validate", "bind", "validate")
	s.step(nil, "bind-discover", "bind", "discover")
	s.grab("bindings/concordia.bindings.candidates.yaml")
	s.step(nil, "clean", "clean", "--all", "--yes")

	s.compare()
}

func TestGoldenBrownfield(t *testing.T) {
	requireE2E(t)

	r := newRepo(t, repoSpec{source: examplesDir, oldName: "commerce-example", project: goldenBrownfield, dirName: "brownfield"})
	s := newScenario(t, "brownfield", r)

	s.step(nil, "index", "index")
	s.step(nil, "check-initial", "check", "--no-color", "--quiet")
	s.grab("build/concordia/results.json")
	s.step(nil, "baseline-create", "baseline", "create")
	s.grab("exceptions/concordia.baseline.json")
	s.step(nil, "check-baselined", "check", "--no-color")
	s.grab("build/concordia/results.json")

	r.write(refundRel, refundSource)
	r.commitAll("Add refunds")

	s.step(nil, "index-refund", "index")
	s.step(nil, "check-refund", "check", "--no-color", "--quiet")
	s.grab("build/concordia/results.json")

	results := new(checkDoc)
	if err := json.Unmarshal([]byte(r.read("build/concordia/results.json")), results); err != nil {
		t.Fatal(err)
	}

	imports := results.findingsWhere(func(f *checkFinding) bool {
		return f.Disposition == "active" && f.Edge != nil && f.Edge.Kind == "imports"
	})
	if len(imports) != 1 {
		t.Fatalf("%d active imports findings, want 1", len(imports))
	}

	s.step(nil, "waiver-add", "waiver", "add", "--finding", imports[0].Fingerprint,
		"--owner", "team-billing", "--reason", "Refund migration in progress", "--expires", "2026-12-31")
	s.grab("exceptions/waivers.yaml")
	s.step([]string{"CONCORDIA_NOW=2026-12-31T23:59:00Z"}, "check-waived", "check", "--format", "json")
	s.grab("build/concordia/results.json")
	s.step([]string{"CONCORDIA_NOW=2027-01-01T00:00:00Z"}, "check-expired", "check", "--format", "json")
	s.grab("build/concordia/results.json")
	s.step([]string{"CONCORDIA_NOW=2027-01-01T00:00:00Z"}, "waiver-validate", "waiver", "validate")
	s.step(nil, "clean", "clean", "--all", "--yes")

	s.compare()
}

func TestGoldenInit(t *testing.T) {
	requireE2E(t)

	dir := filepath.Join(t.TempDir(), "init")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		t.Fatal(err)
	}

	// The workspace name of init is the directory name, so the directory is called "init" as in the capture script.
	r := &repo{t: t, dir: dir, project: "init"}
	r.git("init", "-q", "-b", "main")

	s := newScenario(t, "init", r)

	s.step(nil, "init-services", "init", "--preset", "services", "--ci", "github")
	s.snapshot()
	s.step(nil, "init-modular", "init", "--preset", "modular-monolith", "--ci", "gitlab", "--force")
	s.snapshot()
	s.step(nil, "init-custom", "init", "--preset", "custom", "--ci", "generic", "--force")
	s.snapshot()

	s.compare()
}
