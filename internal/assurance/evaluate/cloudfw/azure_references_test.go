package cloudfw_test

import (
	"fmt"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
)

// More addresses of the Azure fixture.
const (
	azureAppASG     = "azurerm_application_security_group.app"
	azureAppSubnet  = "azurerm_subnet.app"
	azureDBSubnet   = "azurerm_subnet.db"
	azureDBAssoc    = "azurerm_subnet_network_security_group_association.db"
	azureASGListKey = "security_rule[%d].source_application_security_group_ids"
	azurePolicy     = "private_endpoint_network_policies"
	azureVNetName   = "virtual_network_name"
	azureRuleList   = "security_rule"
)

// azureReferenceScenarios are references that name the wrong resource or several resources, and address sets that
// hold more than one prefix.
func azureReferenceScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "inline ASG references in set order that differs from the configuration",
			from: azureVM, to: azureDBNIC, port: 5432, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureSwappedASGReferences,
			rules:  azureRules(azureDBNSG + " allow-postgres (priority 300)"),
			gaps: evaluate.GapUnresolvedReference + ": " + azureDBNSG + ".security_rule[*].source_application_security_group_ids: " +
				"the blocks name different resources, and the plan does not keep the configuration order of the blocks",
		},
		{
			name: "subnet association whose NSG reference names an ASG",
			from: azureVM, to: azureDBNIC, port: 9999, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureReference(azureAttr{address: azureDBAssoc, path: "network_security_group_id"}, azureAppASG),
			gaps: evaluate.GapUnresolvedReference + ": " + azureDBAssoc + ".network_security_group_id names " + azureAppASG +
				", a azurerm_application_security_group, not one of azurerm_network_security_group",
		},
		{
			name: "NIC whose subnet reference names the virtual network",
			from: azureVM, to: azureDBNIC, port: 9999, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureReference(azureAttr{address: azureDBNIC, path: "ip_configuration[0].subnet_id"}, azureVNet),
			gaps: evaluate.GapUnresolvedReference + ": " + azureDBNIC + ".ip_configuration[0].subnet_id names " + azureVNet +
				", a azurerm_virtual_network, not one of azurerm_subnet",
		},
		{
			name: "source subnet with two address prefixes",
			from: azureVM, to: azureDBNIC, port: 6379, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureTwoPrefixes,
			rules:  azureRules(azureDBNSG + " allow-redis (priority 310)"),
			gaps:   evaluate.GapAddressDependent + ": 10.1.1.0/24 covers only part of 10.1.1.0/24, 10.1.3.0/24",
		},
		{
			name: "VirtualNetwork through a reference to a network with two instances",
			from: azureVM, to: azureDBNIC, port: 5432, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureTwoVNetInstances,
			rules:  azureRules(azureVNetOut, azureVNetNICIn),
			gaps: azureRules(
				evaluate.GapPeeringNotEvaluated+": VirtualNetwork: "+azureVM+" and "+azureDBNIC+" are not in one virtual network of the plan",
				evaluate.GapUnresolvedReference+": "+azureVNet+" has 2 instances",
			),
		},
		{
			name: "subnets of two provider configurations", from: azureVM, to: azureDBNIC, port: 8443,
			want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: func(in *cloudfw.Input) { azureResource(in, azureDBSubnet).ProviderConfig = "azurerm.other" },
			rules:  azureRules(azureVNetOut, azureVNetNICIn),
			gaps: azureRules(
				evaluate.GapPeeringNotEvaluated + ": VirtualNetwork: " + azureVM + " and " + azureDBNIC + " are not in one virtual " +
					"network of the plan",
			),
		},
	}
}

// azureAttr is one attribute of a resource of the fixture.
type azureAttr struct {
	address, path string
}

// azureCopy names a resource of the fixture and the address of a copy of it.
type azureCopy struct {
	from, to string
}

// azureReference replaces the configuration references of one attribute of a resource.
func azureReference(at azureAttr, target string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		azureResource(in, at.address).References[at.path] = []string{target}
	}
}

// azureUnknownSubnetValue makes an attribute of a subnet unknown until apply.
func azureUnknownSubnetValue(at azureAttr) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		subnet := azureResource(in, at.address)
		delete(subnet.Values, at.path)
		subnet.Unknown = append(subnet.Unknown, at.path)
	}
}

// azureSwappedASGReferences gives the allow-ssh rule an unknown ASG list too, and keys the references of the two
// rules in the other order: the configuration lists the blocks in an order that the plan does not keep.
func azureSwappedASGReferences(in *cloudfw.Input) {
	azureAddCopy(in, azureCopy{from: azureAppASG, to: "azurerm_application_security_group.bastion"})

	nsg := azureResource(in, azureDBNSG)
	allowSSH := azureRuleValues(nsg, 1)
	allowSSH["source_address_prefix"] = ""
	delete(allowSSH, "source_application_security_group_ids")

	nsg.Unknown = append(nsg.Unknown, azureListKey(1))
	nsg.References[azureListKey(1)] = []string{azureAppASG}
	nsg.References[azureListKey(2)] = []string{"azurerm_application_security_group.bastion"}
}

func azureListKey(index int) string {
	return fmt.Sprintf(azureASGListKey, index)
}

// azureTwoPrefixes gives the source subnet a second address prefix.
func azureTwoPrefixes(in *cloudfw.Input) {
	azureResource(in, azureAppSubnet).Values["address_prefixes"] = []any{"10.1.1.0/24", "10.1.3.0/24"}
}

// azureTwoVNetInstances makes the network names of both subnets unknown, so only their references name the
// network, and gives the network two instances.
func azureTwoVNetInstances(in *cloudfw.Input) {
	for _, address := range []string{azureAppSubnet, azureDBSubnet} {
		azureUnknownSubnetValue(azureAttr{address: address, path: azureVNetName})(in)
	}

	azureAddCopy(in, azureCopy{from: azureVNet, to: azureVNet + "[1]"})
	azureResource(in, azureVNet).Address = azureVNet + "[0]"
}

// azureAddCopy adds a copy of a resource at another address.
func azureAddCopy(in *cloudfw.Input, copyOf azureCopy) {
	res := *azureResource(in, copyOf.from)
	res.Address = copyOf.to
	in.Plans[0].Resources = append(in.Plans[0].Resources, res)
}
