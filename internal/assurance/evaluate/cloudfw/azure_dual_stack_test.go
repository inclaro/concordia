package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureIPv4DenyRules are the rules of the IPv4 deny scenarios.
var azureIPv4DenyRules = azureRules(azureDBNSG + " deny-ssh (priority 100)")

// azureIPv4DenyGaps is the gap of an IPv4 deny that does not decide.
const azureIPv4DenyGaps = evaluate.GapAddressDependent + ": " + azureDBNSG + " deny-ssh (priority 100): an IPv4 deny " +
	"decided, and the plan does not show that either endpoint is IPv4-only"

// azureDualStackScenarios are denies through an IPv4 prefix: they decide only when the plan proves that an endpoint
// is IPv4-only. The fixture NICs state IPv4 and the subnets IPv4 prefixes.
func azureDualStackScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "IPv4 deny with two dual-stack NICs",
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
			name: "IPv4 deny for IPv4-only endpoints denies",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			mutate: azureDenySSHByPrefix,
			rules:  azureIPv4DenyRules,
		},
		{
			name: "IPv4 deny that covers one of two IP configurations of the source NIC",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureMutations(azureDenySSHByPrefix, azureSecondIPv4(azureAppNIC)),
			rules:  azureIPv4DenyRules, gaps: evaluate.GapAddressDependent + ": 10.1.1.0/24 covers only part of 10.1.1.0/24, 10.1.9.5/32",
		},
	}
}

// Resources of the IPv4 deny scenarios.
const (
	azureAppNIC    = "azurerm_network_interface.app"
	azureIPVersion = "private_ip_address_version"
)

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

// azureDenySSHByPrefix limits the deny-ssh rule of the database NSG to the IPv4 range of the source subnet.
func azureDenySSHByPrefix(in *cloudfw.Input) {
	azureRuleValues(azureResource(in, azureDBNSG), 0)["source_address_prefix"] = "10.1.1.0/24"
}

// azureDualStackNIC gives the NIC a second, IPv6 IP configuration in the subnet of its first one, with an address
// that is unknown until apply.
func azureDualStackNIC(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		nic := azureResource(in, address)
		configs, _ := nic.Values["ip_configuration"].([]any)
		nic.Values["ip_configuration"] = append(configs, map[string]any{"name": "v6", azureIPVersion: "IPv6"})
		nic.Unknown = append(nic.Unknown, "ip_configuration[1].private_ip_address", "ip_configuration[1].subnet_id")
		nic.References["ip_configuration[1].subnet_id"] = nic.References["ip_configuration[0].subnet_id"]
	}
}

// azureSecondIPv4 gives the NIC a second IPv4 IP configuration with a static address outside its subnet's first
// prefix.
func azureSecondIPv4(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		nic := azureResource(in, address)
		configs, _ := nic.Values["ip_configuration"].([]any)
		nic.Values["ip_configuration"] = append(configs, map[string]any{
			"name": "second", "private_ip_address": "10.1.9.5", azureIPVersion: "IPv4",
		})
	}
}
