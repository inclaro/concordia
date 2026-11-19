package conformance_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// twoEnvironments is a rule of the family `deploy` with a violation in production and in staging (billing has a
// workload in each and neither uses an image). The entries set the mode per environment.
func twoEnvironments(entries ...workspace.EnforcementEntry) *world {
	w := newWorld(observation.KindUsesImage)
	w.add(resource("billing-prod", "Deployment", "production", "billing"), "billing")
	w.add(resource("billing-stg", "Deployment", "staging", "billing"), "billing")

	rule := typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, func(s *policy.ObligationSpec) {
		s.Scope.Environments = []string{"production", "staging"}
	})
	rule.Family, rule.Enforcement = "deploy", ""
	w.plans = []policy.RulePlan{rule}
	w.enforcement = conformance.EnforcementConfig{Entries: entries}

	return w
}

// deployEntryOf is the enforcement entry of the family `deploy` for the environment.
func deployEntryOf(env, mode string) workspace.EnforcementEntry {
	return workspace.EnforcementEntry{Family: "deploy", Environment: env, Mode: mode}
}

// assertFindingModes checks that there is one violation in each environment, with the mode of its environment.
func assertFindingModes(t *testing.T, out *conformance.EvaluateOutput, modes map[string]policy.Enforcement) {
	t.Helper()

	if len(out.Findings) != 2 {
		t.Fatalf("findings = %d, want one violation in each environment: %+v", len(out.Findings), out.Findings)
	}

	for i := range out.Findings {
		f := &out.Findings[i]
		if f.Result != conformance.VerdictViolation || f.Enforcement != modes[f.Environment] {
			t.Errorf("finding in %s: result %s enforcement %q, want violation %q", f.Environment, f.Result, f.Enforcement, modes[f.Environment])
		}
	}
}

// assertObligationModes checks that the rule keeps its violation and each obligation has the mode of its environment.
func assertObligationModes(t *testing.T, rule *conformance.RuleResult, modes map[string]policy.Enforcement) {
	t.Helper()

	if rule.Verdict != conformance.VerdictViolation {
		t.Errorf("the rule keeps its verdict whatever the mode is: %s", rule.Verdict)
	}

	for i := range rule.Obligations {
		if got := rule.Obligations[i].Enforcement; got != modes[rule.Obligations[i].Environment] {
			t.Errorf("obligation in %s: enforcement %q, want %q", rule.Obligations[i].Environment, got, modes[rule.Obligations[i].Environment])
		}
	}
}

// MIG-03: a family that is mandatory in staging and advisory in production blocks on the violation in staging only.
func TestMIG03PerEnvironmentGate(t *testing.T) {
	t.Parallel()

	mandatory := func(env string) workspace.EnforcementEntry { return deployEntryOf(env, workspace.ModeMandatory) }
	advisory := func(env string) workspace.EnforcementEntry { return deployEntryOf(env, workspace.ModeAdvisory) }
	shadow := func(env string) workspace.EnforcementEntry { return deployEntryOf(env, workspace.ModeShadow) }

	for _, tc := range []struct { //nolint:govet // a test table.
		name    string
		entries []workspace.EnforcementEntry
		want    int
		modes   map[string]policy.Enforcement
	}{
		{
			"mandatory in staging, advisory in production: staging blocks",
			[]workspace.EnforcementEntry{mandatory("staging"), advisory("production")},
			diag.Violation,
			map[string]policy.Enforcement{"staging": policy.EnforcementMandatory, "production": policy.EnforcementAdvisory},
		},
		{
			"advisory in staging, mandatory in production: production blocks",
			[]workspace.EnforcementEntry{advisory("staging"), mandatory("production")},
			diag.Violation,
			map[string]policy.Enforcement{"staging": policy.EnforcementAdvisory, "production": policy.EnforcementMandatory},
		},
		{
			"advisory in both: the exit code stays 0",
			[]workspace.EnforcementEntry{advisory("")},
			diag.OK,
			map[string]policy.Enforcement{"staging": policy.EnforcementAdvisory, "production": policy.EnforcementAdvisory},
		},
		{
			"shadow in production, advisory in staging: the exit code stays 0",
			[]workspace.EnforcementEntry{shadow("production"), advisory("staging")},
			diag.OK,
			map[string]policy.Enforcement{"staging": policy.EnforcementAdvisory, "production": policy.EnforcementShadow},
		},
		{
			"no entry: a typed rule is advisory", nil, diag.OK,
			map[string]policy.Enforcement{"staging": policy.EnforcementAdvisory, "production": policy.EnforcementAdvisory},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := twoEnvironments(tc.entries...).evaluate()

			assertFindingModes(t, &out, tc.modes)
			assertObligationModes(t, typedRuleResult(t, &out, "T-1"), tc.modes)

			if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != tc.want {
				t.Fatalf("exit code = %d, want %d", got, tc.want)
			}
		})
	}
}

// partialInProduction is a rule with a mandatory environment and an advisory one, whose unknown is in production only
// (partial coverage there); staging passes.
func partialInProduction(mandatoryEnv string) *world {
	other := "production"
	if mandatoryEnv == "production" {
		other = "staging"
	}

	w := twoEnvironments(deployEntryOf(mandatoryEnv, workspace.ModeMandatory), deployEntryOf(other, workspace.ModeAdvisory))
	w.add(observation.ObservedNode{ObservedID: "cbm:p:img", Label: observation.LabelImage, QualifiedName: "registry/billing"})
	w.edges = []*observation.EvidenceEdge{fact(observation.KindUsesImage, "billing-stg", "img", func(e *observation.EvidenceEdge) { e.Environment = "staging" })}
	w.scope.Predicates = records(observation.CoverageComplete, observation.KindUsesImage)

	for i := range w.scope.Predicates {
		if rec := &w.scope.Predicates[i]; rec.Scope.Environment == "production" {
			rec.Status, rec.Reasons = observation.CoveragePartial, []string{"test: partial"}
		}
	}

	w.plans[0].EvidenceRequirements.OnUnknown = policy.UnknownFail

	return w
}

// The unknown is in production only (partial coverage there); staging passes. The exit code is 4 only when production is
// mandatory: an unknown obligation takes the mode of its own environment.
func TestAnUnknownObligationGatesOnlyInAMandatoryEnvironment(t *testing.T) {
	t.Parallel()

	for env, want := range map[string]int{"production": diag.Unknown, "staging": diag.OK} {
		out := partialInProduction(env).evaluate()

		if rule := typedRuleResult(t, &out, "T-1"); rule.Verdict != conformance.VerdictUnknown {
			t.Fatalf("mandatory in %s: verdict %s, want unknown", env, rule.Verdict)
		}

		if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != want {
			t.Errorf("mandatory only in %s: exit code %d, want %d", env, got, want)
		}
	}
}

// exitcodeRuleResultByID finds the result of the rule, or nil.
func exitcodeRuleResultByID(out *conformance.EvaluateOutput, id string) *conformance.RuleResult {
	for i := range out.RuleResults {
		if out.RuleResults[i].RuleID == id {
			return &out.RuleResults[i]
		}
	}

	return nil
}

func TestEveryRuleResultCarriesItsMode(t *testing.T) {
	t.Parallel()

	w := twoEnvironments()
	w.plans = append(w.plans, asBuilt())

	out := w.evaluate()

	for i := range out.RuleResults {
		if out.RuleResults[i].Enforcement == "" {
			t.Errorf("rule %s has no enforcement mode", out.RuleResults[i].RuleID)
		}
	}

	asBuiltResult := exitcodeRuleResultByID(&out, "R-1")
	if asBuiltResult == nil || asBuiltResult.Enforcement != policy.EnforcementMandatory {
		t.Fatalf("an as-built rule gates as mandatory: %+v", asBuiltResult)
	}
}

func TestRollbackMakesTheTypedRulesShadow(t *testing.T) {
	t.Parallel()

	w := twoEnvironments(workspace.EnforcementEntry{Family: "deploy", Mode: workspace.ModeMandatory})
	w.enforcement.Rollback = &conformance.RunRollback{Reason: "r", AuthorizedBy: "a", Expires: "2026-12-31"}

	out := w.evaluate()

	for i := range out.Findings {
		if out.Findings[i].Enforcement != policy.EnforcementShadow {
			t.Errorf("finding %+v is not shadow", out.Findings[i])
		}
	}

	if len(out.Findings) != 2 || conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError) != diag.OK {
		t.Fatalf("shadow findings are kept and do not gate: %+v", out.Findings)
	}
}
