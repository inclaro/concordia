package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureFreshInline are the inline rules of the NSG without those that a rule resource of the NSG repeats by name:
// the planned values of the resource win over the copy in the list. The rules are marked stale when a rule resource
// that the plan deletes or updates may belong to the NSG.
func (e *Evaluator) azureFreshInline(nsg *plan.Resource, standalone []azureRule) []azureRule {
	names := azureOwnedNames(standalone)
	stale := e.azureNSGStale(nsg)

	inline := slices.DeleteFunc(azureInlineRules(nsg), func(rule azureRule) bool {
		name, ok := attrString(rule.res, rule.base+"name")

		return ok && slices.Contains(names, name)
	})

	for i := range inline {
		inline[i].stale = stale
	}

	return inline
}

// azureOwnedNames are the known names of the rule resources that surely belong to the NSG.
func azureOwnedNames(standalone []azureRule) []string {
	var names []string

	for _, rule := range standalone {
		if name, ok := attrString(rule.res, "name"); ok && rule.owned == triYes {
			names = append(names, name)
		}
	}

	return names
}

// azureNSGStale reports whether a rule resource that the plan deletes or updates may belong to the NSG.
func (e *Evaluator) azureNSGStale(nsg *plan.Resource) bool {
	return slices.ContainsFunc(e.changedOfType(azureNSGRule), func(res *plan.Resource) bool {
		return e.azureRuleOwner(res, nsg, &side{}) != triNo
	})
}

// decision is the result of the first rule that matches: its access, or maybe when the match is uncertain, when the
// rule is an inline copy that the plan may have changed, or when an IPv4 deny decided for endpoints that may be
// dual-stack.
func (r *azureRule) decision(pair *azurePair, matched tri, report *side) tri {
	switch {
	case matched != triYes:
		return matched
	case r.stale:
		report.gaps = append(report.gaps, staleInlineGap(r.res, azureRulesAttr))

		return triMaybe
	case r.dualStackDeny(pair):
		report.gap(evaluate.GapAddressDependent, fmt.Sprintf("%s %s: an IPv4 deny decided, and the endpoints may be dual-stack",
			r.address, r.label()))

		return triMaybe
	}

	return r.access()
}

// dualStackDeny reports whether the rule is a deny through an address prefix, and an endpoint may be dual-stack: the
// deny then holds for IPv4 only, while IPv6 traffic falls through to a later rule.
func (r *azureRule) dualStackDeny(pair *azurePair) bool {
	if r.access() != triNo || !r.byAddress() {
		return false
	}

	return pair.dualStack()
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

// dualStack reports whether the plan shows that an endpoint of the pair has IPv6 addresses.
func (pair *azurePair) dualStack() bool {
	return azureDualStack(pair.from) || azureDualStack(pair.to)
}

// azureDualStack reports whether the NIC of the endpoint has an IPv6 IP configuration, or its subnet an IPv6 prefix.
func azureDualStack(end *azureEndpoint) bool {
	if end.nic != nil && azureIPv6Configuration(end.nic) {
		return true
	}

	return end.subnet != nil && slices.ContainsFunc(attrStrings(end.subnet, "address_prefixes"), isIPv6Range)
}

// azureIPv6Configuration reports whether an `ip_configuration` of the NIC has `private_ip_address_version` IPv6.
func azureIPv6Configuration(nic *plan.Resource) bool {
	for i := range blockCount(nic, "ip_configuration") {
		version, _ := attrString(nic, fmt.Sprintf("ip_configuration[%d].private_ip_address_version", i))
		if strings.EqualFold(version, "IPv6") {
			return true
		}
	}

	return false
}

// isIPv6Range reports whether the text is an IPv6 CIDR or address.
func isIPv6Range(text string) bool {
	prefix, ok := parseRange(text)

	return ok && !prefix.Addr().Is4()
}
