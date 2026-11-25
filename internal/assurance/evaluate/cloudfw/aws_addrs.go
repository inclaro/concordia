package cloudfw

import (
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// awsNICSpec names the address attributes of a network interface.
var awsNICSpec = awsEndpointSpecs[awsNetworkInterface]

// awsEndpointAddrs is the address set of an endpoint: the addresses of its own interface and of every network
// interface that is attached to it. An instance whose interfaces all come from its `network_interface` blocks has
// only the addresses of those interfaces.
func (e *Evaluator) awsEndpointAddrs(res *plan.Resource, spec *awsEndpointSpec, nics []*plan.Resource, viaInterfaces bool) addrSet {
	sets := make([]addrSet, 0, len(nics)+1)
	if !viaInterfaces {
		sets = append(sets, e.awsAddrs(res, spec))
	}

	for _, nic := range nics {
		sets = append(sets, e.awsAddrs(nic, &awsNICSpec))
	}

	return unionSet(sets...)
}

// awsAddrs is the address set of an endpoint: the private IPs of a network interface when the plan knows them all,
// else its private IP when it is known, else the CIDRs of all its subnets, else unknown.
func (e *Evaluator) awsAddrs(res *plan.Resource, spec *awsEndpointSpec) addrSet {
	if set := awsPrivateIPs(res); set.known() {
		return set
	}

	// An endpoint type without an IP attribute, or an IP unknown until apply, gives no host set.
	ip, _ := attrString(res, spec.ip)
	if set := hostSet(ip); set.known() {
		return set
	}

	if spec.subnets == "" {
		return unknownAddrs
	}

	// A subnet that does not resolve leaves the addresses unknown; a rule prefix that depends on them then gives
	// the gap address-dependent-range.
	subnets, ok := e.refsOfType(res, spec.subnets, &side{}, awsSubnet)
	if !ok {
		return unknownAddrs
	}

	sets := make([]addrSet, 0, len(subnets))
	for _, subnet := range subnets {
		cidr, _ := attrString(subnet, "cidr_block")
		sets = append(sets, prefixSet(cidr))
	}

	return unionSet(sets...)
}

// awsPrivateIPs is the set of the `private_ips` of a network interface when the plan knows the whole list, else
// unknown.
func awsPrivateIPs(res *plan.Resource) addrSet {
	const path = "private_ips"

	if res.Type != awsNetworkInterface || unknownAt(res, path) {
		return unknownAddrs
	}

	ips := attrStrings(res, path)
	sets := make([]addrSet, 0, len(ips))

	for _, ip := range ips {
		sets = append(sets, hostSet(ip))
	}

	return unionSet(sets...)
}
