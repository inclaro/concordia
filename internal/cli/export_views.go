package cli

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// The names below the directory `views/<id>/` of the bundle.
const (
	viewManifestFile   = "manifest.json"
	viewProjectionFile = "projection.json"
	viewArtifactsDir   = "artifacts/"
)

// viewProjectionDocument is `views/<id>/projection.json`: the semantic projection of the run for one view.
// Observed is null when the view compares only against the declared projection.
type viewProjectionDocument struct {
	Model              *views.Projection `json:"model"`
	Observed           *views.Projection `json:"observed"`
	SemanticDigest     string            `json:"semanticDigest"`
	DigestContract     string            `json:"digestContract"`
	ProjectionContract string            `json:"projectionContract"`
}

// viewNeed is one committed file that a declared view needs: its role, its repository path, and the bundle
// path that holds its content.
type viewNeed struct {
	role, repoPath, bundlePath string
}

// viewNeeds lists the committed files of a view in a fixed order: the manifest of a generated view, then its
// output and exports, or the source of an authored view. Artifacts go below `artifacts/` at their repository
// path, the manifest at `manifest.json`.
func viewNeeds(view *views.Definition) []viewNeed {
	dir := bundle.ViewsDir + view.ID + "/"
	artifact := func(role, p string) viewNeed {
		clean := path.Clean(filepath.ToSlash(p))

		return viewNeed{role: role, repoPath: clean, bundlePath: dir + viewArtifactsDir + clean}
	}

	switch {
	case view.Origin == views.OriginAuthored && view.Source != nil:
		return []viewNeed{artifact(views.RoleSource, view.Source.Path)}
	case view.Origin == views.OriginGenerated && view.Output != nil:
		manifest := path.Clean(filepath.ToSlash(view.ManifestPath()))
		needs := []viewNeed{{role: views.RoleManifest, repoPath: manifest, bundlePath: dir + viewManifestFile}}
		needs = append(needs, artifact(views.RoleOutput, view.Output.Path))

		for _, e := range view.Output.Exports {
			needs = append(needs, artifact(views.RoleExport, e.Path))
		}

		return needs
	default:
		return nil
	}
}

// addViews adds the files of the declared views to the bundle (design D14): the committed manifest of a generated
// view, its artifacts at their repository paths, and the semantic projection of the run. The committed bytes are
// read from the tracked revision, never from the working tree. A declared file that the tracked revision does not
// hold has no content and is listed with the state `absent`. A workspace without declared views adds nothing.
func addViews(ctx context.Context, app *Context, wsp *workspace.Workspace, run *conformance.Run, byPath map[string][]byte) ([]bundle.View, error) {
	if len(wsp.Views) == 0 {
		return nil, nil
	}

	if run.ViewInput == nil {
		return nil, diag.Fail(diag.REP004, diag.Detail("the run kept no view input for the declared views"))
	}

	git := app.Git(wsp.Root)

	revision, err := git.Revision(ctx)
	if err != nil {
		return nil, fmt.Errorf("export the views: read the revision: %w", err)
	}

	inputs := views.InputsOf(run.ViewInput)
	defs := slices.Clone(wsp.Views)
	slices.SortFunc(defs, func(a, b views.Definition) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	out := make([]bundle.View, 0, len(defs))

	for i := range defs {
		entry, err := addView(ctx, app, git, revision, inputs, &defs[i], byPath)
		if err != nil {
			return nil, err
		}

		out = append(out, entry)
	}

	return out, nil
}

func addView(ctx context.Context, app *Context, git workspace.GitPort, revision string, inputs *views.Inputs, view *views.Definition, byPath map[string][]byte) (bundle.View, error) {
	entry := bundle.View{ID: view.ID, Origin: string(view.Origin), State: bundle.StatePresent, Files: []bundle.ViewFile{}}

	for _, need := range viewNeeds(view) {
		file := bundle.ViewFile{Role: need.role, Path: need.repoPath, State: bundle.StateAbsent}

		data, found, err := committedViewFile(ctx, app, git, revision, need.repoPath)
		if err != nil {
			return bundle.View{}, err
		}

		if found {
			file.State, file.File, file.SHA256 = bundle.StatePresent, need.bundlePath, digest.OfBytes(data)
			byPath[need.bundlePath] = data
		} else {
			entry.State = bundle.StateAbsent
		}

		entry.Files = append(entry.Files, file)
	}

	snapshot, err := views.SnapshotOf(inputs, view)
	if err != nil {
		return bundle.View{}, fmt.Errorf("export the view %s: %w", view.ID, err)
	}

	data, err := canonjson.Marshal(viewProjectionDocument{
		Model: snapshot.Model, Observed: snapshot.Observed, SemanticDigest: snapshot.Digests.Semantic,
		DigestContract: views.DigestContract, ProjectionContract: views.ProjectionContract,
	})
	if err != nil {
		return bundle.View{}, fmt.Errorf("encode the projection of view %s: %w", view.ID, err)
	}

	file := bundle.ViewFile{Role: bundle.RoleProjection, State: bundle.StatePresent, File: bundle.ViewsDir + view.ID + "/" + viewProjectionFile, SHA256: digest.OfBytes(data)}
	byPath[file.File] = data
	entry.Files = append(entry.Files, file)

	return entry, nil
}

// committedViewFile reads a file of a view from the tracked revision. With no tracked revision, or for a path
// that leaves the repository, nothing is committed: found is false (the second case also gives CO-REP-006).
func committedViewFile(ctx context.Context, app *Context, git workspace.GitPort, revision, repoPath string) (data []byte, found bool, err error) {
	if revision == "" {
		return nil, false, nil
	}

	if repoPath == ".." || strings.HasPrefix(repoPath, "../") || path.IsAbs(repoPath) {
		app.Err(diag.FormatDiagnostic(diag.New(diag.REP006, diag.Params{keyPath: repoPath, keyDetail: "the path of a view file is outside the workspace root"})) + "\n")

		return nil, false, nil
	}

	data, found, err = git.ShowFile(ctx, revision, repoPath)
	if err != nil {
		return nil, false, fmt.Errorf("read %s at revision %s: %w", repoPath, revision, err)
	}

	return data, found, nil
}
