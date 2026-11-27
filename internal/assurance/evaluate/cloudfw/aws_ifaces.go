package cloudfw

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// awsTargetOf is the one network interface of the endpoint that an attachment which concerns the endpoint names: by
// its known ID, or by the one resource that its configuration references resolve to. It is nil when the attachment
// names no single interface. A known ID names an attached network interface before the endpoint (interfaces[0]):
// the `primary_network_interface_id` of an instance is also the ID of the network interface of its primary
// `network_interface` block, and that interface, not the instance, has a view when the blocks give the groups.
func (e *Evaluator) awsTargetOf(link *awsLink, interfaces []*plan.Resource) *plan.Resource {
	if value, ok := attrString(link.res, link.path); ok {
		ordered := append(slices.Clone(interfaces[1:]), interfaces[0])
		at := slices.IndexFunc(ordered, func(nic *plan.Resource) bool { return slices.Contains(awsIDs(nic), value) })

		if at < 0 {
			return nil
		}

		return ordered[at]
	}

	found, _ := e.resolveAll(link.res, link.res.References[planPath(link.path)], &side{})
	if len(found) != 1 {
		return nil
	}

	return found[0]
}

// awsInterfaceViews are the network interfaces of an endpoint, each with the groups that the plan puts on that
// interface and its own addresses: the endpoint's own interface (unless its groups come from its `network_interface`
// blocks) and each attached interface. A group that an attachment adds without naming one interface is on none of
// them, so a view may hold fewer groups than the interface has. Every view keeps the pending rules of the endpoint.
func (e *Evaluator) awsInterfaceViews(res *plan.Resource, spec *awsEndpointSpec, own []*plan.Resource, attached *awsAttached, viaInterfaces bool) []*awsEndpoint {
	views := make([]*awsEndpoint, 0, len(attached.nics)+1)
	if !viaInterfaces {
		views = append(views, &awsEndpoint{
			groups:  sortResources(slices.Concat(own, attached.byNIC[res])),
			pending: attached.pending, addrs: e.awsAddrs(res, spec),
		})
	}

	for _, nic := range attached.nics {
		views = append(views, &awsEndpoint{
			groups:  sortResources(slices.Clone(attached.byNIC[nic])),
			pending: attached.pending, addrs: e.awsAddrs(nic, &awsNICSpec),
		})
	}

	return views
}

// interfaces are the views of the network interfaces of the endpoint, or the endpoint itself when it has no views.
func (endpoint *awsEndpoint) interfaces() []*awsEndpoint {
	if len(endpoint.ifaces) == 0 {
		return []*awsEndpoint{endpoint}
	}

	return endpoint.ifaces
}

// severalInterfaces reports whether the endpoint has more than one network interface.
func (endpoint *awsEndpoint) severalInterfaces() bool { return len(endpoint.ifaces) > 1 }

// awsInterfacePair confirms an `allowed` result between endpoints of which one has several network interfaces.
// Traffic leaves through one interface of the source and arrives at one interface of the destination, and the rules
// of those two interfaces apply to it. The union of the groups of all interfaces can allow traffic that no pair of
// interfaces allows, so `allowed` needs one pair of interfaces that allows it on its own groups. found is false when
// no pair does.
func (e *Evaluator) awsInterfacePair(query *Query, src, dst *awsEndpoint) (res Result, found bool) {
	for _, out := range src.interfaces() {
		for _, in := range dst.interfaces() {
			if res = decide(e.awsSide(query, out, in, &awsEgress), e.awsSide(query, in, out, &awsIngress)); res.Verdict == VerdictAllowed {
				return res, true
			}
		}
	}

	return Result{}, false
}

// awsNoInterfacePair is the gap of an `allowed` result that no one pair of network interfaces confirms.
func awsNoInterfacePair(from, to *plan.Resource) assurance.Gap {
	return assurance.Gap{
		Kind: evaluate.GapUnsupportedResource,
		Detail: fmt.Sprintf("%s to %s: the groups of several network interfaces together allow the traffic, but no one "+
			"pair of network interfaces is shown to allow it", from.Address, to.Address),
	}
}
