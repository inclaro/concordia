package conformance_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// ruleIn makes a typed rule that declares a family, a pack, and (optionally) its own mode.
func ruleIn(id, family, pack string, own policy.Enforcement) policy.RulePlan {
	rule := typedRule(id, []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)
	rule.Family, rule.Pack, rule.Enforcement = family, policy.PackRef{ID: pack, Version: "1"}, own

	return rule
}

func asBuilt() policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyAcyclic}, nil)
}

func TestResolveUsesTheFirstMatchingEntry(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{
		{Rules: []string{"T-1"}, Mode: workspace.ModeShadow},
		{Family: "deploy", Environment: "staging", Mode: workspace.ModeMandatory},
		{Family: "deploy", Mode: workspace.ModeAdvisory},
		{Pack: "infra", Mode: workspace.ModeMandatory},
	}}

	for _, tc := range []struct {
		name    string
		env     string
		want    policy.Enforcement
		source  conformance.ModeSource
		rule    policy.RulePlan
		wantIdx int
	}{
		{"the first entry wins over a later entry that also matches", "staging", policy.EnforcementShadow, conformance.SourceWorkspace, ruleIn("T-1", "deploy", "p", ""), 0},
		{"an entry by family and environment", "staging", policy.EnforcementMandatory, conformance.SourceWorkspace, ruleIn("T-2", "deploy", "p", ""), 1},
		{"the same family in another environment falls to the next entry", "production", policy.EnforcementAdvisory, conformance.SourceWorkspace, ruleIn("T-2", "deploy", "p", ""), 2},
		{"a result with no environment does not match an entry that names one", "", policy.EnforcementAdvisory, conformance.SourceWorkspace, ruleIn("T-2", "deploy", "p", ""), 2},
		{"an entry by pack", "production", policy.EnforcementMandatory, conformance.SourceWorkspace, ruleIn("T-3", "", "infra", ""), 3},
		{"the workspace overrides the mode of the rule", "production", policy.EnforcementAdvisory, conformance.SourceWorkspace, ruleIn("T-4", "deploy", "p", policy.EnforcementMandatory), 2},
		{"the rule's own mode applies when no entry matches", "production", policy.EnforcementShadow, conformance.SourceRule, ruleIn("T-5", "other", "p", policy.EnforcementShadow), -1},
		{"a typed rule defaults to advisory", "production", policy.EnforcementAdvisory, conformance.SourceDefault, ruleIn("T-5", "other", "p", ""), -1},
		{"an as-built rule defaults to mandatory", "", policy.EnforcementMandatory, conformance.SourceDefault, asBuilt(), -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := cfg.Resolve(&tc.rule, tc.env)

			mismatch := got.Mode != tc.want || got.Source != tc.source
			if mismatch || got.Entry != tc.wantIdx {
				t.Fatalf("Resolve = %+v, want mode %s source %s entry %d", got, tc.want, tc.source, tc.wantIdx)
			}
		})
	}
}

func TestResolveNeedsEverySelectorToMatch(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Family: "deploy", Pack: "infra", Mode: workspace.ModeMandatory}}}

	if got := cfg.Resolve(new(ruleIn("T-1", "deploy", "other", "")), "production"); got.Source != conformance.SourceDefault {
		t.Fatalf("a rule of another pack does not match: %+v", got)
	}

	if got := cfg.Resolve(new(ruleIn("T-1", "deploy", "infra", "")), "production"); got.Mode != policy.EnforcementMandatory {
		t.Fatalf("a rule that matches both selectors does: %+v", got)
	}
}

func guarantees(families map[string]observation.FamilyState, declared []string, predicates []observation.PredicateCoverage) *conformance.EnforcementGuarantees {
	return &conformance.EnforcementGuarantees{
		Environments: conformance.RunEnvironments{Selected: declared, Excluded: []string{}}, Declared: declared, Families: families, Predicates: predicates,
	}
}

var stableUsesImage = map[string]observation.FamilyState{
	"uses-image": {Level: observation.SupportSupported, Maturity: observation.MaturityStable},
}

func mandatoryEntry(env string) []workspace.EnforcementEntry {
	return []workspace.EnforcementEntry{{Family: "deploy", Environment: env, Mode: workspace.ModeMandatory}}
}

// refusal runs the validation and gives the diagnostic error.
func refusal(t *testing.T, cfg *conformance.EnforcementConfig, rules []policy.RulePlan, g *conformance.EnforcementGuarantees) *diag.Error {
	t.Helper()

	err := cfg.ValidateEnforcement(rules, g)
	if err == nil {
		t.Fatalf("no refusal, want CO-POL-018")
	}

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a diagnostic", err)
	}

	rejected := failure.Diagnostic.Code != diag.POL018 || failure.ExitCode != diag.InvalidInput
	if rejected || failure.Diagnostic.Severity != diag.SeverityError {
		t.Fatalf("diagnostic = %+v, want CO-POL-018, error, exit 2", failure)
	}

	return failure
}

func mentions(t *testing.T, failure *diag.Error, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(failure.Diagnostic.Message, part) {
			t.Errorf("message %q does not name %q", failure.Diagnostic.Message, part)
		}
	}
}

func TestMandatoryWithoutEnvironmentAttributionIsRefused(t *testing.T) {
	t.Parallel()

	rule := ruleIn("T-1", "deploy", "p", "")
	cfg := conformance.EnforcementConfig{Entries: mandatoryEntry("production")}

	failure := refusal(t, &cfg, []policy.RulePlan{rule}, guarantees(stableUsesImage, nil, records(observation.CoverageComplete, observation.KindUsesImage)))
	mentions(t, failure, "T-1", "production", "environment attribution missing")

	// An environment that the workspace does not declare gives the same refusal, even when others are declared.
	failure = refusal(t, &cfg, []policy.RulePlan{rule}, guarantees(stableUsesImage, []string{"staging"}, records(observation.CoverageComplete, observation.KindUsesImage)))
	mentions(t, failure, "T-1", "production", "environment attribution missing")
}

func TestMandatoryRuleOfTheRuleItselfNeedsAnEnvironmentToo(t *testing.T) {
	t.Parallel()

	rule := ruleIn("T-1", "deploy", "p", policy.EnforcementMandatory)
	rule.Obligation.Scope.Environments = []string{}

	var cfg conformance.EnforcementConfig

	failure := refusal(t, &cfg, []policy.RulePlan{rule}, guarantees(stableUsesImage, nil, records(observation.CoverageComplete, observation.KindUsesImage)))
	mentions(t, failure, "T-1", "all environments", "environment attribution missing")
}

func TestMandatoryOnAnAdvisoryFamilyIsRefused(t *testing.T) {
	t.Parallel()

	advisory := map[string]observation.FamilyState{"uses-image": {Level: observation.SupportSupported, Maturity: observation.MaturityAdvisory}}
	cfg := conformance.EnforcementConfig{Entries: mandatoryEntry("production")}
	rule := ruleIn("T-1", "deploy", "p", "")
	declared := []string{"production", "staging"}

	failure := refusal(t, &cfg, []policy.RulePlan{rule}, guarantees(advisory, declared, records(observation.CoverageComplete, observation.KindUsesImage)))
	mentions(t, failure, "T-1", "production", "uses-image", "advisory")

	// No profile (nil families) leaves every infrastructure family advisory.
	failure = refusal(t, &cfg, []policy.RulePlan{rule}, guarantees(nil, declared, records(observation.CoverageComplete, observation.KindUsesImage)))
	mentions(t, failure, "uses-image", "advisory")
}

func TestMandatoryWithoutACoveragePredicateIsRefused(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryEntry("production")}

	failure := refusal(t, &cfg, []policy.RulePlan{ruleIn("T-1", "deploy", "p", "")}, guarantees(stableUsesImage, []string{"production"}, nil))
	mentions(t, failure, "T-1", "production", "coverage predicate uses-image missing")
}

func TestEveryAbsentGuaranteeIsListed(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Family: "deploy", Mode: workspace.ModeMandatory}}}
	rules := []policy.RulePlan{ruleIn("T-2", "deploy", "p", ""), ruleIn("T-1", "deploy", "p", "")}

	failure := refusal(t, &cfg, rules, guarantees(nil, []string{"production"}, nil))
	mentions(t, failure, "T-1")

	if len(failure.Diagnostic.Cause) < 3 {
		t.Fatalf("each absent guarantee of each rule is a cause, got %d: %+v", len(failure.Diagnostic.Cause), failure.Diagnostic.Cause)
	}
}

func TestMandatoryWithItsGuaranteesRuns(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryEntry("production")}
	g := guarantees(stableUsesImage, []string{"production", "staging"}, records(observation.CoverageComplete, observation.KindUsesImage))

	if err := cfg.ValidateEnforcement([]policy.RulePlan{ruleIn("T-1", "deploy", "p", "")}, g); err != nil {
		t.Fatalf("the guarantees are present: %v", err)
	}
}

func TestExplicitAdvisoryRunsWithoutTheGuarantees(t *testing.T) {
	t.Parallel()

	rules := []policy.RulePlan{ruleIn("T-1", "deploy", "p", "")}

	for name, entries := range map[string][]workspace.EnforcementEntry{
		"advisory": {{Family: "deploy", Mode: workspace.ModeAdvisory}},
		"shadow":   {{Family: "deploy", Mode: workspace.ModeShadow}},
		"advisory only in the environment that lacks them": {
			{Family: "deploy", Environment: "production", Mode: workspace.ModeAdvisory}, {Family: "deploy", Mode: workspace.ModeShadow},
		},
		"the typed default": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := conformance.EnforcementConfig{Entries: entries}
			// No environment is declared, the family is advisory (no profile), and no coverage record exists.
			if err := cfg.ValidateEnforcement(rules, guarantees(nil, nil, nil)); err != nil {
				t.Fatalf("%s must run without a configuration error: %v", name, err)
			}

			if got := cfg.Resolve(&rules[0], "production").Mode; got == policy.EnforcementMandatory {
				t.Fatalf("mode = %s: Concordia never raises a mode", got)
			}
		})
	}
}

func TestADefaultAsBuiltRuleIsNotValidated(t *testing.T) {
	t.Parallel()

	var cfg conformance.EnforcementConfig

	if err := cfg.ValidateEnforcement([]policy.RulePlan{asBuilt()}, guarantees(nil, nil, nil)); err != nil {
		t.Fatalf("a default is not a choice: %v", err)
	}
}

func TestNoModeIsEverLowered(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryEntry("production")}
	rule := ruleIn("T-1", "deploy", "p", "")

	// The refusal leaves the configured mode as it is: nothing in the package turns it into advisory or shadow.
	_ = refusal(t, &cfg, []policy.RulePlan{rule}, guarantees(nil, nil, nil))

	if got := cfg.Resolve(&rule, "production"); got.Mode != policy.EnforcementMandatory {
		t.Fatalf("mode after the refusal = %s, want mandatory", got.Mode)
	}
}

func TestExcludedEnvironmentsAreNotValidated(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryEntry("production")}
	g := guarantees(nil, []string{"staging"}, nil)
	g.Environments = conformance.RunEnvironments{Selected: []string{"staging"}, Excluded: []string{"production"}}

	rule := ruleIn("T-1", "deploy", "p", "")
	rule.Obligation.Scope.Environments = []string{"production"}

	if err := cfg.ValidateEnforcement([]policy.RulePlan{rule}, g); err != nil {
		t.Fatalf("`--environment staging` leaves production out: %v", err)
	}
}

// rollbackWorkspace has a mandatory entry for production and a rollback that expires on 2026-11-15.
func rollbackWorkspace() *workspace.Workspace {
	return &workspace.Workspace{Check: workspace.Check{
		Enforcement: mandatoryEntry("production"),
		Rollback:    &workspace.Rollback{Reason: "false positives (INC-1)", AuthorizedBy: "jane", Expires: "2026-11-15"},
	}}
}

// requireTypedRollback requires the rollback that rollbackWorkspace declares, with the shadow message.
func requireTypedRollback(t *testing.T, rb *conformance.RunRollback) {
	t.Helper()

	if rb == nil {
		t.Fatalf("rollback = %+v", rb)
	}

	if rb.Reason != "false positives (INC-1)" || rb.AuthorizedBy != "jane" {
		t.Fatalf("rollback = %+v", rb)
	}

	if rb.Expires != "2026-11-15" || !strings.Contains(rb.Message, "new enforcement was disabled") {
		t.Fatalf("rollback = %+v", rb)
	}
}

func TestRollbackForcesTypedRulesToShadow(t *testing.T) {
	t.Parallel()

	cfg, err := conformance.NewEnforcementConfig(rollbackWorkspace(), time.Date(2026, 11, 15, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("a rollback that expires today is active: %v", err)
	}

	requireTypedRollback(t, cfg.Rollback)

	typed := ruleIn("T-1", "deploy", "p", policy.EnforcementMandatory)

	for _, env := range []string{"production", "staging", ""} {
		if got := cfg.Resolve(&typed, env); got.Mode != policy.EnforcementShadow || got.Source != conformance.SourceRollback {
			t.Errorf("typed rule in %q = %+v, want shadow by rollback (also over mandatory entries and the rule's own mode)", env, got)
		}
	}

	if got := cfg.Resolve(new(asBuilt()), ""); got.Mode != policy.EnforcementMandatory {
		t.Errorf("an as-built rule keeps its mode: %+v", got)
	}

	// A rule that runs as shadow depends on no guarantee.
	if err := cfg.ValidateEnforcement([]policy.RulePlan{typed}, guarantees(nil, nil, nil)); err != nil {
		t.Fatalf("a rolled back rule needs no guarantee: %v", err)
	}
}

// Scenario: Rollback overrides an explicit governance mode.
// Scenario: Rollback leaves the other families alone.
// An active rollback runs the decision-governance rules as shadow, also over a `mandatory` entry for their family,
// and leaves the code families and `view-currency` with the mode that the entry, the rule, or the default gives.
func TestRollbackCoversDecisionGovernanceRules(t *testing.T) {
	t.Parallel()

	wsp := rollbackWorkspace()
	wsp.Check.Enforcement = []workspace.EnforcementEntry{
		{Family: string(policy.FamilyDecisionGovernance), Mode: string(policy.EnforcementMandatory)},
		{Family: string(policy.FamilyView), Mode: string(policy.EnforcementAdvisory)},
	}

	cfg, err := conformance.NewEnforcementConfig(wsp, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(cfg.Rollback.Message, "decision-governance, and review rules ran as shadow") {
		t.Errorf("rollback message = %q", cfg.Rollback.Message)
	}

	governance := plan(policy.Predicate{Family: policy.FamilyDecisionGovernance}, nil)
	governance.Family = string(policy.FamilyDecisionGovernance)

	view := plan(policy.Predicate{Family: policy.FamilyView}, nil)
	view.Family = string(policy.FamilyView)

	for _, tc := range []struct {
		name   string
		mode   policy.Enforcement
		source conformance.ModeSource
		rule   policy.RulePlan
	}{
		{"governance rule: shadow by rollback over the mandatory entry", policy.EnforcementShadow, conformance.SourceRollback, governance},
		{"view rule: advisory from its entry", policy.EnforcementAdvisory, conformance.SourceWorkspace, view},
		{"as-built rule: its mandatory default", policy.EnforcementMandatory, conformance.SourceDefault, asBuilt()},
	} {
		got := cfg.Resolve(&tc.rule, "")
		if got.Mode != tc.mode || got.Source != tc.source {
			t.Errorf("%s: got %+v, want mode %s source %s", tc.name, got, tc.mode, tc.source)
		}
	}
}

func TestAnExpiredRollbackStopsTheRun(t *testing.T) {
	t.Parallel()

	_, err := conformance.NewEnforcementConfig(rollbackWorkspace(), time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want CO-WRK-018 with exit 2", err)
	}

	if failure.Diagnostic.Code != diag.WRK018 || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("error = %v, want CO-WRK-018 with exit 2", err)
	}

	for _, part := range []string{"2026-11-15", "false positives", "jane"} {
		if !strings.Contains(failure.Diagnostic.Message, part) {
			t.Errorf("message %q does not name %q", failure.Diagnostic.Message, part)
		}
	}
}

func TestNoRollbackSetsNothing(t *testing.T) {
	t.Parallel()

	cfg, err := conformance.NewEnforcementConfig(&workspace.Workspace{Check: workspace.Check{Enforcement: mandatoryEntry("")}}, time.Now())
	if err != nil || cfg.Rollback != nil {
		t.Fatalf("config = %+v, error = %v", cfg, err)
	}

	if len(cfg.Entries) != 1 {
		t.Fatalf("config = %+v, error = %v", cfg, err)
	}
}

func TestFamilyMaturityOfAKind(t *testing.T) {
	t.Parallel()

	name, maturity, ok := conformance.FamilyMaturity(stableUsesImage, observation.KindUsesImage)
	if !ok {
		t.Fatalf("maturity = %s %s %v", name, maturity, ok)
	}

	if name != "uses-image" || maturity != observation.MaturityStable {
		t.Fatalf("maturity = %s %s %v", name, maturity, ok)
	}

	// No profile: the infrastructure families are advisory, the code families stable.
	if _, maturity, _ := conformance.FamilyMaturity(nil, observation.KindUsesImage); maturity != observation.MaturityAdvisory {
		t.Errorf("infrastructure family without a profile = %s, want advisory", maturity)
	}

	if _, maturity, _ := conformance.FamilyMaturity(nil, observation.KindImports); maturity != observation.MaturityStable {
		t.Errorf("code family without a profile = %s, want stable", maturity)
	}

	if _, _, ok := conformance.FamilyMaturity(nil, observation.KindUnsupported); ok {
		t.Errorf("a kind without a family reports ok")
	}
}

func TestStrongerOrdersTheModes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ a, b, want policy.Enforcement }{
		{policy.EnforcementShadow, policy.EnforcementAdvisory, policy.EnforcementAdvisory},
		{policy.EnforcementAdvisory, policy.EnforcementMandatory, policy.EnforcementMandatory},
		{policy.EnforcementMandatory, policy.EnforcementShadow, policy.EnforcementMandatory},
		{policy.EnforcementShadow, "", ""},
	} {
		if got := conformance.Stronger(tc.a, tc.b); got != tc.want {
			t.Errorf("Stronger(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestVisibleSkipsShadowFindings(t *testing.T) {
	t.Parallel()

	for mode, want := range map[policy.Enforcement]bool{"": true, policy.EnforcementMandatory: true, policy.EnforcementAdvisory: true, policy.EnforcementShadow: false} {
		if got := conformance.Visible(&conformance.Finding{Enforcement: mode}); got != want {
			t.Errorf("Visible(%q) = %v, want %v", mode, got, want)
		}
	}
}

// An assurance claim stays advisory until its fixture suite passes (design D7, D25): the rules of the assurance
// families are refused at `mandatory`, whatever the guarantees of the run are.
func TestMandatoryAssuranceRuleIsRefusedAndAdvisoryOrShadowRuns(t *testing.T) {
	t.Parallel()

	rules := map[string]policy.RulePlan{
		"claim":            claimPlan("exact-lineage", func(p *policy.RulePlan) { p.RuleID, p.Family = "ASR-PRV-001", "assurance" }),
		"check result":     checkPlan(),
		"effective access": accessPlan(),
	}
	g := guarantees(nil, []string{"production"}, nil)

	for name, rule := range rules {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Rules: []string{rule.RuleID}, Mode: workspace.ModeMandatory}}}
			mentions(t, refusal(t, &cfg, []policy.RulePlan{rule}, g), rule.RuleID, "family "+string(rule.Predicate.Family)+" has maturity advisory")

			own := rule
			own.Enforcement = policy.EnforcementMandatory

			var none conformance.EnforcementConfig

			mentions(t, refusal(t, &none, []policy.RulePlan{own}, g), "has maturity advisory")

			for _, mode := range []string{workspace.ModeAdvisory, workspace.ModeShadow} {
				allowed := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Rules: []string{rule.RuleID}, Mode: mode}}}
				if err := allowed.ValidateEnforcement([]policy.RulePlan{rule}, g); err != nil {
					t.Errorf("%s must run: %v", mode, err)
				}
			}
		})
	}
}
