package conformance_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// A09: the query of one project fails and another project is complete. Obligations that depend on the failed
// project are `unknown` with a coverage reason, obligations of the other project keep their results, and a
// mandatory obligation that depends on the failed project gives exit code 4.

const scopePolicy = `
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: demo-pack, version: 1.0.0}
rules:
  - id: CO-BND-001
    name: Billing must not call pay
    severity: error
    subject: {calmNodes: [billing-service]}
    forbid:
      target: {calmNodes: [pay-service]}
      edges: [HTTP_CALLS]
      traversal: direct
    evidence: {minimumConfidence: heuristic}
  - id: CO-BND-002
    name: Billing must not import fulfillment
    severity: error
    subject: {calmNodes: [billing-service]}
    forbid:
      target: {calmNodes: [fulfillment-service]}
      edges: [IMPORTS]
      traversal: direct
`

const scopeBindings = `
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: b-billing, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "services/billing/**"}]}}
  - {id: b-fulfillment, architectureRef: "calm:node:fulfillment-service", match: {any: [{sourceRoot: "services/fulfillment/**"}]}}
  - {id: b-pay, architectureRef: "calm:node:pay-service", match: {any: [{sourceRoot: "@pay/src/**"}]}}
`

// scopeSetup makes a workspace with the projects demo (billing and fulfillment) and pay. failQuery gives the
// error of the `query_graph` call of pay; nil means no failure.
func scopeSetup(t *testing.T, policyText string, ruleIDs []any, failQuery func() error) *setup {
	t.Helper()

	ws := strings.Replace(wsText(""), "exclude: [\"services/legacy/**\"]}", "exclude: [\"services/legacy/**\"]}\n    - {name: pay, root: pay}", 1)
	files := map[string]string{
		"architecture/a.calm.json":         scopeArchitecture(ruleIDs),
		"bindings/concordia.bindings.yaml": scopeBindings,
		"policies/demo.policy.yaml":        policyText,
	}

	demo, pay := scopeGraphs()

	s := newSetup(t, files, &setupOpts{ws: ws, graph: demo})
	s.deps.CreateClient = func(*workspace.Workspace, *loggerT) observation.Client {
		return scopeClient(testutil.MultiHandler(demo, pay), failQuery)
	}
	s.deps.CreateGit = func(root string) workspace.GitPort { return scopeGit(root) }

	return s
}

// scopeArchitecture is the CALM document of the three services; billing carries the controls of the rules.
func scopeArchitecture(ruleIDs []any) string {
	return mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes": []any{
			calmNode("billing-service", map[string]any{"controls": controlOf(map[string]any{"rule-ids": ruleIDs})}),
			calmNode("fulfillment-service", nil),
			calmNode("pay-service", nil),
		},
		"relationships": []any{},
	})
}

// scopeGraphs gives the fake graphs of the projects demo and pay.
func scopeGraphs() (demo, pay *testutil.FakeGraph) {
	billing := testutil.FileNode("1", billingFile)
	store := testutil.FileNode("2", storeFile)
	demo = testutil.NewFakeGraph("demo", []testutil.FakeNode{billing, store}, nil)

	payFile := testutil.FileNode("1", "src/h.ts")
	handler := testutil.FakeNode{ID: "2", Label: "Function", QN: "src.handler", Path: "src/h.ts", Line: 3}
	pay = testutil.NewFakeGraph("pay", []testutil.FakeNode{payFile, handler}, []testutil.FakeEdge{{Type: "DEFINES", From: &payFile, To: &handler}})
	pay.HeadSHA = "def456"

	return demo, pay
}

// scopeClient wraps the handler of the graphs in a client whose `query_graph` call for pay fails with failQuery.
func scopeClient(inner func(tool string, args map[string]any) (any, error), failQuery func() error) *observation.FakeClient {
	return observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		project, _ := args["project"].(string)
		if failQuery == nil || tool != "query_graph" {
			return inner(tool, args)
		}

		if project == "pay" {
			return nil, failQuery()
		}

		return inner(tool, args)
	}, "")
}

// scopeGit gives the fake git of the project root: pay has its own revision and files.
func scopeGit(root string) *workspace.FakeGit {
	if filepath.Base(root) == "pay" {
		return &workspace.FakeGit{Rev: "def456", Files: []string{"src/h.ts"}}
	}

	return &workspace.FakeGit{Rev: "abc123", Files: []string{billingFile, storeFile}}
}

var bothRules = []any{"CO-BND-001", "CO-BND-002"}

func cbmQueryFails() error { return diag.Fail(diag.OBS001, diag.Detail("query_graph: boom")) }

func verdictOf(t *testing.T, run *conformance.Run, ruleID string) *conformance.RuleResult {
	t.Helper()

	for i := range run.Rules {
		if run.Rules[i].RuleID == ruleID {
			return &run.Rules[i]
		}
	}

	t.Fatalf("no result for %s in %v", ruleID, ruleVerdicts(run))

	return nil
}

// assertDependentRuleUnknown checks that the rule that depends on the failed project is unknown with the coverage reason.
func assertDependentRuleUnknown(t *testing.T, dependent *conformance.RuleResult) {
	t.Helper()

	namesPay := func(r conformance.Reason) bool { return r.Code == diag.OBS018 && strings.Contains(r.Message, "pay") }

	if dependent.Verdict != conformance.VerdictUnknown {
		t.Errorf("dependent rule: %s %v", dependent.Verdict, dependent.Reasons)
	}

	if !slices.ContainsFunc(dependent.Reasons, namesPay) {
		t.Errorf("dependent rule: %s %v", dependent.Verdict, dependent.Reasons)
	}
}

// assertPayScopeFailure checks that the coverage section holds the one failure of the project pay, with its reason.
func assertPayScopeFailure(t *testing.T, run *conformance.Run) {
	t.Helper()

	f := run.Coverage.ScopeFailures
	if len(f) != 1 {
		t.Fatalf("scope failures = %+v", f)
	}

	if f[0].Project != "pay" || !strings.Contains(f[0].Reason, "CO-OBS-001") {
		t.Errorf("scope failures = %+v", f)
	}
}

// importsCoverageByProject gives the coverage status of the `imports` predicate of each project.
func importsCoverageByProject(run *conformance.Run) map[string]observation.CoverageStatus {
	status := map[string]observation.CoverageStatus{}

	for i := range run.Coverage.Predicates {
		rec := &run.Coverage.Predicates[i]
		if rec.Predicate == "imports" {
			status[rec.Scope.Project] = rec.Status
		}
	}

	return status
}

func TestA09ProviderFailsOnOneScope(t *testing.T) {
	t.Parallel()

	run := scopeSetup(t, scopePolicy, bothRules, cbmQueryFails).mustCheck(t, nil)

	assertDependentRuleUnknown(t, verdictOf(t, run, "CO-BND-001"))

	independent := verdictOf(t, run, "CO-BND-002")
	if independent.Verdict != conformance.VerdictPass || len(independent.Reasons) != 0 {
		t.Errorf("independent rule: %s %v", independent.Verdict, independent.Reasons)
	}

	// The rule is mandatory (check.unknown: fail): the run is blocked with exit code 4.
	if run.Summary.ExitCode != diag.Unknown {
		t.Errorf("exit code = %d, want %d", run.Summary.ExitCode, diag.Unknown)
	}

	if !slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.OBS018 }) {
		t.Errorf("no CO-OBS-018 in %+v", run.Diagnostics)
	}

	assertPayScopeFailure(t, run)

	status := importsCoverageByProject(run)
	if status["pay"] != observation.CoverageFailed || status["demo"] != observation.CoverageComplete {
		t.Errorf("imports coverage = %v", status)
	}
}

func TestA09FailedScopeUsedByNoRuleChangesNoVerdict(t *testing.T) {
	t.Parallel()

	onlyDemo := strings.Replace(scopePolicy, "CO-BND-001", "CO-X-001", 1)
	head, _, _ := strings.Cut(onlyDemo, "  - id: CO-X-001")
	_, tail, _ := strings.Cut(onlyDemo, "  - id: CO-BND-002")
	onlyDemo = head + "  - id: CO-BND-002" + tail

	run := scopeSetup(t, onlyDemo, []any{"CO-BND-002"}, cbmQueryFails).mustCheck(t, nil)

	if got := verdictOf(t, run, "CO-BND-002"); got.Verdict != conformance.VerdictPass {
		t.Errorf("verdict = %s %v", got.Verdict, got.Reasons)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Errorf("exit code = %d", run.Summary.ExitCode)
	}

	// The failure is still shown in the coverage section.
	if f := run.Coverage.ScopeFailures; len(f) != 1 || f[0].Project != "pay" {
		t.Errorf("scope failures = %+v", f)
	}
}

func TestA09HealthyRunHasNoFailure(t *testing.T) {
	t.Parallel()

	run := scopeSetup(t, scopePolicy, bothRules, nil).mustCheck(t, nil)

	if len(run.Coverage.ScopeFailures) != 0 || run.Coverage.ScopeFailures == nil {
		t.Errorf("scope failures = %#v", run.Coverage.ScopeFailures)
	}

	if got := verdictOf(t, run, "CO-BND-001"); got.Verdict != conformance.VerdictPass {
		t.Errorf("verdict = %s %v", got.Verdict, got.Reasons)
	}
}

func TestA09ConnectionFailureStopsTheRunWithExit3(t *testing.T) {
	t.Parallel()

	for name, fail := range map[string]func() error{
		"graph changed": func() error { return diag.Fail(diag.OBS008, diag.Detail("generation changed")) },
		"cbm crashed":   func() error { return diag.Fail(diag.OBS003, diag.Detail("exited")) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := scopeSetup(t, scopePolicy, bothRules, fail).failureOf(t, nil)
			if e.ExitCode == diag.OK || e.ExitCode == diag.Unknown {
				t.Errorf("exit code = %d (%s)", e.ExitCode, e.Diagnostic.Code)
			}
		})
	}

	// OBS008 is exit 3; OBS003 (the executable) is exit 5.
	if e := scopeSetup(t, scopePolicy, bothRules, func() error { return diag.Fail(diag.OBS008, diag.Detail("x")) }).failureOf(t, nil); e.ExitCode != diag.AdapterFailure {
		t.Errorf("OBS008 exit code = %d", e.ExitCode)
	}

	if e := scopeSetup(t, scopePolicy, bothRules, func() error { return diag.Fail(diag.OBS003, diag.Detail("x")) }).failureOf(t, nil); e.ExitCode != diag.Toolchain {
		t.Errorf("OBS003 exit code = %d", e.ExitCode)
	}
}

func TestA09EveryProjectFailingStopsTheRun(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)

	s.deps.CreateClient = func(*workspace.Workspace, *loggerT) observation.Client {
		return observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
			if tool == "query_graph" {
				return nil, cbmQueryFails()
			}

			return s.graph.Handler(tool, args)
		}, "")
	}

	if e := s.failureOf(t, nil); e.Diagnostic.Code != diag.OBS001 || e.ExitCode != diag.AdapterFailure {
		t.Errorf("code %s exit %d", e.Diagnostic.Code, e.ExitCode)
	}
}
