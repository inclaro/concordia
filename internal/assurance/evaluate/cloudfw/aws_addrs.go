package cloudfw

import (
	"slices"

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

// awsAddrs is the address set of an endpoint: its private IPs when the plan gives all of them, else the CIDRs of all
// its subnets, else unknown.
func (e *Evaluator) awsAddrs(res *plan.Resource, spec *awsEndpointSpec) addrSet {
	if set := awsHostAddrs(res, spec); set.known() {
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

// awsHostAddrs is the set of the private IPs of an endpoint when the plan gives all of them, else unknown. A network
// interface gives them in `private_ips`; when that list is unknown, its private IP is the only one only when the
// configuration sets none of `private_ips`, `private_ips_count`, and `private_ip_list`. An instance adds its
// `secondary_private_ips`, which are none when the configuration does not set them.
func awsHostAddrs(res *plan.Resource, spec *awsEndpointSpec) addrSet {
	if set := awsPrivateIPs(res); set.known() {
		return set
	}

	secondary, complete := awsSecondaryIPs(res)
	if !complete {
		return unknownAddrs
	}

	ip, _ := attrString(res, spec.ip)

	return unionSet(append(secondary, hostSet(ip))...)
}

// awsSecondaryIPs are the addresses that an endpoint has besides its private IP. complete is false when the plan
// may hide some.
func awsSecondaryIPs(res *plan.Resource) (secondary []addrSet, complete bool) {
	switch res.Type {
	case awsNetworkInterface:
		return nil, awsUnsetAll(res, "private_ips", "private_ips_count", "private_ip_list")
	case awsInstance:
		return awsHostList(res, "secondary_private_ips")
	}

	return nil, true
}

// awsHostList is the set of each address of the list attribute. complete is false when the list is unknown until
// apply and the configuration may set it.
func awsHostList(res *plan.Resource, path string) (hosts []addrSet, complete bool) {
	if unknownAt(res, path) {
		return nil, leavesUnset(res, path)
	}

	for _, ip := range attrStrings(res, path) {
		hosts = append(hosts, hostSet(ip))
	}

	return hosts, true
}

// awsUnsetAll reports whether the configuration, which the plan has for the resource, sets none of the attributes.
func awsUnsetAll(res *plan.Resource, paths ...string) bool {
	return !slices.ContainsFunc(paths, func(path string) bool { return !leavesUnset(res, path) })
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
