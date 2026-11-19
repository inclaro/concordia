package quality_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

var measureNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const measureRevision = "0123456789abcdef0123456789abcdef01234567"

// failoverScenario is the A37 scenario: failover-seconds must be at most 30 s in production.
func failoverScenario() *quality.Scenario {
	return &quality.Scenario{
		ID: "QS-FAILOVER", Mode: quality.ModeMeasured, Environment: "production", Area: quality.AreaAvailability,
		Criterion: quality.Criterion{Metric: "failover-seconds", Unit: "s", Comparison: quality.CompareLTE, Threshold: 30},
	}
}

// predicate is an admissible measurement of the failover scenario; each case changes one thing.
func predicate(value float64) map[string]any {
	return map[string]any{
		"scenario": "QS-FAILOVER", "metric": "failover-seconds", "value": value, "unit": "s", "threshold": 30, "comparison": "lte",
		"testConditions": "3 nodes, 1000 rps", "revision": measureRevision, "environment": "production",
		"evaluator": map[string]any{"name": "k6", "version": "1.0"}, "outcome": "passed",
		"window": map[string]any{"start": "2026-10-30T10:00:00Z", "end": "2026-10-30T11:00:00Z"},
	}
}

func changed(p map[string]any, key string, value any) map[string]any {
	p[key] = value

	return p
}

func withoutKey(p map[string]any, key string) map[string]any {
	delete(p, key)

	return p
}

func window(start, end string) map[string]any { return map[string]any{"start": start, "end": end} }

func verifiedBy(producer string) statement.Verification {
	return statement.Verification{Signature: statement.SignatureVerified, ProducerID: producer, Subject: statement.SubjectUnchecked}
}

// read builds a measurement from a predicate through ReadMeasurement, as the integrator does.
func read(t *testing.T, path string, verification statement.Verification, p map[string]any) quality.Measurement {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"_type":         statement.StatementType,
		"subject":       []statement.Subject{{Name: "rev", Digest: map[string]string{"gitCommit": measureRevision}}},
		"predicateType": statement.PredicateMeasurement, "predicate": p,
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := statement.Parse(path, data)
	if err != nil {
		t.Fatal(err)
	}

	m, diags := quality.ReadMeasurement(&loaded, verification)
	if len(diags) != 0 {
		t.Fatalf("ReadMeasurement: %v", diags)
	}

	return m
}

func measureInput(ms ...quality.Measurement) *quality.MeasuredInput {
	return &quality.MeasuredInput{
		Context:      assurance.Context{Now: measureNow, Environment: "staging-run", Revision: measureRevision},
		Producers:    []workspace.Producer{{ID: "k6-ci", Claims: []string{workspace.ProducerClaimMeasurement}}, {ID: "builder", Claims: []string{workspace.ProducerClaimBuildProvenance}}},
		Measurements: ms,
	}
}

// measuredCase is one case of TestEvaluateMeasuredVerdicts.
type measuredCase struct {
	name         string
	scenario     func(*quality.Scenario)
	verification statement.Verification
	predicate    map[string]any
	verdict      quality.Verdict
	reason       string
	diagnostic   string
	details      []assurance.Detail
}

func measuredVerdictCases() []measuredCase {
	return []measuredCase{
		{name: "A37 above the threshold in the right environment", predicate: predicate(42), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictViolation},
		{name: "within the threshold", predicate: predicate(12), verification: verifiedBy("k6-ci"), verdict: quality.VerdictPass},
		{name: "at the threshold for lte", predicate: predicate(30), verification: verifiedBy("k6-ci"), verdict: quality.VerdictPass},
		{name: "A37 wrong environment", predicate: changed(predicate(42), "environment", "staging"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailDifferentEnvironment}, diagnostic: diag.QUA003},
		{name: "failed run", predicate: changed(predicate(12), "outcome", "failed"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailFailedRun}, diagnostic: diag.QUA003},
		{name: "invalid outcome", predicate: changed(predicate(12), "outcome", "green"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailInvalidOutcome}, diagnostic: diag.QUA003},
		{name: "other revision", predicate: changed(predicate(12), "revision", "feedface"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailDifferentRevision}, diagnostic: diag.QUA003},
		{name: "stale window end", scenario: func(sc *quality.Scenario) { sc.Criterion.MaxAgeDays = 1 },
			predicate: changed(predicate(12), "window", window("2026-10-28T10:00:00Z", "2026-10-28T11:00:00Z")), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailStale}, diagnostic: diag.QUA003},
		{name: "old window without a maximum age is not stale",
			predicate: changed(predicate(12), "window", window("2026-01-01T10:00:00Z", "2026-01-01T11:00:00Z")), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictPass},
		{name: "producer not declared", predicate: predicate(12), verification: verifiedBy("stranger"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailProducerNotTrusted}, diagnostic: diag.QUA003},
		{name: "producer not trusted for measurement", predicate: predicate(12), verification: verifiedBy("builder"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailProducerNotTrustedForClaim}, diagnostic: diag.QUA003},
		{name: "unsigned", predicate: predicate(12),
			verification: statement.Verification{Signature: statement.SignatureNone, Subject: statement.SubjectUnchecked},
			verdict:      quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailNotAuthenticated}, diagnostic: diag.QUA003},
		{name: "threshold mismatch", predicate: changed(predicate(42), "threshold", 60), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailThresholdMismatch}, diagnostic: diag.QUA003},
		{name: "comparison mismatch", predicate: changed(predicate(42), "comparison", "gte"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailThresholdMismatch}, diagnostic: diag.QUA003},
		{name: "unit mismatch is not converted", predicate: changed(predicate(42000), "unit", "ms"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.DetailUnitMismatch}, diagnostic: diag.QUA003},
		{name: "missing test conditions", predicate: withoutKey(predicate(12), "testConditions"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.MissingField("testConditions")}, diagnostic: diag.QUA003},
		{name: "missing value is not a zero", predicate: withoutKey(predicate(12), "value"), verification: verifiedBy("k6-ci"),
			verdict: quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{assurance.MissingField("value")}, diagnostic: diag.QUA003},
		{name: "every reason is recorded",
			predicate:    changed(changed(changed(predicate(42), "environment", "staging"), "outcome", "failed"), "revision", "feedface"),
			verification: verifiedBy("stranger"),
			verdict:      quality.VerdictUnknown, reason: quality.ReasonNoEvidenceSupplied,
			details: []assurance.Detail{
				assurance.DetailDifferentEnvironment, assurance.DetailDifferentRevision, assurance.DetailFailedRun, assurance.DetailProducerNotTrusted,
			}, diagnostic: diag.QUA003},
	}
}

func TestEvaluateMeasuredVerdicts(t *testing.T) {
	t.Parallel()

	for _, tt := range measuredVerdictCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sc := failoverScenario()
			if tt.scenario != nil {
				tt.scenario(sc)
			}

			got := quality.EvaluateMeasured(sc, measureInput(read(t, "build/m.json", tt.verification, tt.predicate)))

			if got.Result.Verdict != tt.verdict || got.Result.Reason != tt.reason {
				t.Fatalf("verdict=%s reason=%q, want %s %q (%v)", got.Result.Verdict, got.Result.Reason, tt.verdict, tt.reason, got.Result.Details)
			}

			if got.Result.Mode != quality.ModeMeasured || got.Result.Scenario != sc.ID {
				t.Errorf("label = %s %s", got.Result.Scenario, got.Result.Mode)
			}

			if len(got.Assessed) != 1 || !slices.Equal(got.Assessed[0].Admission.Details, append([]assurance.Detail{}, tt.details...)) {
				t.Errorf("admission details = %+v, want %v", got.Assessed, tt.details)
			}

			expectMeasuredDiagnostics(t, got.Diagnostics, tt.diagnostic)

			if satisfies := got.Result.Satisfies(sc); satisfies != (tt.verdict == quality.VerdictPass) {
				t.Errorf("Satisfies = %v", satisfies)
			}
		})
	}
}

// expectMeasuredDiagnostics requires no diagnostics when want is empty, and else exactly one with the code.
func expectMeasuredDiagnostics(t *testing.T, got []diag.Diagnostic, want string) {
	t.Helper()

	if want == "" {
		if len(got) != 0 {
			t.Errorf("diagnostics = %v", got)
		}

		return
	}

	if len(got) != 1 || got[0].Code != want {
		t.Errorf("diagnostics = %v, want one %s", got, want)
	}
}

func TestEvaluateMeasuredViolationShowsValueThresholdAndWindow(t *testing.T) {
	t.Parallel()

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(read(t, "build/m.json", verifiedBy("k6-ci"), predicate(42))))

	want := "build/m.json: value 42 s against lte 30 s in the window 2026-10-30T10:00:00Z to 2026-10-30T11:00:00Z is violation"
	if len(got.Result.Details) != 1 || got.Result.Details[0] != want {
		t.Errorf("details = %q, want %q", got.Result.Details, want)
	}

	if !slices.Equal(got.Result.Evidence, []string{"build/m.json"}) {
		t.Errorf("evidence = %v", got.Result.Evidence)
	}
}

func TestEvaluateMeasuredIgnoresTheVerdictAndComparisonOfTheProducer(t *testing.T) {
	t.Parallel()

	// A producer that claims a pass for a value above the threshold does not make a pass.
	p := changed(predicate(42), "verdict", "pass")

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(read(t, "m.json", verifiedBy("k6-ci"), p)))
	if got.Result.Verdict != quality.VerdictViolation {
		t.Errorf("verdict = %s", got.Result.Verdict)
	}
}

func TestEvaluateMeasuredComparisons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		comparison quality.Comparison
		verdict    quality.Verdict
		value      float64
	}{
		{quality.CompareLT, quality.VerdictPass, 29}, {quality.CompareLT, quality.VerdictViolation, 30},
		{quality.CompareLTE, quality.VerdictPass, 30}, {quality.CompareLTE, quality.VerdictViolation, 31},
		{quality.CompareGT, quality.VerdictPass, 31}, {quality.CompareGT, quality.VerdictViolation, 30},
		{quality.CompareGTE, quality.VerdictPass, 30}, {quality.CompareGTE, quality.VerdictViolation, 29},
		{quality.CompareEQ, quality.VerdictPass, 30}, {quality.CompareEQ, quality.VerdictViolation, 29},
		{quality.Comparison("approx"), quality.VerdictError, 30},
	}

	for _, tt := range tests {
		t.Run(string(tt.comparison), func(t *testing.T) {
			t.Parallel()

			sc := failoverScenario()
			sc.Criterion.Comparison = tt.comparison

			p := changed(predicate(tt.value), "comparison", string(tt.comparison))

			if got := quality.EvaluateMeasured(sc, measureInput(read(t, "m.json", verifiedBy("k6-ci"), p))); got.Result.Verdict != tt.verdict {
				t.Errorf("%v %s 30: verdict = %s, want %s", tt.value, tt.comparison, got.Result.Verdict, tt.verdict)
			}
		})
	}
}

func TestEvaluateMeasuredWithoutAMeasurementIsUnknown(t *testing.T) {
	t.Parallel()

	other := changed(predicate(12), "scenario", "QS-OTHER")

	for name, input := range map[string]*quality.MeasuredInput{
		"none":                              measureInput(),
		"a measurement of another scenario": measureInput(read(t, "m.json", verifiedBy("k6-ci"), other)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := quality.EvaluateMeasured(failoverScenario(), input)

			if got.Result.Verdict != quality.VerdictUnknown || got.Result.Reason != quality.ReasonNoEvidenceSupplied {
				t.Errorf("verdict=%s reason=%q", got.Result.Verdict, got.Result.Reason)
			}

			expectLoaded(t, "assessed", len(got.Assessed), 0)
			expectLoaded(t, "diagnostics", len(got.Diagnostics), 0)
			expectLoaded(t, "details", len(got.Result.Details), 1)
		})
	}
}

func TestEvaluateMeasuredMetricOfAnotherQuantityIsInadmissible(t *testing.T) {
	t.Parallel()

	p := changed(predicate(12), "metric", "p99-latency")

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(read(t, "m.json", verifiedBy("k6-ci"), p)))

	if got.Result.Verdict != quality.VerdictUnknown || got.Result.Reason != quality.ReasonNoEvidenceSupplied {
		t.Errorf("verdict=%s reason=%q", got.Result.Verdict, got.Result.Reason)
	}

	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != diag.QUA003 {
		t.Errorf("diagnostics = %v", got.Diagnostics)
	}
}

func TestEvaluateMeasuredInadmissibleMeasurementDoesNotOverrideAnAdmissibleOne(t *testing.T) {
	t.Parallel()

	good := read(t, "a.json", verifiedBy("k6-ci"), predicate(12))
	wrongEnvironment := read(t, "b.json", verifiedBy("k6-ci"), changed(predicate(42), "environment", "staging"))

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(wrongEnvironment, good))

	if got.Result.Verdict != quality.VerdictPass || !slices.Equal(got.Result.Evidence, []string{"a.json"}) {
		t.Errorf("verdict=%s evidence=%v (%v)", got.Result.Verdict, got.Result.Evidence, got.Result.Details)
	}

	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != diag.QUA003 {
		t.Errorf("diagnostics = %v, want one inadmissible warning", got.Diagnostics)
	}
}

func TestEvaluateMeasuredConflictingAdmissibleMeasurements(t *testing.T) {
	t.Parallel()

	pass := read(t, "a.json", verifiedBy("k6-ci"), predicate(12))
	violation := read(t, "b.json", verifiedBy("k6-ci"), predicate(42))

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(violation, pass))

	if got.Result.Verdict != quality.VerdictUnknown || got.Result.Reason != quality.ReasonEvidenceConflict {
		t.Fatalf("verdict=%s reason=%q", got.Result.Verdict, got.Result.Reason)
	}

	if !slices.Equal(got.Result.Evidence, []string{"a.json", "b.json"}) {
		t.Errorf("evidence = %v", got.Result.Evidence)
	}

	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != diag.PRV012 {
		t.Errorf("diagnostics = %v, want one %s", got.Diagnostics, diag.PRV012)
	}
}

func TestEvaluateMeasuredAgreeingMeasurementsOfOneWindow(t *testing.T) {
	t.Parallel()

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(
		read(t, "a.json", verifiedBy("k6-ci"), predicate(12)), read(t, "b.json", verifiedBy("k6-ci"), predicate(20))))

	if got.Result.Verdict != quality.VerdictPass {
		t.Errorf("verdict=%s diagnostics=%v evidence=%v", got.Result.Verdict, got.Diagnostics, got.Result.Evidence)
	}

	expectLoaded(t, "diagnostics", len(got.Diagnostics), 0)
	expectLoaded(t, "evidence", len(got.Result.Evidence), 2)
}

func TestEvaluateMeasuredNewestWindowDecides(t *testing.T) {
	t.Parallel()

	older := read(t, "old.json", verifiedBy("k6-ci"), changed(predicate(42), "window", window("2026-10-29T10:00:00Z", "2026-10-29T11:00:00Z")))
	newer := read(t, "new.json", verifiedBy("k6-ci"), predicate(12))

	got := quality.EvaluateMeasured(failoverScenario(), measureInput(older, newer))

	if got.Result.Verdict != quality.VerdictPass || !slices.Equal(got.Result.Evidence, []string{"new.json"}) {
		t.Fatalf("verdict=%s evidence=%v", got.Result.Verdict, got.Result.Evidence)
	}

	if len(got.Diagnostics) != 0 {
		t.Errorf("an older window is not a conflict: %v", got.Diagnostics)
	}

	if len(got.Result.Details) != 2 {
		t.Errorf("details = %v", got.Result.Details)
	}
}

func TestEvaluateMeasuredIsIndependentOfInputOrder(t *testing.T) {
	t.Parallel()

	a := read(t, "a.json", verifiedBy("k6-ci"), predicate(12))
	b := read(t, "b.json", verifiedBy("k6-ci"), predicate(42))
	c := read(t, "c.json", verifiedBy("k6-ci"), changed(predicate(1), "environment", "staging"))

	first := quality.EvaluateMeasured(failoverScenario(), measureInput(a, b, c))
	second := quality.EvaluateMeasured(failoverScenario(), measureInput(c, b, a))

	expectLoaded(t, "reason", first.Result.Reason, second.Result.Reason)
	expectLoaded(t, "diagnostics", len(first.Diagnostics), len(second.Diagnostics))

	if !slices.Equal(first.Result.Details, second.Result.Details) {
		t.Errorf("order changes the result: %v vs %v", first.Result.Details, second.Result.Details)
	}
}

func TestMeasuredScenarioIsNotSatisfiedByOtherModes(t *testing.T) {
	t.Parallel()

	sc := failoverScenario()
	structural := quality.EvaluateStructural(&quality.Scenario{ID: sc.ID, Mode: quality.ModeStructural, Criterion: quality.Criterion{Rules: []string{"a"}}},
		map[string]quality.RuleResult{"a": {Verdict: quality.VerdictPass}})
	reviewed := quality.Result{Scenario: sc.ID, Mode: quality.ModeReviewed, Verdict: quality.VerdictPass}

	for _, got := range []quality.Result{structural, reviewed} {
		if got.Satisfies(sc) {
			t.Errorf("a %s pass satisfies a measured scenario", got.Mode)
		}
	}

	measured := quality.EvaluateMeasured(sc, measureInput(read(t, "m.json", verifiedBy("k6-ci"), predicate(12))))
	if !measured.Result.Satisfies(sc) {
		t.Error("a measured pass must satisfy the measured scenario")
	}
}

func TestReadMeasurementMalformedIsAWarning(t *testing.T) {
	t.Parallel()

	statementFor := func(predicateType string, predicate any) statement.Loaded {
		data, err := json.Marshal(map[string]any{
			"_type": statement.StatementType, "subject": []statement.Subject{{Digest: map[string]string{"sha256": "aaa"}}},
			"predicateType": predicateType, "predicate": predicate,
		})
		if err != nil {
			t.Fatal(err)
		}

		loaded, err := statement.Parse("bad.json", data)
		if err != nil {
			t.Fatal(err)
		}

		return loaded
	}

	tests := map[string]statement.Loaded{
		"value of the wrong type":   statementFor(statement.PredicateMeasurement, map[string]any{"scenario": "QS-FAILOVER", "value": "fast"}),
		"predicate of another type": statementFor(statement.PredicateCheckResult, map[string]any{"scenario": "QS-FAILOVER"}),
		"no scenario":               statementFor(statement.PredicateMeasurement, withoutKey(predicate(12), "scenario")),
	}

	for name, loaded := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, diags := quality.ReadMeasurement(&loaded, verifiedBy("k6-ci"))

			if len(diags) != 1 || m.Path != "" {
				t.Fatalf("measurement %+v diagnostics %v, want one %s", m, diags, diag.QUA003)
			}

			expectLoaded(t, "diagnostic code", diags[0].Code, diag.QUA003)
		})
	}
}
