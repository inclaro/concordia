package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureEndpoint is one end of a query: its NIC and subnet (either can be nil), its address set, and the application
// security groups of its NIC.
type azureEndpoint struct {
	res    *plan.Resource
	nic    *plan.Resource
	subnet *plan.Resource
	ip     string
	addrs  addrSet
	asgs   []*plan.Resource
	// asgGaps say why the ASG membership of the endpoint is not fully known.
	asgGaps []assurance.Gap
	// policyGaps say why subnetNSG is maybe.
	policyGaps []assurance.Gap
	// subnetNSG is yes when the NSG of the subnet applies to inbound traffic of the endpoint, no when it does not
	// (a private endpoint whose subnet disables network policies), and maybe when the plan does not say.
	subnetNSG tri
}

// azureEndpointKinds reads the NIC and subnet of each supported endpoint type.
var azureEndpointKinds = map[string]func(e *Evaluator, end *azureEndpoint, report *side) bool{
	azureNIC:             (*Evaluator).azureNICEndpoint,
	azureLinuxVM:         (*Evaluator).azureVMEndpoint,
	azureWindowsVM:       (*Evaluator).azureVMEndpoint,
	azureSubnet:          (*Evaluator).azureSubnetEndpoint,
	azurePrivateEndpoint: (*Evaluator).azurePrivateEndpoint,
}

// azureEndpointOf resolves an endpoint. ok is false, with the gaps in report, when the endpoint cannot be evaluated.
func (e *Evaluator) azureEndpointOf(r *plan.Resource, report *side) (*azureEndpoint, bool) {
	read, supported := azureEndpointKinds[r.Type]
	if !supported {
		report.gaps = append(report.gaps, unsupportedResource(r))

		return nil, false
	}

	end := &azureEndpoint{res: r, subnetNSG: triYes}
	if !read(e, end, report) {
		return nil, false
	}

	end.addrs = azureAddrs(end)
	e.azureMembership(end)

	return end, true
}

func (e *Evaluator) azureNICEndpoint(end *azureEndpoint, report *side) bool {
	return e.azureWithNIC(end, end.res, report)
}

// azureVMEndpoint reads a virtual machine through its only NIC. A machine with more than one NIC is not supported.
func (e *Evaluator) azureVMEndpoint(end *azureEndpoint, report *side) bool {
	nics, ok := e.refsOfType(end.res, "network_interface_ids", report, azureNIC)

	switch {
	case !ok:
		return false
	case len(nics) > 1:
		report.gap(evaluate.GapUnsupportedResource, fmt.Sprintf("%s: a virtual machine with %d network interfaces is not supported",
			end.res.Address, len(nics)))

		return false
	case len(nics) == 0:
		report.gap(evaluate.GapUnresolvedReference, end.res.Address+" has no network interface in the plan")

		return false
	}

	return e.azureWithNIC(end, nics[0], report)
}

func (e *Evaluator) azureSubnetEndpoint(end *azureEndpoint, _ *side) bool {
	end.subnet = end.res

	return true
}

// azurePrivateEndpoint reads the subnet of a private endpoint, and whether its NSG applies to the endpoint.
func (e *Evaluator) azurePrivateEndpoint(end *azureEndpoint, report *side) bool {
	subnet, ok := e.refOfType(end.res, azureSubnetIDAttr, report, azureSubnet)
	end.subnet = subnet

	if subnet != nil {
		policy := &side{}
		end.subnetNSG = azureEndpointPolicies(subnet, policy)
		end.policyGaps = policy.gaps
	}

	return ok
}

// azureWithNIC reads the subnet and the private IP of the NIC of the endpoint.
func (e *Evaluator) azureWithNIC(end *azureEndpoint, nic *plan.Resource, report *side) bool {
	subnet, ok := e.refOfType(nic, azureNICSubnetAttr, report, azureSubnet)
	end.nic, end.subnet = nic, subnet
	end.ip, _ = attrString(nic, azureNICPrivateIPAttr)

	return ok
}

// azureAddrs is the address set of the endpoint: its private IP, else the prefix of its subnet. A subnet with more
// than one prefix gives an unknown set, because the endpoint can have an address in any of them.
func azureAddrs(end *azureEndpoint) addrSet {
	if end.ip != "" {
		return hostSet(end.ip)
	}

	if end.subnet == nil {
		return unknownAddrs
	}

	if prefixes := attrStrings(end.subnet, "address_prefixes"); len(prefixes) == 1 {
		return prefixSet(prefixes[0])
	}

	return unknownAddrs
}

// azureMembership reads the application security groups of the NIC of the endpoint.
func (e *Evaluator) azureMembership(end *azureEndpoint) {
	if end.nic == nil {
		end.asgGaps = []assurance.Gap{azureNoInterface(end)}

		return
	}

	scratch := &side{}
	for _, assoc := range e.ofType(azureNICASGAssoc) {
		e.azureAddMember(end, assoc, scratch)
	}

	end.asgGaps = scratch.gaps
}

// azureAddMember adds the ASG of one association when it binds the NIC of the endpoint. An association that does
// not resolve leaves a gap in report.
func (e *Evaluator) azureAddMember(end *azureEndpoint, assoc *plan.Resource, report *side) {
	nic, ok := e.refOfType(assoc, azureNICIDAttr, report, azureNIC)
	if !ok || nic != end.nic {
		return
	}

	if asg, ok := e.refOfType(assoc, azureASGIDAttr, report, azureASG); ok && asg != nil {
		end.asgs = appendUnique(end.asgs, asg)
	}
}

// azureInGroups is yes when the endpoint is a member of one of the ASGs, maybe when its membership is not fully
// known, else no.
func azureInGroups(end *azureEndpoint, asgs []*plan.Resource, report *side) tri {
	if slices.ContainsFunc(asgs, func(a *plan.Resource) bool { return slices.Contains(end.asgs, a) }) {
		return triYes
	}

	if len(end.asgGaps) > 0 {
		report.gaps = append(report.gaps, end.asgGaps...)

		return triMaybe
	}

	return triNo
}

// azureNoInterface is the gap of an ASG rule against an endpoint without a NIC: its members are not known.
func azureNoInterface(end *azureEndpoint) assurance.Gap {
	return assurance.Gap{
		Kind:   evaluate.GapUnsupportedKind,
		Detail: fmt.Sprintf("application security group against %s, which has no network interface", end.res.Address),
	}
}

// azureIsAddress reports whether a prefix value is meant as an address (a CIDR or an IP), not a service tag.
func azureIsAddress(value string) bool {
	return strings.ContainsAny(value, "/:") || (value != "" && value[0] >= '0' && value[0] <= '9')
}
