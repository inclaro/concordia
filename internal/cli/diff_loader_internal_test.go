package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const diffTrustCALM = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "ledger-db", "node-type": "database", "name": "Ledger DB", "description": "Ledger data",
     "concordia": {"trust": {"classification": "restricted"}}}
  ]
}`

// diffTrustLoader is a diff loader over a workspace that declares the classification that diffTrustCALM uses, with
// the same document in the worktree and at HEAD.
func diffTrustLoader(t *testing.T) *diffLoader {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"architecture/a.calm.json": diffTrustCALM})

	wsp := &workspace.Workspace{
		Root:         dir,
		Path:         filepath.Join(dir, "concordia.yaml"),
		Architecture: workspace.Architecture{Source: "architecture/a.calm.json"},
		Trust:        &workspace.Trust{Classifications: []string{"public", "internal", "restricted"}},
	}
	git := &workspace.FakeGit{
		Rev:     "abc123",
		Refs:    map[string]string{"HEAD": "abc123"},
		FilesAt: map[string]map[string]string{"abc123": {"architecture/a.calm.json": diffTrustCALM}},
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
