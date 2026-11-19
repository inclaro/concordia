package conformance_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func ruleVerdicts(run *conformance.Run) [][2]string {
	out := make([][2]string, 0, len(run.Rules))
	for i := range run.Rules {
		out = append(out, [2]string{run.Rules[i].RuleID, string(run.Rules[i].Verdict)})
	}

	return out
}

// expectAll reports the message with the value when any of the checks is false.
func expectAll(t *testing.T, format string, got any, checks ...bool) {
	t.Helper()

	if slices.Contains(checks, false) {
		t.Errorf(format, got)
	}
}

// requireAll stops the test with the message and the value when any of the checks is false.
func requireAll(t *testing.T, format string, got any, checks ...bool) {
	t.Helper()

	if slices.Contains(checks, false) {
		t.Fatalf(format, got)
	}
}

func TestRunCheckEvaluatesRulesAndReportsEvidence(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)

	if run.SchemaVersion != "1.1" {
		t.Errorf("schemaVersion %s", run.SchemaVersion)
	}

	want := [][2]string{{"CO-BND-001", "violation"}, {"CO-COV-001", "unknown"}, {"CO-DECL-001", "unknown"}}
	if got := ruleVerdicts(run); !slices.Equal(got, want) {
		t.Fatalf("rules %v", got)
	}

	s := run.Summary
	expectAll(t, "summary %+v", s,
		s.Verdict == conformance.VerdictViolation, s.ExitCode == 1, s.Active == 1, s.UnknownRules == 2, s.FailOn == "error", s.UnknownPolicy == "fail")

	if len(run.Findings) != 1 {
		t.Fatalf("findings %d", len(run.Findings))
	}

	assertBoundaryFinding(t, &run.Findings[0])

	if !slices.Equal(evidencePathSteps(run.Findings[0].EvidencePath), []string{billingFile, "IMPORTS", storeFile}) {
		t.Errorf("evidence path %v", evidencePathSteps(run.Findings[0].EvidencePath))
	}
}

// assertBoundaryFinding checks the forbidden dependency of billing on fulfillment that the fixture holds.
func assertBoundaryFinding(t *testing.T, f *conformance.Finding) {
	t.Helper()

	expectAll(t, "finding %+v", f,
		f.RuleID == "CO-BND-001", f.Kind == conformance.KindForbiddenDependency, f.Source.ArchitectureID == "billing-service",
		f.Target.ArchitectureID == "fulfillment-service", f.Disposition == exceptions.DispositionActive)

	if f.Location == nil || f.Location.Path != billingFile {
		t.Errorf("finding %+v", f)
	}

	if f.Owner == nil || *f.Owner != "team-billing" {
		t.Errorf("finding %+v", f)
	}

	if len(f.Fingerprint) != len("sha256:")+64 {
		t.Errorf("fingerprint %s", f.Fingerprint)
	}
}

// evidencePathSteps names each step of an evidence path by the native kind of its edge or the file of its node.
func evidencePathSteps(path []conformance.EvidenceStep) []string {
	var steps []string

	for _, x := range path {
		if x.Edge != nil {
			steps = append(steps, x.Edge.NativeKind)
		} else {
			steps = append(steps, x.Node.FilePath)
		}
	}

	return steps
}

func TestRunCheckUnknownWhenCoverageShort(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-COV-001"} })
	rule := run.Rules[0]

	if rule.Verdict != conformance.VerdictUnknown || len(rule.Reasons) != 2 {
		t.Fatalf("rule %+v", rule)
	}

	if rule.Reasons[0].Code != "CO-CON-021" || rule.Reasons[1].Code != "CO-CON-026" {
		t.Fatalf("rule %+v", rule)
	}

	if !slices.Equal(rule.Coverage.Files.Excluded, []string{legacyFile}) || rule.Coverage.Files.Total != 2 {
		t.Errorf("coverage %+v exit %d", rule.Coverage.Files, run.Summary.ExitCode)
	}

	if run.Summary.ExitCode != 4 {
		t.Errorf("coverage %+v exit %d", rule.Coverage.Files, run.Summary.ExitCode)
	}
}

func TestRunCheckUnknownWarnAndIgnoreExitZero(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)

	for _, unknown := range []string{"warn", "ignore"} {
		run := s.mustCheck(t, func(o *conformance.CheckOptions) {
			o.Rules = []string{"CO-COV-001"}
			o.Unknown = policyUnknown(unknown)
		})

		if run.Rules[0].Verdict != conformance.VerdictUnknown || string(run.Rules[0].Plan.EvidenceRequirements.OnUnknown) != unknown {
			t.Errorf("%s: %+v exit %d", unknown, run.Rules[0], run.Summary.ExitCode)
		}

		if run.Summary.ExitCode != 0 {
			t.Errorf("%s: %+v exit %d", unknown, run.Rules[0], run.Summary.ExitCode)
		}
	}
}

func TestRunCheckMinimumCoverageOverrides(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, func(o *conformance.CheckOptions) {
		o.Rules = []string{"CO-COV-001"}
		cov := 0.0
		o.MinimumCoverage = &cov
	})

	if run.Rules[0].Verdict != conformance.VerdictPass || run.Summary.ExitCode != 0 {
		t.Fatalf("rule %s exit %d", run.Rules[0].Verdict, run.Summary.ExitCode)
	}
}

func TestRunCheckReconciliationConforming(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	want := []conformance.ReconciliationEntry{{
		From: "billing-service", To: "fulfillment-service", RelationshipID: "billing-to-fulfillment",
		Classification: conformance.ClassConforming, ObservedKinds: []observation.CanonicalEdgeKind{observation.KindImports}, EdgeCount: 1,
	}}

	if len(run.Reconciliation) != 1 {
		t.Fatalf("reconciliation %+v", run.Reconciliation)
	}

	got := run.Reconciliation[0]
	requireAll(t, "reconciliation %+v", run.Reconciliation,
		got.From == want[0].From, got.RelationshipID == want[0].RelationshipID, got.Classification == want[0].Classification,
		slices.Equal(got.ObservedKinds, want[0].ObservedKinds), got.EdgeCount == 1)
}

func TestRunCheckUndeclaredEdgeIsViolation(t *testing.T) {
	t.Parallel()

	graph := buildGraph(graphOpts{})
	store := testutil.FileNode("2", storeFile)
	billing := testutil.FileNode("1", billingFile)
	graph.Edges = append(graph.Edges, testutil.FakeEdge{Type: "IMPORTS", From: &store, To: &billing})

	run := newSetup(t, nil, &setupOpts{graph: graph}).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-DECL-001"} })

	if run.Rules[0].Verdict != conformance.VerdictViolation {
		t.Fatalf("verdict %s", run.Rules[0].Verdict)
	}

	f := run.Findings[0]
	expectAll(t, "finding %+v", f, f.Kind == conformance.KindUndeclared, f.Source.ArchitectureID == "fulfillment-service", f.Target.ArchitectureID == "billing-service")
}

func TestRunCheckUnknownRuleIsUsageError(t *testing.T) {
	t.Parallel()

	e := newSetup(t, nil, nil).failureOf(t, func(o *conformance.CheckOptions) { o.Rules = []string{"NOPE-1"} })
	if e.Diagnostic.Code != "CO-WRK-010" || e.ExitCode != 2 {
		t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestRunCheckFailsOnRequiredBindingWithoutMatch(t *testing.T) {
	t.Parallel()

	ghost := `  - {id: b-ghost, architectureRef: "calm:node:legacy-service", required: true, match: {any: [{sourceRoot: "services/ghost/**"}]}}` + "\n"
	s := newSetup(t, map[string]string{"bindings/concordia.bindings.yaml": bindingsText + ghost}, nil)

	e := s.failureOf(t, nil)
	if e.Diagnostic.Code != "CO-BND-004" || e.ExitCode != 2 {
		t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestRunCheckWarnsWithoutRevision(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{Rev: "", Files: []string{billingFile, storeFile, legacyFile}}
	}

	run := s.mustCheck(t, nil)
	if run.Run.SourceRevision != nil {
		t.Errorf("sourceRevision = %v", *run.Run.SourceRevision)
	}

	if !slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == "CO-WRK-013" }) {
		t.Errorf("diagnostics %+v", run.Diagnostics)
	}
}

func TestRunCheckBadFlagValues(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	bad := 2.0

	if e := s.failureOf(t, func(o *conformance.CheckOptions) { o.MinimumCoverage = &bad }); e.Diagnostic.Code != "CO-WRK-010" {
		t.Errorf("minimum coverage: %s", e.Diagnostic.Code)
	}

	if e := s.failureOf(t, func(o *conformance.CheckOptions) { o.Projects = []string{"other"} }); e.Diagnostic.Code != "CO-WRK-010" {
		t.Errorf("project: %s", e.Diagnostic.Code)
	}

	if e := s.failureOf(t, func(o *conformance.CheckOptions) { o.Unknown = "maybe" }); e.Diagnostic.Code != "CO-WRK-010" {
		t.Errorf("unknown: %s", e.Diagnostic.Code)
	}
}

func TestRunCheckListsControls(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)

	if len(run.Controls) != 1 {
		t.Fatalf("controls %+v", run.Controls)
	}

	c := run.Controls[0]
	expectAll(t, "control %+v", c,
		c.ControlID == "control-1", c.Scope.Level == "node", c.Scope.ID == "billing-service", slices.Equal(c.RuleIDs, []string{"CO-BND-001"}),
		c.Verdict == conformance.VerdictViolation)
}

func TestRunCheckControlKeepsRulesOfControl(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, func(o *conformance.CheckOptions) { o.Controls = []string{"control-1"} })

	if len(run.Rules) != 1 || run.Rules[0].RuleID != "CO-BND-001" {
		t.Fatalf("rules %v", ruleVerdicts(run))
	}
}

func TestRunCheckControlErrors(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	if e := s.failureOf(t, func(o *conformance.CheckOptions) { o.Controls = []string{"nope"} }); e.Diagnostic.Code != "CO-WRK-010" {
		t.Errorf("unknown control: %s", e.Diagnostic.Code)
	}

	bad := mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes": []any{
			calmNode("billing-service", map[string]any{"controls": controlOf(map[string]any{"rule-ids": []any{"CO-MISSING-9"}})}),
			calmNode("fulfillment-service", nil), calmNode("legacy-service", nil),
		},
		"relationships": []any{},
	})

	e := newSetup(t, map[string]string{"architecture/a.calm.json": bad}, nil).failureOf(t, nil)
	if e.Diagnostic.Code != "CO-POL-005" || e.ExitCode != 2 {
		t.Errorf("unknown rule in control: %s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestReadIndexedRevision(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"build/concordia/coverage.json": `{"project":"demo","sourceRevision":"abc"}`})

	if got := conformance.ReadIndexedRevision(dir, conformance.CoverageFile, "demo"); got != "abc" {
		t.Errorf("got %q", got)
	}

	if got := conformance.ReadIndexedRevision(dir, conformance.CoverageFile, "other"); got != "" {
		t.Errorf("other project: %q", got)
	}

	if got := conformance.ReadIndexedRevision(t.TempDir(), conformance.CoverageFile, "demo"); got != "" {
		t.Errorf("missing file: %q", got)
	}
}

func TestComputeExitCodeOrder(t *testing.T) {
	t.Parallel()

	rules := []conformance.RuleResult{{Verdict: conformance.VerdictError}, {Verdict: conformance.VerdictViolation}}
	findings := []conformance.Finding{{Severity: "error", Disposition: exceptions.DispositionActive}}

	if got := conformance.ComputeExitCode(rules, findings, "error"); got != 7 {
		t.Errorf("error verdict: %d", got)
	}

	if got := conformance.ComputeExitCode(rules[1:], findings, "error"); got != 1 {
		t.Errorf("active finding: %d", got)
	}

	warn := []conformance.Finding{{Severity: "warning", Disposition: exceptions.DispositionActive}}
	if got := conformance.ComputeExitCode(nil, warn, "error"); got != 0 {
		t.Errorf("warning under failOn error: %d", got)
	}

	if got := conformance.ComputeExitCode(nil, warn, "warning"); got != 1 {
		t.Errorf("warning under failOn warning: %d", got)
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()

	decode(t, []byte(testutil.ReadFile(t, path)), into)
}
