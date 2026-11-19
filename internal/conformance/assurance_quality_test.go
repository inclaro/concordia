package conformance_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
)

// The quality scenarios of a run (tasks 9.2, 9.5, 9.6, 9.7): scenario files listed under `assurance.quality` are
// evaluated after the rules and the review evaluation, and each scenario is one obligation with the enforcement of
// the scenario.

const qualityWorkspaceKey = "assurance:\n  quality:\n    scenarios: [\"quality/*.yaml\"]\n"

// qualityCacheTradeoff is an open tradeoff finding that lowers `modifiability`.
const qualityCacheTradeoff = "findings:\n  - id: F-CACHE\n    type: tradeoff\n    statement: The cache lowers modifiability.\n    owner: team-billing\n    status: open\n" +
	"    scenarios: [QS-BND]\n    areas: [modifiability]\n    addressedBy: [ADR-7]\n    assumptions: [ASM-ONE]\n"

// scenarioFile is one QualityScenarioSet with the given scenario entries (each a full list item) and findings.
func scenarioFile(scenarios ...string) string {
	return "apiVersion: concordia.io/v1alpha1\nkind: QualityScenarioSet\nscenarios:\n" + strings.Join(scenarios, "")
}

// structuralScenario is a structural scenario over the rule CO-BND-001 of the demo policy.
func structuralScenario(area, enforcement string) string {
	return "  - id: QS-BND" + "\n    area: " + area + "\n    qualityAttribute: " + area + "\n    stimulusSource: developer\n" +
		"    stimulus: a developer changes billing\n    environment: prod\n    expectedResponse: billing stays inside its boundary\n" +
		"    owner: team-billing\n    scope: [billing-service]\n    mode: structural\n    enforcement: " + enforcement + "\n" +
		"    criterion:\n      rules: [CO-BND-001]\n"
}

// measuredScenario is a measured scenario that no measurement names.
func measuredScenario(enforcement string) string {
	return "  - id: QS-FAILOVER" + "\n    area: availability\n    qualityAttribute: availability" + "\n    stimulusSource: operator\n" +
		"    stimulus: the primary fails\n    environment: prod\n    expectedResponse: a replica takes over\n" +
		"    owner: team-billing\n    scope: [billing-service]\n    mode: measured\n    enforcement: " + enforcement + "\n" +
		"    criterion:\n      metric: failover-seconds\n      unit: s\n      comparison: lte\n      threshold: 30\n"
}

// reviewedScenario is a reviewed scenario of the quality assessment with the subject `billing-review`.
func reviewedScenario(id, area, enforcement string) string {
	return "  - id: " + id + "\n    area: " + area + "\n    qualityAttribute: " + area + "\n    stimulusSource: on-call engineer\n" +
		"    stimulus: an alert fires\n    environment: prod\n    expectedResponse: the runbook leads to a fix\n" +
		"    owner: team-billing\n    scope: [billing-service]\n    mode: reviewed\n    enforcement: " + enforcement + "\n" +
		"    criterion:\n      subject: billing-review\n      authority: sec\n"
}

// qualityWorkspace is the demo workspace with the environment prod, the assurance key, and extra keys. A `check:`
// block in extra replaces the default one.
func qualityWorkspace(extra string) string {
	text := wsText(reviewEnvironments + qualityWorkspaceKey)
	if strings.Contains(extra, "check:") {
		text = strings.Replace(text, checkMarker, "", 1)
	}

	return text + extra
}

// qualitySetup is the demo workspace with the quality scenarios of one file.
func qualitySetup(t *testing.T, ws string, scenarios ...string) *setup {
	t.Helper()

	return newSetup(t, map[string]string{"quality/scenarios.yaml": scenarioFile(scenarios...)}, &setupOpts{ws: ws, graph: buildGraph(graphOpts{noEdges: true})})
}

// violatingSetup is qualitySetup over the graph in which billing imports fulfillment: the rule CO-BND-001 is a violation.
func violatingSetup(t *testing.T, ws string, scenarios ...string) *setup {
	t.Helper()

	return newSetup(t, map[string]string{"quality/scenarios.yaml": scenarioFile(scenarios...)}, &setupOpts{ws: ws})
}

func qualityRuleOf(t *testing.T, run *conformance.Run, scenario string) *conformance.RuleResult {
	t.Helper()

	return reviewRuleOf(t, run, "quality:"+scenario)
}

func qualityEntryOf(t *testing.T, run *conformance.Run, scenario string) *conformance.QualityEntry {
	t.Helper()

	if run.Assurance == nil {
		t.Fatal("the run has no assurance section")
	}

	for i := range run.Assurance.Quality {
		if run.Assurance.Quality[i].Scenario == scenario {
			return &run.Assurance.Quality[i]
		}
	}

	t.Fatalf("no quality entry %s: %+v", scenario, run.Assurance.Quality)

	return nil
}

// advisoryBoundary makes the rule CO-BND-001 advisory, so that its own violation does not gate and only the scenario does.
const advisoryBoundary = "check:\n  minimumCoverage: 0.5\n  unknown: fail\n  failOn: error\n  enforcement:\n    - {rules: [CO-BND-001], mode: advisory}\n"

// Scenario: Mandatory scenario unknown. A mandatory scenario with no evidence is unknown and, with `check.unknown:
// fail`, exits 4. A passing conformance area does not make the run an all-clear.
func TestMandatoryUnknownScenarioExitsFour(t *testing.T) {
	t.Parallel()

	s := qualitySetup(t, qualityWorkspace(""), measuredScenario("mandatory"))

	run := s.mustCheck(t, onlyBoundary)

	requireUnknownQualityRule(t, qualityRuleOf(t, run, "QS-FAILOVER"), "error", "fail")

	if run.Summary.ExitCode != diag.Unknown {
		t.Fatalf("exit code = %d, want 4 (rules %+v)", run.Summary.ExitCode, run.Summary)
	}
}

// Scenario: Mandatory scenario unknown with a green conformance area. The summary and the areas show the unknown.
func TestUnknownMandatoryScenarioIsNotShownAsPassing(t *testing.T) {
	t.Parallel()

	s := qualitySetup(t, qualityWorkspace(""), measuredScenario("mandatory"))
	run := s.mustCheck(t, onlyBoundary)

	if run.Areas.Conformance.Verdict != conformance.AreaVerdict(conformance.VerdictPass) {
		t.Fatalf("conformance area = %s, want pass (the quality scenario must not move it)", run.Areas.Conformance.Verdict)
	}

	requireOneUnknownQualityFinding(t, &run.Areas.QualityEvaluation)

	if run.Summary.Verdict != conformance.VerdictUnknown {
		t.Errorf("summary verdict = %s, want unknown", run.Summary.Verdict)
	}

	requireQualityEntryShape(t, qualityEntryOf(t, run, "QS-FAILOVER"), qualityEntryShape{"measured", conformance.VerdictUnknown, conformance.ReasonNoEvidenceSupplied, "mandatory"})

	for _, f := range qualityFindingsOf(run, "quality:QS-FAILOVER") {
		requireNoEvidenceFinding(t, f)
	}
}

// requireOneUnknownQualityFinding reports a quality area that is not unknown with exactly one finding.
func requireOneUnknownQualityFinding(t *testing.T, area *conformance.AreaResult) {
	t.Helper()

	type areaShape struct {
		verdict          conformance.AreaVerdict
		unknown, finding int
	}

	got := areaShape{area.Verdict, area.Counts["unknown"], area.Counts["findings"]}
	if want := (areaShape{conformance.AreaVerdict(conformance.VerdictUnknown), 1, 1}); got != want {
		t.Errorf("areas.qualityEvaluation = %+v", *area)
	}
}

// requireNoEvidenceFinding reports a finding of the quality area that does not say that no evidence was supplied.
func requireNoEvidenceFinding(t *testing.T, f *conformance.Finding) {
	t.Helper()

	if f.Area != conformance.AreaQualityEvaluation {
		t.Errorf("finding area %s reason %s", f.Area, f.Reason)
	}

	if f.Reason != conformance.ReasonNoEvidenceSupplied {
		t.Errorf("finding area %s reason %s", f.Area, f.Reason)
	}
}

// Scenario: Advisory scenario unknown. The verdict is shown and the run does not fail because of it.
func TestAdvisoryUnknownScenarioDoesNotFail(t *testing.T) {
	t.Parallel()

	s := qualitySetup(t, qualityWorkspace(""), measuredScenario("advisory"))
	run := s.mustCheck(t, onlyBoundary)

	requireUnknownQualityRule(t, qualityRuleOf(t, run, "QS-FAILOVER"), "warning", "warn")

	if run.Summary.ExitCode != diag.OK {
		t.Errorf("exit code = %d, want 0", run.Summary.ExitCode)
	}

	requireQualityAreaVerdict(t, &run.Areas.QualityEvaluation, conformance.VerdictUnknown)
}

// Scenario: Advisory scenario violation. The rule CO-BND-001 is advisory too, so only the scenarios can gate.
func TestAdvisoryViolationDoesNotFailAndMandatoryDoes(t *testing.T) {
	t.Parallel()

	ws := qualityWorkspace(advisoryBoundary)

	advisory := violatingSetup(t, ws, structuralScenario("modifiability", "advisory")).mustCheck(t, onlyBoundary)
	requireAdvisoryViolation(t, advisory)

	mandatory := violatingSetup(t, ws, structuralScenario("modifiability", "mandatory")).mustCheck(t, onlyBoundary)

	if rule := qualityRuleOf(t, mandatory, "QS-BND"); rule.Verdict != conformance.VerdictViolation || rule.Severity != "error" {
		t.Fatalf("mandatory rule = %s %s", rule.Verdict, rule.Severity)
	}

	if mandatory.Summary.ExitCode != diag.Violation {
		t.Errorf("a mandatory violation exited %d, want 1", mandatory.Summary.ExitCode)
	}
}

// requireAdvisoryViolation checks the run of an advisory scenario that is a violation: the rule warns and the run passes.
func requireAdvisoryViolation(t *testing.T, run *conformance.Run) {
	t.Helper()

	rule := qualityRuleOf(t, run, "QS-BND")

	type ruleOutcome struct {
		verdict     conformance.RuleVerdict
		severity    policy.RuleSeverity
		enforcement policy.Enforcement
	}

	if got := (ruleOutcome{rule.Verdict, rule.Severity, rule.Enforcement}); got != (ruleOutcome{conformance.VerdictViolation, "warning", "advisory"}) {
		t.Fatalf("advisory rule = %s %s %s", rule.Verdict, rule.Severity, rule.Enforcement)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Errorf("an advisory violation exited %d", run.Summary.ExitCode)
	}

	got := qualityEntryOf(t, run, "QS-BND")
	if got.Verdict != conformance.VerdictViolation || got.Mode != "structural" {
		t.Errorf("entry = %+v", got)
	}

	if len(got.Findings) != 1 {
		t.Errorf("entry = %+v", got)
	}
}

// Scenario: An exception changes the disposition only. A baseline entry for the finding of the scenario leaves the
// verdict a violation and the finding visible, and the run no longer fails.
func TestExceptionChangesTheDispositionOfAScenarioOnly(t *testing.T) {
	t.Parallel()

	ws := qualityWorkspace(advisoryBoundary)
	scenario := structuralScenario("modifiability", "mandatory")

	first := violatingSetup(t, ws, scenario).mustCheck(t, onlyBoundary)
	fingerprint := qualityFingerprintOf(first, "quality:QS-BND")

	if fingerprint == "" || first.Summary.ExitCode != diag.Violation {
		t.Fatalf("fingerprint %q, exit %d", fingerprint, first.Summary.ExitCode)
	}

	s := violatingSetup(t, ws, scenario)
	writeBaseline(t, s.dir, nil, exceptions.BaselineEntry{
		Fingerprint: fingerprint, RuleID: "quality:QS-BND", Owner: "team-billing", Reason: "Known", CreatedAt: "2026-01-01T00:00:00Z",
	})

	run := s.mustCheck(t, onlyBoundary)

	if rule := qualityRuleOf(t, run, "QS-BND"); rule.Verdict != conformance.VerdictViolation {
		t.Errorf("the verdict changed to %s", rule.Verdict)
	}

	for _, f := range qualityFindingsOf(run, "quality:QS-BND") {
		if f.Disposition != exceptions.DispositionBaselined {
			t.Errorf("disposition = %s", f.Disposition)
		}
	}

	if run.Summary.ExitCode != diag.OK || run.Summary.Baselined != 1 {
		t.Errorf("exit %d, summary %+v", run.Summary.ExitCode, run.Summary)
	}

	requireQualityAreaVerdict(t, &run.Areas.QualityEvaluation, conformance.VerdictViolation)
}

// qualityFingerprintOf is the fingerprint of the last finding of a rule, or empty when the rule has none.
func qualityFingerprintOf(run *conformance.Run, ruleID string) string {
	findings := qualityFindingsOf(run, ruleID)
	if len(findings) == 0 {
		return ""
	}

	return findings[len(findings)-1].Fingerprint
}

// Scenario: Structural pass for a measured scenario. A passing structural scenario does not satisfy a measured one,
// and the report shows the mode of each result.
func TestStructuralPassDoesNotSatisfyAMeasuredScenario(t *testing.T) {
	t.Parallel()

	s := qualitySetup(t, qualityWorkspace(""),
		structuralScenario("modifiability", "mandatory"), measuredScenario("mandatory"))
	run := s.mustCheck(t, onlyBoundary)

	requireQualityEntryShape(t, qualityEntryOf(t, run, "QS-BND"), qualityEntryShape{"structural", conformance.VerdictPass, "", "mandatory"})
	requireQualityEntryShape(t, qualityEntryOf(t, run, "QS-FAILOVER"), qualityEntryShape{"measured", conformance.VerdictUnknown, conformance.ReasonNoEvidenceSupplied, "mandatory"})

	areas := qualityAreasByName(run)
	if availability, modifiability := areas["availability"], areas["modifiability"]; availability.Verdict != "unknown" || modifiability.Verdict != "pass" {
		t.Errorf("availability %+v modifiability %+v", availability, modifiability)
	}
}

// Scenario: Required area missing. An area that `requiredAreas` lists and that no scenario covers is a violation that
// gates; the other areas without a scenario are `no scenario`, never a pass.
func TestRequiredAreaWithoutAScenarioIsAViolation(t *testing.T) {
	t.Parallel()

	ws := qualityWorkspace("")
	ws = strings.Replace(ws, "scenarios: [\"quality/*.yaml\"]", "scenarios: [\"quality/*.yaml\"]\n    requiredAreas: [operability]", 1)

	run := qualitySetup(t, ws, structuralScenario("modifiability", "advisory")).mustCheck(t, onlyBoundary)

	rule := reviewRuleOf(t, run, "quality:area:operability")
	if rule.Verdict != conformance.VerdictViolation || rule.Severity != "error" {
		t.Fatalf("required area rule = %s %s", rule.Verdict, rule.Severity)
	}

	if run.Summary.ExitCode != diag.Violation {
		t.Errorf("exit code = %d, want 1", run.Summary.ExitCode)
	}

	verdicts := qualityAreasByName(run)

	got := verdicts["operability"]
	if got.Verdict != "violation" || !got.Required {
		t.Errorf("operability = %+v", got)
	}

	if len(got.Reported) != 1 {
		t.Errorf("operability = %+v", got)
	}

	if got := verdicts["security"]; got.Verdict != "no-scenario" {
		t.Errorf("security = %+v, want no-scenario", got)
	}

	requireQualityAreaVerdict(t, &run.Areas.QualityEvaluation, conformance.VerdictViolation)
}

// A run without a scenario has no quality rule, and the area is not configured, never a pass.
func TestAWorkspaceWithoutScenariosLeavesTheQualityAreaNotConfigured(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)

	if got := run.Areas.QualityEvaluation.Verdict; got != conformance.AreaNotConfigured {
		t.Errorf("area = %s", got)
	}

	if run.Assurance != nil {
		t.Errorf("a run without an assurance key has an assurance section")
	}
}

// withLinks adds an `addressedBy` list to a scenario entry.
func withLinks(scenario string, ids ...string) string {
	return scenario + "    addressedBy: [" + strings.Join(ids, ", ") + "]\n"
}

// decisionsHook is a governance hook that gives a decision summary with the decisions (id to applicability state).
func decisionsHook(states map[string]string) *fakeDecisionHook {
	return &fakeDecisionHook{build: func(*conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
		summary := govSummary()
		summary.Decisions, summary.Findings = nil, []string{}

		for id, state := range states {
			summary.Decisions = append(summary.Decisions, conformance.DecisionEntry{
				ID: id, Path: govADRPath, Status: "accepted", StatusText: "Accepted", Expect: "active", Categories: []string{},
				Presence:      conformance.DecisionState{State: "committed", Reason: "committed"},
				Applicability: conformance.DecisionApplicability{State: state, Reason: state},
				Enforcement:   conformance.DecisionEnforcement{State: "unlinked", Rules: []string{}},
				Governs:       []string{}, Released: []string{}, Successors: []string{}, Predecessors: []string{},
			})
		}

		return &conformance.DecisionEvaluation{Summary: summary}, nil
	}}
}

// linkSetup is the quality workspace with a decision register and the scenarios of one file.
func linkSetup(t *testing.T, hook *fakeDecisionHook, scenarios ...string) *setup {
	t.Helper()

	ws := wsText(reviewEnvironments + qualityWorkspaceKey + "adr:\n  files: [" + govRegisterPath + "]\n")
	s := newSetup(t, map[string]string{"quality/scenarios.yaml": scenarioFile(scenarios...)}, &setupOpts{ws: ws, graph: buildGraph(graphOpts{noEdges: true})})
	s.deps.EvaluateDecisions = hook.fn()

	return s
}

func linkOf(t *testing.T, entry *conformance.QualityEntry) conformance.QualityLink {
	t.Helper()

	if len(entry.Links) != 1 {
		t.Fatalf("links = %+v", entry.Links)
	}

	return entry.Links[0]
}

// Scenario: Scenario addressed by an active ADR. `ADR-0007` is compared by canonical id with the registered `ADR-7`.
func TestAnAddressedByLinkReadsTheDecisionSummary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, state, status string
		detail              string
		rationale           bool
	}{
		{name: "active", state: "active", status: "active"},
		{name: "superseded", state: "superseded", status: "not-active", rationale: true},
		{name: "inactive", state: "inactive", status: "not-active", rationale: true},
		{name: "ambiguous", state: "ambiguous", status: "unknown", detail: "ambiguous"},
		{name: "unknown", state: "unknown", status: "unknown", detail: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := linkSetup(t, decisionsHook(map[string]string{"ADR-7": tc.state}), withLinks(structuralScenario("modifiability", "advisory"), "ADR-0007"))
			run := s.mustCheck(t, onlyBoundary)

			entry := qualityEntryOf(t, run, "QS-BND")
			link := linkOf(t, entry)

			want := qualityLinkShape{"ADR-0007", "ADR-7", tc.status, tc.state, tc.detail, tc.rationale}
			if got := shapeOfQualityLink(&link); got != want {
				t.Errorf("link = %+v", link)
			}

			if entry.ReviewRationale != tc.rationale {
				t.Errorf("reviewRationale = %t", entry.ReviewRationale)
			}
		})
	}
}

// Scenario: ADR governance not evaluated. With no decision summary the link is `unknown` with
// `adr-governance-not-available`, never active, and the file loads.
func TestAnAddressedByLinkWithoutADRGovernanceIsUnknown(t *testing.T) {
	t.Parallel()

	s := qualitySetup(t, qualityWorkspace(""), withLinks(structuralScenario("modifiability", "advisory"), "ADR-0007"))
	run := s.mustCheck(t, onlyBoundary)

	link := linkOf(t, qualityEntryOf(t, run, "QS-BND"))
	if link.Status != "unknown" || link.Detail != "adr-governance-not-available" {
		t.Errorf("link = %+v", link)
	}

	if link.State != "" {
		t.Errorf("link = %+v", link)
	}
}

// Scenario: Unregistered ADR identifier with ADR governance evaluated. The run stops with exit code 2 and the
// diagnostic names the identifier and its place in the file.
func TestAnUnregisteredADRWithADRGovernanceIsInvalidInput(t *testing.T) {
	t.Parallel()

	s := linkSetup(t, decisionsHook(map[string]string{"ADR-7": "active"}), withLinks(structuralScenario("modifiability", "advisory"), "ADR-9"))

	_, err := s.check(t, onlyBoundary)

	requireQualityConfigError(t, err, "ADR-9")
}

// Scenario: Malformed ADR identifier. `ADR_7` is invalid whether or not governance is evaluated.
func TestAMalformedADRIdentifierIsInvalidInput(t *testing.T) {
	t.Parallel()

	for name, s := range map[string]*setup{
		"with governance":    linkSetup(t, decisionsHook(map[string]string{"ADR-7": "active"}), withLinks(structuralScenario("modifiability", "advisory"), "ADR_7")),
		"without governance": qualitySetup(t, qualityWorkspace(""), withLinks(structuralScenario("modifiability", "advisory"), "ADR_7")),
	} {
		de := s.failureOf(t, onlyBoundary)
		if de.ExitCode != diag.InvalidInput || !strings.Contains(de.Error(), "ADR_7") {
			t.Errorf("%s: %+v", name, de)
		}
	}
}

// Scenario: Tradeoff stays open. The `performance` scenario passes and an open tradeoff lowers `modifiability`: the
// report lists the tradeoff next to `modifiability` and the passing scenario does not close it.
func TestAnOpenTradeoffIsListedNextToItsAttribute(t *testing.T) {
	t.Parallel()

	s := linkSetup(t, decisionsHook(map[string]string{"ADR-7": "active"}), structuralScenario("performance", "advisory"), qualityCacheTradeoff)
	run := s.mustCheck(t, onlyBoundary)

	areas := qualityAreasByName(run)
	if got := areas["performance"]; got.Verdict != "pass" || len(got.Scenarios) != 1 {
		t.Errorf("performance = %+v", got)
	}

	modifiability := areas["modifiability"]
	requireOpenCacheTradeoff(t, &modifiability)
}

// requireOpenCacheTradeoff stops the test unless the area has no scenario and lists the open tradeoff F-CACHE with
// its active ADR link.
func requireOpenCacheTradeoff(t *testing.T, area *conformance.QualityAreaEntry) {
	t.Helper()

	if area.Verdict != "no-scenario" || len(area.Findings) != 1 {
		t.Fatalf("modifiability = %+v", area)
	}

	finding := area.Findings[0]
	if finding.ID != "F-CACHE" || finding.Status != "open" {
		t.Fatalf("modifiability = %+v", area)
	}

	if len(finding.Links) != 1 {
		t.Fatalf("finding links = %+v", finding.Links)
	}

	if link := finding.Links[0]; link.Canonical != "ADR-7" || link.Status != "active" {
		t.Errorf("finding links = %+v", finding.Links)
	}
}

// The results schema accepts the quality sections: scenarios of the three modes with their links, the areas with a
// required one, and the findings next to their areas.
func TestResultsSchemaAcceptsTheQualitySections(t *testing.T) {
	t.Parallel()

	ws := wsText(reviewEnvironments + "assurance:\n  quality:\n    scenarios: [\"quality/*.yaml\"]\n    requiredAreas: [operability]\n" +
		"adr:\n  files: [" + govRegisterPath + "]\n")

	s := newSetup(t, map[string]string{"quality/scenarios.yaml": scenarioFile(
		withLinks(structuralScenario("performance", "advisory"), "ADR-7"),
		measuredScenario("mandatory"), reviewedScenario("QS-RUNBOOK", "security", "advisory"), qualityCacheTradeoff)},
		&setupOpts{ws: ws, graph: buildGraph(graphOpts{noEdges: true})})
	s.deps.EvaluateDecisions = decisionsHook(map[string]string{"ADR-7": "superseded"}).fn()

	run := s.mustCheck(t, onlyBoundary)

	if run.Assurance == nil {
		t.Fatal("the run has no assurance section")
	}

	if len(run.Assurance.Quality) != 3 || len(run.Assurance.QualityAreas) != 6 {
		t.Fatalf("assurance = %+v", run.Assurance)
	}

	if errs := conformance.ValidateResultJSON(canonicalOf(t, run)); len(errs) > 0 {
		t.Fatalf("results.json does not validate: %v", errs)
	}

	if run.Assurance.Quality[0].Scenario != "QS-BND" || run.Assurance.Quality[2].Scenario != "QS-RUNBOOK" {
		t.Errorf("the scenarios are not sorted: %+v", run.Assurance.Quality)
	}

	requireQualityEntryShape(t, qualityEntryOf(t, run, "QS-RUNBOOK"), qualityEntryShape{"reviewed", conformance.VerdictUnknown, conformance.ReasonReviewedModeNotAvailable, "advisory"})
}

// Scenario: Rollback covers quality scenarios. A mandatory scenario that is unknown exits 4 (see
// TestMandatoryUnknownScenarioExitsFour); with an active `check.rollback` its rule runs as shadow, keeps its verdict,
// and the run exits 0, as for every typed rule.
func TestRollbackRunsAMandatoryScenarioAsShadow(t *testing.T) {
	t.Parallel()

	const rollback = "check:\n  minimumCoverage: 0.5\n  unknown: fail\n  failOn: error\n" +
		"  rollback:\n    reason: quality migration\n    authorizedBy: jane.doe\n    expires: \"2099-01-01\"\n"

	s := qualitySetup(t, qualityWorkspace(rollback), measuredScenario("mandatory"))
	run := s.mustCheck(t, onlyBoundary)

	rule := qualityRuleOf(t, run, "QS-FAILOVER")
	if rule.Verdict != conformance.VerdictUnknown || rule.Enforcement != policy.EnforcementShadow {
		t.Fatalf("rule = %s %s, want unknown under shadow", rule.Verdict, rule.Enforcement)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Fatalf("exit code = %d, want 0 under the rollback", run.Summary.ExitCode)
	}
}
