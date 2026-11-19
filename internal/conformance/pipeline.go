package conformance

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

// Env is what the caller (the CLI) gives to a check run.
type Env struct {
	// Logger gets diagnostics and trace events. May be nil.
	Logger *logger.Logger
	// RunID is the ID of this run (see internal/runid).
	RunID string
	// WorkspacePath is the absolute path of the workspace file.
	WorkspacePath string
	// Cwd is the working directory. Relative flag paths are relative to it.
	Cwd string
	// Profile is `--profile`.
	Profile string
	// ToolVersion is the Concordia version for `toolchain.concordia`.
	ToolVersion string
	// FailOn is `--fail-on`. Empty uses the workspace `check.failOn`.
	FailOn  diag.Severity
	Offline bool
}

// Deps are the replaceable dependencies. Real runs use the defaults.
type Deps struct {
	// CreateClient makes the CBM client. Default: DefaultClient.
	CreateClient func(wspace *workspace.Workspace, log *logger.Logger) observation.Client
	// CreateGit makes the git port for the workspace root. Default: workspace.NewCliGit.
	CreateGit func(root string) workspace.GitPort
	// Git is a ready git port. It takes precedence over CreateGit.
	Git workspace.GitPort
	// RenderReport renders one report. It is needed when reports are written.
	RenderReport func(format workspace.ReportFormat, run *Run) ([]byte, error)
	// Now gives the current time. Default: clock.Now.
	Now func() time.Time
	// HTTPClient downloads remote CALM resources. nil gives fetch.NewClient().
	HTTPClient *http.Client
	// ViewDiagnostics validates the view definitions against the model and suggests default views. nil: none.
	ViewDiagnostics ViewDiagnosticsFunc
	// EvaluateViews runs the presence, freshness and correspondence checks of the declared views. nil: none.
	EvaluateViews EvaluateViewsFunc
	// EvaluateDecisions runs the ADR governance checks of the decision register. nil: none.
	EvaluateDecisions EvaluateDecisionsFunc
	// Getenv reads the environment for the Structurizr CLI of the freshness check. nil: the process environment.
	Getenv func(string) string
}

// CheckOptions are the options of RunCheck.
type CheckOptions struct {
	// MinimumCoverage is `--minimum-coverage`: 0..1.
	MinimumCoverage *float64
	Deps            Deps
	// Architecture is `--architecture`: replaces `architecture.source`. Relative to the working directory.
	Architecture string
	// UseBaseline is `--use-baseline`: replaces `exceptions.baseline`. The file must exist.
	UseBaseline string
	// Waivers is `--waivers`: replaces `exceptions.waivers`. The file must exist.
	Waivers string
	// Unknown is `--unknown`.
	Unknown policy.UnknownDisposition
	// EvidenceDir is `--evidence-dir`: one `<fingerprint hex>.json` for each finding (written when reports are written).
	EvidenceDir string
	// Changed is `--changed`: the git reference that limits the reporting. "" is off.
	Changed string
	// Projects is `--project`: names of workspace projects. Empty selects all projects.
	Projects []string
	// Policies is `--policy`: replaces `policies.files`. Relative to the working directory.
	Policies []string
	// Rules is `--rule`: keep only these rules.
	Rules []string
	// Environments is `--environment`: ids of declared environments. Empty selects all.
	Environments []string
	// Controls is `--control`: keep only the rules of these controls.
	Controls []string
	Env      Env
	// SkipReports writes no files. `explain`, `render` and `baseline` set it.
	// Otherwise RunCheck writes `report.outputs` (and always the canonical JSON),
	// the evidence files and the run manifest.
	SkipReports bool
	// KeepEvidence puts the merged evidence in Run.Evidence (`export bundle --include-graph`).
	KeepEvidence bool
	// KeepViewInput puts the state that the view checks read in Run.ViewInput (`render --views`).
	KeepViewInput bool
	// Incremental is `--incremental`: the review evaluation reads and writes the impact cache under
	// `.concordia/cache/impact/`. Without it `check` writes nothing there.
	Incremental bool
}

func usage(detail string) *diag.Error { return diag.Fail(diag.WRK010, diag.Detail(detail)) }

// minimumCoverageInRange says whether `--minimum-coverage`, when given, is a number from 0 to 1.
func minimumCoverageInRange(m *float64) bool {
	return m == nil || (*m >= 0 && *m <= 1 && !math.IsNaN(*m))
}

func validateFlags(opts *CheckOptions) error {
	if !minimumCoverageInRange(opts.MinimumCoverage) {
		return usage("--minimum-coverage must be a number from 0 to 1")
	}

	switch opts.Unknown {
	case "", policy.UnknownFail, policy.UnknownWarn, policy.UnknownIgnore:
		return nil
	default:
		return usage(fmt.Sprintf("--unknown must be fail, warn, or ignore (got '%s')", opts.Unknown))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}

func inputRef(root, abs string) (InputRef, error) {
	rel, err := fsutil.ToPosixRelative(root, abs)
	if err != nil {
		return InputRef{}, err //nolint:wrapcheck // already has context.
	}

	d, err := digest.OfFile(abs)
	if err != nil {
		return InputRef{}, fmt.Errorf("digest %s: %w", rel, err)
	}

	return InputRef{Path: rel, Digest: d}, nil
}

func inputRefs(root string, absPaths []string) ([]InputRef, error) {
	refs := make([]InputRef, 0, len(absPaths))

	for _, p := range absPaths {
		ref, err := inputRef(root, p)
		if err != nil {
			return nil, err
		}

		refs = append(refs, ref)
	}

	slices.SortStableFunc(refs, func(a, b InputRef) int { return compareStrings(a.Path, b.Path) })

	return refs, nil
}

// GeneratedPathFilter matches the files that Concordia writes itself: they are not
// source, and they must not change the dirty-tree digest. They are the run outputs and
// the declared outputs, exports and manifests of the generated views (`render --views`).
func GeneratedPathFilter(wspace *workspace.Workspace) func(string) bool {
	projection := path.Clean(fsutil.ToPosix(wspace.Projection.Output))
	if !strings.HasSuffix(projection, "/") {
		projection += "/"
	}

	prefixes := []string{"build/concordia/", ".concordia/", projection}
	exact := map[string]struct{}{".concordia.lock": {}}

	for _, o := range wspace.Report.Outputs {
		exact[path.Clean(fsutil.ToPosix(o.Path))] = struct{}{}
	}

	for i := range wspace.Views {
		for _, p := range wspace.Views[i].GeneratedPaths() {
			exact[path.Clean(fsutil.ToPosix(p))] = struct{}{}
		}
	}

	return func(p string) bool {
		if _, ok := exact[p]; ok {
			return true
		}

		return slices.ContainsFunc(prefixes, func(x string) bool { return strings.HasPrefix(p, x) })
	}
}

// controlUnion collects the plans and controls that each of several `--control` selections kept.
type controlUnion struct {
	keep        map[string]struct{}
	controls    map[string]*policy.ControlPlan
	diagnostics []diag.Diagnostic
	parts       int
}

// add takes in the result of one control selection. The diagnostics are those of the first.
func (u *controlUnion) add(part *policy.ApplyControlsResult) {
	if u.parts == 0 {
		u.diagnostics = part.Diagnostics
	}

	u.parts++

	for i := range part.Plans {
		u.keep[part.Plans[i].RuleID] = struct{}{}
	}

	for i := range part.Controls {
		c := &part.Controls[i]
		u.controls[c.ControlID+"\x00"+c.Pointer] = c
	}
}

// result gives the plans of the given ones that some selection kept, with the merged and sorted controls.
func (u *controlUnion) result(plans []policy.RulePlan) policy.ApplyControlsResult {
	merged := make([]policy.ControlPlan, 0, len(u.controls))
	for _, c := range u.controls {
		merged = append(merged, *c)
	}

	slices.SortStableFunc(merged, func(a, b policy.ControlPlan) int {
		return cmp.Or(compareStrings(a.ControlID, b.ControlID), compareStrings(a.Pointer, b.Pointer))
	})

	kept := make([]policy.RulePlan, 0, len(plans))

	for i := range plans {
		if _, ok := u.keep[plans[i].RuleID]; ok {
			kept = append(kept, plans[i])
		}
	}

	return policy.ApplyControlsResult{Plans: kept, Controls: merged, Diagnostics: u.diagnostics}
}

// selectControls applies `--control` (several allowed): the union of the rules and
// controls of each given control.
func selectControls(model *intent.ArchitectureModel, plans []policy.RulePlan, controlIDs, knownRuleIDs []string) (policy.ApplyControlsResult, error) {
	if len(controlIDs) == 0 {
		return policy.ApplyControls(model, plans, policy.ApplyControlsOptions{KnownRuleIDs: knownRuleIDs})
	}

	union := &controlUnion{keep: map[string]struct{}{}, controls: map[string]*policy.ControlPlan{}}

	for _, id := range controlIDs {
		part, err := policy.ApplyControls(model, plans, policy.ApplyControlsOptions{ControlID: id, KnownRuleIDs: knownRuleIDs})
		if err != nil {
			return policy.ApplyControlsResult{}, err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		union.add(&part)
	}

	return union.result(plans), nil
}

// CanonicalJSON is the canonical JSON text of a run (the `json` report).
func CanonicalJSON(run *Run) ([]byte, error) {
	return canonjson.Marshal(run)
}

// checker holds the state of one RunCheck.
type checker struct {
	opts *CheckOptions
	log  *logger.Logger
	// owned is the CBM client that the run made itself (no Deps.CreateClient). closeClient ends its process.
	owned *observation.ProcessClient
	// viewInput is the state for the view checks, set with CheckOptions.KeepViewInput or when the run evaluates views.
	viewInput *ViewInput
	// views are the results of the view checks and structurizr is the CLI version that verified an export.
	views       []ViewResult
	structurizr string
	// decisions is the result of the ADR governance hook; nil when the run did not evaluate governance.
	decisions *decisionRun
	// review is the result of the review-currency hook; nil when the run did not evaluate assessments.
	review *reviewRun
	// anchor is the trust anchor, read once per run (see reviewAnchor).
	anchor *assessment.Anchor
	// external is the assurance evidence of the run: the results of the rules of the families `effective-access`,
	// `check-result`, and `assurance-claim`, and the verdicts of the checks of the controls (see external.go). nil: the
	// workspace has no `assurance` key, so those rules are `unknown` and every check has no result.
	external *ExternalEvaluation
	// knownCheckIDs are the check identifiers that the result sources of the workspace define (the `check` of the
	// check-result statements that `assurance.verification.results` matches). It is set before the policies are
	// compiled: `require.checkResult` may name these and the `check-ids` of the controls, and no other check (CO-POL-005).
	knownCheckIDs []string
	// assurance is the assurance evidence and report of the run (see assurance.go); nil without an `assurance` key.
	assurance *assuranceRun
	// trust is the boundary-crossing section of the run; nil when no node, relationship, or binding declares trust.
	trust *TrustResult
	now   func() time.Time
	// startedAt is the start of the run: the evaluation time of the assurance evidence and the start time of the run.
	startedAt   time.Time
	diags       []diag.Diagnostic
	indexInputs []InputRef // the index result records that the run read
	t0          time.Time
	// environments is the environment selection of the run.
	environments RunEnvironments
	// enforcement is the enforcement setting of the run (`check.enforcement`, `check.rollback`).
	enforcement EnforcementConfig
	// runCov is the coverage section of the run, set before the rules are evaluated.
	runCov RunCoverage
	// cacheHits and conflicts are the counters that only the stage that sees them knows (see Observability).
	cacheHits, conflicts int
}

// newChecker gives the state of a run, or of a command that reads the inputs of a run.
func newChecker(opts *CheckOptions) *checker {
	chk := &checker{opts: opts, log: opts.Env.Logger, now: opts.Deps.Now, t0: time.Now()}
	if chk.now == nil {
		chk.now = clock.Now
	}

	chk.startedAt = chk.now()

	return chk
}

func (c *checker) phase(event string) {
	if c.log != nil {
		c.log.Trace(logger.TraceEvent{Phase: "check", Event: event, DurationMs: logger.Millis(time.Since(c.t0))})
	}
}

func (c *checker) resolveCwd(p string) string {
	if filepath.IsAbs(p) {
		return p
	}

	return filepath.Join(c.opts.Env.Cwd, filepath.FromSlash(p))
}

// inputs holds the loaded workspace inputs of a run.
type inputs struct {
	traceFiles   []string // workspace-relative runtime trace files
	model        *intent.ArchitectureModel
	baseline     *exceptions.Baseline
	rootsByNode  map[string][]string
	lockPath     string
	baselinePath string
	waiversPath  string
	compiled     policy.CompileResult
	bindings     binding.Loaded
	// lockProfile is the capability profile that the lockfile records. nil when there is no lock or it records none.
	lockProfile    *observation.ProfileRef
	selected       policy.ApplyControlsResult
	patternPaths   []string
	decoratorPaths []string
	waivers        []exceptions.Waiver
	// scoped is the verification state of the scoped waivers of the register. nil when it has none.
	scoped *scopedState
	// policyFiles are the policy files of the run (workspace files and packs), for the policy digest of the review rules.
	policyFiles []policy.PolicyFile
}

// runState is what the stages of RunCheck hand on to each other.
type runState struct {
	wspace   *workspace.Workspace
	frozen   *frozen
	inp      *inputs
	evidence *fetched
	typed    TypedContext
}

// evaluatedRun is the evaluation of a run with the reconcile input that it was made from.
type evaluatedRun struct {
	reconcile ReconcileInput
	out       EvaluateOutput
}

// RunCheck is the check pipeline: freeze, validate inputs, bindings, policies, edges,
// verify the freeze, resolve, evaluate, dispositions, result.
//
// It gives a *diag.Error for input, adapter, toolchain and lock errors (exit codes 2,
// 3, 5, 6). Rule verdicts are not errors: the caller takes Run.Summary.ExitCode.
func RunCheck(ctx context.Context, opts *CheckOptions) (*Run, error) {
	chk := newChecker(opts)
	defer chk.closeClient()

	if err := validateFlags(opts); err != nil {
		return nil, err
	}

	wspace, state, err := chk.freezeRun(ctx)
	if err != nil {
		return nil, err
	}

	gathered, err := chk.gatherRun(ctx, wspace, state)
	if err != nil {
		return nil, err
	}

	evaluated, err := chk.evaluateRun(ctx, gathered)
	if err != nil {
		return nil, err
	}

	chk.trust = deriveTrust(&trustInput{Model: gathered.inp.model, Vocab: trust.FromWorkspace(wspace), Bindings: gathered.inp.bindings.Bindings, Reconcile: &evaluated.reconcile})

	chk.phase("evaluate")

	return chk.completeRun(ctx, gathered, evaluated)
}

// selectRun loads the workspace and selects the projects and the environments of the run.
func (c *checker) selectRun() (*workspace.Workspace, []workspace.Project, error) {
	wspace, err := workspace.Load(c.opts.Env.WorkspacePath, c.opts.Env.Profile)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	selected, err := SelectProjects(wspace, c.opts.Projects)
	if err != nil {
		return nil, nil, err
	}

	envs, err := SelectEnvironments(wspace, c.opts.Environments)
	if err != nil {
		return nil, nil, err
	}

	c.environments = envs

	return wspace, selected, nil
}

// freezeRun selects the scope of the run and freezes the source revision and graph generation.
func (c *checker) freezeRun(ctx context.Context) (*workspace.Workspace, *frozen, error) {
	wspace, selected, err := c.selectRun()
	if err != nil {
		return nil, nil, err
	}

	// An expired rollback stops the run before any work (CO-WRK-018, exit 2).
	c.enforcement, err = NewEnforcementConfig(wspace, c.startedAt)
	if err != nil {
		return nil, nil, err
	}

	state, err := c.freeze(ctx, wspace, selected)
	if err != nil {
		return nil, nil, err
	}

	c.phase("freeze")

	if err := prepareFrozen(ctx, c.opts, wspace, state); err != nil {
		return nil, nil, err
	}

	return wspace, state, nil
}

// gatherRun loads the inputs of the run, stops on contradicting rules, and fetches the evidence.
func (c *checker) gatherRun(ctx context.Context, wspace *workspace.Workspace, state *frozen) (*runState, error) {
	// Workspace inputs, bindings, policies, controls, baseline and waivers.
	inp, err := c.loadInputs(ctx, wspace, state, &policy.ProviderCaps{SupportedCanonicalKinds: state.caps.SupportedCanonicalKinds, Profile: state.caps.Profile, Families: state.caps.Families})
	if err != nil {
		return nil, err
	}

	c.phase("inputs")

	if err := c.checkViewDiagnostics(ctx, wspace, inp.model); err != nil {
		return nil, err
	}

	typed := TypedContext{
		Workspace: wspace, Environments: &c.environments, Bindings: &inp.bindings, Families: state.caps.Families,
		Regressions: capabilityRegressions(inp.lockProfile, &state.caps), Precedences: precedencesOf(inp.compiled.Packs),
	}

	// Rules that contradict each other stop the run before any evaluation (CO-POL-017, exit 2).
	if _, err := CheckPolicyConflicts(inp.selected.Plans, inp.model, &typed); err != nil {
		return nil, err
	}

	evidence, err := c.fetchEvidence(ctx, wspace, state, inp)
	if err != nil {
		return nil, err
	}

	conflicts := identityConflicts(wspace, evidence)
	c.conflicts = len(conflicts) + len(evidence.bindingSet.Ambiguous)
	c.diags = append(c.diags, conflictDiagnostics(conflicts)...)
	typed.Conflicts = conflicts

	return &runState{wspace: wspace, frozen: state, inp: inp, evidence: evidence, typed: typed}, nil
}

// runFiles are the files that the coverage of a run is judged over.
type runFiles struct {
	workspaceFiles   []string
	sizes            map[string]int64
	include, exclude []string
}

// filesOfRun gives the git files of the selected projects and the coverage globs.
func filesOfRun(ctx context.Context, state *frozen) (*runFiles, error) {
	workspaceFiles, sizes, err := workspaceFilesOf(ctx, state)
	if err != nil {
		return nil, err
	}

	include, exclude := coverageGlobs(state)

	return &runFiles{workspaceFiles: workspaceFiles, sizes: sizes, include: include, exclude: exclude}, nil
}

// evaluateRun evaluates the rules, then the views, the decisions, the review and the quality scenarios.
func (c *checker) evaluateRun(ctx context.Context, state *runState) (*evaluatedRun, error) {
	files, err := filesOfRun(ctx, state.frozen)
	if err != nil {
		return nil, err
	}

	// ADR governance needs the compiled policies, the evidence and the file coverage; an invalid register stops the
	// run here, before any area is produced.
	if err := c.runDecisions(ctx, state.wspace, state.inp, state.frozen, &decisionFiles{
		evidence: state.evidence, workspaceFiles: files.workspaceFiles, sizes: files.sizes, include: files.include, exclude: files.exclude,
	}); err != nil {
		return nil, err
	}

	// Predicate coverage, index results, the manifest cross-check, failed scopes and limits.
	scope, runCov := c.buildScope(&coverageInput{wspace: state.wspace, state: state.frozen, evidence: state.evidence, bindings: &state.inp.bindings, workspaceFiles: files.workspaceFiles})

	if err := c.validateGuarantees(state, &scope); err != nil {
		return nil, err
	}

	reconcile := state.reconcileInput(files, effectiveMinimumCoverage(state.wspace, c.opts))
	evaluated := &evaluatedRun{reconcile: reconcile}

	evaluated.out = Evaluate(&EvaluateInput{
		Plans:          state.inp.selected.Plans,
		Snapshot:       Snapshot{Project: state.frozen.lead().ref.Project, GraphGeneration: state.frozen.generation},
		Scope:          scope,
		Typed:          state.typed,
		Profile:        state.frozen.caps.Profile,
		Enforcement:    c.enforcement,
		ReconcileInput: reconcile,
		External:       c.external,
	})

	c.finishCoverage(&runCov, &evaluated.out)
	c.runCov = runCov

	if err := c.evaluateFollowUps(ctx, state, evaluated); err != nil {
		return nil, err
	}

	return evaluated, nil
}

// validateGuarantees checks that a mandatory rule has its guarantees; if one is absent the run gives no gate result
// (CO-POL-018, exit 2).
func (c *checker) validateGuarantees(rs *runState, scope *ScopeContext) error {
	return c.enforcement.ValidateEnforcement(rs.inp.selected.Plans, &EnforcementGuarantees{
		Environments: c.environments, Declared: declaredEnvironments(rs.wspace), Families: rs.frozen.caps.Families, Predicates: scope.Predicates,
		Authorities: authorityCount(rs.wspace), TrustAnchor: rs.wspace.Assessments != nil && rs.wspace.Assessments.TrustAnchor != nil,
	})
}

// reconcileInput is the input of the reconciliation of the run.
func (rs *runState) reconcileInput(files *runFiles, minimumCoverage float64) ReconcileInput {
	return ReconcileInput{
		Model:      rs.inp.model,
		BindingSet: rs.evidence.bindingSet,
		Evidence:   EvidenceSet{Nodes: nodesWithHostingTargets(&rs.evidence.result), Edges: rs.evidence.result.Edges},
		CoverageInputs: CoverageInputs{
			WorkspaceFiles: files.workspaceFiles, Include: files.include, Exclude: files.exclude,
			CbmFiles: rs.evidence.cbmFiles, DirtyFiles: dirtyFilesOf(rs.frozen), MinimumCoverage: minimumCoverage,
			Sizes: files.sizes,
		},
		BindingRootsByNode:      rs.inp.rootsByNode,
		SupportedCanonicalKinds: rs.frozen.caps.SupportedCanonicalKinds,
	}
}

// followUpInput is the input that the checks after the first evaluation get.
func (rs *runState) followUpInput(evaluated *evaluatedRun) *EvaluateInput {
	return &EvaluateInput{Profile: rs.frozen.caps.Profile, ReconcileInput: evaluated.reconcile, Typed: rs.typed}
}

// evaluateFollowUps evaluates the views, the decisions, the review and the quality scenarios over the evaluation.
func (c *checker) evaluateFollowUps(ctx context.Context, state *runState, evaluated *evaluatedRun) error {
	if err := c.prepareViews(ctx, state.wspace, state.inp, state.frozen, state.evidence, &evaluated.out, &evaluated.reconcile); err != nil {
		return err
	}

	if err := c.evaluateViews(ctx, state.wspace, &evaluated.out, state.followUpInput(evaluated)); err != nil {
		return err
	}

	c.mergeDecisions(state.inp, &evaluated.out, state.followUpInput(evaluated))

	c.mergeReview(state.inp, &evaluated.out, state.followUpInput(evaluated))

	// Quality scenarios read the rule verdicts and the review currency of this run, so they come after both (D21). They
	// get the run's enforcement, so `check.enforcement` and an active `check.rollback` apply to them as to other rules.
	quality := state.followUpInput(evaluated)
	quality.Enforcement = c.enforcement

	return c.evaluateQuality(state.inp, &evaluated.out, quality)
}

// completeRun assembles the run from its evaluation, logs it, and writes its files.
func (c *checker) completeRun(ctx context.Context, rs *runState, evaluated *evaluatedRun) (*Run, error) {
	run, err := c.buildRun(rs.wspace, rs.inp, rs.frozen, &rs.evidence.result, &rs.evidence.bindingSet, &evaluated.out, c.startedAt)
	if err != nil {
		return nil, err
	}

	c.logFresh(run, rs.frozen.conn.Diagnostics)

	run.ViewInput = c.viewInput

	if err := c.finishRun(ctx, rs.wspace, run, &rs.evidence.result); err != nil {
		return nil, err
	}

	c.phase("done")

	return run, nil
}

// prepareFrozen applies the changed-files freeze and adds the runtime-trace edge kind to the capabilities.
func prepareFrozen(ctx context.Context, opts *CheckOptions, wspace *workspace.Workspace, state *frozen) error {
	if opts.Changed != "" {
		if err := freezeChanged(ctx, state, opts.Changed); err != nil {
			return err
		}
	}

	if wspace.Observation.Runtime != nil && !slices.Contains(state.caps.SupportedCanonicalKinds, observation.KindCallsService) {
		kinds := append(slices.Clone(state.caps.SupportedCanonicalKinds), observation.KindCallsService)
		slices.Sort(kinds)
		state.caps.SupportedCanonicalKinds = kinds
	}

	return nil
}

// effectiveMinimumCoverage is `--minimum-coverage` or else the workspace `check.minimumCoverage`.
func effectiveMinimumCoverage(wspace *workspace.Workspace, opts *CheckOptions) float64 {
	if opts.MinimumCoverage != nil {
		return *opts.MinimumCoverage
	}

	return wspace.Check.MinimumCoverage
}

// finishRun keeps the evidence on request and writes the report files unless skipped.
func (c *checker) finishRun(ctx context.Context, wspace *workspace.Workspace, run *Run, evidence *observation.EvidenceQueryResult) error {
	if c.opts.KeepEvidence {
		run.Evidence = evidence
	}

	if !c.opts.SkipReports {
		return writeRunFiles(ctx, c.opts, wspace, run)
	}

	return nil
}

// logFresh logs the diagnostics of the run. The version diagnostics were already written when the client was made.
func (c *checker) logFresh(run *Run, already []diag.Diagnostic) {
	if c.log == nil {
		return
	}

	var fresh []diag.Diagnostic

	for _, d := range run.Diagnostics {
		if !slices.ContainsFunc(already, func(o diag.Diagnostic) bool { return reflect.DeepEqual(o, d) }) {
			fresh = append(fresh, d)
		}
	}

	c.log.Diagnostics(fresh)
}
