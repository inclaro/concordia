package integration_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/policy"
)

// checkDeploysWorkspace runs the A43 workspace (rules with the `deploys` token on the infra recording) with the workspace
// text. The edits change the check options (for example the run ID and the clock).
func checkDeploysWorkspace(t *testing.T, workspaceText string, edits ...func(*conformance.CheckOptions)) *conformance.Run {
	t.Helper()

	w := newInfraWorkspace(t, infraRecordingFile, deploysWorkspaceFiles(workspaceText))
	log := logger.New(logger.Options{Out: io.Discard, RunID: "run", Quiet: true})

	opts := w.checkOptions(log, append([]func(*conformance.CheckOptions){func(o *conformance.CheckOptions) { o.SkipReports = true }}, edits...)...)

	run, err := conformance.RunCheck(context.Background(), opts)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}

	return run
}

// A43 in the rollout report: the rule with `deploys` gives `pass` or `violation`, with the family advisory the
// violation is on the would-block list, and the exit code stays 0.
func TestFoundationA43DeploysRuleInTheRolloutReport(t *testing.T) {
	workspaceText := strings.Replace(deploysWorkspace, "  failOn: error\n",
		"  failOn: error\n  enforcement:\n    - {rules: [CO-DEP-001, CO-DEP-002], mode: advisory}\n", 1)
	if workspaceText == deploysWorkspace {
		t.Fatal("the workspace text has no failOn line to extend")
	}

	run := checkDeploysWorkspace(t, workspaceText)
	report := conformance.BuildRolloutReport(run)

	if run.Summary.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0: the rules are advisory", run.Summary.ExitCode)
	}

	if len(report.Families) != 1 {
		t.Fatalf("rows = %+v", report.Families)
	}

	row := report.Families[0]
	if row.Enforcement != string(policy.EnforcementAdvisory) || row.Verdicts.Violation != 1 || row.Verdicts.Pass != 1 || row.Verdicts.Unknown != 0 {
		t.Errorf("row = %+v, want advisory with one violation, one pass and no unknown", row)
	}

	if len(row.Unknown) != 0 {
		t.Errorf("unknown = %+v, want none: the deploys family is supported", row.Unknown)
	}

	if len(row.WouldBlock) == 0 {
		t.Fatal("the would-block list is empty")
	}

	for _, b := range row.WouldBlock {
		if b.RuleID != "CO-DEP-001" || b.Enforcement != policy.EnforcementAdvisory {
			t.Errorf("would block %+v, want CO-DEP-001 findings with the mode advisory", b)
		}
	}

	// Without the entry the rules are mandatory: the same violation blocks and nothing would block.
	mandatory := conformance.BuildRolloutReport(checkDeploysWorkspace(t, deploysWorkspace))
	if got := mandatory.Families[0]; got.Enforcement != string(policy.EnforcementMandatory) || len(got.WouldBlock) != 0 {
		t.Errorf("mandatory row = %+v", got)
	}
}
