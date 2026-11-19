package conformance_test

import (
	"maps"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// mixed is a mandatory rule with three obligations: billing passes, ledger is unknown (ambiguous binding), ns-prod is a
// violation (absent).
func mixed(onUnknown policy.UnknownDisposition) *world {
	w := newWorld(observation.KindUsesImage)
	w.add(resource("billing-a", "Deployment", "production", "billing-a"), "billing")
	w.add(resource("ledger-a", "Deployment", "production", "ledger-a"), "ledger")
	w.add(observation.ObservedNode{ObservedID: "cbm:p:img", Label: observation.LabelImage, QualifiedName: "registry/billing"})
	w.set.Ambiguous = map[string][]string{"cbm:p:ledger-a": {"b-1", "b-2"}}
	w.edges = []*observation.EvidenceEdge{fact(observation.KindUsesImage, "billing-a", "img", func(e *observation.EvidenceEdge) { e.Environment = "production" })}

	rule := typedRule("T-1", []string{"billing", "ledger", "ns-prod"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)
	rule.Enforcement = policy.EnforcementMandatory
	rule.EvidenceRequirements.OnUnknown = onUnknown
	w.plans = []policy.RulePlan{rule}

	return w
}

// exitcodeSubjectVerdicts gives the verdict of each obligation of the rule by subject.
func exitcodeSubjectVerdicts(rule *conformance.RuleResult) map[string]conformance.RuleVerdict {
	got := map[string]conformance.RuleVerdict{}
	for i := range rule.Obligations {
		got[rule.Obligations[i].Subject] = rule.Obligations[i].Verdict
	}

	return got
}

// exitcodeKeepsOnlyReason reports whether the rule carries exactly one reason, with the code.
func exitcodeKeepsOnlyReason(rule *conformance.RuleResult, code conformance.UnknownReason) bool {
	return len(rule.Reasons) == 1 && rule.Reasons[0].Code == string(code)
}

func TestAggregateHidesNothing(t *testing.T) {
	t.Parallel()

	out := mixed(policy.UnknownFail).evaluate()
	rule := typedRuleResult(t, &out, "T-1")

	if rule.Verdict != conformance.VerdictViolation {
		t.Fatalf("aggregate = %s, want violation (error > violation > unknown > pass)", rule.Verdict)
	}

	got := exitcodeSubjectVerdicts(rule)
	want := map[string]conformance.RuleVerdict{"billing": conformance.VerdictPass, "ledger": conformance.VerdictUnknown, "ns-prod": conformance.VerdictViolation}

	if !maps.Equal(got, want) {
		t.Fatalf("individual results = %v, want %v", got, want)
	}

	if !exitcodeKeepsOnlyReason(rule, conformance.ReasonAmbiguousBinding) {
		t.Fatalf("the unknown reason stays visible: %+v", rule.Reasons)
	}
}

func TestAggregateOrder(t *testing.T) {
	t.Parallel()

	order := []conformance.RuleVerdict{
		conformance.VerdictError, conformance.VerdictViolation, conformance.VerdictUnknown, conformance.VerdictPass, conformance.VerdictNotApplicable,
	}

	for i, worse := range order {
		for _, better := range order[i+1:] {
			if got := conformance.WorstVerdict([]conformance.RuleVerdict{better, worse, better}); got != worse {
				t.Errorf("WorstVerdict(%s, %s) = %s, want %s", better, worse, got, worse)
			}
		}
	}

	if got := conformance.WorstVerdict([]conformance.RuleVerdict{conformance.VerdictNotApplicable, conformance.VerdictNotApplicable}); got != conformance.VerdictNotApplicable {
		t.Fatalf("all not-applicable = %s", got)
	}

	if got := conformance.WorstVerdict([]conformance.RuleVerdict{conformance.VerdictNotApplicable, conformance.VerdictPass}); got != conformance.VerdictPass {
		t.Fatalf("pass with not-applicable = %s", got)
	}
}

func TestAllNotApplicableAggregatesToNotApplicable(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing", "ledger"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload,
		func(s *policy.ObligationSpec) {
			s.Applies = policy.MatchProperties(policy.DimensionArchitecture, map[string][]string{policy.PropertyID: {"nobody"}})
		})}

	out := w.evaluate()
	rule := typedRuleResult(t, &out, "T-1")

	if rule.Verdict != conformance.VerdictNotApplicable || len(rule.Obligations) != 2 {
		t.Fatalf("verdict %s obligations %d, want not-applicable with both results visible", rule.Verdict, len(rule.Obligations))
	}

	for i := range rule.Obligations {
		if rule.Obligations[i].Verdict != conformance.VerdictNotApplicable {
			t.Fatalf("obligation = %+v", rule.Obligations[i])
		}
	}

	if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != diag.OK {
		t.Fatalf("exit %d", got)
	}
}

func TestUnresolvedScopeIsNeverNotApplicable(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload,
		func(s *policy.ObligationSpec) { s.Scope.Environments = []string{"nowhere"} })}

	out := w.evaluate()
	if rule := typedRuleResult(t, &out, "T-1"); rule.Verdict != conformance.VerdictUnknown {
		t.Fatalf("verdict %s, want unknown", rule.Verdict)
	}
}

// exitcodeViolationOnly is a typed rule of the given mode whose only obligation is a violation.
func exitcodeViolationOnly(enforcement policy.Enforcement) *world {
	w := newWorld(observation.KindUsesImage)
	rule := typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)
	rule.Enforcement = enforcement
	w.plans = []policy.RulePlan{rule}

	return w
}

// exitcodeUnknownOnly is a typed rule of the given mode whose only obligation is unknown, for partial coverage.
func exitcodeUnknownOnly(enforcement policy.Enforcement, onUnknown policy.UnknownDisposition) *world {
	w := exitcodeViolationOnly(enforcement)
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
	w.plans[0].EvidenceRequirements.OnUnknown = onUnknown

	return w
}

func TestExitCodesOfTypedRules(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct { //nolint:govet // a test table.
		name  string
		world *world
		want  int
	}{
		{"mandatory violation blocks", exitcodeViolationOnly(policy.EnforcementMandatory), diag.Violation},
		{"advisory violation keeps the verdict and does not gate", exitcodeViolationOnly(policy.EnforcementAdvisory), diag.OK},
		{"shadow violation does not gate", exitcodeViolationOnly(policy.EnforcementShadow), diag.OK},
		{"mandatory unknown with onUnknown fail is exit 4", exitcodeUnknownOnly(policy.EnforcementMandatory, policy.UnknownFail), diag.Unknown},
		{"mandatory unknown with onUnknown warn does not block", exitcodeUnknownOnly(policy.EnforcementMandatory, policy.UnknownWarn), diag.OK},
		{"advisory unknown does not gate", exitcodeUnknownOnly(policy.EnforcementAdvisory, policy.UnknownFail), diag.OK},
		{"a live violation outranks an unknown", mixed(policy.UnknownFail), diag.Violation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := tc.world.evaluate()
			rule := typedRuleResult(t, &out, "T-1")

			if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != tc.want {
				t.Fatalf("exit code = %d, want %d (rule %s)", got, tc.want, rule.Verdict)
			}
		})
	}
}

// exitcodeIsAdvisoryViolation reports whether the run summary is a violation that does not gate, with one finding.
func exitcodeIsAdvisoryViolation(summary *conformance.RunSummary, findings int) bool {
	return summary.Verdict == conformance.VerdictViolation && summary.ExitCode == diag.OK && findings == 1
}

func TestAdvisoryResultsKeepTheirVerdicts(t *testing.T) {
	t.Parallel()

	out := exitcodeViolationOnly(policy.EnforcementAdvisory).evaluate()
	summary := conformance.Summarize(out.RuleResults, out.Findings, diag.SeverityError, policy.UnknownFail)

	if !exitcodeIsAdvisoryViolation(&summary, len(out.Findings)) {
		t.Fatalf("an advisory violation is a violation that does not gate: %+v", summary)
	}
}

func TestErrorVerdictGatesOnlyWhenMandatory(t *testing.T) {
	t.Parallel()

	for enforcement, want := range map[policy.Enforcement]int{policy.EnforcementMandatory: diag.Internal, policy.EnforcementAdvisory: diag.OK, "": diag.Internal} {
		rules := []conformance.RuleResult{{RuleID: "R", Verdict: conformance.VerdictError, Enforcement: enforcement}}
		if got := conformance.ComputeExitCode(rules, nil, diag.SeverityError); got != want {
			t.Errorf("enforcement %q: exit %d, want %d", enforcement, got, want)
		}
	}
}

func TestBindingGapFindingNeverFailsTheRunByItself(t *testing.T) {
	t.Parallel()

	finding := conformance.Finding{
		Severity: policy.SeverityError, Kind: conformance.KindBindingGap, Result: conformance.VerdictUnknown,
		Disposition: exceptions.DispositionActive, Enforcement: policy.EnforcementMandatory,
	}

	if got := conformance.ComputeExitCode(nil, []conformance.Finding{finding}, diag.SeverityError); got != diag.OK {
		t.Fatalf("exit %d: an unknown binding gap gates through the rule verdict, not as a violation", got)
	}
}
