package conformance_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

func rolloutRow(t *testing.T, report *conformance.RolloutReport, family, environment string) *conformance.RolloutFamily {
	t.Helper()

	for i := range report.Families {
		if report.Families[i].Family == family && report.Families[i].Environment == environment {
			return &report.Families[i]
		}
	}

	t.Fatalf("no row %q/%q in %+v", family, environment, report.Families)

	return nil
}

// rolloutRowShape is the enforcement, the verdict counts, and the number of unknown results of a rollout row.
type rolloutRowShape struct {
	enforcement string
	verdicts    conformance.RuleCounts
	unknown     int
}

// requireRolloutRow stops the test when the row differs from the shape.
func requireRolloutRow(t *testing.T, name string, row *conformance.RolloutFamily, want rolloutRowShape) {
	t.Helper()

	got := rolloutRowShape{row.Enforcement, row.Verdicts, len(row.Unknown)}
	if got != want {
		t.Fatalf("%s = %+v", name, row)
	}
}

// rolloutUnknownShape is the part of an unknown result that names its rule, obligation, and reason.
type rolloutUnknownShape struct {
	rule, obligation, reason string
	wouldBlock               bool
}

// requireSingleMissing stops the test unless the unknown result lacks exactly one thing of the kind and subject.
func requireSingleMissing(t *testing.T, u *conformance.RolloutUnknown, kind, subject string) {
	t.Helper()

	if len(u.Missing) != 1 {
		t.Fatalf("unknown = %+v", *u)
	}

	if got := u.Missing[0]; got.Kind != kind || got.Subject != subject {
		t.Fatalf("unknown = %+v", *u)
	}
}

func TestRolloutReportListsUnknownWithReasonAndMissingBindingOrCoverage(t *testing.T) {
	t.Parallel()

	report := conformance.BuildRolloutReport(rolloutRun())

	staging := rolloutRow(t, report, "images", "staging")
	requireRolloutRow(t, "staging", staging, rolloutRowShape{"advisory", conformance.RuleCounts{Violation: 1, Unknown: 1}, 1})

	u := staging.Unknown[0]
	if got := (rolloutUnknownShape{u.RuleID, u.ObligationID, u.Reason, u.WouldBlock}); got != (rolloutUnknownShape{"CO-IMG-001", "ob-stg-2", "binding-gap", true}) {
		t.Fatalf("unknown = %+v", u)
	}

	requireSingleMissing(t, &u, conformance.MissingBinding, "")

	if u.Missing[0].NextAction != conformance.NextActionResolveBinding {
		t.Fatalf("unknown = %+v", u)
	}

	production := rolloutRow(t, report, "images", "production")
	requireRolloutRow(t, "production", production, rolloutRowShape{"shadow", conformance.RuleCounts{Pass: 1, Unknown: 1}, 1})

	if production.Unknown[0].Reason != "coverage-incomplete" {
		t.Fatalf("production unknown = %+v", production.Unknown[0])
	}

	requireSingleMissing(t, &production.Unknown[0], conformance.MissingCoverage, "uses-image")

	requirePlainRolloutRow(t, rolloutRow(t, report, "", ""))
}

// requirePlainRolloutRow checks that a rule of the as-built families gives its reason code and the coverage and
// binding that it lacks.
func requirePlainRolloutRow(t *testing.T, plain *conformance.RolloutFamily) {
	t.Helper()

	if plain.Enforcement != "mixed" || len(plain.Unknown) != 1 {
		t.Fatalf("plain = %+v", plain)
	}

	if plain.Unknown[0].Reason != "CO-CON-020" {
		t.Fatalf("plain = %+v", plain)
	}

	kinds := map[string]string{}
	for _, mm := range plain.Unknown[0].Missing {
		kinds[mm.Kind+"/"+mm.Subject] = mm.Detail
	}

	for _, want := range []string{"binding/", "coverage/imports", "coverage/files"} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("missing %q: have %v", want, kinds)
		}
	}
}

func TestRolloutReportListsWhatWouldBlockIfMandatory(t *testing.T) {
	t.Parallel()

	report := conformance.BuildRolloutReport(rolloutRun())

	staging := rolloutRow(t, report, "images", "staging")
	if len(staging.WouldBlock) != 1 {
		t.Fatalf("staging would block = %+v", staging.WouldBlock)
	}

	if blocked := staging.WouldBlock[0]; blocked.ObligationID != "ob-stg-1" || blocked.Enforcement != policy.EnforcementAdvisory {
		t.Fatalf("staging would block = %+v", staging.WouldBlock)
	}

	// The waived finding does not block, and the mandatory finding already blocks: neither is "would block".
	plain := rolloutRow(t, report, "", "")
	if len(plain.WouldBlock) != 1 || plain.WouldBlock[0].Fingerprint != "sha256:"+strings.Repeat("b2", 32) {
		t.Fatalf("plain would block = %+v", plain.WouldBlock)
	}
}

func TestRolloutReportGolden(t *testing.T) {
	t.Parallel()

	got, err := conformance.BuildRolloutReport(rolloutRun()).JSON()
	if err != nil {
		t.Fatal(err)
	}

	if os.Getenv(updateGoldenEnv) != "" {
		writeRolloutGolden(t, got)

		return
	}

	want, err := os.ReadFile(rolloutGolden())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("the rollout report differs from %s; run with %s=1 after a deliberate change\ngot:\n%s", rolloutGolden(), updateGoldenEnv, got)
	}
}

func writeRolloutGolden(t *testing.T, got []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(rolloutGolden()), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(rolloutGolden(), got, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRolloutReportTextNamesTheModeAndWhatWouldBlock(t *testing.T) {
	t.Parallel()

	text := conformance.BuildRolloutReport(rolloutRun()).Text("build/concordia/rollout-report.json")

	for _, want := range []string{
		"rollout report (build/concordia/rollout-report.json)",
		"images / staging: advisory; pass 0, violation 1, unknown 1",
		"images / production: shadow",
		"(no family) / (no environment): mixed",
		"unknown CO-IMG-001 billing-service: binding-gap; missing binding",
		"would block if mandatory: CO-IMG-001 a1a1a1a1a1a1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

// An advisory typed rule with a violation is in the would-block list and the exit code stays 0.
func TestRolloutReportOfAnAdvisoryTypedRule(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    family: images\n", requireImage))
	run := newSetup(t, files, nil).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} })

	report := conformance.BuildRolloutReport(run)
	row := rolloutRow(t, report, "images", "")

	if row.Enforcement != "advisory" || row.Verdicts.Violation != 1 {
		t.Fatalf("row = %+v", row)
	}

	if len(row.WouldBlock) != 1 || row.WouldBlock[0].RuleID != "CO-TYP-001" {
		t.Fatalf("row = %+v", row)
	}

	if run.Summary.ExitCode != 0 || row.WouldBlock[0].Fingerprint != run.Findings[0].Fingerprint {
		t.Fatalf("exit code = %d, would block = %+v", run.Summary.ExitCode, row.WouldBlock)
	}
}

// A shadow family stays out of the gate; the report still lists what would block.
func TestRolloutReportOfAShadowFamilyFromTheWorkspace(t *testing.T) {
	t.Parallel()

	ws := wsText("")
	ws = strings.Replace(ws, "failOn: error}", "failOn: error, enforcement: [{family: images, mode: shadow}]}", 1)
	files := typedFiles("", typedRuleText("CO-TYP-001", "    family: images\n", requireImage))

	run := newSetup(t, files, &setupOpts{ws: ws}).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} })

	row := rolloutRow(t, conformance.BuildRolloutReport(run), "images", "")
	if row.Enforcement != "shadow" || len(row.WouldBlock) != 1 {
		t.Fatalf("row = %+v", row)
	}

	if row.WouldBlock[0].Enforcement != policy.EnforcementShadow {
		t.Fatalf("row = %+v", row)
	}
}
