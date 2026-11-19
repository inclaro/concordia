package conformance

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// The review-currency hook (design D1, D8, D12). The family `assessment-currency` is not a predicate over edges:
// `check` builds the fact source once, calls assessment.Evaluate once, and makes the results of the four rules of the
// `review-currency` pack from the Evaluation. It is not a Deps hook: this package imports `internal/assessment`
// directly, and the evaluation is the one that `assessment status` and the views share.

// reviewRun is what the review hook gave: the evaluation, the governance changes, and the rule results and
// findings. mergeReview adds the rules and findings to the output of Evaluate; buildRun adds the rest to the run.
type reviewRun struct {
	eval   *assessment.Evaluation
	set    *assessment.Set
	anchor *assessment.Anchor
	// disposed are the governance changes that a verified record of a governance authority covers.
	disposed   map[string]bool
	governance []impact.SettingChange
	// governanceFailure says why the anchor workspace could not be compared after the anchor was read; "" when it was.
	governanceFailure string
	rules             []RuleResult
	findings          []Finding
	// dispositions are the entries of the `governance.dispositions` list.
	dispositions []DispositionEntry
	// anchorConfigured says that the workspace sets `assessments.trustAnchor`.
	anchorConfigured bool
}

// reviewConfigured says whether the run evaluates assessments: the workspace has an `assessments` key, or a
// selected rule is a review rule (the `review-currency` pack with no `assessments` key gives `not-applicable` rules).
func reviewConfigured(wspace *workspace.Workspace, plans []policy.RulePlan) bool {
	if wspace.Assessments != nil {
		return true
	}

	for i := range plans {
		if isReview(&plans[i]) {
			return true
		}
	}

	return false
}

// reviewPlans gives the selected review rules, sorted by rule ID.
func reviewPlans(plans []policy.RulePlan) []policy.RulePlan {
	var out []policy.RulePlan

	for i := range plans {
		if isReview(&plans[i]) {
			out = append(out, plans[i])
		}
	}

	slices.SortStableFunc(out, func(a, b policy.RulePlan) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) })

	return out
}

// reviewAnchor reads the trust anchor once per run. A workspace with no `assessments.trustAnchor` has an
// unreadable anchor (`no trust anchor is configured`).
func (c *checker) reviewAnchor(ctx context.Context, wspace *workspace.Workspace) *assessment.Anchor {
	if c.anchor != nil {
		return c.anchor
	}

	ref := ""
	if wspace.Assessments != nil && wspace.Assessments.TrustAnchor != nil {
		ref = wspace.Assessments.TrustAnchor.Ref
	}

	c.anchor = assessment.ReadAnchor(ctx, &assessment.AnchorSource{
		Git: c.newGit(wspace.Root), Ref: ref, WorkspacePath: c.opts.Env.WorkspacePath, Profile: c.opts.Env.Profile,
	})

	return c.anchor
}

// runReview loads the assessments, builds the fact source once, and evaluates the currency. It runs after the
// evidence, the bindings, and the coverage exist, and before the views, which read the evaluation. An invalid
// assessment document is a *diag.Error (exit 2) that stops the run before any area is produced.
func (c *checker) runReview(ctx context.Context, wspace *workspace.Workspace, inp *inputs, state *frozen, evidence *fetched) error {
	if !reviewConfigured(wspace, inp.selected.Plans) {
		return nil
	}

	run := &reviewRun{disposed: map[string]bool{}}
	c.review = run

	cfg := wspace.Assessments
	if cfg == nil {
		// The pack is enabled and no `assessments` key exists: nothing is required, the rules are not applicable.
		return nil
	}

	set, err := assessment.Load(wspace.Root, cfg.Paths)
	if err != nil {
		return err
	}

	run.set = set
	run.anchor = c.reviewAnchor(ctx, wspace)
	run.anchorConfigured = cfg.TrustAnchor != nil

	if run.anchorConfigured && run.anchor.Diagnostic != nil {
		c.diags = append(c.diags, *run.anchor.Diagnostic)
	}

	eval, err := assessment.Evaluate(ctx, c.reviewEvalInput(wspace, inp, run, c.reviewFacts(wspace, inp, state, evidence)))
	if err != nil {
		return fmt.Errorf("evaluate assessments: %w", err)
	}

	run.eval = eval
	c.diags = append(c.diags, eval.Diagnostics...)

	run.governance, run.governanceFailure = c.governanceChanges(ctx, wspace, run.anchor)
	run.markGovernanceDispositions(set, eval)

	return nil
}

// reviewFacts builds the fact source of the evaluation from the evidence, the bindings, and the coverage of the run.
func (c *checker) reviewFacts(wspace *workspace.Workspace, inp *inputs, state *frozen, evidence *fetched) *ReviewFacts {
	return NewReviewFacts(&ReviewFactsInput{
		Evidence: &evidence.result, BindingSet: &evidence.bindingSet, Profile: profileOf(&state.caps), Model: inp.model,
		Git: c.newGit(wspace.Root), Root: wspace.Root, ProviderVersion: state.caps.ExecutableVersion, AdapterSchema: state.caps.AdapterSchema,
		Coverage: c.runCov.Predicates, Bindings: inp.bindings.Bindings, Environments: environmentsOfWorkspace(wspace), Assurance: c.reviewAssurance(),
	})
}

// reviewEvalInput makes the input of the evaluation. The required assessments come from the trust anchor when one is
// configured and readable, else from the working tree.
func (c *checker) reviewEvalInput(wspace *workspace.Workspace, inp *inputs, run *reviewRun, facts *ReviewFacts) *assessment.EvalInput {
	cfg := wspace.Assessments

	required := cfg.Required
	if run.anchor.Readable() && run.anchorConfigured {
		required = run.anchor.Required()
	}

	verifier := assessment.NewAuthorities(&assessment.AuthorityOptions{
		Git: c.newGit(wspace.Root), Anchor: run.anchor, Root: wspace.Root, Getenv: c.getenv(), Offline: c.opts.Env.Offline,
	})

	input := &assessment.EvalInput{
		Set: run.set, Verifier: verifier, Required: required, Facts: facts, Now: c.now,
		PolicyDigest: assessment.PolicyDigest(inp.policyFiles), AuthorityDigest: reviewAuthorityDigest(wspace, run.anchor),
		Limits: impact.Limits{MaxDepth: cfg.AnalysisLimits.MaxDepth, MaxVisited: cfg.AnalysisLimits.MaxVisited, MaxSeconds: cfg.AnalysisLimits.MaxSeconds},
	}

	// Only `check --incremental` reads and writes the impact cache; a plain check writes nothing under the workspace.
	if c.opts.Incremental {
		input.Incremental = &assessment.Incremental{Dir: filepath.Join(wspace.Root, filepath.FromSlash(impact.CacheDir)), AnchorRevision: run.anchor.Revision}
	}

	return input
}

// profileOf gives the capability profile of the build, nil when none applies.
func profileOf(caps *observation.Capabilities) *observation.CapabilityProfile {
	if caps.Profile == nil {
		return nil
	}

	profile, ok := observation.ProfileFor(caps.ExecutableVersion, caps.AdapterSchema)
	if !ok {
		return nil
	}

	return profile
}

// mergeReview builds the rule results and findings of the review rules from the evaluation and appends them to
// the output of the evaluation, so that dispositions, `check.failOn`, `check.unknown`, and the exit code treat them
// as normal findings. It also fills the area. A run that does not evaluate assessments is left as it is.
func (c *checker) mergeReview(inp *inputs, evaluated *EvaluateOutput, input *EvaluateInput) {
	run := c.review
	if run == nil {
		return
	}

	plans := reviewPlans(inp.selected.Plans)
	if len(plans) == 0 {
		return
	}

	for i := range plans {
		plan := &plans[i]
		rule, found := c.reviewRule(run, plan)
		run.rules = append(run.rules, rule)
		run.findings = append(run.findings, found...)
	}

	completeFindings(run.findings, run.rules, input, evaluated.Reconciliation)

	evaluated.RuleResults = append(evaluated.RuleResults, run.rules...)
	slices.SortStableFunc(evaluated.RuleResults, func(a, b RuleResult) int { return compareStrings(a.RuleID, b.RuleID) })

	evaluated.Findings = append(evaluated.Findings, run.findings...)
	slices.SortStableFunc(evaluated.Findings, func(a, b Finding) int {
		return cmp.Or(compareStrings(a.RuleID, b.RuleID), compareStrings(a.Fingerprint, b.Fingerprint))
	})
}

// reviewUnit is the result of one unit of a review rule: a required assessment, a removed item, a capability loss, or a
// relaxation. A violation or an unknown unit has a finding.
type reviewUnit struct {
	finding *Finding
	subject string
	// env is the environment of the unit: its enforcement mode is the mode of the rule in it.
	env     string
	verdict RuleVerdict
	// code and message are the reason of a unit that is not a pass.
	code, message string
	// key identifies the unit within the rule, for the obligation id of a unit that has no finding.
	key string
}

// mode gives the enforcement mode of a review rule in an environment: the stronger of the mode that the working tree
// gives and the mode that the trust anchor gives (design D7: the mode that verification uses is read from the
// anchor, so a candidate cannot lower it). An active rollback overrides both, as it does for every rule.
func (r *reviewRun) mode(cfg *EnforcementConfig, plan *policy.RulePlan, env string) policy.Enforcement {
	mode := cfg.Resolve(plan, env)
	if cfg.Rollback != nil || !r.anchorGoverns() {
		return mode.Mode
	}

	atAnchor := EnforcementConfig{Entries: r.anchor.Enforcement}

	return Stronger(mode.Mode, atAnchor.Resolve(plan, env).Mode)
}

// anchorGoverns says whether the trust anchor is configured and readable, so that its enforcement modes count.
func (r *reviewRun) anchorGoverns() bool {
	return r.anchor != nil && r.anchor.Readable() && r.anchorConfigured
}

// open says whether the unit is a violation or an unknown, which gives a reason and counts toward the mode of the rule.
func (u *reviewUnit) open() bool {
	return u.verdict == VerdictViolation || u.verdict == VerdictUnknown
}

// reviewUnits gives the units of a review rule: one set for each required assessment, none when nothing is required.
func (c *checker) reviewUnits(run *reviewRun, plan *policy.RulePlan) []reviewUnit {
	if run.eval == nil || len(run.eval.Required) == 0 {
		return nil
	}

	switch plan.Predicate.AssessmentCurrency {
	case "approval":
		return c.approvalUnits(run, plan)
	case "disposition":
		return c.dispositionUnits(run, plan)
	case "capability":
		return c.capabilityUnits(run, plan)
	case "governance":
		return c.governanceUnits(run, plan)
	default:
		return nil
	}
}

// reviewTally collects what the units of one review rule add up to.
type reviewTally struct {
	mode     policy.Enforcement
	seen     map[string]bool
	reasons  []Reason
	subjects []string
	found    []Finding
	oblig    []ObligationResult
	verdicts []RuleVerdict
	haveMode bool
}

func newReviewTally(size int) *reviewTally {
	return &reviewTally{
		reasons:  []Reason{},
		oblig:    make([]ObligationResult, 0, size),
		verdicts: make([]RuleVerdict, 0, size),
		seen:     map[string]bool{},
	}
}

// add counts one unit that has the given enforcement mode.
func (t *reviewTally) add(plan *policy.RulePlan, unit *reviewUnit, unitMode policy.Enforcement) {
	t.verdicts = append(t.verdicts, unit.verdict)
	t.oblig = append(t.oblig, reviewObligation(plan, unit, unitMode))

	if unit.verdict != VerdictNotApplicable {
		t.subjects = append(t.subjects, unit.subject)
	}

	if unit.open() {
		t.reasons = append(t.reasons, Reason{Code: unit.code, Message: unit.message})
		t.raiseMode(unitMode)
	}

	if unit.finding != nil && !t.seen[unit.finding.Fingerprint] {
		t.seen[unit.finding.Fingerprint] = true
		unit.finding.Enforcement = unitMode

		t.found = append(t.found, *unit.finding)
	}
}

// raiseMode makes the mode of the rule the strongest mode of the units that are not a pass.
func (t *reviewTally) raiseMode(unitMode policy.Enforcement) {
	if !t.haveMode {
		t.mode, t.haveMode = unitMode, true

		return
	}

	t.mode = Stronger(t.mode, unitMode)
}

// result makes the rule result and the findings of the tally; a rule with no unit says that nothing is required.
func (t *reviewTally) result(plan *policy.RulePlan) (RuleResult, []Finding) {
	if len(t.verdicts) == 0 {
		t.reasons = append(t.reasons, Reason{Code: "no-required-assessment", Message: "no assessment is required"})
	}

	slices.SortStableFunc(t.found, func(a, b Finding) int { return canonjson.CompareUTF16(a.Fingerprint, b.Fingerprint) })
	slices.SortStableFunc(t.oblig, func(a, b ObligationResult) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	if len(t.oblig) == 0 {
		t.oblig = nil
	}

	return RuleResult{
		RuleID: plan.RuleID, Name: plan.Name, Severity: plan.Severity, Policy: plan.Pack, Family: policy.FamilyAssessmentCurrency,
		Verdict: WorstVerdict(t.verdicts), Reasons: t.reasons, Subjects: sortedUniqueStrings(t.subjects), Targets: []string{},
		Plan: *plan, FindingCount: len(t.found), Enforcement: t.mode, Obligations: t.oblig,
	}, t.found
}

// reviewRule makes the rule result of one review rule from its units, and its findings.
func (c *checker) reviewRule(run *reviewRun, plan *policy.RulePlan) (RuleResult, []Finding) {
	units := c.reviewUnits(run, plan)
	tally := newReviewTally(len(units))

	for i := range units {
		unit := &units[i]
		tally.add(plan, unit, run.mode(&c.enforcement, plan, unit.env))
	}

	if !tally.haveMode {
		tally.mode = run.mode(&c.enforcement, plan, "")
	}

	return tally.result(plan)
}

// reviewObligation gives the obligation result of one unit of a review rule. Its mode is the mode of the unit's
// environment, which the gates read; the id is the one that the finding of the unit carries.
func reviewObligation(plan *policy.RulePlan, unit *reviewUnit, mode policy.Enforcement) ObligationResult {
	id := reviewObligationID(plan.RuleID, unit.subject, unit.env, unit.key)
	if unit.finding != nil {
		id = unit.finding.ObligationID
	}

	result := ObligationResult{
		Verdict: unit.verdict, Occurrences: []OccurrenceResult{}, Enforcement: mode,
		Obligation: Obligation{ID: id, RuleID: plan.RuleID, Subject: unit.subject, Environment: unit.env, Bindings: []string{}},
	}

	if unit.open() {
		result.Detail = unit.message
	}

	if unit.verdict == VerdictUnknown {
		result.Reason = closedReviewReason(unit.code)
	}

	return result
}

// sortedUniqueStrings gives the sorted unique strings; never nil.
func sortedUniqueStrings(in []string) []string {
	out := slices.Clone(in)
	slices.SortFunc(out, canonjson.CompareUTF16)

	return append([]string{}, slices.Compact(out)...)
}

// reviewInputs fills the run manifest inputs of the review evaluation: the assessment and assumption files, the
// approval and revocation files, the digest of the authority configuration that verification used, and the
// revision of the trust anchor.
func (c *checker) reviewInputs(wspace *workspace.Workspace, inputs *RunInputs) {
	run := c.review
	if run == nil || run.set == nil {
		return
	}

	for _, f := range run.set.Files {
		ref := InputRef{Path: f.Path, Digest: f.Digest}

		switch f.Kind {
		case assessment.KindAssessment, assessment.KindAssumption:
			inputs.Assessments = append(inputs.Assessments, ref)
		case assessment.KindApproval, assessment.KindRevocation:
			inputs.Approvals = append(inputs.Approvals, ref)
		}
	}

	inputs.AuthorityDigest = reviewAuthorityDigest(wspace, run.anchor)

	if run.anchor != nil {
		inputs.TrustAnchor = run.anchor.Revision
	}
}

// applyReview adds the review sections and the area to the run. A run that does not evaluate assessments is left
// as it is, so its result is the one of before.
func (c *checker) applyReview(run *Run, wspace *workspace.Workspace) {
	review := c.review
	if review == nil {
		return
	}

	if review.eval == nil {
		// The pack is enabled and the workspace requires no assessment: not configured, never a pass.
		if len(review.rules) > 0 {
			run.Areas.AssessmentCurrency = AreaResult{Verdict: AreaNotConfigured, Counts: map[string]int{}}
		}

		return
	}

	run.ReviewEvidence = c.reviewEvidence(wspace)
	run.AssessmentCurrency = currencyReport(review.eval)
	run.Governance = review.governanceReport()
	run.Analysis = analysisReport(review.eval)
	run.Areas.AssessmentCurrency = review.reviewArea(run.Rules, run.Findings)
}

// reviewAuthorityDigest is the digest of the authority configuration that verification used: the trust anchor's when
// one is configured (empty when it is unreadable, so nothing is compared), else the working tree's. `assessment
// approve` records the same value.
func reviewAuthorityDigest(wspace *workspace.Workspace, anchor *assessment.Anchor) string {
	if wspace.Assessments == nil {
		return ""
	}

	if wspace.Assessments.TrustAnchor == nil {
		return assessment.AuthorityDigest(wspace.Assessments.Authorities)
	}

	if !anchor.Readable() || anchor.Config == nil {
		return ""
	}

	return assessment.AuthorityDigest(anchor.Config.Authorities)
}

// currencyEvaluation gives the evaluation of the run for the consumers (the views), nil when the run evaluated no
// assessments. It is the evaluation that the review rules use, not a second one.
func (c *checker) currencyEvaluation() *assessment.Evaluation {
	if c.review == nil {
		return nil
	}

	return c.review.eval
}

// getenv gives the environment reader of the run: Deps.Getenv, else the process environment. Every signature check
// (approval records and scoped waivers) uses it, so `$COSIGN` is honored the same way everywhere.
func (c *checker) getenv() func(string) string {
	if c.opts.Deps.Getenv != nil {
		return c.opts.Deps.Getenv
	}

	return os.Getenv
}
