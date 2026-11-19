package reporting_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

const reviewFile = "assessments/ASM-EDGE/v1.yaml"

// reviewRun is a run with one required assessment: its currency is the given one, the rule REV-APR-001 has the
// mode, and the finding (when the currency is not current) points at the assessment file.
func reviewRun(currency string, mode policy.Enforcement) *conformance.Run {
	run := testutil.SampleRun()
	run.Rules, run.Findings = nil, nil

	verdict, reason := conformance.VerdictViolation, ""

	switch currency {
	case "current":
		verdict = conformance.VerdictPass
	case "unknown":
		verdict, reason = conformance.VerdictUnknown, "trust-anchor-unreadable"
	}

	trigger := impact.ReviewTrigger{
		Class: impact.ClassExposure, Change: impact.ChangeModified, Dependency: "public-entry-point:k8s:ingress/api",
		Assumption: []string{"ASM-EDGE-1"}, Assessment: "ASM-EDGE@v1", Path: []string{"public-entry-point:k8s:ingress/api", "ASM-EDGE-1", "ASM-EDGE@v1"},
		Evidence: []string{}, Related: []impact.Related{}, Action: impact.ActionGetReview,
	}

	entry := conformance.AssessmentEntry{
		ID: "ASM-EDGE", Version: 1, Type: "security", Environment: "production", Outcome: "satisfactory", State: "approved", Currency: currency,
		UnknownReason: reason, Reasons: []string{}, Details: []string{}, Causes: []conformance.CurrencyCause{}, NotApplied: []conformance.CurrencyNotApplied{},
		Triggers: []impact.ReviewTrigger{}, Required: []string{"REQ-EDGE"}, File: reviewFile, Line: 1, Digest: "sha256:" + strings.Repeat("a", 64), Latest: true,
		Analysis: conformance.CurrencyAnalysis{Complete: currency != "unknown"},
	}

	if currency == "review-required" {
		entry.Reasons, entry.Triggers = []string{"subject digest mismatch"}, []impact.ReviewTrigger{trigger}
	}

	run.AssessmentCurrency = &conformance.AssessmentCurrencyReport{
		EvaluatedAt: "2026-06-01T00:00:00Z", ContractVersion: "review-digest/1", CapabilityProfile: "cbm-0.12.1",
		Assessments: []conformance.AssessmentEntry{entry},
		Required: []conformance.RequiredEntry{{
			ID: "REQ-EDGE", Assessment: "ASM-EDGE", Type: "security", Environment: "production", Authority: "sec", Version: 1,
			Currency: currency, UnknownReason: reason, Reasons: entry.Reasons, Verdict: verdict,
		}},
	}
	run.Governance = &conformance.GovernanceReport{Relaxations: []conformance.GovernanceChange{}, Dispositions: []conformance.DispositionEntry{}}
	run.Analysis = &conformance.AnalysisReport{Mode: conformance.AnalysisFull, Complete: currency != "unknown"}

	rule := conformance.RuleResult{
		RuleID: "REV-APR-001", Name: "A required assessment has a current verified approval", Severity: policy.SeverityError,
		Policy: policy.PackRef{ID: "review-currency", Version: "1.0.0"}, Family: policy.FamilyAssessmentCurrency, Verdict: verdict,
		Reasons: []conformance.Reason{}, Subjects: []string{"ASM-EDGE"}, Targets: []string{}, Enforcement: mode,
		Plan: policy.RulePlan{RuleID: "REV-APR-001", Family: string(policy.FamilyAssessmentCurrency), EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: policy.UnknownFail}},
	}

	run.Rules = []conformance.RuleResult{rule}

	if verdict != conformance.VerdictPass {
		message := "the required assessment REQ-EDGE needs review: " + currency + "; next action: get review"
		if currency == "review-required" {
			message = trigger.Text()
		}

		run.Findings = []conformance.Finding{{
			Fingerprint: "sha256:" + strings.Repeat("e", 64), RuleID: "REV-APR-001", Severity: policy.SeverityError, Kind: conformance.FindingKind("assessment-" + currency),
			Message: message, Source: conformance.Endpoint{ArchitectureID: "REQ-EDGE"}, Target: conformance.Endpoint{ArchitectureID: "ASM-EDGE"},
			EvidencePath: []conformance.EvidenceStep{}, RelatedLocations: []conformance.Location{}, Location: &conformance.Location{Path: reviewFile, Line: 1},
			Disposition: exceptions.DispositionActive, Result: verdict, Reason: conformance.UnknownReason(reason), NextAction: "get review",
			Enforcement: mode, Expected: "a current verified approval", Observed: currency, Area: conformance.AreaAssessmentCurrency,
			ProviderLimits: conformance.ProviderLimits{Families: []conformance.FamilyLimit{}},
			Related:        []conformance.RelatedRef{{Kind: "assessment", ID: "ASM-EDGE"}},
		}}
	}

	run.Summary = conformance.Summarize(run.Rules, run.Findings, diag.SeverityError, policy.UnknownFail)
	run.Areas = conformance.Areas{AssessmentCurrency: conformance.AreaResult{
		Verdict: conformance.AreaVerdict(verdict), Counts: map[string]int{"assessments": 1, "required": 1},
	}}
	run.SettleAreas()

	return run
}

// Scenario: OUT-03 area shown.
func TestAssessmentCurrencyAreaIsShownSeparately(t *testing.T) {
	t.Parallel()

	text := reporting.RenderPretty(reviewRun("review-required", policy.EnforcementMandatory), plain)
	contains(t, text, "Assessment currency: violation")
	contains(t, text, "ASM-EDGE v1 (security): review-required (subject digest mismatch)\n    outcome satisfactory; no approval; "+reviewFile+":1")
	contains(t, text, "next action: get review")
	contains(t, text, "Violation area(s): Assessment currency")
	notContains(t, text, "vulnerable")
	notContains(t, text, "compliant")
}

// Scenario: Unknown mandatory currency not hidden.
func TestUnknownMandatoryCurrencyIsNotAnAllClear(t *testing.T) {
	t.Parallel()

	run := reviewRun("unknown", policy.EnforcementMandatory)
	text := reporting.RenderPretty(run, reporting.PrettyOptions{Color: true})

	contains(t, text, "Unknown area(s): Assessment currency")
	contains(t, text, "unknown (trust-anchor-unreadable)")
	notContains(t, text, "\x1b[32m"+lastLine(text))
	equal(t, run.Summary.Verdict, conformance.VerdictUnknown)
	equal(t, run.Summary.ExitCode, diag.Unknown)
}

func TestAdvisoryReviewViolationIsNotGreen(t *testing.T) {
	t.Parallel()

	run := reviewRun("review-required", policy.EnforcementAdvisory)
	equal(t, run.Summary.ExitCode, diag.OK)

	text := reporting.RenderPretty(run, reporting.PrettyOptions{Color: true})
	contains(t, text, "(advisory: does not change the exit code)")
	notContains(t, text, "\x1b[32m"+lastLine(text))
}

func TestAShadowReviewRuleStaysOutOfThePrettyReport(t *testing.T) {
	t.Parallel()

	text := reporting.RenderPretty(reviewRun("review-required", policy.EnforcementShadow), plain)
	notContains(t, text, "FAIL REV-APR-001")
}

// Scenario: SARIF has one result for each blocking or unknown review obligation, located at the assessment file.
func TestReviewSARIFPointsAtTheAssessmentFile(t *testing.T) {
	t.Parallel()

	schema := sarifValidator(t)

	for _, currency := range []string{"review-required", "unknown"} {
		out, doc := renderSarif(t, reviewRun(currency, policy.EnforcementMandatory))
		assertValid(t, schema, out)

		results := doc.Runs[0].Results
		if len(results) != 1 {
			t.Fatalf("%s: results = %d, want 1", currency, len(results))
		}

		text := string(out)
		for _, want := range []string{reviewFile, "REV-APR-001", "get review", "assessmentCurrency"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: SARIF lacks %s", currency, want)
			}
		}

		if strings.Contains(text, "vulnerable") || strings.Contains(text, "compliant") {
			t.Errorf("%s: SARIF uses a verdict word", currency)
		}
	}

	out, _ := renderSarif(t, reviewRun("current", policy.EnforcementMandatory))
	if doc := string(out); strings.Contains(doc, `"partialFingerprints"`) {
		t.Error("a current assessment has a SARIF result")
	}
}

func TestReviewSARIFTriggerTextUsesGetReview(t *testing.T) {
	t.Parallel()

	out, _ := renderSarif(t, reviewRun("review-required", policy.EnforcementMandatory))

	if !strings.Contains(string(out), "is a reason to review ASM-EDGE@v1") || !strings.Contains(string(out), "next action: get review") {
		t.Errorf("SARIF = %s", out)
	}
}

// Scenario: JUnit has one case for each required assessment.
func TestReviewJUnitHasACaseForEachRequiredAssessment(t *testing.T) {
	t.Parallel()

	xml := reporting.RenderJUnit(reviewRun("review-required", policy.EnforcementMandatory))
	contains(t, xml, `<testsuite name="assessments/review-currency" tests="1" failures="1"`)
	contains(t, xml, `name="REQ-EDGE ASM-EDGE"`)
	contains(t, xml, `<failure type="violation" message="review-required">`)
	notContains(t, xml, `name="REV-APR-001`)

	pass := reporting.RenderJUnit(reviewRun("current", policy.EnforcementMandatory))
	contains(t, pass, `<testsuite name="assessments/review-currency" tests="1" failures="0"`)

	unknown := reporting.RenderJUnit(reviewRun("unknown", policy.EnforcementMandatory))
	contains(t, unknown, `<failure type="unknown" message="trust-anchor-unreadable">`)

	advisory := reporting.RenderJUnit(reviewRun("review-required", policy.EnforcementAdvisory))
	contains(t, advisory, `<testsuite name="assessments/review-currency" tests="1" failures="0"`)
	contains(t, advisory, "<system-out>")
	// The rule is shown (mandatory in another environment) but this entry's finding is shadow: the case is skipped,
	// never a pass.
	hidden := reviewRun("review-required", policy.EnforcementMandatory)
	hidden.Findings[0].Enforcement = policy.EnforcementShadow

	skipped := reporting.RenderJUnit(hidden)
	contains(t, skipped, `<skipped message="review-required (not shown in this enforcement mode)"/>`)
	contains(t, skipped, `skipped="1"`)
}

// Scenario: Review-required assessment in the area. A run without assessments shows `not-configured`.
func TestAssessmentCurrencyAreaInResults(t *testing.T) {
	t.Parallel()

	run := reviewRun("review-required", policy.EnforcementMandatory)

	type areaDoc struct {
		Counts  map[string]int `json:"counts"`
		Verdict string         `json:"verdict"`
	}

	type currencyDoc struct {
		Assessments []struct {
			ID       string `json:"id"`
			Currency string `json:"currency"`
		} `json:"assessments"`
	}

	var doc struct {
		Areas              map[string]areaDoc `json:"areas"`
		AssessmentCurrency currencyDoc        `json:"assessmentCurrency"`
	}

	data, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	area := doc.Areas["assessmentCurrency"]
	if area.Verdict == "pass" || area.Verdict != "violation" || area.Counts["assessments"] != 1 {
		t.Errorf("area = %+v", area)
	}

	if len(doc.AssessmentCurrency.Assessments) != 1 || doc.AssessmentCurrency.Assessments[0].Currency != "review-required" {
		t.Errorf("assessmentCurrency = %+v", doc.AssessmentCurrency)
	}

	none := testutil.SampleRun()
	none.SettleAreas()
	equal(t, none.Areas.AssessmentCurrency.Verdict, conformance.AreaNotConfigured)
}
