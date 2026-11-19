package integration_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/workspace"
)

// infraWorkspace is a temporary copy of the infra fixture with the workspace files of a test and the parsed recording
// that a replay client answers from. It is the one place that knows what a check on the fixture needs.
type infraWorkspace struct {
	rec observation.Recording
	dir string
	// files are the files of the fixture, relative to its root: the list that FakeGit reports.
	files []string
}

// deploysWorkspaceFiles are the workspace files of the deploys tests (A43) with the given workspace text.
func deploysWorkspaceFiles(workspaceText string) map[string]string {
	return map[string]string{
		"concordia.yaml":                   workspaceText,
		"architecture/infra.calm.json":     deploysArchitecture,
		"bindings/concordia.bindings.yaml": deploysBindings,
		"policies/deploys.policy.yaml":     deploysPolicy,
	}
}

// newInfraWorkspace copies the infra fixture, writes the files (relative path to text) over the copy, and parses the
// recording file. It runs no check.
func newInfraWorkspace(t *testing.T, recordingFile string, files map[string]string) *infraWorkspace {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(deploysFixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	var fixtureFiles []string

	err := fs.WalkDir(os.DirFS(deploysFixtureDir), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			fixtureFiles = append(fixtureFiles, p)
		}

		return walkErr
	})
	if err != nil {
		t.Fatalf("list the fixture: %v", err)
	}

	for rel, text := range files {
		writeDeploysFile(t, dir, rel, text)
	}

	var rec observation.Recording
	if err = json.Unmarshal(mustRead(t, recordingFile), &rec); err != nil {
		t.Fatalf("parse the recording: %v", err)
	}

	return &infraWorkspace{rec: rec, dir: dir, files: fixtureFiles}
}

// checkOptions gives the options of a check of the workspace with a replay client and a fake git. The edits run last.
func (w *infraWorkspace) checkOptions(log *logger.Logger, edits ...func(*conformance.CheckOptions)) *conformance.CheckOptions {
	opts := &conformance.CheckOptions{
		Env: conformance.Env{
			Logger:        log,
			RunID:         runID,
			WorkspacePath: filepath.Join(w.dir, "concordia.yaml"),
			Cwd:           w.dir,
			ToolVersion:   "test",
		},
		Deps: conformance.Deps{
			CreateClient: func(*workspace.Workspace, *logger.Logger) observation.Client {
				return observation.NewReplayClient(w.rec)
			},
			CreateGit:    func(string) workspace.GitPort { return &workspace.FakeGit{Rev: w.rec.HeadSHA, Files: w.files} },
			RenderReport: reporting.Renderer(reporting.PrettyOptions{}),
		},
	}

	for _, edit := range edits {
		if edit != nil {
			edit(opts)
		}
	}

	return opts
}
