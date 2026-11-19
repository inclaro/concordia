package quality_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/diag"
)

func areaRow(t *testing.T, rows []quality.AreaCoverage, area string) quality.AreaCoverage {
	t.Helper()

	for _, row := range rows {
		if row.Area == area {
			return row
		}
	}

	t.Fatalf("no row for area %s", area)

	return quality.AreaCoverage{}
}

func scenarioIn(id, area string) *quality.Scenario {
	return &quality.Scenario{ID: id, Area: area, Mode: quality.ModeStructural}
}

func resultOf(id string, verdict quality.Verdict) quality.Result {
	return quality.Result{Scenario: id, Mode: quality.ModeStructural, Verdict: verdict}
}

func TestCoverageAreaWithoutScenarioIsNoScenario(t *testing.T) {
	t.Parallel()

	set := &quality.Set{Scenarios: []*quality.Scenario{scenarioIn("QS-1", quality.AreaPerformance)}}
	rows := quality.Coverage(set, map[string]quality.Result{"QS-1": resultOf("QS-1", quality.VerdictPass)}, nil)

	if len(rows) != len(quality.Areas) {
		t.Fatalf("rows = %d, want one per area", len(rows))
	}

	op := areaRow(t, rows, quality.AreaOperability)
	expectLoaded(t, "operability verdict", op.Verdict, quality.AreaNoScenario)
	expectLoaded(t, "operability verdict text", op.Verdict.Text(), "no scenario")
	expectLoaded(t, "operability required", op.Required, false)
	expectLoaded(t, "operability scenarios", len(op.Scenarios), 0)

	if perf := areaRow(t, rows, quality.AreaPerformance); perf.Verdict != quality.AreaPass {
		t.Errorf("performance = %+v", perf)
	}
}

func TestCoverageRequiredAreaWithoutScenarioIsViolationOfEmptySet(t *testing.T) {
	t.Parallel()

	for name, set := range map[string]*quality.Set{
		"empty set":          {},
		"other area covered": {Scenarios: []*quality.Scenario{scenarioIn("QS-1", quality.AreaSecurity)}},
	} {
		rows := quality.Coverage(set, nil, []string{quality.AreaOperability})

		op := areaRow(t, rows, quality.AreaOperability)
		expectLoaded(t, name+": operability verdict", op.Verdict, quality.AreaViolation)
		expectLoaded(t, name+": operability required", op.Required, true)
		expectLoaded(t, name+": operability detail", op.Detail, quality.DetailRequiredAreaNoScenario)

		avail := areaRow(t, rows, quality.AreaAvailability)
		expectLoaded(t, name+": an area that is not required", avail.Verdict, quality.AreaNoScenario)
	}
}

func TestCoverageAreaVerdictIsWorstOfScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct { //nolint:govet // a test table.
		name     string
		verdicts []quality.Verdict
		want     quality.AreaVerdict
	}{
		{"pass and unknown", []quality.Verdict{quality.VerdictPass, quality.VerdictUnknown}, quality.AreaUnknown},
		{"pass and violation", []quality.Verdict{quality.VerdictPass, quality.VerdictViolation}, quality.AreaViolation},
		{"unknown and violation", []quality.Verdict{quality.VerdictUnknown, quality.VerdictViolation}, quality.AreaViolation},
		{"error", []quality.Verdict{quality.VerdictViolation, quality.VerdictError}, quality.AreaError},
		{"all pass", []quality.Verdict{quality.VerdictPass, quality.VerdictPass}, quality.AreaPass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := &quality.Set{}
			results := map[string]quality.Result{}

			for i, v := range tt.verdicts {
				id := string(rune('A' + i))
				set.Scenarios = append(set.Scenarios, scenarioIn(id, quality.AreaOperability))
				results[id] = resultOf(id, v)
			}

			if got := areaRow(t, quality.Coverage(set, results, nil), quality.AreaOperability); got.Verdict != tt.want {
				t.Errorf("verdict = %s, want %s", got.Verdict, tt.want)
			}
		})
	}
}

func TestCoverageScenarioWithoutResultIsUnknown(t *testing.T) {
	t.Parallel()

	set := &quality.Set{Scenarios: []*quality.Scenario{scenarioIn("QS-1", quality.AreaOperability)}}

	got := areaRow(t, quality.Coverage(set, nil, nil), quality.AreaOperability)
	expectLoaded(t, "area verdict", got.Verdict, quality.AreaUnknown)
	expectLoaded(t, "scenario verdict", got.Scenarios[0].Verdict, quality.VerdictUnknown)
}

// expectAreaFindings requires the findings listed next to an area to be the findings of the ids, in order.
func expectAreaFindings(t *testing.T, what string, got []quality.AreaFinding, ids ...string) {
	t.Helper()

	if len(got) != len(ids) {
		t.Fatalf("%s findings = %+v, want %v", what, got, ids)
	}

	for i, id := range ids {
		expectLoaded(t, what+" finding id", got[i].ID, id)
	}
}

func tradeoffCoverageSet() *quality.Set {
	return &quality.Set{
		Scenarios: []*quality.Scenario{scenarioIn("QS-PERF", quality.AreaPerformance), scenarioIn("QS-OPS", quality.AreaOperability)},
		Findings: []*quality.Finding{
			{
				ID: "F-TRADE", Type: quality.FindingTradeoff, Status: quality.FindingOpen, Statement: "caching lowers modifiability",
				Owner: "t", Scenarios: []string{"QS-PERF"}, Areas: []string{quality.AreaModifiability},
			},
			{
				ID: "F-RISK", Type: quality.FindingRisk, Status: quality.FindingOpen, Statement: "no load test",
				Owner: "t", Scenarios: []string{"QS-PERF", "QS-OPS"},
			},
		},
	}
}

func TestCoverageListsOpenTradeoffBesideItsAttribute(t *testing.T) {
	t.Parallel()

	results := map[string]quality.Result{
		"QS-PERF": resultOf("QS-PERF", quality.VerdictPass), "QS-OPS": resultOf("QS-OPS", quality.VerdictPass),
	}

	rows := quality.Coverage(tradeoffCoverageSet(), results, nil)

	mod := areaRow(t, rows, quality.AreaModifiability)
	expectAreaFindings(t, "modifiability", mod.Findings, "F-TRADE")
	expectLoaded(t, "modifiability finding status", mod.Findings[0].Status, quality.FindingOpen)
	expectLoaded(t, "a finding must not change an area verdict", mod.Verdict, quality.AreaNoScenario)

	// The tradeoff is listed only where it concerns; the performance pass does not hide it.
	perf := areaRow(t, rows, quality.AreaPerformance)
	expectLoaded(t, "performance verdict", perf.Verdict, quality.AreaPass)
	expectAreaFindings(t, "performance", perf.Findings, "F-RISK")

	expectAreaFindings(t, "operability", areaRow(t, rows, quality.AreaOperability).Findings, "F-RISK")
}

func TestValidateRequiredAreas(t *testing.T) {
	t.Parallel()

	if errs := quality.ValidateRequiredAreas("concordia.yaml", []string{"operability", "security"}); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}

	errs := quality.ValidateRequiredAreas("concordia.yaml", []string{"operability", "reliability"})
	if len(errs) != 1 {
		t.Fatalf("errors = %+v", errs)
	}

	expectLoaded(t, "code", errs[0].Diagnostic.Code, diag.QUA002)
	expectLoaded(t, "exit code", errs[0].ExitCode, diag.InvalidInput)
	expectLoaded(t, "source file", errs[0].Diagnostic.Source.URI, "concordia.yaml")
}
