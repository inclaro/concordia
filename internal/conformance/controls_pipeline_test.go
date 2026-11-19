package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
)

// checksControl is a control with `check-ids` and, when ruleIDs is not nil, `rule-ids`.
func checksControl(disposition string, ruleIDs []any, checkIDs ...any) map[string]any {
	config := map[string]any{
		"control-id": "control-1", "name": "Checks", "description": "Checked", "engine": "concordia",
		"check-ids": checkIDs, "unknown-disposition": disposition,
	}
	if ruleIDs != nil {
		config["rule-ids"] = ruleIDs
	}

	return map[string]any{"control-1": map[string]any{
		"description": "Checked",
		"requirements": []any{map[string]any{
			"requirement-url": "https://calm.finos.org/release/1.2/meta/control-requirement.json",
			"config":          config,
		}},
	}}
}

func checkedArch(control map[string]any) string {
	return archText(map[string]any{"controls": control})
}

func TestControlWithARuleAndAnUnbackedCheckIsTheWorstOfBoth(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{archPath: checkedArch(checksControl("warn", []any{"CO-BND-001"}, "unit-tests"))}, nil)
	run := s.mustCheck(t, nil)

	if len(run.Controls) != 1 {
		t.Fatalf("controls = %+v", run.Controls)
	}

	c := run.Controls[0]
	if !slices.Equal(c.RuleIDs, []string{"CO-BND-001"}) || !slices.Equal(c.CheckIDs, []string{"unit-tests"}) {
		t.Errorf("control = %+v", c)
	}

	if c.Verdict != conformance.VerdictViolation {
		t.Errorf("control = %+v", c)
	}

	if len(c.Reasons) != 1 {
		t.Fatalf("reasons = %+v", c.Reasons)
	}

	if c.Reasons[0].Code != "no-evidence-supplied" || !strings.Contains(c.Reasons[0].Message, "unit-tests") {
		t.Errorf("reasons = %+v", c.Reasons)
	}
}

// `--control` keeps the rules of a control. A control that lists only checks keeps no rule, and the run still judges
// its checks: the control is `unknown` because no check has a result, never `pass` and never absent.
func TestControlOptionWithChecksOnlyRunsNoRuleAndJudgesTheChecks(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{archPath: checkedArch(checksControl("warn", nil, "unit-tests", "sast"))}, nil)
	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.Controls = []string{"control-1"} })

	if len(run.Rules) != 0 {
		t.Errorf("rules = %v", ruleVerdicts(run))
	}

	if len(run.Controls) != 1 {
		t.Fatalf("controls = %+v", run.Controls)
	}

	control := run.Controls[0]
	if control.Verdict != conformance.VerdictUnknown {
		t.Errorf("controls = %+v", run.Controls)
	}

	if !slices.Equal(control.CheckIDs, []string{"sast", "unit-tests"}) {
		t.Errorf("controls = %+v", run.Controls)
	}

	if len(control.RuleIDs) != 0 || len(control.Reasons) != 2 {
		t.Errorf("controls = %+v", run.Controls)
	}

	// The control says `warn`, so the unknown checks do not fail the run.
	if run.Summary.ExitCode != diag.OK {
		t.Errorf("exit code = %d", run.Summary.ExitCode)
	}
}

func TestControlThatFailsOnUnknownFailsTheRunForACheckWithNoResult(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{archPath: checkedArch(checksControl("fail", nil, "unit-tests"))}, nil)

	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.Controls = []string{"control-1"} })
	if run.Summary.ExitCode != diag.Unknown {
		t.Errorf("exit code = %d, want %d", run.Summary.ExitCode, diag.Unknown)
	}

	// A violation keeps its own, stronger exit code.
	withRule := checksControl("fail", []any{"CO-BND-001"}, "unit-tests")

	run = newSetup(t, map[string]string{archPath: checkedArch(withRule)}, nil).mustCheck(t, nil)
	if run.Summary.ExitCode != diag.Violation {
		t.Errorf("exit code = %d, want %d", run.Summary.ExitCode, diag.Violation)
	}
}
