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
		{
			name: "standalone rule whose known NSG name comes through a variable allows",
			from: azureVM, to: azureDBNIC, port: 8443, want: cloudfw.VerdictAllowed,
			mutate: azureOwnerThroughVariable,
			rules:  azureRules(azureVNetOut, azureVNetNICIn, azureHTTPSRule+" allow-https (priority 340)"),
		},
	}
}

// azureReviewRound1Scenarios are review round 1 findings 7, 8, 27, and 28.
func azureReviewRound1Scenarios() []azureScenario {
	https := azureRules(azureVNetOut, azureVNetNICIn, azureHTTPSRule+" allow-https (priority 340)")

	return []azureScenario{
		{
			name: "rule owner by reference in the resource group of the NSG", from: azureVM, to: azureDBNIC, port: 8443,
			want: cloudfw.VerdictAllowed, mutate: azureOwnerByReferenceOnly(azureOwnerRef{"rg-pay", []string{azureDBNSG}}),
			rules: https,
		},
		{
			name: "finding 7: rule owner by reference in another resource group", from: azureVM, to: azureDBNIC,
			port: 8443, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureOwnerByReferenceOnly(azureOwnerRef{"rg-other", []string{azureDBNSG}}),
			rules:  azureRules(azureHTTPSRule + " allow-https (priority 340)"),
			gaps: evaluate.GapUnresolvedReference + ": " + azureHTTPSRule + ".network_security_group_name: the rule is in " +
				"resource group rg-other and " + azureDBNSG + " is in rg-pay",
		},
		{
			name: "finding 28: rule owner by a reference that names two NSGs", from: azureVM, to: azureDBNIC, port: 8443,
			want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureOwnerByReferenceOnly(azureOwnerRef{"rg-pay", []string{azureDBNSG, azureAppNSG}}),
			rules:  azureRules(azureHTTPSRule + " allow-https (priority 340)"),
			gaps: evaluate.GapUnresolvedReference + ": " + azureHTTPSRule + ".network_security_group_name: the " +
				"configuration names 2 network security groups",
		},
		{
			name: "finding 8: subnets of two provider configurations", from: azureVM, to: azureDBNIC, port: 8443,
			want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: func(in *cloudfw.Input) { azureResource(in, azureDBSubnet).ProviderConfig = "azurerm.other" },
			rules:  azureRules(azureVNetOut, azureVNetNICIn),
			gaps: azureRules(
				evaluate.GapPeeringNotEvaluated + ": VirtualNetwork: " + azureVM + " and " + azureDBNIC + " are not in one virtual " +
					"network of the plan",
			),
		},
		{
			name: "finding 27: private endpoint subnet without network policies and an unresolved NSG association",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictAllowed, rules: azureRules(azureVNetOut),
			mutate: azureMutations(azureSubnetValue(map[string]any{"private_endpoint_network_policies": "Disabled"}),
				azureUnresolvedNSGAssociation),
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

// azureUnresolvedNSGAssociation makes the NSG of the NSG association of the database subnet unknown until apply,
// without a configuration reference.
func azureUnresolvedNSGAssociation(in *cloudfw.Input) {
	assoc := azureResource(in, azureDBAssoc)
	delete(assoc.Values, "network_security_group_id")
	delete(assoc.References, "network_security_group_id")
	assoc.Unknown = append(assoc.Unknown, "network_security_group_id")
}

// azureHTTPSRule is the standalone rule resource of the database NSG that the NSG name finds.
const azureHTTPSRule = "azurerm_network_security_rule.https"

// azureOwnerThroughVariable makes the configuration name the NSG of the https rule through a variable, as
// `network_security_group_name = var.nsg_name` does; the plan still knows the name.
func azureOwnerThroughVariable(in *cloudfw.Input) {
	azureResource(in, azureHTTPSRule).References = map[string][]string{"network_security_group_name": {"var.nsg_name"}}
}

// azureIPv4DenyRules are the rules of the IPv4 deny scenarios.
var azureIPv4DenyRules = azureRules(azureDBNSG + " deny-ssh (priority 100)")

// azureIPv4DenyGaps is the gap of an IPv4 deny that does not decide.
const azureIPv4DenyGaps = evaluate.GapAddressDependent + ": " + azureDBNSG + " deny-ssh (priority 100): an IPv4 deny " +
	"decided, and the plan does not show that either endpoint is IPv4-only"

// azureDualStackScenarios are denies through an IPv4 prefix: they decide only when the plan proves that an endpoint
// is IPv4-only (review 2, finding G; review 4, step 4). The fixture NICs state IPv4 and the subnets IPv4 prefixes.
func azureDualStackScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "finding G: IPv4 deny with two dual-stack NICs",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureMutations(azureDenySSHByPrefix, azureDualStackNIC(azureAppNIC), azureDualStackNIC(azureDBNIC)),
			rules:  azureIPv4DenyRules, gaps: azureIPv4DenyGaps,
		},
		{
			name: "IPv4 deny with a dual-stack destination NIC and an IPv4-only source",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			mutate: azureMutations(azureDenySSHByPrefix, azureDualStackNIC(azureDBNIC)),
			rules:  azureIPv4DenyRules,
		},
		{
			name: "IPv4 deny with IP versions unknown until apply",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureMutations(azureDenySSHByPrefix, azureUnknownVersion(azureAppNIC), azureUnknownVersion(azureDBNIC)),
			rules:  azureIPv4DenyRules, gaps: azureIPv4DenyGaps,
		},
		{
			name: "IPv4 deny with an IPv6 prefix in the destination subnet and a dual-stack source NIC",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureMutations(azureDenySSHByPrefix, azureDualStackNIC(azureAppNIC), azureIPv6Prefix(azureDBSubnet)),
			rules:  azureIPv4DenyRules, gaps: azureIPv4DenyGaps,
		},
		{
			name: "IPv4 deny with absent IP versions, which default to IPv4",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			mutate: azureMutations(azureDenySSHByPrefix, azureAbsentVersion(azureAppNIC), azureAbsentVersion(azureDBNIC)),
			rules:  azureIPv4DenyRules,
		},
		{
			name: "finding G: IPv4 deny for IPv4-only endpoints denies",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			mutate: azureDenySSHByPrefix,
			rules:  azureIPv4DenyRules,
		},
	}
}

// Resources of the IPv4 deny scenarios.
const (
	azureAppNIC    = "azurerm_network_interface.app"
	azureIPVersion = "private_ip_address_version"
)

// azureMutations applies the mutations in order.
func azureMutations(mutations ...func(in *cloudfw.Input)) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		for _, mutate := range mutations {
			mutate(in)
		}
	}
}

// azureUnknownVersion makes the IP version of the first IP configuration of the NIC unknown until apply.
func azureUnknownVersion(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		nic := azureResource(in, address)
		delete(azureFirstConfiguration(nic), azureIPVersion)
		nic.Unknown = append(nic.Unknown, "ip_configuration[0]."+azureIPVersion)
	}
}

// azureAbsentVersion removes the IP version of the first IP configuration of the NIC.
func azureAbsentVersion(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) { delete(azureFirstConfiguration(azureResource(in, address)), azureIPVersion) }
}

// azureFirstConfiguration is the first IP configuration of the NIC.
func azureFirstConfiguration(nic *plan.Resource) map[string]any {
	configs, _ := nic.Values["ip_configuration"].([]any)
	config, _ := configs[0].(map[string]any)

	return config
}

// azureIPv6Prefix adds an IPv6 prefix to the address prefixes of the subnet.
func azureIPv6Prefix(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		subnet := azureResource(in, address)
		prefixes, _ := subnet.Values["address_prefixes"].([]any)
		subnet.Values["address_prefixes"] = append(prefixes, "fd00:1::/64")
	}
}

// azureUpdatedSSHRule adds a rule resource of the database NSG that the plan updates to allow SSH, with the name and
// priority of the inline deny-ssh rule, which still holds the old Deny.
func azureUpdatedSSHRule(in *cloudfw.Input) {
	in.Plans[0].Resources = append(in.Plans[0].Resources, plan.Resource{
		Address: azureSSHRule, Mode: "managed", Type: "azurerm_network_security_rule", Name: "ssh",
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

// azureDenySSHByPrefix limits the deny-ssh rule of the database NSG to the IPv4 range of the source subnet.
func azureDenySSHByPrefix(in *cloudfw.Input) {
	azureRuleValues(azureResource(in, azureDBNSG), 0)["source_address_prefix"] = "10.1.1.0/24"
}

// azureDualStackNIC gives the NIC a second, IPv6 IP configuration.
func azureDualStackNIC(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		nic := azureResource(in, address)
		configs, _ := nic.Values["ip_configuration"].([]any)
		nic.Values["ip_configuration"] = append(configs, map[string]any{"name": "v6", azureIPVersion: "IPv6"})
	}
}
