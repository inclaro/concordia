package conformance_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// govViolationArea is the verdict of an area whose only rule is a violation.
const govViolationArea = conformance.AreaVerdict(conformance.VerdictViolation)

// govRequireViolationAreas fails the test unless the governance finding is counted in `areas.adrGovernance` and not
// in `conformance`.
func govRequireViolationAreas(t *testing.T, run *conformance.Run) {
	t.Helper()

	area := run.Areas.ADRGovernance
	if govAnyOf(area.Verdict != govViolationArea, area.Counts["violation"] != 1, area.Counts["findings"] != 1) {
		t.Errorf("areas.adrGovernance = %+v", area)
	}

	if run.Areas.Conformance.Counts["findings"] != 0 || run.Areas.Conformance.Counts["violation"] != 0 {
		t.Errorf("areas.conformance = %+v: a governance finding is not a conformance finding", run.Areas.Conformance)
	}
}

// govRequireDecisionInputs fails the test unless the run has the decision summary and the register as its one input.
func govRequireDecisionInputs(t *testing.T, run *conformance.Run) {
	t.Helper()

	if govAnyOf(run.DecisionSummary == nil, len(run.Inputs.Decisions) != 1) {
		t.Fatalf("decisionSummary %+v inputs %+v", run.DecisionSummary, run.Inputs.Decisions)
	}

	if run.Inputs.Decisions[0].Path != govRegisterPath {
		t.Fatalf("decisionSummary %+v inputs %+v", run.DecisionSummary, run.Inputs.Decisions)
	}
}

// govRequireMandatoryFinding fails the test unless the run has the governance finding, in its area, with its block.
func govRequireMandatoryFinding(t *testing.T, run *conformance.Run) {
	t.Helper()

	finding := govFindingIn(run)
	if finding == nil {
		t.Fatalf("finding = %+v", finding)
	}

	if govAnyOf(finding.Area != conformance.AreaADRGovernance, finding.Enforcement != policy.EnforcementMandatory, finding.Decision == nil) {
		t.Fatalf("finding = %+v", finding)
	}
}

// govRequireResultsFile fails the test unless results.json validates and has the new keys of the governance result.
func govRequireResultsFile(t *testing.T, dir string) {
	t.Helper()

	results := testutil.ReadFile(t, filepath.Join(dir, "build", "concordia", "results.json"))
	if errs := conformance.ValidateResultJSON([]byte(results)); len(errs) > 0 {
		t.Errorf("results.json does not validate: %v", errs)
	}

	var tree struct {
		SchemaVersion   string `json:"schemaVersion"`
		DecisionSummary struct {
			Decisions []struct {
				ID string `json:"id"`
			} `json:"decisions"`
		} `json:"decisionSummary"`
		Inputs struct {
			Decisions []conformance.InputRef `json:"decisions"`
		} `json:"inputs"`
	}

	err := json.Unmarshal([]byte(results), &tree)
	if govAnyOf(err != nil, tree.SchemaVersion != "1.1", len(tree.DecisionSummary.Decisions) != 1, len(tree.Inputs.Decisions) != 1) {
		t.Errorf("results.json = %+v, err %v", tree, err)
	}
}

// The findings of the hook land in `areas.adrGovernance`, not in `conformance`; the result validates against the
// schema with `decisionSummary`, `inputs.decisions` and the finding `decision` block.
// Scenario: Governance violation blocks.
// Scenario: Area in the machine result.
// Scenario: New result keys.
// Scenario: ADR governance detail is a top-level key.
func TestDecisionFindingsReportInTheGovernanceArea(t *testing.T) {
	t.Parallel()

	hook := gapHook()
	s := govSetup(t, nil, hook)

	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		onlyBoundary(o)
		o.SkipReports = false
	})

	govRequireViolationAreas(t, run)

	if run.Summary.ExitCode != diag.Violation || run.Summary.Verdict != conformance.VerdictViolation {
		t.Errorf("summary = %+v", run.Summary)
	}

	govRequireDecisionInputs(t, run)
	govRequireMandatoryFinding(t, run)
	govRequireResultsFile(t, s.dir)
}

// govRollbackWorkspace is the governance workspace with a `check.rollback` and an explicit `mandatory` entry for the
// decision-governance family, which the rollback overrides.
func govRollbackWorkspace() string {
	const check = "check: {minimumCoverage: 0.5, unknown: fail, failOn: error}\n"

	return strings.Replace(govWorkspace(), check, "check:\n  minimumCoverage: 0.5\n  unknown: fail\n  failOn: error\n"+
		"  enforcement:\n    - {family: decision-governance, mode: mandatory}\n"+
		"  rollback:\n    reason: register migration\n    authorizedBy: jane.doe\n    expires: \"2099-01-01\"\n", 1)
}

// govRequireRollbackRecorded fails the test unless the summary records the rollback that ran the rules as shadow.
func govRequireRollbackRecorded(t *testing.T, run *conformance.Run) {
	t.Helper()

	rb := run.Summary.Rollback
	if rb == nil {
		t.Fatalf("summary rollback = %+v", rb)
	}

	if rb.AuthorizedBy != "jane.doe" || !strings.Contains(rb.Message, "decision-governance, and review rules ran as shadow") {
		t.Fatalf("summary rollback = %+v", rb)
	}
}

// govRequireShadowFinding fails the test unless the enforcement finding of the run is kept as a shadow violation.
func govRequireShadowFinding(t *testing.T, run *conformance.Run) *conformance.Finding {
	t.Helper()

	gap := govFindingIn(run)
	if gap == nil {
		t.Fatalf("governance finding = %+v, want it kept as a shadow violation", gap)
	}

	if gap.Enforcement != policy.EnforcementShadow || gap.Result != conformance.VerdictViolation {
		t.Fatalf("governance finding = %+v, want it kept as a shadow violation", gap)
	}

	return gap
}

// Scenario: Rollback covers decision-governance rules.
// A live governance violation gives exit 1; with an active rollback the rule runs as shadow, the finding and the
// verdict are kept, the rollback is recorded, and the exit code is 0.
func TestRollbackRunsGovernanceRulesAsShadow(t *testing.T) {
	t.Parallel()

	live := govSetup(t, nil, gapHook()).mustCheck(t, onlyBoundary)
	if live.Summary.ExitCode != diag.Violation {
		t.Fatalf("without a rollback: exit %d, want %d", live.Summary.ExitCode, diag.Violation)
	}

	s := newSetup(t, nil, &setupOpts{ws: govRollbackWorkspace(), graph: buildGraph(graphOpts{noEdges: true})})
	s.deps.EvaluateDecisions = gapHook().fn()

	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		onlyBoundary(o)
		o.SkipReports = false
	})

	if run.Summary.ExitCode != diag.OK || run.Areas.ADRGovernance.Verdict != govViolationArea {
		t.Fatalf("with a rollback: exit %d area %+v, want exit 0 and the violation kept", run.Summary.ExitCode, run.Areas.ADRGovernance)
	}

	gap := govRequireShadowFinding(t, run)
	govRequireRollbackRecorded(t, run)

	results := testutil.ReadFile(t, filepath.Join(s.dir, "build", "concordia", "results.json"))
	if !strings.Contains(results, `"enforcement": "shadow"`) || !strings.Contains(results, gap.Fingerprint) {
		t.Error("results.json keeps the shadow governance finding")
	}
}

func govWaiver(id, fingerprint, expires string) string {
	return `
apiVersion: concordia.io/v1alpha1
kind: ExceptionRegister
waivers:
  - {id: ` + id + `, finding: "` + fingerprint + `", rule: ` + govGapRule + `, owner: team-billing, reason: "tracked in ARCH-7", expires: ` + expires + `}
`
}

func govFingerprint(t *testing.T) string {
	t.Helper()

	return govGapFinding().Fingerprint
}

// govDispositionFinding gives the finding of the enforcement rule, and fails the test when the run has none.
func govDispositionFinding(t *testing.T, run *conformance.Run) *conformance.Finding {
	t.Helper()

	finding := govFindingIn(run)
	if finding == nil {
		t.Fatal("no governance finding")
	}

	return finding
}

// govIsWaivedBy says whether the finding is waived by the waiver.
func govIsWaivedBy(finding *conformance.Finding, waiver string) bool {
	if finding.Disposition != exceptions.DispositionWaived || finding.DispositionRef == nil {
		return false
	}

	return *finding.DispositionRef == waiver
}

// govRuleVerdictIs says whether the enforcement rule has the verdict in the run.
func govRuleVerdictIs(run *conformance.Run, want conformance.RuleVerdict) bool {
	i := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == govGapRule })

	return run.Rules[i].Verdict == want
}

// govWaiverSetup is the governance run with a waiver of the enforcement finding, at the time of the test.
func govWaiverSetup(t *testing.T, now string) *setup {
	t.Helper()

	s := govSetup(t, map[string]string{"exceptions/waivers.yaml": govWaiver("WV-7", govFingerprint(t), "2026-12-31")}, gapHook())
	t.Setenv("CONCORDIA_NOW", now)

	return s
}

func testWaiverGivesWaived(t *testing.T) {
	run := govWaiverSetup(t, "2026-06-01T00:00:00Z").mustCheck(t, onlyBoundary)

	if f := govDispositionFinding(t, run); !govIsWaivedBy(f, "WV-7") {
		t.Errorf("finding = %+v", f)
	}

	if run.Summary.ExitCode != diag.OK || run.Summary.Waived != 1 {
		t.Errorf("summary = %+v", run.Summary)
	}

	if run.Areas.ADRGovernance.Verdict != govViolationArea {
		t.Errorf("the area verdict is %s: a waiver does not change the verdict", run.Areas.ADRGovernance.Verdict)
	}

	if !govRuleVerdictIs(run, conformance.VerdictViolation) {
		t.Error("the rule verdict is not violation")
	}
}

func testBaselineEntryGivesBaselined(t *testing.T) {
	s := govSetup(t, nil, gapHook())
	writeBaseline(t, s.dir, nil, exceptions.BaselineEntry{
		Fingerprint: govFingerprint(t), RuleID: govGapRule, Owner: "team-billing", Reason: "inherited", CreatedAt: "2026-01-01T00:00:00Z",
	})

	run := s.mustCheck(t, onlyBoundary)

	if f := govDispositionFinding(t, run); f.Disposition != exceptions.DispositionBaselined {
		t.Errorf("finding = %+v", f)
	}

	if govAnyOf(run.Summary.ExitCode != diag.OK, run.Summary.Baselined != 1, run.Areas.ADRGovernance.Verdict != govViolationArea) {
		t.Errorf("summary = %+v area = %+v", run.Summary, run.Areas.ADRGovernance)
	}
}

func testExpiredWaiverGivesExitOne(t *testing.T) {
	run := govWaiverSetup(t, "2027-01-01T00:00:00Z").mustCheck(t, onlyBoundary)

	if f := govDispositionFinding(t, run); f.Disposition != exceptions.DispositionExpiredWaiver {
		t.Errorf("finding = %+v", f)
	}

	if run.Summary.ExitCode != diag.Violation || run.Summary.ExpiredWaiver != 1 {
		t.Errorf("summary = %+v", run.Summary)
	}
}

// Dispositions apply to a governance finding: a waiver gives `waived`, a baseline entry gives `baselined`, the
// verdict of the rule and of the area stays `violation`, and an expired waiver gives exit 1.
// Scenario: Waived enforcement gap.
func TestDecisionFindingDispositions(t *testing.T) {
	t.Run("a waiver gives waived", testWaiverGivesWaived)
	t.Run("a baseline entry gives baselined", testBaselineEntryGivesBaselined)
	t.Run("an expired waiver gives exit 1", testExpiredWaiverGivesExitOne)
}

// An unknown rule result follows `check.unknown`: exit 4 by default, 0 with --unknown warn.
// Scenario: Governance unknown blocks by default.
// Scenario: Unknown relaxed by flag.
func TestDecisionUnknownFollowsTheUnknownPolicy(t *testing.T) {
	t.Parallel()

	hook := &fakeDecisionHook{build: func(in *conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
		return &conformance.DecisionEvaluation{
			Summary: govSummary(),
			Rules:   []conformance.RuleResult{govRule(govJustifyRule, conformance.VerdictUnknown, in.OnUnknown, 0)},
		}, nil
	}}
	s := govSetup(t, nil, hook)

	if run := s.mustCheck(t, onlyBoundary); run.Summary.ExitCode != diag.Unknown || run.Areas.ADRGovernance.Verdict != conformance.AreaVerdict(conformance.VerdictUnknown) {
		t.Errorf("default: exit %d area %+v, want exit %d and an unknown area", run.Summary.ExitCode, run.Areas.ADRGovernance, diag.Unknown)
	}

	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		onlyBoundary(o)
		o.Unknown = policy.UnknownWarn
	})

	if run.Summary.ExitCode != diag.OK {
		t.Errorf("--unknown warn: exit %d, want 0", run.Summary.ExitCode)
	}
}

// govNotApplicableHook is a governance hook for a workspace without a register: every governance rule of the plans is
// `not-applicable`.
func govNotApplicableHook(t *testing.T) *fakeDecisionHook {
	t.Helper()

	return &fakeDecisionHook{build: func(in *conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
		if in.Workspace.ADR != nil {
			t.Error("the workspace has a register")
		}

		rules := []conformance.RuleResult{}

		for i := range in.Policies.Plans {
			plan := &in.Policies.Plans[i]
			if plan.Predicate.Family == policy.FamilyDecisionGovernance {
				rule := govRule(plan.RuleID, conformance.VerdictNotApplicable, in.OnUnknown, 0)
				rule.Plan = *plan
				rules = append(rules, rule)
			}
		}

		return &conformance.DecisionEvaluation{Rules: rules}, nil
	}}
}

// govRequireEachRuleOnce fails the test when one of the governance rules of the pack has not exactly one result.
func govRequireEachRuleOnce(t *testing.T, run *conformance.Run) {
	t.Helper()

	seen := map[string]int{}
	for i := range run.Rules {
		seen[run.Rules[i].RuleID]++
	}

	for _, id := range []string{"GOV-ADR-001", "GOV-ADR-006"} {
		if seen[id] != 1 {
			t.Errorf("%s has %d results", id, seen[id])
		}
	}
}

// The pack without a register gives `not-applicable` rules: the area is configured and never `pass`.
func TestADRPackWithoutRegisterCallsTheHook(t *testing.T) {
	t.Parallel()

	hook := govNotApplicableHook(t)
	s := newSetup(t, nil, &setupOpts{ws: wsText(govPackKey), graph: buildGraph(graphOpts{noEdges: true})})
	s.deps.EvaluateDecisions = hook.fn()

	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.Unknown = policy.UnknownIgnore })

	if hook.calls != 1 {
		t.Fatalf("the hook ran %d times", hook.calls)
	}

	area := run.Areas.ADRGovernance
	if govAnyOf(area.Verdict != conformance.AreaVerdict(conformance.VerdictNotApplicable), area.Counts["notApplicable"] != 6, area.Counts["findings"] != 0) {
		t.Errorf("areas.adrGovernance = %+v", area)
	}

	// Evaluate does not give the governance rules a result of its own: each rule appears once.
	govRequireEachRuleOnce(t, run)

	if run.DecisionSummary != nil || len(run.Inputs.Decisions) != 0 {
		t.Errorf("decisionSummary %+v inputs %+v: no register, so none", run.DecisionSummary, run.Inputs.Decisions)
	}
}

// govRequireChangedInput fails the test unless the hook got the changed scope of the one project.
func govRequireChangedInput(t *testing.T, in *conformance.DecisionEvalInput) {
	t.Helper()

	if in == nil {
		t.Fatalf("hook input = %+v", in)
	}

	if govAnyOf(in.Changed == nil, len(in.Projects) != 1) {
		t.Fatalf("hook input = %+v", in)
	}

	if in.Projects[0].ChangedBase != "abc123abc123abc123" {
		t.Fatalf("hook input = %+v", in)
	}
}

// govRequireGapRuleInFull fails the test unless the enforcement rule of the run ran on the full scope.
func govRequireGapRuleInFull(t *testing.T, run *conformance.Run) {
	t.Helper()

	i := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == govGapRule })
	if i < 0 {
		t.Fatalf("rules = %+v", run.Rules)
	}

	if run.Rules[i].ChangedScope != "full" {
		t.Fatalf("rules = %+v", run.Rules)
	}
}

// With --changed, the governance rules run on the full scope and CO-CON-027 lists them.
// Scenario: Changed scope with a stale decision.
func TestChangedKeepsGovernanceRulesInFull(t *testing.T) {
	t.Parallel()

	hook := gapHook()
	s := govSetup(t, nil, hook)
	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{
			Rev: "abc123", Files: []string{billingFile, storeFile, legacyFile},
			Changed: workspace.ChangeSet{Ref: "HEAD", Base: "abc123abc123abc123", Files: []string{billingFile}},
		}
	}

	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		onlyBoundary(o)
		o.Changed = "HEAD"
	})

	govRequireChangedInput(t, hook.input)
	govRequireGapRuleInFull(t, run)

	if len(run.Findings) != 1 {
		t.Errorf("findings = %d: a governance finding is not reduced to the changed files", len(run.Findings))
	}

	d := slices.IndexFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.CON027 })
	if d < 0 || !strings.Contains(run.Diagnostics[d].Message, govGapRule) {
		t.Fatalf("diagnostics = %+v, want CO-CON-027 naming %s", run.Diagnostics, govGapRule)
	}
}
