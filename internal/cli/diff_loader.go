package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

type diffLoader struct {
	app  *Context
	wsp  *workspace.Workspace
	tmps []string
}

func (l *diffLoader) workspace() (*workspace.Workspace, error) {
	if l.wsp != nil {
		return l.wsp, nil
	}

	wsp, err := l.app.LoadWorkspace()
	if err != nil {
		return nil, err
	}

	l.wsp = wsp

	return wsp, nil
}

func (l *diffLoader) tempDir() (string, error) {
	dir, err := os.MkdirTemp("", "concordia-diff-")
	if err != nil {
		return "", fmt.Errorf("make temporary directory: %w", err)
	}

	l.tmps = append(l.tmps, dir)

	return dir, nil
}

func (l *diffLoader) cleanup() {
	for _, d := range l.tmps {
		_ = os.RemoveAll(d)
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// resolve finds the kind of a side argument and loads what the side gives.
func (l *diffLoader) resolve(ctx context.Context, arg string) (*diffSide, error) {
	abs := l.app.Abs(arg)

	switch {
	case arg == sideWorktree:
		return l.worktree(ctx)
	case isFile(abs) && strings.HasSuffix(arg, ".tar.gz"):
		return l.bundle(ctx, arg, abs)
	case isFile(abs):
		return l.resultFile(arg, abs)
	default:
		return l.gitRef(ctx, arg)
	}
}

// resultFile loads the side that a results file gives. The run has to be complete when the workspace is known.
func (l *diffLoader) resultFile(arg, abs string) (*diffSide, error) {
	run, err := readRunFile(abs)
	if err != nil {
		return nil, diag.Fail(diag.WRK010, diag.Detail(arg+" is not a results file or a bundle"))
	}

	if wsp, wspErr := l.workspace(); wspErr == nil {
		if completeErr := conformance.RequireCompleteRun(wsp.Root, run.Run.ID); completeErr != nil {
			return nil, completeErr
		}
	}

	return &diffSide{kind: sideResult, value: arg, run: run}, nil
}

// readRunFile reads and decodes the results file at an absolute path.
func readRunFile(abs string) (*conformance.Run, error) {
	text, err := os.ReadFile(abs) //nolint:gosec // the user names the results file.
	if err != nil {
		return nil, fmt.Errorf("read results: %w", err)
	}

	return decodeRun(text)
}

// modelLoadFailure gives the code and detail of the error for a CALM document that did not load: CO-INT-003 when a
// diagnostic has it, else CO-INT-001, and the message of the first diagnostic as the detail.
func modelLoadFailure(diagnostics []diag.Diagnostic) (code, detail string) {
	code, detail = diag.INT001, "architecture not loaded"

	for i := range diagnostics {
		if diagnostics[i].Code == diag.INT003 {
			code = diag.INT003
		}
	}

	if len(diagnostics) > 0 {
		detail = diagnostics[0].Message
	}

	return code, detail
}

// modelLoadContext is the context that loads a CALM document of a workspace: its root, the URL mapping, and its trust
// vocabulary.
func modelLoadContext(wsp *workspace.Workspace, urlMapping map[string]string) *intent.LoadContext {
	return &intent.LoadContext{Root: wsp.Root, URLMapping: urlMapping, Trust: trust.FromWorkspace(wsp)}
}

// loadModelFile loads a CALM document at an absolute path. Its diagnostics are ignored.
func loadModelFile(ctx context.Context, abs string, lc *intent.LoadContext, res *remote.Resolver) (*intent.ArchitectureModel, error) {
	if res != nil {
		lc.Remote = func(u string) (string, string, string) { return res.Resolve(ctx, u) }
	}

	result := intent.LoadArchitecture(abs, *lc)
	if result.Model == nil {
		code, detail := modelLoadFailure(result.Diagnostics)

		return nil, diag.Fail(code, diag.Detail(detail))
	}

	return result.Model, nil
}

func (l *diffLoader) worktreeModel(ctx context.Context, wsp *workspace.Workspace) (*intent.ArchitectureModel, error) {
	source := wsp.Architecture.Source
	if source == "" {
		return nil, diag.Fail(diag.WRK010, diag.Detail("no architecture: set architecture.source"))
	}

	res := l.app.remoteResolver(wsp)

	local, _, err := remote.ResolvePath(ctx, res, source)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if !strings.HasPrefix(source, "https://") {
		local = wsp.ResolveInRoot(local)
	}

	urlMapping, err := intent.LoadURLMapping(wsp)
	if err != nil {
		return nil, fmt.Errorf("load url mapping: %w", err)
	}

	return loadModelFile(ctx, local, modelLoadContext(wsp, urlMapping), res)
}

func bindingsOf(wsp *workspace.Workspace, model *intent.ArchitectureModel) (*[]binding.Binding, error) {
	if model == nil {
		model = &intent.ArchitectureModel{}
	}

	loaded, err := binding.Load(wsp, model)
	if err != nil {
		return nil, fmt.Errorf("load bindings: %w", err)
	}

	return &loaded.Bindings, nil
}

func (l *diffLoader) worktree(ctx context.Context) (*diffSide, error) {
	wsp, err := l.workspace()
	if err != nil {
		return nil, err
	}

	side := &diffSide{kind: sideWorktree, value: sideWorktree}

	if side.model, err = l.worktreeModel(ctx, wsp); err != nil {
		return nil, err
	}

	if side.bindings, err = bindingsOf(wsp, side.model); err != nil {
		return nil, err
	}

	if text, ok := worktreeReport(wsp); ok {
		if side.run, err = completeRunOf(wsp, text); err != nil {
			return nil, err
		}
	}

	return side, nil
}

// worktreeReport is the content of the JSON report of the workspace, and whether it could be read.
func worktreeReport(wsp *workspace.Workspace) ([]byte, bool) {
	text, err := os.ReadFile(wsp.ResolveInRoot(jsonReportPath(wsp)))

	return text, err == nil
}

// completeRunOf decodes a report and requires that its run is complete.
func completeRunOf(wsp *workspace.Workspace, text []byte) (*conformance.Run, error) {
	run, err := decodeRun(text)
	if err != nil {
		return nil, err
	}

	if completeErr := conformance.RequireCompleteRun(wsp.Root, run.Run.ID); completeErr != nil {
		return nil, completeErr
	}

	return run, nil
}

// gitSnapshot is a git revision that is read into the root of wsp: a copy of the current workspace disk, so that the
// architecture of the revision is validated against the trust vocabulary that the worktree side also uses.
type gitSnapshot struct {
	git  workspace.GitPort
	wsp  *workspace.Workspace
	disk *workspace.Workspace
	rev  string
}

func (l *diffLoader) gitRef(ctx context.Context, ref string) (*diffSide, error) {
	wsp, err := l.workspace()
	if err != nil {
		return nil, err
	}

	git := l.app.Git(wsp.Root)

	rev, err := git.ResolveRef(ctx, ref)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	dir, err := l.tempDir()
	if err != nil {
		return nil, err
	}

	side := &diffSide{kind: sideGitRef, value: ref}

	source, err := gitRefSource(wsp)
	if err != nil {
		return nil, err
	}

	clone := *wsp
	clone.Root = dir
	snapshot := gitSnapshot{git: git, rev: rev, wsp: &clone, disk: wsp}

	if side.model, err = snapshot.model(ctx, source); err != nil {
		return nil, err
	}

	if err := snapshot.writeBindingFiles(ctx, wsp.Bindings.Files); err != nil {
		return nil, err
	}

	if side.bindings, err = bindingsOf(&clone, side.model); err != nil {
		return nil, err
	}

	return side, nil
}

// gitRefSource gives the workspace-relative architecture source that a git ref can read.
func gitRefSource(wsp *workspace.Workspace) (string, error) {
	source := wsp.Architecture.Source
	if source == "" || strings.HasPrefix(source, "https://") {
		return "", diag.Fail(diag.WRK010, diag.Detail("a git ref needs a workspace-relative architecture.source"))
	}

	source = filepath.ToSlash(filepath.Clean(source))
	if strings.HasPrefix(source, "/") || !filepath.IsLocal(filepath.FromSlash(source)) {
		return "", diag.Fail(diag.WRK010, diag.Detail("architecture.source '"+wsp.Architecture.Source+"' is outside the workspace; a git ref needs a workspace-relative source"))
	}

	return source, nil
}

// model loads the architecture at source as of the revision. A source that the revision does not have gives an empty
// model.
func (s gitSnapshot) model(ctx context.Context, source string) (*intent.ArchitectureModel, error) {
	data, found, err := s.git.ShowFile(ctx, s.rev, source)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if !found {
		return &intent.ArchitectureModel{}, nil
	}

	target := filepath.Join(s.wsp.Root, filepath.FromSlash(source))
	if err = fsutil.WriteFileAtomic(target, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", target, err)
	}

	urlMapping, err := adr.URLMappingAt(ctx, adr.Revision{Git: s.git, Rev: s.rev}, s.disk, s.wsp)
	if err != nil {
		return nil, fmt.Errorf("url mapping at %s: %w", s.rev, err)
	}

	return loadModelFile(ctx, target, modelLoadContext(s.wsp, urlMapping), nil)
}

// writeBindingFiles copies the binding files that patterns match from the revision into the directory.
func (s gitSnapshot) writeBindingFiles(ctx context.Context, patterns []string) error {
	all, err := s.git.ListFilesAt(ctx, s.rev)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	match := fsutil.GlobMatcher(patterns)

	for _, file := range all {
		if !match(file) {
			continue
		}

		content, ok, showErr := s.git.ShowFile(ctx, s.rev, file)
		if showErr != nil {
			return showErr //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		if !ok {
			continue
		}

		target := filepath.Join(s.wsp.Root, filepath.FromSlash(file))
		if err = fsutil.WriteFileAtomic(target, content); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
	}

	return nil
}

func (l *diffLoader) bundle(ctx context.Context, arg, abs string) (*diffSide, error) {
	dir, err := l.tempDir()
	if err != nil {
		return nil, err
	}

	if _, err := bundle.Open(abs, dir); err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	text, err := os.ReadFile(filepath.Join(dir, "results", "results.json")) //nolint:gosec // the bundle was extracted into our temporary directory.
	if err != nil {
		return nil, diag.Fail(diag.REP004, diag.Detail("the bundle has no results/results.json"))
	}

	run, err := decodeRun(text)
	if err != nil {
		return nil, err
	}

	wsp, err := l.bundleWorkspace(run, filepath.Join(dir, "inputs"))
	if err != nil {
		return nil, err
	}

	side := &diffSide{kind: sideBundle, value: arg, run: run}

	if side.model, err = bundleModel(ctx, wsp); err != nil {
		return nil, err
	}

	side.bindings, err = bindingsOf(wsp, side.model)

	return side, err
}

// bundleModel loads the local architecture of the workspace that a bundle holds, with the URL mapping file and the
// mapped files that the bundle holds. A bundle without the mapping file (one written before bundles held it) loads
// with an empty mapping.
func bundleModel(ctx context.Context, wsp *workspace.Workspace) (*intent.ArchitectureModel, error) {
	source := wsp.Architecture.Source
	if source == "" || strings.HasPrefix(source, "https://") {
		return nil, diag.Fail(diag.WRK010, diag.Detail("the bundle has no local architecture.source"))
	}

	urlMapping := map[string]string{}

	if bundleHasFile(wsp, wsp.Architecture.URLMapping) {
		mapping, err := intent.LoadURLMapping(wsp)
		if err != nil {
			return nil, fmt.Errorf("load the url mapping of the bundle: %w", err)
		}

		urlMapping = confinedMapping(wsp.Root, mapping)
	}

	return loadModelFile(ctx, wsp.ResolveInRoot(source), modelLoadContext(wsp, urlMapping), nil)
}

// bundleHasFile says whether the workspace-relative path is a file that the bundle holds below the root of its
// workspace, also after symbolic links.
func bundleHasFile(wsp *workspace.Workspace, rel string) bool {
	if rel == "" {
		return false
	}

	abs, err := fsutil.Confine(wsp.Root, filepath.ToSlash(rel))

	return err == nil && isFile(abs)
}

// confinedMapping keeps the entries of a bundle's URL mapping whose file stays below the root of the bundle. A bundle
// is untrusted input: an absolute entry, a `..` entry, or a symbolic link that leaves the root is dropped, so its URL
// stays unmapped and nothing outside the bundle is read.
func confinedMapping(root string, mapping map[string]string) map[string]string {
	out := make(map[string]string, len(mapping))

	for url, abs := range mapping {
		rel, err := filepath.Rel(root, abs)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}

		if _, err := fsutil.Confine(root, filepath.ToSlash(rel)); errors.Is(err, fsutil.ErrOutsideRoot) {
			continue
		}

		out[url] = abs
	}

	return out
}

// bundleWorkspace loads the workspace that a bundle holds among its inputs.
func (l *diffLoader) bundleWorkspace(run *conformance.Run, inputs string) (*workspace.Workspace, error) {
	wsPath := run.Inputs.Workspace.Path
	if wsPath == "" || strings.HasPrefix(wsPath, "builtin:") {
		wsPath = "concordia.yaml"
	}

	wsp, err := workspace.Load(filepath.Join(inputs, filepath.FromSlash(wsPath)), l.app.Opts.Profile)
	if err != nil {
		return nil, fmt.Errorf("load the workspace of the bundle: %w", err)
	}

	return wsp, nil
}
