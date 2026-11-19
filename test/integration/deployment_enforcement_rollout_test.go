package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

const (
	enfPlacementProduction = "deployment-placement/production"
	enfPlacementStaging    = "deployment-placement/staging"
	enfTraceProduction     = "deployment-trace/production"
	enfTraceStaging        = "deployment-trace/staging"
)

// enfRolloutRows indexes the rows of a rollout report by family and environment.
func enfRolloutRows(report *conformance.RolloutReport) map[string]conformance.RolloutFamily {
	rows := map[string]conformance.RolloutFamily{}

	for i := range report.Families {
		row := &report.Families[i]
		rows[row.Family+"/"+row.Environment] = *row
	}

	return rows
}

func enfRequireRolloutRows(t *testing.T, name string, report *conformance.RolloutReport, rows map[string]conformance.RolloutFamily) {
	t.Helper()

	for _, key := range []string{enfPlacementProduction, enfPlacementStaging, enfTraceProduction, enfTraceStaging} {
		if _, ok := rows[key]; !ok {
			t.Fatalf("%s: no row %s in %v", name, key, report.Families)
		}
	}
}

func enfRequireRolloutEnforcement(t *testing.T, name string, rows map[string]conformance.RolloutFamily) {
	t.Helper()

	if got := rows[enfPlacementStaging].Enforcement; got != string(policy.EnforcementShadow) {
		t.Errorf("%s: placement staging enforcement = %q, want shadow", name, got)
	}

	for _, key := range []string{enfPlacementProduction, enfTraceProduction, enfTraceStaging} {
		if got := rows[key].Enforcement; got != string(policy.EnforcementAdvisory) {
			t.Errorf("%s: %s enforcement = %q, want advisory", name, key, got)
		}
	}
}

// enfIsExplainedUnknown says whether an unknown has its reason, what is missing, and its obligation and subject.
func enfIsExplainedUnknown(u *conformance.RolloutUnknown) bool {
	if u.Reason == "" || len(u.Missing) == 0 {
		return false
	}

	return u.ObligationID != "" && u.Subject != ""
}

func enfFindRolloutUnknown(unknowns []conformance.RolloutUnknown, rule, subject string) *conformance.RolloutUnknown {
	var found *conformance.RolloutUnknown

	for i := range unknowns {
		if u := &unknowns[i]; u.RuleID == rule && u.Subject == subject {
			found = u
		}
	}

	return found
}

// enfIsUsesImageCoverageGap says whether the unknown is incomplete coverage of `uses-image`.
func enfIsUsesImageCoverageGap(u *conformance.RolloutUnknown) bool {
	if u.Reason != "coverage-incomplete" || len(u.Missing) == 0 {
		return false
	}

	return u.Missing[0].Kind == "coverage" && u.Missing[0].Subject == "uses-image"
}

// enfRequireRolloutUnknowns checks that each unknown of trace/production has its reason and what is missing, and that
// INFRA-TRC-004 of api-service is the unknown of incomplete `uses-image` coverage.
func enfRequireRolloutUnknowns(t *testing.T, name string, row *conformance.RolloutFamily) {
	t.Helper()

	for i := range row.Unknown {
		if !enfIsExplainedUnknown(&row.Unknown[i]) {
			t.Errorf("%s: unknown without reason or missing evidence: %+v", name, row.Unknown[i])
		}
	}

	trc004 := enfFindRolloutUnknown(row.Unknown, "INFRA-TRC-004", "api-service")
	if trc004 == nil {
		t.Fatalf("%s: the trace/production row lacks the INFRA-TRC-004 unknown of api-service: %+v", name, row.Unknown)
	}

	if !enfIsUsesImageCoverageGap(trc004) {
		t.Errorf("%s: TRC-004 unknown = %+v, want coverage-incomplete with missing uses-image coverage", name, *trc004)
	}
}

// enfHasWorkerPlacementBlock says whether the would-block list holds the live INFRA-PLC-001 violation of the worker.
func enfHasWorkerPlacementBlock(blocks []conformance.RolloutBlock) bool {
	found := false

	for i := range blocks {
		if b := &blocks[i]; b.RuleID == "INFRA-PLC-001" && b.Subject == "worker-service" && b.Kind == conformance.KindObligationAbsent {
			found = found || b.Enforcement == policy.EnforcementAdvisory
		}
	}

	return found
}

// enfRequireRolloutWouldBlock checks the would-block lists: the live violation of the worker in production, and for the
// trace family only the TRC-001 violation of the worker.
func enfRequireRolloutWouldBlock(t *testing.T, name string, rows map[string]conformance.RolloutFamily) {
	t.Helper()

	placement := rows[enfPlacementProduction].WouldBlock
	if !enfHasWorkerPlacementBlock(placement) {
		t.Errorf("%s: the would-block list of placement/production lacks INFRA-PLC-001 worker-service: %+v", name, placement)
	}

	trace := rows[enfTraceProduction].WouldBlock
	for i := range trace {
		if b := &trace[i]; b.RuleID != "INFRA-TRC-001" || b.Subject != "worker-service" {
			t.Errorf("%s: unexpected would-block entry of trace/production: %+v", name, *b)
		}
	}

	if len(trace) != 1 {
		t.Errorf("%s: trace/production would-block = %+v, want the TRC-001 violation of worker-service", name, trace)
	}
}

func enfCheckRolloutReport(t *testing.T, name string, report *conformance.RolloutReport) {
	t.Helper()

	rows := enfRolloutRows(report)
	enfRequireRolloutRows(t, name, report, rows)
	enfRequireRolloutEnforcement(t, name, rows)

	traceProduction := rows[enfTraceProduction]
	enfRequireRolloutUnknowns(t, name, &traceProduction)
	enfRequireRolloutWouldBlock(t, name, rows)
}

func enfReadRolloutReport(t *testing.T, d *deployment) *conformance.RolloutReport {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(d.w.dir, "build", "concordia", "rollout-report.json"))
	if err != nil {
		t.Fatal(err)
	}

	var report conformance.RolloutReport
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("rollout-report.json: %v", err)
	}

	return &report
}

// `check --rollout-report`: one row per family and environment; each unknown has its reason and what is missing; the
// violations that block under `mandatory` are on the would-block list; the exit code does not change.
func TestDeploymentEnforcementRolloutReport(t *testing.T) {
	d := deploymentRun(t, &deploymentSpec{check: deploymentCheck + enfPlacementShadowStaging})

	plain := d.cli(t, "check")

	res := d.cli(t, "check", "--rollout-report")
	if res.code != plain.code || res.code != 0 {
		t.Fatalf("exit code = %d (without the flag %d), want 0: %s", res.code, plain.code, res.stderr)
	}

	enfRequireContains(t, "the rollout summary", res.stderr,
		"rollout report (build/concordia/rollout-report.json)",
		"deployment-placement / production: advisory",
		"deployment-placement / staging: shadow",
		"deployment-trace / production: advisory",
		"deployment-trace / staging: advisory",
		"would block if mandatory: INFRA-PLC-001",
		"unknown INFRA-TRC-004 api-service: coverage-incomplete",
	)

	reports := map[string]*conformance.RolloutReport{"file": enfReadRolloutReport(t, d), "built": conformance.BuildRolloutReport(d.run)}
	for name, report := range reports {
		enfCheckRolloutReport(t, name, report)
	}
}
