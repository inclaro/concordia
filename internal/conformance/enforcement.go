package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// ModeSource says where the enforcement mode of a rule came from.
type ModeSource string

// Sources of an enforcement mode, in the order of precedence (design D10).
const (
	// SourceRollback is an active `check.rollback`: a typed, decision-governance, or review rule runs as `shadow`,
	// whatever else is set.
	SourceRollback ModeSource = "rollback"
	// SourceWorkspace is the first matching entry of `check.enforcement`.
	SourceWorkspace ModeSource = "workspace"
	// SourceRule is the `enforcement` that the rule declares.
	SourceRule ModeSource = "rule"
	// SourceDefault is the default: `mandatory` for the rules of the code evidence families, `advisory` for typed
	// rules and review rules.
	SourceDefault ModeSource = "default"
)

// ResolvedMode is the enforcement mode of one rule in one environment.
type ResolvedMode struct {
	Mode   policy.Enforcement `json:"mode"`
	Source ModeSource         `json:"source"`
	// Entry is the index of the `check.enforcement` entry that gave the mode. -1 when another source did.
	Entry int `json:"entry"`
}

// Explicit says that the workspace or the rule chose the mode, so a guarantee that the mode needs must hold.
// A default does not need one, and Concordia never lowers a mode itself.
func (m ResolvedMode) Explicit() bool { return m.Source == SourceWorkspace || m.Source == SourceRule }

// RunRollback is the `check.rollback` that was active in a run. The run manifest and the summary record it.
type RunRollback struct {
	Reason       string `json:"reason"`
	AuthorizedBy string `json:"authorizedBy"`
	Expires      string `json:"expires"`
	// Message says what the rollback did to the run.
	Message string `json:"message"`
}

// EnforcementConfig is the enforcement setting of a run: the entries of `check.enforcement` in order, and the
// rollback that is active. The zero value sets nothing: the rule's own mode, then the defaults.
//
// Resolve is the one place that decides the mode of a rule. Every result, finding, and gate takes it from there.
type EnforcementConfig struct {
	// Rollback is the active rollback. nil: none.
	Rollback *RunRollback
	Entries  []workspace.EnforcementEntry
}

// NewEnforcementConfig reads `check.enforcement` and `check.rollback` of the workspace. A rollback that expired
// before `at` is CO-WRK-018 (exit 2): the run stops, so that a rollback never stays in place by accident.
func NewEnforcementConfig(ws *workspace.Workspace, at time.Time) (EnforcementConfig, error) {
	cfg := EnforcementConfig{Entries: ws.Check.Enforcement}

	rollback := ws.Check.Rollback
	if rollback == nil {
		return cfg, nil
	}

	if !rollback.Active(at) {
		return cfg, diag.Fail(diag.WRK018, diag.Params{
			"expires": rollback.Expires, paramDetail: fmt.Sprintf("reason '%s', authorized by %s", rollback.Reason, rollback.AuthorizedBy),
		})
	}

	cfg.Rollback = &RunRollback{
		Reason: rollback.Reason, AuthorizedBy: rollback.AuthorizedBy, Expires: rollback.Expires,
		Message: fmt.Sprintf("new enforcement was disabled: typed-obligation, decision-governance, and review rules ran as shadow (reason: %s; authorized by %s; expires %s)",
			rollback.Reason, rollback.AuthorizedBy, rollback.Expires),
	}

	return cfg, nil
}

// isTyped says whether the rule gives typed obligation results: a rule of the `typed` family, or of one of the
// assurance families (`effective-access`, `check-result`, `assurance-claim`), which report one obligation result for
// each subject and environment. Like typed rules they are new enforcement families: advisory by default, run as
// `shadow` under a rollback, and a `mandatory` mode needs the environment attribution of the workspace.
func isTyped(plan *policy.RulePlan) bool {
	return plan.Predicate.Family == policy.FamilyTyped || isExternal(plan)
}

// isReview says whether the rule is a review rule: a rule of the `assessment-currency` family (`review-currency` pack).
func isReview(plan *policy.RulePlan) bool {
	return plan.Predicate.Family == policy.FamilyAssessmentCurrency
}

// rolledBack says whether an active rollback runs the rule as `shadow`: the new-enforcement families, the rules of
// typed obligations, of the `decision-governance` family, and of the `assessment-currency` family (the review rules).
// The code families and `view-currency` keep their mode.
func rolledBack(plan *policy.RulePlan) bool {
	return isTyped(plan) || plan.Predicate.Family == policy.FamilyDecisionGovernance || isReview(plan)
}

// matches says whether the entry selects the rule in the environment. The selectors that the entry gives must all match.
func matches(entry *workspace.EnforcementEntry, plan *policy.RulePlan, environment string) bool {
	return (entry.Family == "" || entry.Family == plan.Family) &&
		(entry.Pack == "" || entry.Pack == plan.Pack.ID) &&
		(len(entry.Rules) == 0 || slices.Contains(entry.Rules, plan.RuleID)) &&
		(entry.Environment == "" || entry.Environment == environment)
}

// Resolve gives the enforcement mode of a rule in an environment ("" for a result that has none): an active
// rollback (typed, decision-governance, and review rules only), else the first matching entry of `check.enforcement`,
// else the mode that the rule declares, else the default (mandatory for the code families, advisory for typed rules
// and review rules).
func (c *EnforcementConfig) Resolve(plan *policy.RulePlan, environment string) ResolvedMode {
	if c.Rollback != nil && rolledBack(plan) {
		return ResolvedMode{Mode: policy.EnforcementShadow, Source: SourceRollback, Entry: -1}
	}

	for i := range c.Entries {
		if matches(&c.Entries[i], plan, environment) {
			return ResolvedMode{Mode: policy.Enforcement(c.Entries[i].Mode), Source: SourceWorkspace, Entry: i}
		}
	}

	if plan.Enforcement != "" {
		return ResolvedMode{Mode: plan.Enforcement, Source: SourceRule, Entry: -1}
	}

	if isTyped(plan) || isReview(plan) {
		return ResolvedMode{Mode: policy.EnforcementAdvisory, Source: SourceDefault, Entry: -1}
	}

	return ResolvedMode{Mode: policy.EnforcementMandatory, Source: SourceDefault, Entry: -1}
}

// Stronger gives the mode that gates more: mandatory, then advisory, then shadow. An empty mode counts as
// `mandatory` (a result with no mode gates).
func Stronger(a, b policy.Enforcement) policy.Enforcement {
	if strength(b) > strength(a) {
		return b
	}

	return a
}

func strength(mode policy.Enforcement) int {
	switch mode {
	case policy.EnforcementShadow:
		return 0
	case policy.EnforcementAdvisory:
		return 1
	case policy.EnforcementMandatory, "":
		return 2
	}

	return 0
}

// RuleFamilies gives the evidence families that the rule depends on, sorted: the family of the relation of a typed
// rule, else the families of the edge kinds that the rule traverses.
func RuleFamilies(plan *policy.RulePlan) []string {
	kinds := plan.Traversal.EdgeKinds
	if plan.Obligation != nil {
		kinds = []observation.CanonicalEdgeKind{plan.Obligation.Relation.Kind}
	}

	var out []string

	for _, kind := range kinds {
		if name, ok := observation.FamilyForKind(kind); ok && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}

	slices.Sort(out)

	return out
}

// FamilyMaturity gives the maturity of the evidence family of an edge kind in this run. families is
// observation.Capabilities.Families; nil means that no profile applies (observation.NoProfileFamilies). The bool
// is false for a kind that has no family.
func FamilyMaturity(families map[string]observation.FamilyState, kind observation.CanonicalEdgeKind) (string, observation.FamilyMaturity, bool) {
	name, ok := observation.FamilyForKind(kind)
	if !ok {
		return "", "", false
	}

	if families == nil {
		families = observation.NoProfileFamilies()
	}

	state, ok := families[name]
	if !ok {
		return name, "", false
	}

	return name, state.Maturity, true
}

// EnforcementGuarantees are what a `mandatory` rule depends on, besides the rule itself.
type EnforcementGuarantees struct {
	// Environments is the environment selection of the run.
	Environments RunEnvironments
	// Declared are the IDs of the declared environments.
	Declared []string
	// Families is the support of each family in this run (observation.Capabilities.Families). nil: no profile applies.
	Families map[string]observation.FamilyState
	// Predicates are the coverage records of the run.
	Predicates []observation.PredicateCoverage
	// Authorities is the number of authorities under `assessments.authorities`.
	Authorities int
	// TrustAnchor says whether `assessments.trustAnchor` is configured.
	TrustAnchor bool
}

const noEnvironment = "all environments"

// ValidateEnforcement checks the rules that run as `mandatory` by the choice of the workspace or of the rule
// (a default is not a choice). Such a rule depends on guarantees: environment attribution (the environment is
// declared), the maturity `stable` of its evidence family, and a coverage predicate for the family. If one is absent,
// the result is CO-POL-018 (exit 2) naming the rule, the environment, and the guarantee; the run stops and gives no gate
// result. A mandatory review rule also needs one authority and a trust anchor, else the result is CO-ASM-004 (exit 2).
// Concordia never lowers a mode to make the check pass: the workspace chooses `advisory` or `shadow`.
func (c *EnforcementConfig) ValidateEnforcement(plans []policy.RulePlan, have *EnforcementGuarantees) error {
	sorted := slices.Clone(plans)
	slices.SortStableFunc(sorted, func(a, b policy.RulePlan) int { return cmp.Compare(a.RuleID, b.RuleID) })

	var found enforcementProblems

	for i := range sorted {
		c.validatePlan(&sorted[i], have, &found)
	}

	return found.err()
}

// enforcementProblems collects the problems of ValidateEnforcement. The first one gives the error its code, message,
// and exit code; the others are its causes.
type enforcementProblems struct {
	first    *diag.Error
	problems []diag.Diagnostic
}

func (p *enforcementProblems) add(code string, params diag.Params, rule string) {
	p.problems = append(p.problems, diag.New(code, params, diag.WithRule(rule)))

	if p.first == nil {
		p.first = diag.Fail(code, params, diag.WithRule(rule))
	}
}

// err gives the error of the first problem with the others as causes, nil when there is none.
func (p *enforcementProblems) err() error {
	if p.first == nil {
		return nil
	}

	if len(p.problems) > 1 {
		p.first.Diagnostic.Cause = p.problems[1:]
	}

	return p.first
}

// validatePlan checks one rule in each environment that it runs in: a mandatory mode that the workspace or the rule
// chose needs its guarantees, and a mandatory review rule also needs an authority and a trust anchor (CO-ASM-004).
func (c *EnforcementConfig) validatePlan(plan *policy.RulePlan, have *EnforcementGuarantees, found *enforcementProblems) {
	reviewChecked := false

	for _, env := range c.environmentsOf(plan, have) {
		mode := c.Resolve(plan, env)
		if mode.Mode != policy.EnforcementMandatory || !mode.Explicit() {
			continue
		}

		shown := env
		if shown == "" {
			shown = noEnvironment
		}

		if isReview(plan) && !reviewChecked {
			reviewChecked = true

			for _, missing := range missingReviewGuarantees(have) {
				found.add(diag.ASM004, diag.Detail("rule "+plan.RuleID+" is mandatory in "+shown+", but "+missing), plan.RuleID)
			}
		}

		for _, absent := range absentGuarantees(plan, env, have) {
			found.add(diag.POL018, diag.Params{"rule": plan.RuleID, "environment": shown, "guarantee": absent}, plan.RuleID)
		}
	}
}

// missingReviewGuarantees says what a mandatory review rule lacks in the workspace: an authority and a trust anchor.
func missingReviewGuarantees(have *EnforcementGuarantees) []string {
	var missing []string

	if have.Authorities == 0 {
		missing = append(missing, "no authority is configured under `assessments.authorities`")
	}

	if !have.TrustAnchor {
		missing = append(missing, "no trust anchor is configured under `assessments.trustAnchor`")
	}

	return missing
}

// environmentsOf gives the environments in which the mode of a rule is checked, sorted: the environments that a
// typed rule scopes to, else the selected declared environments plus the environments that entries name for the
// rule. "" stands for a rule with no environment (no environment is declared).
func (c *EnforcementConfig) environmentsOf(plan *policy.RulePlan, have *EnforcementGuarantees) []string {
	set := map[string]struct{}{}

	if plan.Obligation != nil && len(plan.Obligation.Scope.Environments) > 0 {
		for _, e := range plan.Obligation.Scope.Environments {
			set[e] = struct{}{}
		}
	} else {
		for _, e := range have.Environments.Selected {
			set[e] = struct{}{}
		}

		for i := range c.Entries {
			if e := c.Entries[i].Environment; e != "" && matches(&c.Entries[i], plan, e) {
				set[e] = struct{}{}
			}
		}

		if len(set) == 0 {
			set[""] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))

	for e := range set {
		if !slices.Contains(have.Environments.Excluded, e) {
			out = append(out, e)
		}
	}

	slices.Sort(out)

	return out
}

// absentGuarantees gives the guarantees that a mandatory rule needs in the environment and the run does not have.
func absentGuarantees(plan *policy.RulePlan, env string, have *EnforcementGuarantees) []string {
	var absent []string

	switch {
	case env == "" && (isTyped(plan) || isReview(plan)):
		absent = append(absent, "environment attribution missing: the workspace declares no environment")
	case env != "" && !slices.Contains(have.Declared, env):
		absent = append(absent, "environment attribution missing: '"+env+"' is not declared under `environments`")
	}

	// A rule of the assurance families is advisory in its maturity: no claim is stable until its fixture suite passes
	// (design D7, D25), so a mandatory mode is refused like one on a family that is not stable.
	if plan.Predicate.Maturity == policy.MaturityAdvisory {
		absent = append(absent, fmt.Sprintf("family %s has maturity %s", plan.Predicate.Family, plan.Predicate.Maturity))
	}

	needsCoverage := plan.Obligation == nil || plan.Obligation.Completeness != policy.CompletenessAny || plan.Obligation.PassNeed() != policy.CompletenessAny

	for _, family := range RuleFamilies(plan) {
		if _, maturity, ok := FamilyMaturity(have.Families, observation.CanonicalEdgeKind(family)); ok && maturity != observation.MaturityStable {
			absent = append(absent, fmt.Sprintf("family %s has maturity %s", family, maturity))
		}

		if needsCoverage && !hasPredicate(have.Predicates, family, env) {
			absent = append(absent, fmt.Sprintf("coverage predicate %s missing", family))
		}
	}

	return absent
}

// hasPredicate says whether the run has a coverage record for the family that applies in the environment: a project-scope
// record, or a record of the environment.
func hasPredicate(records []observation.PredicateCoverage, family, env string) bool {
	return slices.ContainsFunc(records, func(r observation.PredicateCoverage) bool {
		return r.Predicate == family && (r.Scope.Environment == "" || r.Scope.Environment == env)
	})
}

// Visible says whether the renderers show a finding: a `shadow` finding is written to `results.json` and the
// manifest only (pretty, SARIF, and JUnit skip it).
func Visible(f *Finding) bool { return f.Enforcement != policy.EnforcementShadow }

// RuleVisible says whether the renderers show a rule: a rule whose every counted obligation is `shadow` is written
// to `results.json` and the manifest only. A rule with no mode shows.
func RuleVisible(r *RuleResult) bool { return r.Enforcement != policy.EnforcementShadow }

// declaredEnvironments gives the IDs of the environments that the workspace declares, in declaration order.
func declaredEnvironments(ws *workspace.Workspace) []string {
	out := make([]string, len(ws.Environments))
	for i := range ws.Environments {
		out[i] = ws.Environments[i].ID
	}

	return out
}

// authorityCount gives the number of authorities that the workspace configures for review approvals.
func authorityCount(ws *workspace.Workspace) int {
	if ws.Assessments == nil {
		return 0
	}

	return len(ws.Assessments.Authorities)
}
