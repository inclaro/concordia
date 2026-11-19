package conformance

import (
	"cmp"
	"context"
	"slices"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// decisionRun is what the ADR governance hook gave: it is merged into the evaluation after Evaluate, and the
// summary and the inputs go to the run.
type decisionRun struct {
	evaluation *DecisionEvaluation
}

// governanceActive says whether the run evaluates ADR governance: the workspace has a decision register
// (`adr.files`), or a selected rule is a decision-governance rule (the `adr-governance` pack with no register
// gives `not-applicable` rules). A run with neither never calls the hook, so its result is the one of before.
func governanceActive(wspace *workspace.Workspace, plans []policy.RulePlan) bool {
	if wspace.ADR != nil && len(wspace.ADR.Files) > 0 {
		return true
	}

	return slices.ContainsFunc(plans, func(plan policy.RulePlan) bool {
		return plan.Predicate.Family == policy.FamilyDecisionGovernance
	})
}

// decisionEvidenceKinds gives the canonical edge kinds that ADR governance reads: the supersession edges, the
// mentions, and the file references. A fetch that asks for `supersedes` also returns every ADR node of the graph
// (see the observation adapter). Without active governance the result is empty.
func decisionEvidenceKinds(wspace *workspace.Workspace, plans []policy.RulePlan) []observation.CanonicalEdgeKind {
	if !governanceActive(wspace, plans) {
		return nil
	}

	return []observation.CanonicalEdgeKind{observation.KindSupersedes, observation.KindMentions, observation.KindReferencesFile}
}

// effectiveFailOn gives `check.failOn` of the run: the flag or environment, else the workspace.
func (c *checker) effectiveFailOn(wspace *workspace.Workspace) diag.Severity {
	if c.opts.Env.FailOn != "" {
		return c.opts.Env.FailOn
	}

	return wspace.Check.FailOn
}

// effectiveUnknown gives `check.unknown` of the run after `--unknown`.
func (c *checker) effectiveUnknown(wspace *workspace.Workspace) policy.UnknownDisposition {
	if c.opts.Unknown != "" {
		return c.opts.Unknown
	}

	return policy.UnknownDisposition(wspace.Check.Unknown)
}

// decisionFiles are the file inputs of the coverage of the decision hook.
type decisionFiles struct {
	evidence       *fetched
	sizes          map[string]int64
	workspaceFiles []string
	include        []string
	exclude        []string
}

// decisionCoverage gives the file coverage of each selected project, parallel to the projects: the stale files
// and the extraction completeness that ADR governance reads.
func (c *checker) decisionCoverage(wspace *workspace.Workspace, state *frozen, files *decisionFiles) []observation.FileCoverage {
	out := make([]observation.FileCoverage, len(state.projects))
	dirty := dirtyFilesOf(state)

	for i := range state.projects {
		name := state.projects[i].project.Name
		own := func(p string) bool { return projectOfPath(wspace, p) == name }

		out[i] = observation.ComputeFileCoverage(&observation.FileCoverageInput{
			WorkspaceFiles: slices.DeleteFunc(slices.Clone(files.workspaceFiles), func(p string) bool { return !own(p) }),
			Include:        files.include, Exclude: files.exclude,
			CbmFiles:   slices.DeleteFunc(slices.Clone(files.evidence.cbmFiles), func(p string) bool { return !own(p) }),
			DirtyFiles: dirty, Sizes: files.sizes,
		})
	}

	return out
}

// runDecisions runs Deps.EvaluateDecisions when the hook is set and the run evaluates ADR governance. It runs
// after the policy compilation and the evidence fetch, which it needs, and before any rule is evaluated: an
// invalid register, link, lifecycle profile or trigger is a *diag.Error (exit 2) that stops the run before any
// area is produced. With no hook, or with no register and no governance rule, it does nothing.
func (c *checker) runDecisions(ctx context.Context, wspace *workspace.Workspace, inp *inputs, state *frozen, files *decisionFiles) error {
	hook := c.opts.Deps.EvaluateDecisions
	if hook == nil || !governanceActive(wspace, inp.selected.Plans) {
		return nil
	}

	projects := make([]DecisionProject, len(state.projects))

	for i := range state.projects {
		proj := &state.projects[i]
		projects[i] = DecisionProject{
			Git: proj.git, Name: proj.project.Name, Prefix: proj.prefix, Root: proj.root,
			Revision: proj.revision, ChangedBase: proj.changedBase,
		}
	}

	out, err := hook(ctx, &DecisionEvalInput{
		Workspace: wspace, Model: inp.model, Evidence: &files.evidence.result, Capabilities: state.caps,
		Policies: &inp.compiled, Projects: projects, Changed: state.changed,
		Coverage: c.decisionCoverage(wspace, state, files),
		FailOn:   c.effectiveFailOn(wspace), OnUnknown: c.effectiveUnknown(wspace),
	})
	if err != nil {
		return err
	}

	if out != nil {
		c.decisions = &decisionRun{evaluation: out}
	}

	return nil
}

// mergeDecisions appends the rule results and findings of the governance hook to the output of the evaluation, so
// that dispositions, `check.failOn`, `check.unknown` and the exit code treat them as normal findings. The
// diagnostics join the run.
func (c *checker) mergeDecisions(inp *inputs, evaluated *EvaluateOutput, input *EvaluateInput) {
	if c.decisions == nil {
		return
	}

	out := c.decisions.evaluation

	for i := range out.Rules {
		rule := &out.Rules[i]

		if rule.Family == "" {
			rule.Family = policy.FamilyDecisionGovernance
		}

		if rule.Plan.RuleID == "" {
			if at := slices.IndexFunc(inp.selected.Plans, func(p policy.RulePlan) bool { return p.RuleID == rule.RuleID }); at >= 0 {
				rule.Plan = inp.selected.Plans[at]
			}
		}

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

	c.diags = append(c.diags, out.Diagnostics...)
}
