package packs_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

type infraRule struct {
	family      string
	declaration string
}

// infraRules are the rules of the infrastructure pack with the family and the start of the declaration of each.
var infraRules = map[string]infraRule{
	"INFRA-PLC-001": {"deployment-placement", "required forAll from-workload placed-in deployedIn=true"},
	"INFRA-PLC-002": {"deployment-placement", "forbidden forAll from-workload placed-in deployedIn=false"},
	"INFRA-TRC-001": {"deployment-trace", "required exists from-workload placed-in"},
	"INFRA-TRC-002": {"deployment-trace", "required forAll from-workload uses-image"},
	"INFRA-TRC-003": {"deployment-trace", "required forAll from-image built-from"},
	"INFRA-TRC-004": {"deployment-trace", "required forAll from-workload uses-image"},
}

var infraIDPattern = regexp.MustCompile(`^INFRA-[A-Z]+-\d{3}$`)

func TestInfrastructurePackCompilesWithoutDiagnostics(t *testing.T) {
	t.Parallel()

	result := compileCleanPack(t, "infrastructure")

	if len(result.Plans) != 6 {
		t.Fatalf("plans: got %d, want 6", len(result.Plans))
	}
}

// assertInfraRule checks that one compiled rule of the infrastructure pack is typed and advisory by default.
func assertInfraRule(t *testing.T, plan *policy.RulePlan, want infraRule) {
	t.Helper()

	if !infraIDPattern.MatchString(plan.RuleID) || strings.HasPrefix(plan.RuleID, "CORE-") {
		t.Errorf("rule ID %s", plan.RuleID)
	}

	if !allTrue(plan.Family == want.family, plan.Enforcement == "", plan.Obligation != nil) {
		t.Errorf("%s family=%q enforcement=%q obligation=%v", plan.RuleID, plan.Family, plan.Enforcement, plan.Obligation)

		return
	}

	got := plan.Obligation.Declaration()
	if !allTrue(strings.HasPrefix(got, want.declaration), len(plan.Obligation.Scope.Environments) == 0) {
		t.Errorf("%s declaration %q scope %+v, want prefix %q", plan.RuleID, got, plan.Obligation.Scope, want.declaration)
	}

	requireTrimmedDocs(t, plan)
}

func TestInfrastructureRulesAreTypedAndAdvisoryByDefault(t *testing.T) {
	t.Parallel()

	plans := compilePack(t, "infrastructure").Plans
	for i := range plans {
		plan := &plans[i]

		want, ok := infraRules[plan.RuleID]
		if !ok {
			t.Errorf("unexpected rule %s", plan.RuleID)

			continue
		}

		assertInfraRule(t, plan, want)
	}
}

func TestInfrastructurePassCompleteness(t *testing.T) {
	t.Parallel()

	want := map[string]policy.Completeness{
		"INFRA-PLC-001": policy.CompletenessAny,
		"INFRA-TRC-001": policy.CompletenessAny,
		"INFRA-TRC-002": policy.CompletenessPartial,
		"INFRA-TRC-003": policy.CompletenessAny,
		"INFRA-PLC-002": "",
		"INFRA-TRC-004": "",
	}

	plans := compilePack(t, "infrastructure").Plans
	for i := range plans {
		plan := &plans[i]

		if got := plan.Obligation.PassCompleteness; got != want[plan.RuleID] {
			t.Errorf("%s passCompleteness %q, want %q", plan.RuleID, got, want[plan.RuleID])
		}
	}
}
