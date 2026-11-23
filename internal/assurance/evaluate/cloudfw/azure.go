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
	azureASG              = "azurerm_application_security_group"
	azureVNetType         = "azurerm_virtual_network"
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
	end      *azureEndpoint
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
		{end: src, nicLevel: true, outbound: true},
		{end: src, nicLevel: false, outbound: true},
		{end: dst, nicLevel: false, outbound: false},
		{end: dst, nicLevel: true, outbound: false},
	}

	sides := make([]*side, 0, len(points))
	for i := range points {
		sides = append(sides, e.azureCheckPoint(pair, &points[i]))
	}

	return decide(sides...)
}

// azureCheckPoint evaluates one check point. A point without an NSG, or whose NSG does not apply to the endpoint,
// allows the traffic.
func (e *Evaluator) azureCheckPoint(pair *azurePair, point *azurePoint) *side {
	report := &side{verdict: triYes}

	nsg, known := e.azureNSGOf(point, report)
	applies := point.nsgApplies()

	switch {
	case !known:
		report.verdict = triMaybe
	case nsg == nil || applies == triNo:
		// No NSG applies at this point: it allows the traffic.
	case applies == triMaybe:
		report.gaps = append(report.gaps, point.end.policyGaps...)
		report.verdict = triMaybe
	default:
		e.azureCheckNSG(nsg, point.outbound, pair, report)
	}

	return report
}

// nsgApplies is whether the NSG of the point applies: the subnet NSG of a private endpoint applies to its inbound
// traffic only when the subnet enables network policies for private endpoints.
func (point *azurePoint) nsgApplies() tri {
	if point.nicLevel || point.outbound {
		return triYes
	}

	return point.end.subnetNSG
}

// azureNSGOf is the NSG associated with the NIC or the subnet of the point, nil when there is none. known is false,
// with the gaps in report, when an association does not resolve.
func (e *Evaluator) azureNSGOf(point *azurePoint, report *side) (nsg *plan.Resource, known bool) {
	target, assocType, idAttr := point.end.subnet, azureSubnetNSGAssoc, azureSubnetIDAttr
	if point.nicLevel {
		target, assocType, idAttr = point.end.nic, azureNICNSGAssoc, azureNICIDAttr
	}

	if target == nil {
		return nil, true
	}

	assocs, known := e.azureAssociationsOf(target, assocType, idAttr, report)
	if !known {
		return nil, false
	}

	return e.azureAssociatedNSG(target, assocs, report)
}

// azureAssociationsOf are the associations of the type whose idAttr names the target. known is false when the
// idAttr of an association does not resolve to a resource of the target's type: that association may name the target.
func (e *Evaluator) azureAssociationsOf(target *plan.Resource, assocType, idAttr string, report *side) ([]*plan.Resource, bool) {
	var out []*plan.Resource

	known := true

	for _, assoc := range e.ofType(assocType) {
		owner, ok := e.refOfType(assoc, idAttr, report, target.Type)
		known = known && ok

		if ok && owner == target {
			out = append(out, assoc)
		}
	}

	return out, known
}

// azureAssociatedNSG is the one NSG that the associations of the target name.
func (e *Evaluator) azureAssociatedNSG(target *plan.Resource, assocs []*plan.Resource, report *side) (*plan.Resource, bool) {
	var nsgs []*plan.Resource

	for _, assoc := range assocs {
		nsg, ok := e.refOfType(assoc, azureNSGIDAttr, report, azureNSG)
		if !ok || nsg == nil {
			return nil, false
		}

		nsgs = appendUnique(nsgs, nsg)
	}

	if len(nsgs) > 1 {
		report.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s has %d associated network security groups", target.Address, len(nsgs)))

		return nil, false
	}

	if len(nsgs) == 1 {
		return nsgs[0], true
	}

	return nil, true
}

// appendUnique appends the resource to the list unless the list holds it.
func appendUnique(list []*plan.Resource, r *plan.Resource) []*plan.Resource {
	if slices.Contains(list, r) {
		return list
	}

	return append(list, r)
}

// azureCheckNSG evaluates the rules of one NSG for one direction in ascending priority: the first rule that matches
// decides. A rule that may match before a decision makes the NSG result unknown, and so does a rule list that is
// unknown until apply, because it can hold any rule at any priority.
func (e *Evaluator) azureCheckNSG(nsg *plan.Resource, outbound bool, pair *azurePair, report *side) {
	if azureRulesUnknown(nsg, report) {
		report.verdict = triMaybe

		return
	}

	for _, rule := range e.azureRulesOf(nsg, outbound) {
		matched := e.azureMatch(&rule, pair, report)
		if matched == triNo {
			continue
		}

		report.rule(rule.address, rule.label())
		report.verdict = rule.decision(pair, matched, report)

		return
	}

	report.verdict = triNo
}
