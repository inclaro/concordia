package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/projection"
	"github.com/inclaro/concordia/internal/workspace"
)

// ErrNoObservedEvidence is the cause of the error of a view that needs the observed evidence of a run when the
// caller has none (`render --views --results`: a results file holds no evidence).
var ErrNoObservedEvidence = errors.New("the observed evidence of a run is not available")

// Inputs is the state that a generated view is built from. `render --views` and the freshness check build the
// same view from the same inputs, so that they agree byte for byte.
type Inputs struct {
	Workspace *workspace.Workspace
	Model     *intent.ArchitectureModel
	// Observed is the evidence of the run. nil: the run has none (a results file).
	Observed *ObservedEvidence
	// Bindings, Policies: the bindings and the compiled rules of the run.
	Bindings []binding.Binding
	Policies []policy.RulePlan
	// Reconciliation is the classification of the declared relationships; the committed DSL reads nothing else of
	// the run (design D6).
	Reconciliation []conformance.ReconciliationEntry
}

// ObservedEvidence is the observed side of the inputs.
type ObservedEvidence struct {
	// Reconcile is the reconciliation input of the run: the model, the binding set, the evidence, the kinds.
	Reconcile *conformance.ReconcileInput
	// Evidence is the merged evidence with its entities and derived `placed-in` facts (the placement input).
	Evidence *observation.EvidenceQueryResult
	// Coverage are the predicate coverage records of the run.
	Coverage []observation.PredicateCoverage
}

// InputsOf gives the inputs of the state that the pipeline keeps (conformance.CheckOptions.KeepViewInput).
func InputsOf(kept *conformance.ViewInput) *Inputs {
	return &Inputs{
		Workspace: kept.Workspace, Model: kept.Reconcile.Model, Bindings: kept.Bindings, Policies: kept.Policies,
		Reconciliation: kept.Reconciliation,
		Observed:       &ObservedEvidence{Reconcile: kept.Reconcile, Evidence: kept.Evidence, Coverage: kept.Coverage},
	}
}

// NeedsObserved says whether the projections of a view read the observed evidence of the run: it compares
// against the observed graph, or it is a deployment view (its placement comes from `placed-in` facts).
func NeedsObserved(view *Definition) bool {
	return view.Kind == KindDeployment || slices.Contains(view.EffectiveAgainst(), workspace.CompareObserved)
}

// Generated is a generated view that is built in memory: its two projections, the digests, and the DSL.
type Generated struct {
	View *Definition
	// Model is the model projection. Observed is the observed projection, nil when the view compares only
	// against the declared projection (an explicit missing value in the manifest).
	Model    *Projection
	Observed *Projection
	Digests  Digests
	// DSL is the committed Structurizr DSL of the view (output.path).
	DSL []byte
	// Diagnostics are the non-error diagnostics of the DSL generation (CO-PRJ-006, CO-PRJ-008).
	Diagnostics []diag.Diagnostic
}

// DSLDigest is the digest of the DSL bytes.
func (g *Generated) DSLDigest() string { return digest.OfBytes(g.DSL) }

// Generate builds a generated view in memory: the projections, the digests and the DSL. It does not run the
// Structurizr CLI (see RenderExports) and writes nothing.
func Generate(inputs *Inputs, view *Definition) (*Generated, error) {
	if view.Origin != OriginGenerated || view.Output == nil {
		return nil, fmt.Errorf("view %s is not a generated view", view.ID)
	}

	snap, err := SnapshotOf(inputs, view)
	if err != nil {
		return nil, err
	}

	out := &Generated{View: view, Model: snap.Model, Observed: snap.Observed, Digests: snap.Digests}

	dsl, err := committedDSL(inputs, view, snap.Placements, snap.Coverage, &out.Diagnostics)
	if err != nil {
		return nil, fmt.Errorf("view %s: %w", view.ID, err)
	}

	out.DSL = []byte(dsl)

	return out, nil
}

func environmentsOf(wsp *workspace.Workspace) []deployment.Environment {
	out := make([]deployment.Environment, len(wsp.Environments))
	for i := range wsp.Environments {
		out[i] = deployment.Environment{ID: wsp.Environments[i].ID, Cluster: wsp.Environments[i].Cluster}
	}

	return out
}

// dslKinds maps a view kind to the Structurizr view kind that writes it.
var dslKinds = map[Kind]projection.ViewKind{
	KindSystemContext: projection.ViewContext,
	KindContainer:     projection.ViewContainer,
	KindComponent:     projection.ViewComponent,
	KindDeployment:    projection.ViewDeployment,
}

// committedDSL makes the Structurizr DSL of one view in committed mode (design D6): one Structurizr view with the
// key of the view ID, the status from the classification and the placement records only, and no run state.
//
// The scope is the focus when it names one element; a scope of several elements writes the view of each system
// (the first one has the key of the view ID). Only a deployment view has deployment environments, and then only
// its own: the DSL of the other kinds has no `deployed-in` placement, so a move of a workload leaves their bytes
// unchanged (their semantic digest does not cover placement either).
func committedDSL(inputs *Inputs, view *Definition, set deployment.PlacementSet, coverage []observation.PredicateCoverage, diagnostics *[]diag.Diagnostic) (string, error) {
	kind := dslKinds[view.Kind]
	model := inputs.Model
	opts := projection.DSLOptions{
		Views: map[projection.ViewKind]bool{kind: true}, ViewIDs: map[projection.ViewKind]string{kind: view.ID},
		Bindings: inputs.Bindings, WorkspaceName: inputs.Workspace.Metadata.Name, Diagnostics: diagnostics, Committed: true,
	}

	if len(view.Scope) == 1 {
		opts.Focus = view.Scope[0]
	}

	if view.Kind == KindDeployment {
		opts.Environments, opts.Placements, opts.Coverage = []string{view.Environment}, set, coverage
	} else {
		model = withoutPlacement(model)
	}

	dsl, err := projection.ToStructurizrDSL(model, &projection.RunView{Reconciliation: inputs.Reconciliation}, opts)
	if err != nil {
		return "", fmt.Errorf("make the DSL: %w", err)
	}

	return dsl, nil
}

// withoutPlacement gives the model without its `deployed-in` relationships.
func withoutPlacement(model *intent.ArchitectureModel) *intent.ArchitectureModel {
	stripped := *model
	stripped.Relationships = slices.DeleteFunc(slices.Clone(model.Relationships), func(r intent.RelationshipDeclaration) bool {
		return r.Kind == intent.RelDeployedIn
	})

	return &stripped
}

// Tools are the program settings that exports need.
type Tools struct {
	// Getenv supplies PATH and JAVA_HOME and the STRUCTURIZR_CLI setting. nil reads the process environment.
	Getenv func(string) string
	// WorkspaceRoot is the absolute workspace directory, for the allowed includes.
	WorkspaceRoot string
	// AllowIncludes is projection.allowIncludes.
	AllowIncludes []string
}

// ExportArtifact is one regenerated export, in the form that is committed.
type ExportArtifact struct {
	Path             string
	Format           string
	Digest           string
	Canonicalization string
	// Data is the canonical bytes of the export (projection.CanonicalizeExport).
	Data []byte
}

// ExportOutcome is the regeneration of one export: the artifact, or the reason that it could not be made.
type ExportOutcome struct {
	// Err is nil when Artifact is set.
	Err      error
	Artifact ExportArtifact
}

// ExportRun is the result of RenderExports.
type ExportRun struct {
	// Version is the Structurizr CLI version that made the exports ("" when no export ran).
	Version string
	// Outcomes are the exports in declaration order.
	Outcomes []ExportOutcome
}

// RenderExports regenerates the exports of the view with the pinned Structurizr CLI in the confined runner
// (design D8, D9). A failure belongs to its export (ExportOutcome.Err): a missing CLI is CO-PRJ-001, another
// version than the pin is CO-DGM-004, a refused directive is CO-DGM-003, an allowed include that cannot be read
// wraps projection.ErrIncludeUnavailable, and a failing CLI is CO-PRJ-002.
func RenderExports(ctx context.Context, generated *Generated, tools *Tools) ExportRun {
	exports := generated.View.Output.Exports
	if len(exports) == 0 {
		return ExportRun{}
	}

	run := ExportRun{Outcomes: make([]ExportOutcome, len(exports))}
	failAll := func(err error) ExportRun {
		for i := range run.Outcomes {
			run.Outcomes[i].Err = err
		}

		return run
	}

	getenv := tools.Getenv

	cli, ok := projection.FindCLI(orProcessEnv(getenv))
	if !ok {
		return failAll(diag.Fail(diag.PRJ001, diag.Params{"format": projection.NeedViewExports}))
	}

	version, err := projection.VerifyPinned(ctx, cli, getenv)
	if err != nil {
		return failAll(err) //nolint:wrapcheck // a *diag.Error that the caller classifies.
	}

	run.Version = version

	for i, export := range exports {
		run.Outcomes[i] = renderExport(ctx, generated, tools, cli, export)
	}

	return run
}

func renderExport(ctx context.Context, generated *Generated, tools *Tools, cli string, export workspace.ViewExport) ExportOutcome {
	format := projection.ExportFormat(export.Format)

	rule, ok := projection.CanonicalizationFor(format)
	if !ok {
		return ExportOutcome{Err: fmt.Errorf("%w: %s", projection.ErrNoCanonicalization, export.Format)}
	}

	res, err := projection.RunConfined(ctx, &projection.ConfinedOptions{
		Getenv: tools.Getenv, CLI: cli, Format: format, WorkspaceRoot: tools.WorkspaceRoot, DSL: generated.DSL,
		AllowIncludes: tools.AllowIncludes, VerifiedPin: true,
	})
	if err != nil {
		return ExportOutcome{Err: err}
	}

	name := exportFileName(format, generated.View.ID)

	idx := slices.IndexFunc(res.Files, func(f projection.ExportFile) bool { return f.Path == name })
	if idx < 0 {
		return ExportOutcome{Err: fmt.Errorf("the %s export did not write %s", export.Format, name)}
	}

	data, err := projection.CanonicalizeExport(format, res.Files[idx].Data)
	if err != nil {
		return ExportOutcome{Err: err} //nolint:wrapcheck // the cause names the format.
	}

	return ExportOutcome{Artifact: ExportArtifact{
		Path: export.Path, Format: export.Format, Data: data, Digest: digest.OfBytes(data), Canonicalization: rule,
	}}
}

// exportFileName is the file of the Structurizr export that holds the view with the given key: the first view of a
// kind has the view ID as its key.
func exportFileName(format projection.ExportFormat, key string) string {
	switch format {
	case projection.FormatJSON:
		return "workspace.json"
	case projection.FormatMermaid:
		return "structurizr-" + key + ".mmd"
	case projection.FormatPlantUML:
		return "structurizr-" + key + ".puml"
	case projection.FormatDot:
		return "structurizr-" + key + ".dot"
	case projection.FormatStatic:
	}

	return key
}

func orProcessEnv(getenv func(string) string) func(string) string {
	if getenv != nil {
		return getenv
	}

	return os.Getenv
}
