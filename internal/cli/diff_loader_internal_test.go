package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	diffControlURL  = "https://example.org/controls/data-ownership/1.0/requirement.json"
	diffRequirement = "architecture/resources/requirement.json"
)

// diffTrustFiles are the workspace files in the worktree and at HEAD: a CALM document with a restricted ledger-db
// whose control requirement URL is URL-mapped to diffRequirement.
var diffTrustFiles = map[string]string{
	"architecture/a.calm.json": `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "ledger-db", "node-type": "database", "name": "Ledger DB", "description": "Ledger data",
     "concordia": {"trust": {"classification": "restricted"}},
     "controls": {"ctl-ownership": {"description": "The ledger owns its data.", "requirements": [
       {"requirement-url": "` + diffControlURL + `", "config": {"control-id": "ctl-ownership", "engine": "concordia", "rule-ids": ["DATA-001"]}}
     ]}}}
  ],
  "relationships": []
}`,
	"architecture/url-mapping.json": `{"` + diffControlURL + `": "resources/requirement.json"}`,
	diffRequirement:                 `{"description": "data ownership requirement"}`,
}

// diffTrustLoader is a diff loader over a workspace that declares the classification and the URL mapping that
// diffTrustFiles use, with the same files in the worktree and at HEAD.
func diffTrustLoader(t *testing.T) *diffLoader {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, diffTrustFiles)

	wsp := &workspace.Workspace{
		Root:         dir,
		Path:         filepath.Join(dir, "concordia.yaml"),
		Architecture: workspace.Architecture{Source: "architecture/a.calm.json", URLMapping: "architecture/url-mapping.json"},
		Trust:        &workspace.Trust{Classifications: []string{"public", "internal", "restricted"}},
	}
	git := &workspace.FakeGit{
		Rev:     "abc123",
		Refs:    map[string]string{"HEAD": "abc123"},
		FilesAt: map[string]map[string]string{"abc123": diffTrustFiles},
	}
	app := &Context{Deps: Deps{NewGit: func(string) workspace.GitPort { return git }}}

	loader := &diffLoader{app: app, wsp: wsp}
	t.Cleanup(loader.cleanup)

	return loader
}

// wantRestricted stops the test unless the model keeps the restricted classification of ledger-db.
func wantRestricted(t *testing.T, side string, model *intent.ArchitectureModel) {
	t.Helper()

	if len(model.Nodes) != 1 {
		t.Fatalf("%s: nodes = %+v, want ledger-db only", side, model.Nodes)
	}

	if got := model.Nodes[0].Trust; got == nil || got.Classification != "restricted" {
		t.Fatalf("%s: ledger-db lost its workspace-declared classification: %+v", side, got)
	}
}

func TestDiffSidesKeepAWorkspaceDeclaredClassification(t *testing.T) {
	t.Parallel()

	loader := diffTrustLoader(t)

	ref, err := loader.gitRef(context.Background(), "HEAD")
	if err != nil {
		t.Fatalf("git ref side: %v", err)
	}

	wantRestricted(t, "git ref", ref.model)

	worktree, err := loader.worktreeModel(context.Background(), loader.wsp)
	if err != nil {
		t.Fatalf("worktree side: %v", err)
	}

	wantRestricted(t, "worktree", worktree)
}

// The git ref side reads the URL-mapped files of the revision, so that its mapped references resolve.
func TestDiffGitRefSideReadsTheMappedFilesOfTheRevision(t *testing.T) {
	t.Parallel()

	loader := diffTrustLoader(t)

	if _, err := loader.gitRef(context.Background(), "HEAD"); err != nil {
		t.Fatalf("git ref side: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(loader.tmps[0], filepath.FromSlash(diffRequirement)))
	if err != nil || string(got) != diffTrustFiles[diffRequirement] {
		t.Fatalf("mapped requirement of the revision = %q, %v", got, err)
	}
}

// A bundle written before bundles held the URL mapping has the workspace key but not the file: its architecture loads
// with an empty mapping, as before.
func TestDiffBundleSideWithoutTheMappingFileLoads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"architecture/a.calm.json": diffTrustFiles["architecture/a.calm.json"]})

	wsp := &workspace.Workspace{
		Root:         dir,
		Architecture: workspace.Architecture{Source: "architecture/a.calm.json", URLMapping: "architecture/url-mapping.json"},
		Trust:        &workspace.Trust{Classifications: []string{"public", "internal", "restricted"}},
	}

	model, err := bundleModel(context.Background(), wsp)
	if err != nil {
		t.Fatalf("bundle side: %v", err)
	}

	wantRestricted(t, "bundle", model)
}

// bundlePatternURL is the `$schema` pattern URL of the CALM document of a bundle in the confinement cases.
const bundlePatternURL = "https://example.org/patterns/bundle.json"

// bundlePattern is a CALM pattern that accepts any document.
const bundlePattern = `{"$schema": "https://calm.finos.org/release/1.2/meta/calm.json", "$id": "` + bundlePatternURL + `", "type": "object"}`

// bundleWithMappedPattern writes a bundle workspace below a directory whose file outside.json, outside the bundle,
// holds the pattern as a sentinel, and maps the pattern URL with the entry that entry gives for the sentinel path.
func bundleWithMappedPattern(t *testing.T, entry func(outside string) string) *workspace.Workspace {
	t.Helper()

	outer := t.TempDir()
	outside := filepath.Join(outer, "outside.json")
	testutil.WriteTree(t, outer, map[string]string{"outside.json": bundlePattern})

	root := filepath.Join(outer, "bundle")
	testutil.WriteTree(t, root, map[string]string{
		"architecture/a.calm.json":          `{"$schema": "` + bundlePatternURL + `", "nodes": [{"unique-id": "a", "node-type": "service", "name": "A", "description": "A"}], "relationships": []}`,
		"architecture/url-mapping.json":     `{"` + bundlePatternURL + `": ` + strconv.Quote(entry(outside)) + `}`,
		"architecture/patterns/inside.json": bundlePattern,
	})

	return &workspace.Workspace{
		Root:         root,
		Architecture: workspace.Architecture{Source: "architecture/a.calm.json", URLMapping: "architecture/url-mapping.json"},
	}
}

// A bundle is untrusted input: a mapping entry that leaves the bundle (absolute or `..`) is not read, so its URL stays
// unmapped and the CALM document that needs it does not load. An entry inside the bundle still resolves.
func TestDiffBundleSideReadsNoMappedFileOutsideTheBundle(t *testing.T) {
	t.Parallel()

	outsideEntries := map[string]func(string) string{
		"absolute": func(outside string) string { return outside },
		"dot-dot":  func(string) string { return "../../outside.json" },
	}

	for name, entry := range outsideEntries {
		wantUnmapped(t, name, bundleWithMappedPattern(t, entry))
	}

	inside := bundleWithMappedPattern(t, func(string) string { return "patterns/inside.json" })
	if _, err := bundleModel(context.Background(), inside); err != nil {
		t.Fatalf("an entry inside the bundle: %v", err)
	}

	linked := bundleWithMappedPattern(t, func(string) string { return "patterns/link.json" })
	if err := os.Symlink(filepath.Join(filepath.Dir(linked.Root), "outside.json"),
		filepath.Join(linked.Root, "architecture", "patterns", "link.json")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	wantUnmapped(t, "symbolic link", linked)
}

// wantUnmapped reports a bundle side that loads: its mapping entry leaves the bundle, so the pattern URL is unmapped.
func wantUnmapped(t *testing.T, name string, wsp *workspace.Workspace) {
	t.Helper()

	_, err := bundleModel(context.Background(), wsp)
	if err == nil || !strings.Contains(err.Error(), "has no entry in architecture.urlMapping") {
		t.Errorf("%s entry: err = %v, want the URL unmapped", name, err)
	}
}
