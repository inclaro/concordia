package quality_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/quality"
)

func structuralScenario(rules ...string) *quality.Scenario {
	return &quality.Scenario{
		ID: "QS-S", Mode: quality.ModeStructural, Area: quality.AreaModifiability,
		Criterion: quality.Criterion{Rules: rules},
	}
}

func TestEvaluateStructural(t *testing.T) {
	t.Parallel()

	pass := quality.RuleResult{Verdict: quality.VerdictPass}
	coverage := quality.RuleResult{Verdict: quality.VerdictUnknown, Reason: quality.ReasonCoverageIncomplete}

	tests := []struct { //nolint:govet // a test table.
		name       string
		rules      []string
		results    map[string]quality.RuleResult
		verdict    quality.Verdict
		reason     string
		detailsLen int
	}{
		{"all pass", []string{"a", "b"}, map[string]quality.RuleResult{"a": pass, "b": pass}, quality.VerdictPass, "", 2},
		{"violation outranks pass", []string{"a", "b"},
			map[string]quality.RuleResult{"a": pass, "b": {Verdict: quality.VerdictViolation}}, quality.VerdictViolation, "", 2},
		{"error outranks violation", []string{"a", "b"},
			map[string]quality.RuleResult{"a": {Verdict: quality.VerdictError}, "b": {Verdict: quality.VerdictViolation}}, quality.VerdictError, "", 2},
		{"coverage unknown propagates", []string{"a", "b"},
			map[string]quality.RuleResult{"a": pass, "b": coverage}, quality.VerdictUnknown, quality.ReasonCoverageIncomplete, 2},
		{"violation outranks unknown", []string{"a", "b"},
			map[string]quality.RuleResult{"a": coverage, "b": {Verdict: quality.VerdictViolation}}, quality.VerdictViolation, "", 2},
		{"rule that was not evaluated", []string{"a", "b"},
			map[string]quality.RuleResult{"a": pass}, quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, 2},
		{"not-applicable rule is no pass", []string{"a"},
			map[string]quality.RuleResult{"a": {Verdict: quality.VerdictNotApplicable}}, quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, 1},
		{"unknown without a reason", []string{"a"},
			map[string]quality.RuleResult{"a": {Verdict: quality.VerdictUnknown}}, quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, 1},
		{"no rules named", nil, nil, quality.VerdictUnknown, quality.ReasonNoEvidenceSupplied, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := quality.EvaluateStructural(structuralScenario(tt.rules...), tt.results)

			if got.Verdict != tt.verdict || got.Reason != tt.reason {
				t.Errorf("verdict=%s reason=%q, want %s %q", got.Verdict, got.Reason, tt.verdict, tt.reason)
			}

			if got.Mode != quality.ModeStructural || got.Scenario != "QS-S" {
				t.Errorf("label = %s %s", got.Scenario, got.Mode)
			}

			if len(got.Details) != tt.detailsLen {
				t.Errorf("details = %v", got.Details)
			}
		})
	}
}

func TestStructuralPassDoesNotSatisfyMeasuredScenario(t *testing.T) {
	t.Parallel()

	structural := structuralScenario("a")
	got := quality.EvaluateStructural(structural, map[string]quality.RuleResult{"a": {Verdict: quality.VerdictPass}})

	if !got.Satisfies(structural) {
		t.Fatal("a structural pass must satisfy its structural scenario")
	}

	// The same id and a pass, but the scenario is measured: the label of the result decides.
	for _, mode := range []quality.Mode{quality.ModeMeasured, quality.ModeReviewed} {
		other := &quality.Scenario{ID: structural.ID, Mode: mode}
		if got.Satisfies(other) {
			t.Errorf("a structural pass satisfies a %s scenario", mode)
		}
	}

	unknown := quality.Result{Scenario: "QS-S", Mode: quality.ModeStructural, Verdict: quality.VerdictUnknown}
	if unknown.Satisfies(structural) {
		t.Error("an unknown result satisfies a scenario")
	}
}

func TestStructuralDetailsAreDeterministic(t *testing.T) {
	t.Parallel()

	rules := map[string]quality.RuleResult{
		"b": {Verdict: quality.VerdictPass}, "a": {Verdict: quality.VerdictViolation, Detail: "edge x"},
	}
	sc := structuralScenario("b", "a")

	first := quality.EvaluateStructural(sc, rules)

	for range 20 {
		if again := quality.EvaluateStructural(sc, rules); !slices.Equal(first.Details, again.Details) {
			t.Fatalf("details differ: %v vs %v", first.Details, again.Details)
		}
	}

	if first.Details[0] != "b: pass" || first.Details[1] != "a: violation (edge x)" {
		t.Errorf("details = %v", first.Details)
	}
}
