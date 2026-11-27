package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureRuleListScenarios are inline rule lists that are unknown until apply.
func azureRuleListScenarios() []azureScenario {
	listGap := evaluate.GapUnknownAfterApply + ": " + azureDBNSG + ".security_rule"

	return []azureScenario{
		{
			name: "inline rule list with a wholly unknown element",
			from: azureVM, to: azureDBNIC, port: 5432, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureUnknownRuleElement, gaps: listGap,
		},
		{
			name: "inline rule list unknown as a whole",
			from: azureVM, to: azureDBNIC, port: 9999, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureUnknownRuleList, gaps: listGap,
		},
	}
}

// azureUnknownRuleElement adds an inline rule that is wholly unknown until apply.
func azureUnknownRuleElement(in *cloudfw.Input) {
	nsg := azureResource(in, azureDBNSG)
	nsg.Unknown = append(nsg.Unknown, "security_rule[7]")
}

// azureUnknownRuleList makes the inline rule list of the database NSG unknown as a whole.
func azureUnknownRuleList(in *cloudfw.Input) {
	nsg := azureResource(in, azureDBNSG)
	delete(nsg.Values, azureRuleList)
	nsg.Unknown = []string{"id", azureRuleList}
}

// azureRuleValues are the values of one inline rule of an NSG.
func azureRuleValues(nsg *plan.Resource, index int) map[string]any {
	rules, _ := nsg.Values[azureRuleList].([]any)
	rule, _ := rules[index].(map[string]any)

	return rule
}
