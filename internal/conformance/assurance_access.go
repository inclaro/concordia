package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/evaluate/rbac"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// A rule of the family `effective-access` asks the built-in evaluators (network-policy, rbac, cloud-firewall) what an
// environment allows between two sets of workloads or planned cloud resources. The evaluators read the rendered
// documents or the plans of the environment; the code graph has no say, because it records no selection edge for an
// empty `podSelector` or a `matchExpressions` selector (O6), and absence of an edge is never isolation (A35). Three
// gates stand before a verdict:
//
//  1. the workspace must enable an evaluator of the kind for the environment (`assurance.evaluators`), else the result
//     is `unknown` with `no-evaluator-available`;
//  2. the environment must have the evidence of the evaluator (rendered resources, or a usable plan for
//     cloud-firewall), else `rendered-evidence-missing`; and a namespace whose render is not complete gives
//     `coverage-incomplete` for the results that involve it;
//  3. a result that is `denied` (or `not-granted`) holds only if the enforcement it assumes is declared in
//     `assurance.evaluators[].assumes`, else `unknown` with `enforcement-not-declared`. A result that is `allowed`
//     (or `granted`) needs no assumption: an unenforced policy only allows more.

// EvaluatorIdentity is the identity of a built-in evaluator that the run used: the manifest toolchain and the bundle
// record it, so that an evaluator upgrade that changes results is visible.
type EvaluatorIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// ContractDigest is the digest of the evaluator contract (`sha256:<hex>`).
	ContractDigest string `json:"contractDigest"`
}

// builtinEvaluators are the built-in evaluators by workspace kind, in the order of evaluatorIdentities.
var builtinEvaluators = []struct {
	contract func() evaluate.Contract
	kind     string
}{
	{cloudfw.Contract, workspace.EvaluatorCloudFirewall},
	{netpol.Contract, workspace.EvaluatorNetworkPolicy},
	{rbac.Contract, workspace.EvaluatorRBAC},
}

// evaluatorIdentities lists the evaluators that the workspace enables for at least one environment, sorted by id.
func evaluatorIdentities(cfg *workspace.Assurance) []EvaluatorIdentity {
	if cfg == nil {
		return nil
	}

	var out []EvaluatorIdentity

	for _, builtin := range builtinEvaluators {
		enabled := slices.ContainsFunc(cfg.Evaluators, func(e workspace.AssuranceEvaluator) bool { return slices.Contains(e.Kinds, builtin.kind) })
		if enabled {
			contract := builtin.contract()
			out = append(out, EvaluatorIdentity{ID: contract.ID, Version: contract.Version, ContractDigest: contract.Digest()})
		}
	}

	slices.SortFunc(out, func(a, b EvaluatorIdentity) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return out
}

// EvaluationEntry is one effective-policy evaluation of `assurance.evaluations`: one query that a rule used, with the
// evaluator that answered it and what the answer rests on.
type EvaluationEntry struct {
	Environment string `json:"environment"`
	// Kind is the evaluator kind: `network-policy`, `rbac`, or `cloud-firewall`.
	Kind string `json:"kind"`
	// Query says what was asked, for example `tcp/8080 from shop/web to shop/db` or
	// `get secrets/db in shop for ServiceAccount shop/api`.
	Query string `json:"query"`
	// Verdict is the answer of the evaluator: `allowed`, `denied`, `granted`, `not-granted`, or `unknown`.
	Verdict string `json:"verdict"`
	// Reason is the reason of the evaluator for an `unknown` answer.
	Reason string `json:"reason,omitempty"`
	// Evaluator, EvaluatorVersion, and InputDigest identify the evaluator and the rendered input that it read.
	Evaluator        string `json:"evaluator"`
	EvaluatorVersion string `json:"evaluatorVersion"`
	InputDigest      string `json:"inputDigest"`
	// Integrity is the integrity of the input binding (`cloud-firewall` only): `content not verified`, or `producer
	// authenticated` when a verified statement binds the plan.
	Integrity string `json:"integrity,omitempty"`
	// Policies are the policies, bindings, and roles that decided the answer, sorted.
	Policies []string `json:"policies"`
	// Assumptions are the enforcement assumptions that the answer rests on, and DeclaredAssumptions the ones of them that
	// the workspace declares for the environment.
	Assumptions         []string `json:"assumptions"`
	DeclaredAssumptions []string `json:"declaredAssumptions"`
	// Limits are what the evaluator does not evaluate.
	Limits []string        `json:"limits"`
	Gaps   []assurance.Gap `json:"gaps"`
}

// recordEvaluation adds an entry to the report. The list stays sorted by environment, kind, and query, and an entry
// that two rules asked for is kept once, so the list does not depend on the order of the rules.
func (r *assuranceRun) recordEvaluation(entry *EvaluationEntry) {
	if r.report == nil {
		return
	}

	index, found := slices.BinarySearchFunc(r.report.Evaluations, entry, func(a EvaluationEntry, b *EvaluationEntry) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Environment, b.Environment), canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Query, b.Query),
		)
	})
	if found {
		return
	}

	r.report.Evaluations = slices.Insert(r.report.Evaluations, index, *entry)
}

// accessKind gives the evaluator kind that answers the dimension of a rule.
func accessKind(access *policy.EffectiveAccess) string {
	if access.Dimension == policy.DimensionAuthorization {
		return workspace.EvaluatorRBAC
	}

	return workspace.EvaluatorNetworkPolicy
}

// evaluatorScope is one evaluator kind in one environment.
type evaluatorScope struct {
	kind, environment string
}

// noEvaluatorDetail is the detail of a pair whose evaluator kind the workspace does not enable for the environment.
func (s evaluatorScope) noEvaluatorDetail() string {
	return fmt.Sprintf("no %s evaluator is enabled for environment %s (assurance.evaluators)", s.kind, s.environment)
}

// evaluatorFor gives the declaration that enables an evaluator of the kind for the environment, or nil.
func evaluatorFor(cfg *workspace.Assurance, scope evaluatorScope) *workspace.AssuranceEvaluator {
	if cfg == nil {
		return nil
	}

	for i := range cfg.Evaluators {
		if cfg.Evaluators[i].Environment == scope.environment && slices.Contains(cfg.Evaluators[i].Kinds, scope.kind) {
			return &cfg.Evaluators[i]
		}
	}

	return nil
}

// accessEnvironments gives the environments that an effective-access rule is evaluated in: the selected environments
// of the run, else (no environment is declared) the ones that have rendered evidence or an evaluator.
func (c *checker) accessEnvironments(run *assuranceRun) []string {
	if len(c.environments.Selected) > 0 {
		return c.environments.Selected
	}

	var environments []string

	for _, result := range run.ev.Renders {
		environments = append(environments, result.Environment)
	}

	if run.cfg != nil {
		for i := range run.cfg.Evaluators {
			environments = append(environments, run.cfg.Evaluators[i].Environment)
		}
	}

	slices.SortFunc(environments, canonjson.CompareUTF16)

	return slices.Compact(environments)
}

// accessOutcomes evaluates a rule of the family `effective-access`: for each environment, each subject and target pair
// (CALM elements mapped to rendered resources by the bindings), and each port (network) or verb (authorization).
func (c *checker) accessOutcomes(plan *policy.RulePlan) ([]ExternalOutcome, bool) {
	run := c.assurance
	if run == nil || run.ev == nil {
		return nil, false
	}

	if len(plan.Subjects) == 0 || len(plan.Targets) == 0 {
		return nil, true
	}

	environments := c.accessEnvironments(run)
	if len(environments) == 0 {
		return nil, false
	}

	kind := accessKind(plan.Predicate.EffectiveAccess)

	var outcomes []ExternalOutcome

	for _, environment := range environments {
		outcomes = append(outcomes, run.accessIn(plan, evaluatorScope{kind: kind, environment: environment})...)
	}

	c.warnEvaluatorGaps(run)

	return outcomes, true
}

// warnEvaluatorGaps adds a CO-PRV-013 warning for each kind of gap of an evaluation that the run has not warned about
// yet in the environment and evaluator kind: an unsupported construct (a CNI-specific policy kind, an unresolvable
// named port, a cluster-dependent range) or incomplete input of a namespace. The warning names the first gap of the
// kind that the run met; every gap is in `assurance.evaluations`. However many rules and queries meet a gap kind, the
// run warns once. The evaluations are sorted, so the warnings do not depend on the order of the rules.
func (c *checker) warnEvaluatorGaps(run *assuranceRun) {
	if run.report == nil {
		return
	}

	for i := range run.report.Evaluations {
		entry := &run.report.Evaluations[i]

		for _, gap := range entry.Gaps {
			key := fmt.Sprintf("environment %s, evaluator %s: %s: ", entry.Environment, entry.Kind, gap.Kind)

			seen := slices.ContainsFunc(c.diags, func(d diag.Diagnostic) bool {
				return d.Code == diag.PRV013 && strings.Contains(d.Message, key)
			})
			if !seen {
				c.diags = append(c.diags, diag.New(diag.PRV013, diag.Detail(key+gap.Detail)))
			}
		}
	}
}

// pairsOf lists the subject and target pairs of a rule. A pair of one element with itself says nothing about access
// between workloads, so it is left out.
func pairsOf(plan *policy.RulePlan) [][2]string {
	var pairs [][2]string

	for _, subject := range plan.Subjects {
		for _, target := range plan.Targets {
			if subject != target {
				pairs = append(pairs, [2]string{subject, target})
			}
		}
	}

	return pairs
}

// accessAsk is one environment and one rule: the evaluator input, the declaration that enables the evaluator, and the
// state of the queries that are made.
type accessAsk struct {
	run         *assuranceRun
	plan        *policy.RulePlan
	model       *envModel
	declaration *workspace.AssuranceEvaluator
	kind        string
}

// unknownCause is the reason and the detail of outcomes that are `unknown` for one cause.
type unknownCause struct {
	reason UnknownReason
	detail string
}

// unknownAll gives one `unknown` outcome for each pair, with the same cause.
func (a *accessAsk) unknownAll(pairs [][2]string, cause unknownCause) []ExternalOutcome {
	out := make([]ExternalOutcome, 0, len(pairs))

	for _, pair := range pairs {
		outcome := a.baseOutcome(pair)
		outcome.Verdict, outcome.Reason, outcome.Detail = VerdictUnknown, cause.reason, cause.detail
		out = append(out, outcome)
	}

	return out
}

// accessIn evaluates the rule in one environment. A network rule sends each pair whose ends are planned cloud
// resources to cloud-firewall (assurance_access_cloud.go); the other pairs need rendered evidence.
func (r *assuranceRun) accessIn(plan *policy.RulePlan, scope evaluatorScope) []ExternalOutcome {
	ask := r.newAccessAsk(plan, scope)
	pairs := pairsOf(plan)

	if scope.kind != workspace.EvaluatorNetworkPolicy {
		return ask.rendered(pairs)
	}

	outcomes, rest := ask.splitCloud(pairs)
	if len(rest) == 0 {
		return outcomes
	}

	return append(outcomes, ask.rendered(rest)...)
}

// newAccessAsk indexes the renders of the environment for the rule.
func (r *assuranceRun) newAccessAsk(plan *policy.RulePlan, scope evaluatorScope) *accessAsk {
	var renders []*render.Result

	for _, result := range r.ev.Renders {
		if result.Environment == scope.environment {
			renders = append(renders, result)
		}
	}

	return &accessAsk{run: r, plan: plan, model: newEnvModel(scope.environment, renders, r.cfg), kind: scope.kind}
}

// scope is the evaluator kind and the environment of the ask.
func (a *accessAsk) scope() evaluatorScope {
	return evaluatorScope{kind: a.kind, environment: a.model.environment}
}

// rendered answers the pairs from the rendered evidence, after the gates of the evaluator declaration and the render.
func (a *accessAsk) rendered(pairs [][2]string) []ExternalOutcome {
	a.declaration = evaluatorFor(a.run.cfg, a.scope())
	if a.declaration == nil {
		return a.unknownAll(pairs, unknownCause{ReasonNoEvaluatorAvailable, a.scope().noEvaluatorDetail()})
	}

	if !a.model.usable() {
		return a.unknownAll(pairs, unknownCause{ReasonRenderedEvidenceMissing,
			fmt.Sprintf("environment %s has no rendered resources (assurance.rendering.sources)", a.model.environment)})
	}

	if a.kind == workspace.EvaluatorRBAC {
		return a.authorization(pairs)
	}

	return a.network(pairs)
}

// accessState is the answer of one query, reduced to what the expectation compares: access exists (`allowed` or
// `granted`), none does (`denied` or `not-granted`), or the evaluator cannot say.
type accessState int

const (
	stateUnknown accessState = iota
	stateOpen
	stateClosed
)

// probe is the answer of one query with what a verdict needs of it.
type probe struct {
	// reason says why an answer is unknown.
	reason UnknownReason
	// label names the query inside the outcome, for example `tcp/8080` or `get`.
	label string
	// evidence names the policies, bindings, and roles that decided the answer.
	evidence    string
	assumptions []string
	state       accessState
}

// judge turns the answers for one subject and target into a verdict against the expectation of the rule. Access
// between the two elements exists when any query is open and is absent when every query is closed: `expect: denied`
// is violated by one open port, `expect: allowed` is met by one open port. A closed answer is only as good as the
// enforcement it assumes, so it needs the declaration (gate 3).
func (a *accessAsk) judge(probes []probe) (RuleVerdict, UnknownReason, string) {
	open, unknown, closed := partitionProbes(probes)

	switch {
	case len(open) > 0:
		return a.verdictFor(stateOpen), "", probeGroup{"open", open}.describe()
	case len(unknown) > 0:
		return VerdictUnknown, unknown[0].reason, probeGroup{"unknown", unknown}.describe()
	case len(closed) == 0:
		return VerdictUnknown, ReasonNoEvidenceSupplied, "the evaluator gave no result"
	}

	detail := probeGroup{"closed", closed}.describe()
	if missing := a.undeclared(closed); len(missing) > 0 {
		return VerdictUnknown, ReasonEnforcementNotDeclared,
			fmt.Sprintf("%s; the result assumes %s, which assurance.evaluators does not declare for the environment", detail, strings.Join(missing, ", "))
	}

	if assumed := assumptionsOf(closed); len(assumed) > 0 {
		detail += "; assumes " + strings.Join(assumed, ", ") + " (declared)"
	}

	return a.verdictFor(stateClosed), "", detail
}

// partitionProbes splits the answers by state, keeping their order.
func partitionProbes(probes []probe) (open, unknown, closed []probe) {
	for _, p := range probes {
		switch p.state {
		case stateOpen:
			open = append(open, p)
		case stateClosed:
			closed = append(closed, p)
		case stateUnknown:
			unknown = append(unknown, p)
		}
	}

	return open, unknown, closed
}

// verdictFor compares the state of the access (open or closed) with what the rule expects: an open access violates
// `expect: denied` and meets `expect: allowed`; a closed access does the reverse.
func (a *accessAsk) verdictFor(state accessState) RuleVerdict {
	access := a.plan.Predicate.EffectiveAccess
	wantClosed := access.Expect == policy.ExpectDenied || access.Expect == policy.ExpectNotGranted

	if (state == stateOpen) == wantClosed {
		return VerdictViolation
	}

	return VerdictPass
}

// assumptionsOf lists the assumptions of the probes, sorted and without duplicates.
func assumptionsOf(probes []probe) []string {
	var out []string

	for _, p := range probes {
		out = append(out, p.assumptions...)
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// undeclared lists the assumptions of the probes that the workspace does not declare for the environment.
func (a *accessAsk) undeclared(probes []probe) []string {
	var missing []string

	for _, assumption := range assumptionsOf(probes) {
		if !slices.Contains(a.declaration.Assumes, assumption) {
			missing = append(missing, assumption)
		}
	}

	return missing
}

// describeLimit is the number of probes that a detail names; the evaluations of the report hold all of them.
const describeLimit = 3

// probeGroup is the probes of one state, named by the state (`open`, `unknown`, `closed`).
type probeGroup struct {
	state  string
	probes []probe
}

// describe lists the probes of the group with their labels and the evidence that decided them.
func (g probeGroup) describe() string {
	shown := g.probes
	if len(shown) > describeLimit {
		shown = shown[:describeLimit]
	}

	parts := make([]string, 0, len(shown)+1)

	for _, p := range shown {
		text := p.label
		if p.evidence != "" {
			text += " (" + p.evidence + ")"
		}

		parts = append(parts, text)
	}

	if rest := len(g.probes) - len(shown); rest > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", rest))
	}

	return g.state + ": " + strings.Join(parts, ", ")
}

// reasonOf maps a reason of the evaluators onto the closed set of the results. The evaluators run only on rendered
// evidence, so a workload, namespace, or role that the rendered input does not hold is missing rendered evidence; a
// construct that the evaluator does not support is a gap in the coverage of the input.
func reasonOf(reason string) UnknownReason {
	switch reason {
	case evaluate.ReasonRenderedEvidenceMissing, evaluate.ReasonSubjectNotFound, evaluate.ReasonRoleTargetMissing:
		return ReasonRenderedEvidenceMissing
	case evaluate.ReasonCoverageIncomplete, evaluate.ReasonUnsupportedConstruct:
		return ReasonCoverageIncomplete
	}

	return ReasonCoverageIncomplete
}

// gapText lists gaps as `kind: detail`.
func gapText(gaps []assurance.Gap) string {
	parts := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		parts = append(parts, gap.Kind+": "+gap.Detail)
	}

	return strings.Join(parts, "; ")
}

// answerFacts are the facts of one evaluator answer that go into the report.
type answerFacts struct {
	query, verdict, reason string
	evaluator, version     string
	digest, integrity      string
	policies               []string
	assumptions, limits    []string
	gaps                   []assurance.Gap
}

// entryOf builds the entry of the report for one answer.
func (a *accessAsk) entryOf(facts *answerFacts) *EvaluationEntry {
	declared := []string{}

	for _, assumption := range facts.assumptions {
		if slices.Contains(a.declaration.Assumes, assumption) {
			declared = append(declared, assumption)
		}
	}

	slices.Sort(facts.policies)

	return &EvaluationEntry{
		Environment: a.model.environment, Kind: a.kind, Query: facts.query, Verdict: facts.verdict, Reason: facts.reason,
		Policies: nonNil(slices.Compact(facts.policies)), Assumptions: nonNil(facts.assumptions), DeclaredAssumptions: declared,
		Limits: nonNil(facts.limits), Gaps: gapsOrEmpty(facts.gaps), Evaluator: facts.evaluator, EvaluatorVersion: facts.version,
		InputDigest: facts.digest, Integrity: facts.integrity,
	}
}

// baseOutcome is the outcome of a pair before any verdict is set.
func (a *accessAsk) baseOutcome(pair [2]string) ExternalOutcome {
	return ExternalOutcome{Subject: pair[0], Target: pair[1], Environment: a.model.environment}
}

// bindingGap gives the `unknown` outcome of a pair because a CALM element has no binding to what the evaluator
// reads in the environment.
func (a *accessAsk) bindingGap(pair [2]string, gap unboundElement) ExternalOutcome {
	out := a.baseOutcome(pair)
	out.Verdict, out.Reason = VerdictUnknown, ReasonBindingGap
	out.Detail = fmt.Sprintf("CALM element %s has no binding to %s of environment %s", gap.calmID, gap.what, a.model.environment)

	return out
}

// unboundElement is a CALM element of a pair and what it has no binding to, for example `a rendered workload`.
type unboundElement struct {
	calmID, what string
}
