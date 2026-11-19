package parity_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/parity"
)

func TestClassifyPicksTheValidationModeOfAFixture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, doc, want string
	}{
		{"timeline wins over nodes", `{"moments": [], "nodes": []}`, parity.KindTimeline},
		{"pattern by properties.nodes", `{"properties": {"nodes": {}}, "nodes": []}`, parity.KindPattern},
		{"pattern by properties.relationships", `{"properties": {"relationships": {}}}`, parity.KindPattern},
		{"architecture by nodes", `{"nodes": []}`, parity.KindArchitecture},
		{"architecture by relationships", `{"relationships": []}`, parity.KindArchitecture},
		{"properties without nodes", `{"properties": {"name": {}}}`, parity.KindNone},
		{"other object", `{"name": "x"}`, parity.KindNone},
		{"array", `[{"nodes": []}]`, parity.KindNone},
		{"not JSON", `{`, parity.KindNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parity.Classify([]byte(tt.doc)); got != tt.want {
				t.Errorf("Classify(%s) = %q, want %q", tt.doc, got, tt.want)
			}
		})
	}
}

func TestCaseAndRunNamesReplaceSlashes(t *testing.T) {
	t.Parallel()

	if got := parity.CaseName("cli/test_fixtures/getting-started"); got != "cli__test_fixtures__getting-started" {
		t.Errorf("CaseName = %q", got)
	}

	if got := parity.CaseName("cli/test_fixtures"); got != "cli__test_fixtures" {
		t.Errorf("CaseName = %q", got)
	}

	if got := parity.RunName("STEP-3/architecture.json"); got != "STEP-3__architecture" {
		t.Errorf("RunName = %q", got)
	}
}

func TestRuleIDsFindsOnlyRuleKeys(t *testing.T) {
	t.Parallel()

	source := `const rules = {
    rules: {
        'architecture-has-nodes-relationships': {
            description: 'x',
            then: [{ field: 'nodes', function: truthy }],
        },
        'flow-transitions-have-unique-sequence-numbers': {
            given: '$.flows[*].transitions',
        }
    }
};`

	want := []string{"architecture-has-nodes-relationships", "flow-transitions-have-unique-sequence-numbers"}
	if got := parity.RuleIDs(source); !slices.Equal(got, want) {
		t.Errorf("RuleIDs = %v, want %v", got, want)
	}
}

func TestCompareNumericOrdersReleases(t *testing.T) {
	t.Parallel()

	got := []string{"1.10", "1.2", "1.0-rc2", "1.0", "1.0-rc1", "1.1"}
	slices.SortFunc(got, parity.CompareNumeric)

	want := []string{"1.0", "1.0-rc1", "1.0-rc2", "1.1", "1.2", "1.10"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestRewritePathWithIDsUsesTheUniqueIDOfArrayItems(t *testing.T) {
	t.Parallel()

	doc := map[string]any{"nodes": []any{
		map[string]any{"unique-id": "a", "interfaces": []any{map[string]any{"host": "x"}}},
	}}

	tests := map[string]string{
		"/nodes/0/unique-id":         "/nodes/a/unique-id",
		"/nodes/0/interfaces/0/host": "/nodes/a/interfaces/0/host",
		"/nodes/5/unique-id":         "/nodes/5/unique-id",
		"/":                          "/",
		"/missing/0":                 "/missing/0",
	}

	for in, want := range tests {
		if got := parity.RewritePathWithIDs(in, doc); got != want {
			t.Errorf("RewritePathWithIDs(%q) = %q, want %q", in, got, want)
		}
	}
}

// The random temporary directory of a run never reaches expected.json: its case copy and any other path below it
// become fixed placeholders, also in the resolved form of a symlinked temporary directory, and the other fields stay.
func TestWithoutTempPathsMakesRecordedMessagesStable(t *testing.T) {
	t.Parallel()

	link := filepath.Join(t.TempDir(), "link")
	// The resolved form of link, as the calm CLI names it: t.TempDir() itself is a symlink on macOS (/var).
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err = os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	outcome := parity.Outcome{LoadError: true, Findings: []parity.Finding{
		{Rule: "r", Severity: "error", Path: "/nodes/0", Message: "ENOENT: open '" + filepath.Join(link, "case", "configs", "missing.json") + "'"},
		{Rule: "r", Severity: "error", Path: "/nodes/1", Message: "home is " + link + "/.calm and " + filepath.Join(target, "case", "a.json")},
		{Rule: "r", Severity: "warning", Path: "/nodes/2", Message: "no path here"},
	}}

	got := parity.WithoutTempPaths(outcome, link)

	want := []string{
		"ENOENT: open '<case>/configs/missing.json'",
		"home is <tmp>/.calm and <case>/a.json",
		"no path here",
	}

	for i, f := range got.Findings {
		if f.Message != want[i] || f.Path != outcome.Findings[i].Path || f.Rule != "r" {
			t.Errorf("finding %d = %+v, want message %q", i, f, want[i])
		}
	}

	if !got.LoadError || !strings.Contains(outcome.Findings[0].Message, link) {
		t.Error("the other fields are kept and the input outcome is not changed")
	}
}
