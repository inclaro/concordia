package conformance

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// disposed holds the result of the exception registers.
type disposed struct {
	orphaned []string
	resolved []string
	applied  []AppliedWaiver
	// scoped are the records of the scoped waivers. nil when the register has none.
	scoped []ScopedWaiver
}

// applyDispositions sets the disposition of each finding. Only rules that gave a real
// answer (pass or violation) can resolve a baseline entry or orphan a waiver.
func (c *checker) applyDispositions(wspace *workspace.Workspace, inp *inputs, evaluated *EvaluateOutput, when time.Time) (*disposed, error) {
	ran := ranRuleIDs(evaluated.RuleResults)
	findings := evaluated.Findings
	result := exceptions.ApplyDispositions(findingKeys(findings), inp.baseline, inp.scoped.applicable(inp.waivers), when, ran)

	for i := range findings {
		findings[i].Disposition = result.Assignments[i].Disposition

		if ref := result.Assignments[i].Ref; ref != "" {
			findings[i].DispositionRef = &ref
		}
	}

	waiverByID := make(map[string]*exceptions.Waiver, len(inp.waivers))
	for i := range inp.waivers {
		waiverByID[inp.waivers[i].ID] = &inp.waivers[i]
	}

	orphaned := orphanedWaiverIDs(result.OrphanedWaivers, waiverByID, ran)

	if err := c.noteExceptionDiagnostics(wspace, inp, &result, orphaned, waiverByID); err != nil {
		return nil, err
	}

	return &disposed{
		orphaned: orphaned, resolved: nonNilStrings(result.ResolvedBaselineEntries), applied: appliedWaivers(findings, waiverByID, when),
		scoped: inp.scoped.records(inp.waivers, findings, when),
	}, nil
}

// ranRuleIDs gives the IDs of the rules that gave a real answer.
func ranRuleIDs(results []RuleResult) []string {
	ran := []string{}

	for i := range results {
		if v := results[i].Verdict; v == VerdictPass || v == VerdictViolation {
			ran = append(ran, results[i].RuleID)
		}
	}

	return ran
}

func findingKeys(findings []Finding) []exceptions.Key {
	keys := make([]exceptions.Key, len(findings))

	for i := range findings {
		keys[i] = exceptions.Key{
			Fingerprint: findings[i].Fingerprint, RuleID: findings[i].RuleID,
			Subject: findings[i].Subject, Environment: findings[i].Environment, ObligationID: findings[i].ObligationID,
		}
	}

	return keys
}

// orphanedWaiverIDs keeps the orphaned waivers of the rules that ran.
func orphanedWaiverIDs(ids []string, waiverByID map[string]*exceptions.Waiver, ran []string) []string {
	orphaned := []string{}

	for _, id := range ids {
		if w := waiverByID[id]; w != nil && slices.Contains(ran, w.Rule) {
			orphaned = append(orphaned, id)
		}
	}

	return orphaned
}

func (c *checker) noteExceptionDiagnostics(
	wspace *workspace.Workspace, inp *inputs, result *exceptions.Result, orphaned []string, waiverByID map[string]*exceptions.Waiver,
) error {
	waiversRel, err := waiversRelPath(wspace.Root, inp.waiversPath)
	if err != nil {
		return err
	}

	for _, d := range result.Diagnostics {
		if d.Code != diag.EXC003 && d.Code != diag.EXC010 {
			c.diags = append(c.diags, d)
		}
	}

	c.diags = append(c.diags, orphanDiagnostics(orphaned, waiverByID, waiversRel)...)

	return nil
}

// waiversRelPath gives the workspace-relative path of the waiver file, or "" when there is none.
func waiversRelPath(root, waiversPath string) (string, error) {
	if waiversPath == "" {
		return "", nil
	}

	rel, err := fsutil.ToPosixRelative(root, waiversPath)
	if err != nil {
		return "", err //nolint:wrapcheck // already has context.
	}

	return rel, nil
}

// orphanDiagnostics gives one diagnostic per orphaned waiver, sourced at the waiver file when there is one.
func orphanDiagnostics(orphaned []string, waiverByID map[string]*exceptions.Waiver, waiversRel string) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0, len(orphaned))

	for _, id := range orphaned {
		d := exceptions.OrphanDiagnostic(waiverByID[id])
		if waiversRel != "" {
			d.Source = &diag.Source{URI: waiversRel}
		}

		out = append(out, d)
	}

	return out
}

// buildRun applies the dispositions and assembles the result.
func (c *checker) buildRun(
	wspace *workspace.Workspace, inp *inputs, state *frozen, result *observation.EvidenceQueryResult, bindingSet *binding.Set, evaluated *EvaluateOutput, startedAt time.Time,
) (*Run, error) {
	opts := c.opts

	exc, err := c.applyDispositions(wspace, inp, evaluated, startedAt)
	if err != nil {
		return nil, err
	}

	ruleResults, findings := c.reduceToChangedFiles(state, evaluated.RuleResults, evaluated.Findings)
	failOn := c.effectiveFailOn(wspace)
	controls, summary := c.controlsAndSummary(inp.selected.Controls, ruleResults, findings, failOn, c.effectiveUnknown(wspace))

	runInputs, err := c.runInputs(wspace, inp, result.QueryPlanDigest)
	if err != nil {
		return nil, err
	}

	if c.decisions != nil {
		runInputs.Decisions = c.decisions.evaluation.Inputs
	}

	c.reviewInputs(wspace, &runInputs)

	lead := state.lead()

	run := &Run{
		SchemaVersion: ResultSchemaVersion,
		Run: RunInfo{
			ID:              opts.Env.RunID,
			StartedAt:       clock.ISO(startedAt),
			FinishedAt:      clock.ISO(c.now()),
			SourceRevision:  optionalRevision(lead.revision),
			DirtyTreeDigest: lead.dirtyDigest,
			GraphGeneration: state.generation,
			Project:         lead.ref.Project,
			Projects:        runProjectsOf(state),
			Changed:         state.changed,
			Host:            RunHost{Platform: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version()},
		},
		Toolchain: RunToolchain{
			Concordia:  opts.Env.ToolVersion,
			CalmSchema: inp.model.SchemaVersion,
			Cbm: CbmToolchain{
				ExecutableVersion: state.caps.ExecutableVersion, SHA256: state.conn.SHA256,
				AdapterSchema: state.caps.AdapterSchema, Profile: NewCbmProfile(state.caps.Profile),
			},
		},
		Inputs:         runInputs,
		Summary:        summary,
		Rules:          ruleResults,
		Findings:       findings,
		Reconciliation: evaluated.Reconciliation,
		Controls:       controls,
		Exceptions:     exc.runExceptions(),
		Diagnostics:    nonNilDiagnostics(diag.Sort(c.diags)),
		Coverage:       c.runCov,
		Observability:  observabilityOf(result, &c.runCov, c.conflicts+occurrenceConflicts(evaluated.RuleResults), c.cacheHits),
	}

	run.DeploymentTrace = *deployment.BuildTrace(c.traceInput(wspace, inp, result, bindingSet, evaluated.RuleResults))
	run.Areas.DeploymentTrace = areaOfTrace(&run.DeploymentTrace)

	c.attachRunExtras(run, wspace, findings)

	sum, err := ResultDigest(run)
	if err != nil {
		return nil, err
	}

	run.Summary.ResultDigest = sum

	return run, nil
}

// reduceToChangedFiles keeps the rules and findings of the changed files when the run is a `--changed` run, and
// notes the rules of full scope and the empty change set. Without a change set it returns its input.
func (c *checker) reduceToChangedFiles(state *frozen, ruleResults []RuleResult, findings []Finding) ([]RuleResult, []Finding) {
	if state.changed == nil {
		return ruleResults, findings
	}

	set := make(map[string]struct{}, len(state.changed.Files))
	for _, f := range state.changed.Files {
		set[f] = struct{}{}
	}

	ruleResults, findings = reduceToChanged(ruleResults, findings, set)

	if full := fullScopeRules(ruleResults); len(full) > 0 {
		c.diags = append(c.diags, diag.New(diag.CON027, diag.Params{"rules": strings.Join(full, ", ")}))
	}

	if len(set) == 0 {
		c.diags = append(c.diags, diag.New(diag.CON029, nil))
	}

	return ruleResults, findings
}

// attachRunExtras adds the views, the decisions, the trust, the assurance and the review to the run and settles
// its areas.
func (c *checker) attachRunExtras(run *Run, wspace *workspace.Workspace, findings []Finding) {
	if len(c.views) > 0 {
		run.Views = c.views
		run.Toolchain.Structurizr = c.structurizr
		run.Areas.DiagramCurrency = AreaOfViews(c.views, findings)
	}

	if c.decisions != nil {
		run.DecisionSummary = c.decisions.evaluation.Summary
		run.DecisionRedactions = c.decisions.evaluation.Redactions
	}

	run.Trust = c.trust

	c.applyAssurance(run)

	c.applyReview(run, wspace)

	run.SettleAreas()
}

// optionalRevision gives a pointer to the revision, or nil when there is none.
func optionalRevision(revision string) *string {
	if revision == "" {
		return nil
	}

	return &revision
}

// runProjectsOf gives the project records of a multi-project run, or nil for a single project.
func runProjectsOf(state *frozen) []RunProject {
	if !state.multi() {
		return nil
	}

	projects := make([]RunProject, len(state.projects))

	for i := range state.projects {
		proj := &state.projects[i]
		projects[i] = RunProject{
			Name: proj.project.Name, Root: proj.project.Root, SourceRevision: optionalRevision(proj.revision),
			DirtyTreeDigest: proj.dirtyDigest, GraphGeneration: proj.ref.GraphGeneration,
			ChangedBase: proj.changedBase,
		}
	}

	return projects
}

// summarize counts the rules and findings of the run and records the active rollback, if any.
func (c *checker) summarize(rules []RuleResult, findings []Finding, failOn diag.Severity, unknownPolicy policy.UnknownDisposition) RunSummary {
	summary := Summarize(rules, findings, failOn, unknownPolicy)
	summary.Rollback = c.enforcement.Rollback

	return summary
}

func nonNilDiagnostics(list []diag.Diagnostic) []diag.Diagnostic {
	if list == nil {
		return []diag.Diagnostic{}
	}

	return list
}

func appliedWaivers(findings []Finding, waiverByID map[string]*exceptions.Waiver, when time.Time) []AppliedWaiver {
	ids := usedWaiverIDs(findings, waiverByID)
	out := make([]AppliedWaiver, 0, len(ids))

	for _, id := range ids {
		w := waiverByID[id]
		state := waiverStateExpired

		if exceptions.IsWaiverActive(w, when) {
			state = waiverStateActive
		}

		out = append(out, AppliedWaiver{
			ID: w.ID, Rule: w.Rule, Finding: w.Finding, Owner: w.Owner, Reason: w.Reason,
			Issue: w.Issue, Expires: w.Expires, State: state,
		})
	}

	return out
}

// usedWaiverIDs gives the sorted IDs of the known waivers that the findings reference.
func usedWaiverIDs(findings []Finding, waiverByID map[string]*exceptions.Waiver) []string {
	used := map[string]struct{}{}

	for i := range findings {
		ref := findings[i].DispositionRef
		if ref == nil || waiverByID[*ref] == nil {
			continue
		}

		used[*ref] = struct{}{}
	}

	ids := make([]string, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, compareStrings)

	return ids
}

func (c *checker) runInputs(wspace *workspace.Workspace, inp *inputs, queryPlanDigest string) (RunInputs, error) {
	out := RunInputs{
		QueryPlanDigest: queryPlanDigest,
		Environments:    c.environments,
		IndexResults:    slices.Clone(c.indexInputs),
		Workspace:       InputRef{Path: relOrBase(wspace.Root, wspace.Path), Digest: wspace.Digest},
		Architecture:    InputRef{Path: inp.model.SourceURI, Digest: inp.model.Digest},
		Policies:        policyInputRefs(inp),
	}

	if out.IndexResults == nil {
		out.IndexResults = []InputRef{}
	}

	err := runFileInputs(wspace.Root, inp, &out)

	return out, err
}

// runFileInputs resolves the inputs that are files of the workspace: the lock, the pattern, decorator,
// binding and trace files, the baseline and the waiver file.
func runFileInputs(root string, inp *inputs, out *RunInputs) error {
	var err error

	if out.Patterns, err = inputRefs(root, inp.patternPaths); err != nil {
		return err
	}

	if out.Decorators, err = inputRefs(root, inp.decoratorPaths); err != nil {
		return err
	}

	if out.Bindings, err = inputRefs(root, runWorkspacePaths(root, inp.bindings.Files)); err != nil {
		return err
	}

	if out.Traces, err = inputRefs(root, runWorkspacePaths(root, inp.traceFiles)); err != nil {
		return err
	}

	return runSingleFileInputs(root, inp, out)
}

// runSingleFileInputs resolves the lock, the baseline and the waiver file, each of which is absent when its path
// is empty (or, for the lock, when the file does not exist).
func runSingleFileInputs(root string, inp *inputs, out *RunInputs) error {
	if fileExists(inp.lockPath) {
		if err := setRunInputRef(&out.Lock, root, inp.lockPath); err != nil {
			return err
		}
	}

	if err := setRunInputRef(&out.Baseline, root, inp.baselinePath); err != nil {
		return err
	}

	return setRunInputRef(&out.Waivers, root, inp.waiversPath)
}

// policyInputRefs gives the policy packs as input references, sorted by path.
func policyInputRefs(inp *inputs) []InputRef {
	refs := make([]InputRef, 0, len(inp.compiled.Packs))
	for _, p := range inp.compiled.Packs {
		refs = append(refs, InputRef{Path: p.Path, Digest: p.Digest})
	}

	slices.SortStableFunc(refs, func(a, b InputRef) int { return compareStrings(a.Path, b.Path) })

	return refs
}

// runWorkspacePaths turns slash-separated workspace paths into absolute paths.
func runWorkspacePaths(root string, rels []string) []string {
	paths := make([]string, 0, len(rels))
	for _, f := range rels {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(f)))
	}

	return paths
}

// setRunInputRef sets dst to the reference of the file. An empty path leaves dst nil.
func setRunInputRef(dst **InputRef, root, abs string) error {
	if abs == "" {
		return nil
	}

	ref, err := inputRef(root, abs)
	if err != nil {
		return err
	}

	*dst = &ref

	return nil
}

func relOrBase(root, p string) string {
	rel, err := fsutil.ToPosixRelative(root, p)
	if err != nil {
		return filepath.Base(p)
	}

	return rel
}

// workspaceFilesOf gives the git files of the selected projects (workspace paths, with the
// project prefix) and their sizes.
func workspaceFilesOf(ctx context.Context, state *frozen) (files []string, sizes map[string]int64, err error) {
	sizes = map[string]int64{}

	for i := range state.projects {
		proj := &state.projects[i]

		raw, listErr := proj.git.ListFiles(ctx)
		if listErr != nil {
			return nil, nil, fmt.Errorf("git ls-files: %w", listErr)
		}

		for f, size := range observation.FileSizes(proj.root, raw) {
			sizes[proj.prefix+f] = size
		}

		files = append(files, proj.prefixed(raw)...)
	}

	return files, sizes, nil
}

// dirtyFilesOf gives the dirty files of the selected projects, with the project prefix.
func dirtyFilesOf(state *frozen) []string {
	var out []string

	for i := range state.projects {
		out = append(out, state.projects[i].dirty...)
	}

	return out
}

// coverageGlobs gives the union of the include and exclude globs of the selected projects,
// each with its project prefix. A project without include globs covers all its files when
// another project has include globs.
func coverageGlobs(state *frozen) (include, exclude []string) {
	anyInclude := slices.ContainsFunc(state.projects, func(p frozenProject) bool { return len(p.project.Include) > 0 })

	for i := range state.projects {
		proj := &state.projects[i]

		switch {
		case len(proj.project.Include) > 0:
			include = append(include, proj.prefixed(proj.project.Include)...)
		case anyInclude:
			include = append(include, proj.prefix+"**")
		}

		exclude = append(exclude, proj.prefixed(proj.project.Exclude)...)
	}

	return include, exclude
}

// prepareViews evaluates the assessment currency and keeps the state that the view checks read. The currency is
// evaluated once per run, over the full evidence: `--changed` reduces the findings of edge rules only and never the
// currency (A44). The views read the evaluation.
func (c *checker) prepareViews(ctx context.Context, wspace *workspace.Workspace, inp *inputs, state *frozen, evidence *fetched, evaluated *EvaluateOutput, reconcile *ReconcileInput) error {
	if err := c.runReview(ctx, wspace, inp, state, evidence); err != nil {
		return err
	}

	if c.needsViewInput(wspace) {
		c.viewInput = &ViewInput{
			Workspace: wspace, Reconcile: reconcile, Evidence: &evidence.result, Bindings: inp.bindings.Bindings,
			Policies: inp.selected.Plans, Coverage: c.runCov.Predicates, Reconciliation: evaluated.Reconciliation,
		}
	}

	return nil
}
