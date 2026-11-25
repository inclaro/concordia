package cloudfw

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpStackPath is the stack type of the first network interface of an instance.
const gcpStackPath = "network_interface.0.stack_type"

// gcpIPv4Only is the stack type of an interface or a subnetwork that has no IPv6 addresses.
const gcpIPv4Only = "IPV4_ONLY"

// gcpProvenIPv4Only reports whether the plan proves that the instance has IPv4 addresses only: the stack type of its
// interface is IPV4_ONLY, or the interface states none and the stack type of its subnetwork in the plan is
// IPV4_ONLY. A stack type is IPV4_ONLY when it is known as such, or when it is unknown until apply only because the
// configuration does not set it: the provider then uses IPV4_ONLY. Any other unknown stack type proves nothing.
func (e *Evaluator) gcpProvenIPv4Only(res *plan.Resource) bool {
	if gcpDefaultInterfaceStack(res) {
		return true
	}

	if stack, ok := attrString(res, gcpStackPath); ok {
		return stack == gcpIPv4Only
	}

	if unknownAt(res, gcpStackPath) {
		return false
	}

	sub := e.gcpSubnet(res)

	return sub != nil && gcpSubnetIPv4Only(sub)
}

// gcpDefaultInterfaceStack reports whether the stack type of the first interface is the provider default: unknown
// and not set by the configuration, whose interface blocks are all static, and the interface has no IPv6 setting.
func gcpDefaultInterfaceStack(res *plan.Resource) bool {
	return staticBlocks(res, gcpInterfaceList) && unsetComputed(res, gcpStackPath) && gcpInterfaceWithoutIPv6(res)
}

// gcpSubnetIPv4Only reports whether the stack type of the subnetwork is IPV4_ONLY, known, or by default when the
// subnetwork has no IPv6 setting.
func gcpSubnetIPv4Only(sub *plan.Resource) bool {
	if unsetComputed(sub, gcpStackAttr) {
		return gcpWithoutIPv6(sub, gcpSubnetIPv6)
	}

	stack, ok := attrString(sub, gcpStackAttr)

	return ok && stack == gcpIPv4Only
}

// Attributes of the stack type.
const (
	gcpInterfaceList = "network_interface"
	gcpStackAttr     = "stack_type"
)

// gcpDecision is the result of a rule that matches: its action, or maybe when a deny of a firewall rule decided
// through address ranges and the plan does not prove that an endpoint is IPv4-only, because the deny then holds for
// IPv4 only and IPv6 traffic falls through to a later rule.
func (e *Evaluator) gcpDecision(rule *gcpRule, check *gcpCheck, point *side) tri {
	if !e.gcpUnprovenDeny(rule, check) {
		return rule.verdict()
	}

	point.gap(evaluate.GapAddressDependent, rule.res.Address+" "+rule.name+
		": an IPv4 deny decided, and the stack type of neither endpoint is known to be "+gcpIPv4Only)

	return triMaybe
}

// gcpUnprovenDeny reports whether the rule is a deny of a firewall rule that matched through address ranges while
// neither endpoint is proven IPv4-only. IPv6 traffic between the two needs an IPv6 address at both ends, so one
// IPv4-only end is enough for the deny to decide.
func (e *Evaluator) gcpUnprovenDeny(rule *gcpRule, check *gcpCheck) bool {
	if !rule.deny || rule.res == nil {
		return false
	}

	if check.target.ipv4Only || check.peer.ipv4Only {
		return false
	}

	return e.gcpByRange(rule.res, check)
}

// gcpByRange reports whether the rule may match through address ranges: an egress rule always does (its
// destination ranges default to 0.0.0.0/0); an ingress rule does unless only source tags or service accounts select
// the peer.
func (e *Evaluator) gcpByRange(firewall *plan.Resource, check *gcpCheck) bool {
	if !check.ingress {
		return true
	}

	ranges := e.gcpSetAt(firewall, "source_ranges", "")
	identities := e.gcpSetAt(firewall, "source_tags", "")
	accounts := e.gcpSetAt(firewall, "source_service_accounts", "email")

	return !ranges.empty() || e.gcpOwnRangesSet(firewall) || gcpAllEmpty(&identities, &accounts)
}

// gcpOwnRangesSet reports whether an ingress rule may limit its destination ranges: they are not empty, and not
// the provider default of a configuration that does not set them.
func (e *Evaluator) gcpOwnRangesSet(firewall *plan.Resource) bool {
	const path = "destination_ranges"
	if unsetComputed(firewall, path) {
		return false
	}

	own := e.gcpSetAt(firewall, path, "")

	return !own.empty()
}
