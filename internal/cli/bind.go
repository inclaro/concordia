package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	keyPath        = "path"
	keyDetail      = "detail"
	calmNodePrefix = "calm:node:"

	// edgeKindImports is the native CBM kind of an import edge.
	edgeKindImports = "IMPORTS"

	// nodeLabelRoute is the label of a route node.
	nodeLabelRoute = "Route"

	// candidatesFile is the default file of `bind discover` (relative to the workspace root).
	candidatesFile = "bindings/concordia.bindings.candidates.yaml"

	strategyPath    = "path"
	strategyPackage = "package"
	strategyBuild   = "build"
	strategyRoute   = "route"
	strategyCluster = "cluster"
	// strategyDeployment proposes deployed-resource bindings with status proposed.
	strategyDeployment = "deployment"
)

type bindOptions struct {
	architecture string
	strategy     string
	projects     []string
	strict       bool
	showUnbound  bool
}

func newBindCmd(host *Host) *cobra.Command {
	bind := &cobra.Command{Use: "bind", Short: "Identity binding commands"}

	var validateOpts, discoverOpts bindOptions

	validate := &cobra.Command{
		Use:   cmdValidate,
		Short: "Resolve bindings against the observed graph; fail on conflicts and unmatched required bindings",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return bindValidate(ctx, app, &validateOpts)
		}),
	}
	validate.Flags().StringVar(&validateOpts.architecture, "architecture", "", "CALM architecture file")
	validate.Flags().StringArrayVar(&validateOpts.projects, "project", nil, "workspace project name; repeatable (default: all projects)")
	validate.Flags().BoolVar(&validateOpts.strict, "strict", false, "unbound files also give exit code 2")
	validate.Flags().BoolVar(&validateOpts.showUnbound, "show-unbound", false, "list indexed files that have no binding")

	discover := &cobra.Command{
		Use:   "discover",
		Short: "Suggest binding candidates (written to --output, default " + candidatesFile + ")",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return bindDiscover(ctx, app, &discoverOpts)
		}),
	}
	discover.Flags().StringVar(&discoverOpts.architecture, "architecture", "", "CALM architecture file")
	discover.Flags().StringArrayVar(&discoverOpts.projects, "project", nil, "workspace project name; repeatable (default: all projects)")
	discover.Flags().StringVar(&discoverOpts.strategy, "strategy", strategyPath+","+strategyPackage, "comma-separated: path,package,build,route,cluster,deployment")

	bind.AddCommand(validate, discover)

	return bind
}

// bindContext is the loaded input of both bind commands.
type bindContext struct {
	ws       *workspace.Workspace
	model    *intent.ArchitectureModel
	provider *observation.CbmProvider
	// project is the name of the first selected project.
	project string
	// generation is the graph generation of the selected projects.
	generation string
	// nodes are the observed nodes of the selected projects; paths of other projects have the prefix @<name>/.
	nodes []observation.ObservedNode
	// files are the indexed files of the selected projects, with the same prefixes.
	files []string
	// links are the file-to-file IMPORTS edges, for the cluster strategy.
	links []fileLink
	// exclude and scope are the unioned coverage globs of the selected projects.
	scope bindScope
}

// bindScope is the coverage scope of the selected projects.
type bindScope struct {
	include []string
	exclude []string
}

// covers gives the matcher of the files in scope: included and not excluded.
// Without include globs, all files are included.
func (s bindScope) covers() func(file string) bool {
	include := s.include
	if len(include) == 0 {
		include = []string{"**"}
	}

	inScope, excluded := fsutil.GlobMatcher(include), fsutil.GlobMatcher(s.exclude)

	return func(file string) bool { return inScope(file) && !excluded(file) }
}

// bindLoader loads the architecture model of the bind commands.
type bindLoader struct {
	app *Context
	wsp *workspace.Workspace
	res *remote.Resolver
}

// architecturePath gives the local path of the architecture file: the flag, else architecture.source.
func (l *bindLoader) architecturePath(ctx context.Context, flag string) (string, error) {
	switch {
	case flag != "":
		return l.app.Abs(flag), nil
	case l.wsp.Architecture.Source != "":
		return l.sourcePath(ctx)
	}

	return "", diag.Fail(diag.WRK010, diag.Detail("no architecture: pass --architecture or set architecture.source"))
}

// sourcePath resolves architecture.source: a remote file stays where it was fetched, a local one is rooted in the workspace.
func (l *bindLoader) sourcePath(ctx context.Context) (string, error) {
	source := l.wsp.Architecture.Source

	local, _, err := remote.ResolvePath(ctx, l.res, source)
	if err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if strings.HasPrefix(source, "https://") {
		return local, nil
	}

	return l.wsp.ResolveInRoot(local), nil
}

// loadModel loads the architecture model, with its patterns and decorators.
func (l *bindLoader) loadModel(ctx context.Context, flag string) (*intent.ArchitectureModel, error) {
	archPath, err := l.architecturePath(ctx, flag)
	if err != nil {
		return nil, err
	}

	urlMapping, err := intent.LoadURLMapping(l.wsp)
	if err != nil {
		return nil, err
	}

	patterns, err := resolveArchitectureFiles(ctx, l.res, l.wsp, l.wsp.Architecture.Patterns)
	if err != nil {
		return nil, err
	}

	decorators, err := resolveArchitectureFiles(ctx, l.res, l.wsp, l.wsp.Architecture.Decorators)
	if err != nil {
		return nil, err
	}

	loaded := intent.LoadArchitecture(archPath, intent.LoadContext{
		Root: l.wsp.Root, URLMapping: urlMapping, Patterns: patterns,
		Decorators: decorators, ArchitectureURL: remoteArchitectureURL(l.wsp, flag), Trust: trust.FromWorkspace(l.wsp),
		Remote: func(u string) (string, string, string) { return l.res.Resolve(ctx, u) },
	})
	if err = policy.ThrowOnErrors(loaded.Diagnostics); err != nil {
		return nil, fmt.Errorf("load architecture: %w", err)
	}

	if loaded.Model == nil {
		return nil, diag.Fail(diag.INT001, diag.Detail("architecture not loaded"))
	}

	return loaded.Model, nil
}

func prepareBind(ctx context.Context, app *Context, opts *bindOptions, strategies map[string]bool) (*bindContext, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return nil, err
	}

	selected, err := conformance.SelectProjects(wsp, opts.projects)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	loader := &bindLoader{app: app, wsp: wsp, res: app.remoteResolver(wsp)}

	model, err := loader.loadModel(ctx, opts.architecture)
	if err != nil {
		return nil, err
	}

	conn, err := app.Connect(ctx, wsp)
	if err != nil {
		return nil, err
	}

	collector := &projectCollector{app: app, wsp: wsp, provider: conn.Provider, strategies: strategies}

	graph, err := collector.collectAll(ctx, selected)
	if err != nil {
		return nil, err
	}

	return &bindContext{
		ws: wsp, model: model, provider: conn.Provider, project: selected[0].Name, generation: graph.generation,
		nodes: graph.nodes, files: graph.files, links: graph.links, scope: coverageScopeOf(wsp, selected),
	}, nil
}

// coverageScopeOf gives the union of the include and exclude globs of the projects, each with its path prefix.
// A project without include globs covers all its files when another project has include globs.
func coverageScopeOf(wsp *workspace.Workspace, selected []workspace.Project) bindScope {
	anyInclude := slices.ContainsFunc(selected, func(p workspace.Project) bool { return len(p.Include) > 0 })

	var scope bindScope

	for _, project := range selected {
		prefix := wsp.PathPrefix(&project)

		switch {
		case len(project.Include) > 0:
			for _, g := range project.Include {
				scope.include = append(scope.include, prefix+g)
			}
		case anyInclude:
			scope.include = append(scope.include, prefix+"**")
		}

		for _, g := range project.Exclude {
			scope.exclude = append(scope.exclude, prefix+g)
		}
	}

	return scope
}

type bindValidateSummary struct {
	Project         string            `json:"project"`
	GraphGeneration string            `json:"graphGeneration"`
	Warnings        []diag.Diagnostic `json:"warnings"`
	Bindings        int               `json:"bindings"`
	ObservedNodes   int               `json:"observedNodes"`
	BoundNodes      int               `json:"boundNodes"`
	UnboundFiles    int               `json:"unboundFiles"`
}

// boundFiles gives the files of the observed nodes that have a binding.
func (b *bindContext) boundFiles(set *binding.Set) map[string]struct{} {
	bound := map[string]struct{}{}

	for i := range b.nodes {
		if _, ok := set.ByObserved[b.nodes[i].ObservedID]; ok && b.nodes[i].FilePath != "" {
			bound[b.nodes[i].FilePath] = struct{}{}
		}
	}

	return bound
}

// unboundWarnings gives CO-BND-005 for each indexed file in scope that has no bound node.
func unboundWarnings(bind *bindContext, set *binding.Set, files []string) []diag.Diagnostic {
	covers := bind.scope.covers()
	bound := bind.boundFiles(set)
	warnings := []diag.Diagnostic{}

	for _, f := range files {
		if _, isBound := bound[f]; !isBound && covers(f) {
			warnings = append(warnings, diag.New(diag.BND005, diag.Params{keyPath: f}, diag.WithSource(diag.Source{URI: f})))
		}
	}

	return diag.Sort(warnings)
}

func writeBindSummary(app *Context, summary *bindValidateSummary) error {
	if app.Opts.Format != modelFormatJSON {
		return app.WriteOutput(fmt.Sprintf("bindings valid: %d binding(s), %d/%d observed node(s) bound, %d unbound file(s)\n",
			summary.Bindings, summary.BoundNodes, summary.ObservedNodes, summary.UnboundFiles), "")
	}

	data, err := canonjson.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}

	return app.WriteOutput(string(data), "")
}

// resolveBindings loads the bindings and resolves them against the observed nodes.
func resolveBindings(bind *bindContext) (loaded binding.Loaded, set binding.Set, err error) {
	loaded, err = binding.Load(bind.ws, bind.model)
	if err != nil {
		return loaded, set, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err = policy.ThrowOnErrors(loaded.Diagnostics); err != nil {
		return loaded, set, fmt.Errorf("load bindings: %w", err)
	}

	set = binding.Resolve(loaded.Bindings, bind.nodes, binding.BuildFiles(bind.files))

	if err = policy.ThrowOnErrors(binding.Problems(&loaded, &set)); err != nil {
		return loaded, set, fmt.Errorf("resolve bindings: %w", err)
	}

	return loaded, set, nil
}

func bindValidate(ctx context.Context, app *Context, opts *bindOptions) (int, error) {
	bind, err := prepareBind(ctx, app, opts, nil)
	if err != nil {
		return diag.Internal, err
	}

	loaded, set, err := resolveBindings(bind)
	if err != nil {
		return diag.Internal, err
	}

	warnings := unboundWarnings(bind, &set, bind.files)
	summary := bindValidateSummary{
		Project: bind.project, GraphGeneration: bind.generation, Bindings: len(loaded.Bindings),
		ObservedNodes: len(bind.nodes), BoundNodes: len(set.ByObserved), UnboundFiles: len(warnings), Warnings: []diag.Diagnostic{},
	}

	if opts.showUnbound {
		summary.Warnings = warnings

		for _, d := range warnings {
			app.Err(diag.FormatDiagnostic(d) + "\n")
		}
	}

	if writeErr := writeBindSummary(app, &summary); writeErr != nil {
		return diag.Internal, writeErr
	}

	if opts.strict && len(warnings) > 0 {
		return diag.InvalidInput, nil
	}

	return diag.OK, nil
}

// discoverCandidates proposes bindings from the strategies. links are the file-to-file IMPORTS
// edges for the cluster strategy.
func discoverCandidates(model *intent.ArchitectureModel, files []string, nodes []observation.ObservedNode, links []fileLink, include []string, strategies map[string]bool) []candidate {
	var sources candidateSources
	if strategies[strategyPath] {
		sources.dirs = candidateDirs(files, include)
	}

	if strategies[strategyBuild] {
		sources.buildDirs = buildPackageDirs(files)
	}

	if strategies[strategyCluster] {
		sources.clusters = clusterPrefixes(links)
	}

	raw := make([]candidate, 0, len(model.Nodes))

	for i := range model.Nodes {
		raw = append(raw, nodeCandidates(&model.Nodes[i], nodes, &sources, strategies)...)
	}

	return finishCandidates(raw, nodes, files)
}

type candidateDoc struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Candidates []candidate `yaml:"candidates"`
}

// parseStrategies reads the comma-separated --strategy value.
func parseStrategies(text string) (map[string]bool, error) {
	strategies := map[string]bool{}

	for _, s := range strings.Split(text, ",") {
		if s = strings.TrimSpace(s); s != "" {
			strategies[s] = true
		}
	}

	if len(strategies) == 0 || hasUnknownStrategy(strategies) {
		return nil, diag.Fail(diag.WRK010, diag.Detail(
			fmt.Sprintf("--strategy accepts only 'path', 'package', 'build', 'route', 'cluster' and 'deployment', got '%s'", text)))
	}

	return strategies, nil
}

// hasUnknownStrategy says whether a strategy is none of the known ones.
func hasUnknownStrategy(strategies map[string]bool) bool {
	known := []string{strategyPath, strategyPackage, strategyBuild, strategyRoute, strategyCluster, strategyDeployment}

	for s := range strategies {
		if !slices.Contains(known, s) {
			return true
		}
	}

	return false
}

// encodeCandidates gives the YAML of the candidates document.
func encodeCandidates(candidates []candidate) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(candidateDoc{APIVersion: apiVersion, Kind: "BindingCandidates", Candidates: candidates}); err != nil {
		return nil, fmt.Errorf("encode candidates: %w", err)
	}

	return buf.Bytes(), nil
}

func bindDiscover(ctx context.Context, app *Context, opts *bindOptions) (int, error) {
	strategies, err := parseStrategies(opts.strategy)
	if err != nil {
		return diag.InvalidInput, err
	}

	bind, err := prepareBind(ctx, app, opts, strategies)
	if err != nil {
		return diag.Internal, err
	}

	candidates := discoverCandidates(bind.model, bind.files, bind.nodes, bind.links, bind.scope.include, strategies)
	if candidates == nil {
		candidates = []candidate{}
	}

	data, err := encodeCandidates(candidates)
	if err != nil {
		return diag.Internal, err
	}

	target := filepath.Join(bind.ws.Root, filepath.FromSlash(candidatesFile))
	if app.Opts.Output != "" {
		target = app.Abs(app.Opts.Output)
	}

	if err = fsutil.WriteFileAtomic(target, data); err != nil {
		return diag.Internal, diag.Fail(diag.REP001, diag.Params{keyPath: target, keyDetail: err.Error()})
	}

	app.Err(fmt.Sprintf("wrote %d binding candidate(s) to %s\n", len(candidates), target))

	return diag.OK, nil
}
