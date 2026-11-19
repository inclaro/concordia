package policy

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// DeclaredRule is a rule that a policy pack declares, with the pack. It is declared whether or not the rule is
// disabled, compiled, or kept by the --rule filter.
type DeclaredRule struct {
	RuleID string  `json:"ruleId"`
	Pack   PackRef `json:"pack"`
}

// decisionObligations are the values of `decisions.obligation`: the obligations of the `adr-governance` pack.
// They repeat the obligation names of internal/adr, which policy cannot import.
var decisionObligations = []string{"presence", "applicability", "justification", "enforcement", "history", "scope"}

// decisionsShape compiles the `decisions` key of a rule of the `adr-governance` pack: a rule with no subject and
// no edges, whose results `check` fills from the decision register. An obligation name that is not one of
// decisionObligations gives CO-POL-002.
func (c *compiler) decisionsShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	name := rule.Decisions.Obligation
	if !slices.Contains(decisionObligations, name) {
		d := diag.New(diag.POL002,
			diag.Detail("decisions obligation '"+name+"' is not supported; use "+strings.Join(decisionObligations, ", ")),
			c.at(base+"/decisions/obligation"))

		return shape{}, &d
	}

	return shape{
		predicate: Predicate{Family: FamilyDecisionGovernance, Decisions: name},
		subjects:  []string{},
		targets:   []string{},
	}, nil
}
