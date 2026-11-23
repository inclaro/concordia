package cloudfw

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Azure resource types and attributes that the evaluator reads.
const (
	azureNIC              = "azurerm_network_interface"
	azureSubnet           = "azurerm_subnet"
	azureNSG              = "azurerm_network_security_group"
	azureNSGRule          = "azurerm_network_security_rule"
	azureSubnetNSGAssoc   = "azurerm_subnet_network_security_group_association"
	azureNICNSGAssoc      = "azurerm_network_interface_security_group_association"
	azureNICASGAssoc      = "azurerm_network_interface_application_security_group_association"
	azureLinuxVM          = "azurerm_linux_virtual_machine"
	azureWindowsVM        = "azurerm_windows_virtual_machine"
	azurePrivateEndpoint  = "azurerm_private_endpoint"
	azureNSGIDAttr        = "network_security_group_id"
	azureNICIDAttr        = "network_interface_id"
	azureSubnetIDAttr     = "subnet_id"
	azureASGIDAttr        = "application_security_group_id"
	azureNICSubnetAttr    = "ip_configuration[0].subnet_id"
	azureNICPrivateIPAttr = "ip_configuration[0].private_ip_address"
)

// azurePair is the query and its two endpoints.
type azurePair struct {
	q        *Query
	from, to *azureEndpoint
}

// azurePoint is one check point on the path: the NIC or the subnet NSG of one endpoint, in one direction.
type azurePoint struct {
	ep       *azureEndpoint
	nicLevel bool
	outbound bool
}

// evaluateAzure follows the traffic through the four NSGs of the path: the NIC and the subnet NSG of the source
// (outbound), then the subnet and the NIC NSG of the destination (inbound). Every NSG on the path must allow it.
func (e *Evaluator) evaluateAzure(q *Query, from, to *plan.Resource) Result {
	ends := &side{}
	src, srcOK := e.azureEndpointOf(from, ends)
	dst, dstOK := e.azureEndpointOf(to, ends)

	if !srcOK || !dstOK {
		return unknownFrom(ends.gaps)
	}

	pair := &azurePair{q: q, from: src, to: dst}
	points := []azurePoint{
		{ep: src, nicLevel: true, outbound: true},
		{ep: src, nicLevel: false, outbound: true},
		{ep: dst, nicLevel: false, outbound: false},
		{ep: dst, nicLevel: true, outbound: false},
	}

	sides := make([]*side, 0, len(points))
	for i := range points {
		sides = append(sides, e.azureCheckPoint(pair, &points[i]))
	}

	return decide(sides...)
}

// azureCheckPoint evaluates one check point. A point without an NSG allows the traffic.
func (e *Evaluator) azureCheckPoint(pair *azurePair, point *azurePoint) *side {
	s := &side{verdict: triYes}

	nsg, known := e.azureNSGOf(point, s)

	switch {
	case !known:
		s.verdict = triMaybe
	case nsg != nil:
		e.azureCheckNSG(nsg, point.outbound, pair, s)
	}

	return s
}

// azureNSGOf is the NSG associated with the NIC or the subnet of the point, nil when there is none. known is false,
// with the gaps in s, when an association does not resolve.
func (e *Evaluator) azureNSGOf(point *azurePoint, s *side) (nsg *plan.Resource, known bool) {
	target, assocType, idAttr := point.ep.subnet, azureSubnetNSGAssoc, azureSubnetIDAttr
	if point.nicLevel {
		target, assocType, idAttr = point.ep.nic, azureNICNSGAssoc, azureNICIDAttr
	}

	if target == nil {
		return nil, true
	}

	assocs, known := e.azureAssociationsOf(target, assocType, idAttr, s)
	if !known {
		return nil, false
	}

	return e.azureAssociatedNSG(target, assocs, s)
}

// azureAssociationsOf are the associations of the type whose idAttr names the target. known is false when the
// idAttr of an association does not resolve: that association may name the target.
func (e *Evaluator) azureAssociationsOf(target *plan.Resource, assocType, idAttr string, s *side) ([]*plan.Resource, bool) {
	var out []*plan.Resource

	known := true

	for _, assoc := range e.ofType(assocType) {
		owner, ok := e.ref(assoc, idAttr, s)
		known = known && ok

		if ok && owner == target {
			out = append(out, assoc)
		}
	}

	return out, known
}

// azureAssociatedNSG is the one NSG that the associations of the target name.
func (e *Evaluator) azureAssociatedNSG(target *plan.Resource, assocs []*plan.Resource, s *side) (*plan.Resource, bool) {
	var nsgs []*plan.Resource

	for _, assoc := range assocs {
		nsg, ok := e.ref(assoc, azureNSGIDAttr, s)
		if !ok || nsg == nil {
			return nil, false
		}

		nsgs = azureAppendUnique(nsgs, nsg)
	}

	if len(nsgs) > 1 {
		s.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s has %d associated network security groups", target.Address, len(nsgs)))

		return nil, false
	}

	if len(nsgs) == 1 {
		return nsgs[0], true
	}

	return nil, true
}

func azureAppendUnique(list []*plan.Resource, r *plan.Resource) []*plan.Resource {
	if slices.Contains(list, r) {
		return list
	}

	return append(list, r)
}

// azureCheckNSG evaluates the rules of one NSG for one direction in ascending priority: the first rule that matches
// decides. A rule that may match before a decision makes the NSG result unknown.
func (e *Evaluator) azureCheckNSG(nsg *plan.Resource, outbound bool, pair *azurePair, s *side) {
	for _, rule := range e.azureRulesOf(nsg, outbound) {
		matched := e.azureMatch(&rule, pair, s)
		if matched == triNo {
			continue
		}

		s.rule(rule.address, rule.label())
		s.verdict = triMaybe

		if matched == triYes {
			s.verdict = rule.access()
		}

		return
	}

	s.verdict = triNo
}
