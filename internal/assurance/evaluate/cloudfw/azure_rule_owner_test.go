package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
)

// azureHTTPSRule is the standalone rule resource of the database NSG that the NSG name finds.
const azureHTTPSRule = "azurerm_network_security_rule.https"

// azureOwnerThroughVariable makes the configuration name the NSG of the https rule through a variable, as
// `network_security_group_name = var.nsg_name` does; the plan still knows the name.
func azureOwnerThroughVariable(in *cloudfw.Input) {
	azureResource(in, azureHTTPSRule).References = map[string][]string{"network_security_group_name": {"var.nsg_name"}}
}

// azureRuleOwnerScenarios are standalone rules whose owner NSG comes from the configuration references.
func azureRuleOwnerScenarios() []azureScenario {
	https := azureRules(azureVNetOut, azureVNetNICIn, azureHTTPSRule+" allow-https (priority 340)")

	return []azureScenario{
		{
			name: "rule owner by reference in the resource group of the NSG", from: azureVM, to: azureDBNIC, port: 8443,
			want: cloudfw.VerdictAllowed, mutate: azureOwnerByReferenceOnly(azureOwnerRef{"rg-pay", []string{azureDBNSG}}),
			rules: https,
		},
		{
			name: "rule owner by reference in another resource group", from: azureVM, to: azureDBNIC,
			port: 8443, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureOwnerByReferenceOnly(azureOwnerRef{"rg-other", []string{azureDBNSG}}),
			rules:  azureRules(azureHTTPSRule + " allow-https (priority 340)"),
			gaps: evaluate.GapUnresolvedReference + ": " + azureHTTPSRule + ".network_security_group_name: the rule is in " +
				"resource group rg-other and " + azureDBNSG + " is in rg-pay",
		},
		{
			name: "rule owner by a reference that names two NSGs", from: azureVM, to: azureDBNIC, port: 8443,
			want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureOwnerByReferenceOnly(azureOwnerRef{"rg-pay", []string{azureDBNSG, azureAppNSG}}),
			rules:  azureRules(azureHTTPSRule + " allow-https (priority 340)"),
			gaps: evaluate.GapUnresolvedReference + ": " + azureHTTPSRule + ".network_security_group_name: the " +
				"configuration names 2 network security groups",
		},
		{
			name: "standalone rule whose known NSG name comes through a variable allows",
			from: azureVM, to: azureDBNIC, port: 8443, want: cloudfw.VerdictAllowed,
			mutate: azureOwnerThroughVariable,
			rules:  azureRules(azureVNetOut, azureVNetNICIn, azureHTTPSRule+" allow-https (priority 340)"),
		},
	}
}

// azureOwnerRef is the resource group of the https rule and the NSGs that its configuration names as its owner.
type azureOwnerRef struct {
	group string
	nsgs  []string
}

// azureOwnerByReferenceOnly makes the NSG name of the https rule unknown until apply, so that only its
// configuration references name its owner, and puts the rule in the resource group.
func azureOwnerByReferenceOnly(ref azureOwnerRef) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		rule := azureResource(in, azureHTTPSRule)
		delete(rule.Values, "network_security_group_name")
		rule.Values["resource_group_name"] = ref.group
		rule.Unknown = append(rule.Unknown, "network_security_group_name")
		rule.References = map[string][]string{"network_security_group_name": ref.nsgs}
	}
}
