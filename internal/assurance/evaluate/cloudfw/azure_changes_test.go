package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureSSHRule is a rule resource of the database NSG with the name of its inline deny-ssh rule.
const azureSSHRule = "azurerm_network_security_rule.ssh"

// azureChangedRuleScenarios are rule resources that a plan against existing state deletes or updates, while the
// inline rule list of the NSG still holds their old values.
func azureChangedRuleScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "rule resource changed from Deny to Allow at the priority of its inline copy",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictAllowed,
			mutate: azureUpdatedSSHRule,
			rules:  azureRules(azureVNetOut, azureVNetNICIn, azureSSHRule+" deny-ssh (priority 100)"),
		},
		{
			name: "deleted rule resource whose inline copy denies",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureDeletedRule(nil),
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
			gaps:   evaluate.GapUnknownAfterApply + ": " + azureDBNSG + ".security_rule may repeat a rule that the plan changes",
		},
		{
			name: "deleted rule resource of the NSG before the change",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureDeletedRule(map[string]string{"network_security_group_name": "NSG-DB", "resource_group_name": "rg-pay"}),
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
			gaps:   evaluate.GapUnknownAfterApply + ": " + azureDBNSG + ".security_rule may repeat a rule that the plan changes",
		},
		{
			name: "deleted rule resource of another NSG before the change",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			mutate: azureDeletedRule(map[string]string{"network_security_group_name": "nsg-db", "resource_group_name": "rg-other"}),
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
		},
		{
			name: "deleted rule resource with a partial owner before the change",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureDeletedRule(map[string]string{"network_security_group_name": "nsg-gone"}),
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
			gaps:   evaluate.GapUnknownAfterApply + ": " + azureDBNSG + ".security_rule may repeat a rule that the plan changes",
		},
	}
}

// azureUpdatedSSHRule adds a rule resource of the database NSG that the plan updates to allow SSH, with the name and
// priority of the inline deny-ssh rule, which still holds the old Deny.
func azureUpdatedSSHRule(in *cloudfw.Input) {
	in.Plans[0].Resources = append(in.Plans[0].Resources, plan.Resource{
		Address: azureSSHRule, Mode: "managed", Type: "azurerm_network_security_rule", Name: "ssh", ProviderConfig: "azurerm",
		Actions: []string{"update"}, Values: map[string]any{
			"name": "deny-ssh", "priority": "100", "direction": "Inbound", "access": "Allow", "protocol": "Tcp",
			"source_port_range": "*", "destination_port_range": "22", "source_address_prefix": "*",
			"destination_address_prefix": "*", "network_security_group_name": "nsg-db", "resource_group_name": "rg-pay",
		},
	})
}

// azureDeletedRule adds a rule resource that the plan deletes: it has no planned values, and prior is the owner that
// the plan reader keeps from before the change (nil when the plan does not give it).
func azureDeletedRule(prior map[string]string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		in.Plans[0].Resources = append(in.Plans[0].Resources, plan.Resource{
			Address: "azurerm_network_security_rule.old", Mode: "managed", Type: "azurerm_network_security_rule",
			Name: "old", Actions: []string{"delete"}, Prior: prior,
		})
	}
}
