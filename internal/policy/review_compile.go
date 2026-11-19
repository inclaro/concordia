package policy

import "github.com/inclaro/concordia/internal/diag"

// assessmentCurrencyShape compiles the `assessmentCurrency` key of a rule (approval, disposition, capability, or governance; the policy schema lists
// the values) of the `review-currency` pack: a rule with
// no subject and no edges, whose results `check` fills from the assessments of the workspace.
func (c *compiler) assessmentCurrencyShape(rule *rawRule, _ string) (shape, *diag.Diagnostic) {
	return shape{
		predicate: Predicate{Family: FamilyAssessmentCurrency, AssessmentCurrency: *rule.AssessmentCurrency},
		subjects:  []string{},
		targets:   []string{},
	}, nil
}
