package views_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// testWorkspace is a workspace that declares the views and one environment `prod`.
func testWorkspace(t *testing.T, defs ...views.Definition) *workspace.Workspace {
	t.Helper()

	return &workspace.Workspace{
		Root:         t.TempDir(),
		Metadata:     workspace.Metadata{Name: "payments"},
		Environments: []workspace.Environment{{ID: "prod", Sources: []string{"deploy/prod/**"}}},
		Views:        defs,
	}
}

// testInputs are the inputs of a run of the standard code graph against the payments model with the given
// protocol of the web-to-orders relationship.
func testInputs(t *testing.T, protocol string, defs ...views.Definition) *views.Inputs {
	t.Helper()

	fns, edges := codeGraph()
	reconcile := codeEvidence(t, fns, edges, projectionBindings())
	model := projectionModel(protocol)
	reconcile.Model = model

	return &views.Inputs{
		Workspace: testWorkspace(t, defs...),
		Model:     model,
		Bindings:  projectionBindings(),
		Observed:  &views.ObservedEvidence{Reconcile: reconcile, Evidence: &observation.EvidenceQueryResult{}},
	}
}

func generate(t *testing.T, in *views.Inputs, view *views.Definition) *views.Generated {
	t.Helper()

	g, err := views.Generate(in, view)
	if err != nil {
		t.Fatal(err)
	}

	return g
}

func mustMarshal(t *testing.T, m *views.Manifest) []byte {
	t.Helper()

	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func provenance() views.Provenance {
	return views.Provenance{
		Revisions: []views.ManifestRevision{{Repository: "payments", Revision: "abc123"}},
		DirtyTree: &views.ManifestDirtyTree{Digest: "sha256:" + strings.Repeat("0", 64)},
	}
}

// asJSONObject is the object that a decoded JSON value holds, or nil.
func asJSONObject(v any) map[string]any {
	obj, _ := v.(map[string]any)

	return obj
}

// asJSONList is the list that a decoded JSON value holds, or nil.
func asJSONList(v any) []any {
	list, _ := v.([]any)

	return list
}

// assertManifestView checks the view section of a decoded manifest of the deployment view `prod-deployment`.
func assertManifestView(t *testing.T, doc map[string]any) {
	t.Helper()

	v := asJSONObject(doc["view"])
	assertEq(t, "view id", v["id"], any("prod-deployment"))
	assertEq(t, "view definitionVersion", v["definitionVersion"], any(views.DefinitionVersion))
	assertEq(t, "view environment", v["environment"], any("prod"))
	assertEq(t, "view kind", v["kind"], any("deployment"))
}

// assertManifestOutputs checks that a decoded manifest lists the DSL of the view and no other output.
func assertManifestOutputs(t *testing.T, doc map[string]any, path, digest string) {
	t.Helper()

	outputs := asJSONList(doc["outputs"])
	requireEq(t, "outputs", len(outputs), 1)

	first := asJSONObject(outputs[0])
	assertEq(t, "output path", first["path"], any(path))
	assertEq(t, "output format", first["format"], any(views.FormatDSL))
	assertEq(t, "output digest", first["digest"], any(digest))
}

// assertNoTimeKey checks that a decoded manifest has no key that names a time.
func assertNoTimeKey(t *testing.T, doc map[string]any) {
	t.Helper()

	for key := range doc {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "time") || strings.Contains(lower, "date") {
			t.Errorf("the manifest has a time key %q", key)
		}
	}
}

// Scenario "Manifest content": the manifest holds the view ID, the definition version, the environment, the
// paths, the artifact digest, the revisions, and the dirty-tree provenance. It has no timestamp.
func TestManifestContent(t *testing.T) {
	t.Parallel()

	view := deploymentView("prod-deployment", "prod")
	view.Output.Path = "docs/architecture/views/prod-deployment.dsl"
	in := testInputs(t, "HTTPS", view)

	g := generate(t, in, &in.Workspace.Views[0])
	manifest := views.BuildManifest(g, &views.ManifestOptions{ToolVersion: "1.2.3", Provenance: provenance()})

	doc := decode(t, mustMarshal(t, manifest))

	assertEq(t, "schema", doc["schema"], any(views.ManifestSchema))
	assertManifestView(t, doc)
	assertManifestOutputs(t, doc, view.Output.Path, g.DSLDigest())
	assertEq(t, "revisions", len(asJSONList(doc["revisions"])), 1)

	if tree := asJSONObject(doc["dirtyTree"]); tree["digest"] == nil {
		t.Errorf("dirtyTree = %v", doc["dirtyTree"])
	}

	assertNoTimeKey(t, doc)
}

// assertObservedDigest checks that a manifest records an observed projection digest.
func assertObservedDigest(t *testing.T, digest *string) {
	t.Helper()

	if digest == nil {
		t.Error("a view that compares against the observed graph has no observed digest")

		return
	}

	if !strings.HasPrefix(*digest, "sha256:") {
		t.Errorf("the observed digest = %q, want a sha256 digest", *digest)
	}
}

// Scenario "Observed projection unavailable": a view that compares only against the declared projection records
// an explicit missing value and not an empty digest.
func TestManifestObservedProjectionUnavailable(t *testing.T) {
	t.Parallel()

	declared := containerView("declared-only")
	declared.Comparison = &views.Comparison{Against: []string{workspace.CompareDeclared}}
	both := containerView("both")
	in := testInputs(t, "HTTPS", declared, both)

	declaredManifest := views.BuildManifest(generate(t, in, &in.Workspace.Views[0]), &views.ManifestOptions{})
	inputs := asJSONObject(decode(t, mustMarshal(t, declaredManifest))["inputs"])

	value, present := inputs["observedProjection"]
	assertEq(t, "observedProjection present", present, true)
	assertEq(t, "observedProjection", value, nil)

	bothManifest := views.BuildManifest(generate(t, in, &in.Workspace.Views[1]), &views.ManifestOptions{})
	assertObservedDigest(t, bothManifest.Inputs.ObservedProjection)

	if declaredManifest.SemanticDigest == bothManifest.SemanticDigest {
		t.Error("a missing observed digest and a present one give the same semantic digest")
	}
}

// Scenario "Dirty tree excludes the view files": the dirty-tree digest does not depend on the view output, the
// exports, and the manifest, and does depend on any other file.
func TestManifestDirtyTreeExcludesViewFiles(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	view.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/architecture/views/payments-container.mmd"}}
	wsp := testWorkspace(t, view)

	tree := func(dirty ...string) string {
		t.Helper()

		got, err := views.DirtyTree(t.Context(), wsp, &workspace.FakeGit{Rev: "abc123", Dirty: dirty})
		if err != nil {
			t.Fatalf("DirtyTree: %v", err)
		}

		if got == nil {
			t.Fatal("DirtyTree: no dirty tree")
		}

		return got.Digest
	}

	base := tree("services/orders/a.go")
	withViewFiles := tree(
		"services/orders/a.go",
		"docs/architecture/views/payments-container.dsl",
		"docs/architecture/views/payments-container.mmd",
		"docs/architecture/views/payments-container.dsl.manifest.json",
	)

	if base != withViewFiles {
		t.Error("the view files change the dirty-tree digest")
	}

	if tree("services/orders/a.go", "docs/architecture/views/other.dsl") == base {
		t.Error("a file that is no view output does not change the dirty-tree digest")
	}

	if tree("services/orders/b.go") == base {
		t.Error("another dirty file gives the same digest")
	}

	none, err := views.DirtyTree(t.Context(), wsp, &workspace.FakeGit{})
	if err != nil {
		t.Errorf("outside a Git repository the dirty tree is nil, got an error %v", err)
	}

	if none != nil {
		t.Errorf("outside a Git repository the dirty tree is nil, got %v", none)
	}
}

// No output and no digest depends on the manifest bytes: the DSL, the digests and the artifact digest of a view
// are the same whatever the manifest says, and the manifest lists no manifest.
func TestManifestIsNotAnInput(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := testInputs(t, "HTTPS", view)

	first := generate(t, in, &in.Workspace.Views[0])
	second := generate(t, in, &in.Workspace.Views[0])

	if !bytes.Equal(first.DSL, second.DSL) {
		t.Fatal("two generations of the same inputs differ in the DSL")
	}

	requireEq(t, "digests of the second generation", second.Digests, first.Digests)

	if bytes.Contains(first.DSL, []byte(views.ManifestSuffix)) {
		t.Error("the DSL refers to the manifest")
	}

	a := views.BuildManifest(first, &views.ManifestOptions{ToolVersion: "1", Provenance: provenance()})
	b := views.BuildManifest(first, &views.ManifestOptions{ToolVersion: "2"})

	assertEq(t, "semantic digest with other provenance", b.SemanticDigest, a.SemanticDigest)
	assertEq(t, "output with other provenance", b.Outputs[0], a.Outputs[0])
	assertEq(t, "inputs with other provenance", b.Inputs, a.Inputs)

	for _, o := range a.Outputs {
		if strings.HasSuffix(o.Path, views.ManifestSuffix) {
			t.Errorf("the manifest lists itself as an output: %s", o.Path)
		}
	}

	// The same bytes in, the same bytes out: the manifest is canonical and no later run adds a timestamp.
	again := views.BuildManifest(first, &views.ManifestOptions{ToolVersion: "1", Provenance: provenance()})
	if !bytes.Equal(mustMarshal(t, a), mustMarshal(t, again)) {
		t.Error("two manifests of the same inputs differ")
	}
}

// Task 4.4: the manifest records the Structurizr version and the confinement when the view has an export, and
// `null` when it has none.
func TestManifestRecordsStructurizrForExports(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := testInputs(t, "HTTPS", view)
	g := generate(t, in, &in.Workspace.Views[0])

	without := asJSONObject(decode(t, mustMarshal(t, views.BuildManifest(g, &views.ManifestOptions{})))["generator"])
	value, present := without["structurizr"]
	assertEq(t, "generator.structurizr present", present, true)
	assertEq(t, "generator.structurizr", value, nil)

	export := views.ExportArtifact{Path: "docs/a.mmd", Format: "mermaid", Data: []byte("graph TB\n"), Digest: views.ArtifactDigest([]byte("graph TB\n")), Canonicalization: "structurizr-text/1"}
	with := views.BuildManifest(g, &views.ManifestOptions{StructurizrVersion: "2025.11.09", Exports: []views.ExportArtifact{export}})

	s := with.Generator.Structurizr
	if s == nil {
		t.Fatal("generator.structurizr = nil, want the version and the confinement")
	}

	assertEq(t, "generator.structurizr version", s.Version, "2025.11.09")
	assertEq(t, "generator.structurizr confinement", s.Confinement, views.ConfinementScrubbedEnv)

	requireEq(t, "outputs", len(with.Outputs), 2)
	assertEq(t, "export path", with.Outputs[1].Path, "docs/a.mmd")
	assertEq(t, "export canonicalization", with.Outputs[1].Canonicalization, "structurizr-text/1")
}

func TestParseManifestIsStrict(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := testInputs(t, "HTTPS", view)
	data := mustMarshal(t, views.BuildManifest(generate(t, in, &in.Workspace.Views[0]), &views.ManifestOptions{Provenance: provenance()}))

	parsed, err := views.ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(mustMarshal(t, parsed), data) {
		t.Error("a manifest does not round-trip")
	}

	if _, err := views.ParseManifest([]byte(`{"schema":"x","trusted":true}`)); err == nil {
		t.Error("an unknown key is accepted")
	}

	if _, err := views.ParseManifest([]byte(`not json`)); err == nil {
		t.Error("text is accepted")
	}
}

func TestGenerateNeedsObservedEvidenceForAnObservedComparison(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := testInputs(t, "HTTPS", view)
	in.Observed = nil

	if _, err := views.Generate(in, &in.Workspace.Views[0]); err == nil {
		t.Fatal("a view that compares against the observed graph was generated with no evidence")
	}

	declared := containerView("declared")
	declared.Comparison = &views.Comparison{Against: []string{workspace.CompareDeclared}}
	in.Workspace.Views = []views.Definition{declared}

	if _, err := views.Generate(in, &in.Workspace.Views[0]); err != nil {
		t.Fatalf("a declared-only view needs no evidence: %v", err)
	}
}

// The path of the manifest of a view and the files that Concordia writes for it.
func TestViewGeneratedPaths(t *testing.T) {
	t.Parallel()

	view := containerView("v")
	view.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/v.mmd"}}

	want := []string{view.Output.Path, "docs/v.mmd", view.Output.Path + ".manifest.json"}
	assertSliceEq(t, "GeneratedPaths", view.GeneratedPaths(), want)

	assertEq(t, "ManifestPath extension", filepath.Ext(view.ManifestPath()), ".json")
}
