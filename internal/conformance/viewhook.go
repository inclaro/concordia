package conformance

import (
	"cmp"
	"context"
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// ViewInput is the state of a run that the view checks read (`internal/views`): the model and the evidence as the
// reconciliation sees them, the merged evidence with its entities and derived `placed-in` facts, the bindings,
// the rules, the predicate coverage and the reconciliation result. It is plain data; no rule result, finding,
// disposition, waiver, or baseline is in it, so a committed artifact does not depend on them.
type ViewInput struct {
	// Workspace is the loaded workspace (the view definitions, the environments, the projects).
	Workspace *workspace.Workspace
	// Reconcile is the input of the reconciliation of the run: the model, the binding set, the evidence and the
	// supported edge kinds.
	Reconcile *ReconcileInput
	// Evidence is the merged evidence of the run, with the synthesized entities and the derived `placed-in` facts.
	Evidence *observation.EvidenceQueryResult
	// Bindings are the bindings of the workspace.
	Bindings []binding.Binding
	// Policies are the compiled rules of the run.
	Policies []policy.RulePlan
	// Coverage are the predicate coverage records of the run.
	Coverage []observation.PredicateCoverage
	// Reconciliation is the classification of the declared relationships.
	Reconciliation []ReconciliationEntry
}

// ViewDiagnosticsFunc checks the view definitions of the workspace against the loaded model
// and gives the suggested default views. An error-severity diagnostic stops the run with its
// exit code; the other diagnostics are part of the run result. `internal/views` implements
// it (this package cannot import it); the CLI injects it as Deps.ViewDiagnostics.
type ViewDiagnosticsFunc func(ctx context.Context, wspace *workspace.Workspace, model *intent.ArchitectureModel, git workspace.GitPort) ([]diag.Diagnostic, error)

// checkViewDiagnostics runs Deps.ViewDiagnostics. With no hook it does nothing.
func (c *checker) checkViewDiagnostics(ctx context.Context, wspace *workspace.Workspace, model *intent.ArchitectureModel) error {
	hook := c.opts.Deps.ViewDiagnostics
	if hook == nil {
		return nil
	}

	found, err := hook(ctx, wspace, model, c.newGit(wspace.Root))
	if err != nil {
		return err
	}

	failures := slices.DeleteFunc(slices.Clone(found), func(d diag.Diagnostic) bool { return d.Severity != diag.SeverityError })
	if len(failures) > 0 {
		exit, stops := diag.ExitCodeOf(failures[0].Code)
		if !stops {
			exit = diag.InvalidInput
		}

		first := failures[0]
		first.Cause = append(slices.Clone(first.Cause), failures[1:]...)

		return &diag.Error{Diagnostic: first, ExitCode: exit}
	}

	c.diags = append(c.diags, found...)

	return nil
}

// ViewEvalInput is the input of Deps.EvaluateViews: the state of the run that the checks read, the Git port of the
// workspace, and the settings of the run.
type ViewEvalInput struct {
	// State is the state of the run (see ViewInput).
	State *ViewInput
	// Currency is the assessment evaluation of the same run. The view `assessmentCurrency` entry lists the matching
	// assessments from it without a second evaluation. nil: the entry is `not-evaluated`.
	Currency *assessment.Evaluation
	// Git is the Git port of the workspace root: presence and freshness read the tracked revision through it.
	Git workspace.GitPort
	// Getenv reads the environment for the Structurizr CLI. nil: the process environment.
	Getenv func(string) string
	// ToolVersion is the Concordia version.
	ToolVersion string
	// OnUnknown is the effective `check.unknown` of the run: the `onUnknown` of the view rules.
	OnUnknown policy.UnknownDisposition
}

// ViewEvaluation is the output of Deps.EvaluateViews: one result for each declared view, and the rule results and
// findings of the `views` pack. They are treated as the rest of the findings: dispositions, `check.failOn`,
// `check.unknown` and the exit code apply (design D1, D3).
type ViewEvaluation struct {
	// StructurizrVersion is the Structurizr CLI version that regenerated an export. Empty when no export was verified.
	StructurizrVersion string
	// Rules are the results of VIEW-PRES-001, VIEW-FRESH-001 and VIEW-CORR-001.
	Rules []RuleResult
	// Findings are the findings of the rules, with their fingerprints.
	Findings []Finding
	// Views are the view results, sorted by view ID.
	Views []ViewResult
}

// EvaluateViewsFunc runs the four checks of every declared view. `internal/views` implements it (this package
// cannot import it); the CLI injects it as Deps.EvaluateViews.
type EvaluateViewsFunc func(ctx context.Context, in *ViewEvalInput) (*ViewEvaluation, error)

// needsViewInput says whether the run keeps the state that the view checks read.
func (c *checker) needsViewInput(wspace *workspace.Workspace) bool {
	return c.opts.KeepViewInput || (len(wspace.Views) > 0 && c.opts.Deps.EvaluateViews != nil)
}

// evaluateViews runs Deps.EvaluateViews after Evaluate and appends the rule results and findings to the output of
// the evaluation, so that dispositions and the exit code treat them as normal findings. With no hook, or no
// declared view, it does nothing.
func (c *checker) evaluateViews(ctx context.Context, wspace *workspace.Workspace, evaluated *EvaluateOutput, input *EvaluateInput) error {
	hook := c.opts.Deps.EvaluateViews
	if hook == nil || len(wspace.Views) == 0 || c.viewInput == nil {
		return nil
	}

	onUnknown := policy.UnknownDisposition(wspace.Check.Unknown)
	if c.opts.Unknown != "" {
		onUnknown = c.opts.Unknown
	}

	out, err := hook(ctx, &ViewEvalInput{
		State: c.viewInput, Git: c.newGit(wspace.Root), Getenv: c.opts.Deps.Getenv,
		ToolVersion: c.opts.Env.ToolVersion, OnUnknown: onUnknown, Currency: c.currencyEvaluation(),
	})
	if err != nil {
		return err
	}

	for i := range out.Rules {
		rule := &out.Rules[i]
		rule.Enforcement = c.enforcement.Resolve(&rule.Plan, "").Mode
	}

	for i := range out.Findings {
		finding := &out.Findings[i]

		if at := slices.IndexFunc(out.Rules, func(r RuleResult) bool { return r.RuleID == finding.RuleID }); at >= 0 {
			finding.Enforcement = out.Rules[at].Enforcement
		}
	}

	completeFindings(out.Findings, out.Rules, input, evaluated.Reconciliation)

	evaluated.RuleResults = append(evaluated.RuleResults, out.Rules...)
	slices.SortStableFunc(evaluated.RuleResults, func(a, b RuleResult) int { return compareStrings(a.RuleID, b.RuleID) })

	evaluated.Findings = append(evaluated.Findings, out.Findings...)
	slices.SortStableFunc(evaluated.Findings, func(a, b Finding) int {
		return cmp.Or(compareStrings(a.RuleID, b.RuleID), compareStrings(a.Fingerprint, b.Fingerprint))
	})

	c.views = out.Views
	c.structurizr = out.StructurizrVersion

	SortViews(c.views)

	return nil
}

// viewEvidenceKinds gives the canonical edge kinds that the checks of the declared views read: the code kinds
// that the observed projection compares (calls and imports), the diagram arrows of an authored structured view,
// and the kinds that `projection.edgeKinds` names. `check` with no policy rule fetches only DEFINES, so a
// workspace with views adds these kinds to the fetch. Without views the result is empty.
func viewEvidenceKinds(wspace *workspace.Workspace) []observation.CanonicalEdgeKind {
	if len(wspace.Views) == 0 {
		return nil
	}

	kinds := []observation.CanonicalEdgeKind{observation.KindCalls, observation.KindImports, observation.KindDiagramArrow}

	for i := range wspace.Views {
		if wspace.Views[i].Projection == nil {
			continue
		}

		for _, token := range wspace.Views[i].Projection.EdgeKinds {
			if resolved, ok := edgekind.Resolve(token); ok {
				kinds = append(kinds, resolved...)
			}
		}
	}

	slices.Sort(kinds)

	return slices.Compact(kinds)
}

// viewsNeedPlacement says whether a declared view reads the `placed-in` facts: a deployment view does.
func viewsNeedPlacement(wspace *workspace.Workspace) bool {
	return slices.ContainsFunc(wspace.Views, func(v workspace.View) bool { return v.Kind == workspace.ViewDeployment })
}
