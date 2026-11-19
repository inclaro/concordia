package adr_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

func justify(decisions []*adr.Decision, class adr.Classifier) []adr.JustificationResult {
	return adr.EvaluateJustification(decisions, class)
}

// justificationOutcome is the comparable shape of an adr.JustificationResult that the scenarios assert on.
type justificationOutcome struct {
	Verdict   conformance.RuleVerdict
	Rule      string
	LinkedBy  int
	Open      int
	Mandatory bool
}

func justificationOutcomeOf(res *adr.JustificationResult) justificationOutcome {
	return justificationOutcome{Verdict: res.Verdict, Mandatory: res.Mandatory, Rule: res.Rule.String(), LinkedBy: len(res.LinkedBy), Open: len(res.Open)}
}

// singleJustification returns the only result, failing the test otherwise.
func singleJustification(t *testing.T, res []adr.JustificationResult) *adr.JustificationResult {
	t.Helper()

	if len(res) != 1 {
		t.Fatalf("results = %+v", res)
	}

	return &res[0]
}

// Scenario: Proposed ADR is the only justification.
func TestJustificationProposedOnlyIsAViolation(t *testing.T) {
	t.Parallel()

	proposed := newDecision("ADR-3", withApplicability(adr.ApplicabilityInactive, "inactive"), enforcedBy(coreRule))

	res := singleJustification(t, justify([]*adr.Decision{proposed}, mandatoryCore()))

	got := justificationOutcomeOf(res)
	got.LinkedBy = 0

	want := justificationOutcome{Verdict: conformance.VerdictViolation, Mandatory: true, Rule: coreRule, Open: 1}
	if got != want || res.Open[0] != proposed {
		t.Fatalf("results = %+v", res)
	}
}

func TestJustificationOneActiveDecisionIsEnough(t *testing.T) {
	t.Parallel()

	deprecated := newDecision("ADR-2", withApplicability(adr.ApplicabilityInactive, "inactive"), enforcedBy(coreRule))
	active := newDecision("ADR-5", enforcedBy(coreRule))

	res := singleJustification(t, justify([]*adr.Decision{deprecated, active}, mandatoryCore()))

	got := justificationOutcome{Verdict: res.Verdict, LinkedBy: len(res.LinkedBy), Open: len(res.Open)}
	if got != (justificationOutcome{Verdict: conformance.VerdictPass, LinkedBy: 2}) {
		t.Fatalf("results = %+v", res)
	}
}

func TestJustificationAdvisoryRuleIsNotApplicable(t *testing.T) {
	t.Parallel()

	inactive := newDecision("ADR-3", withApplicability(adr.ApplicabilityInactive, "inactive"), enforcedBy(coreRule))

	for name, class := range map[string]adr.Classifier{
		"below failOn":     corePlans(policy.SeverityWarning, policy.UnknownFail),
		"onUnknown warn":   corePlans(policy.SeverityError, policy.UnknownWarn),
		"onUnknown ignore": corePlans(policy.SeverityError, policy.UnknownIgnore),
		"info with warn":   corePlans(policy.SeverityInfo, policy.UnknownWarn),
	} {
		res := justify([]*adr.Decision{inactive}, class)
		if len(res) != 1 {
			t.Fatalf("%s: results = %+v", name, res)
		}

		got := justificationOutcome{Verdict: res[0].Verdict, Mandatory: res[0].Mandatory}
		if got != (justificationOutcome{Verdict: conformance.VerdictNotApplicable}) {
			t.Errorf("%s: results = %+v", name, res)
		}
	}
}

// Scenario: Unlinked mandatory rule.
func TestJustificationUnlinkedOrUnselectedRuleHasNoResult(t *testing.T) {
	t.Parallel()

	// No decision links CORE-DB-001: no result, whatever the plans are.
	unlinked := newDecision("ADR-3", enforcedBy("CORE-DB-002@core/1.0.0"))
	if res := justify([]*adr.Decision{unlinked}, mandatoryCore()); len(res) != 0 {
		t.Errorf("a link to a rule without a selected plan gave %+v", res)
	}

	if res := justify([]*adr.Decision{newDecision("ADR-4")}, mandatoryCore()); len(res) != 0 {
		t.Errorf("a decision without links gave %+v", res)
	}
}

func TestJustificationUnknownWhenEveryOpenDecisionIsUnresolved(t *testing.T) {
	t.Parallel()

	ambiguous := newDecision("ADR-1", withApplicability(adr.ApplicabilityAmbiguous, "cycle"), enforcedBy(coreRule))
	unverifiable := newDecision("ADR-2", withApplicability(adr.ApplicabilityUnknown, "no-revision"), enforcedBy(coreRule))
	inactive := newDecision("ADR-3", withApplicability(adr.ApplicabilityInactive, "inactive"), enforcedBy(coreRule))

	if res := justify([]*adr.Decision{ambiguous, unverifiable}, mandatoryCore()); res[0].Verdict != conformance.VerdictUnknown {
		t.Errorf("ambiguous and unverifiable: %s, want unknown", res[0].Verdict)
	}

	if res := justify([]*adr.Decision{ambiguous, inactive}, mandatoryCore()); res[0].Verdict != conformance.VerdictViolation {
		t.Errorf("one definitely inactive decision: %s, want violation", res[0].Verdict)
	}
}

// Scenario: Links of a historical decision do not justify rules.
func TestJustificationLinksOfHistoricalDecisionsNeverJustify(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-3", historical(), enforcedBy(coreRule))
	if res := justify([]*adr.Decision{old}, mandatoryCore()); res[0].Verdict != conformance.VerdictViolation {
		t.Errorf("historical only: %s, want violation", res[0].Verdict)
	}

	// An ambiguous historical decision is still no justification: violation, not unknown.
	amb := newDecision("ADR-4", historical(), withApplicability(adr.ApplicabilityAmbiguous, "cycle"), enforcedBy(coreRule))
	if res := justify([]*adr.Decision{amb}, mandatoryCore()); res[0].Verdict != conformance.VerdictViolation {
		t.Errorf("ambiguous historical: %s, want violation", res[0].Verdict)
	}

	// The active successor that declares the rule justifies it.
	active := newDecision("ADR-7", enforcedBy(coreRule))
	if res := justify([]*adr.Decision{old, active}, mandatoryCore()); res[0].Verdict != conformance.VerdictPass {
		t.Errorf("successor registered: %s, want pass", res[0].Verdict)
	}
}

func TestJustificationResultsAreSortedByRuleRef(t *testing.T) {
	t.Parallel()

	class := adr.Classifier{Plans: []policy.RulePlan{
		plan("CORE-DB-001", "core", policy.SeverityError, policy.UnknownFail), plan("CORE-API-002", "core", policy.SeverityError, policy.UnknownFail),
	}, FailOn: "error"}

	res := justify([]*adr.Decision{newDecision("ADR-1", enforcedBy(coreRule, "CORE-API-002@core/1.0.0"))}, class)

	got := make([]string, len(res))
	for i := range res {
		got[i] = res[i].Rule.Rule
	}

	if !slices.Equal(got, []string{"CORE-API-002", "CORE-DB-001"}) {
		t.Errorf("results = %+v", res)
	}
}
