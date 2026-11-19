package conformance_test

import (
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// dispose applies the waivers to the findings of out at the time, as the pipeline does after verification, and gives
// the diagnostics of the step.
func dispose(out *conformance.EvaluateOutput, waivers []exceptions.Waiver, now time.Time) []diag.Diagnostic {
	keys := make([]exceptions.Key, len(out.Findings))
	for i := range out.Findings {
		f := &out.Findings[i]
		keys[i] = exceptions.Key{Fingerprint: f.Fingerprint, RuleID: f.RuleID, Subject: f.Subject, Environment: f.Environment, ObligationID: f.ObligationID}
	}

	result := exceptions.ApplyDispositions(keys, nil, waivers, now, nil)

	for i := range out.Findings {
		out.Findings[i].Disposition = result.Assignments[i].Disposition
		if ref := result.Assignments[i].Ref; ref != "" {
			out.Findings[i].DispositionRef = &result.Assignments[i].Ref
		}
	}

	return result.Diagnostics
}

// waiversFor gives one waiver of each shape for the finding: by fingerprint (WV-1) and by scope (WV-2).
func waiversFor(f *conformance.Finding, expires string) []exceptions.Waiver {
	return []exceptions.Waiver{
		{ID: "WV-1", Finding: f.Fingerprint, Rule: f.RuleID, Owner: "o", Reason: "r", Expires: expires},
		{
			ID: "WV-2", Rule: f.RuleID, Owner: "o", Reason: "r", Expires: expires,
			Scope:    &exceptions.Scope{Subject: f.Subject, Environment: f.Environment, Obligation: f.ObligationID},
			Approver: &exceptions.Approver{Identity: "alice@example.com", Authority: "exception-board"},
		},
	}
}

// waiverShapes picks one of the two waivers of waiversFor.
var waiverShapes = map[string]func([]exceptions.Waiver) []exceptions.Waiver{
	"finding waiver": func(w []exceptions.Waiver) []exceptions.Waiver { return w[:1] },
	"scoped waiver":  func(w []exceptions.Waiver) []exceptions.Waiver { return w[1:] },
}

// Scenario: Waived violation keeps its verdict.
func TestWaivedViolationStaysAViolation(t *testing.T) {
	t.Parallel()

	out := mixed(policy.UnknownWarn).evaluate()

	if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != diag.Violation {
		t.Fatalf("the live violation gives exit %d, want 1", got)
	}

	for i := range out.Findings {
		if out.Findings[i].Result == conformance.VerdictViolation {
			out.Findings[i].Disposition = exceptions.DispositionWaived
		}
	}

	summary := conformance.Summarize(out.RuleResults, out.Findings, diag.SeverityError, policy.UnknownWarn)

	if summary.Verdict != conformance.VerdictViolation || summary.Rules.Violation != 1 {
		t.Fatalf("waived is not pass: summary = %+v", summary)
	}

	if summary.Waived != 1 || summary.ExitCode != diag.OK {
		t.Fatalf("the disposition is waived and the gate opens: %+v", summary)
	}
}

// waiverRun is the run of the mixed rule with a waiver for its violation, disposed at one time.
type waiverRun struct {
	findings []conformance.Finding
	diags    []diag.Diagnostic
	summary  conformance.RunSummary
}

// lastViolation is the last finding that is a violation, or nil.
func lastViolation(findings []conformance.Finding) *conformance.Finding {
	var violation *conformance.Finding

	for i := range findings {
		if findings[i].Result == conformance.VerdictViolation {
			violation = &findings[i]
		}
	}

	return violation
}

// runWaivedViolation disposes the violation of the mixed rule with the waiver of the shape at the time.
func runWaivedViolation(t *testing.T, pick func([]exceptions.Waiver) []exceptions.Waiver, now time.Time) waiverRun {
	t.Helper()

	out := mixed(policy.UnknownWarn).evaluate()

	violation := lastViolation(out.Findings)
	if violation == nil {
		t.Fatal("no violation finding")
	}

	diags := dispose(&out, pick(waiversFor(violation, "2026-12-31")), now)

	return waiverRun{
		summary:  conformance.Summarize(out.RuleResults, out.Findings, diag.SeverityError, policy.UnknownWarn),
		findings: out.Findings,
		diags:    diags,
	}
}

// violationDisposition is the disposition of the violation finding, or empty without one.
func violationDisposition(findings []conformance.Finding) exceptions.Disposition {
	for i := range findings {
		if findings[i].Result == conformance.VerdictViolation {
			return findings[i].Disposition
		}
	}

	return ""
}

// verdictsAreUnchanged reports whether the waived and the expired run are a violation with the same rule counts.
func verdictsAreUnchanged(waived, expired *waiverRun) bool {
	return waived.summary.Verdict == conformance.VerdictViolation &&
		expired.summary.Verdict == waived.summary.Verdict &&
		waived.summary.Rules == expired.summary.Rules
}

// assertExpiryDispositions checks the disposition of the violation and the diagnostics before and after the expiry.
func assertExpiryDispositions(t *testing.T, waived, expired *waiverRun) {
	t.Helper()

	if got := violationDisposition(waived.findings); got != exceptions.DispositionWaived {
		t.Errorf("before the expiry: disposition %q, want waived", got)
	}

	if got := violationDisposition(expired.findings); got != exceptions.DispositionExpiredWaiver {
		t.Errorf("after the expiry: disposition %q, want expired-waiver", got)
	}

	if len(waived.diags) != 0 {
		t.Errorf("before the expiry: diagnostics %v", waived.diags)
	}

	if len(expired.diags) != 1 || expired.diags[0].Code != diag.EXC006 {
		t.Errorf("after the expiry: diagnostics %v, want CO-EXC-006", expired.diags)
	}
}

// Scenario: A29 valid waiver expires.
// Scenario: A29 gate is waived only while the waiver applies.
func TestA29WaiverExpiryChangesTheDispositionAndNotTheVerdict(t *testing.T) {
	t.Parallel()

	before := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	after := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	for shape, pick := range waiverShapes {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()

			waived := runWaivedViolation(t, pick, before)
			expired := runWaivedViolation(t, pick, after)

			if !verdictsAreUnchanged(&waived, &expired) {
				t.Fatalf("the verdict does not change: before %+v, after %+v", waived.summary, expired.summary)
			}

			assertExpiryDispositions(t, &waived, &expired)

			if waived.summary.ExitCode != diag.OK || expired.summary.ExitCode != diag.Violation {
				t.Errorf("the run blocks only after the expiry: exit %d before, %d after", waived.summary.ExitCode, expired.summary.ExitCode)
			}
		})
	}
}

// unknownObligationWorld is a mandatory rule with an unknown obligation (partial coverage) that fails the run.
func unknownObligationWorld() *world {
	w := newWorld(observation.KindUsesImage)
	rule := typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)
	rule.Enforcement = policy.EnforcementMandatory
	rule.EvidenceRequirements.OnUnknown = policy.UnknownFail
	w.plans = []policy.RulePlan{rule}
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)

	return w
}

// Scenario: Waived unknown obligation still exits 4.
func TestAWaivedUnknownObligationStillExitsFour(t *testing.T) {
	t.Parallel()

	for shape, pick := range waiverShapes {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()

			out := unknownObligationWorld().evaluate()

			if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != diag.Unknown {
				t.Fatalf("without a waiver the exit code is %d, want 4", got)
			}

			if len(out.Findings) == 0 {
				t.Fatalf("the unknown obligation has no finding to waive: %+v", out)
			}

			dispose(&out, pick(waiversFor(&out.Findings[0], "2999-12-31")), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

			if out.Findings[0].Disposition != exceptions.DispositionWaived {
				t.Fatalf("disposition %q, want waived", out.Findings[0].Disposition)
			}

			if got := typedRuleResult(t, &out, "T-1").Verdict; got != conformance.VerdictUnknown {
				t.Errorf("the verdict stays unknown, got %s", got)
			}

			if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != diag.Unknown {
				t.Errorf("a waiver never removes exit 4: exit %d", got)
			}
		})
	}
}

// isWaivedViolation reports whether the finding has the disposition waived and the verdict violation.
func isWaivedViolation(f *conformance.Finding) bool {
	return f.Disposition == exceptions.DispositionWaived && f.Result == conformance.VerdictViolation
}

// isUnblockedViolation reports whether the run is a violation that does not block, with one waived finding.
func isUnblockedViolation(summary *conformance.RunSummary) bool {
	return summary.Verdict == conformance.VerdictViolation && summary.ExitCode == diag.OK && summary.Waived == 1
}

// Scenario: Review obligation waived.
// A waiver changes the disposition of a review finding, never its verdict, and the waived finding does not block. That
// the currency stays review-required under a waiver is shown on the real pipeline by
// test/e2e TestReviewScopedWaiverRealPipeline.
func TestAWaivedReviewObligationKeepsItsVerdict(t *testing.T) {
	t.Parallel()

	finding := conformance.Finding{
		Fingerprint: "sha256:" + strings.Repeat("a", 64), RuleID: "REV-APR-001", Severity: policy.SeverityError,
		Kind: "review-approval-missing", Result: conformance.VerdictViolation, Disposition: exceptions.DispositionActive,
		Enforcement: policy.EnforcementMandatory, Subject: "ASM-EDGE", Environment: "production", ObligationID: "0123456789abcdef",
		Area: conformance.AreaAssessmentCurrency,
	}
	rules := []conformance.RuleResult{{RuleID: "REV-APR-001", Verdict: conformance.VerdictViolation, Enforcement: policy.EnforcementMandatory}}

	out := conformance.EvaluateOutput{RuleResults: rules, Findings: []conformance.Finding{finding}}

	if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != diag.Violation {
		t.Fatalf("the live violation gives exit %d, want 1", got)
	}

	dispose(&out, waiversFor(&finding, "2999-12-31")[1:], time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	got := out.Findings[0]
	if !isWaivedViolation(&got) {
		t.Fatalf("disposition %q, result %q: want waived with the verdict violation", got.Disposition, got.Result)
	}

	summary := conformance.Summarize(out.RuleResults, out.Findings, diag.SeverityError, policy.UnknownFail)
	if !isUnblockedViolation(&summary) {
		t.Errorf("summary = %+v: the verdict stays violation and the waived finding does not block", summary)
	}
}
