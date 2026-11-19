package policy_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

func claimRule(body string) string {
	return rule("    require:\n      claim:\n" + body)
}

func TestCompilesAClaimRequirement(t *testing.T) {
	t.Parallel()

	// The registry is read at evaluation time, so a claim that is not registered compiles like one that is.
	for _, tc := range []struct {
		name, body, id, environment string
	}{
		{"registered claim", "        id: exact-lineage\n", "exact-lineage", ""},
		{"claim with an environment", "        id: complete-resource-set\n        environment: production\n", "complete-resource-set", "production"},
		{"claim that no registry lists", "        id: slsa-build-level-3\n", "slsa-build-level-3", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := compileOne(t, pack(claimRule(tc.body)))
			wantCodes(t, &res)

			p := res.Plans[0]
			if p.Predicate.Family != policy.FamilyAssuranceClaim || p.Predicate.Maturity != policy.MaturityAdvisory {
				t.Fatalf("predicate = %+v", p.Predicate)
			}

			if got := p.Predicate.AssuranceClaim; got.ID != tc.id || got.Environment != tc.environment {
				t.Errorf("claim = %+v", got)
			}

			if len(p.Subjects) != 0 || len(p.Targets) != 0 {
				t.Errorf("subjects %v targets %v", p.Subjects, p.Targets)
			}

			if p.Predicate.Family.EdgeFindings() {
				t.Error("an assurance-claim rule must not be reducible to changed code")
			}
		})
	}
}

func TestAClaimRuleRefusesARuleSubject(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    require:\n      claim: {id: exact-lineage}\n")))
	wantCodes(t, &res, diag.POL001)
}

func TestRejectsAClaimThatIsNotKebabCase(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"ExactLineage", "exact_lineage", "-exact", "exact--lineage", "''"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			res := compileOne(t, pack(claimRule("        id: "+id+"\n")))
			if err := policy.ThrowOnErrors(res.Diagnostics); err == nil {
				t.Fatalf("no error for id %q", id)
			}

			if len(res.Plans) != 0 {
				t.Errorf("plans = %+v", res.Plans)
			}
		})
	}
}
