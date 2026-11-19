package verdict_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const (
	revision = "0123456789abcdef0123456789abcdef01234567"
	other    = "fedcba9876543210fedcba9876543210fedcba98"
)

func producers() []workspace.Producer {
	return []workspace.Producer{
		{ID: "ci", Claims: []string{workspace.ProducerClaimCheckResult}},
		{ID: "builder", Claims: []string{workspace.ProducerClaimBuildProvenance}},
	}
}

func verified(producer string) statement.Verification {
	return statement.Verification{Signature: statement.SignatureVerified, ProducerID: producer, Subject: statement.SubjectUnchecked}
}

func predicate(outcome string) map[string]any {
	return map[string]any{
		"check": "unit-tests", "outcome": outcome, "revision": revision, "environment": "production",
		"evaluator": map[string]any{"name": "go-test", "version": "1"},
		"time":      now.Add(-time.Hour).Format(time.RFC3339),
		"covers":    []any{"shop"},
	}
}

func read(t *testing.T, predicate map[string]any) checkresult.Result {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": statement.PredicateCheckResult, "predicate": predicate,
		"subject": []map[string]any{{"digest": map[string]string{"gitCommit": revision}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := statement.Parse("results/unit.json", data)
	if err != nil {
		t.Fatal(err)
	}

	result, err := checkresult.Parse(&loaded.Statement)
	if err != nil {
		t.Fatal(err)
	}

	return result
}

// input is an admissible question; each case changes one thing.
func input(result *checkresult.Result) verdict.Input {
	return verdict.Input{
		Result: result, Verification: verified("ci"), Path: "results/unit.json", Digest: "sha256:aa",
		Context:   assurance.Context{Now: now, Environment: "production", Revision: revision},
		Producers: producers(),
		MaxAge:    verdict.MaxAge(14),
	}
}

func TestAdmitCollectsEveryReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*verdict.Input, map[string]any)
		want   []assurance.Detail
	}{
		{"trusted passed result for the evaluated revision", func(*verdict.Input, map[string]any) {}, nil},
		{"A36 failed run is admissible", func(_ *verdict.Input, p map[string]any) { p["outcome"] = "failed" }, nil},
		{"A36 different revision", func(_ *verdict.Input, p map[string]any) { p["revision"] = other },
			[]assurance.Detail{assurance.DetailDifferentRevision}},
		{"different environment", func(_ *verdict.Input, p map[string]any) { p["environment"] = "staging" },
			[]assurance.Detail{assurance.DetailDifferentEnvironment}},
		{"stale", func(_ *verdict.Input, p map[string]any) {
			p["time"] = now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
		},
			[]assurance.Detail{assurance.DetailStale}},
		{"no age limit configured", func(in *verdict.Input, p map[string]any) {
			in.MaxAge = verdict.MaxAge(0)
			p["time"] = now.Add(-300 * 24 * time.Hour).Format(time.RFC3339)
		}, nil},
		{"uncommitted changes", func(in *verdict.Input, _ map[string]any) { in.Context.DirtyTree = true },
			[]assurance.Detail{assurance.DetailSubjectNotEvaluatedState}},
		{"producer not declared", func(in *verdict.Input, _ map[string]any) { in.Verification = verified("stranger") },
			[]assurance.Detail{assurance.DetailProducerNotTrusted}},
		{"producer not trusted for check results", func(in *verdict.Input, _ map[string]any) { in.Verification = verified("builder") },
			[]assurance.Detail{assurance.DetailProducerNotTrustedForClaim}},
		{"unsigned", func(in *verdict.Input, _ map[string]any) {
			in.Verification = statement.Verification{Signature: statement.SignatureNone, Subject: statement.SubjectUnchecked}
		}, []assurance.Detail{assurance.DetailNotAuthenticated}},
		{"missing environment", func(_ *verdict.Input, p map[string]any) { delete(p, "environment") },
			[]assurance.Detail{assurance.MissingField("environment")}},
		{"invalid outcome", func(_ *verdict.Input, p map[string]any) { p["outcome"] = "maybe" },
			[]assurance.Detail{assurance.DetailInvalidOutcome}},
		{"other environment and untrusted producer", func(in *verdict.Input, p map[string]any) {
			p["environment"] = "staging"
			in.Verification = verified("stranger")
		}, []assurance.Detail{assurance.DetailDifferentEnvironment, assurance.DetailProducerNotTrusted}},
		{"every condition at once", func(in *verdict.Input, p map[string]any) {
			p["environment"] = "staging"
			p["revision"] = other
			p["time"] = now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
			in.Verification = verified("builder")
			in.Context.DirtyTree = true
		}, []assurance.Detail{
			assurance.DetailDifferentEnvironment, assurance.DetailDifferentRevision, assurance.DetailProducerNotTrustedForClaim,
			assurance.DetailStale, assurance.DetailSubjectNotEvaluatedState,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			p := predicate("passed")
			in := input(nil)
			test.change(&in, p)

			result := read(t, p)
			in.Result = &result

			got := verdict.Admit(&in)

			want := slices.Clone(test.want)
			slices.Sort(want)

			if want == nil {
				want = []assurance.Detail{}
			}

			if !slices.Equal(got.Admission.Details, want) {
				t.Errorf("details = %v, want %v", got.Admission.Details, want)
			}

			if got.Admission.Admissible != (len(want) == 0) {
				t.Errorf("admissible = %v for details %v", got.Admission.Admissible, want)
			}

			if len(want) == 0 && got.Admission.Integrity != assurance.IntegrityAuthenticated {
				t.Errorf("integrity = %s, want producer-authenticated", got.Admission.Integrity)
			}
		})
	}
}

// admitted builds one result through the engine; the fields of the question are changed by change.
func admitted(t *testing.T, outcome, path string, change func(*verdict.Input, map[string]any)) verdict.Admitted {
	t.Helper()

	p := predicate(outcome)
	in := input(nil)

	if change != nil {
		change(&in, p)
	}

	result := read(t, p)
	in.Result = &result
	in.Path = path
	in.Digest = "sha256:" + path

	return verdict.Admit(&in)
}

func untrusted(in *verdict.Input, _ map[string]any) { in.Verification = verified("stranger") }

func TestEvaluateMapsOutcomesToVerdicts(t *testing.T) {
	t.Parallel()

	differentRevision := func(_ *verdict.Input, p map[string]any) { p["revision"] = other }

	tests := []struct {
		name     string
		results  []verdict.Admitted
		verdict  string
		reason   string
		evidence []string
		details  []string
	}{
		{"passed and admissible", []verdict.Admitted{admitted(t, "passed", "a", nil)}, verdict.Pass, "", []string{"sha256:a"}, nil},
		{"failed and admissible", []verdict.Admitted{admitted(t, "failed", "a", nil)}, verdict.Violation, "", []string{"sha256:a"}, nil},
		{"errored and admissible", []verdict.Admitted{admitted(t, "errored", "a", nil)}, verdict.Violation, "", []string{"sha256:a"}, nil},
		{"skipped only", []verdict.Admitted{admitted(t, "skipped", "a", nil)}, verdict.Unknown, verdict.ReasonNoEvidenceSupplied,
			[]string{"sha256:a"}, []string{verdict.DetailSkipped}},
		{"no result", nil, verdict.Unknown, verdict.ReasonNoEvidenceSupplied, nil, []string{verdict.DetailNoAdmissibleResult}},
		{"A36 different revision passed", []verdict.Admitted{admitted(t, "passed", "a", differentRevision)},
			verdict.Unknown, verdict.ReasonNoEvidenceSupplied, nil,
			[]string{verdict.DetailNoAdmissibleResult, "a is inadmissible: different-revision"}},
		{"inadmissible failure is not a violation", []verdict.Admitted{admitted(t, "failed", "a", untrusted)},
			verdict.Unknown, verdict.ReasonNoEvidenceSupplied, nil,
			[]string{verdict.DetailNoAdmissibleResult, "a is inadmissible: producer-not-trusted"}},
		{"inadmissible pass next to an admissible failure", []verdict.Admitted{
			admitted(t, "passed", "a", untrusted), admitted(t, "failed", "b", nil),
		}, verdict.Violation, "", []string{"sha256:b"}, []string{"a is inadmissible: producer-not-trusted"}},
		{"skipped next to a pass says nothing", []verdict.Admitted{
			admitted(t, "skipped", "a", nil), admitted(t, "passed", "b", nil),
		}, verdict.Pass, "", []string{"sha256:b"}, nil},
		{"two passes agree", []verdict.Admitted{
			admitted(t, "passed", "b", nil), admitted(t, "passed", "a", nil),
		}, verdict.Pass, "", []string{"sha256:a", "sha256:b"}, nil},
		{"failed and errored agree", []verdict.Admitted{
			admitted(t, "failed", "a", nil), admitted(t, "errored", "b", nil),
		}, verdict.Violation, "", []string{"sha256:a", "sha256:b"}, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := verdict.Evaluate("unit-tests", test.results)

			if got.Verdict != test.verdict || got.Reason != test.reason {
				t.Errorf("verdict = %s (%q), want %s (%q)", got.Verdict, got.Reason, test.verdict, test.reason)
			}

			if !slices.Equal(got.Evidence, test.evidence) {
				t.Errorf("evidence = %v, want %v", got.Evidence, test.evidence)
			}

			if !slices.Equal(got.Details, test.details) {
				t.Errorf("details = %q, want %q", got.Details, test.details)
			}

			if len(got.Diagnostics) != 0 {
				t.Errorf("unexpected diagnostics: %v", got.Diagnostics)
			}
		})
	}
}

func TestEvaluateIgnoresOtherChecks(t *testing.T) {
	t.Parallel()

	got := verdict.Evaluate("lint", []verdict.Admitted{admitted(t, "passed", "a", nil)})
	if got.Verdict != verdict.Unknown || got.Reason != verdict.ReasonNoEvidenceSupplied {
		t.Errorf("a result of unit-tests gave %s (%q) for lint", got.Verdict, got.Reason)
	}
}

func TestDisagreeingAdmissibleResultsConflict(t *testing.T) {
	t.Parallel()

	// Neither order nor time may decide: the failed result is the older one in one case and the newer in the other.
	older := func(_ *verdict.Input, p map[string]any) { p["time"] = now.Add(-5 * time.Hour).Format(time.RFC3339) }

	tests := []struct {
		name    string
		results []verdict.Admitted
	}{
		{"passed then failed", []verdict.Admitted{admitted(t, "passed", "a", nil), admitted(t, "failed", "b", nil)}},
		{"failed then passed", []verdict.Admitted{admitted(t, "failed", "b", nil), admitted(t, "passed", "a", nil)}},
		{"failed is older", []verdict.Admitted{admitted(t, "passed", "a", nil), admitted(t, "failed", "b", older)}},
		{"passed is older", []verdict.Admitted{admitted(t, "passed", "a", older), admitted(t, "failed", "b", nil)}},
		{"errored counts as not passed", []verdict.Admitted{admitted(t, "passed", "a", nil), admitted(t, "errored", "b", nil)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := verdict.Evaluate("unit-tests", test.results)

			if got.Verdict != verdict.Unknown || got.Reason != verdict.ReasonEvidenceConflict {
				t.Fatalf("verdict = %s (%q), want unknown (evidence-conflict)", got.Verdict, got.Reason)
			}

			if !slices.Equal(got.Evidence, []string{"sha256:a", "sha256:b"}) {
				t.Errorf("evidence = %v", got.Evidence)
			}

			if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != diag.PRV012 {
				t.Fatalf("diagnostics = %v, want one %s", got.Diagnostics, diag.PRV012)
			}

			if got.Diagnostics[0].Severity != diag.SeverityWarning {
				t.Errorf("severity = %v, want a warning", got.Diagnostics[0].Severity)
			}

			// The message names both results.
			message := got.Diagnostics[0].Message
			for _, name := range []string{"a (sha256:a)", "b (sha256:b)"} {
				if !strings.Contains(message, name) {
					t.Errorf("message %q does not name %s", message, name)
				}
			}
		})
	}
}

func TestAnInadmissibleResultDoesNotConflict(t *testing.T) {
	t.Parallel()

	got := verdict.Evaluate("unit-tests", []verdict.Admitted{
		admitted(t, "passed", "a", nil), admitted(t, "failed", "b", untrusted),
	})

	if got.Verdict != verdict.Pass || len(got.Diagnostics) != 0 {
		t.Errorf("verdict = %s with diagnostics %v, want pass without a conflict", got.Verdict, got.Diagnostics)
	}
}
