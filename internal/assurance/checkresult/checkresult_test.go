package checkresult_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/statement"
)

func result() map[string]any {
	return map[string]any{
		"check":       "unit-tests",
		"outcome":     "passed",
		"revision":    "0123456789abcdef0123456789abcdef01234567",
		"environment": "production",
		"evaluator":   map[string]any{"name": "go-test", "version": "1.27.3"},
		"evidence":    []any{map[string]any{"name": "report", "digest": map[string]any{"sha256": "aaa"}}},
		"time":        "2026-10-30T12:00:00Z",
		"covers":      []any{"shop", "CTRL-1"},
	}
}

func parse(t *testing.T, predicate map[string]any) (checkresult.Result, error) {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": statement.PredicateCheckResult, "predicate": predicate,
		"subject": []map[string]any{{"digest": map[string]string{"gitCommit": "0123456789abcdef0123456789abcdef01234567"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := statement.Parse("build/results/unit.json", data)
	if err != nil {
		t.Fatal(err)
	}

	return checkresult.Parse(&loaded.Statement)
}

// without copies the predicate and removes one field; "evaluator.name" removes a nested one.
func without(predicate map[string]any, path string) map[string]any {
	out := maps.Clone(predicate)

	if path == "evaluator.name" || path == "evaluator.version" {
		evaluator, _ := out["evaluator"].(map[string]any)
		child := maps.Clone(evaluator)
		delete(child, path[len("evaluator."):])
		out["evaluator"] = child

		return out
	}

	delete(out, path)

	return out
}

// resultSummary is the fields of a complete result that the first test compares. Covers is compared apart, as a list.
type resultSummary struct {
	check            string
	outcome          string
	environment      string
	evaluatorName    string
	evaluatorVersion string
	evidenceDigests  string
	missing          int
	hasTime          bool
}

func summaryOf(got *checkresult.Result) resultSummary {
	digests := make([]string, 0, len(got.Evidence))

	for i := range got.Evidence {
		digests = append(digests, got.Evidence[i].Digest["sha256"])
	}

	return resultSummary{
		check: got.Check, outcome: got.Outcome, environment: got.Environment,
		evaluatorName: got.Evaluator.Name, evaluatorVersion: got.Evaluator.Version,
		evidenceDigests: strings.Join(digests, ","), missing: len(got.Missing), hasTime: !got.When().IsZero(),
	}
}

func TestParseReadsACompleteResult(t *testing.T) {
	t.Parallel()

	got, err := parse(t, result())
	if err != nil {
		t.Fatal(err)
	}

	want := resultSummary{
		check: "unit-tests", outcome: "passed", environment: "production",
		evaluatorName: "go-test", evaluatorVersion: "1.27.3", evidenceDigests: "aaa", hasTime: true,
	}

	if summaryOf(&got) != want {
		t.Errorf("result = %+v", got)
	}

	if !reflect.DeepEqual(got.Covers, []string{"shop", "CTRL-1"}) {
		t.Errorf("result = %+v", got)
	}
}

// missingCase is a predicate and the fields that Parse must list as missing for it.
type missingCase struct {
	predicate map[string]any
	name      string
	want      []string
}

func missingCases() []missingCase {
	emptyEnvironment := result()
	emptyEnvironment["environment"] = ""

	return []missingCase{
		{name: "an empty string is missing", predicate: emptyEnvironment, want: []string{"environment"}},
		{name: "several fields", predicate: without(without(result(), "environment"), "check"), want: []string{"check", "environment"}},
		{
			name: "nothing", predicate: map[string]any{},
			want: []string{"check", "outcome", "revision", "environment", "evaluator.name", "evaluator.version", "covers", "time"},
		},
	}
}

func TestParseListsEachMissingField(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"check", "outcome", "revision", "environment", "evaluator.name", "evaluator.version", "time", "covers"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			got, err := parse(t, without(result(), field))
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got.Missing, []string{field}) {
				t.Errorf("missing = %v, want [%s]", got.Missing, field)
			}
		})
	}

	for _, test := range missingCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, _ := parse(t, test.predicate)
			if !reflect.DeepEqual(got.Missing, test.want) {
				t.Errorf("missing = %v, want %v", got.Missing, test.want)
			}
		})
	}

	t.Run("a null covered list is missing", func(t *testing.T) {
		t.Parallel()

		checkNullCoveredListIsMissing(t)
	})
}

func checkNullCoveredListIsMissing(t *testing.T) {
	t.Helper()

	predicate := result()
	predicate["covers"] = nil

	got, _ := parse(t, predicate)
	if !reflect.DeepEqual(got.Missing, []string{"covers"}) || got.CoversDeclared() {
		t.Errorf("missing = %v, declared = %v", got.Missing, got.CoversDeclared())
	}
}

func TestAWindowSatisfiesTheTime(t *testing.T) {
	t.Parallel()

	predicate := without(result(), "time")
	predicate["window"] = map[string]any{"start": "2026-10-30T10:00:00Z", "end": "2026-10-30T11:00:00Z"}

	got, err := parse(t, predicate)
	if err != nil || len(got.Missing) != 0 {
		t.Fatalf("result = %+v, %v", got, err)
	}

	if got.When().Hour() != 11 {
		t.Fatalf("result = %+v, %v", got, err)
	}

	predicate["window"] = map[string]any{"start": "2026-10-30T10:00:00Z"}

	if got, _ = parse(t, predicate); !reflect.DeepEqual(got.Missing, []string{"time"}) {
		t.Errorf("a window without an end: missing = %v", got.Missing)
	}
}

// TestMissingAndEmptyCoveredListStayDifferent: a missing list is a missing field; an explicit empty list is a
// statement that the result covers nothing.
func TestMissingAndEmptyCoveredListStayDifferent(t *testing.T) {
	t.Parallel()

	checkEmptyCoveredList(t)
	checkAbsentCoveredList(t)
}

func checkEmptyCoveredList(t *testing.T) {
	t.Helper()

	empty := result()
	empty["covers"] = []any{}

	got, err := parse(t, empty)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Missing) != 0 || !got.CoversDeclared() {
		t.Errorf("empty list: missing = %v, declared = %v, nothing = %v", got.Missing, got.CoversDeclared(), got.CoversNothing())
	}

	if !got.CoversNothing() {
		t.Errorf("empty list: missing = %v, declared = %v, nothing = %v", got.Missing, got.CoversDeclared(), got.CoversNothing())
	}
}

func checkAbsentCoveredList(t *testing.T) {
	t.Helper()

	absent, err := parse(t, without(result(), "covers"))
	if err != nil {
		t.Fatal(err)
	}

	if absent.CoversDeclared() || absent.CoversNothing() {
		t.Errorf("absent list: %+v", absent)
	}

	if !reflect.DeepEqual(absent.Missing, []string{"covers"}) {
		t.Errorf("absent list: %+v", absent)
	}
}

func TestOutcomes(t *testing.T) {
	t.Parallel()

	for outcome, invalid := range map[string]bool{
		"passed": false, "failed": false, "errored": false, "skipped": false,
		"success": true, "PASSED": true, "": false,
	} {
		predicate := result()
		predicate["outcome"] = outcome

		got, err := parse(t, predicate)
		if err != nil {
			t.Fatal(err)
		}

		if got.InvalidOutcome() != invalid {
			t.Errorf("outcome %q: invalid = %v, want %v", outcome, got.InvalidOutcome(), invalid)
		}

		// An empty outcome is a missing field, not an invalid value.
		if outcome == "" && !reflect.DeepEqual(got.Missing, []string{"outcome"}) {
			t.Errorf("empty outcome: missing = %v", got.Missing)
		}
	}
}

func TestParseRejectsAnotherPredicateTypeOrShape(t *testing.T) {
	t.Parallel()

	other := statement.Statement{Type: statement.StatementType, PredicateType: statement.PredicatePlan, Predicate: json.RawMessage(`{}`)}
	if _, err := checkresult.Parse(&other); err == nil {
		t.Error("a plan is not a check result")
	}

	bad := result()
	bad["covers"] = "shop"

	if _, err := parse(t, bad); err == nil {
		t.Error("a covered list that is not a list must be an error")
	}
}
