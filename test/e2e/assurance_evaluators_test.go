package e2e_test

import (
	"testing"
)

// A35: a NetworkPolicy that selects `db` is in the rendered manifests. CBM 0.12.1 emits no selection edge for an empty
// `podSelector` or a `matchExpressions` selector, so without the evaluator no isolation is claimed; with it, the
// claim is evaluated from the manifests and the assumption is listed.
func TestAssuranceA35SelectorEdgeWithoutEvaluator(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	rules := []string{
		asrEffectiveRule("ACC-001", "shop-api", "denied"),
		asrEffectiveRule("ACC-002", "shop-web", "allowed", 8080),
		asrEffectiveRule("ACC-003", "shop-web", "allowed", 22),
	}

	t.Run("no evaluator", func(t *testing.T) {
		t.Parallel()

		for name, evaluators := range map[string]string{"no evaluators": "", "evaluator of another kind": "    - {environment: production, kinds: [rbac], assumes: [rbac-authorizer-enabled]}\n"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				asrEvRequireNoEvaluator(t, rules, evaluators)
			})
		}
	})

	t.Run("evaluator with the declared assumption", func(t *testing.T) {
		t.Parallel()

		r := (&asrWorld{t: t, withShop: true, rules: rules, evaluators: asrNetworkEnforced}).build()
		doc, _ := r.check()

		asrEvRequireEnforcedVerdicts(t, doc)
		asrEvRequireViolationExplained(t, r, doc)
		asrEvRequireEvaluationProvenance(t, r, doc)
	})

	t.Run("evaluator without the assumption", func(t *testing.T) {
		t.Parallel()

		asrEvRequireAssumptionUndeclared(t, rules)
	})
}

// A36: a TESTS edge is a candidate, never a result. Only an admissible check result for the evaluated revision passes
// the obligation; a failed run is a violation and a result for another revision is not admissible.
func TestAssuranceA36CheckResults(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	for _, c := range asrEvCheckCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			asrEvRunCheckCase(t, &c)
		})
	}

	t.Run("untrusted producer", func(t *testing.T) {
		t.Parallel()

		asrEvRequireUntrustedProducerUnknown(t)
	})
}

// A37: a measurement above the threshold in the right environment is a violation with the value, threshold, and window;
// a measurement from another environment decides nothing; a value within the threshold passes. The three modes stay
// separate: the structural scenario passes, and the reviewed scenario is unknown with its reason.
func TestAssuranceA37QualityScenarios(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	for _, c := range asrEvQualityCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			asrEvRunQualityCase(t, &c)
		})
	}
}
