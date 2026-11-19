package adr_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

func enforcement(t *testing.T, class adr.Classifier, d *adr.Decision) adr.EnforcementResult {
	t.Helper()

	res := adr.EvaluateEnforcement([]*adr.Decision{d}, class)
	if len(res) != 1 {
		t.Fatalf("results = %+v", res)
	}

	return res[0]
}

// enforcementOutcome is the comparable shape of an adr.EnforcementResult that the scenarios assert on.
type enforcementOutcome struct {
	State   adr.EnforcementState
	Verdict conformance.RuleVerdict
	Reason  adr.Reason
	Rules   int
	Unit    bool
}

func outcomeOf(res *adr.EnforcementResult) enforcementOutcome {
	return enforcementOutcome{Unit: res.Unit, State: res.State, Verdict: res.Verdict, Reason: res.Reason, Rules: len(res.Rules)}
}

// Scenario: Required enforcement without a rule.
func TestEnforcementRequiredWithoutARuleIsAGap(t *testing.T) {
	t.Parallel()

	res := enforcement(t, mandatoryCore(), newDecision("ADR-3", requiring()))

	want := enforcementOutcome{Unit: true, State: adr.EnforcementUnlinked, Verdict: conformance.VerdictViolation, Reason: adr.ReasonEnforcementGap}
	if got := outcomeOf(&res); got != want {
		t.Errorf("result = %+v", res)
	}
}

func TestEnforcementRequiredWithOnlyAnAdvisoryRule(t *testing.T) {
	t.Parallel()

	res := enforcement(t, corePlans(policy.SeverityWarning, policy.UnknownFail), newDecision("ADR-3", requiring(), enforcedBy(coreRule)))

	got := outcomeOf(&res)
	got.Unit, got.Rules = false, 0

	want := enforcementOutcome{State: adr.EnforcementAdvisoryOnly, Verdict: conformance.VerdictViolation, Reason: adr.ReasonAdvisoryOnly}
	if got != want {
		t.Errorf("result = %+v", res)
	}
}

// Scenario: Required enforcement satisfied.
func TestEnforcementRequiredSatisfied(t *testing.T) {
	t.Parallel()

	res := enforcement(t, mandatoryCore(), newDecision("ADR-3", requiring(), enforcedBy(coreRule)))

	got := outcomeOf(&res)
	got.Unit = false

	want := enforcementOutcome{State: adr.EnforcementEnforced, Verdict: conformance.VerdictPass, Rules: 1}
	if got != want {
		t.Errorf("result = %+v", res)
	}
}

func TestEnforcementInactiveDecisionIsNotApplicable(t *testing.T) {
	t.Parallel()

	for name, d := range map[string]*adr.Decision{
		"superseded": newDecision("ADR-3", requiring(), withApplicability(adr.ApplicabilitySuperseded, "superseded")),
		"inactive":   newDecision("ADR-3", requiring(), withApplicability(adr.ApplicabilityInactive, "inactive")),
		"historical": newDecision("ADR-3", requiring(), historical()),
	} {
		res := enforcement(t, mandatoryCore(), d)

		got := outcomeOf(&res)
		got.Reason, got.Rules = "", 0

		want := enforcementOutcome{Unit: true, State: adr.EnforcementNotApplicable, Verdict: conformance.VerdictNotApplicable}
		if got != want {
			t.Errorf("%s: result = %+v", name, res)
		}
	}
}

// Scenario: Valid decision without enforcement.
func TestEnforcementNotRequiredIsNoUnit(t *testing.T) {
	t.Parallel()

	// Committed and active with no rule: the state is unlinked and nothing is judged.
	res := enforcement(t, mandatoryCore(), newDecision("ADR-3"))

	got := outcomeOf(&res)
	got.Reason, got.Rules = "", 0

	want := enforcementOutcome{State: adr.EnforcementUnlinked}
	if got != want {
		t.Errorf("result = %+v", res)
	}
}

func TestEnforcementUnresolvedActivityIsUnknown(t *testing.T) {
	t.Parallel()

	res := enforcement(t, mandatoryCore(), newDecision("ADR-3", requiring(), withApplicability(adr.ApplicabilityAmbiguous, "cycle")))
	if res.Verdict != conformance.VerdictUnknown {
		t.Errorf("result = %+v", res)
	}
}

func TestEnforcementRuleOutsideTheSelectionCountsAsMandatory(t *testing.T) {
	t.Parallel()

	res := enforcement(t, adr.Classifier{FailOn: "error"}, newDecision("ADR-3", requiring(), enforcedBy(coreRule)))
	if res.State != adr.EnforcementEnforced || res.Verdict != conformance.VerdictPass {
		t.Errorf("result = %+v", res)
	}
}
