package binding_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func modelWith(nodes ...intent.NodeDeclaration) *intent.ArchitectureModel {
	return &intent.ArchitectureModel{SourceURI: "architecture/a.calm.json", Nodes: nodes}
}

func calmNode(bindings ...intent.ExtBinding) intent.NodeDeclaration {
	return intent.NodeDeclaration{ID: "billing", Bindings: bindings, Pointer: "/nodes/0"}
}

// loadTree writes files below a new workspace root, which globs `bindings/*.yaml`, and loads it.
func loadTree(t *testing.T, files map[string]string, model *intent.ArchitectureModel, opts ...binding.LoadOption) binding.Loaded {
	t.Helper()

	root := t.TempDir()
	testutil.WriteTree(t, root, files)

	ws := &workspace.Workspace{Root: root, Bindings: workspace.Files{Files: []string{"bindings/*.yaml"}}}

	got, err := binding.Load(ws, model, opts...)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func loadFiles(t *testing.T, files map[string]string, model *intent.ArchitectureModel) binding.Loaded {
	t.Helper()

	return loadTree(t, files, model)
}

func codesOf(l *binding.Loaded) []string {
	out := make([]string, 0, len(l.Diagnostics))
	for i := range l.Diagnostics {
		out = append(out, l.Diagnostics[i].Code)
	}

	return out
}

func idsOf(bindings []binding.Binding) []string {
	out := make([]string, 0, len(bindings))
	for i := range bindings {
		out = append(out, bindings[i].ID)
	}

	return out
}

const header = "apiVersion: concordia.io/v1alpha1\nkind: BindingSet\nbindings:\n"

// checkFileBinding checks the binding `b1` that the BindingSet file of the first test declares.
func checkFileBinding(t *testing.T, b *binding.Binding) {
	t.Helper()

	if b.ArchitectureRef != "billing" || b.Exclusive {
		t.Errorf("b1 = %+v", *b)
	}

	if !b.Required || len(b.Match) != 2 {
		t.Errorf("b1 = %+v", *b)
	}

	if b.Projection.C4Level != "container" || b.Source != "bindings/a.yaml" {
		t.Errorf("b1 = %+v", *b)
	}
}

// checkEmbeddedBinding checks the binding that the CALM node of the first test declares.
func checkEmbeddedBinding(t *testing.T, ext *binding.Binding) {
	t.Helper()

	if !ext.Exclusive || !ext.Required {
		t.Errorf("ext = %+v", *ext)
	}

	if ext.Source != "architecture/a.calm.json#/nodes/0/concordia/bindings/0" {
		t.Errorf("ext = %+v", *ext)
	}

	if ext.Match[0] != (binding.Match{Kind: binding.MatchQualifiedName, Value: "a.b.C"}) {
		t.Errorf("ext = %+v", *ext)
	}
}

func TestLoadReadsBindingSetFilesAndEmbeddedBindings(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"bindings/a.yaml": header + "  - id: b1\n    architectureRef: calm:node:billing\n    exclusive: false\n    match:\n      any:\n        - sourceRoot: services/billing/**\n        - package: com.acme.billing..\n    projection: {c4Level: container}\n",
	}
	model := modelWith(calmNode(intent.ExtBinding{Kind: "qualified-name", Value: "a.b.C"}))

	got := loadFiles(t, files, model)
	if len(got.Diagnostics) != 0 || !slices.Equal(got.Files, []string{"bindings/a.yaml"}) {
		t.Fatalf("loaded = %+v", got)
	}

	if !slices.Equal(idsOf(got.Bindings), []string{"b1", "calm-ext:billing:0"}) {
		t.Fatalf("bindings = %+v", got.Bindings)
	}

	checkFileBinding(t, &got.Bindings[0])
	checkEmbeddedBinding(t, &got.Bindings[1])
}

func TestLoadReportsProblemsAsDiagnostics(t *testing.T) {
	t.Parallel()

	body := func(match string) string {
		return header + "  - id: b1\n    architectureRef: calm:node:ghost\n    match:\n      any:\n        - " + match + "\n"
	}

	cases := map[string]struct {
		file string
		want []string
	}{
		"unknown ref":       {body("sourceRoot: a/**"), []string{diag.BND001}},
		"bad build label":   {body("buildTarget: a/b"), []string{diag.BND001, diag.BND002}},
		"bad package":       {body("package: com.acme"), []string{diag.BND001, diag.BND002}},
		"absolute root":     {body("sourceRoot: /abs/**"), []string{diag.BND001, diag.BND002}},
		"parent segment":    {body("sourceRoot: a/../b"), []string{diag.BND001, diag.BND002}},
		"schema violation":  {"apiVersion: wrong\nkind: BindingSet\nbindings: []\n", []string{diag.BND002}},
		"yaml syntax error": {"a: [1,\n", []string{diag.BND002}},
		"duplicate key":     {"a: 1\na: 2\n", []string{diag.BND002}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := loadFiles(t, map[string]string{"bindings/a.yaml": tc.file}, modelWith())
			if codes := codesOf(&got); !slices.Equal(codes, tc.want) {
				t.Fatalf("codes = %v, want %v (%+v)", codes, tc.want, got.Diagnostics)
			}
		})
	}
}

func TestLoadReportsEmbeddedProblemsAndDuplicateIDs(t *testing.T) {
	t.Parallel()

	model := modelWith(calmNode(
		intent.ExtBinding{Kind: "build-target", Value: "x"},
		intent.ExtBinding{Kind: "package", Value: "nope"},
	))
	files := map[string]string{
		"bindings/a.yaml": header + "  - id: calm-ext:billing:1\n    architectureRef: calm:node:billing\n    match: {any: [{sourceRoot: a/**}]}\n",
	}

	got := loadFiles(t, files, model)
	if want := []string{diag.BND002, diag.BND002, diag.BND006}; !slices.Equal(codesOf(&got), want) {
		t.Fatalf("codes = %v (%+v)", codesOf(&got), got.Diagnostics)
	}

	// Bindings with a bad match stay, with no match.
	if len(got.Bindings) != 3 {
		t.Fatalf("bindings = %+v", got.Bindings)
	}
}

func TestExpandFileGlobsSkipsToolDirectoriesAndSorts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{
		"b/x.yaml":            "",
		"a/.hidden.yaml":      "",
		".concordia/x.yaml":   "",
		"node_modules/y.yaml": "",
		"a/sub/c.yaml":        "",
		"a/not-yaml.txt":      "",
	})

	got, err := binding.ExpandFileGlobs(root, []string{"**/*.yaml"})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"a/.hidden.yaml", "a/sub/c.yaml", "b/x.yaml"}; !slices.Equal(got, want) {
		t.Fatalf("files = %v", got)
	}

	none, err := binding.ExpandFileGlobs(root, nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("none = %v, %v", none, err)
	}

	if _, err := binding.ExpandFileGlobs(filepath.Join(root, "missing"), []string{"*"}); err == nil {
		t.Fatal("want an error for a missing root")
	}
}

func TestLoadSourceRootAcceptsAKnownProjectPrefix(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	body := func(root string) string {
		return header + "  - id: b1\n    architectureRef: calm:node:billing\n    match:\n      any:\n        - sourceRoot: " + root + "\n"
	}

	testutil.WriteTree(t, root, map[string]string{"bindings/ok.yaml": body("'@pay/src/**'"), "bindings/bad.yaml": body("'@nope/src/**'")})

	ws := &workspace.Workspace{
		Root: root, Bindings: workspace.Files{Files: []string{"bindings/ok.yaml"}},
		Observation: workspace.Observation{Projects: []workspace.Project{{Name: "app"}, {Name: "pay"}}},
	}

	got, err := binding.Load(ws, modelWith(calmNode()))
	if err != nil || len(got.Diagnostics) != 0 {
		t.Fatalf("%v %+v", err, got.Diagnostics)
	}

	ws.Bindings.Files = []string{"bindings/bad.yaml"}

	got, err = binding.Load(ws, modelWith(calmNode()))
	if err != nil || len(got.Diagnostics) != 1 {
		t.Fatalf("%v %+v", err, got.Diagnostics)
	}

	problem := got.Diagnostics[0]
	if problem.Code != diag.BND002 || !strings.Contains(problem.Message, "unknown project 'nope' in sourceRoot") {
		t.Fatalf("%v %+v", err, got.Diagnostics)
	}
}
