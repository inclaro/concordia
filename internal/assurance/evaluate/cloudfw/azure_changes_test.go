package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureSSHRule is a rule resource of the database NSG with the name of its inline deny-ssh rule.
const azureSSHRule = "azurerm_network_security_rule.ssh"

// azureChangedRuleScenarios are rule resources that a plan against existing state deletes or updates, while the
// inline rule list of the NSG still holds their old values (review 2, finding B).
func azureChangedRuleScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "finding B: rule resource changed from Deny to Allow at the priority of its inline copy",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictAllowed,
			mutate: azureUpdatedSSHRule,
			rules:  azureRules(azureVNetOut, azureVNetNICIn, azureSSHRule+" deny-ssh (priority 100)"),
		},
		{
			name: "finding B: deleted rule resource whose inline copy denies",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureDeletedRule,
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
			gaps:   evaluate.GapUnknownAfterApply + ": " + azureDBNSG + ".security_rule may repeat a rule that the plan changes",
		},
	}
}

// azureDualStackScenarios are denies through an IPv4 prefix (review 2, finding G).
func azureDualStackScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "finding G: IPv4 deny with a dual-stack destination NIC",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: func(in *cloudfw.Input) { azureDenySSHByPrefix(in); azureDualStackDBNIC(in) },
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
			gaps: evaluate.GapAddressDependent + ": " + azureDBNSG + " deny-ssh (priority 100): an IPv4 deny decided, and " +
				"the endpoints may be dual-stack",
		},
		{
			name: "finding G: IPv4 deny without dual-stack evidence still denies",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			mutate: azureDenySSHByPrefix,
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
		},
	}
}

// azureUpdatedSSHRule adds a rule resource of the database NSG that the plan updates to allow SSH, with the name and
// priority of the inline deny-ssh rule, which still holds the old Deny.
func azureUpdatedSSHRule(in *cloudfw.Input) {
	in.Resources = append(in.Resources, plan.Resource{
		Address: azureSSHRule, Mode: "managed", Type: "azurerm_network_security_rule", Name: "ssh",
		Actions: []string{"update"}, Values: map[string]any{
			"name": "deny-ssh", "priority": "100", "direction": "Inbound", "access": "Allow", "protocol": "Tcp",
			"source_port_range": "*", "destination_port_range": "22", "source_address_prefix": "*",
			"destination_address_prefix": "*", "network_security_group_name": "nsg-db", "resource_group_name": "rg-pay",
		},
	})
}

// azureDeletedRule adds a rule resource that the plan deletes: it has no planned values, so its NSG is not known.
func azureDeletedRule(in *cloudfw.Input) {
	in.Resources = append(in.Resources, plan.Resource{
		Address: "azurerm_network_security_rule.old", Mode: "managed", Type: "azurerm_network_security_rule",
		Name: "old", Actions: []string{"delete"},
	})
}

// azureDenySSHByPrefix limits the deny-ssh rule of the database NSG to the IPv4 range of the source subnet.
func azureDenySSHByPrefix(in *cloudfw.Input) {
	azureRuleValues(azureResource(in, azureDBNSG), 0)["source_address_prefix"] = "10.1.1.0/24"
}

// azureDualStackDBNIC gives the database NIC a second, IPv6 IP configuration.
func azureDualStackDBNIC(in *cloudfw.Input) {
	nic := azureResource(in, azureDBNIC)
	configs, _ := nic.Values["ip_configuration"].([]any)
	nic.Values["ip_configuration"] = append(configs, map[string]any{"name": "v6", "private_ip_address_version": "IPv6"})
}
