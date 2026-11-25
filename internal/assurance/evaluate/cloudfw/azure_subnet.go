package cloudfw

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Subnet attributes that say whether network policies (NSGs) apply to the private endpoints of the subnet: the
// string of azurerm 3.98 and later, the boolean that it replaced, and the older inverted boolean.
const (
	azurePolicyAttr        = "private_endpoint_network_policies"
	azurePolicyEnabledAttr = "private_endpoint_network_policies_enabled"
	azurePolicyEnforceAttr = "enforce_private_link_endpoint_network_policies"
)

// azurePolicyValues are the values of private_endpoint_network_policies: yes when NSGs apply to private endpoints.
var azurePolicyValues = map[string]tri{
	"enabled":                     triYes,
	"networksecuritygroupenabled": triYes,
	"disabled":                    triNo,
	"routetableenabled":           triNo,
}

// azurePolicyReaders read the policy attributes in order; the first that the plan states decides.
var azurePolicyReaders = []func(subnet *plan.Resource, report *side) (tri, bool){
	azurePolicyText, azurePolicyEnabled, azurePolicyEnforced,
}

// azureEndpointPolicies is yes when the NSG of the subnet applies to its private endpoints, no when the subnet
// disables network policies for them, and maybe when the plan does not say.
func azureEndpointPolicies(subnet *plan.Resource, report *side) tri {
	for _, read := range azurePolicyReaders {
		if applies, stated := read(subnet, report); stated {
			return applies
		}
	}

	report.gap(evaluate.GapUnsupportedKind, subnet.Address+": the plan does not state "+azurePolicyAttr+
		", so whether the network security group of the subnet applies to its private endpoints is not known")

	return triMaybe
}

// azurePolicyUnknown reports, with a gap, whether the attribute is unknown until apply.
func azurePolicyUnknown(subnet *plan.Resource, attribute string, report *side) bool {
	if !unknownAt(subnet, attribute) {
		return false
	}

	report.gap(evaluate.GapUnknownAfterApply, subnet.Address+"."+attribute)

	return true
}

func azurePolicyText(subnet *plan.Resource, report *side) (tri, bool) {
	if azurePolicyUnknown(subnet, azurePolicyAttr, report) {
		return triMaybe, true
	}

	value, stated := attrString(subnet, azurePolicyAttr)
	if !stated {
		return triMaybe, false
	}

	if applies, known := azurePolicyValues[strings.ToLower(value)]; known {
		return applies, true
	}

	report.gap(evaluate.GapUnsupportedKind, fmt.Sprintf("%s: %s %q", subnet.Address, azurePolicyAttr, value))

	return triMaybe, true
}

func azurePolicyEnabled(subnet *plan.Resource, report *side) (tri, bool) {
	return azurePolicyBool(subnet, azurePolicyEnabledAttr, report, true)
}

// azurePolicyEnforced reads the older attribute, where true disables the network policies.
func azurePolicyEnforced(subnet *plan.Resource, report *side) (tri, bool) {
	return azurePolicyBool(subnet, azurePolicyEnforceAttr, report, false)
}

// azurePolicyBool reads a boolean policy attribute: the policies apply when its value is appliesWhen.
func azurePolicyBool(subnet *plan.Resource, attribute string, report *side, appliesWhen bool) (tri, bool) {
	if azurePolicyUnknown(subnet, attribute, report) {
		return triMaybe, true
	}

	flag, stated := attr(subnet, attribute).(bool)
	if !stated {
		return triMaybe, false
	}

	return azureIf(flag == appliesWhen), true
}

// azureVNet identifies the virtual network of the subnet of an endpoint: the plan and the provider configuration of
// the subnet, which fix its subscription, and the resource group and name of the network, or the network's address
// when only the configuration reference names it. It is "" when the network is not known, or when the plan does not
// give the provider configuration of the subnet: two networks with one name can be in two subscriptions. A
// reference that does not resolve to one virtual network leaves a gap in report.
func (e *Evaluator) azureVNet(end *azureEndpoint, report *side) string {
	if end.subnet == nil || end.subnet.ProviderConfig == "" {
		return ""
	}

	network := e.azureVNetOf(end.subnet, report)
	if network == "" {
		return ""
	}

	return fmt.Sprintf("plan %d, provider %s: %s", e.planOf[end.subnet], end.subnet.ProviderConfig, network)
}

// azureVNetOf identifies the virtual network of the subnet by its resource group and name, or by its address when
// only the configuration reference names it, or is "".
func (e *Evaluator) azureVNetOf(subnet *plan.Resource, report *side) string {
	if key, ok := azureNameKey(subnet, "virtual_network_name"); ok {
		return key
	}

	refs := subnet.References["virtual_network_name"]
	if len(refs) == 0 {
		return ""
	}

	found, ok := e.resolveAll(subnet, refs, report)
	if !ok || len(found) != 1 {
		return ""
	}

	return azureVNetKey(found[0], subnet, report)
}

// azureVNetKey is the key of the virtual network that a subnet references: its resource group and name, else its
// address.
func azureVNetKey(vnet, subnet *plan.Resource, report *side) string {
	if vnet.Type != azureVNetType {
		report.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s.virtual_network_name names %s, a %s, not a %s",
			subnet.Address, vnet.Address, vnet.Type, azureVNetType))

		return ""
	}

	if key, ok := azureNameKey(vnet, "name"); ok {
		return key
	}

	return vnet.Address
}

// azureNameKey is `<resource group>/<name>` when both are known.
func azureNameKey(r *plan.Resource, nameAttr string) (string, bool) {
	name, hasName := attrString(r, nameAttr)
	group, hasGroup := attrString(r, "resource_group_name")

	return group + "/" + name, hasName && hasGroup
}

// azureSameVNet is yes when both endpoints are in subnets of one virtual network, else maybe: traffic between
// networks depends on peering, which is not evaluated.
func (e *Evaluator) azureSameVNet(pair *azurePair, report *side) tri {
	if from := e.azureVNet(pair.from, report); from != "" && from == e.azureVNet(pair.to, report) {
		return triYes
	}

	report.gap(evaluate.GapPeeringNotEvaluated, fmt.Sprintf("VirtualNetwork: %s and %s are not in one virtual network of the plan",
		pair.from.res.Address, pair.to.res.Address))

	return triMaybe
}
