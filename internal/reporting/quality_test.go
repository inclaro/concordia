package reporting_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

// qualityRun is a run whose conformance passes and whose quality evaluation has one scenario with the given verdict
// and enforcement, a required area that no scenario covers, and an open tradeoff.
func qualityRun(verdict conformance.RuleVerdict, enforcement policy.Enforcement) *conformance.Run {
	run := testutil.SampleRun()
	run.Rules, run.Findings = nil, nil

	reason := conformance.UnknownReason("")
	if verdict == conformance.VerdictUnknown {
		reason = conformance.ReasonNoEvidenceSupplied
	}

	onUnknown := policy.UnknownWarn
	if enforcement == policy.EnforcementMandatory {
		onUnknown = policy.UnknownFail
	}

	run.Rules = []conformance.RuleResult{
		{
			RuleID: "CO-BND-001", Name: "Billing boundary", Severity: policy.SeverityError, Family: policy.FamilyBoundary, Verdict: conformance.VerdictPass,
			Reasons: []conformance.Reason{}, Subjects: []string{}, Targets: []string{}, Enforcement: policy.EnforcementMandatory,
		},
		{
			RuleID: "quality:QS-FAILOVER", Name: "Quality scenario QS-FAILOVER", Severity: policy.SeverityWarning, Family: policy.FamilyAssuranceClaim, Verdict: verdict,
			Reasons: []conformance.Reason{}, Subjects: []string{}, Targets: []string{}, Enforcement: enforcement,
			Plan: policy.RulePlan{RuleID: "quality:QS-FAILOVER", EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown}},
		},
	}

	run.Assurance = &conformance.AssuranceReport{
		Quality: []conformance.QualityEntry{{
			Scenario: "QS-FAILOVER", Area: "availability", Mode: "measured", Environment: "production", Enforcement: string(enforcement), Verdict: verdict,
			Reason: reason, Details: []string{}, Evidence: []conformance.QualityEvidence{}, RuleID: "quality:QS-FAILOVER",
			Links: []conformance.QualityLink{{ADR: "ADR-7", Canonical: "ADR-7", Status: "not-active", State: "superseded", ReviewRationale: true}},
		}},
		QualityAreas: []conformance.QualityAreaEntry{
			{
				Area: "availability", Verdict: string(verdict), Scenarios: []conformance.QualityAreaScenario{{ID: "QS-FAILOVER", Mode: "measured", Verdict: verdict}},
				Findings: []conformance.QualityFinding{{ID: "F-REPLICA", Type: "tradeoff", Status: "open", Statement: "Replicas lower modifiability."}},
			},
			{Area: "operability", Verdict: "violation", Required: true},
			{Area: "security", Verdict: "no-scenario"},
		},
	}

	run.Summary = conformance.Summarize(run.Rules, run.Findings, diag.SeverityError, policy.UnknownFail)
	run.Areas = conformance.Areas{}
	run.SettleAreas()

	return run
}

// Scenario: Area in terminal output. A run with scenarios shows the quality evaluation area with its own verdict, the
// mode of each scenario, the coverage of the areas, and the tradeoff that is still open.
func TestQualityEvaluationAreaIsShownSeparately(t *testing.T) {
	t.Parallel()

	run := qualityRun(conformance.VerdictUnknown, policy.EnforcementMandatory)
	text := reporting.RenderPretty(run, plain)

	contains(t, text, "Conformance: pass")
	contains(t, text, "Quality evaluation: unknown")
	contains(t, text, "QS-FAILOVER (measured, mandatory, availability): unknown (no-evidence-supplied)")
	contains(t, text, "decision ADR-7 is not active (superseded); review the rationale")
	contains(t, text, "Areas with a scenario: availability unknown\n  Areas without a scenario: operability (required: violation), security")
	contains(t, text, "Open tradeoff F-REPLICA next to availability: Replicas lower modifiability.")
	contains(t, text, "Unknown area(s): Quality evaluation")
	contains(t, text, "UNKNOWN quality:QS-FAILOVER")
}

// Scenario: Unknown mandatory scenario with green conformance. The summary line is not the green all-clear even when
// the exit code is 0 (an advisory scenario), because an area is unknown.
func TestAnUnknownScenarioIsNotAnAllClear(t *testing.T) {
	t.Parallel()

	run := qualityRun(conformance.VerdictUnknown, policy.EnforcementAdvisory)
	text := reporting.RenderPretty(run, reporting.PrettyOptions{Color: true})

	equal(t, run.Summary.ExitCode, diag.OK)
	equal(t, run.Summary.Verdict, conformance.VerdictUnknown)
	notContains(t, text, "\x1b[32m"+lastLine(text))
}

// A violation of the quality area is named before the summary line.
func TestAViolatedQualityAreaIsNamed(t *testing.T) {
	t.Parallel()

	text := reporting.RenderPretty(qualityRun(conformance.VerdictViolation, policy.EnforcementAdvisory), plain)

	contains(t, text, "Quality evaluation: violation")
	contains(t, text, "Violation area(s): Quality evaluation")
}

// A run with no scenario names the area as not configured and prints no section for it.
func TestQualityEvaluationIsNotConfiguredWithoutScenarios(t *testing.T) {
	t.Parallel()

	text := reporting.RenderPretty(testutil.SampleRun(), plain)

	if strings.Contains(text, "Quality evaluation:") {
		t.Errorf("a section for an area that is not configured:\n%s", text)
	}

	contains(t, text, "Not configured:")
	contains(t, text, "Quality evaluation")
}
