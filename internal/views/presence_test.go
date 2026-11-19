package views_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

const presenceRev = "abc123"

func authoredAt(id, path string) views.Definition {
	return views.Definition{
		ID: id, Kind: views.KindContainer, Origin: views.OriginAuthored, Owner: "team", Scope: []string{"payments-system"},
		Abstraction: views.AbstractionContainer,
		Source:      &views.Source{Path: path, Format: "mermaid", Selector: &views.Selector{Title: "Payments containers"}},
	}
}

// committedFiles is the content of the tracked revision of a generated view: the DSL, the exports that the
// definition lists, and the manifest. The bytes are made by the generation of the view, so that a test can
// change one of them.
func committedFiles(t *testing.T, in *views.Inputs, view *views.Definition) map[string]string {
	t.Helper()

	g := generate(t, in, view)
	files := map[string]string{view.Output.Path: string(g.DSL)}

	exports := make([]views.ExportArtifact, 0, len(view.Output.Exports))

	for _, e := range view.Output.Exports {
		data := []byte("export of " + e.Format + "\n")
		files[e.Path] = string(data)
		exports = append(exports, views.ExportArtifact{
			Path: e.Path, Format: e.Format, Data: data, Digest: views.ArtifactDigest(data), Canonicalization: "structurizr-text/1",
		})
	}

	manifest := views.BuildManifest(g, &views.ManifestOptions{ToolVersion: "test", StructurizrVersion: "2025.11.09", Exports: exports, Provenance: provenance()})
	files[view.ManifestPath()] = string(mustMarshal(t, manifest))

	return files
}

func gitAt(files map[string]string) *workspace.FakeGit {
	return &workspace.FakeGit{Rev: presenceRev, FilesAt: map[string]map[string]string{presenceRev: files}}
}

// presence checks the presence of a view in a working tree that is empty.
func presence(t *testing.T, view *views.Definition, git *workspace.FakeGit, indexed ...string) views.PresenceResult {
	t.Helper()

	return views.CheckPresence(t.Context(), &views.PresenceInput{View: view, Git: git, Root: t.TempDir(), IndexedPaths: indexed})
}

func fileOf(t *testing.T, result *views.PresenceResult, p string) views.PresenceFile {
	t.Helper()

	i := slices.IndexFunc(result.Files, func(f views.PresenceFile) bool { return f.Path == p })
	if i < 0 {
		t.Fatalf("no file entry for %s in %+v", p, result.Files)
	}

	return result.Files[i]
}

// assertPresenceReasons checks the status of a presence result and the reasons that it gives.
func assertPresenceReasons(t *testing.T, got *views.PresenceResult, status views.Status, reasons ...string) {
	t.Helper()

	assertEq(t, "status", got.Status, status)
	assertSliceEq(t, "reasons", got.Reasons, reasons)
}

func TestPresencePassesWhenEveryFileIsCommitted(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	view.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/architecture/views/payments-container.mmd"}}
	in := testInputs(t, "HTTPS", view)

	result := presence(t, &in.Workspace.Views[0], gitAt(committedFiles(t, in, &in.Workspace.Views[0])))
	assertEq(t, "status", result.Status, views.StatusPass)
	assertEq(t, "reasons", len(result.Reasons), 0)
	assertEq(t, "files", len(result.Files), 3)

	authored := authoredAt("payments-authored", "docs/arch/payments.md")

	got := presence(t, &authored, gitAt(map[string]string{"docs/arch/payments.md": "# Payments\n"}))
	assertEq(t, "authored view: status", got.Status, views.StatusPass)
	requireEq(t, "authored view: files", len(got.Files) > 0, true)
	assertEq(t, "authored view: role", got.Files[0].Role, views.RoleSource)
}

// A14: CBM reports a diagram for the view, but the declared source is not in the tracked revision.
func TestPresenceA14DiagramOnlyInTheIndexWorkspace(t *testing.T) {
	t.Parallel()

	authored := authoredAt("payments-authored", "docs/arch/payments.md")
	git := gitAt(map[string]string{"README.md": "x"})

	result := presence(t, &authored, git, "docs/arch/payments.md")
	requireEq(t, "status", result.Status, views.StatusViolation)
	requireSliceEq(t, "reasons", result.Reasons, []string{views.ReasonIndexOnly})

	file := fileOf(t, &result, "docs/arch/payments.md")
	assertEq(t, "committed", file.Committed, false)

	if want := "repository listing of revision " + presenceRev + " is complete and does not contain docs/arch/payments.md"; !strings.Contains(file.Detail, want) {
		t.Errorf("detail = %q, want it to contain %q", file.Detail, want)
	}

	// The same file with no CBM report is missing; the cross-check never makes presence pass.
	plain := presence(t, &authored, git)
	assertEq(t, "without the index: status", plain.Status, views.StatusViolation)
	requireEq(t, "without the index: reasons", len(plain.Reasons) > 0, true)
	assertEq(t, "without the index: first reason", plain.Reasons[0], views.ReasonMissing)
}

// writeUntrackedFile writes a file of the working tree that the Git revision does not track.
func writeUntrackedFile(t *testing.T, root, slashPath, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(slashPath))

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Scenario "Working-tree file is not committed".
func TestPresenceUntrackedFileIsNotCommitted(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := testInputs(t, "HTTPS", view)
	files := committedFiles(t, in, &in.Workspace.Views[0])
	delete(files, view.Output.Path)

	root := t.TempDir()
	writeUntrackedFile(t, root, view.Output.Path, "workspace {}\n")

	result := views.CheckPresence(t.Context(), &views.PresenceInput{View: &in.Workspace.Views[0], Git: gitAt(files), Root: root})
	requireEq(t, "status", result.Status, views.StatusViolation)
	requireSliceEq(t, "reasons", result.Reasons, []string{views.ReasonNotCommitted})

	file := fileOf(t, &result, view.Output.Path)
	assertEq(t, "committed", file.Committed, false)
	assertEq(t, "reason", file.Reason, views.ReasonNotCommitted)
}

// Scenario "Manifest names another view".
func TestPresenceViewIDMismatch(t *testing.T) {
	t.Parallel()

	other := containerView("other-view")
	view := containerView("payments-container")
	view.Output.Path = other.Output.Path
	in := testInputs(t, "HTTPS", other, view)

	// The committed manifest belongs to `other-view`; the definition at the same path is `payments-container`.
	files := committedFiles(t, in, &in.Workspace.Views[0])

	result := presence(t, &in.Workspace.Views[1], gitAt(files))
	assertPresenceReasons(t, &result, views.StatusViolation, views.ReasonViewIDMismatch)

	// A manifest with another scope is a scope mismatch, a manifest that cannot be read is not valid.
	scoped := in.Workspace.Views[1]
	scoped.Scope = []string{"ledger"}

	got := presence(t, &scoped, gitAt(committedFiles(t, in, &in.Workspace.Views[1])))
	assertSliceEq(t, "scope: reasons", got.Reasons, []string{views.ReasonScopeMismatch})

	broken := committedFiles(t, in, &in.Workspace.Views[1])
	broken[in.Workspace.Views[1].ManifestPath()] = "{"

	got = presence(t, &in.Workspace.Views[1], gitAt(broken))
	assertSliceEq(t, "invalid manifest: reasons", got.Reasons, []string{views.ReasonManifestInvalid})
}

// Scenario "Workspace is not a Git repository": presence is unknown, never pass or violation.
func TestPresenceUnknownOutsideAGitRepository(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := testInputs(t, "HTTPS", view)

	result := presence(t, &in.Workspace.Views[0], &workspace.FakeGit{})
	assertEq(t, "status", result.Status, views.StatusUnknown)
	assertEq(t, "files", len(result.Files), 0)

	requireEq(t, "reasons", len(result.Reasons), 1)

	if !strings.HasPrefix(result.Reasons[0], views.ReasonNoRevision) {
		t.Errorf("reason = %q, want the prefix %q", result.Reasons[0], views.ReasonNoRevision)
	}
}

func TestDiagramPathsListsTheFilesOfDiagramEvidence(t *testing.T) {
	t.Parallel()

	nodes := map[string]observation.ObservedNode{
		"1": {Label: "Diagram", FilePath: "docs/arch/payments.md"},
		"2": {Label: "DiagramElement", FilePath: "docs/arch/payments.md"},
		"3": {Label: "Function", FilePath: "services/orders/a.go"},
		"4": {Label: "DiagramElement", FilePath: "docs/arch/orders.md"},
	}

	assertSliceEq(t, "DiagramPaths", views.DiagramPaths(nodes), []string{"docs/arch/orders.md", "docs/arch/payments.md"})
}
