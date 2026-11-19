package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/projection"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

const formatDSL = "dsl"

type renderOptions struct {
	view         string
	focus        string
	architecture string
	workspaceOut string
	results      string
	finding      string
	viewID       string
	open         bool
	views        bool
}

var renderViews = []projection.ViewKind{
	projection.ViewLandscape, projection.ViewContext, projection.ViewContainer, projection.ViewComponent, projection.ViewDrift,
	projection.ViewDeployment, projection.ViewDynamic,
}

func newRenderCmd(host *Host) *cobra.Command {
	var opts renderOptions

	cmd := &cobra.Command{
		Use:   "render",
		Short: "Project the architecture and conformance status to a Structurizr DSL workspace (--format dsl|json|mermaid|plantuml|dot|static)",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, renderHandler(ctx, app, &opts)
		}),
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.view, "view", "", "landscape|context|container|component|drift|deployment|dynamic")
	flags.StringVar(&opts.focus, "focus", "", "limit views to the system or container that contains this CALM node")
	flags.StringVar(&opts.architecture, "architecture", "", "CALM architecture file")
	flags.StringVar(&opts.workspaceOut, "workspace-out", "", "DSL output path (default <projection.output>/workspace.dsl)")
	flags.StringVar(&opts.results, "results", "", "use a results.json instead of running the check")
	flags.StringVar(&opts.finding, "finding", "", "add a dynamic view of this finding (fingerprint or hex prefix)")
	flags.BoolVar(&opts.open, "open", false, "open the output when it is written")
	flags.BoolVar(&opts.views, "views", false, "write the declared generated views (artifact, exports, manifest) at their output paths")
	flags.StringVar(&opts.viewID, "view-id", "", "with --views: write only this view")

	return cmd
}

func renderRun(ctx context.Context, app *Context, results string) (*projection.RunView, error) {
	if results != "" {
		var view projection.RunView

		data, err := os.ReadFile(app.Abs(results))
		if err == nil {
			err = json.Unmarshal(data, &view)
		}

		if err != nil {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("cannot read results %s: %s", results, err)))
		}

		return &view, nil
	}

	run, err := runPipeline(ctx, app)
	if err != nil {
		return nil, err
	}

	return projection.NewRunView(run), nil
}

// validateRenderFormat checks that format is dsl or an export format.
func validateRenderFormat(format string) error {
	if format == formatDSL {
		return nil
	}

	exportFormats := make([]string, len(projection.ExportFormats))
	for i, f := range projection.ExportFormats {
		exportFormats[i] = string(f)
	}

	if slices.Contains(exportFormats, format) {
		return nil
	}

	return diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported format '%s' (dsl, %s)", format, strings.Join(exportFormats, ", "))))
}

// validateRenderView checks --view and its need for --finding.
func validateRenderView(opts *renderOptions) error {
	if opts.view == string(projection.ViewDynamic) && opts.finding == "" {
		return diag.Fail(diag.WRK010, diag.Detail("--view dynamic needs --finding"))
	}

	if opts.view == "" || slices.Contains(renderViews, projection.ViewKind(opts.view)) {
		return nil
	}

	names := make([]string, len(renderViews))
	for i, v := range renderViews {
		names[i] = string(v)
	}

	return diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported --view '%s' (%s)", opts.view, strings.Join(names, ", "))))
}

// checkRenderOptions checks --format and --view and finds the Structurizr CLI when the format needs it.
func checkRenderOptions(app *Context, opts *renderOptions) (format, cli string, err error) {
	format = app.Opts.Format
	if format == "" {
		format = formatDSL
	}

	if err := validateRenderFormat(format); err != nil {
		return "", "", err
	}

	if err := validateRenderView(opts); err != nil {
		return "", "", err
	}

	// Fail early when an exported format needs a missing CLI.
	if format == formatDSL {
		return format, "", nil
	}

	if cli, err = projection.RequireCLI(format, app.IO.Getenv); err != nil {
		return "", "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return format, cli, nil
}

// renderArchitecturePath finds the local path of the architecture file: --architecture or architecture.source.
func renderArchitecturePath(ctx context.Context, app *Context, wsp *workspace.Workspace, opts *renderOptions) (string, error) {
	if opts.architecture != "" {
		return app.Abs(opts.architecture), nil
	}

	source := wsp.Architecture.Source
	if source == "" {
		return "", diag.Fail(diag.WRK010, diag.Detail("no architecture: pass --architecture or set architecture.source"))
	}

	local, _, err := remote.ResolvePath(ctx, app.remoteResolver(wsp), source)
	if err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if strings.HasPrefix(source, "https://") {
		return local, nil
	}

	return wsp.ResolveInRoot(local), nil
}

// loadRenderModel loads the architecture that the views show.
func loadRenderModel(ctx context.Context, app *Context, wsp *workspace.Workspace, opts *renderOptions) (*intent.ArchitectureModel, error) {
	archPath, err := renderArchitecturePath(ctx, app, wsp, opts)
	if err != nil {
		return nil, err
	}

	res := app.remoteResolver(wsp)

	urlMapping, err := intent.LoadURLMapping(wsp)
	if err != nil {
		return nil, fmt.Errorf("load url mapping: %w", err)
	}

	patterns, err := resolveArchitectureFiles(ctx, res, wsp, wsp.Architecture.Patterns)
	if err != nil {
		return nil, err
	}

	decorators, err := resolveArchitectureFiles(ctx, res, wsp, wsp.Architecture.Decorators)
	if err != nil {
		return nil, err
	}

	loaded := intent.LoadArchitecture(archPath, intent.LoadContext{
		Root: wsp.Root, URLMapping: urlMapping, Patterns: patterns,
		Decorators: decorators, ArchitectureURL: remoteArchitectureURL(wsp, opts.architecture), Trust: trust.FromWorkspace(wsp),
		Remote: func(u string) (string, string, string) { return res.Resolve(ctx, u) },
	})
	if err := policy.ThrowOnErrors(loaded.Diagnostics); err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if loaded.Model == nil {
		return nil, diag.Fail(diag.INT001, diag.Detail("architecture not loaded"))
	}

	return loaded.Model, nil
}

func renderHandler(ctx context.Context, app *Context, opts *renderOptions) error {
	if err := renderViewsOptions(opts, app.Opts.Format); err != nil {
		return err
	}

	if opts.views {
		return renderViewsHandler(ctx, app, opts)
	}

	format, cli, err := checkRenderOptions(app, opts)
	if err != nil {
		return err
	}

	return renderWithRun(ctx, app, &renderJob{opts: opts, format: format, cli: cli})
}

// renderDSLFromRun writes the DSL workspace for a run that is already computed, whatever the global --format is.
func renderDSLFromRun(ctx context.Context, app *Context, opts *renderOptions, run *conformance.Run) error {
	if opts.view == string(projection.ViewDynamic) && opts.finding == "" {
		return diag.Fail(diag.WRK010, diag.Detail("--view dynamic needs --finding"))
	}

	return renderWithRun(ctx, app, &renderJob{opts: opts, format: formatDSL, run: projection.NewRunView(run)})
}

// renderJob is one render: the options, the export format with its Structurizr CLI, and the run to show.
// A nil run gives the result of a new run, or --results. An empty cli writes the DSL only.
type renderJob struct {
	opts   *renderOptions
	run    *projection.RunView
	format string
	cli    string
}

// renderOutput is where a render writes: the DSL file and the directory of the projection output.
type renderOutput struct {
	dslPath string
	outDir  string
}

// checkRenderFocus checks that --focus names a node of the architecture.
func checkRenderFocus(model *intent.ArchitectureModel, focus string) error {
	if focus == "" || slices.ContainsFunc(model.Nodes, func(n intent.NodeDeclaration) bool { return n.ID == focus }) {
		return nil
	}

	return diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unknown --focus '%s'", focus)))
}

// buildRenderDSL loads the bindings and makes the DSL text of the job. Its diagnostics go to stderr.
func buildRenderDSL(app *Context, wsp *workspace.Workspace, model *intent.ArchitectureModel, job *renderJob) (string, error) {
	bindings, err := binding.Load(wsp, model)
	if err != nil {
		return "", fmt.Errorf("load bindings: %w", err)
	}

	if err := policy.ThrowOnErrors(bindings.Diagnostics); err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	var diagnostics []diag.Diagnostic

	dslOpts, err := renderDSLOptions(wsp, job.opts, job.run, bindings.Bindings, &diagnostics)
	if err != nil {
		return "", err
	}

	dsl, err := projection.ToStructurizrDSL(model, job.run, dslOpts)
	if err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	for _, d := range diagnostics {
		app.Err(diag.FormatDiagnostic(d) + "\n")
	}

	return dsl, nil
}

// writeRenderDSL writes the DSL text to --workspace-out or the projection output.
func writeRenderDSL(app *Context, wsp *workspace.Workspace, opts *renderOptions, dsl string) (renderOutput, error) {
	outDir := wsp.ResolveInRoot(wsp.Projection.Output)
	dslPath := filepath.Join(outDir, "workspace.dsl")

	if opts.workspaceOut != "" {
		dslPath = app.Abs(opts.workspaceOut)
	}

	if err := fsutil.WriteFileAtomic(dslPath, []byte(dsl)); err != nil {
		return renderOutput{}, diag.Fail(diag.REP001, diag.Params{keyPath: dslPath, keyDetail: err.Error()})
	}

	app.Err("workspace written: " + dslPath + "\n")

	return renderOutput{dslPath: dslPath, outDir: outDir}, nil
}

// renderWithRun renders the job.
func renderWithRun(ctx context.Context, app *Context, job *renderJob) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	model, err := loadRenderModel(ctx, app, wsp, job.opts)
	if err != nil {
		return err
	}

	if err := checkRenderFocus(model, job.opts.focus); err != nil {
		return err
	}

	if job.run == nil {
		if job.run, err = renderRun(ctx, app, job.opts.results); err != nil {
			return err
		}
	}

	dsl, err := buildRenderDSL(app, wsp, model, job)
	if err != nil {
		return err
	}

	out, err := writeRenderDSL(app, wsp, job.opts, dsl)
	if err != nil {
		return err
	}

	if job.cli == "" {
		openOutput(ctx, app, job.opts, out.dslPath)

		return nil
	}

	return exportRendered(ctx, app, job, out)
}

// renderBuildGlobs match the build files that bind a dynamic view to its elements.
var renderBuildGlobs = []string{"BUILD", "BUILD.bazel", "BUCK", "**/BUILD", "**/BUILD.bazel", "**/BUCK"}

// renderDSLOptions makes the DSL options: the views, and the dynamic view of --finding.
func renderDSLOptions(wsp *workspace.Workspace, opts *renderOptions, run *projection.RunView, bindings []binding.Binding, diagnostics *[]diag.Diagnostic) (projection.DSLOptions, error) {
	dslOpts := projection.DSLOptions{Bindings: bindings, WorkspaceName: wsp.Metadata.Name, Diagnostics: diagnostics, Focus: opts.focus}
	for _, env := range wsp.Environments {
		dslOpts.Environments = append(dslOpts.Environments, env.ID)
	}

	if opts.view != "" {
		dslOpts.Views = map[projection.ViewKind]bool{projection.ViewKind(opts.view): true}
		if opts.finding != "" {
			dslOpts.Views[projection.ViewDynamic] = true
		}
	}

	if opts.finding == "" {
		return dslOpts, nil
	}

	files, err := binding.ExpandFileGlobs(wsp.Root, renderBuildGlobs)
	if err != nil {
		return dslOpts, fmt.Errorf("find build files: %w", err)
	}

	dslOpts.Dynamic, err = dynamicPathOf(run, opts.finding, bindings, binding.BuildFiles(files), diagnostics)

	return dslOpts, err
}

// exportRendered validates the DSL file with the Structurizr CLI, exports it and opens the result for --open.
func exportRendered(ctx context.Context, app *Context, job *renderJob, out renderOutput) error {
	if err := projection.Validate(ctx, job.cli, out.dslPath); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	exportDir := filepath.Join(out.outDir, job.format)
	if err := projection.Export(ctx, job.cli, out.dslPath, projection.ExportFormat(job.format), exportDir); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	app.Err(fmt.Sprintf("exported %s: %s\n", job.format, exportDir))

	target := exportDir
	if job.format == "static" {
		if index := filepath.Join(exportDir, "index.html"); fileExists(index) {
			target = index
		}
	}

	openOutput(ctx, app, job.opts, target)

	return nil
}

// openOutput opens path when --open is set. A failure gives a CO-PRJ-007 warning.
func openOutput(ctx context.Context, app *Context, opts *renderOptions, path string) {
	if !opts.open {
		return
	}

	if err := app.openPath(ctx, path); err != nil {
		app.Err(diag.FormatDiagnostic(diag.New(diag.PRJ007, diag.Detail(err.Error()))) + "\n")
	}
}

// findingByPrefix finds the one finding whose fingerprint starts with hex.
func findingByPrefix(run *projection.RunView, wanted, hex string) (*conformance.Finding, error) {
	var hits []*conformance.Finding

	if len(hex) >= minPrefixLength {
		for i := range run.Findings {
			if strings.HasPrefix(hexOf(run.Findings[i].Fingerprint), hex) {
				hits = append(hits, &run.Findings[i])
			}
		}
	}

	switch len(hits) {
	case 0:
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("no finding '%s'", wanted)))
	case 1:
		return hits[0], nil
	default:
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("finding prefix '%s' is not unique", wanted)))
	}
}

// preferredID picks the first of preferred that is in ids, else the first of ids, else "".
func preferredID(ids, preferred []string) string {
	for _, candidate := range preferred {
		if slices.Contains(ids, candidate) {
			return candidate
		}
	}

	if len(ids) == 0 {
		return ""
	}

	return ids[0]
}

// lastEvidenceNode is the last node on the evidence path of the finding.
func lastEvidenceNode(finding *conformance.Finding) *observation.ObservedNode {
	var last *observation.ObservedNode

	for i := range finding.EvidencePath {
		if n := finding.EvidencePath[i].Node; n != nil {
			last = n
		}
	}

	return last
}

// dynamicWalk joins the elements that the nodes of an evidence path are bound to into steps.
type dynamicWalk struct {
	// leave is the first edge since the last element: it labels the next step.
	leave *conformance.StepEdge
	prev  string
	steps []projection.DynamicStep
	// seen counts the nodes that were visited.
	seen int
}

// noteEdge remembers the first edge that leaves the current element.
func (w *dynamicWalk) noteEdge(edge *conformance.StepEdge) {
	if w.leave == nil {
		w.leave = edge
	}
}

// visit adds a step when the node is bound to another element than the previous one.
func (w *dynamicWalk) visit(elementID string) {
	switch {
	case w.prev == "":
		w.prev = elementID
	case elementID == w.prev:
		w.leave = nil
	default:
		w.steps = append(w.steps, projection.DynamicStep{FromID: w.prev, ToID: elementID, Label: dynamicLabel(w.leave)})
		w.prev, w.leave = elementID, nil
	}
}

// dynamicPreferred are the elements that a node of the path is bound to by preference: the source for the first node,
// the targets for the last one.
func dynamicPreferred(finding *conformance.Finding, first, last bool) []string {
	switch {
	case first:
		return []string{finding.Source.ArchitectureID}
	case last:
		return strings.Split(finding.Target.ArchitectureID, ",")
	default:
		return nil
	}
}

// dynamicSteps walks the evidence path of the finding and joins the elements that its nodes are bound to.
func dynamicSteps(finding *conformance.Finding, bindings []binding.Binding, buildFiles []string) []projection.DynamicStep {
	last := lastEvidenceNode(finding)
	idsOf := func(n *observation.ObservedNode) []string {
		return binding.Resolve(bindings, []observation.ObservedNode{*n}, buildFiles).ByObserved[n.ObservedID]
	}

	var walk dynamicWalk

	for i := range finding.EvidencePath {
		step := &finding.EvidencePath[i]

		if step.Edge != nil {
			walk.noteEdge(step.Edge)

			continue
		}

		preferred := dynamicPreferred(finding, walk.seen == 0, step.Node == last)
		walk.seen++

		if elementID := preferredID(idsOf(step.Node), preferred); elementID != "" {
			walk.visit(elementID)
		}
	}

	return walk.steps
}

// dynamicPathOf makes the dynamic view of the finding with the given fingerprint prefix.
// It gives nil and no error when the path stays inside one element (PRJ008 is then in diagnostics).
func dynamicPathOf(run *projection.RunView, wanted string, bindings []binding.Binding, buildFiles []string, diagnostics *[]diag.Diagnostic) (*projection.DynamicPath, error) {
	hex := hexOf(wanted)

	finding, err := findingByPrefix(run, wanted, hex)
	if err != nil {
		return nil, err
	}

	steps := dynamicSteps(finding, bindings, buildFiles)
	if len(steps) == 0 {
		*diagnostics = append(*diagnostics, diag.New(diag.PRJ008, diag.Detail("the evidence path stays inside one element")))

		return nil, nil //nolint:nilnil // no dynamic view; the diagnostic says why.
	}

	return &projection.DynamicPath{
		Key:   "evidence-" + hex[:min(fingerprintShort, len(hex))],
		Title: finding.RuleID + ": " + finding.Message,
		Steps: steps,
	}, nil
}

func dynamicLabel(e *conformance.StepEdge) string {
	if e == nil {
		return "evidence"
	}

	label := e.NativeKind + " " + string(e.Confidence)
	if e.Location != nil {
		label += " " + locationText(e.Location)
	}

	return label
}
