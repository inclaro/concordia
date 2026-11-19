package adr_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// Scenario: Pack enabled.
// Scenario: Three states in the report.
// Scenario: ADR in the index but not in the register.
// Scenario: Finding for a superseded decision.
// Scenario: Unregistered successor.
// A12: the superseded accepted decision is the only justification of a mandatory rule.
func TestEvaluateA12SupersededDecisionIsTheOnlyJustification(t *testing.T) {
	t.Parallel()

	out := a12World(t, a12Before).evaluate()

	wantVerdicts(t, out, map[string]conformance.RuleVerdict{
		adr.RulePresence: conformance.VerdictPass, adr.RuleApplicability: conformance.VerdictViolation,
		adr.RuleJustification: conformance.VerdictViolation, adr.RuleEnforcement: conformance.VerdictNotApplicable,
		adr.RuleHistory: conformance.VerdictPass, adr.RuleScope: conformance.VerdictNotApplicable,
	})

	applicability := findingsOf(out, adr.RuleApplicability)
	justification := findingsOf(out, adr.RuleJustification)

	evalRequireEqual(t, "applicability findings", len(applicability), 1)
	evalRequireEqual(t, "justification findings", len(justification), 1)
	evalRequireEqual(t, "findings", len(out.Findings), 2)

	for _, finding := range []conformance.Finding{applicability[0], justification[0]} {
		wantA12CommonFinding(t, &finding)
	}

	wantA12SupersededFinding(t, &applicability[0])
	wantA12UnjustifiedFinding(t, &justification[0])
	wantA12Summary(t, out.Summary)

	if !evalHasDiagnostic(out.Diagnostics, diag.ADR008, evalAnyDiagnostic) {
		t.Errorf("diagnostics = %+v, want CO-ADR-008 for the unregistered candidate", out.Diagnostics)
	}
}

func wantA12CommonFinding(t *testing.T, finding *conformance.Finding) {
	t.Helper()

	want := []conformance.CandidateSuccessor{{ID: "ADR-7", Path: adr7Path, Applied: false}}
	if finding.Decision == nil || !slices.Equal(finding.Decision.CandidateSuccessors, want) {
		t.Errorf("%s: candidate successors = %+v, want ADR-7 with applied false", finding.RuleID, finding.Decision)
	}

	evalWantEqual(t, finding.RuleID+" action", finding.NextAction, adr.NextActionGetReview)
	evalWantEqual(t, finding.RuleID+" area", finding.Area, conformance.AreaADRGovernance)
	evalWantEqual(t, finding.RuleID+" result", finding.Result, conformance.VerdictViolation)
}

func wantA12SupersededFinding(t *testing.T, sup *conformance.Finding) {
	t.Helper()

	evalWantEqual(t, "superseded kind", sup.Kind, "adr-superseded")
	evalWantEqual(t, "superseded status", sup.Decision.Status, "accepted")
	evalWantEqual(t, "superseded status text", sup.Decision.StatusText, "Accepted")
	evalWantEqual(t, "superseded decision path", sup.Decision.Path, adr3Path)
	evalWantEqual(t, "superseded decision line", sup.Decision.Line, 1)
	evalWantEqual(t, "superseded location path", sup.Location.Path, adr3Path)
	evalWantSlice(t, "superseded governs", sup.Decision.Governs, []string{"calm:node:orders-db"})
	evalWantSlice(t, "superseded enforcedBy", sup.Decision.EnforcedBy, []string{coreRule})
	evalWantSlice(t, "superseded successors", sup.Decision.Successors, []string{"ADR-7"})
}

func wantA12UnjustifiedFinding(t *testing.T, just *conformance.Finding) {
	t.Helper()

	evalWantEqual(t, "rule-unjustified architecture ID", just.Source.ArchitectureID, "CORE-DB-001")
	evalWantEqual(t, "rule-unjustified kind", just.Kind, "adr-rule-unjustified")
	evalWantEqual(t, "rule-unjustified path", just.Location.Path, "gov/decisions.yaml")

	if just.Location.Line == 0 {
		t.Errorf("rule-unjustified finding = %+v, want a location line", just)
	}
}

func wantA12Summary(t *testing.T, summary *conformance.DecisionSummary) {
	t.Helper()

	// ADR-0007 governs nothing: it is not an entry and appears as a candidate only.
	evalRequireEqual(t, "decisions", len(summary.Decisions), 1)
	evalRequireEqual(t, "decision ID", summary.Decisions[0].ID, "ADR-3")

	wantA12Entry(t, &summary.Decisions[0])

	wantCandidates := []conformance.DecisionCandidate{{Kind: adr.CandidateUnregistered, ID: "ADR-7", Path: adr7Path}}
	if !slices.Equal(summary.Candidates, wantCandidates) {
		t.Errorf("candidates = %+v", summary.Candidates)
	}

	wantA12History(t, summary.History.Links)
	wantA12RuleJustification(t, summary.RuleJustification)

	if len(summary.Findings) != 2 || !slices.IsSorted(summary.Findings) {
		t.Errorf("summary findings = %v", summary.Findings)
	}
}

func wantA12Entry(t *testing.T, entry *conformance.DecisionEntry) {
	t.Helper()

	evalWantEqual(t, "applicability state", entry.Applicability.State, "superseded")
	evalWantEqual(t, "applicability reason", entry.Applicability.Reason, "superseded-by ADR-7")
	evalWantEqual(t, "status lags", entry.Applicability.StatusLags, true)
	evalWantEqual(t, "presence state", entry.Presence.State, "committed")
	evalWantEqual(t, "enforcement state", entry.Enforcement.State, "not-applicable")
	evalWantEqual(t, "enforcement required", entry.Enforcement.Required, true)
	evalWantSlice(t, "entry successors", entry.Successors, []string{"ADR-7"})
}

func wantA12History(t *testing.T, links []conformance.HistoryLink) {
	t.Helper()

	evalRequireEqual(t, "history links", len(links), 1)
	evalWantEqual(t, "history from", links[0].From, "ADR-7")
	evalWantEqual(t, "history to", links[0].To, "ADR-3")
	evalWantEqual(t, "history provenance", links[0].Provenance, "edge")
	evalWantEqual(t, "history effect", links[0].Effect, "effective")
}

func wantA12RuleJustification(t *testing.T, results []conformance.RuleJustification) {
	t.Helper()

	evalRequireEqual(t, "rule justifications", len(results), 1)
	evalWantEqual(t, "justified rule", results[0].Rule, coreRule)
	evalWantEqual(t, "justification result", results[0].Result, "violation")
	evalWantSlice(t, "justification linkedBy", results[0].LinkedBy, []string{"ADR-3"})
	evalWantEqual(t, "justification mandatory", results[0].Mandatory, true)
}

// A12 closed: a register edit gives ADR-0007 the same scope and makes ADR-0003 historical.
func TestEvaluateA12RegisterEditClosesTheReview(t *testing.T) {
	t.Parallel()

	out := a12World(t, a12After).evaluate()

	wantVerdicts(t, out, map[string]conformance.RuleVerdict{
		adr.RulePresence: conformance.VerdictPass, adr.RuleApplicability: conformance.VerdictPass,
		adr.RuleJustification: conformance.VerdictPass, adr.RuleEnforcement: conformance.VerdictPass,
		adr.RuleHistory: conformance.VerdictPass, adr.RuleScope: conformance.VerdictNotApplicable,
	})

	if len(out.Findings) != 0 {
		t.Errorf("findings = %+v, want none", out.Findings)
	}

	if len(out.Summary.Candidates) != 0 {
		t.Errorf("candidates = %+v, want none: ADR-0007 is registered", out.Summary.Candidates)
	}
}

func TestEvaluateWithoutRegisterAndWithoutRules(t *testing.T) {
	t.Parallel()

	out, err := adr.Evaluate(t.Context(), &conformance.DecisionEvalInput{
		Workspace: &workspace.Workspace{}, Policies: compiledPacks(),
	})
	if out != nil || err != nil {
		t.Errorf("got %+v, %v; want nil, nil", out, err)
	}
}

// Scenario: Register without pack.
func TestEvaluateRegisterWithoutGovernancePack(t *testing.T) {
	t.Parallel()

	w := a12World(t, a12Before)
	w.plans = nil

	out := w.evaluate()

	if len(out.Rules) != 0 || len(out.Findings) != 0 || out.Summary != nil {
		t.Errorf("rules %d findings %d summary %v; want none", len(out.Rules), len(out.Findings), out.Summary)
	}

	evalRequireEqual(t, "diagnostics", len(out.Diagnostics), 1)
	evalWantEqual(t, "diagnostic code", out.Diagnostics[0].Code, diag.ADR007)
	evalWantEqual(t, "diagnostic severity", out.Diagnostics[0].Severity, diag.SeverityInfo)
}

func TestEvaluatePackWithoutRegister(t *testing.T) {
	t.Parallel()

	compiled := compiledPacks()
	compiled.Plans = govPlans()

	out, err := adr.Evaluate(t.Context(), &conformance.DecisionEvalInput{Workspace: &workspace.Workspace{}, Policies: compiled})
	if err != nil {
		t.Fatal(err)
	}

	if out.Summary != nil {
		t.Fatalf("summary %v, want none", out.Summary)
	}

	evalRequireEqual(t, "rules", len(out.Rules), 6)
	evalRequireEqual(t, "findings", len(out.Findings), 0)
	evalRequireEqual(t, "diagnostics", len(out.Diagnostics), 0)

	for i := range out.Rules {
		wantNoRegisterRule(t, &out.Rules[i])
	}
}

func wantNoRegisterRule(t *testing.T, rule *conformance.RuleResult) {
	t.Helper()

	evalWantEqual(t, rule.RuleID+" verdict", rule.Verdict, conformance.VerdictNotApplicable)
	evalWantEqual(t, rule.RuleID+" family", rule.Family, "decision-governance")
	evalWantEqual(t, rule.RuleID+" plan rule", rule.Plan.RuleID, rule.RuleID)

	if len(rule.Reasons) != 1 || rule.Reasons[0].Message != "no register" {
		t.Errorf("%s reasons = %+v, want only no register", rule.RuleID, rule.Reasons)
	}
}

// Only the selected plans give results (--rule).
func TestEvaluateOnlySelectedRules(t *testing.T) {
	t.Parallel()

	w := a12World(t, a12Before)
	w.plans = []policy.RulePlan{w.plans[1]}

	out := w.evaluate()

	evalRequireEqual(t, "rules", len(out.Rules), 1)
	evalWantEqual(t, "rule", out.Rules[0].RuleID, adr.RuleApplicability)
	evalWantEqual(t, "findings", len(out.Findings), 1)
}

func TestEvaluateInvalidRegisterStopsTheRun(t *testing.T) {
	t.Parallel()

	w := a12World(t, "  - id: ADR-0003\n")

	_, err := adr.Evaluate(t.Context(), w.input())
	_ = wantDiag(t, err, diag.ADR001)
}

// Scenario: Provider has no ADR capability.
// A provider without ADR support and a project without revision give unknown results with CO-ADR-006.
func TestEvaluateUnavailableEvidenceIsUnknown(t *testing.T) {
	t.Parallel()

	wantUnsupportedProviderIsUnknown(t)
	wantMissingRevisionIsUnknown(t)
}

func wantUnsupportedProviderIsUnknown(t *testing.T) {
	t.Helper()

	w := a12World(t, a12Before)
	w.unsupportedProvider = true

	out := w.evaluate()
	wantVerdicts(t, out, map[string]conformance.RuleVerdict{
		adr.RulePresence: conformance.VerdictUnknown, adr.RuleApplicability: conformance.VerdictUnknown,
		adr.RuleHistory: conformance.VerdictUnknown,
	})

	findings := findingsOf(out, adr.RulePresence)
	for i := range findings {
		evalWantEqual(t, "finding result", findings[i].Result, conformance.VerdictUnknown)
		evalWantEqual(t, "finding action", findings[i].NextAction, adr.NextActionFixAnalysis)
		evalWantEqual(t, "finding reason", findings[i].Reason, conformance.ReasonUnsupportedFamily)
	}

	isWarning := func(d diag.Diagnostic) bool { return d.Severity == diag.SeverityWarning }
	if !evalHasDiagnostic(out.Diagnostics, diag.ADR006, isWarning) {
		t.Errorf("diagnostics = %+v, want CO-ADR-006", out.Diagnostics)
	}
}

func wantMissingRevisionIsUnknown(t *testing.T) {
	t.Helper()

	in := a12World(t, a12Before).input()
	in.Projects[0].Revision = ""

	res, err := adr.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	wantVerdicts(t, res, map[string]conformance.RuleVerdict{adr.RulePresence: conformance.VerdictUnknown})

	namesRevision := func(d diag.Diagnostic) bool { return strings.Contains(d.Message, "no git revision") }
	if !evalHasDiagnostic(res.Diagnostics, diag.ADR006, namesRevision) {
		t.Errorf("diagnostics = %+v, want CO-ADR-006 naming the revision", res.Diagnostics)
	}
}

// Scenario: Change set adds the decision.
// Scenario: No decision in the change set.
// Scenario: Run without a change set.
// Triggers: no change set gives not-applicable and CO-ADR-009; a change set evaluates them and satisfaction needs a
// changed registered decision with the category.
func TestEvaluateTriggers(t *testing.T) {
	t.Parallel()

	register := `  - id: ADR-0003
    categories: [deployment-strategy]
    governs: [{ref: 'calm:node:orders-db'}]
`
	w := a12World(t, register)
	w.triggers = []workspace.ADRTrigger{{ID: "TRG-DEPLOY", Category: "deployment-strategy", Paths: []string{"deploy/**"}}}

	wantTriggersWithoutChangeSet(t, w)

	w.changed = &conformance.RunChanged{Ref: "main", Base: "abc", Files: []string{"deploy/orders.yaml"}}
	wantUnsatisfiedTrigger(t, w)

	w.changed.Files = []string{"deploy/orders.yaml", adr3Path}
	wantSatisfiedTrigger(t, w)
}

func wantTriggersWithoutChangeSet(t *testing.T, w *world) {
	t.Helper()

	out := w.evaluate()
	evalWantEqual(t, "without change set", ruleOf(t, out, adr.RuleScope).Verdict, conformance.VerdictNotApplicable)

	isInfo := func(d diag.Diagnostic) bool { return d.Severity == diag.SeverityInfo }
	if !evalHasDiagnostic(out.Diagnostics, diag.ADR009, isInfo) {
		t.Errorf("diagnostics = %+v, want CO-ADR-009", out.Diagnostics)
	}
}

func wantUnsatisfiedTrigger(t *testing.T, w *world) {
	t.Helper()

	out := w.evaluate()
	evalRequireEqual(t, "unsatisfied trigger", ruleOf(t, out, adr.RuleScope).Verdict, conformance.VerdictViolation)

	if evalHasDiagnostic(out.Diagnostics, diag.ADR009, evalAnyDiagnostic) {
		t.Errorf("CO-ADR-009 with a change set: %+v", out.Diagnostics)
	}

	f := findingsOf(out, adr.RuleScope)[0]
	evalWantEqual(t, "trigger kind", f.Kind, "adr-trigger-unsatisfied")
	evalWantEqual(t, "trigger action", f.NextAction, adr.NextActionSupplyEvidence)
	evalWantEqual(t, "trigger severity", f.Severity, "warning")
	evalRequireEqual(t, "trigger location set", f.Location != nil, true)
	evalWantEqual(t, "trigger location", f.Location.Path, "deploy/orders.yaml")
}

func wantSatisfiedTrigger(t *testing.T, w *world) {
	t.Helper()

	out := w.evaluate()
	evalWantEqual(t, "satisfied trigger", ruleOf(t, out, adr.RuleScope).Verdict, conformance.VerdictPass)
	evalRequireEqual(t, "trigger summaries", len(out.Summary.Triggers), 1)
	evalWantEqual(t, "trigger satisfied by", out.Summary.Triggers[0].SatisfiedBy[0], "ADR-3")
}

// Scenario: Deterministic order.
// Evaluate reads Git only through the port and gives the same result twice.
func TestEvaluateIsDeterministic(t *testing.T) {
	t.Parallel()

	first, second := a12World(t, a12Before).evaluate(), a12World(t, a12Before).evaluate()

	evalRequireEqual(t, "number of findings", len(first.Findings), len(second.Findings))

	for i := range first.Findings {
		evalWantEqual(t, "fingerprint", first.Findings[i].Fingerprint, second.Findings[i].Fingerprint)
		evalWantEqual(t, "message", first.Findings[i].Message, second.Findings[i].Message)
	}

	samePresence := func(a, b conformance.DecisionEntry) bool { return sameDecisionPresence(&a, &b) }
	if !slices.EqualFunc(first.Summary.Decisions, second.Summary.Decisions, samePresence) {
		t.Error("summaries differ")
	}
}

// sameDecisionPresence compares the ID and the presence of two decision entries.
func sameDecisionPresence(a, b *conformance.DecisionEntry) bool {
	return a.ID == b.ID && a.Presence == b.Presence
}

// A history anomaly is a finding of GOV-ADR-005 and makes the decisions of its component unknown.
func TestEvaluateHistoryAnomaly(t *testing.T) {
	t.Parallel()

	w := newWorld(t, adr3Path, adr7Path)
	w.addADR("1", adr3Path, "ADR-3", "ADR-0003: One", "accepted", "Accepted")
	w.addADR("2", adr7Path, "ADR-7", "ADR-0007: Two", "accepted", "Accepted")
	w.supersede("2", "1")
	w.supersede("1", "2")
	w.register = a12Before

	out := w.evaluate()

	wantVerdicts(t, out, map[string]conformance.RuleVerdict{
		adr.RuleHistory: conformance.VerdictViolation, adr.RuleApplicability: conformance.VerdictUnknown,
		adr.RuleJustification: conformance.VerdictUnknown,
	})

	history := findingsOf(out, adr.RuleHistory)
	evalRequireEqual(t, "history findings", len(history), 1)
	evalWantEqual(t, "history kind", history[0].Kind, "adr-cycle")
	evalWantEqual(t, "history action", history[0].NextAction, adr.NextActionCorrectIntent)

	for _, id := range []string{"ADR-3", "ADR-7"} {
		if !strings.Contains(history[0].Message, id) {
			t.Errorf("history message %q does not name %s", history[0].Message, id)
		}
	}

	app := findingsOf(out, adr.RuleApplicability)[0]
	evalWantEqual(t, "applicability result", app.Result, conformance.VerdictUnknown)
	evalWantEqual(t, "applicability reason", app.Reason, conformance.ReasonEvidenceConflict)
}

// Scenario: Editable ADR workspace text.
// ADR text that exists only in the editable ADR workspace of the provider (`manage_adr`) is no Decision node and no
// tracked file: the evaluation never asks for it, so presence is a violation with reason absent.
func TestEvaluateEditableADRWorkspaceTextIsNoEvidence(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "README.md")
	w.register = a12Before

	out := w.evaluate()

	entry := out.Summary.Decisions[0]
	evalWantEqual(t, "presence", entry.Presence, conformance.DecisionState{State: string(adr.PresenceAbsent), Reason: "absent"})

	wantVerdicts(t, out, map[string]conformance.RuleVerdict{adr.RulePresence: conformance.VerdictViolation})

	for _, call := range w.git.calls {
		if !slices.Contains([]string{"Revision", "DirtyFiles", "ListFilesAt"}, call) {
			t.Errorf("unexpected Git call %s", call)
		}
	}
}

// Scenario: Selector scope is reported.
func TestEvaluateReportsTheResolvedSelectorScope(t *testing.T) {
	t.Parallel()

	out := a12World(t, `  - id: ADR-0003
    governs:
      - select: {tags: [orders-data]}
      - select: {nodeTypes: [service]}
`).evaluate()

	want := []string{"calm:node:billing", "calm:node:orders", "calm:node:orders-db"}
	evalWantSlice(t, "governs (the three resolved nodes)", out.Summary.Decisions[0].Governs, want)
}

// Scenario: Valid decision without enforcement.
func TestEvaluateValidDecisionWithoutEnforcement(t *testing.T) {
	t.Parallel()

	out := a12World(t, "  - id: ADR-0007\n    governs: [{ref: 'calm:node:orders-db'}]\n").evaluate()

	entry := out.Summary.Decisions[0]
	evalWantEqual(t, "presence state", entry.Presence.State, string(adr.PresenceCommitted))
	evalWantEqual(t, "applicability state", entry.Applicability.State, string(adr.ApplicabilityActive))
	evalWantEqual(t, "enforcement state", entry.Enforcement.State, string(adr.EnforcementUnlinked))
	evalWantEqual(t, "enforcement required", entry.Enforcement.Required, false)

	if len(out.Findings) != 0 {
		t.Errorf("findings = %+v, want none", out.Findings)
	}
}

// Scenario: No triggers configured.
func TestEvaluateWithoutTriggersGivesNoTriggerResult(t *testing.T) {
	t.Parallel()

	w := a12World(t, a12Before)
	w.changed = &conformance.RunChanged{Ref: "main", Base: "abc", Files: []string{"deploy/orders.yaml"}}

	out := w.evaluate()

	if len(out.Summary.Triggers) != 0 || len(findingsOf(out, adr.RuleScope)) != 0 {
		t.Errorf("triggers %+v findings %+v, want none", out.Summary.Triggers, findingsOf(out, adr.RuleScope))
	}

	if evalHasDiagnostic(out.Diagnostics, diag.ADR009, evalAnyDiagnostic) {
		t.Errorf("diagnostics = %+v, want no CO-ADR-009 without triggers", out.Diagnostics)
	}
}

// Scenario: Changed scope with a stale decision.
func TestEvaluateChangedScopeStillReportsAStaleDecision(t *testing.T) {
	t.Parallel()

	w := a12World(t, a12Before)
	w.changed = &conformance.RunChanged{Ref: "main", Base: "abc", Files: []string{"services/billing/main.go"}}

	out := w.evaluate()

	applicability := findingsOf(out, adr.RuleApplicability)
	evalRequireEqual(t, "applicability findings", len(applicability), 1)
	evalWantEqual(t, "applicability kind", applicability[0].Kind, "adr-superseded")
	evalWantEqual(t, "justification findings", len(findingsOf(out, adr.RuleJustification)), 1)
}

// Scenario: Unrecognized status.
// Scenario: Status text mapping.
func TestEvaluateShowsTheOriginalStatusAndTheMapping(t *testing.T) {
	t.Parallel()

	register := "  - id: ADR-0003\n    governs: [{ref: 'calm:node:orders-db'}]\n"

	wantUnrecognizedStatusShownAsIs(t, register)
	wantMappedStatusShownWithItsMapping(t, register)
}

func wantUnrecognizedStatusShownAsIs(t *testing.T, register string) {
	t.Helper()

	w := newWorld(t, adr3Path)
	w.addADR("1", adr3Path, "ADR-3", "ADR-0003: Orders", "unknown", "Under review")
	w.register = register

	entry := w.evaluate().Summary.Decisions[0]
	if entry.Applicability.State == string(adr.ApplicabilityActive) {
		t.Errorf("unrecognized status: entry = %+v, want not active", entry)
	}

	evalWantEqual(t, "unrecognized status", entry.Status, "unknown")
	evalWantEqual(t, "unrecognized status text", entry.StatusText, "Under review")
}

func wantMappedStatusShownWithItsMapping(t *testing.T, register string) {
	t.Helper()

	w := newWorld(t, adr3Path)
	w.addADR("1", adr3Path, "ADR-3", "ADR-0003: Orders", "unknown", "Ratified")
	w.register = register

	in := w.input()
	in.Workspace.ADR.Lifecycle.StatusMap = map[string]string{"Ratified": "accepted"}

	out, err := adr.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	entry := out.Summary.Decisions[0]
	evalWantEqual(t, "mapped applicability", entry.Applicability.State, string(adr.ApplicabilityActive))
	evalWantEqual(t, "mapped original status", entry.Status, "unknown")
	evalWantEqual(t, "mapped original text", entry.StatusText, "Ratified")
	evalWantEqual(t, "mapped status", entry.MappedStatus, "accepted")
}

// Scenario: Read-only check.
// Evaluation writes neither the register nor ADR files nor the architecture: every file under the root is unchanged.
func TestEvaluateLeavesTheWorkspaceFilesUnchanged(t *testing.T) {
	t.Parallel()

	in := a12World(t, a12Before).input()

	before := evalSnapshotFiles(t, in.Workspace.Root)
	if len(before) == 0 {
		t.Fatal("the workspace has no register file")
	}

	if _, err := adr.Evaluate(t.Context(), in); err != nil {
		t.Fatal(err)
	}

	if after := evalSnapshotFiles(t, in.Workspace.Root); !maps.Equal(before, after) {
		t.Errorf("files changed: before %v after %v", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	}
}
