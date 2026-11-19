package conformance_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// ---- 10.6: conflicts in the pipeline ----

func conflictPlans() []policy.RulePlan {
	return []policy.RulePlan{
		typedRule("A-require", []string{"billing"}, policy.ObligationRequired, observation.KindRoutesTo, policy.DirectionToSubject, nil),
		typedRule("B-forbid", []string{"billing"}, policy.ObligationForbidden, observation.KindRoutesTo, policy.DirectionToSubject, nil),
	}
}

func TestContradictingRulesStopTheRun(t *testing.T) { // POL-05
	t.Parallel()

	w := newWorld(observation.KindRoutesTo)
	_, err := conformance.CheckPolicyConflicts(conflictPlans(), w.model, &w.typed)

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want a diagnostic", err)
	}

	if failure.ExitCode != diag.InvalidInput {
		t.Fatalf("exit code = %d, want 2", failure.ExitCode)
	}

	text := failure.Error()
	for _, want := range []string{diag.POL017, "A-require", "B-forbid", "billing", "production"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q is not in %q", want, text)
		}
	}
}

// isSupersededByBForbid says whether the rule lost to B-forbid under version 1 of the precedence pack-a.
func isSupersededByBForbid(rule *conformance.RuleResult) bool {
	by := rule.SupersededBy
	if by == nil || by.RuleID != "B-forbid" {
		return false
	}

	return by.PrecedenceVersion == 1 && by.PrecedenceID == "pack-a"
}

func TestPrecedenceResolvesAndShowsTheLoser(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindRoutesTo)
	w.plans = conflictPlans()
	w.typed.Precedences = []policy.NamedPrecedence{{ID: "pack-a", Precedence: policy.Precedence{Version: 1, Order: []string{"B-forbid", "A-require"}}}}

	if _, err := conformance.CheckPolicyConflicts(w.plans, w.model, &w.typed); err != nil {
		t.Fatalf("precedence resolves the conflict: %v", err)
	}

	out := w.evaluate()
	loser := typedRuleResult(t, &out, "A-require")
	winner := typedRuleResult(t, &out, "B-forbid")

	if !isSupersededByBForbid(loser) {
		t.Fatalf("superseded by = %+v", loser.SupersededBy)
	}

	if winner.SupersededBy != nil || winner.Verdict != conformance.VerdictPass {
		t.Fatalf("winner = %+v", winner)
	}

	requireLoserVisibleButNotDeciding(t, loser)

	if len(out.Findings) != 0 {
		t.Fatalf("a superseded obligation must not gate: %+v", out.Findings)
	}
}

// requireLoserVisibleButNotDeciding requires that a superseded rule is not applicable, keeps its one obligation, and
// that the obligation keeps its own violation.
func requireLoserVisibleButNotDeciding(t *testing.T, loser *conformance.RuleResult) {
	t.Helper()

	if loser.Verdict != conformance.VerdictNotApplicable || len(loser.Obligations) != 1 {
		t.Fatalf("the loser stays visible but does not decide: %+v", loser)
	}

	if !loser.Obligations[0].Superseded() {
		t.Fatalf("the loser stays visible but does not decide: %+v", loser)
	}

	if loser.Obligations[0].Verdict != conformance.VerdictViolation {
		t.Fatalf("the loser keeps its own result: %s", loser.Obligations[0].Verdict)
	}
}

func TestPrecedenceDoesNotResolveAnUnlistedPair(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindRoutesTo)
	w.typed.Precedences = []policy.NamedPrecedence{{ID: "pack-a", Precedence: policy.Precedence{Version: 1, Order: []string{"B-forbid", "C-other"}}}}

	if _, err := conformance.CheckPolicyConflicts(conflictPlans(), w.model, &w.typed); err == nil {
		t.Fatal("a pair that precedence does not list must still conflict")
	}
}

func TestEvaluateNeverPicksAWinnerForAnUnresolvedConflict(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindRoutesTo)
	w.plans = conflictPlans()

	out := w.evaluate()

	for _, id := range []string{"A-require", "B-forbid"} {
		if rr := typedRuleResult(t, &out, id); rr.Verdict != conformance.VerdictError {
			t.Fatalf("%s = %s, want error: no rule wins by order", id, rr.Verdict)
		}
	}
}
