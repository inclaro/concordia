package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureIPv4Version is the `private_ip_address_version` of an IPv4 IP configuration, and the provider default.
const azureIPv4Version = "IPv4"

// unprovenDeny reports whether the rule is a deny through an address prefix while the plan does not prove that an
// endpoint is IPv4-only: the deny then holds for IPv4 only, while IPv6 traffic falls through to a later rule. IPv6
// traffic between the two needs an IPv6 address at both ends, so one IPv4-only end is enough for the deny to decide.
func (r *azureRule) unprovenDeny(pair *azurePair) bool {
	if r.access() != triNo || !r.byAddress() {
		return false
	}

	return !pair.from.ipv4Only && !pair.to.ipv4Only
}

// byAddress reports whether the source or the destination of the rule names a CIDR or an IP address, so that the
// rule matches the traffic of one address family only.
func (r *azureRule) byAddress() bool {
	for _, fields := range []azurePeerFields{azureSourceFields, azureDestinationFields} {
		values := append(attrStrings(r.res, r.base+fields.prefix), attrStrings(r.res, r.base+fields.prefixes)...)
		if slices.ContainsFunc(values, azureIsAddress) {
			return true
		}
	}

	return false
}

// azureProvenIPv4Only reports whether the plan proves that the endpoint has IPv4 addresses only: every address prefix
// of its subnet is a known IPv4 prefix, and every IP configuration of its NIC, when it has one, is IPv4. An endpoint
// without a NIC (a subnet or a private endpoint) is proven by its subnet alone.
func azureProvenIPv4Only(end *azureEndpoint) bool {
	if end.subnet == nil || !azureIPv4Prefixes(end.subnet) {
		return false
	}

	return end.nic == nil || azureIPv4Configurations(end.nic)
}

// azureIPv4Prefixes reports whether the `address_prefixes` of the subnet are known, not empty, and all IPv4.
func azureIPv4Prefixes(subnet *plan.Resource) bool {
	const path = "address_prefixes"

	prefixes, ok := attr(subnet, path).([]any)
	if !ok || unknownAt(subnet, path) {
		return false
	}

	return len(prefixes) > 0 && !slices.ContainsFunc(prefixes, notIPv4Prefix)
}

// notIPv4Prefix reports whether an item of a prefix list is not an IPv4 CIDR or address.
func notIPv4Prefix(item any) bool {
	text, _ := item.(string)
	prefix, parsed := parseRange(text)

	return !parsed || !prefix.Addr().Is4()
}

// azureIPv4Configurations reports whether the `ip_configuration` list of the NIC is known and not empty, and every
// configuration has a known `private_ip_address_version` of IPv4. An absent version is IPv4, the provider default.
func azureIPv4Configurations(nic *plan.Resource) bool {
	count := blockCount(nic, azureIPConfigurations)
	if count == 0 || listUnknown(nic, azureIPConfigurations) {
		return false
	}

	for i := range count {
		path := fmt.Sprintf("%s[%d].private_ip_address_version", azureIPConfigurations, i)
		if unknownAt(nic, path) {
			return false
		}

		if version, ok := attrString(nic, path); ok && !strings.EqualFold(version, azureIPv4Version) {
			return false
		}
	}

	return true
}

// azureIPConfigurations is the list of IP configurations of a NIC.
const azureIPConfigurations = "ip_configuration"
