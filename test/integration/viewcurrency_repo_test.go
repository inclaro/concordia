package integration_test

import (
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// The fixture of the view-currency tests at one recording: the copy of the fixture, the run of `check`, and the
// inputs of the view checks.

// vcRepo is the fixture at one recording: the run of `check`, the inputs that the view checks read, and the diagram
// evidence that the provider kept.
type vcRepo struct {
	tracked   map[string]string
	run       *conformance.Run
	inputs    *views.Inputs
	reconcile *conformance.ReconcileInput
	diagram   *views.DiagramEvidence
	// dir is the working tree; tracked is the content of the tracked revision by path.
	dir string
	rec observation.Recording
}

// vcEdit replaces text in a file of the fixture, in the working tree and in the tracked revision.
type vcEdit struct{ path, from, to string }

// vcTrackedFixture reads every file of the fixture by path.
func vcTrackedFixture(t *testing.T) map[string]string {
	t.Helper()

	tracked := map[string]string{}

	err := fs.WalkDir(os.DirFS(vcFixtureDir), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		data, readErr := os.ReadFile(filepath.Join(vcFixtureDir, filepath.FromSlash(p)))
		tracked[p] = string(data)

		return readErr
	})
	if err != nil {
		t.Fatalf("list the fixture: %v", err)
	}

	return tracked
}

// applyVCEdits replaces the text of each edit in the tracked revision and in the working tree under dir.
func applyVCEdits(t *testing.T, dir string, tracked map[string]string, edits []vcEdit) {
	t.Helper()

	for _, e := range edits {
		content, ok := tracked[e.path]
		if !ok || !strings.Contains(content, e.from) {
			t.Fatalf("edit of %s: %q not found", e.path, e.from)
		}

		tracked[e.path] = strings.Replace(content, e.from, e.to, 1)

		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(e.path)), []byte(tracked[e.path]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func loadVCRecording(t *testing.T, recordingFile string) observation.Recording {
	t.Helper()

	data, err := os.ReadFile(recordingFile)
	if err != nil {
		t.Fatal(err)
	}

	var rec observation.Recording
	if err = json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("parse the recording: %v", err)
	}

	return rec
}

func newVCRepo(t *testing.T, recordingFile string, edits ...vcEdit) *vcRepo {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(vcFixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	tracked := vcTrackedFixture(t)
	applyVCEdits(t, dir, tracked, edits)

	r := &vcRepo{dir: dir, tracked: tracked, rec: loadVCRecording(t, recordingFile)}
	r.check(t)
	r.observe(t)

	return r
}

// check runs the pipeline of `check` on the working tree and keeps the run.
func (r *vcRepo) check(t *testing.T) {
	t.Helper()

	run, err := conformance.RunCheck(t.Context(), &conformance.CheckOptions{
		Env: conformance.Env{
			Logger: logger.New(logger.Options{Out: io.Discard, RunID: "run", Quiet: true}), RunID: runID,
			WorkspacePath: filepath.Join(r.dir, workspaceFile), Cwd: r.dir, ToolVersion: "test",
		},
		Deps: conformance.Deps{
			CreateClient: func(*workspace.Workspace, *logger.Logger) observation.Client {
				return observation.NewReplayClient(r.rec)
			},
			CreateGit: func(string) workspace.GitPort {
				return &workspace.FakeGit{Rev: r.rec.HeadSHA, Files: slices.Sorted(maps.Keys(r.tracked))}
			},
		},
		SkipReports: true, KeepViewInput: true,
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	r.run = run
}

// fetchEdges fetches the code edges that the views compare and the diagram arrows with the provider adapter.
func (r *vcRepo) fetchEdges(t *testing.T) observation.EvidenceQueryResult {
	t.Helper()

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{
		Client: observation.NewReplayClient(r.rec), ExecutableVersion: r.rec.Version,
	})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: vcProject, GitRevision: r.rec.HeadSHA})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"CALLS", "IMPORTS", "DIAGRAM_ARROW"})
	if err != nil {
		t.Fatalf("fetch the edges: %v", err)
	}

	return res
}

// observe fetches the code edges that the views compare and the diagram arrows with the provider adapter, and makes the
// inputs of the view checks. The check of a workspace without a rule that reads them fetches neither: the fetch of the
// view checks adds their kinds.
func (r *vcRepo) observe(t *testing.T) {
	t.Helper()

	res := r.fetchEdges(t)
	kept := r.run.ViewInput

	nodes := make([]observation.ObservedNode, 0, len(res.Nodes))
	for id := range res.Nodes {
		nodes = append(nodes, res.Nodes[id])
	}

	r.reconcile = &conformance.ReconcileInput{
		Model:                   kept.Reconcile.Model,
		BindingSet:              binding.Resolve(kept.Bindings, nodes, nil),
		Evidence:                conformance.EvidenceSet{Nodes: res.Nodes, Edges: res.Edges},
		SupportedCanonicalKinds: observation.SupportedCanonicalKinds(),
	}

	r.inputs = &views.Inputs{
		Workspace: kept.Workspace, Model: kept.Reconcile.Model, Bindings: kept.Bindings, Policies: kept.Policies,
		Reconciliation: kept.Reconciliation,
		Observed:       &views.ObservedEvidence{Reconcile: r.reconcile, Evidence: &res, Coverage: kept.Coverage},
	}
	r.diagram = views.DiagramEvidenceOf(&res)
}

// view gives the definition of a view of the workspace.
func (r *vcRepo) view(t *testing.T, id string) *views.Definition {
	t.Helper()

	for i := range r.inputs.Workspace.Views {
		if r.inputs.Workspace.Views[i].ID == id {
			return &r.inputs.Workspace.Views[i]
		}
	}

	t.Fatalf("no view %s", id)

	return nil
}

// git is the tracked revision: the fixture, without the given paths.
func (r *vcRepo) git(without ...string) *workspace.FakeGit {
	files := map[string]string{}

	for p, content := range r.tracked {
		if !slices.Contains(without, p) {
			files[p] = content
		}
	}

	return &workspace.FakeGit{Rev: r.rec.HeadSHA, FilesAt: map[string]map[string]string{r.rec.HeadSHA: files}}
}

// gitWith is the tracked revision with the given files added or replaced.
func (r *vcRepo) gitWith(files map[string]string) *workspace.FakeGit {
	all := maps.Clone(r.tracked)
	maps.Copy(all, files)

	return &workspace.FakeGit{Rev: r.rec.HeadSHA, FilesAt: map[string]map[string]string{r.rec.HeadSHA: all}}
}

// projections gives the model projection and the observed projection of a view.
func (r *vcRepo) projections(view *views.Definition) (declared, observed *views.Projection) {
	declared = views.BuildModelProjection(view, &views.ModelInput{Model: r.inputs.Model, Bindings: r.inputs.Bindings})
	observed = views.ObserveProjection(view, &views.ObserveInput{Reconcile: r.reconcile, Bindings: r.inputs.Bindings})

	return declared, observed
}

// generated gives the generated view, and its committed files: the DSL and the manifest.
func (r *vcRepo) generated(t *testing.T, id string) (generated *views.Generated, files map[string]string) {
	t.Helper()

	view := r.view(t, id)

	g, err := views.Generate(r.inputs, view)
	if err != nil {
		t.Fatalf("generate %s: %v", id, err)
	}

	manifest := views.BuildManifest(g, &views.ManifestOptions{ToolVersion: "test"})

	data, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return g, map[string]string{view.Output.Path: string(g.DSL), view.ManifestPath(): string(data)}
}

// vcImageDigestOf is the digest of the image that the correspondence check of an image view is given.
func vcImageDigestOf(id string) string {
	switch id {
	case vcImageView:
		return vcImageDigest
	case vcDepictView:
		return vcDepictedDigest
	}

	return ""
}

// correspondence runs the correspondence check of an authored view on the evidence of the repo.
func (r *vcRepo) correspondence(t *testing.T, id string) views.CorrespondenceResult {
	t.Helper()

	view := r.view(t, id)
	declared, observed := r.projections(view)

	return views.Correspond(&views.CorrespondenceInput{
		View: view, Declared: declared, Observed: observed, Evidence: r.diagram, Model: r.inputs.Model, Bindings: r.inputs.Bindings,
		ImageDigest: vcImageDigestOf(id),
	})
}

// semantic gives the semantic digest of a view, with the projections of the repo.
func (r *vcRepo) semantic(t *testing.T, id string) views.Digests {
	t.Helper()

	view := r.view(t, id)
	declared, observed := r.projections(view)

	digests, err := views.ComputeDigests(&views.DigestInput{
		View: view, Model: r.inputs.Model, ModelProjection: declared, ObservedProjection: observed,
		Bindings: r.inputs.Bindings, Controls: r.inputs.Model.Controls, Policies: r.inputs.Policies,
	})
	if err != nil {
		t.Fatal(err)
	}

	return digests
}

func vcDifferences(r *views.CorrespondenceResult) []string {
	out := make([]string, 0, len(r.Differences))
	for i := range r.Differences {
		d := &r.Differences[i]
		out = append(out, strings.Join([]string{d.Kind, d.Subject, d.Basis, d.Key()}, "|"))
	}

	return out
}

// callsInOrders counts the observed calls between two functions of one file of the orders service.
func callsInOrders(repo *vcRepo) int {
	var n int

	nodes := repo.reconcile.Evidence.Nodes

	for _, e := range repo.reconcile.Evidence.Edges {
		if isOrdersInternalCall(nodes, e) {
			n++
		}
	}

	return n
}

// isOrdersInternalCall is true for a call between two functions of one file of the orders service.
func isOrdersInternalCall(nodes map[string]observation.ObservedNode, e *observation.EvidenceEdge) bool {
	if e.Kind != observation.KindCalls || nodes[e.From].FilePath != nodes[e.To].FilePath {
		return false
	}

	return strings.HasPrefix(nodes[e.From].FilePath, "services/orders/")
}
