package cli_test

import (
	"strings"
	"testing"
)

func TestCheckRolloutReportWritesTheDefaultPathAndPrintsTheSummaryOnStderr(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "check", "--rollout-report")
	if res.Code == 2 || res.Code == 3 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !strings.Contains(res.Stderr, "rollout report (build/concordia/rollout-report.json)") {
		t.Errorf("stderr lacks the summary: %q", res.Stderr)
	}

	if strings.Contains(res.Stdout, "rollout report") {
		t.Errorf("the summary is on stdout: %q", res.Stdout)
	}

	report := readFile(t, s.dir, "build/concordia/rollout-report.json")
	for _, want := range []string{`"schemaVersion": "1.0"`, `"families": [`, `"ruleId": "CO-BND-001"`, `"enforcement": "mandatory"`} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %s: %s", want, report)
		}
	}
}

func TestCheckRolloutReportAcceptsAPathAndDoesNotChangeTheExitCode(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)
	plain := s.run(t, "check")

	res := s.run(t, "check", "--rollout-report=out/rollout.json")
	if res.Code != plain.Code {
		t.Fatalf("exit code = %d, without the flag %d", res.Code, plain.Code)
	}

	if report := readFile(t, s.dir, "out/rollout.json"); !strings.Contains(report, `"families"`) {
		t.Errorf("report = %s", report)
	}

	if exists(s.dir, "build/concordia/rollout-report.json") {
		t.Error("the default path was written although a path was given")
	}
}
