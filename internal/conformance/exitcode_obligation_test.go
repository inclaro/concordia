package conformance_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
)

// obligationRule is a rule whose obligations have a verdict and the mode of their environment. The rule verdict is the
// worst of them, as the evaluation gives it.
func obligationRule(ruleID string, family policy.PredicateFamily, obligations ...conformance.ObligationResult) conformance.RuleResult {
	verdicts := make([]conformance.RuleVerdict, 0, len(obligations))
	for i := range obligations {
		verdicts = append(verdicts, obligations[i].Verdict)
	}

	return conformance.RuleResult{
		RuleID: ruleID, Family: family, Verdict: conformance.WorstVerdict(verdicts), Obligations: obligations,
		Plan: policy.RulePlan{RuleID: ruleID, EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: policy.UnknownFail}},
	}
}

func obligation(verdict conformance.RuleVerdict, env string, mode policy.Enforcement) conformance.ObligationResult {
	return conformance.ObligationResult{Verdict: verdict, Enforcement: mode, Obligation: conformance.Obligation{ID: env, Environment: env}}
}

func obligationViolation(env string, mode policy.Enforcement) conformance.ObligationResult {
	return obligation(conformance.VerdictViolation, env, mode)
}

func obligationUnknown(env string, mode policy.Enforcement) conformance.ObligationResult {
	return obligation(conformance.VerdictUnknown, env, mode)
}

// The mode of an obligation is the mode of its environment, so an advisory violation in one environment never hides
// a mandatory unknown in another, whatever the worst verdict of the rule is (typed rules and review rules alike).
func TestAnAdvisoryViolationDoesNotHideAMandatoryUnknown(t *testing.T) {
	t.Parallel()

	for _, family := range []policy.PredicateFamily{policy.FamilyTyped, policy.FamilyAssessmentCurrency} {
		for name, tc := range map[string]struct {
			obl  []conformance.ObligationResult
			want int
		}{
			"advisory violation and mandatory unknown exit 4": {[]conformance.ObligationResult{
				obligationViolation("staging", policy.EnforcementAdvisory), obligationUnknown("production", policy.EnforcementMandatory),
			}, diag.Unknown},
			"shadow violation and mandatory unknown exit 4": {[]conformance.ObligationResult{
				obligationViolation("staging", policy.EnforcementShadow), obligationUnknown("production", policy.EnforcementMandatory),
			}, diag.Unknown},
			"a mandatory unknown and an advisory unknown with an advisory violation is exit 4": {[]conformance.ObligationResult{
				obligationViolation("a", policy.EnforcementAdvisory), obligationUnknown("b", policy.EnforcementAdvisory), obligationUnknown("c", policy.EnforcementMandatory),
			}, diag.Unknown},
			"no obligation gates when every mode is advisory": {[]conformance.ObligationResult{
				obligationViolation("staging", policy.EnforcementAdvisory), obligationUnknown("production", policy.EnforcementAdvisory),
			}, diag.OK},
			"a mandatory unknown alone exits 4 as before": {[]conformance.ObligationResult{
				obligationUnknown("production", policy.EnforcementMandatory), obligation(conformance.VerdictPass, "staging", policy.EnforcementMandatory),
			}, diag.Unknown},
		} {
			t.Run(string(family)+"/"+name, func(t *testing.T) {
				t.Parallel()

				rule := obligationRule("R-1", family, tc.obl...)
				if got := conformance.ComputeExitCode([]conformance.RuleResult{rule}, nil, diag.SeverityError); got != tc.want {
					t.Fatalf("exit code = %d, want %d (rule verdict %s)", got, tc.want, rule.Verdict)
				}
			})
		}
	}
}

// A live mandatory violation finding still wins over a gating unknown: the precedence of ComputeExitCode is unchanged.
func TestAMandatoryViolationStillExitsBeforeAnUnknown(t *testing.T) {
	t.Parallel()

	rule := obligationRule("R-1", policy.FamilyAssessmentCurrency,
		obligation(conformance.VerdictViolation, "staging", policy.EnforcementMandatory),
		obligation(conformance.VerdictUnknown, "production", policy.EnforcementMandatory))
	finding := conformance.Finding{
		RuleID: "R-1", Severity: policy.SeverityError, Disposition: exceptions.DispositionActive, Enforcement: policy.EnforcementMandatory,
		Result: conformance.VerdictViolation,
	}

	if got := conformance.ComputeExitCode([]conformance.RuleResult{rule}, []conformance.Finding{finding}, diag.SeverityError); got != diag.Violation {
		t.Fatalf("exit code = %d, want 1", got)
	}
}
