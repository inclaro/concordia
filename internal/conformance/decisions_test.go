package conformance_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

const (
	govRegisterPath = "docs/adr/register.yaml"
	govADRPath      = "docs/adr/0007-queue.md"
	govGapRule      = "GOV-ADR-004"
	govJustifyRule  = "GOV-ADR-003"
	govPackKey      = "policies:\n  files: [\"policies/*.policy.yaml\"]\n  packs:\n    - use: builtin:adr-governance\n"
)

// govWorkspace is the demo workspace with a decision register.
func govWorkspace() string {
	return wsText("adr:\n  files: [" + govRegisterPath + "]\n")
}

// govPlan is the plan of a governance rule as the hook of a run with the pack, or without it, would give it.
func govPlan(ruleID string, onUnknown policy.UnknownDisposition) policy.RulePlan {
	return policy.RulePlan{
		RuleID: ruleID, Name: ruleID, Severity: policy.SeverityError, Family: string(policy.FamilyDecisionGovernance),
		Pack:                 policy.PackRef{ID: "adr-governance", Version: "1.0.0"},
		Predicate:            policy.Predicate{Family: policy.FamilyDecisionGovernance},
		EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown}, Subjects: []string{}, Targets: []string{},
	}
}

func govRule(ruleID string, verdict conformance.RuleVerdict, onUnknown policy.UnknownDisposition, findings int) conformance.RuleResult {
	return conformance.RuleResult{
		RuleID: ruleID, Name: ruleID, Severity: policy.SeverityError, Policy: policy.PackRef{ID: "adr-governance", Version: "1.0.0"},
		Family: policy.FamilyDecisionGovernance, Verdict: verdict, Reasons: []conformance.Reason{}, Subjects: []string{}, Targets: []string{},
		Plan: govPlan(ruleID, onUnknown), FindingCount: findings,
	}
}

// govGapFinding is an enforcement-gap finding of GOV-ADR-004 with the `decision` block.
func govGapFinding() conformance.Finding {
	return conformance.Finding{
		Fingerprint: conformance.Fingerprint(conformance.Parts{
			RuleID: govGapRule, SourceArchitectureID: govRegisterPath + "#ADR-0007", TargetArchitectureID: "decision", CanonicalEdgeKind: "adr:enforcement-gap",
		}),
		RuleID: govGapRule, Severity: policy.SeverityError, Kind: "decision-enforcement-gap", Message: "ADR-0007 requires enforcement and links no mandatory rule",
		Source: conformance.Endpoint{ArchitectureID: "ADR-0007"}, Target: conformance.Endpoint{ArchitectureID: "decision"},
		EvidencePath: []conformance.EvidenceStep{}, RelatedLocations: []conformance.Location{}, Location: &conformance.Location{Path: govADRPath, Line: 1},
		Result: conformance.VerdictViolation, NextAction: "get review", Area: conformance.AreaADRGovernance,
		Related: []conformance.RelatedRef{{Kind: "decision", ID: "ADR-0007"}},
		Decision: &conformance.FindingDecision{
			ID: "ADR-0007", Title: "Use a queue", Status: "accepted", StatusText: "Accepted", Path: govADRPath, Line: 1,
			Successors: []string{}, Predecessors: []string{}, Governs: []string{"calm:node:billing-service"}, EnforcedBy: []string{},
			CandidateSuccessors: []conformance.CandidateSuccessor{},
		},
	}
}

func govSummary(findings ...string) *conformance.DecisionSummary {
	return &conformance.DecisionSummary{
		ProviderSupport: "supported",
		Decisions: []conformance.DecisionEntry{{
			ID: "ADR-0007", Title: "Use a queue", Path: govADRPath, Line: 1, Status: "accepted", StatusText: "Accepted", Expect: "active",
			Categories: []string{}, Presence: conformance.DecisionState{State: "committed", Reason: "committed"},
			Applicability: conformance.DecisionApplicability{State: "active", Reason: "accepted"},
			Enforcement:   conformance.DecisionEnforcement{State: "unlinked", Required: true, Rules: []string{}},
			Governs:       []string{"calm:node:billing-service"}, Released: []string{}, Successors: []string{}, Predecessors: []string{},
		}},
		History:           conformance.DecisionHistory{Links: []conformance.HistoryLink{}},
		RuleJustification: []conformance.RuleJustification{}, Triggers: []conformance.TriggerResult{},
		Candidates: []conformance.DecisionCandidate{}, Findings: findings,
	}
}

// fakeDecisions is a governance hook with a fixed outcome. It records what it was called with.
type fakeDecisionHook struct {
	input *conformance.DecisionEvalInput
	build func(in *conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error)
	calls int
}

func (h *fakeDecisionHook) fn() conformance.EvaluateDecisionsFunc {
	return func(_ context.Context, in *conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
		h.calls++
		h.input = in

		return h.build(in)
	}
}

// gapHook gives a violation of the enforcement rule with one finding, the summary, and the register input.
func gapHook() *fakeDecisionHook {
	return &fakeDecisionHook{build: func(in *conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
		finding := govGapFinding()

		return &conformance.DecisionEvaluation{
			Summary:  govSummary(finding.Fingerprint),
			Rules:    []conformance.RuleResult{govRule(govGapRule, conformance.VerdictViolation, in.OnUnknown, 1)},
			Findings: []conformance.Finding{finding},
			Inputs:   []conformance.InputRef{{Path: govRegisterPath, Digest: "sha256:" + strings.Repeat("1", 64)}},
		}, nil
	}}
}

// onlyBoundary keeps the other rules of the fixture out of the way: CO-BND-001 passes on a graph with no edges.
func onlyBoundary(o *conformance.CheckOptions) { o.Rules = []string{"CO-BND-001"} }

func govSetup(t *testing.T, files map[string]string, hook *fakeDecisionHook) *setup {
	t.Helper()

	s := newSetup(t, files, &setupOpts{ws: govWorkspace(), graph: buildGraph(graphOpts{noEdges: true})})
	s.deps.EvaluateDecisions = hook.fn()

	return s
}

// govAnyOf says whether any of the checks of a failure condition holds.
func govAnyOf(checks ...bool) bool { return slices.Contains(checks, true) }

// govFindingIn gives the finding of the enforcement rule in the run, nil when the run has none.
func govFindingIn(run *conformance.Run) *conformance.Finding {
	for i := range run.Findings {
		if run.Findings[i].RuleID == govGapRule {
			return &run.Findings[i]
		}
	}

	return nil
}

// govRequireNoDecisionKeys fails the test when the machine result has a key that only a register gives.
func govRequireNoDecisionKeys(t *testing.T, run *conformance.Run) {
	t.Helper()

	var tree map[string]any
	if err := json.Unmarshal(canonicalOf(t, run), &tree); err != nil {
		t.Fatal(err)
	}

	if _, present := tree["decisionSummary"]; present {
		t.Error("a run without a register has a `decisionSummary` key")
	}

	inputs, _ := tree["inputs"].(map[string]any)
	if _, present := inputs["decisions"]; present {
		t.Error("a run without a register has an `inputs.decisions` key")
	}
}

// Without an `adr` key and without the pack the hook is never called and the result is the one of before: no
// decisionSummary, no inputs.decisions, adrGovernance not configured.
// Scenario: A run without the feature has no new key.
func TestRunWithoutADRGovernanceIsTheRunOfBefore(t *testing.T) {
	t.Parallel()

	without := newSetup(t, nil, nil)
	with := newSetup(t, nil, nil)
	hook := gapHook()
	with.deps.EvaluateDecisions = hook.fn()

	before := without.mustCheck(t, nil)
	after := with.mustCheck(t, nil)

	if hook.calls != 0 {
		t.Errorf("the hook ran %d times for a workspace without a register and without the pack", hook.calls)
	}

	if stable(t, before) != stable(t, after) {
		t.Error("a run with the hook installed differs from a run without it")
	}

	govRequireNoDecisionKeys(t, after)

	if got := after.Areas.ADRGovernance.Verdict; got != conformance.AreaNotConfigured {
		t.Errorf("areas.adrGovernance = %s, want not-configured", got)
	}
}

// An invalid register (an error of the hook) stops the run with exit 2 before any area is produced.
func TestInvalidRegisterStopsTheRun(t *testing.T) {
	t.Parallel()

	hook := &fakeDecisionHook{build: func(*conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
		return nil, diag.Fail(diag.ADR001, diag.Detail(govRegisterPath+": unknown key"))
	}}
	s := govSetup(t, nil, hook)

	run, err := s.check(t, nil)
	if run != nil {
		t.Fatalf("a run was produced: %+v", run.Areas)
	}

	de := s.failureOf(t, nil)
	if de.Diagnostic.Code != diag.ADR001 || de.ExitCode != diag.InvalidInput {
		t.Errorf("error = %+v, want CO-ADR-001 with exit %d", de, diag.InvalidInput)
	}

	if err == nil {
		t.Error("no error")
	}
}

// govRequireHookProject fails the test unless the hook got the one primary project of the run.
func govRequireHookProject(t *testing.T, in *conformance.DecisionEvalInput) {
	t.Helper()

	if len(in.Projects) != 1 {
		t.Errorf("projects = %+v", in.Projects)

		return
	}

	project := &in.Projects[0]
	if govAnyOf(project.Revision != "abc123", project.Prefix != "", project.Git == nil, project.Root == "") {
		t.Errorf("projects = %+v", in.Projects)
	}
}

// The hook gets the frozen state of the run.
func TestDecisionHookGetsTheStateOfTheRun(t *testing.T) {
	t.Parallel()

	hook := gapHook()
	s := govSetup(t, nil, hook)

	s.mustCheck(t, func(o *conformance.CheckOptions) { o.Unknown = policy.UnknownWarn })

	in := hook.input
	if hook.calls != 1 || in == nil {
		t.Fatalf("the hook ran %d times", hook.calls)
	}

	if govAnyOf(in.Workspace == nil, in.Model == nil, in.Policies == nil, in.Evidence == nil) {
		t.Fatalf("input = %+v", in)
	}

	govRequireHookProject(t, in)

	if govAnyOf(in.OnUnknown != policy.UnknownWarn, in.FailOn != diag.SeverityError, in.Changed != nil) {
		t.Errorf("onUnknown %q failOn %q changed %+v", in.OnUnknown, in.FailOn, in.Changed)
	}
}

// A governance plan gets no result from Evaluate: the register gives the results, so a rule never appears twice.
func TestEvaluateSkipsGovernanceRules(t *testing.T) {
	t.Parallel()

	gov := plan(policy.Predicate{Family: policy.FamilyDecisionGovernance, Decisions: "presence"}, func(p *policy.RulePlan) { p.RuleID = "GOV-ADR-001" })

	out := conformance.Evaluate(build(&scenario{observed: observed, plans: []policy.RulePlan{gov, boundary(nil)}}))

	if len(out.RuleResults) != 1 || out.RuleResults[0].RuleID != "R-1" {
		t.Fatalf("results = %+v", out.RuleResults)
	}
}

func TestAreaOfDecisionGovernanceFamily(t *testing.T) {
	t.Parallel()

	if got := conformance.AreaOfFamily(policy.FamilyDecisionGovernance); got != conformance.AreaADRGovernance {
		t.Errorf("AreaOfFamily = %s, want %s", got, conformance.AreaADRGovernance)
	}

	if got := conformance.AreaOfFamily(policy.FamilyView); got != conformance.AreaDiagramCurrency {
		t.Errorf("AreaOfFamily(view) = %s", got)
	}

	if got := conformance.AreaOfFamily(policy.FamilyBoundary); got != conformance.AreaConformance {
		t.Errorf("AreaOfFamily(boundary) = %s", got)
	}
}

// govEncoder gives the JSON text of a result tree and fails the test when it cannot be encoded.
func govEncoder(t *testing.T, tree map[string]any) func() []byte {
	t.Helper()

	return func() []byte {
		data, err := json.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}

		return data
	}
}

// The closed objects of `decisionSummary` reject an unknown key, and a result without it still validates.
// Scenario: ADR governance detail is a top-level key.
func TestResultSchemaClosesTheDecisionSummary(t *testing.T) {
	t.Parallel()

	hook := gapHook()
	s := govSetup(t, nil, hook)
	run := s.mustCheck(t, onlyBoundary)

	var tree map[string]any
	if err := json.Unmarshal(canonicalOf(t, run), &tree); err != nil {
		t.Fatal(err)
	}

	encode := govEncoder(t, tree)

	if errs := conformance.ValidateResultJSON(encode()); len(errs) > 0 {
		t.Fatalf("the result does not validate: %v", errs)
	}

	summary, _ := tree["decisionSummary"].(map[string]any)
	summary["surprise"] = true

	if errs := conformance.ValidateResultJSON(encode()); len(errs) == 0 {
		t.Error("an unknown decisionSummary key validates")
	}

	delete(summary, "surprise")
	delete(tree, "decisionSummary")

	if errs := conformance.ValidateResultJSON(encode()); len(errs) > 0 {
		t.Errorf("a result without decisionSummary does not validate: %v", errs)
	}
}

// govRunParts is the text of the parts of a run that two runs on identical inputs share.
func govRunParts(t *testing.T, run *conformance.Run) string {
	t.Helper()

	text, err := json.Marshal([]any{run.Areas.ADRGovernance, run.Findings, run.DecisionSummary})
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

// Scenario: Deterministic order.
// Two runs on identical inputs give the same area, findings and decision summary, apart from the run identity.
func TestDecisionGovernanceRunsAreDeterministic(t *testing.T) {
	t.Parallel()

	first := govSetup(t, nil, gapHook()).mustCheck(t, onlyBoundary)
	second := govSetup(t, nil, gapHook()).mustCheck(t, onlyBoundary)

	if len(first.Findings) == 0 || first.DecisionSummary == nil {
		t.Fatalf("findings %d summary %v", len(first.Findings), first.DecisionSummary)
	}

	if stable(t, first) != stable(t, second) {
		t.Error("two runs on identical inputs differ")
	}

	if govRunParts(t, first) != govRunParts(t, second) {
		t.Error("the area, the findings or the decision summary differ between two runs")
	}
}
