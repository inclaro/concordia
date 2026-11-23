package cloudfw

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpStackPath is the stack type of the first network interface of an instance.
const gcpStackPath = "network_interface.0.stack_type"

// gcpIPv4Only is the stack type of an interface or a subnetwork that has no IPv6 addresses.
const gcpIPv4Only = "IPV4_ONLY"

// gcpDualStack reports whether the plan shows that the instance may have IPv6 addresses: its interface or its
// subnetwork states a stack type other than IPV4_ONLY.
func (e *Evaluator) gcpDualStack(res *plan.Resource) bool {
	if gcpStacked(res, gcpStackPath) {
		return true
	}

	sub := e.gcpSubnet(res)

	return sub != nil && gcpStacked(sub, "stack_type")
}

// gcpStacked reports whether the stack type at the path is known and is not IPV4_ONLY.
func gcpStacked(res *plan.Resource, path string) bool {
	stack, ok := attrString(res, path)

	return ok && stack != "" && stack != gcpIPv4Only
}

// gcpDecision is the result of a rule that matches: its action, or maybe when a deny of a firewall rule decided
// through address ranges and an endpoint may be dual-stack, because the deny then holds for IPv4 only.
func (e *Evaluator) gcpDecision(rule *gcpRule, check *gcpCheck, point *side) tri {
	if !e.gcpDualStackDeny(rule, check) {
		return rule.verdict()
	}

	point.gap(evaluate.GapAddressDependent, rule.res.Address+" "+rule.name+
		": an IPv4 deny decided, and the endpoints may be dual-stack")

	return triMaybe
}

// gcpDualStackDeny reports whether the rule is a deny of a firewall rule that matched through address ranges while an
// endpoint may be dual-stack.
func (e *Evaluator) gcpDualStackDeny(rule *gcpRule, check *gcpCheck) bool {
	if !rule.deny || rule.res == nil {
		return false
	}

	if !check.target.dualStack && !check.peer.dualStack {
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
	own := e.gcpSetAt(firewall, "destination_ranges", "")
	identities := e.gcpSetAt(firewall, "source_tags", "")
	accounts := e.gcpSetAt(firewall, "source_service_accounts", "email")

	return !ranges.empty() || !own.empty() || gcpAllEmpty(&identities, &accounts)
}
