package quality_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/diag"
)

const fixtures = "../../../test/fixtures/assurance/quality"

func set(ids ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		out[id] = struct{}{}
	}

	return out
}

// known is the model and policy that the fixtures refer to.
func known() quality.Known {
	return quality.Known{
		Elements: set("orders-db", "orders-service", "orders-calls-db"),
		Rules:    set("boundary-rule", "layering-rule"),
	}
}

func loadFixtures(t *testing.T, patterns ...string) (*quality.Set, []*diag.Error) {
	t.Helper()

	return quality.Load(fixtures, patterns, known())
}

func TestLoadValidFile(t *testing.T) {
	t.Parallel()

	got, errs := loadFixtures(t, "valid.yaml")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	if counts := [3]int{len(got.Scenarios), len(got.Findings), len(got.Files)}; counts != [3]int{3, 1, 1} {
		t.Fatalf("scenarios=%d findings=%d files=%d", len(got.Scenarios), len(got.Findings), len(got.Files))
	}

	expectLoaded(t, "file path", got.Files[0].Path, "valid.yaml")

	if !strings.HasPrefix(got.Files[0].Digest, "sha256:") {
		t.Errorf("file = %+v", got.Files[0])
	}

	failover := got.Scenario("QS-FAILOVER")
	if failover == nil {
		t.Fatal("QS-FAILOVER not loaded")
	}

	expectFailoverScenario(t, failover)
	expectBoundaryAndRunbook(t, got)
	expectTradeoffFinding(t, got.Findings[0])

	ids := []string{got.Scenarios[0].ID, got.Scenarios[1].ID, got.Scenarios[2].ID}
	if strings.Join(ids, ",") != "QS-BOUNDARY,QS-FAILOVER,QS-RUNBOOK" {
		t.Errorf("scenarios are not sorted by id: %v", ids)
	}
}

func expectLoaded[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func expectFailoverScenario(t *testing.T, failover *quality.Scenario) {
	t.Helper()

	expectLoaded(t, "failover mode", failover.Mode, quality.ModeMeasured)
	expectLoaded(t, "failover enforcement", failover.Enforcement, quality.EnforcementMandatory)
	expectLoaded(t, "failover metric", failover.Criterion.Metric, "failover-seconds")
	expectLoaded(t, "failover unit", failover.Criterion.Unit, "s")
	expectLoaded(t, "failover comparison", failover.Criterion.Comparison, quality.CompareLTE)
	expectLoaded(t, "failover threshold", failover.Criterion.Threshold, 30)
	expectLoaded(t, "failover max age", failover.Criterion.MaxAgeDays, 30)
	expectLoaded(t, "location file", failover.Location.File, "valid.yaml")
	expectLoaded(t, "location line", failover.Location.Line, 4)

	if len(failover.AddressedBy) != 1 {
		t.Fatalf("addressedBy = %+v", failover.AddressedBy)
	}

	expectLoaded(t, "addressedBy id", failover.AddressedBy[0].ID, "ADR-0007")

	if failover.AddressedBy[0].Location.Line == 0 {
		t.Errorf("addressedBy = %+v", failover.AddressedBy)
	}
}

func expectBoundaryAndRunbook(t *testing.T, got *quality.Set) {
	t.Helper()

	boundary := got.Scenario("QS-BOUNDARY")
	if boundary.Enforcement != quality.EnforcementAdvisory {
		t.Errorf("enforcement default = %q, want advisory", boundary.Enforcement)
	}

	if len(boundary.Criterion.Rules) != 2 {
		t.Errorf("rules = %v", boundary.Criterion.Rules)
	}

	review := got.Scenario("QS-RUNBOOK")
	expectLoaded(t, "reviewed subject", review.Criterion.Subject, "orders-runbook")
	expectLoaded(t, "reviewed authority", review.Criterion.Authority, "sre-lead")
}

func expectTradeoffFinding(t *testing.T, finding *quality.Finding) {
	t.Helper()

	expectLoaded(t, "finding type", finding.Type, quality.FindingTradeoff)
	expectLoaded(t, "finding status", finding.Status, quality.FindingOpen)
	expectLoaded(t, "finding areas", len(finding.Areas), 1)
	expectLoaded(t, "finding assumptions", len(finding.Assumptions), 1)
	expectLoaded(t, "finding addressedBy", len(finding.AddressedBy), 1)

	if len(finding.Areas) == 1 {
		expectLoaded(t, "finding area", finding.Areas[0], quality.AreaModifiability)
	}
}

type invalidLoadCase struct { //nolint:govet // a test table.
	name     string
	patterns []string
	code     string
	file     string
	line     int
	contains []string
}

func invalidLoadCases() []invalidLoadCase {
	return []invalidLoadCase{
		{"missing owner names scenario and field", []string{"missing-owner.yaml"}, diag.QUA001, "missing-owner.yaml", 4,
			[]string{"QS-NO-OWNER", "owner"}},
		{"vague criterion is not measurable", []string{"vague-criterion.yaml"}, diag.QUA001, "vague-criterion.yaml", 14,
			[]string{"QS-VAGUE", "not measurable"}},
		{"measured criterion without threshold", []string{"measured-incomplete.yaml"}, diag.QUA001, "measured-incomplete.yaml", 14,
			[]string{"QS-NO-THRESHOLD", "threshold"}},
		{"comparison outside the five", []string{"bad-comparison.yaml"}, diag.QUA001, "bad-comparison.yaml", 14,
			[]string{"QS-BAD-COMPARISON", "/criterion/comparison"}},
		{"structural criterion in free text", []string{"structural-free-text.yaml"}, diag.QUA001, "structural-free-text.yaml", 14,
			[]string{"QS-FREE-TEXT", "free text", "rules"}},
		{"reviewed criterion without authority", []string{"reviewed-incomplete.yaml"}, diag.QUA001, "reviewed-incomplete.yaml", 14,
			[]string{"QS-NO-AUTHORITY", "authority"}},
		{"duplicate id across files names both locations", []string{"duplicate-a.yaml", "duplicate-b.yaml"}, diag.QUA001, "duplicate-b.yaml", 4,
			[]string{"QS-SAME", "duplicate-a.yaml:4", "duplicate-b.yaml:4"}},
		{"duplicate id in one file", []string{"duplicate-same-file.yaml"}, diag.QUA001, "duplicate-same-file.yaml", 15,
			[]string{"QS-TWICE", "duplicate-same-file.yaml:4", "duplicate-same-file.yaml:15"}},
		{"unknown CALM element", []string{"unknown-element.yaml"}, diag.QUA002, "unknown-element.yaml", 14,
			[]string{"QS-UNKNOWN-ELEMENT", "ghost-service"}},
		{"unresolved rule", []string{"unknown-rule.yaml"}, diag.QUA002, "unknown-rule.yaml", 17,
			[]string{"QS-UNKNOWN-RULE", "no-such-rule"}},
		{"finding without scenario", []string{"finding-no-scenario.yaml"}, diag.QUA001, "finding-no-scenario.yaml", 21,
			[]string{"F-ORPHAN", "scenarios"}},
		{"finding names an undeclared scenario", []string{"finding-unknown-scenario.yaml"}, diag.QUA002, "finding-unknown-scenario.yaml", 21,
			[]string{"F-MISSING", "QS-NOT-DECLARED"}},
		{"ADR identifier of the wrong form", []string{"adr-malformed.yaml"}, diag.QUA001, "adr-malformed.yaml", 17,
			[]string{"ADR_7", "ADR-<number>"}},
		{"document that is not a mapping", []string{"not-a-mapping.yaml"}, diag.QUA001, "not-a-mapping.yaml", 0,
			[]string{"mapping"}},
		{"pattern that matches no file", []string{"nothing-*.yaml"}, diag.QUA001, "nothing-*.yaml", 0,
			[]string{"nothing-*.yaml"}},
	}
}

func TestLoadInvalidFiles(t *testing.T) {
	t.Parallel()

	for _, tt := range invalidLoadCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, errs := loadFixtures(t, tt.patterns...)
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly one", errs)
			}

			requireLoadError(t, errs[0], &tt)
		})
	}
}

func requireLoadErrorSource(t *testing.T, src *diag.Source, tt *invalidLoadCase) {
	t.Helper()

	if src == nil || src.URI != tt.file {
		t.Fatalf("source = %+v, want file %s", src, tt.file)
	}

	if src.Line != tt.line {
		t.Errorf("line = %d, want %d", src.Line, tt.line)
	}
}

func requireLoadError(t *testing.T, got *diag.Error, tt *invalidLoadCase) {
	t.Helper()

	if got.Diagnostic.Code != tt.code {
		t.Fatalf("error = %+v, want %s with exit 2", got, tt.code)
	}

	if got.ExitCode != diag.InvalidInput {
		t.Fatalf("error = %+v, want %s with exit 2", got, tt.code)
	}

	if got.Diagnostic.Severity != diag.SeverityError {
		t.Fatalf("error = %+v, want %s with exit 2", got, tt.code)
	}

	requireLoadErrorSource(t, got.Diagnostic.Source, tt)

	for _, want := range tt.contains {
		if !strings.Contains(got.Diagnostic.Message, want) {
			t.Errorf("message %q lacks %q", got.Diagnostic.Message, want)
		}
	}
}

func TestLoadAcceptsOnlyKnownIdentifiers(t *testing.T) {
	t.Parallel()

	// A nil set accepts nothing: a caller that forgets to pass the model gets errors and not a silent pass.
	_, errs := quality.Load(fixtures, []string{"valid.yaml"}, quality.Known{})
	if len(errs) == 0 {
		t.Fatal("no error with empty known sets")
	}

	for _, err := range errs {
		if err.Diagnostic.Code != diag.QUA002 || err.ExitCode != diag.InvalidInput {
			t.Errorf("error = %+v, want CO-QUA-002 with exit 2", err)
		}
	}
}

func TestLoadGlobMergesFilesInOrder(t *testing.T) {
	t.Parallel()

	got, errs := loadFixtures(t, "valid.yaml", "adr-unregistered.yaml")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	if len(got.Scenarios) != 4 || len(got.Files) != 2 {
		t.Fatalf("scenarios=%d files=%d", len(got.Scenarios), len(got.Files))
	}

	if got.Files[0].Path != "adr-unregistered.yaml" {
		t.Errorf("files are not in path order: %+v", got.Files)
	}
}
