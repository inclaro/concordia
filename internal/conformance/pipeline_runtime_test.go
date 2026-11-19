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

const runtimeBindings = `
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: b-billing, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "services/billing/**"}]}}
  - {id: b-fulfillment, architectureRef: "calm:node:fulfillment-service", match: {any: [{sourceRoot: "services/fulfillment/**"}]}}
`

const runtimeWorkspace = `
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata: {name: demo}
architecture: {source: architecture/a.calm.json}
observation:
  projects:
    - {name: demo, root: ., include: ["services/**"], exclude: ["services/legacy/**"]}
  runtime: {traces: ["traces/*.jsonl"]}
check: {minimumCoverage: 0.5, unknown: fail, failOn: error}
`

const runtimePolicy = `
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: demo-pack, version: 1.0.0}
rules:
  - id: CO-REQ-001
    name: Required relationships are observed
    severity: error
    subject: {nodeTypes: [service]}
    observedRequired: {}
`

func runtimeArch() string {
	otel := func(name string) map[string]any {
		return map[string]any{"concordia": map[string]any{"bindings": []any{map[string]any{"kind": "otel-service", "value": name}}}}
	}

	return mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":   []any{calmNode("billing-service", otel("billing")), calmNode("fulfillment-service", otel("fulfillment"))},
		"relationships": []any{map[string]any{
			"unique-id": "billing-to-fulfillment",
			"relationship-type": map[string]any{"connects": map[string]any{
				"source": map[string]any{"node": "billing-service"}, "destination": map[string]any{"node": "fulfillment-service"},
			}},
			"concordia": map[string]any{"observation": map[string]any{
				"allowed-edge-kinds": []any{"calls-service"}, "required": true,
			}},
		}},
	})
}

func spanLine(service, spanID, parentID string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"` + service +
		`"}}]},"scopeSpans":[{"spans":[{"traceId":"t1","spanId":"` + spanID + `","parentSpanId":"` + parentID +
		`","endTimeUnixNano":"1760000000000000000"}]}]}]}` + "\n"
}

func TestRunCheckRuntimeTraceSatisfiesRequiredRelationship(t *testing.T) {
	t.Parallel()

	rules := func(o *conformance.CheckOptions) { o.Rules = []string{"CO-REQ-001"} }
	files := func(trace string) map[string]string {
		return map[string]string{
			"concordia.yaml":                   runtimeWorkspace,
			"bindings/concordia.bindings.yaml": runtimeBindings,
			"architecture/a.calm.json":         runtimeArch(),
			"policies/demo.policy.yaml":        runtimePolicy,
			"traces/a.jsonl":                   trace,
		}
	}

	linked := newSetup(t, files(spanLine("billing", "s1", "")+spanLine("fulfillment", "s2", "s1")), nil).mustCheck(t, rules)
	if linked.Rules[0].Verdict != conformance.VerdictPass || len(linked.Findings) != 0 {
		t.Fatalf("with a parent span: %s, findings %+v", linked.Rules[0].Verdict, linked.Findings)
	}

	if len(linked.Inputs.Traces) != 1 || linked.Inputs.Traces[0].Path != "traces/a.jsonl" {
		t.Errorf("trace inputs %+v", linked.Inputs.Traces)
	}

	unlinked := newSetup(t, files(spanLine("billing", "s1", "")+spanLine("fulfillment", "s2", "")), nil).mustCheck(t, rules)
	if len(unlinked.Findings) != 1 || unlinked.Findings[0].Kind != conformance.KindUnobserved {
		t.Fatalf("without a parent span: %s, findings %+v", unlinked.Rules[0].Verdict, unlinked.Findings)
	}
}

const multiPolicy = `
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
`

const multiBindings = `
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: b-billing, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "services/billing/**"}]}}
  - {id: b-pay, architectureRef: "calm:node:pay-service", match: {any: [{sourceRoot: "@pay/src/**"}]}}
`

func multiSetup(t *testing.T, crossTarget, crossPath string) *setup {
	t.Helper()

	arch := mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes": []any{
			calmNode("billing-service", map[string]any{"controls": controlOf(nil)}),
			calmNode("pay-service", nil),
		},
		// No relationships: the cross-repository edge is undeclared. The key is required by the CALM rules.
		"relationships": []any{},
	})

	ws := strings.Replace(wsText(""), "exclude: [\"services/legacy/**\"]}", "exclude: [\"services/legacy/**\"]}\n    - {name: pay, root: pay}", 1)
	files := map[string]string{
		"architecture/a.calm.json":         arch,
		"bindings/concordia.bindings.yaml": multiBindings,
		"policies/demo.policy.yaml":        multiPolicy,
	}

	billing := testutil.FileNode("1", billingFile)
	route := testutil.FakeNode{ID: "20", Label: "Route", QN: crossTarget, Path: crossPath, Line: 3}
	demo := testutil.NewFakeGraph("demo", []testutil.FakeNode{billing, route},
		[]testutil.FakeEdge{{Type: "CROSS_HTTP_CALLS", From: &billing, To: &route}})

	payFile := testutil.FileNode("1", "src/h.ts")
	handler := testutil.FakeNode{ID: "2", Label: "Function", QN: "src.handler", Path: "src/h.ts", Line: 3}
	pay := testutil.NewFakeGraph("pay", []testutil.FakeNode{payFile, handler}, []testutil.FakeEdge{{Type: "DEFINES", From: &payFile, To: &handler}})

	s := newSetup(t, files, &setupOpts{ws: ws, graph: demo})
	s.deps.CreateClient = func(*workspace.Workspace, *loggerT) observation.Client {
		return observation.NewFakeClient(testutil.MultiHandler(demo, pay), "")
	}
	s.deps.CreateGit = func(root string) workspace.GitPort {
		if filepath.Base(root) == "pay" {
			return &workspace.FakeGit{Rev: "def456", Files: []string{"src/h.ts"}}
		}

		return &workspace.FakeGit{Rev: "abc123", Files: []string{billingFile}}
	}

	pay.HeadSHA = "def456"

	return s
}

func TestRunCheckProjectsCrossRepositoryEdge(t *testing.T) {
	t.Parallel()

	run := multiSetup(t, "pay.src.handler", "src/h.ts").mustCheck(t, nil)

	assertMultiProjectRun(t, run)

	if len(run.Findings) != 1 {
		t.Fatalf("findings %+v diagnostics %+v", run.Findings, run.Diagnostics)
	}

	if paths := evidenceNodeFiles(run.Findings[0].EvidencePath); !slices.Equal(paths, []string{billingFile, "@pay/src/h.ts"}) {
		t.Errorf("evidence path %v", paths)
	}

	if run.Findings[0].Target.ArchitectureID != "pay-service" {
		t.Errorf("target %+v", run.Findings[0].Target)
	}
}

// assertMultiProjectRun checks that the run names both projects and the revision of each.
func assertMultiProjectRun(t *testing.T, run *conformance.Run) {
	t.Helper()

	projects := run.Run.Projects
	if len(projects) != 2 {
		t.Fatalf("projects %+v", projects)
	}

	if projects[0].Name != "demo" || projects[1].Name != "pay" || *projects[1].SourceRevision != "def456" {
		t.Fatalf("projects %+v", projects)
	}

	if !strings.HasPrefix(run.Run.GraphGeneration, "multi:") || run.Run.Project != "demo" {
		t.Errorf("run %+v", run.Run)
	}

	if *run.Run.SourceRevision != "abc123" {
		t.Errorf("run %+v", run.Run)
	}
}

// evidenceNodeFiles gives the file of each node of an evidence path; a step that is an edge has none.
func evidenceNodeFiles(path []conformance.EvidenceStep) []string {
	var files []string

	for _, step := range path {
		if step.Node != nil {
			files = append(files, step.Node.FilePath)
		}
	}

	return files
}

func TestRunCheckProjectsDropsUnresolvedCrossEdge(t *testing.T) {
	t.Parallel()

	run := multiSetup(t, "pay.src.unknown", "src/zzz.ts").mustCheck(t, nil)

	if len(run.Findings) != 0 {
		t.Errorf("findings %d diagnostics %+v", len(run.Findings), run.Diagnostics)
	}

	if !slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == "CO-OBS-015" }) {
		t.Errorf("findings %d diagnostics %+v", len(run.Findings), run.Diagnostics)
	}
}

// rollbackWorkspaceText has a mandatory typed rule family and a rollback that expires on the given day.
func rollbackWorkspaceText(expires string) string {
	return typedWorkspace("  enforcement:\n    - {family: deploy, mode: mandatory}\n" +
		"  rollback:\n    reason: false positives on the new rules\n    authorizedBy: jane.doe\n    expires: \"" + expires + "\"\n")
}

// Scenario: MIG-06 rollback recorded.
// MIG-06: an active rollback runs the typed rules as shadow, keeps the findings, and records itself in the manifest and the summary.
func TestMIG06RollbackIsRecordedAndTypedRulesRunAsShadow(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    family: deploy\n", forbidImports))
	s := newSetup(t, files, &setupOpts{ws: rollbackWorkspaceText("2099-01-01")})
	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		o.SkipReports = false
		o.Rules = []string{"CO-TYP-001"}
	})

	if len(run.Findings) != 1 {
		t.Fatalf("findings = %+v, rules = %+v: the finding is kept as shadow", run.Findings, run.Rules)
	}

	if run.Findings[0].Enforcement != "shadow" || run.Rules[0].Enforcement != "shadow" {
		t.Fatalf("findings = %+v, rules = %+v: the finding is kept as shadow", run.Findings, run.Rules)
	}

	if run.Summary.ExitCode != diag.OK || run.Summary.Verdict != conformance.VerdictViolation {
		t.Fatalf("exit %d verdict %s: the violation is kept and does not gate", run.Summary.ExitCode, run.Summary.Verdict)
	}

	rollback := run.Summary.Rollback
	assertRollbackSummary(t, rollback)

	var manifest struct {
		Summary struct {
			Rollback conformance.RunRollback `json:"rollback"`
		} `json:"summary"`
	}

	readJSON(t, filepath.Join(s.dir, ".concordia", "runs", run.Run.ID, "manifest.json"), &manifest)

	if manifest.Summary.Rollback != *rollback {
		t.Fatalf("manifest rollback = %+v, want %+v", manifest.Summary.Rollback, *rollback)
	}

	results := testutil.ReadFile(t, filepath.Join(s.dir, "build", "concordia", "results.json"))
	if !strings.Contains(results, `"enforcement": "shadow"`) || !strings.Contains(results, "new enforcement was disabled") {
		t.Error("results.json keeps the shadow finding and the rollback")
	}
}

// assertRollbackSummary checks that the summary records the rollback of the fixture: reason, who authorized it, when it ends.
func assertRollbackSummary(t *testing.T, rollback *conformance.RunRollback) {
	t.Helper()

	if rollback == nil {
		t.Fatalf("summary rollback = %+v", rollback)
	}

	requireAll(t, "summary rollback = %+v", rollback,
		rollback.Reason == "false positives on the new rules", rollback.AuthorizedBy == "jane.doe", rollback.Expires == "2099-01-01",
		strings.Contains(rollback.Message, "new enforcement was disabled"))
}

func TestNoRollbackLeavesTheSummaryWithoutOne(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    family: deploy\n", forbidImports))
	run := newSetup(t, files, &setupOpts{ws: typedWorkspace("")}).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} })

	if run.Summary.Rollback != nil {
		t.Fatalf("rollback = %+v, want none", run.Summary.Rollback)
	}
}

func TestAnExpiredRollbackFailsTheCheckWithExit2(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    family: deploy\n", forbidImports))
	s := newSetup(t, files, &setupOpts{ws: rollbackWorkspaceText("2020-01-01")})

	failure := s.failureOf(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} })

	requireAll(t, "failure = %+v", failure,
		failure.Diagnostic.Code == diag.WRK018, failure.ExitCode == diag.InvalidInput, strings.Contains(failure.Error(), "2020-01-01"))
}
