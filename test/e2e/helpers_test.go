package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/procexec"
)

// End-to-end helpers: the real binary and the real CBM, on a temporary git copy of the commerce example.

const (
	fixtureDir      = "../fixtures/commerce"
	examplesDir     = "../../docs/examples/commerce"
	sarifSchemaPath = "../schemas/sarif-schema-2.1.0.json"
	sarifSchemaID   = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"
	cbmExecutable   = "codebase-memory-mcp"
	workspaceFile   = "concordia.yaml"
	defaultIgnore   = "build/\n.concordia/\n"
	cliTimeout      = 5 * time.Minute
	gitTimeout      = time.Minute
	dirMode         = 0o755
	fileMode        = 0o644
)

// requireE2E skips the test unless CONCORDIA_E2E=1, and checks that CBM is installed.
func requireE2E(t *testing.T) {
	t.Helper()

	if os.Getenv(e2eEnvName) != "1" {
		t.Skip("set CONCORDIA_E2E=1 to run the end-to-end tests")
	}

	if _, err := exec.LookPath(cbmExecutable); err != nil {
		t.Fatalf("%s is not on the PATH: %v", cbmExecutable, err)
	}
}

// cliResult is the result of one run of the binary.
type cliResult struct {
	stdout string
	stderr string
	code   int
}

// output is stdout and stderr together.
func (r cliResult) output() string {
	return r.stderr + r.stdout
}

// repo is a temporary git repository with its own CBM project.
type repo struct {
	t       *testing.T
	dir     string
	project string
}

// repoSpec says which example a repository copies.
type repoSpec struct {
	// source is the example directory (OS path).
	source string
	// oldName is the project name in the workspace file of the example.
	oldName string
	// project is the CBM project name for this repository.
	project string
	// dirName is the name of the repository directory.
	dirName string
}

// newRepo copies the example to a temporary directory, sets the project name
// and makes the first commit. The cleanup deletes the CBM project.
func newRepo(t *testing.T, spec repoSpec) *repo {
	t.Helper()

	dir := filepath.Join(t.TempDir(), spec.dirName)

	if err := os.CopyFS(dir, os.DirFS(spec.source)); err != nil {
		t.Fatalf("copy %s: %v", spec.source, err)
	}

	workspacePath := filepath.Join(dir, workspaceFile)

	text, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}

	patched := strings.Replace(string(text), "name: "+spec.oldName, "name: "+spec.project, 1)
	if patched == string(text) {
		t.Fatalf("project name %q not found in %s", spec.oldName, workspacePath)
	}

	if err := os.WriteFile(workspacePath, []byte(patched), fileMode); err != nil {
		t.Fatal(err)
	}

	ignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignorePath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignorePath, []byte(defaultIgnore), fileMode); err != nil {
			t.Fatal(err)
		}
	}

	r := &repo{t: t, dir: dir, project: spec.project}
	r.git("init", "-q", "-b", "main")
	r.commitAll("Initial commerce example")
	t.Cleanup(r.dispose)

	return r
}

// dispose removes the CBM project. A failed test may leave a half-made state, so it
// uses `clean --all --yes` and then deletes the project by name.
func (r *repo) dispose() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.t.Context()), gitTimeout)
	defer cancel()

	if _, err := os.Stat(r.dir); err == nil {
		_, _ = procexec.Run(ctx, concordiaBin, []string{"clean", "--all", "--yes"}, procexec.Options{Dir: r.dir, Timeout: gitTimeout})
	}

	args, err := json.Marshal(map[string]string{"project": r.project})
	if err != nil {
		return
	}

	_, _ = procexec.Run(ctx, cbmExecutable, []string{"cli", "delete_project", string(args)}, procexec.Options{Timeout: gitTimeout})
}

func (r *repo) path(rel string) string {
	return filepath.Join(r.dir, filepath.FromSlash(rel))
}

// cli runs the binary in the repository. extraEnv is a list of "KEY=value" entries.
// CONCORDIA_NOW is empty unless extraEnv sets it.
func (r *repo) cli(extraEnv []string, args ...string) cliResult {
	r.t.Helper()

	env := append(os.Environ(), "NO_COLOR=1", "CONCORDIA_NOW=")
	env = append(env, extraEnv...)

	res, err := procexec.Run(r.t.Context(), concordiaBin, args, procexec.Options{Dir: r.dir, Env: env, Timeout: cliTimeout})
	if err != nil {
		r.t.Fatalf("concordia %s: %v", strings.Join(args, " "), err)
	}

	return cliResult{code: res.Code, stdout: res.Stdout, stderr: res.Stderr}
}

// gu runs `concordia` with the arguments (global options first).
func (r *repo) gu(args ...string) cliResult {
	r.t.Helper()

	return r.cli(nil, args...)
}

// checkJSON runs `concordia --format json check` and parses the result.
func (r *repo) checkJSON(extraEnv []string) (*checkDoc, cliResult) {
	r.t.Helper()

	res := r.cli(extraEnv, "--format", "json", "check")

	doc := new(checkDoc)
	if err := json.Unmarshal([]byte(res.stdout), doc); err != nil {
		r.t.Fatalf("check output is not JSON (exit %d): %v\nstdout: %s\nstderr: %s", res.code, err, res.stdout, res.stderr)
	}

	return doc, res
}

// git runs git in the repository with a fixed author, committer and date.
func (r *repo) git(args ...string) {
	r.t.Helper()

	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Concordia Docs", "GIT_AUTHOR_EMAIL=docs@concordia.invalid",
		"GIT_COMMITTER_NAME=Concordia Docs", "GIT_COMMITTER_EMAIL=docs@concordia.invalid",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
	)

	res, err := procexec.Run(r.t.Context(), "git", args, procexec.Options{Dir: r.dir, Env: env, Timeout: gitTimeout})
	if err != nil {
		r.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}

	if res.Code != 0 {
		r.t.Fatalf("git %s failed (%d): %s", strings.Join(args, " "), res.Code, res.Stderr)
	}
}

func (r *repo) commitAll(message string) {
	r.t.Helper()

	r.git("add", "-A")
	r.git("-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
}

func (r *repo) write(rel, content string) {
	r.t.Helper()

	target := r.path(rel)

	if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
		r.t.Fatal(err)
	}

	if err := os.WriteFile(target, []byte(content), fileMode); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) read(rel string) string {
	r.t.Helper()

	data, err := os.ReadFile(r.path(rel))
	if err != nil {
		r.t.Fatal(err)
	}

	return string(data)
}

// sha is the SHA-256 of a file as hex.
func (r *repo) sha(rel string) string {
	r.t.Helper()

	sum := sha256.Sum256([]byte(r.read(rel)))

	return hex.EncodeToString(sum[:])
}

// checkDoc is the part of `results.json` that the tests read.
type checkDoc struct {
	Diagnostics []struct {
		Code string `json:"code"`
	} `json:"diagnostics"`
	Rules      []checkRule     `json:"rules"`
	Findings   []*checkFinding `json:"findings"`
	Exceptions checkExceptions `json:"exceptions"`
	Run        checkRun        `json:"run"`
	Summary    checkSummary    `json:"summary"`
}

type checkRun struct {
	GraphGeneration string `json:"graphGeneration"`
}

type checkSummary struct {
	Verdict       string `json:"verdict"`
	ExitCode      int    `json:"exitCode"`
	Active        int    `json:"active"`
	Baselined     int    `json:"baselined"`
	Waived        int    `json:"waived"`
	ExpiredWaiver int    `json:"expiredWaiver"`
	UnknownRules  int    `json:"unknownRules"`
}

type checkExceptions struct {
	Waivers []struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Finding string `json:"finding"`
	} `json:"waivers"`
}

type checkRule struct {
	Reasons []struct {
		Code string `json:"code"`
	} `json:"reasons"`
	RuleID   string `json:"ruleId"`
	Verdict  string `json:"verdict"`
	Coverage struct {
		Files struct {
			Excluded []string `json:"excluded"`
			Total    int      `json:"total"`
			Covered  int      `json:"covered"`
		} `json:"files"`
	} `json:"coverage"`
}

type checkEndpoint struct {
	Observed *struct {
		FilePath string `json:"filePath"`
	} `json:"observed"`
	ArchitectureID string `json:"architectureId"`
}

type checkFinding struct {
	Edge *struct {
		Kind string `json:"kind"`
	} `json:"edge"`
	Location *struct {
		Path string `json:"path"`
	} `json:"location"`
	DispositionRef *string       `json:"dispositionRef"`
	Source         checkEndpoint `json:"source"`
	Target         checkEndpoint `json:"target"`
	Fingerprint    string        `json:"fingerprint"`
	RuleID         string        `json:"ruleId"`
	Kind           string        `json:"kind"`
	Disposition    string        `json:"disposition"`
}

// locationPath is the path of the finding location, or empty.
func (f *checkFinding) locationPath() string {
	if f.Location == nil {
		return ""
	}

	return f.Location.Path
}

func (d *checkDoc) rule(id string) *checkRule {
	for i := range d.Rules {
		if d.Rules[i].RuleID == id {
			return &d.Rules[i]
		}
	}

	return nil
}

func (d *checkDoc) findingsWhere(keep func(*checkFinding) bool) []*checkFinding {
	var out []*checkFinding

	for _, f := range d.Findings {
		if keep(f) {
			out = append(out, f)
		}
	}

	return out
}

// withoutVolatile is the result as text without the fields that differ between two runs.
func withoutVolatile(t *testing.T, stdout string) string {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}

	runInfo, ok := doc["run"].(map[string]any)
	if !ok {
		t.Fatal("results.json has no run object")
	}

	for _, key := range []string{"id", "startedAt", "finishedAt", "host"} {
		delete(runInfo, key)
	}

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// xmlIsWellFormed reports whether the text is well-formed XML (tags nest and close).
func xmlIsWellFormed(text string) bool {
	dec := xml.NewDecoder(strings.NewReader(text))

	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return true
		}

		if err != nil {
			return false
		}
	}
}

// containsAll checks that text has every part.
func containsAll(t *testing.T, text string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("output does not contain %q:\n%s", part, text)
		}
	}
}
