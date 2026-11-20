package cli_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// renderViewsArch is a payments system with the two services of the standard test workspace and one declared
// relationship between them.
func renderViewsArch() string {
	return chkJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes": []any{
			chkNode("payments-system", map[string]any{"node-type": "system"}),
			chkNode("billing-service", nil), chkNode("fulfillment-service", nil), chkNode("legacy-service", nil),
		},
		"relationships": []any{
			map[string]any{
				"unique-id": "payments-composition",
				"relationship-type": map[string]any{"composed-of": map[string]any{
					"container": "payments-system", "nodes": []any{"billing-service", "fulfillment-service", "legacy-service"},
				}},
			},
			map[string]any{
				"unique-id": "billing-to-fulfillment",
				"relationship-type": map[string]any{"connects": map[string]any{
					"source": map[string]any{"node": "billing-service"}, "destination": map[string]any{"node": "fulfillment-service"},
				}},
			},
		},
	})
}

func viewsText(views ...string) string {
	return "views:\n" + strings.Join(views, "")
}

func generatedView(id, path, extra string) string {
	return "  - id: " + id + "\n    kind: container\n    origin: generated\n    owner: team\n    scope: [payments-system]\n" +
		"    abstraction: container\n    comparison: {against: [declared]}\n    output:\n      path: " + path + "\n" + extra
}

const authoredContextView = `  - id: payments-context
    kind: system-context
    origin: authored
    owner: team
    scope: [payments-system]
    abstraction: system
    source: {path: docs/arch/payments.md, format: mermaid, selector: {title: Payments context}}
    mapping: {elements: {payments: payments-system}}
`

func viewsSetup(t *testing.T, views string) *chkSetup {
	t.Helper()

	return newChkSetup(t, map[string]string{
		"concordia.yaml":           chkWorkspaceText(views),
		"architecture/a.calm.json": renderViewsArch(),
		"docs/arch/payments.md":    "# Payments\n",
	}, nil)
}

const (
	viewAPath = "docs/views/a.dsl"
	viewBPath = "docs/views/b.dsl"
)

func twoViews() string {
	return viewsText(generatedView("view-a", viewAPath, ""), generatedView("view-b", viewBPath, ""), authoredContextView)
}

// Scenario "Repeated render is identical": two runs on identical inputs write byte-identical artifacts and
// manifests, and the manifest records the view, the paths and the digests.
func TestRenderViewsRepeatedRenderIsIdentical(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())

	if res := s.run(t, "render", "--views"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	} else if !strings.Contains(res.Stderr, "view written: "+viewAPath+".manifest.json") {
		t.Errorf("stderr = %q", res.Stderr)
	}

	files := []string{viewAPath, viewAPath + ".manifest.json", viewBPath, viewBPath + ".manifest.json"}
	first := map[string]string{}

	for _, f := range files {
		first[f] = readFile(t, s.dir, f)
	}

	if !strings.Contains(first[viewAPath], `container gu_payments_system "view-a"`) {
		t.Errorf("the Structurizr view is not named with the view ID:\n%s", first[viewAPath])
	}

	manifest := first[viewAPath+".manifest.json"]
	wantContains(t, manifest, `"schema": "concordia-view-manifest/1"`, `"id": "view-a"`, `"path": "`+viewAPath+`"`,
		`"observedProjection": null`, `"definitionVersion": "concordia-view-definition/1"`)

	if res := s.run(t, "render", "--views"); res.Code != 0 {
		t.Fatalf("second render: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, f := range files {
		if got := readFile(t, s.dir, f); got != first[f] {
			t.Errorf("%s changed between two renders:\n%s\n---\n%s", f, first[f], got)
		}
	}

	if exists(s.dir, "docs/arch/payments.md.manifest.json") {
		t.Error("an authored view got a manifest")
	}
}

// Scenario "Limited regeneration": --view-id writes only that view and its manifest.
func TestRenderViewsLimitedRegeneration(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())

	if res := s.run(t, "render", "--views", "--view-id", "view-b"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exists(s.dir, viewBPath) || !exists(s.dir, viewBPath+".manifest.json") {
		t.Error("view-b and its manifest are not written")
	}

	if exists(s.dir, viewAPath) || exists(s.dir, viewAPath+".manifest.json") {
		t.Error("view-a is written")
	}
}

// Scenario "Default output stays": render without --views writes build/concordia/structurizr/workspace.dsl and no
// view file.
func TestRenderViewsDefaultOutputStays(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())

	if res := s.run(t, "render"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exists(s.dir, "build/concordia/structurizr/workspace.dsl") {
		t.Error("no workspace.dsl in the default output directory")
	}

	if exists(s.dir, viewAPath) || exists(s.dir, viewAPath+".manifest.json") {
		t.Error("render without --views wrote a view")
	}
}

// An output path that is not valid gives exit 2 before any file is written, for the valid views too.
func TestRenderViewsInvalidPathWritesNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, path, want string
	}{
		{"under projection.output", "build/concordia/structurizr/b.dsl", "CO-DGM-002"},
		{"outside the workspace", "../b.dsl", "CO-DGM-002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := viewsSetup(t, viewsText(generatedView("view-a", viewAPath, ""), generatedView("view-b", tc.path, "")))

			res := s.run(t, "render", "--views")
			if res.Code != 2 || !strings.Contains(res.Stderr, tc.want) {
				t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
			}

			if exists(s.dir, viewAPath) || exists(s.dir, viewAPath+".manifest.json") {
				t.Error("a file was written although a path is not valid")
			}
		})
	}
}

func TestRenderViewsIgnoredPathWritesNothing(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, viewsText(generatedView("view-a", viewAPath, "")))
	s.cfg.Deps.NewGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{Rev: "abc123", Ignored: map[string]bool{viewAPath: true}}
	}

	res := s.run(t, "render", "--views")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-DGM-002") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if exists(s.dir, viewAPath) {
		t.Error("an ignored output was written")
	}
}

func TestRenderViewsOptionErrors(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())

	for _, args := range [][]string{
		{"render", "--view-id", "view-a"},
		{"render", "--views", "--view-id", "nope"},
		{"render", "--views", "--view-id", "payments-context"},
		{"render", "--views", "--view", "container"},
		{"render", "--views", "--results", "results.json"},
		{"render", "--views", "--focus", "billing-service"},
		{"render", "--views", "--workspace-out", "x.dsl"},
		{"--format", "mermaid", "render", "--views"},
	} {
		res := s.run(t, args...)
		if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}

	if exists(s.dir, viewAPath) {
		t.Error("a refused command wrote a view")
	}

	none := viewsSetup(t, "")
	if res := none.run(t, "render", "--views"); res.Code != 2 || !strings.Contains(res.Stderr, "declares no views") {
		t.Errorf("no views: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// An export needs the Structurizr CLI. Without it the views that need no export are written, the view with exports
// is skipped with CO-PRJ-008 and keeps its files, and the command fails with CO-PRJ-001 and exit 5.
func TestRenderViewsWithoutTheCLIWritesTheViewsThatNeedNoExport(t *testing.T) {
	t.Parallel()

	export := "      exports:\n        - {format: mermaid, path: docs/views/a.mmd}\n"
	s := viewsSetup(t, viewsText(generatedView("view-a", viewAPath, export), generatedView("view-b", viewBPath, "")))
	testutil.WriteTree(t, s.dir, map[string]string{viewAPath: "old dsl"})

	cfg := *s.cfg
	cfg.Env = maps.Clone(s.cfg.Env)
	cfg.Env["PATH"] = ""

	res := runWith(t, &cfg, "render", "--views")
	if res.Code != 5 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stderr, "CO-PRJ-008", "view-a: its exports need the Structurizr CLI",
		"CO-PRJ-001: Structurizr CLI not available; it is needed for the exports of view view-a", "hint: install the Structurizr CLI")

	if !exists(s.dir, viewBPath) || !exists(s.dir, viewBPath+".manifest.json") {
		t.Error("view-b, which needs no export, is not written")
	}

	if readFile(t, s.dir, viewAPath) != "old dsl" {
		t.Error("the artifact of view-a changed although its export could not run")
	}

	for _, f := range []string{viewAPath + ".manifest.json", "docs/views/a.mmd"} {
		if exists(s.dir, f) {
			t.Errorf("%s was written although the export of view-a could not run", f)
		}
	}
}
