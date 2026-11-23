package cli

import (
	"context"
	"os"
	"path/filepath"
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
