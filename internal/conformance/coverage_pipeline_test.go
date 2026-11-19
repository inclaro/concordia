package conformance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

const envWorkspace = "environments:\n  - id: prod\n    sources: [\"deploy/**\"]\n"

func hasDiag(run *conformance.Run, code string) bool {
	return slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == code })
}

// allConditionsHold says whether every condition holds.
func allConditionsHold(conds ...bool) bool {
	return !slices.Contains(conds, false)
}

func TestRunCoverageSectionOfAPlainRun(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	cov := run.Coverage

	if !allConditionsHold(len(cov.Predicates) > 0, cov.ScopeFailures != nil, cov.IndexResults != nil, cov.Limits.Breaches != nil, cov.SkipReasons != nil) {
		t.Fatalf("coverage = %+v", cov)
	}

	if cov.Skipped != (conformance.SkipCounts{}) {
		t.Errorf("skipped = %+v", cov.Skipped)
	}

	requireProjectScopedRecords(t, run)

	if !indexResultIsMissing(run) {
		t.Errorf("index results = %+v", cov.IndexResults)
	}

	requireRuleTraversesFamilies(t, run, "CO-BND-001", "imports", "calls")
}

// requireProjectScopedRecords checks one record for each family in the project, with no environment declared.
func requireProjectScopedRecords(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Coverage.Predicates {
		scope := run.Coverage.Predicates[i].Scope
		if scope.Project != "demo" || scope.Environment != "" {
			t.Fatalf("record scope = %+v", scope)
		}
	}
}

// indexResultIsMissing says whether the run has one index result, and it is unavailable for want of `concordia index`.
func indexResultIsMissing(run *conformance.Run) bool {
	results := run.Coverage.IndexResults

	return len(results) == 1 && !results[0].Available && strings.Contains(results[0].Reason, "concordia index")
}

// requireRuleTraversesFamilies checks that the rule shows the records of the families it traverses.
func requireRuleTraversesFamilies(t *testing.T, run *conformance.Run, ruleID string, families ...string) {
	t.Helper()

	rule := verdictOf(t, run, ruleID)
	names := make([]string, 0, len(rule.Coverage.Predicates))

	for i := range rule.Coverage.Predicates {
		names = append(names, rule.Coverage.Predicates[i].Predicate)
	}

	for _, family := range families {
		if !slices.Contains(names, family) {
			t.Errorf("rule predicates = %v", names)

			return
		}
	}
}

func TestRunCoverageIsTheSameForTheSameInputs(t *testing.T) {
	t.Parallel()

	encode := func() string {
		run := newSetup(t, nil, nil).mustCheck(t, nil)

		data, err := json.Marshal(run.Coverage)
		if err != nil {
			t.Fatal(err)
		}

		return string(data)
	}

	first := encode()
	if second := encode(); first != second {
		t.Error("the coverage sections differ for the same inputs")
	}
}

// prodPredicateRecord gives the record of the predicate in the prod environment.
func prodPredicateRecord(t *testing.T, run *conformance.Run, predicate string) *observation.PredicateCoverage {
	t.Helper()

	for i := range run.Coverage.Predicates {
		rec := &run.Coverage.Predicates[i]
		if rec.Predicate == predicate && rec.Scope.Environment == "prod" {
			return rec
		}
	}

	t.Fatalf("no %s record for prod", predicate)

	return nil
}

// indexResultIsRecorded says whether the first index result of the run is available and holds the excluded
// directories and the partly parsed error range that the test wrote.
func indexResultIsRecorded(run *conformance.Run) bool {
	state := run.Coverage.IndexResults[0]

	return state.Available && slices.Equal(state.Record.ExcludedDirs, []string{".git", "deploy"}) && state.Record.ParsePartial[0].ErrorRanges == "1-4"
}

// requireExcludedDirectoryGap checks the gap that the default excluded `deploy/` directory leaves in the record.
func requireExcludedDirectoryGap(t *testing.T, rec *observation.PredicateCoverage) {
	t.Helper()

	if rec.Status != observation.CoveragePartial || !rec.HasLossMode(observation.LossDefaultExcludedDirectory) {
		t.Fatalf("with the excluded directory: %+v", rec)
	}

	idx := slices.IndexFunc(rec.LossModes, func(m observation.LossMode) bool { return m.ID == observation.LossDefaultExcludedDirectory })

	gap := rec.LossModes[idx].Gaps[0]
	if gap.Source != "deploy/**" || !strings.Contains(gap.Hint, "`!deploy/`") {
		t.Errorf("gap = %+v", gap)
	}
}

// requireRasterNotIndexed checks that the raster image is recorded as not indexed in the project scope.
func requireRasterNotIndexed(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Coverage.Predicates {
		p := &run.Coverage.Predicates[i]
		if p.Predicate == "diagram-identity" && p.Scope.Project == "demo" && !p.HasLossMode(observation.LossRasterNotIndexed) {
			t.Errorf("diagram-identity = %+v", p)
		}
	}
}

func TestIndexResultInTheRunRecord(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, &setupOpts{ws: wsText(envWorkspace)})
	first := s.mustCheck(t, nil)

	// No index result: the environment sources are unknown with the reason "index result unavailable".
	if rec := prodPredicateRecord(t, first, "deploys-artifact"); rec.Status != observation.CoverageUnknown || !slices.Contains(rec.Reasons, "index result unavailable") {
		t.Errorf("without an index result: %+v", rec)
	}

	// `concordia index` records the result for the graph generation.
	record := observation.IndexRecord{
		Project: "demo", GraphGeneration: first.Run.GraphGeneration, ExcludedDirs: []string{".git", "deploy"},
		NotIndexedFiles: []observation.PathReason{{Path: "docs/context.png", Reason: "ignored-suffix"}},
		ParsePartial:    []observation.PartialFile{{Path: "charts/api/templates/deployment.yaml", ErrorRanges: "1-4"}},
	}
	if err := conformance.WriteIndexRecord(s.dir, conformance.IndexRecordFile, &record); err != nil {
		t.Fatal(err)
	}

	second := s.mustCheck(t, nil)

	if !indexResultIsRecorded(second) {
		t.Fatalf("index result = %+v", second.Coverage.IndexResults[0])
	}

	requireExcludedDirectoryGap(t, prodPredicateRecord(t, second, "deploys-artifact"))

	if !hasDiag(second, diag.OBS020) {
		t.Errorf("no CO-OBS-020 in %+v", second.Diagnostics)
	}

	if len(second.Inputs.IndexResults) != 1 || second.Inputs.IndexResults[0].Path != conformance.IndexRecordFile {
		t.Errorf("inputs = %+v", second.Inputs.IndexResults)
	}

	// With `!deploy/` in .cbmignore the next index result lists only .git: no gap.
	record.ExcludedDirs = []string{".git"}
	if err := conformance.WriteIndexRecord(s.dir, conformance.IndexRecordFile, &record); err != nil {
		t.Fatal(err)
	}

	third := s.mustCheck(t, nil)
	if rec := prodPredicateRecord(t, third, "deploys-artifact"); rec.HasLossMode(observation.LossDefaultExcludedDirectory) || hasDiag(third, diag.OBS020) {
		t.Errorf("a re-included directory has a gap: %+v", rec)
	}

	requireRasterNotIndexed(t, third)
}

func TestIndexResultOfAnotherGenerationIsUnavailable(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, &setupOpts{ws: wsText(envWorkspace)})
	record := observation.IndexRecord{Project: "demo", GraphGeneration: "cbm:demo:other", ExcludedDirs: []string{".git"}}

	if err := conformance.WriteIndexRecord(s.dir, conformance.IndexRecordFile, &record); err != nil {
		t.Fatal(err)
	}

	run := s.mustCheck(t, nil)
	if state := run.Coverage.IndexResults[0]; state.Available || !strings.Contains(state.Reason, "another graph generation") {
		t.Errorf("index result = %+v", state)
	}

	if _, err := os.Stat(filepath.Join(s.dir, conformance.IndexRecordFile)); err != nil {
		t.Fatal(err)
	}
}

// isGraphMaxNodesBreach says whether the breaches are the one breach of the one-node graph limit.
func isGraphMaxNodesBreach(breaches []conformance.LimitBreach) bool {
	return len(breaches) == 1 && breaches[0].Limit == conformance.LimitGraphMaxNodes && breaches[0].Configured == 1 && breaches[0].Observed >= 2
}

// requireNoRulePassesAboveLimit checks that a forbidden dependency is still a violation and the other rules are
// unknown with the reason.
func requireNoRulePassesAboveLimit(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Rules {
		r := &run.Rules[i]
		if r.Verdict == conformance.VerdictPass {
			t.Errorf("%s passes above a limit", r.RuleID)
		}

		if r.Verdict == conformance.VerdictUnknown && !slices.ContainsFunc(r.Reasons, func(x conformance.Reason) bool { return x.Code == diag.CON030 }) {
			t.Errorf("%s is unknown without CO-CON-030: %v", r.RuleID, r.Reasons)
		}
	}

	if run.Summary.Rules.Violation == 0 {
		t.Errorf("the forbidden dependency is no longer a violation: %+v", run.Summary)
	}
}

func TestGraphLimitsInTheRun(t *testing.T) {
	t.Parallel()

	ws := wsText("limits:\n  graph: {maxNodes: 1}\n")
	run := newSetup(t, nil, &setupOpts{ws: ws}).mustCheck(t, func(o *conformance.CheckOptions) { o.KeepEvidence = true })

	if b := run.Coverage.Limits.Breaches; !isGraphMaxNodesBreach(b) {
		t.Fatalf("breaches = %+v", b)
	}

	if !hasDiag(run, diag.CON030) {
		t.Errorf("no CO-CON-030 in %+v", run.Diagnostics)
	}

	// Evidence is not cut: every node that was read is still in the run.
	if len(run.Evidence.Nodes) < 2 {
		t.Errorf("evidence has %d nodes", len(run.Evidence.Nodes))
	}

	// No rule passes above the limit.
	requireNoRulePassesAboveLimit(t, run)

	// Without the limit the run has no breach.
	free := newSetup(t, nil, nil).mustCheck(t, nil)
	if len(free.Coverage.Limits.Breaches) != 0 || hasDiag(free, diag.CON030) {
		t.Errorf("breaches without a limit: %+v", free.Coverage.Limits)
	}
}

func TestSkippedEdgesAreReportedInTheRun(t *testing.T) {
	t.Parallel()

	// The legacy file is excluded from coverage: edges from it are out of scope. They stay in the evidence.
	legacy := testutilLegacyGraph()
	run := newSetup(t, nil, &setupOpts{graph: legacy}).mustCheck(t, func(o *conformance.CheckOptions) { o.KeepEvidence = true })

	if run.Coverage.Skipped.OutOfScope != 1 || run.Coverage.SkipReasons[conformance.SkipOutOfScope] == "" {
		t.Errorf("run skipped = %+v reasons %v", run.Coverage.Skipped, run.Coverage.SkipReasons)
	}

	found := false

	for _, e := range run.Evidence.Edges {
		if e.NativeKind == "IMPORTS" {
			found = true
		}
	}

	if !found {
		t.Error("the skipped edge is not in the evidence")
	}
}

func testutilLegacyGraph() *testutil.FakeGraph {
	billing := testutil.FileNode("1", billingFile)
	store := testutil.FileNode("2", storeFile)
	legacy := testutil.FileNode("3", legacyFile)

	return testutil.NewFakeGraph("demo", []testutil.FakeNode{billing, store, legacy}, []testutil.FakeEdge{{Type: "IMPORTS", From: &billing, To: &legacy}})
}
