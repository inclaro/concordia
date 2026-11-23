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

	end := &azureEndpoint{res: r}
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
	nics, ok := e.refs(end.res, "network_interface_ids", report)

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

func (e *Evaluator) azurePrivateEndpoint(end *azureEndpoint, report *side) bool {
	subnet, ok := e.ref(end.res, azureSubnetIDAttr, report)
	end.subnet = subnet

	return ok
}

// azureWithNIC reads the subnet and the private IP of the NIC of the endpoint.
func (e *Evaluator) azureWithNIC(end *azureEndpoint, nic *plan.Resource, report *side) bool {
	if nic.Type != azureNIC {
		report.gaps = append(report.gaps, unsupportedResource(nic))

		return false
	}

	subnet, ok := e.ref(nic, azureNICSubnetAttr, report)
	end.nic, end.subnet = nic, subnet
	end.ip, _ = attrString(nic, azureNICPrivateIPAttr)

	return ok
}

// azureAddrs is the address set of the endpoint: its private IP, else the first prefix of its subnet.
func azureAddrs(end *azureEndpoint) addrSet {
	if end.ip != "" {
		return hostSet(end.ip)
	}

	if end.subnet == nil {
		return unknownAddrs
	}

	if prefixes := attrStrings(end.subnet, "address_prefixes"); len(prefixes) > 0 {
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
	nic, ok := e.ref(assoc, azureNICIDAttr, report)
	if !ok || nic != end.nic {
		return
	}

	if asg, ok := e.ref(assoc, azureASGIDAttr, report); ok && asg != nil {
		end.asgs = azureAppendUnique(end.asgs, asg)
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

// azureVNet identifies the virtual network of the subnet of an endpoint: the known name and resource group, or the
// configuration reference. It is "" when neither is known.
func azureVNet(end *azureEndpoint) string {
	if end.subnet == nil {
		return ""
	}

	name, hasName := attrString(end.subnet, "virtual_network_name")
	group, hasGroup := attrString(end.subnet, "resource_group_name")

	if hasName && hasGroup {
		return "name:" + group + "/" + name
	}

	if refs := end.subnet.References["virtual_network_name"]; len(refs) == 1 {
		return "ref:" + refs[0]
	}

	return ""
}

// azureSameVNet is yes when both endpoints are in subnets of one virtual network, else maybe: traffic between
// networks depends on peering, which is not evaluated.
func azureSameVNet(pair *azurePair, report *side) tri {
	if from := azureVNet(pair.from); from != "" && from == azureVNet(pair.to) {
		return triYes
	}

	report.gap(evaluate.GapPeeringNotEvaluated, fmt.Sprintf("VirtualNetwork: %s and %s are not in one virtual network of the plan",
		pair.from.res.Address, pair.to.res.Address))

	return triMaybe
}

// azureIsAddress reports whether a prefix value is meant as an address (a CIDR or an IP), not a service tag.
func azureIsAddress(value string) bool {
	return strings.ContainsAny(value, "/:") || (value != "" && value[0] >= '0' && value[0] <= '9')
}
