package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// modelValidateRun is what one run of `model validate` validates with.
type modelValidateRun struct {
	app     *Context
	wsp     *workspace.Workspace
	res     *remote.Resolver
	opts    *modelValidateOptions
	loadCtx *intent.LoadContext
}

// newModelValidateRun makes the context that documents are loaded with.
func newModelValidateRun(ctx context.Context, app *Context, wsp *workspace.Workspace, opts *modelValidateOptions) (*modelValidateRun, error) {
	res := app.remoteResolver(wsp)

	urlMapping, err := intent.LoadURLMapping(wsp)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller reports the error as it is.
	}

	return &modelValidateRun{
		app: app, wsp: wsp, res: res, opts: opts,
		loadCtx: &intent.LoadContext{
			Root: wsp.Root, URLMapping: urlMapping, Trust: trust.FromWorkspace(wsp),
			Remote: func(u string) (string, string, string) { return res.Resolve(ctx, u) },
		},
	}, nil
}

// outcome validates what the options select: the timeline, the patterns alone, or the architecture.
func (r *modelValidateRun) outcome(ctx context.Context) (modelValidateOutcome, error) {
	switch {
	case r.opts.timeline != "":
		return documentOutcome(modeTimeline, []intent.DocumentResult{intent.ValidateTimeline(r.app.Abs(r.opts.timeline), r.loadCtx)}), nil
	case r.opts.patternOnly:
		return r.patternOnlyOutcome(ctx)
	default:
		return r.architectureOutcome(ctx)
	}
}

// patternOnlyOutcome validates the patterns alone.
func (r *modelValidateRun) patternOnlyOutcome(ctx context.Context) (modelValidateOutcome, error) {
	patterns, err := r.patterns(ctx)
	if err != nil {
		return modelValidateOutcome{}, err
	}

	if len(patterns) == 0 {
		return modelValidateOutcome{}, diag.Fail(diag.WRK010, diag.Detail("--pattern-only needs at least one pattern"))
	}

	results := make([]intent.DocumentResult, len(patterns))
	for i, p := range patterns {
		results[i] = intent.ValidatePatternDocument(p, r.loadCtx)
	}

	return documentOutcome(modePattern, results), nil
}

// patterns gives the absolute paths of the workspace patterns and the --pattern files.
func (r *modelValidateRun) patterns(ctx context.Context) ([]string, error) {
	return r.configuredAndFlagged(ctx, r.wsp.Architecture.Patterns, r.opts.patterns)
}

// decorators gives the absolute paths of the workspace decorators and the --decorator files.
func (r *modelValidateRun) decorators(ctx context.Context) ([]string, error) {
	return r.configuredAndFlagged(ctx, r.wsp.Architecture.Decorators, r.opts.decorators)
}

// configuredAndFlagged gives the absolute paths of the files that the workspace configures, then of those that
// a flag names.
func (r *modelValidateRun) configuredAndFlagged(ctx context.Context, configured, flagged []string) ([]string, error) {
	files, err := resolveArchitectureFiles(ctx, r.res, r.wsp, configured)
	if err != nil {
		return nil, err
	}

	for _, f := range flagged {
		files = append(files, r.app.Abs(f))
	}

	return files, nil
}

// architecturePath gives the architecture: --architecture, else architecture.source.
func (r *modelValidateRun) architecturePath(ctx context.Context) (string, error) {
	archPath := r.opts.architecture

	if archPath == "" {
		var err error

		archPath, _, err = remote.ResolvePath(ctx, r.res, r.wsp.Architecture.Source)
		if err != nil {
			return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}
	}

	if archPath == "" {
		return "", diag.Fail(diag.WRK010, diag.Detail("no architecture: pass --architecture or set architecture.source"))
	}

	return archPath, nil
}

// architectureFile gives the file to load for the architecture path.
func (r *modelValidateRun) architectureFile(archPath string) string {
	if r.opts.architecture == "" && !strings.HasPrefix(r.wsp.Architecture.Source, "https://") {
		return r.wsp.ResolveInRoot(archPath)
	}

	return archPath
}

// architectureOutcome validates the architecture, with its patterns and decorators, and the views.
func (r *modelValidateRun) architectureOutcome(ctx context.Context) (modelValidateOutcome, error) {
	archPath, err := r.architecturePath(ctx)
	if err != nil {
		return modelValidateOutcome{}, err
	}

	patterns, err := r.patterns(ctx)
	if err != nil {
		return modelValidateOutcome{}, err
	}

	decorators, err := r.decorators(ctx)
	if err != nil {
		return modelValidateOutcome{}, err
	}

	r.loadCtx.Patterns = patterns
	r.loadCtx.Decorators = decorators
	r.loadCtx.ArchitectureURL = remoteArchitectureURL(r.wsp, r.opts.architecture)

	result := intent.LoadArchitecture(r.app.Abs(r.architectureFile(archPath)), *r.loadCtx)

	out := architectureSubjectOutcome(archPath, len(patterns) > 0, result.Model)

	viewDiagnostics, err := views.Diagnose(ctx, r.wsp, result.Model, r.app.Git(r.wsp.Root))
	if err != nil {
		return modelValidateOutcome{}, fmt.Errorf("view diagnostics: %w", err)
	}

	out.diagnostics = diag.Sort(append(append([]diag.Diagnostic{}, result.Diagnostics...), viewDiagnostics...))

	return out, nil
}

// architectureSubjectOutcome names the mode and the subject of an architecture validation. The subject is the
// model when the document loaded, else the path.
func architectureSubjectOutcome(archPath string, withPatterns bool, model *intent.ArchitectureModel) modelValidateOutcome {
	out := modelValidateOutcome{mode: modeArchitecture, subject: modelValidateArch{Path: archPath}}
	if withPatterns {
		out.mode = modeArchitectureWithPattern
	}

	if model != nil {
		out.subject = modelValidateArch{Path: model.SourceURI, Digest: &model.Digest}
	}

	return out
}

// documentOutcome reports a pattern or timeline validation. The subject is the first document.
func documentOutcome(mode string, results []intent.DocumentResult) modelValidateOutcome {
	out := modelValidateOutcome{mode: mode, subject: modelValidateArch{Path: results[0].SourceURI}}
	if results[0].Digest != "" {
		out.subject.Digest = &results[0].Digest
	}

	var all []diag.Diagnostic
	for i := range results {
		all = append(all, results[i].Diagnostics...)
	}

	out.diagnostics = diag.Sort(all)

	return out
}
