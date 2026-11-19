package e2e_test

import (
	"strings"
	"testing"
)

// asrEvQualityCase is one measurement scenario of A37 and the entry that the quality report makes of it.
type asrEvQualityCase struct {
	name        string
	verdict     string
	reason      string
	detail      string
	measurement asrMeasurement
	finding     bool
}

// asrEvQualityCases are the measurement scenarios of A37.
func asrEvQualityCases() []asrEvQualityCase {
	return []asrEvQualityCase{
		{
			name: "above the threshold", measurement: asrMeasurement{file: "failover.json", scenario: "QS-FAILOVER", value: 42}, verdict: "violation",
			detail: "value 42 s against lte 30 s in the window 2026-10-31T09:00:00Z to 2026-10-31T10:00:00Z is violation", finding: true,
		},
		{
			name: "wrong environment", measurement: asrMeasurement{file: "failover.json", scenario: "QS-FAILOVER", value: 42, environment: "staging"},
			verdict: "unknown", reason: "no-evidence-supplied", detail: "inadmissible (different-environment)", finding: true,
		},
		{
			name: "within the threshold", measurement: asrMeasurement{file: "failover.json", scenario: "QS-FAILOVER", value: 12}, verdict: "pass",
			detail: "value 12 s against lte 30 s",
		},
	}
}

// asrEvQualityRun is the outcome of the check of a quality case.
type asrEvQualityRun struct {
	r     *asrRepo
	doc   jv
	entry jv
	c     *asrEvQualityCase
}

// asrEvQualityEntry is the quality entry of the scenario in the results of the check.
func asrEvQualityEntry(doc jv, scenario string) jv {
	return doc.at("assurance", "quality").where("scenario", scenario)
}

// asrEvRunQualityCase checks the entry of the measured scenario, the other modes, the explanation, and the provenance.
func asrEvRunQualityCase(t *testing.T, c *asrEvQualityCase) {
	t.Helper()

	w := &asrWorld{t: t, scenarios: asrScenarios, requiredAreas: "    requiredAreas: [security]\n", measurements: []asrMeasurement{c.measurement}}
	r := w.build()
	doc, _ := r.check()

	run := &asrEvQualityRun{r: r, doc: doc, entry: asrEvQualityEntry(doc, "QS-FAILOVER"), c: c}
	asrEvRequireMeasuredEntry(t, run)

	// The result of the rule agrees with the entry.
	if got := doc.rule("quality:QS-FAILOVER").at("verdict").str(); got != c.verdict {
		t.Errorf("rule verdict = %s, want %s", got, c.verdict)
	}

	asrEvRequireOtherModes(t, doc)
	asrEvRequireSecurityAreaFinding(t, doc)
	asrEvRequireMeasurementExplained(t, run)
	asrEvRequireMeasurementProvenance(t, run)
}

// asrEvRequireMeasuredEntry checks the measured entry of the scenario.
func asrEvRequireMeasuredEntry(t *testing.T, run *asrEvQualityRun) {
	t.Helper()

	entry, c := run.entry, run.c
	if !asrEvStrFields(entry, "mode", "measured", "verdict", c.verdict, "reason", c.reason, "enforcement", "mandatory") ||
		!strings.Contains(entry.at("details").text(), c.detail) {
		t.Fatalf("QS-FAILOVER = %s, want %s %q %q", entry.text(), c.verdict, c.reason, c.detail)
	}
}

// asrEvRequireOtherModes checks that structural reads the rule results, and reviewed has no assessment to read.
func asrEvRequireOtherModes(t *testing.T, doc jv) {
	t.Helper()

	if boundary := asrEvQualityEntry(doc, "QS-BOUNDARY"); !asrEvStrFields(boundary, "verdict", "pass", "mode", "structural") {
		t.Errorf("QS-BOUNDARY = %s", boundary.text())
	}

	if runbook := asrEvQualityEntry(doc, "QS-RUNBOOK"); !asrEvStrFields(runbook, "verdict", "unknown", "mode", "reviewed") {
		t.Errorf("QS-RUNBOOK = %s", runbook.text())
	}
}

// asrEvSecurityAreaFlagged says whether the area is required, has no scenario, and is reported as a violation.
func asrEvSecurityAreaFlagged(area jv) bool {
	if area.at("verdict").str() != "violation" || area.at("required").text() != "true" {
		return false
	}

	return len(area.at("scenarios").list()) == 0 && len(area.at("reported").list()) == 1
}

// asrEvRequireSecurityAreaFinding checks that the required area without a scenario is a finding of the quality report.
func asrEvRequireSecurityAreaFinding(t *testing.T, doc jv) {
	t.Helper()

	if security := doc.at("assurance", "qualityAreas").where("area", "security"); !asrEvSecurityAreaFlagged(security) {
		t.Errorf("security area = %s", security.text())
	}
}

// asrEvRequireMeasurementExplained checks that the explanation shows the value, the threshold, and the window.
func asrEvRequireMeasurementExplained(t *testing.T, run *asrEvQualityRun) {
	t.Helper()

	out := run.r.explain("quality:QS-FAILOVER").output()
	containsAll(t, out, "Verdict: "+run.c.verdict)

	if run.c.finding {
		containsAll(t, out, "Finding ", run.c.detail)
	} else if !strings.Contains(out, "No findings.") {
		t.Errorf("a passing scenario has a finding:\n%s", out)
	}
}

// asrEvRequireMeasurementProvenance checks that the entry cites the measurement statement and the bundle persists it.
func asrEvRequireMeasurementProvenance(t *testing.T, run *asrEvQualityRun) {
	t.Helper()

	if run.c.verdict != "unknown" {
		if ref := run.entry.at("evidence", "0", "ref").str(); ref != "build/measurements/failover.json" {
			t.Errorf("evidence = %s", run.entry.at("evidence").text())
		}
	}

	summary := run.doc.at("assurance", "statements").where("kind", "measurement")
	// The statement is authentic and admissible as a statement; the scenario decides that it is of another environment.
	if summary.at("admissible").text() != "true" || !asrEvStrFields(summary, "producer", "ci", "integrity", "producer-authenticated") {
		t.Errorf("measurement statement = %s", summary.text())
	}

	dir, paths := run.r.bundleFiles()
	requireStatementInBundle(t, dir, paths, summary.at("digest").str(), "sha256:"+strings.Repeat("d", 64))
}
