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
var azureEndpointKinds = map[string]func(e *Evaluator, ep *azureEndpoint, s *side) bool{
	azureNIC:             (*Evaluator).azureNICEndpoint,
	azureLinuxVM:         (*Evaluator).azureVMEndpoint,
	azureWindowsVM:       (*Evaluator).azureVMEndpoint,
	azureSubnet:          (*Evaluator).azureSubnetEndpoint,
	azurePrivateEndpoint: (*Evaluator).azurePrivateEndpoint,
}

// azureEndpointOf resolves an endpoint. ok is false, with the gaps in s, when the endpoint cannot be evaluated.
func (e *Evaluator) azureEndpointOf(r *plan.Resource, s *side) (*azureEndpoint, bool) {
	read, supported := azureEndpointKinds[r.Type]
	if !supported {
		s.gaps = append(s.gaps, unsupportedResource(r))

		return nil, false
	}

	ep := &azureEndpoint{res: r}
	if !read(e, ep, s) {
		return nil, false
	}

	ep.addrs = azureAddrs(ep)
	e.azureMembership(ep)

	return ep, true
}

func (e *Evaluator) azureNICEndpoint(ep *azureEndpoint, s *side) bool {
	return e.azureWithNIC(ep, ep.res, s)
}

// azureVMEndpoint reads a virtual machine through its only NIC. A machine with more than one NIC is not supported.
func (e *Evaluator) azureVMEndpoint(ep *azureEndpoint, s *side) bool {
	nics, ok := e.refs(ep.res, "network_interface_ids", s)

	switch {
	case !ok:
		return false
	case len(nics) > 1:
		s.gap(evaluate.GapUnsupportedResource, fmt.Sprintf("%s: a virtual machine with %d network interfaces is not supported",
			ep.res.Address, len(nics)))

		return false
	case len(nics) == 0:
		s.gap(evaluate.GapUnresolvedReference, ep.res.Address+" has no network interface in the plan")

		return false
	}

	return e.azureWithNIC(ep, nics[0], s)
}

func (e *Evaluator) azureSubnetEndpoint(ep *azureEndpoint, _ *side) bool {
	ep.subnet = ep.res

	return true
}

func (e *Evaluator) azurePrivateEndpoint(ep *azureEndpoint, s *side) bool {
	subnet, ok := e.ref(ep.res, azureSubnetIDAttr, s)
	ep.subnet = subnet

	return ok
}

// azureWithNIC reads the subnet and the private IP of the NIC of the endpoint.
func (e *Evaluator) azureWithNIC(ep *azureEndpoint, nic *plan.Resource, s *side) bool {
	if nic.Type != azureNIC {
		s.gaps = append(s.gaps, unsupportedResource(nic))

		return false
	}

	subnet, ok := e.ref(nic, azureNICSubnetAttr, s)
	ep.nic, ep.subnet = nic, subnet
	ep.ip, _ = attrString(nic, azureNICPrivateIPAttr)

	return ok
}

// azureAddrs is the address set of the endpoint: its private IP, else the first prefix of its subnet.
func azureAddrs(ep *azureEndpoint) addrSet {
	if ep.ip != "" {
		return hostSet(ep.ip)
	}

	if ep.subnet == nil {
		return unknownAddrs
	}

	if prefixes := attrStrings(ep.subnet, "address_prefixes"); len(prefixes) > 0 {
		return prefixSet(prefixes[0])
	}

	return unknownAddrs
}

// azureMembership reads the application security groups of the NIC of the endpoint.
func (e *Evaluator) azureMembership(ep *azureEndpoint) {
	if ep.nic == nil {
		ep.asgGaps = []assurance.Gap{azureNoInterface(ep)}

		return
	}

	scratch := &side{}
	for _, assoc := range e.ofType(azureNICASGAssoc) {
		e.azureAddMember(ep, assoc, scratch)
	}

	ep.asgGaps = scratch.gaps
}

// azureAddMember adds the ASG of one association when it binds the NIC of the endpoint. An association that does
// not resolve leaves a gap in s.
func (e *Evaluator) azureAddMember(ep *azureEndpoint, assoc *plan.Resource, s *side) {
	nic, ok := e.ref(assoc, azureNICIDAttr, s)
	if !ok || nic != ep.nic {
		return
	}

	if asg, ok := e.ref(assoc, azureASGIDAttr, s); ok && asg != nil {
		ep.asgs = azureAppendUnique(ep.asgs, asg)
	}
}

// azureInGroups is yes when the endpoint is a member of one of the ASGs, maybe when its membership is not fully
// known, else no.
func azureInGroups(ep *azureEndpoint, asgs []*plan.Resource, s *side) tri {
	if slices.ContainsFunc(asgs, func(a *plan.Resource) bool { return slices.Contains(ep.asgs, a) }) {
		return triYes
	}

	if len(ep.asgGaps) > 0 {
		s.gaps = append(s.gaps, ep.asgGaps...)

		return triMaybe
	}

	return triNo
}

// azureNoInterface is the gap of an ASG rule against an endpoint without a NIC: its members are not known.
func azureNoInterface(ep *azureEndpoint) assurance.Gap {
	return assurance.Gap{
		Kind:   evaluate.GapUnsupportedKind,
		Detail: fmt.Sprintf("application security group against %s, which has no network interface", ep.res.Address),
	}
}

// azureVNet identifies the virtual network of the subnet of an endpoint: the known name and resource group, or the
// configuration reference. It is "" when neither is known.
func azureVNet(ep *azureEndpoint) string {
	if ep.subnet == nil {
		return ""
	}

	name, hasName := attrString(ep.subnet, "virtual_network_name")
	group, hasGroup := attrString(ep.subnet, "resource_group_name")

	if hasName && hasGroup {
		return "name:" + group + "/" + name
	}

	if refs := ep.subnet.References["virtual_network_name"]; len(refs) == 1 {
		return "ref:" + refs[0]
	}

	return ""
}

// azureSameVNet is yes when both endpoints are in subnets of one virtual network, else maybe: traffic between
// networks depends on peering, which is not evaluated.
func azureSameVNet(pair *azurePair, s *side) tri {
	if from := azureVNet(pair.from); from != "" && from == azureVNet(pair.to) {
		return triYes
	}

	s.gap(evaluate.GapPeeringNotEvaluated, fmt.Sprintf("VirtualNetwork: %s and %s are not in one virtual network of the plan",
		pair.from.res.Address, pair.to.res.Address))

	return triMaybe
}

// azureIsAddress reports whether a prefix value is meant as an address (a CIDR or an IP), not a service tag.
func azureIsAddress(value string) bool {
	return strings.ContainsAny(value, "/:") || (value != "" && value[0] >= '0' && value[0] <= '9')
}
