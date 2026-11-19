package cli_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
)

const modelWorkspaceYAML = "metadata: {name: demo}\narchitecture: {source: a.calm.json}\nobservation:\n  projects:\n    - {name: demo, root: .}\n"

func modelDoc(t *testing.T, ids ...string) string {
	t.Helper()

	nodes := make([]any, len(ids))
	for i, id := range ids {
		nodes[i] = bindNode(id, nil)
	}

	data, err := json.Marshal(map[string]any{
		"$schema":       "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":         nodes,
		"relationships": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestModelValidateJUnitHasOneCasePerCategoryAndMarksFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": modelWorkspaceYAML, "a.calm.json": modelDoc(t, "a", "a")})

	res := runIn(t, dir, "model", "validate", "--format", "junit")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if got := strings.Count(res.Stdout, "<testcase "); got != 10 {
		t.Errorf("testcase count = %d, want 10", got)
	}

	if got := strings.Count(res.Stdout, "<failure "); got != 1 {
		t.Errorf("failure count = %d, want 1", got)
	}

	if !strings.Contains(res.Stdout, `type="CO-INT-004"`) {
		t.Errorf("no CO-INT-004 failure in %s", res.Stdout)
	}
}

func TestModelValidateChecksDecorators(t *testing.T) {
	t.Parallel()

	const (
		arch      = `{"$schema":"https://calm.finos.org/release/1.3/meta/calm.json","nodes":[],"relationships":[]}`
		decorator = `{"unique-id":"d1","type":"deployment","target":["../architecture/a.calm.json"],"applies-to":["missing"],"data":{}}`
		wsHead    = "metadata: {name: demo}\nobservation:\n  projects:\n    - {name: demo, root: .}\n"
	)

	for _, tc := range []struct {
		name, archLine string
		args           []string
	}{
		{"workspace", "architecture: {source: architecture/a.calm.json, decorators: [decorators/d.json]}\n", nil},
		{"flag", "architecture: {source: architecture/a.calm.json}\n", []string{"--decorator", "decorators/d.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			testutil.WriteTree(t, dir, map[string]string{
				"concordia.yaml":           wsHead + tc.archLine,
				"architecture/a.calm.json": arch,
				"decorators/d.json":        decorator,
			})

			res := runIn(t, dir, append([]string{"model", "validate"}, tc.args...)...)
			if res.Code != 2 || !strings.Contains(res.Stdout+res.Stderr, "CO-INT-009") {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
			}
		})
	}
}

func TestModelValidateValidDocument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": modelWorkspaceYAML, "a.calm.json": modelDoc(t, "a", "b")})

	res := runIn(t, dir, "model", "validate")
	if res.Code != 0 || !strings.Contains(res.Stdout, "model valid:") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	res = runIn(t, dir, "model", "validate", "--format", "json")

	var doc struct {
		Architecture struct {
			Digest string `json:"digest"`
		} `json:"architecture"`
		Diagnostics []any `json:"diagnostics"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil || doc.Architecture.Digest == "" || doc.Diagnostics == nil {
		t.Errorf("json output = %q (%v)", res.Stdout, err)
	}

	res = runIn(t, dir, "model", "validate", "--format", "sarif")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Errorf("unsupported format: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

const viewsEnvironments = "environments:\n  - {id: staging, sources: [deploy/staging/**]}\n"

// viewDef is a generated container view with the output path and the extra lines.
func viewDef(outputPath, extra string) string {
	return "views:\n  - id: v1\n    kind: container\n    origin: generated\n    owner: team\n    scope: [a]\n    abstraction: container\n" +
		extra + "    output: {path: " + outputPath + "}\n"
}

func TestModelValidateViewScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, view, want string
		code             int
	}{
		{"Output path is ignored by Git", viewDef("build/concordia/views/container.dsl", ""), "ignored by Git", 2},
		{"Tracked path is accepted", viewDef("docs/architecture/views/container.dsl", ""), "", 0},
		{"Tracked path that a rule matches is accepted", viewDef("docs/architecture/views/kept.gen.dsl", ""), "", 0},
		{"Unknown environment", viewDef("docs/v.dsl", "    environment: prod-eu\n"), "environment 'prod-eu' is not declared (declared environments: staging)", 2},
		{"Coverage names an unknown element", viewDef("docs/v.dsl", "    coverage: {elements: [ghost]}\n"), "coverage.elements names 'ghost'", 2},
		{"Mapping to an unknown CALM ID", "views:\n  - id: v1\n    kind: system-context\n    origin: authored\n    owner: team\n    scope: [a]\n    abstraction: system\n" +
			"    source: {path: docs/a.md, format: mermaid, selector: {title: T}}\n    mapping: {elements: {api: ghost}}\n", "mapping.elements key 'api' maps to 'ghost'", 2},
		{"Duplicate view ID", viewDef("docs/v.dsl", "") + "  - id: v1\n    kind: container\n    origin: generated\n    owner: t\n    scope: [a]\n    abstraction: container\n    output: {path: docs/w.dsl}\n", "views[0] and views[1]", 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			testutil.WriteTree(t, dir, map[string]string{
				"concordia.yaml":                       modelWorkspaceYAML + viewsEnvironments + tc.view,
				"a.calm.json":                          modelDoc(t, "a"),
				".gitignore":                           "build/concordia/\n*.gen.dsl\n",
				"docs/architecture/views/kept.gen.dsl": "tracked although a rule matches it",
			})
			testutil.Git(t, dir, "init", "-q")
			testutil.Git(t, dir, "add", "-f", "-A")

			res := runIn(t, dir, "model", "validate")
			if res.Code != tc.code {
				t.Fatalf("exit code = %d, want %d\nstdout %q\nstderr %q", res.Code, tc.code, res.Stdout, res.Stderr)
			}

			if got := res.Stdout + res.Stderr; !strings.Contains(got, tc.want) {
				t.Errorf("output has no %q: %q", tc.want, got)
			}
		})
	}
}

func TestModelValidateSuggestsDefaultViewsWithoutChangingTheExitCode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml": modelWorkspaceYAML + "environments:\n  - {id: prod, sources: [deploy/prod/**]}\n",
		"a.calm.json":    modelDoc(t, "a"),
	})

	res := runIn(t, dir, "model", "validate")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr %q", res.Code, res.Stderr)
	}

	if got := res.Stdout + res.Stderr; !strings.Contains(got, "CO-DGM-005") || !strings.Contains(got, "deployment view for the production environment 'prod'") {
		t.Errorf("no suggestion in %q", got)
	}
}

func TestModelValidateAcceptsTheViewsFixture(t *testing.T) {
	t.Parallel()

	const fixture = "../../test/fixtures/views"

	dir := t.TempDir()

	if err := filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, relErr := filepath.Rel(fixture, p)
		if relErr != nil {
			return relErr
		}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}

		testutil.WriteTree(t, dir, map[string]string{filepath.ToSlash(rel): string(data)})

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res := runIn(t, dir, "model", "validate")
	if res.Code != 0 {
		t.Fatalf("exit code = %d\nstdout %q\nstderr %q", res.Code, res.Stdout, res.Stderr)
	}

	if strings.Contains(res.Stdout+res.Stderr, "CO-DGM") {
		t.Errorf("a fully declared workspace prints no suggestion: %q", res.Stdout+res.Stderr)
	}
}

// trustModelDoc is a two-node CALM document of the given release whose node `a` declares the given trust object.
func trustModelDoc(release, trustJSON string) string {
	return `{"$schema":"https://calm.finos.org/release/` + release + `/meta/calm.json","nodes":[` +
		`{"unique-id":"a","node-type":"service","name":"a","description":"d","concordia":{"trust":` + trustJSON + `}},` +
		`{"unique-id":"b","node-type":"service","name":"b","description":"d"}],` +
		`"relationships":[{"unique-id":"r","relationship-type":{"connects":{"source":{"node":"a"},"destination":{"node":"b"}}}}]}`
}

func TestModelValidateChecksTrustDeclarations(t *testing.T) {
	t.Parallel()

	const workspaceYAML = modelWorkspaceYAML + "trust: {dimensions: [tenant], classifications: [public, internal, restricted]}\n"

	for _, tc := range []struct {
		name, release, trust string
		wantText             string
		wantCode             int
	}{
		// Scenario: SEC-01 dimensions on a node.
		{"valid under 1.2", "1.2", `{"dimensions":{"organization":"acme","zone":"internet","tenant":"t1"},"classification":"restricted"}`, "", 0},
		{"valid under 1.3", "1.3", `{"dimensions":{"account":"a1"}}`, "", 0},
		// Scenario: SEC-01 undeclared dimension name.
		{"undeclared dimension", "1.2", `{"dimensions":{"firewall":"x"}}`, "CO-SEC-001", 2},
		// Scenario: SEC-01 classification outside the declared list.
		{"classification outside the list", "1.2", `{"classification":"secret"}`, "CO-SEC-001", 2},
		// Scenario: Unknown trust key.
		{"unknown key", "1.3", `{"zones":{"zone":"x"}}`, "CO-INT-007", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": workspaceYAML, "a.calm.json": trustModelDoc(tc.release, tc.trust)})

			res := runIn(t, dir, "model", "validate")
			if res.Code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", res.Code, tc.wantCode, res.Stdout, res.Stderr)
			}

			if tc.wantText != "" && !strings.Contains(res.Stdout+res.Stderr, tc.wantText) {
				t.Errorf("output lacks %s:\nstdout: %s\nstderr: %s", tc.wantText, res.Stdout, res.Stderr)
			}
		})
	}
}
