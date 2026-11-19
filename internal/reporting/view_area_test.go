package reporting_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

const (
	testViewID       = "context-landscape"
	testViewSource   = "docs/architecture/context.dsl"
	testViewArtifact = "docs/architecture/context.puml"
)

// viewRuleResult is the result of one of the three built-in rules of the views pack.
func viewRuleResult(ruleID, name string, verdict conformance.RuleVerdict, onUnknown policy.UnknownDisposition) conformance.RuleResult {
	rule := ruleWith(verdict, onUnknown)
	rule.RuleID, rule.Name, rule.Family = ruleID, name, policy.FamilyView
	rule.Policy = policy.PackRef{ID: "views", Version: "1.0.0"}

	return rule
}

// viewFindingSpec says what a view finding is about: the rule that raised it, its kind and result, the file that it is
// located at, its next action, and the view.
type viewFindingSpec struct {
	kind       conformance.FindingKind
	result     conformance.RuleVerdict
	ruleID     string
	location   string
	nextAction string
	viewID     string
}

// viewFinding is a finding of a view rule: the view is its source and its related entry.
func viewFinding(spec *viewFindingSpec) conformance.Finding {
	finding := testutil.SampleRun().Findings[0]
	finding.RuleID, finding.Kind, finding.Result, finding.NextAction = spec.ruleID, spec.kind, spec.result, spec.nextAction
	finding.Message = "view " + spec.viewID + ": " + string(spec.kind)
	finding.Fingerprint = "sha256:" + strings.Repeat("9", 64)
	finding.Area = conformance.AreaDiagramCurrency
	finding.Related = []conformance.RelatedRef{{Kind: "view", ID: spec.viewID}}
	finding.Source = conformance.Endpoint{ArchitectureID: spec.viewID}
	finding.Target = conformance.Endpoint{}
	finding.Location = &conformance.Location{Path: spec.location}
	finding.EvidencePath = []conformance.EvidenceStep{}
	finding.RelatedLocations = []conformance.Location{}
	finding.Edge = nil
	finding.Disposition, finding.DispositionRef = exceptions.DispositionActive, nil
	finding.Severity = policy.SeverityError

	return finding
}

// viewChecks are the verdicts of the three checks of a view.
type viewChecks struct {
	presence, freshness, correspondence conformance.RuleVerdict
}

// testView is a generated view whose checks have the verdicts.
func testView(id string, checks viewChecks, nextAction string) conformance.ViewResult {
	return conformance.ViewResult{
		ID: id, Kind: "context", Origin: "generated", Severity: "error", NextAction: nextAction,
		Presence:       conformance.ViewPresence{Status: checks.presence, Reasons: []string{}, Files: []conformance.ViewPresenceFile{}},
		Freshness:      conformance.ViewFreshness{Status: checks.freshness, Reasons: []string{}},
		Correspondence: conformance.ViewCorrespondence{Status: checks.correspondence, Method: "generated-source", Reasons: []string{}},
		Assessment:     conformance.ViewAssessmentCurrency{State: conformance.AssessmentCurrencyNotEvaluated},
		Scope:          []string{},
	}
}

// viewRun is a run whose conformance rules all pass, with the views, the three view rules, and the view findings.
// The view rules take the worst verdict of the view checks, as the pipeline gives them.
func viewRun(views []conformance.ViewResult, findings []conformance.Finding) *conformance.Run {
	run := unknownAreaRun()
	run.Areas = conformance.Areas{}
	run.Views = views

	for _, check := range []struct {
		verdict  func(*conformance.ViewResult) conformance.RuleVerdict
		id, name string
	}{
		{func(v *conformance.ViewResult) conformance.RuleVerdict { return v.Presence.Status }, "VIEW-PRES-001", "View is present"},
		{func(v *conformance.ViewResult) conformance.RuleVerdict { return v.Freshness.Status }, "VIEW-FRESH-001", "View is fresh"},
		{func(v *conformance.ViewResult) conformance.RuleVerdict { return v.Correspondence.Status }, "VIEW-CORR-001", "View corresponds to the architecture"},
	} {
		verdicts := make([]conformance.RuleVerdict, 0, len(views))
		for i := range views {
			verdicts = append(verdicts, check.verdict(&views[i]))
		}

		rule := viewRuleResult(check.id, check.name, conformance.WorstVerdict(verdicts), policy.UnknownFail)
		rule.FindingCount = len(findings)
		run.Rules = append(run.Rules, rule)
	}

	run.Findings = findings
	run.Areas.DiagramCurrency = conformance.AreaOfViews(views, findings)
	run.Summary = conformance.Summarize(run.Rules, run.Findings, diag.SeverityError, policy.UnknownFail)
	run.SettleAreas()

	return run
}

// unknownCorrespondenceRun has one view with correspondence `unknown` and presence and freshness `pass`.
func unknownCorrespondenceRun() *conformance.Run {
	view := testView(testViewID, viewChecks{conformance.VerdictPass, conformance.VerdictPass, conformance.VerdictUnknown}, "supply evidence")
	finding := viewFinding(&viewFindingSpec{
		ruleID: "VIEW-CORR-001", kind: "view-drift", result: conformance.VerdictUnknown, location: testViewSource,
		nextAction: "supply evidence", viewID: testViewID,
	})

	run := viewRun([]conformance.ViewResult{view}, []conformance.Finding{finding})

	for i := range run.Rules {
		if run.Rules[i].RuleID == "VIEW-CORR-001" {
			run.Rules[i].Reasons = []conformance.Reason{{Code: "CO-DGM-004", Message: "the diagram has an element that no mapping resolves"}}
		}
	}

	return run
}

// regenerateViewRun is a run with one view whose presence is violated, and the finding of that violation.
func regenerateViewRun() *conformance.Run {
	view := testView(testViewID, viewChecks{conformance.VerdictViolation, conformance.VerdictNotApplicable, conformance.VerdictNotApplicable}, "regenerate view")
	finding := viewFinding(&viewFindingSpec{
		ruleID: "VIEW-PRES-001", kind: "view-absent", result: conformance.VerdictViolation, location: testViewSource,
		nextAction: "regenerate view", viewID: testViewID,
	})

	return viewRun([]conformance.ViewResult{view}, []conformance.Finding{finding})
}

// assertAreaVerdictInJSON checks that the results file gives the diagram area the verdict `unknown`.
func assertAreaVerdictInJSON(t *testing.T, run *conformance.Run) {
	t.Helper()

	var doc struct {
		Areas map[string]struct {
			Verdict string `json:"verdict"`
		} `json:"areas"`
	}

	decodeJSON(t, renderJSON(t, run), &doc)
	equal(t, doc.Areas["diagramCurrency"].Verdict, "unknown")
}

// assertUnknownViewInSARIF checks the SARIF document of a run with one unknown view: the result, and the property of
// the diagram area with the view.
func assertUnknownViewInSARIF(t *testing.T, run *conformance.Run) {
	t.Helper()

	schema := sarifValidator(t)
	data, doc := renderSarif(t, run)
	assertValid(t, schema, data)
	equal(t, len(doc.Runs[0].Results), 1)
	equal(t, doc.Runs[0].Results[0].Properties["result"], any("unknown"))
	equal(t, doc.Runs[0].Results[0].Properties["viewId"], any(testViewID))

	var raw struct {
		Runs []struct {
			Properties struct {
				DiagramCurrency struct {
					Verdict string `json:"verdict"`
					Views   []struct {
						ID             string `json:"id"`
						Correspondence string `json:"correspondence"`
						Method         string `json:"correspondenceMethod"`
						NextAction     string `json:"nextAction"`
					} `json:"views"`
				} `json:"diagramCurrency"`
			} `json:"properties"`
		} `json:"runs"`
	}

	decodeJSON(t, data, &raw)

	area := raw.Runs[0].Properties.DiagramCurrency
	equal(t, area.Verdict, "unknown")
	equal(t, len(area.Views), 1)
	equal(t, area.Views[0].Correspondence, "unknown")
	equal(t, area.Views[0].Method, "generated-source")
	equal(t, area.Views[0].NextAction, "supply evidence")
}

// viewSuite is the testsuite of a view check in a JUnit document, and fails the test when there is none.
func viewSuite(t *testing.T, xml, name string) string {
	t.Helper()

	start := strings.Index(xml, `<testsuite name="views/`+name+`"`)
	if start < 0 {
		t.Fatalf("no suite %s in:\n%s", name, xml)
	}

	return xml[start : start+strings.Index(xml[start:], "</testsuite>")]
}

// twoViewsRun has a stale view and a view with an unknown correspondence, and a finding for each. The unknown one
// only warns.
func twoViewsRun() *conformance.Run {
	views := []conformance.ViewResult{
		testView("a-view", viewChecks{conformance.VerdictPass, conformance.VerdictViolation, conformance.VerdictNotApplicable}, "regenerate view"),
		testView("b-view", viewChecks{conformance.VerdictPass, conformance.VerdictPass, conformance.VerdictUnknown}, "supply evidence"),
	}
	findings := []conformance.Finding{
		viewFinding(&viewFindingSpec{
			ruleID: "VIEW-FRESH-001", kind: "view-stale", result: conformance.VerdictViolation, location: testViewArtifact,
			nextAction: "regenerate view", viewID: "a-view",
		}),
		viewFinding(&viewFindingSpec{
			ruleID: "VIEW-CORR-001", kind: "view-drift", result: conformance.VerdictUnknown, location: testViewSource,
			nextAction: "supply evidence", viewID: "b-view",
		}),
	}
	run := viewRun(views, findings)

	// The rule of a check that is not `unknown` in this run: unknown follows the onUnknown of its rule.
	setOnUnknown(run, "VIEW-CORR-001", policy.UnknownWarn)

	return run
}

// assertUnknownViewShown checks the pretty report of a run with one unknown view: the unknown view is not hidden by
// the passing conformance area.
func assertUnknownViewShown(t *testing.T, run *conformance.Run) {
	t.Helper()

	equal(t, run.Areas.Conformance.Verdict, conformance.AreaVerdict(conformance.VerdictPass))
	equal(t, run.Summary.Verdict, conformance.VerdictUnknown)

	text := reporting.RenderPretty(run, plain)
	contains(t, text, "Conformance: pass (1 pass)")
	contains(t, text, "Diagram presence, freshness, and correspondence: unknown (")
	contains(t, text, "  "+testViewID+" (context, generated): presence: pass, freshness: pass, correspondence: unknown (method: generated-source), "+
		"assessment currency: not-evaluated; next action: supply evidence")
	contains(t, text, "UNKNOWN VIEW-CORR-001 View corresponds to the architecture")
	contains(t, text, "Unknown area(s): Diagram currency")
	notContains(t, text, "Diagram presence, freshness, and correspondence: pass")
	notContains(t, text, "Not configured: Deployment trace, ADR governance, Diagram currency")
	contains(t, text, "  Cannot judge: the diagram has an element that no mapping resolves (CO-DGM-004)")
	equal(t, strings.Count(text, testViewID+" ("), 1)
}

// assertNoViewsDeclared checks that a run with no views has no diagram area in any output.
func assertNoViewsDeclared(t *testing.T) {
	t.Helper()

	run := testutil.SampleRun()
	equal(t, run.Areas.DiagramCurrency.Verdict, conformance.AreaNotConfigured)
	equal(t, conformance.AreaOfViews(nil, nil).Verdict, conformance.AreaNotConfigured)
	equal(t, len(conformance.AreaOfViews(nil, nil).Counts), 0)

	text := reporting.RenderPretty(run, plain)
	contains(t, text, "Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation")
	notContains(t, text, "Diagram presence, freshness, and correspondence")
	notContains(t, text, "Diagram currency: pass")
	notContains(t, text, "Unknown area(s): Diagram currency")
	notContains(t, reporting.RenderJUnit(run), `name="views/`)

	data, _ := renderSarif(t, run)
	notContains(t, string(data), "diagramCurrency")
}

// assertRegeneratedViewInSARIF checks the SARIF result of the run with one absent view: its location, its
// properties, and the descriptors of the three view rules.
func assertRegeneratedViewInSARIF(t *testing.T) {
	t.Helper()

	data, doc := renderSarif(t, regenerateViewRun())
	assertValid(t, sarifValidator(t), data)

	results := doc.Runs[0].Results
	equal(t, len(results), 1)
	equal(t, len(results[0].Locations), 1)
	equal(t, results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI, testViewSource)
	equal(t, results[0].Locations[0].PhysicalLocation.ArtifactLocation.URIBaseID, "SRCROOT")
	equal(t, results[0].Properties["viewId"], any(testViewID))
	equal(t, results[0].Properties["area"], any("diagramCurrency"))
	equal(t, results[0].Properties["nextAction"], any("regenerate view"))
	equal(t, results[0].Properties["findingKind"], any("view-absent"))

	ids := map[string]bool{}
	for _, rule := range doc.Runs[0].Tool.Driver.Rules {
		ids[rule.ID] = true
	}

	for _, id := range []string{"VIEW-PRES-001", "VIEW-FRESH-001", "VIEW-CORR-001"} {
		if !ids[id] {
			t.Errorf("rule descriptor %s is missing", id)
		}
	}
}

// assertViewSuites checks the suites of the JUnit document of twoViewsRun.
func assertViewSuites(t *testing.T, xml string) {
	t.Helper()

	contains(t, viewSuite(t, xml, "a-view/presence"), `<testcase classname="views/a-view" name="VIEW-PRES-001 View is present"/>`)
	contains(t, viewSuite(t, xml, "a-view/freshness"), `tests="1" failures="1" errors="0" skipped="0"`)
	contains(t, viewSuite(t, xml, "a-view/freshness"), `<failure type="violation" message="1 active finding(s)">`)
	contains(t, viewSuite(t, xml, "a-view/correspondence"), `skipped="1"`)
	contains(t, viewSuite(t, xml, "a-view/correspondence"), "<skipped")
	contains(t, viewSuite(t, xml, "b-view/correspondence"), `failures="0"`)
	contains(t, viewSuite(t, xml, "b-view/correspondence"), "<system-out>unknown (warn)")
	notContains(t, viewSuite(t, xml, "b-view/correspondence"), "<failure")
}

// mixedViewsRun has an authored view that passes and a generated view whose presence is violated.
func mixedViewsRun() *conformance.Run {
	views := []conformance.ViewResult{
		testView("a-view", viewChecks{conformance.VerdictPass, conformance.VerdictPass, conformance.VerdictPass}, ""),
		testView("b-view", viewChecks{conformance.VerdictViolation, conformance.VerdictNotApplicable, conformance.VerdictNotApplicable}, "regenerate view"),
	}
	views[0].Origin = "authored"

	return viewRun(views, nil)
}

// staleViewRun has a generated view that is stale, and its finding, located at line 4 of the output path.
func staleViewRun() *conformance.Run {
	view := testView(testViewID, viewChecks{conformance.VerdictPass, conformance.VerdictViolation, conformance.VerdictPass}, "regenerate view")
	finding := viewFinding(&viewFindingSpec{
		ruleID: "VIEW-FRESH-001", kind: "view-stale", result: conformance.VerdictViolation, location: testViewArtifact,
		nextAction: "regenerate view", viewID: testViewID,
	})
	finding.Location.Line = 4

	return viewRun([]conformance.ViewResult{view}, []conformance.Finding{finding})
}

func TestViewArea(t *testing.T) {
	t.Parallel()

	t.Run("Unknown view is not hidden", func(t *testing.T) {
		t.Parallel()
		assertUnknownViewShown(t, unknownCorrespondenceRun())
	})

	t.Run("the area heading follows the conformance section and precedes the not-configured line", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(unknownCorrespondenceRun(), plain)
		conformanceAt := strings.Index(text, "Conformance: pass")
		areaAt := strings.Index(text, "Diagram presence, freshness, and correspondence: unknown")
		rowAt := strings.Index(text, "  "+testViewID+" (")
		ruleAt := strings.Index(text, "UNKNOWN VIEW-CORR-001")
		notConfiguredAt := strings.Index(text, "Not configured:")

		if !inOrder(conformanceAt, areaAt, rowAt, ruleAt, notConfiguredAt) {
			t.Errorf("section order %d, %d, %d, %d, %d in:\n%s", conformanceAt, areaAt, rowAt, ruleAt, notConfiguredAt, text)
		}
	})

	t.Run("a view that does not pass shows every check by word", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(mixedViewsRun(), plain)
		contains(t, text, "  a-view (context, authored): presence: pass, freshness: pass, correspondence: pass (method: generated-source), assessment currency: not-evaluated\n")
		contains(t, text, "  b-view (context, generated): presence: violation, freshness: not-applicable, correspondence: not-applicable (method: generated-source), "+
			"assessment currency: not-evaluated; next action: regenerate view")
		contains(t, text, "Diagram presence, freshness, and correspondence: violation (")
	})

	t.Run("a view finding shows its view and its declared source, not CALM references", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(regenerateViewRun(), plain)

		contains(t, text, "FAIL VIEW-PRES-001 View is present")
		contains(t, text, "    "+testViewSource+"\n      view "+testViewID)
		contains(t, text, "  Fix: ")
		notContains(t, text, "calm:node:"+testViewID)
		notContains(t, text, "Target: calm:node:")
	})

	t.Run("Unknown correspondence in the area", func(t *testing.T) {
		t.Parallel()

		run := unknownCorrespondenceRun()
		equal(t, run.Areas.DiagramCurrency.Verdict, conformance.AreaVerdict(conformance.VerdictUnknown))
		equal(t, run.Areas.DiagramCurrency.Counts["unknown"], 1)
		equal(t, run.Areas.DiagramCurrency.Counts["pass"], 2)
		equal(t, run.Areas.DiagramCurrency.Counts["views"], 1)

		assertAreaVerdictInJSON(t, run)
		contains(t, reporting.RenderPretty(run, plain), "Diagram presence, freshness, and correspondence: unknown (")

		xml := reporting.RenderJUnit(run)
		contains(t, xml, `<testsuite name="views/`+testViewID+`/correspondence" tests="1" failures="1" errors="0" skipped="0">`)
		contains(t, xml, `<failure type="unknown"`)

		assertUnknownViewInSARIF(t, run)
	})

	t.Run("No views declared", func(t *testing.T) {
		t.Parallel()
		assertNoViewsDeclared(t)
	})

	t.Run("SARIF location", func(t *testing.T) {
		t.Parallel()
		assertRegeneratedViewInSARIF(t)
	})

	t.Run("a generated view finding is located at the output path", func(t *testing.T) {
		t.Parallel()

		_, doc := renderSarif(t, staleViewRun())
		loc := doc.Runs[0].Results[0].Locations[0].PhysicalLocation
		equal(t, loc.ArtifactLocation.URI, testViewArtifact)
		equal(t, loc.Region.StartLine, 4)
	})

	t.Run("JUnit has one suite for each view check", func(t *testing.T) {
		t.Parallel()

		xml := reporting.RenderJUnit(twoViewsRun())
		contains(t, xml, `<testsuites name="concordia" tests="7" failures="1" errors="0" skipped="1">`)
		equal(t, strings.Count(xml, `<testsuite name="views/`), 6)
		assertViewSuites(t, xml)

		// The pack suite does not count the rules of the views again.
		notContains(t, xml, `<testsuite name="views@1.0.0"`)
	})
}
