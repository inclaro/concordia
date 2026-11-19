package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	viewsRevision    = "abc123"
	viewAuthoredPath = "docs/arch/payments.md"
)

// committedViews renders the views of the setup, gives the rendered files, and makes the tracked revision hold the
// files of `committed` (a subset of the rendered files and of the authored source) with their rendered bytes.
func committedViews(t *testing.T, s *chkSetup, committed ...string) map[string]string {
	t.Helper()

	if res := s.run(t, "render", "--views"); res.Code != 0 {
		t.Fatalf("render: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	rendered := map[string]string{}
	for _, f := range []string{viewAPath, viewAPath + ".manifest.json", viewBPath, viewBPath + ".manifest.json", viewAuthoredPath} {
		rendered[f] = readFile(t, s.dir, f)
	}

	tree := map[string]string{}
	for _, f := range committed {
		tree[f] = rendered[f]
	}

	s.cfg.Deps.NewGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{Rev: viewsRevision, FilesAt: map[string]map[string]string{viewsRevision: tree}}
	}

	return rendered
}

// exportedViews exports a bundle, verifies it, and gives its manifest and the extraction directory.
func exportedViews(t *testing.T, s *chkSetup) (manifest *bundle.Manifest, dir string) {
	t.Helper()

	out := exportedBundle(t, s)

	if _, err := bundle.Verify(out); err != nil {
		t.Fatalf("verify: %v", err)
	}

	dir = t.TempDir()

	manifest, err := bundle.Open(out, dir)
	if err != nil {
		t.Fatal(err)
	}

	return manifest, dir
}

func viewEntry(t *testing.T, manifest *bundle.Manifest, id string) bundle.View {
	t.Helper()

	for _, v := range manifest.Views {
		if v.ID == id {
			return v
		}
	}

	t.Fatalf("the bundle does not list the view %s: %+v", id, manifest.Views)

	return bundle.View{}
}

func viewFile(t *testing.T, v bundle.View, role string) bundle.ViewFile {
	t.Helper()

	for _, f := range v.Files {
		if f.Role == role {
			return f
		}
	}

	t.Fatalf("view %s lists no %s file: %+v", v.ID, role, v.Files)

	return bundle.ViewFile{}
}

// Scenario "Bundle content": the bundle holds the committed manifest, the committed artifacts at their repository
// paths and the semantic projection of each view. The committed bytes are read from the tracked revision, so an edit
// of the working tree does not enter the bundle.
func TestExportBundleHoldsViewManifestArtifactsAndProjection(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())
	rendered := committedViews(t, s, viewAPath, viewAPath+".manifest.json", viewBPath, viewBPath+".manifest.json", viewAuthoredPath)

	if err := os.WriteFile(filepath.Join(s.dir, filepath.FromSlash(viewAPath)), []byte("edited in the working tree"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, dir := exportedViews(t, s)

	ids := make([]string, 0, len(manifest.Views))
	for _, v := range manifest.Views {
		ids = append(ids, v.ID)
	}

	if !slices.Equal(ids, []string{"payments-context", "view-a", "view-b"}) {
		t.Fatalf("views = %+v", manifest.Views)
	}

	for _, v := range manifest.Views {
		if v.State != bundle.StatePresent {
			t.Errorf("view %s is %s", v.ID, v.State)
		}

		wantViewProjection(t, dir, v)
	}

	wantViewAArtifacts(t, dir, viewEntry(t, manifest, "view-a"), rendered)

	if got := readExtractedViewFile(t, dir, "views/payments-context/artifacts/"+viewAuthoredPath); got != rendered[viewAuthoredPath] {
		t.Errorf("source of the authored view = %q", got)
	}

	if src := viewFile(t, viewEntry(t, manifest, "payments-context"), "source"); src.Path != viewAuthoredPath {
		t.Errorf("source entry: %+v", src)
	}
}

// readExtractedViewFile reads a file of the extracted bundle.
func readExtractedViewFile(t *testing.T, dir, p string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// wantViewProjection checks the projection file of a view: its entry, its keys, the model, and the semantic digest.
func wantViewProjection(t *testing.T, dir string, v bundle.View) {
	t.Helper()

	proj := viewFile(t, v, bundle.RoleProjection)
	if proj.File != "views/"+v.ID+"/projection.json" || proj.State != bundle.StatePresent {
		t.Errorf("projection of %s: %+v", v.ID, proj)
	}

	text := readExtractedViewFile(t, dir, proj.File)

	var doc map[string]json.RawMessage

	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("projection of %s: %v", v.ID, err)
	}

	for _, key := range []string{"model", "observed", "semanticDigest", "digestContract", "projectionContract"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("projection of %s has no %s: %s", v.ID, key, text)
		}
	}

	if string(doc["model"]) == "null" || !strings.HasPrefix(string(doc["semanticDigest"]), `"sha256:`) {
		t.Errorf("projection of %s: %s", v.ID, text)
	}
}

// wantViewAArtifacts checks the manifest and the artifact of view-a: the committed files, not the working tree.
func wantViewAArtifacts(t *testing.T, dir string, a bundle.View, rendered map[string]string) {
	t.Helper()

	if got := readExtractedViewFile(t, dir, "views/view-a/manifest.json"); got != rendered[viewAPath+".manifest.json"] {
		t.Errorf("manifest of view-a = %q", got)
	}

	// The artifact is the committed DSL, not the edited working tree file.
	if got := readExtractedViewFile(t, dir, "views/view-a/artifacts/"+viewAPath); got != rendered[viewAPath] {
		t.Errorf("artifact of view-a = %q, want the committed DSL", got)
	}

	if out := viewFile(t, a, "output"); out.Path != viewAPath || out.File != "views/view-a/artifacts/"+viewAPath {
		t.Errorf("output entry: %+v", out)
	}

	if man := viewFile(t, a, "manifest"); man.Path != viewAPath+".manifest.json" || man.State != bundle.StatePresent {
		t.Errorf("manifest entry: %+v", man)
	}
}

// Scenario "Absent artifact": the declared files that the tracked revision lacks are listed with the state `absent`
// and no content; the view is `absent`; the projection of the run is still in the bundle.
func TestExportBundleListsAnAbsentViewArtifact(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())
	// view-b was rendered, not committed; the working tree holds its files.
	committedViews(t, s, viewAPath, viewAPath+".manifest.json", viewAuthoredPath)

	manifest, dir := exportedViews(t, s)

	if got := viewEntry(t, manifest, "view-a").State; got != bundle.StatePresent {
		t.Errorf("view-a is %s", got)
	}

	b := viewEntry(t, manifest, "view-b")
	if b.State != bundle.StateAbsent {
		t.Errorf("view-b is %s", b.State)
	}

	for role, path := range map[string]string{"manifest": viewBPath + ".manifest.json", "output": viewBPath} {
		if f := viewFile(t, b, role); !isAbsentViewFile(&f, path) {
			t.Errorf("%s entry: %+v", role, f)
		}
	}

	if proj := viewFile(t, b, bundle.RoleProjection); proj.State != bundle.StatePresent {
		t.Errorf("projection entry: %+v", proj)
	}

	for _, p := range []string{"views/view-b/manifest.json", "views/view-b/artifacts/" + viewBPath} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
			t.Errorf("the bundle holds %s, which the tracked revision lacks", p)
		}
	}
}

// isAbsentViewFile says whether a view file is listed as absent at the path, with no bundle file and no digest.
func isAbsentViewFile(f *bundle.ViewFile, path string) bool {
	if f.State != bundle.StateAbsent || f.Path != path {
		return false
	}

	return f.File == "" && f.SHA256 == ""
}

// With no tracked revision no declared file is committed: every file is listed as absent.
func TestExportBundleWithoutARevisionListsEveryViewFileAbsent(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, twoViews())

	s.cfg.Deps.NewGit = func(string) workspace.GitPort { return &workspace.FakeGit{} }

	manifest, _ := exportedViews(t, s)

	for _, v := range manifest.Views {
		if v.State != bundle.StateAbsent {
			t.Errorf("view %s is %s", v.ID, v.State)
		}

		wantViewFilesAbsent(t, v)
	}
}

// wantViewFilesAbsent reports each file of the view, except the projection, that is not listed as absent.
func wantViewFilesAbsent(t *testing.T, v bundle.View) {
	t.Helper()

	for _, f := range v.Files {
		if f.Role != bundle.RoleProjection && f.State != bundle.StateAbsent {
			t.Errorf("view %s: %+v", v.ID, f)
		}
	}
}

// A workspace without views makes the bundle of before: no `views` key.
func TestExportBundleWithoutViewsHasNoViewEntries(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	manifest, dir := exportedViews(t, s)
	if len(manifest.Views) != 0 {
		t.Errorf("views = %+v", manifest.Views)
	}

	text, err := os.ReadFile(filepath.Join(dir, bundle.ManifestPath))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(text), `"views"`) {
		t.Errorf("manifest has a views key")
	}

	for _, f := range manifest.Files {
		if strings.HasPrefix(f.Path, bundle.ViewsDir) {
			t.Errorf("unexpected file %s", f.Path)
		}
	}
}
