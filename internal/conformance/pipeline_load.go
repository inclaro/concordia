package conformance

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

func (c *checker) architecturePath(ctx context.Context, res *remote.Resolver, wspace *workspace.Workspace) (string, []workspace.ResourceRef, error) {
	switch {
	case c.opts.Architecture != "":
		return c.resolveCwd(c.opts.Architecture), nil, nil
	case wspace.Architecture.Source != "":
		p, ref, err := remote.ResolvePath(ctx, res, wspace.Architecture.Source)
		if err != nil {
			return "", nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		if ref == nil {
			return wspace.ResolveInRoot(p), nil, nil
		}

		return p, []workspace.ResourceRef{*ref}, nil
	default:
		return "", nil, usage("no architecture: pass --architecture or set architecture.source")
	}
}

// resolveArchitectureFiles gives the absolute OS paths of workspace pattern or decorator
// entries. It appends the remote entries to refs.
func resolveArchitectureFiles(ctx context.Context, res *remote.Resolver, wspace *workspace.Workspace, values []string, refs *[]workspace.ResourceRef) ([]string, error) {
	out := make([]string, 0, len(values))

	for _, p := range values {
		local, ref, err := remote.ResolvePath(ctx, res, p)
		if err != nil {
			return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		if ref != nil {
			*refs = append(*refs, *ref)
			out = append(out, local)

			continue
		}

		out = append(out, wspace.ResolveInRoot(local))
	}

	return out, nil
}

// loadModel reads the architecture, its patterns and decorators, into inp. It gives the remote and url-mapped
// resources that the model came from, for the lock check.
func (c *checker) loadModel(ctx context.Context, wspace *workspace.Workspace, inp *inputs) ([]workspace.ResourceRef, error) {
	res := &remote.Resolver{Root: wspace.Root, LockPath: workspace.LockPathFor(wspace.Root), Client: c.opts.Deps.HTTPClient, Offline: c.opts.Env.Offline}

	archPath, refs, err := c.architecturePath(ctx, res, wspace)
	if err != nil {
		return nil, err
	}

	if inp.patternPaths, err = resolveArchitectureFiles(ctx, res, wspace, wspace.Architecture.Patterns, &refs); err != nil {
		return nil, err
	}

	if inp.decoratorPaths, err = resolveArchitectureFiles(ctx, res, wspace, wspace.Architecture.Decorators, &refs); err != nil {
		return nil, err
	}

	loaded, err := c.loadArchitectureModel(ctx, res, wspace, modelFiles{architecture: archPath, patterns: inp.patternPaths, decorators: inp.decoratorPaths})
	if err != nil {
		return nil, err
	}

	inp.model = loaded.Model
	inp.lockPath = workspace.LockPathFor(wspace.Root)

	return append(refs, loaded.Resources...), nil
}

// modelFiles are the architecture file, the patterns and the decorators that a run loads.
type modelFiles struct {
	architecture string
	patterns     []string
	decorators   []string
}

// loadArchitectureModel loads the model from files. It fails on error diagnostics, keeps the others, and counts
// the cache hits of res.
func (c *checker) loadArchitectureModel(ctx context.Context, res *remote.Resolver, wspace *workspace.Workspace, files modelFiles) (intent.LoadResult, error) {
	urlMapping, err := intent.LoadURLMapping(wspace)
	if err != nil {
		return intent.LoadResult{}, fmt.Errorf("load url mapping: %w", err)
	}

	archURL := ""
	if c.opts.Architecture == "" && strings.HasPrefix(wspace.Architecture.Source, "https://") {
		archURL = wspace.Architecture.Source
	}

	loaded := intent.LoadArchitecture(files.architecture, intent.LoadContext{
		Root: wspace.Root, URLMapping: urlMapping, Patterns: files.patterns,
		Decorators: files.decorators, ArchitectureURL: archURL, Trust: trust.FromWorkspace(wspace),
		Remote: func(u string) (string, string, string) { return res.Resolve(ctx, u) },
	})
	if err := policy.ThrowOnErrors(loaded.Diagnostics); err != nil {
		return intent.LoadResult{}, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	c.cacheHits += res.CacheHits

	c.diags = append(c.diags, loaded.Diagnostics...)

	if loaded.Model == nil {
		return intent.LoadResult{}, diag.Fail(diag.INT001, diag.Detail("architecture not loaded"))
	}

	return loaded, nil
}

func (c *checker) loadInputs(ctx context.Context, wspace *workspace.Workspace, state *frozen, caps *policy.ProviderCaps) (*inputs, error) {
	inp := &inputs{}

	resources, err := c.loadModel(ctx, wspace, inp)
	if err != nil {
		return nil, err
	}

	steps := []func() error{
		func() error { return c.loadBindings(wspace, inp) },
		// The assurance evidence comes before the policies: they may name its check ids. The scenario files come after,
		// because they may name the compiled rules (see assurance_load.go).
		func() error { return c.loadAssurance(ctx, wspace, state, inp) },
		func() error { return c.compilePolicies(wspace, inp, caps) },
		func() error { return c.loadScenarios(wspace, inp) },
		// The lock check needs the packs, so it follows the policy compile.
		func() error { return c.verifyLock(wspace, inp, state.conn, caps.Profile, resources) },
		// Baseline and waivers are loaded before any query, so an invalid file stops the run early.
		func() error { return c.loadExceptions(wspace, inp) },
		// A scoped waiver counts only when its approver is verified at the trust anchor.
		func() error { return c.verifyScopedWaivers(ctx, wspace, inp) },
	}

	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}

	return inp, nil
}

// verifyLock checks the lockfile against the toolchain, the url-mapped
// resources and the policy packs of this run. The packs of `policies.packs`
// are locked; with `--policy` there are none.
func (c *checker) verifyLock(wspace *workspace.Workspace, inp *inputs, conn *observation.Connection, profile *observation.ProfileRef, resources []workspace.ResourceRef) error {
	lock, _, err := workspace.ReadLock(inp.lockPath)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if lock != nil && lock.Resolved.CBM.Profile != nil {
		inp.lockProfile = &observation.ProfileRef{ID: lock.Resolved.CBM.Profile.ID, Digest: lock.Resolved.CBM.Profile.Digest}
	}

	_, warnings, err := workspace.VerifyLock(lock, &workspace.VerifyContext{
		Root: wspace.Root, LockPath: inp.lockPath, Offline: c.opts.Env.Offline, CBMProfile: lockProfileOf(profile), Resources: resources, Packs: lockPacksOf(inp.compiled.Packs),
		CBMVersion: conn.Version, CBMExecutable: conn.Executable, CBMSHA256: conn.SHA256,
		CalmSchemaVersion: inp.model.SchemaVersion,
	})
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	c.diags = append(c.diags, warnings...)

	return nil
}

// lockPacksOf gives the lock form of the compiled packs that come from `policies.packs`.
func lockPacksOf(packs []policy.PolicyPackRef) []workspace.PackRef {
	var out []workspace.PackRef

	for _, p := range packs {
		if p.Use != "" {
			out = append(out, workspace.PackRef{ID: p.ID, Version: p.Version, Source: p.Use, SHA256: p.Digest})
		}
	}

	return out
}

// lockProfileOf gives the lock form of a profile reference (nil: no profile applies).
func lockProfileOf(ref *observation.ProfileRef) *workspace.LockProfile {
	if ref == nil {
		return nil
	}

	return &workspace.LockProfile{ID: ref.ID, Digest: ref.Digest}
}

func (c *checker) loadBindings(wspace *workspace.Workspace, inp *inputs) error {
	loaded, err := binding.Load(wspace, inp.model)
	if err != nil {
		return fmt.Errorf("load bindings: %w", err)
	}

	if err := policy.ThrowOnErrors(loaded.Diagnostics); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	c.diags = append(c.diags, loaded.Diagnostics...)
	inp.bindings = loaded
	inp.rootsByNode = map[string][]string{}

	for i := range loaded.Bindings {
		addSourceRoots(inp.rootsByNode, &loaded.Bindings[i])
	}

	return nil
}

// addSourceRoots adds the source-root matches of bnd to the sorted, duplicate-free roots of its architecture node.
func addSourceRoots(rootsByNode map[string][]string, bnd *binding.Binding) {
	for j := range bnd.Match {
		m := &bnd.Match[j]
		if m.Kind != binding.MatchSourceRoot {
			continue
		}

		list := rootsByNode[bnd.ArchitectureRef]
		if !slices.Contains(list, m.Value) {
			list = append(list, m.Value)
		}

		slices.SortFunc(list, compareStrings)
		rootsByNode[bnd.ArchitectureRef] = list
	}
}

// loadPolicyFiles reads the policy files and resolves the packs. `--policy`
// replaces both `policies.files` and `policies.packs`. This never uses the network.
func (c *checker) loadPolicyFiles(wspace *workspace.Workspace) ([]policy.PolicyFile, error) {
	if len(c.opts.Policies) > 0 {
		paths := make([]string, 0, len(c.opts.Policies))
		for _, p := range c.opts.Policies {
			paths = append(paths, c.resolveCwd(p))
		}

		return policy.ReadPolicyFiles(wspace.Root, paths) //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	files, problems, err := policy.LoadWorkspaceFiles(wspace)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := policy.ThrowOnErrors(problems); err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return files, nil
}

func (c *checker) compilePolicies(wspace *workspace.Workspace, inp *inputs, caps *policy.ProviderCaps) error {
	opts := c.opts

	files, err := c.loadPolicyFiles(wspace)
	if err != nil {
		return err
	}

	overrides := policy.CompileOverrides{
		CLI:           policy.PartialEvidence{MinimumCoverage: opts.MinimumCoverage, OnUnknown: opts.Unknown},
		Workspace:     policy.PartialEvidence{MinimumCoverage: &wspace.Check.MinimumCoverage, OnUnknown: policy.UnknownDisposition(wspace.Check.Unknown)},
		RuleIDs:       opts.Rules,
		KnownCheckIDs: c.knownCheckIDs,
	}

	compiled, err := policy.CompilePolicies(files, inp.model, *caps, &overrides)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := policy.ThrowOnErrors(compiled.Diagnostics); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	c.diags = append(c.diags, compiled.Diagnostics...)
	inp.compiled = compiled
	inp.policyFiles = files

	selected, err := selectControls(inp.model, compiled.Plans, opts.Controls, compiled.RuleIDs)
	if err != nil {
		return err
	}

	if err := policy.ThrowOnErrors(selected.Diagnostics); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	c.diags = append(c.diags, selected.Diagnostics...)
	inp.selected = selected

	return nil
}

// loadExceptions reads the baseline and the waiver file, each only when one applies.
func (c *checker) loadExceptions(wspace *workspace.Workspace, inp *inputs) error {
	inp.baselinePath = c.pickExceptionPath(wspace, c.opts.UseBaseline, wspace.Exceptions.Baseline)
	inp.waiversPath = c.pickExceptionPath(wspace, c.opts.Waivers, wspace.Exceptions.Waivers)

	var err error
	if inp.baseline, err = readBaselineAt(inp.baselinePath); err != nil {
		return err
	}

	if inp.waivers, err = readWaiversAt(inp.waiversPath, wspace.Exceptions.MaxExpiryDays, c.now()); err != nil {
		return err
	}

	return nil
}

// readBaselineAt reads the baseline file at path (nil for "": none applies).
func readBaselineAt(path string) (*exceptions.Baseline, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // no baseline applies: nothing is read.
	}

	b, err := exceptions.ReadBaseline(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return &b, nil
}

// readWaiversAt reads the waiver file at path (nil for "": none applies).
func readWaiversAt(path string, maxExpiryDays int, now time.Time) ([]exceptions.Waiver, error) {
	if path == "" {
		return nil, nil
	}

	w, err := exceptions.ReadWaivers(path, exceptions.WithMaxExpiry(maxExpiryDays, now))
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return w, nil
}

// pickExceptionPath gives the file of the flag (resolved against the working directory), else the configured
// file when it exists in the workspace root, else "".
func (c *checker) pickExceptionPath(wspace *workspace.Workspace, flag, configured string) string {
	if flag != "" {
		return c.resolveCwd(flag)
	}

	if p := wspace.ResolveInRoot(configured); fileExists(p) {
		return p
	}

	return ""
}
