package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// mustInit runs `init` in the directory and stops the test unless it exits 0.
func mustInit(t *testing.T, dir string, deps cli.Deps, args ...string) result {
	t.Helper()

	res := initIn(t, dir, deps, args...)
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return res
}

// expectFilesExist reports each of the files that `init` did not write.
func expectFilesExist(t *testing.T, dir string, files ...string) {
	t.Helper()

	for _, f := range files {
		if !exists(dir, f) {
			t.Errorf("%s was not written", f)
		}
	}
}

// expectFileContains reports failure when the file does not contain want.
func expectFileContains(t *testing.T, dir, rel, want string) {
	t.Helper()

	if text := readFile(t, dir, rel); !strings.Contains(text, want) {
		t.Errorf("%s lacks %q: %q", rel, want, text)
	}
}

// expectFileEquals reports failure when the file is not exactly want.
func expectFileEquals(t *testing.T, dir, rel, want string) {
	t.Helper()

	if got := readFile(t, dir, rel); got != want {
		t.Errorf("%s = %q", rel, got)
	}
}

func TestInitWritesStarterFilesForAPresetAndValidatesThem(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "my repo-x")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	res := mustInit(t, dir, cbmVersion("0.12.1"), "--preset", "services", "--ci", "github")

	expectFilesExist(t, dir,
		"concordia.yaml",
		"architecture/architecture.json",
		"bindings/concordia.bindings.yaml",
		"policies/default.policy.yaml",
		"exceptions/waivers.yaml",
		".concordia.lock",
		".github/workflows/concordia.yml",
	)

	expectFileContains(t, dir, ".concordia.lock", "0.12.1")
	expectFileContains(t, dir, "policies/default.policy.yaml", "CO-DECL-001")
	expectFileEquals(t, dir, ".gitignore", "build/concordia/\n.concordia/\n")
	expectFileContains(t, dir, "concordia.yaml", "name: my-repo-x")

	for _, line := range []string{"wrote concordia.yaml", "updated .gitignore", "initialized: workspace, architecture, and policies are valid"} {
		if !strings.Contains(res.Stdout, line) {
			t.Errorf("stdout lacks %q: %q", line, res.Stdout)
		}
	}
}

func TestInitSupportsTheOtherPresetsAndCITemplates(t *testing.T) {
	t.Parallel()

	a := t.TempDir()
	mustInit(t, a, cbmVersion(""), "--preset", "modular-monolith", "--ci", "gitlab")

	expectFileContains(t, a, "policies/default.policy.yaml", "CO-CYC-001")

	if !exists(a, ".gitlab/concordia.gitlab-ci.yml") {
		t.Error("the GitLab template is missing")
	}

	expectFileContains(t, a, ".concordia.lock", "unknown")

	b := t.TempDir()
	mustInit(t, b, cbmVersion(""), "--ci", "generic")

	if !exists(b, "ci/concordia.sh") {
		t.Error("the generic template is missing")
	}
}

// initCITemplate is the file that a CI choice writes, the text that it has, and the text that it must not have.
type initCITemplate struct {
	ci       string
	file     string
	contains []string
	absent   []string
}

// check reports the wanted text that the template lacks, the unwanted text that it has, and invalid YAML.
func (c *initCITemplate) check(t *testing.T, text string) {
	t.Helper()

	for _, want := range c.contains {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", c.file, want)
		}
	}

	for _, bad := range c.absent {
		if strings.Contains(text, bad) {
			t.Errorf("%s has %q", c.file, bad)
		}
	}

	c.checkYAML(t, text)
}

// checkYAML reports a template that is not YAML. The generic template is a shell script.
func (c *initCITemplate) checkYAML(t *testing.T, text string) {
	t.Helper()

	if c.ci == "generic" {
		return
	}

	var doc any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Errorf("%s is not YAML: %v", c.file, err)
	}
}

func TestInitCITemplates(t *testing.T) {
	t.Parallel()

	goInstall := "go install github.com/inclaro/concordia/cmd/concordia@latest"
	cbmComment := "Install the pinned codebase-memory-mcp build (the default directory ~/.local/bin is not on the PATH of a runner)."
	// The release check greps the tree for the variable name, so the test splits it.
	goPrivate := "GO" + "PRIVATE"

	cases := []initCITemplate{
		{"github", ".github/workflows/concordia.yml", []string{"actions/setup-go@v5", `go-version: "1.27"`, goInstall, "# " + cbmComment, `- run: concordia cbm install --dir "$HOME/.local/bin" && echo "$HOME/.local/bin" >> "$GITHUB_PATH"`, "- run: concordia index"}, []string{"npm", "npx", "setup-node", "0.12.x", goPrivate}},
		{"gitlab", ".gitlab/concordia.gitlab-ci.yml", []string{"image: golang:1", goInstall, "# " + cbmComment, `- concordia cbm install --dir "$HOME/.local/bin"`, `- export PATH="$HOME/.local/bin:$PATH"`, "- concordia index"}, []string{"npm", "npx", "node", "0.12.x", goPrivate}},
		{"generic", "ci/concordia.sh", []string{"# " + cbmComment, "concordia cbm install\n", `export PATH="$HOME/.local/bin:$PATH"`, "concordia index", "concordia check --format json"}, []string{"npm", "npx", "0.12.x"}},
	}

	for _, tc := range cases {
		t.Run(tc.ci, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			mustInit(t, dir, cbmVersion(""), "--ci", tc.ci)

			tc.check(t, readFile(t, dir, tc.file))
		})
	}
}

func TestInitDoesNotOverwriteWithoutForceAndDoesNotDuplicateGitignoreLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"bindings/concordia.bindings.yaml": "bindings: []\n# mine\n",
		".gitignore":                       "node_modules/\nbuild/concordia/\n.concordia/\n",
	})

	res := mustInit(t, dir, cbmVersion(""))

	expectFileContains(t, dir, "bindings/concordia.bindings.yaml", "# mine")

	if !strings.Contains(res.Stderr, "CO-WRK-005") {
		t.Errorf("stderr = %q", res.Stderr)
	}

	expectFileEquals(t, dir, ".gitignore", "node_modules/\nbuild/concordia/\n.concordia/\n")

	mustInit(t, dir, cbmVersion(""), "--force")

	if strings.Contains(readFile(t, dir, "bindings/concordia.bindings.yaml"), "# mine") {
		t.Error("--force did not overwrite")
	}
}

func TestInitAppendsToGitignoreWithoutFinalNewline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{".gitignore": "node_modules/\r\n.concordia/"})

	mustInit(t, dir, cbmVersion(""))

	expectFileEquals(t, dir, ".gitignore", "node_modules/\r\n.concordia/\nbuild/concordia/\n")
}

func TestInitWithCalmDoesNotWriteTheDefaultArchitectureAndAnUnreadableFileFailsValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	res := initIn(t, dir, cbmVersion(""), "--calm", "arch/missing.json")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-INT-001") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if exists(dir, "architecture/architecture.json") {
		t.Error("the default architecture was written")
	}
}

func TestInitLocksTheCalmSchemaOfTheArchitecture(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"arch/a.json": `{"$schema":"https://calm.finos.org/release/1.2/meta/calm.json","nodes":[],"relationships":[]}`,
	})

	mustInit(t, dir, cbmVersion("0.12.1"), "--calm", "arch/a.json")

	if lock := readFile(t, dir, ".concordia.lock"); !strings.Contains(lock, `schemaVersion: "1.2"`) {
		t.Errorf("lock = %q", lock)
	}
}

func TestInitRejectsBadChoices(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--preset", "x"}, {"--ci", "jenkins"}} {
		res := initIn(t, t.TempDir(), cbmVersion(""), args...)
		if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}
}

// initGoldenSkipped reports whether a file of `init` has no golden counterpart on purpose: the CI templates changed
// (Go instead of Node.js) and the Go starter bindings file is a valid BindingSet.
func initGoldenSkipped(rel string) bool {
	if rel == "bindings/concordia.bindings.yaml" {
		return true
	}

	return slices.ContainsFunc([]string{"concordia.yml", "gitlab-ci.yml", ".sh"}, func(suffix string) bool {
		return strings.HasSuffix(rel, suffix)
	})
}

// compareInitGolden compares the files of the init directory with the golden files of the scenario, and gives the
// number of files that it compared.
func compareInitGolden(t *testing.T, dir, golden, prefix string) int {
	t.Helper()

	compared := 0

	err := filepath.WalkDir(dir, func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)

		if initGoldenSkipped(rel) {
			return nil
		}

		want, readErr := os.ReadFile(filepath.Join(golden, prefix+"-"+strings.ReplaceAll(rel, "/", "_")))
		if readErr != nil {
			t.Errorf("%s: no golden file for %s: %v", prefix, rel, readErr)

			return nil
		}

		compared++

		initGoldenFile{label: prefix + " " + rel, rel: rel, want: want}.compare(t, readFile(t, dir, rel))

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return compared
}

// TestInitMatchesTheTypeScriptGolden compares the files of `init` with the
// golden files of the TypeScript tool. The YAML and JSON files must parse to
// equal values; the CI templates changed on purpose (Go instead of Node.js).
func TestInitMatchesTheTypeScriptGolden(t *testing.T) {
	t.Parallel()

	golden := filepath.Join("..", "..", "test", "golden", "commerce", "init", "files")
	dir := filepath.Join(t.TempDir(), "init")

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	scenarios := []struct {
		prefix string
		args   []string
	}{
		{"01", []string{"--preset", "services", "--ci", "github"}},
		{"02", []string{"--preset", "modular-monolith", "--ci", "gitlab", "--force"}},
		{"03", []string{"--preset", "custom", "--ci", "generic", "--force"}},
	}

	for _, sc := range scenarios {
		if res := initIn(t, dir, cbmVersion("0.12.1"), sc.args...); res.Code != 0 {
			t.Fatalf("%s: exit code = %d, stderr = %q", sc.prefix, res.Code, res.Stderr)
		}

		if compared := compareInitGolden(t, dir, golden, sc.prefix); compared < 6 {
			t.Errorf("%s: compared only %d files", sc.prefix, compared)
		}
	}
}

// initGoldenFile is a golden file of the TypeScript tool, as the label of its comparison, its path in the init
// directory and its content.
type initGoldenFile struct {
	label string
	rel   string
	want  []byte
}

// compare checks that the generated content is the golden one: the same text for .gitignore, else equal parsed values.
func (g initGoldenFile) compare(t *testing.T, got string) {
	t.Helper()

	if g.rel == ".gitignore" {
		if string(g.want) != got {
			t.Errorf("%s: got %q, want %q", g.label, got, g.want)
		}

		return
	}

	var wantValue, gotValue any

	unmarshal := yaml.Unmarshal
	if strings.HasSuffix(g.rel, ".json") {
		unmarshal = json.Unmarshal
	}

	if err := unmarshal(g.want, &wantValue); err != nil {
		t.Fatalf("%s: golden does not parse: %v", g.label, err)
	}

	if err := unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("%s: output does not parse: %v", g.label, err)
	}

	if !reflect.DeepEqual(wantValue, gotValue) {
		t.Errorf("%s:\n got %v\nwant %v", g.label, gotValue, wantValue)
	}
}

func TestInitStarterBindingsLoadWithoutErrors(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "starter")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if res := initIn(t, dir, cbmVersion("0.12.1"), "--preset", "services"); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wsp, err := workspace.Load(filepath.Join(dir, "concordia.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}

	loaded := intent.LoadArchitecture(wsp.ResolveInRoot(wsp.Architecture.Source), intent.LoadContext{Root: wsp.Root})
	if loaded.Model == nil {
		t.Fatalf("architecture not loaded: %v", loaded.Diagnostics)
	}

	set, err := binding.Load(wsp, loaded.Model)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range set.Diagnostics {
		if d.Severity == diag.SeverityError {
			t.Errorf("starter bindings give an error: %s %s", d.Code, d.Message)
		}
	}
}
