package cloudfw_test

import (
	"maps"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
)

// azurePolicyScenarios are private endpoints whose subnet does or does not apply its NSG to them.
func azurePolicyScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "subnet disables network policies for private endpoints",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictAllowed,
			mutate: azureSubnetValue(map[string]any{azurePolicy: "Disabled"}), rules: azureRules(azureVNetOut),
		},
		{
			name: "azurerm 3.x boolean private_endpoint_network_policies_enabled false",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictAllowed,
			mutate: azureSubnetValue(map[string]any{"private_endpoint_network_policies_enabled": false}),
			rules:  azureRules(azureVNetOut),
		},
		{
			name: "older enforce_private_link_endpoint_network_policies true disables them",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictAllowed,
			mutate: azureSubnetValue(map[string]any{"enforce_private_link_endpoint_network_policies": true}),
			rules:  azureRules(azureVNetOut),
		},
		{
			name: "subnet enables network policies for NSGs only",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictDenied,
			mutate: azureSubnetValue(map[string]any{azurePolicy: "NetworkSecurityGroupEnabled"}),
			rules:  azureRules(azureDBNSG + " deny-all-in (priority 4096)"),
		},
		{
			name: "subnet policy not stated in the plan",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureSubnetValue(map[string]any{}),
			gaps: evaluate.GapUnsupportedKind + ": " + azureDBSubnet + ": the plan does not state " + azurePolicy +
				", so whether the network security group of the subnet applies to its private endpoints is not known",
		},
		{
			name: "subnet policy unknown until apply",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureUnknownSubnetValue(azureAttr{address: azureDBSubnet, path: azurePolicy}),
			gaps:   evaluate.GapUnknownAfterApply + ": " + azureDBSubnet + "." + azurePolicy,
		},
		{
			name: "private endpoint subnet without network policies and an unresolved NSG association",
			from: azureVM, to: azureDBPE, port: 9999, want: cloudfw.VerdictAllowed, rules: azureRules(azureVNetOut),
			mutate: azureMutations(azureSubnetValue(map[string]any{"private_endpoint_network_policies": "Disabled"}),
				azureUnresolvedNSGAssociation),
		},
	}
}

// azureSubnetValue replaces the private endpoint policy of the database subnet with the given attributes.
func azureSubnetValue(values map[string]any) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		subnet := azureResource(in, azureDBSubnet)
		delete(subnet.Values, azurePolicy)
		maps.Copy(subnet.Values, values)
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
