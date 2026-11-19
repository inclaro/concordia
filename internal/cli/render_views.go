package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// renderViewsOptions checks that no option of the ad-hoc render is combined with --views, and that --view-id
// needs --views.
func renderViewsOptions(opts *renderOptions, format string) error {
	if !opts.views {
		if opts.viewID != "" {
			return diag.Fail(diag.WRK010, diag.Detail("--view-id needs --views"))
		}

		return nil
	}

	for _, conflict := range []struct{ name, value string }{
		{"--view", opts.view},
		{"--focus", opts.focus},
		{"--architecture", opts.architecture},
		{"--workspace-out", opts.workspaceOut},
		{"--results", opts.results},
		{"--finding", opts.finding},
		{"--format", format},
	} {
		if conflict.value != "" {
			return diag.Fail(diag.WRK010, diag.Detail(conflict.name+" does not apply to --views: the views are written as their definitions declare"))
		}
	}

	return nil
}

// selectViews gives the generated views that `render --views` writes: the one that --view-id names, or all.
func selectViews(wsp *workspace.Workspace, viewID string) ([]*views.Definition, error) {
	if viewID != "" {
		i := slices.IndexFunc(wsp.Views, func(v views.Definition) bool { return v.ID == viewID })
		if i < 0 {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unknown --view-id '%s' (the workspace declares %d views)", viewID, len(wsp.Views))))
		}

		if wsp.Views[i].Origin != views.OriginGenerated {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("view '%s' is authored: render --views writes generated views only", viewID)))
		}

		return []*views.Definition{&wsp.Views[i]}, nil
	}

	var out []*views.Definition

	for i := range wsp.Views {
		if wsp.Views[i].Origin == views.OriginGenerated {
			out = append(out, &wsp.Views[i])
		}
	}

	return out, nil
}

// viewFile is a file that `render --views` writes.
type viewFile struct {
	path string
	data []byte
}

// renderViewsHandler writes the artifact, the exports and the manifest of each generated view at the declared
// paths (design D6). It validates every definition first (CO-DGM-001, CO-DGM-002), builds every file in memory,
// and writes only then, so an invalid path or a failed export writes no file.
func renderViewsHandler(ctx context.Context, app *Context, opts *renderOptions) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	selected, err := selectViews(wsp, opts.viewID)
	if err != nil {
		return err
	}

	if len(wsp.Views) == 0 {
		return diag.Fail(diag.WRK010, diag.Detail("the workspace declares no views"))
	}

	model, err := loadRenderModel(ctx, app, wsp, opts)
	if err != nil {
		return err
	}

	if err := validateViewsForRender(ctx, app, wsp, model); err != nil {
		return err
	}

	if len(selected) == 0 {
		app.Err("no generated view to render\n")

		return nil
	}

	// render writes the views that check verifies: it does not evaluate them.
	deps := checkDeps(app)
	deps.EvaluateViews = nil

	run, err := conformance.RunCheck(ctx, &conformance.CheckOptions{Env: checkEnv(app), Deps: deps, SkipReports: true, KeepViewInput: true})
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	files, err := buildViewFiles(ctx, app, wsp, views.InputsOf(run.ViewInput), selected)
	if err != nil {
		return err
	}

	for _, f := range files {
		if err := fsutil.WriteFileAtomic(filepath.Join(wsp.Root, filepath.FromSlash(f.path)), f.data); err != nil {
			return diag.Fail(diag.REP001, diag.Params{keyPath: f.path, keyDetail: err.Error()})
		}

		app.Err("view written: " + f.path + "\n")
	}

	return nil
}

// validateViewsForRender runs views.Diagnose: an error diagnostic (a definition or an output path that is not
// valid) stops the command with its exit code before any file is written; the other diagnostics are printed.
func validateViewsForRender(ctx context.Context, app *Context, wsp *workspace.Workspace, model *intent.ArchitectureModel) error {
	found, err := views.Diagnose(ctx, wsp, model, app.Git(wsp.Root))
	if err != nil {
		return fmt.Errorf("check the views: %w", err)
	}

	if err := policy.ThrowOnErrors(found); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	for _, d := range found {
		app.Err(diag.FormatDiagnostic(d) + "\n")
	}

	return nil
}

// buildViewFiles builds every file of the selected views: the DSL, the exports and the manifest.
func buildViewFiles(ctx context.Context, app *Context, wsp *workspace.Workspace, inputs *views.Inputs, selected []*views.Definition) ([]viewFile, error) {
	provenance, err := views.CollectProvenance(ctx, wsp, func(root string) workspace.GitPort { return app.Git(root) })
	if err != nil {
		return nil, fmt.Errorf("collect the provenance: %w", err)
	}

	tools := &views.Tools{Getenv: app.IO.Getenv, WorkspaceRoot: wsp.Root, AllowIncludes: wsp.Projection.AllowIncludes}

	var files []viewFile

	for _, view := range selected {
		generated, err := views.Generate(inputs, view)
		if err != nil {
			return nil, fmt.Errorf("render view %s: %w", view.ID, err)
		}

		for _, d := range generated.Diagnostics {
			app.Err(diag.FormatDiagnostic(d) + "\n")
		}

		exports := views.RenderExports(ctx, generated, tools)

		for _, outcome := range exports.Outcomes {
			if outcome.Err != nil {
				return nil, outcome.Err //nolint:wrapcheck // a *diag.Error that the caller reports.
			}
		}

		artifacts := make([]views.ExportArtifact, len(exports.Outcomes))
		for i := range exports.Outcomes {
			artifacts[i] = exports.Outcomes[i].Artifact
		}

		manifest, err := views.BuildManifest(generated, &views.ManifestOptions{
			ToolVersion: app.Build.Version, StructurizrVersion: exports.Version, Exports: artifacts, Provenance: provenance,
		}).Marshal()
		if err != nil {
			return nil, fmt.Errorf("write the manifest of view %s: %w", view.ID, err)
		}

		files = append(files, viewFile{path: view.Output.Path, data: generated.DSL})

		for _, a := range artifacts {
			files = append(files, viewFile{path: a.Path, data: a.Data})
		}

		files = append(files, viewFile{path: view.ManifestPath(), data: manifest})
	}

	return files, nil
}
