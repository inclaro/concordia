package conformance

import (
	"context"
	"os"
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// The assurance evidence is loaded once per run, in two steps, because two inputs depend on each other:
//
//   - The check ids of the check-result statements must be known before the policies compile (`require.checkResult`
//     and the `check-ids` of controls may name only those, CO-POL-005).
//   - The quality scenario files may name rules, and the rules are known only after the policies compiled.
//
// So loadAssurance loads everything except the scenario files (evidence.Options.DeferScenarios) before the compile, and
// loadScenarios reads the scenario files right after it, with the compiled rule ids. `concordia assurance validate`
// goes through LoadAssurance, which runs the same two steps, so the command and `check` cannot disagree.

// AssuranceLoad is what LoadAssurance gives: the workspace and the loaded assurance evidence.
type AssuranceLoad struct {
	Workspace *workspace.Workspace
	Evidence  *evidence.Evidence
}

// LoadAssurance loads the assurance inputs of the workspace the way `check` does and evaluates nothing. It needs no
// code graph: the revision comes from git. renderTwice renders every source a second time, to find non-deterministic
// output (design D18); a check run never does.
//
// The errors are those of evidence.Load: *evidence.ConfigError (exit 2), a toolchain *diag.Error (exit 5), and the
// context error of an interrupted run. A workspace without an `assurance` key gives an empty Evidence.
func LoadAssurance(ctx context.Context, opts *CheckOptions, renderTwice bool) (*AssuranceLoad, error) {
	chk := newChecker(opts)

	wspace, err := workspace.Load(opts.Env.WorkspacePath, opts.Env.Profile)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	inp := &inputs{lockPath: workspace.LockPathFor(wspace.Root)}

	if _, err := chk.loadModel(ctx, wspace, inp); err != nil {
		return nil, err
	}

	git := chk.newGit(wspace.Root)

	revision, err := git.Revision(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	dirty, err := git.DirtyFiles(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	dirty = slices.DeleteFunc(dirty, GeneratedPathFilter(wspace))

	loadCtx := assurance.Context{Now: chk.startedAt, Revision: revision, Offline: opts.Env.Offline, DirtyTree: len(dirty) > 0}

	if err := chk.loadAssuranceEvidence(ctx, wspace, inp, loadCtx, renderTwice); err != nil {
		return nil, err
	}

	// The policies compile against every canonical kind: validate has no provider, and a rule of an unsupported kind
	// is not its concern.
	caps := &policy.ProviderCaps{SupportedCanonicalKinds: observation.CanonicalEdgeKinds()}

	if err := chk.compilePolicies(wspace, inp, caps); err != nil {
		return nil, err
	}

	if err := chk.loadScenarios(wspace, inp); err != nil {
		return nil, err
	}

	result := &AssuranceLoad{Workspace: wspace, Evidence: &evidence.Evidence{}}
	if chk.assurance != nil {
		result.Evidence = chk.assurance.ev
	}

	return result, nil
}

// loadAssurance loads the assurance evidence of a check run, without the scenario files, and sets what the policy
// compile and the evaluation read: the assurance state, the external evaluation, and the known check ids.
func (c *checker) loadAssurance(ctx context.Context, wspace *workspace.Workspace, state *frozen, inp *inputs) error {
	lead := state.lead()

	loadCtx := assurance.Context{Now: c.startedAt, Revision: lead.revision, Offline: c.opts.Env.Offline, DirtyTree: len(lead.dirty) > 0}

	return c.loadAssuranceEvidence(ctx, wspace, inp, loadCtx, false)
}

func (c *checker) loadAssuranceEvidence(ctx context.Context, wspace *workspace.Workspace, inp *inputs, loadCtx assurance.Context, renderTwice bool) error {
	cfg := wspace.Assurance
	if cfg == nil {
		return nil
	}

	lock, _, err := workspace.ReadLock(inp.lockPath)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	environments := make(map[string]string, len(wspace.Environments))
	for i := range wspace.Environments {
		environments[wspace.Environments[i].ID] = wspace.Environments[i].Cluster
	}

	getenv := c.opts.Deps.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	found, err := evidence.Load(ctx, &evidence.Options{
		Assurance: cfg, Environments: environments, Pins: evidence.PinsFromLock(lock), Root: wspace.Root,
		Context: loadCtx, DeferScenarios: true, RenderTwice: renderTwice, Getenv: getenv, Locate: render.LocatorWith(getenv),
	})
	if err != nil {
		return err //nolint:wrapcheck // a *evidence.ConfigError, a toolchain *diag.Error, or the context error of an interrupted run.
	}

	c.diags = append(c.diags, found.Diagnostics...)

	run := &assuranceRun{cfg: cfg, ctx: loadCtx, bindings: inp.bindings.Bindings, ev: found, report: newAssuranceReport()}
	run.report.Rendered = renderedEntries(found)
	run.report.Plans = planEntries(found)
	run.report.Statements = found.Statements
	run.report.CheckResults = checkResultEntries(found)

	c.assurance = run
	c.knownCheckIDs = found.CheckIDs
	c.external = &ExternalEvaluation{
		Claims: loadedContracts(cfg), Rule: c.externalRule, Checks: externalChecks(found, c.environments.Excluded), Locate: run.locate,
	}

	return nil
}

// loadScenarios reads the quality scenario files with the ids of the model and of the compiled rules.
func (c *checker) loadScenarios(wspace *workspace.Workspace, inp *inputs) error {
	if c.assurance == nil {
		return nil
	}

	known := quality.Known{Elements: elementIDs(inp.model), Rules: make(map[string]struct{}, len(inp.compiled.RuleIDs))}
	for _, id := range inp.compiled.RuleIDs {
		known.Rules[id] = struct{}{}
	}

	return c.assurance.ev.LoadScenarios(wspace.Assurance, wspace.Root, known) //nolint:wrapcheck // a *evidence.ConfigError (exit 2).
}

// elementIDs gives the ids of the nodes and relationships of the model: what a scenario may name as its scope.
func elementIDs(model *intent.ArchitectureModel) map[string]struct{} {
	ids := make(map[string]struct{}, len(model.Nodes)+len(model.Relationships))

	for i := range model.Nodes {
		ids[model.Nodes[i].ID] = struct{}{}
	}

	for i := range model.Relationships {
		ids[model.Relationships[i].ID] = struct{}{}
	}

	return ids
}

// loadedContracts is the claim registry narrowed to the contracts whose inputs the workspace declares. A claim whose
// contract is not loaded is `claim-not-enabled`, so a workspace that declares no plan cannot pass a plan claim.
func loadedContracts(cfg *workspace.Assurance) *claims.Registry {
	declared := func(contractID string) bool {
		switch contractID {
		case "rendering":
			// The rendering contract is always loaded with an `assurance` key: an environment with no rendering source is
			// `rendered-evidence-missing`, not a claim that no contract could enable.
			return true
		case "plan":
			return len(cfg.Plans) > 0
		case "provenance":
			return cfg.Provenance != nil || cfg.Records != nil
		case "verification":
			return cfg.Verification != nil
		case "quality":
			return cfg.Quality != nil
		default:
			// An evaluator contract is loaded when an environment enables the evaluator of that kind.
			return slices.ContainsFunc(cfg.Evaluators, func(e workspace.AssuranceEvaluator) bool { return slices.Contains(e.Kinds, contractID) })
		}
	}

	var loaded []claims.Contract

	registered := claims.Default().Contracts()
	for i := range registered {
		contract := &registered[i]
		if declared(contract.ID) {
			loaded = append(loaded, *contract)
		}
	}

	registry, err := claims.New(loaded)
	if err != nil {
		// A subset of the compiled-in registry is valid: an error here is a defect that the tests catch.
		panic(err.Error())
	}

	return registry
}
