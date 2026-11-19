package packs_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

// assuranceClaims maps each rule of the assurance pack to the registered claim that it requires.
var assuranceClaims = map[string]string{
	"ASR-REN-001": "complete-resource-set",
	"ASR-PRV-001": "exact-lineage",
	"ASR-PRV-002": "runtime-drift",
}

// requiresClaim reports whether the predicate of the plan is the assurance claim with this ID.
func requiresClaim(plan *policy.RulePlan, claim string) bool {
	if plan.Predicate.Family != policy.FamilyAssuranceClaim {
		return false
	}

	registered := plan.Predicate.AssuranceClaim

	return registered != nil && registered.ID == claim
}

// assertAssuranceRule checks that one compiled rule of the assurance pack requires its claim and is a warning.
func assertAssuranceRule(t *testing.T, plan *policy.RulePlan, claim string) {
	t.Helper()

	if !requiresClaim(plan, claim) {
		t.Errorf("%s predicate = %+v, want claim %s", plan.RuleID, plan.Predicate, claim)
	}

	if !allTrue(plan.Severity == policy.SeverityWarning, plan.Enforcement == "", len(plan.Subjects) == 0) {
		t.Errorf("%s severity=%s enforcement=%q subjects=%v", plan.RuleID, plan.Severity, plan.Enforcement, plan.Subjects)
	}

	requireTrimmedDocs(t, plan)
	requirePlanPack(t, plan, "assurance")
}

func TestAssurancePackRequiresOneRegisteredClaimPerRule(t *testing.T) {
	t.Parallel()

	result := compileCleanPack(t, "assurance")

	if len(result.Plans) != len(assuranceClaims) {
		t.Fatalf("plans: got %d, want %d", len(result.Plans), len(assuranceClaims))
	}

	for i := range result.Plans {
		plan := &result.Plans[i]

		claim, ok := assuranceClaims[plan.RuleID]
		if !ok {
			t.Errorf("unexpected rule %s", plan.RuleID)

			continue
		}

		assertAssuranceRule(t, plan, claim)
	}
}
