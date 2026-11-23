package cloudfw

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// GCP firewall rule values.
const (
	gcpFirewallType     = "google_compute_firewall"
	gcpIngress          = "INGRESS"
	gcpEgress           = "EGRESS"
	gcpDefaultPriority  = 1000
	gcpImpliedPriority  = 65535
	gcpAllow            = "allow"
	gcpDeny             = "deny"
	gcpAllAddresses     = "0.0.0.0/0"
	gcpImpliedRuleOwner = "implied"
)

// gcpRule is one firewall rule of a direction. res is nil for an implied rule, which matches all traffic.
type gcpRule struct {
	res      *plan.Resource
	priority int
	deny     bool
}

func (r *gcpRule) action() string {
	if r.deny {
		return gcpDeny
	}

	return gcpAllow
}

func (r *gcpRule) verdict() tri {
	if r.deny {
		return triNo
	}

	return triYes
}

func (r *gcpRule) record(point *side) {
	owner := gcpImpliedRuleOwner
	if r.res != nil {
		owner = r.res.Address
	}

	point.rule(owner, fmt.Sprintf("priority %d %s", r.priority, r.action()))
}

// gcpCheck is one check point: egress at the source, or ingress at the destination. target is the instance that
// the rules apply to, peer the other end.
type gcpCheck struct {
	query   *Query
	target  *gcpEndpoint
	peer    *gcpEndpoint
	ingress bool
}

// gcpSide evaluates the rules of the check point in order: the first rule that matches decides, and a rule that may
// match before a decision makes the side unknown.
func (e *Evaluator) gcpSide(check *gcpCheck) *side {
	point := &side{}

	for _, rule := range e.gcpRules(check.ingress) {
		scratch := &side{}

		switch e.gcpMatch(&rule, check, scratch) {
		case triNo:
			continue
		case triMaybe:
			rule.record(point)
			point.gaps = append(point.gaps, scratch.gaps...)
			point.verdict = triMaybe
		case triYes:
			rule.record(point)
			point.verdict = rule.verdict()
		}

		return point
	}

	return point
}

// gcpRules are the enabled firewall rules of the direction and the implied rule, by ascending priority, deny before
// allow at equal priority.
func (e *Evaluator) gcpRules(ingress bool) []gcpRule {
	direction, impliedDeny := gcpEgress, false
	if ingress {
		direction, impliedDeny = gcpIngress, true
	}

	rules := []gcpRule{{priority: gcpImpliedPriority, deny: impliedDeny}}

	for _, firewall := range e.ofType(gcpFirewallType) {
		if attrBool(firewall, "disabled") || gcpDirection(firewall) != direction {
			continue
		}

		priority, ok := attrInt(firewall, "priority")
		if !ok {
			priority = gcpDefaultPriority
		}

		rules = append(rules, gcpRule{res: firewall, priority: priority, deny: blockCount(firewall, gcpDeny) > 0})
	}

	slices.SortStableFunc(rules, func(a, b gcpRule) int {
		return cmpChain(cmp.Compare(a.priority, b.priority), gcpDenyFirst(a.deny, b.deny))
	})

	return rules
}

func gcpDirection(firewall *plan.Resource) string {
	if direction, ok := attrString(firewall, "direction"); ok && direction != "" {
		return direction
	}

	return gcpIngress
}

func gcpDenyFirst(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}

	return 1
}

// gcpMatch matches one rule against the traffic of the check point.
func (e *Evaluator) gcpMatch(rule *gcpRule, check *gcpCheck, point *side) tri {
	if rule.res == nil {
		return triYes
	}

	firewall := rule.res
	applies := and(e.gcpInNetwork(firewall, check.target.network, point), e.gcpTargets(firewall, check.target, point))
	ports := gcpPorts(firewall, rule.action(), check.query, point)

	return and(and(ports, applies), e.gcpPeer(firewall, check, point))
}

// gcpInNetwork is whether the firewall rule belongs to the network.
func (e *Evaluator) gcpInNetwork(firewall *plan.Resource, network string, point *side) tri {
	key, ok := e.gcpNetworkKey(firewall, "network", point)

	switch {
	case !ok:
		return triMaybe
	case key == network:
		return triYes
	}

	return triNo
}

// gcpTargets is whether the rule applies to the instance: it has no targets, or a target tag or target service
// account of the instance.
func (e *Evaluator) gcpTargets(firewall *plan.Resource, endpoint *gcpEndpoint, point *side) tri {
	tags := e.gcpSetAt(firewall, "target_tags", "")
	accounts := e.gcpSetAt(firewall, "target_service_accounts", "email")

	if tags.empty() && accounts.empty() {
		return triYes
	}

	return or(gcpOverlap(&tags, &endpoint.tags, point), gcpOverlap(&accounts, &endpoint.accounts, point))
}

// gcpPeer matches the other end: the destination ranges for egress, and the source ranges, tags, and service
// accounts for ingress. A rule without a peer matches every address.
func (e *Evaluator) gcpPeer(firewall *plan.Resource, check *gcpCheck, point *side) tri {
	if !check.ingress {
		ranges := e.gcpSetAt(firewall, "destination_ranges", "")
		if ranges.empty() {
			ranges.vals = []string{gcpAllAddresses}
		}

		return gcpRanges(&ranges, check.peer.addrs, point)
	}

	ranges := e.gcpSetAt(firewall, "source_ranges", "")
	tags := e.gcpSetAt(firewall, "source_tags", "")
	accounts := e.gcpSetAt(firewall, "source_service_accounts", "email")

	if gcpAllEmpty(&ranges, &tags, &accounts) {
		ranges.vals = []string{gcpAllAddresses}
	}

	identity := or(gcpOverlap(&tags, &check.peer.tags, point), gcpOverlap(&accounts, &check.peer.accounts, point))

	return or(gcpRanges(&ranges, check.peer.addrs, point), identity)
}

func gcpAllEmpty(sets ...*gcpSet) bool {
	return !slices.ContainsFunc(sets, func(set *gcpSet) bool { return !set.empty() })
}

// gcpRanges matches address ranges against the addresses of an endpoint. An empty list matches nothing.
func gcpRanges(ranges *gcpSet, addrs addrSet, point *side) tri {
	result := anyPrefix(ranges.vals, addrs, point)
	if result != triYes && len(ranges.gaps) > 0 {
		point.gaps = append(point.gaps, ranges.gaps...)

		return triMaybe
	}

	return result
}

// gcpOverlap is yes when the rule values and the endpoint values share one, no when both are known and share none,
// and maybe otherwise. An empty rule list matches nothing.
func gcpOverlap(rule, endpoint *gcpSet, point *side) tri {
	switch {
	case rule.empty():
		return triNo
	case slices.ContainsFunc(rule.vals, func(value string) bool { return slices.Contains(endpoint.vals, value) }):
		return triYes
	case len(rule.gaps)+len(endpoint.gaps) == 0:
		return triNo
	}

	point.gaps = append(append(point.gaps, rule.gaps...), endpoint.gaps...)

	return triMaybe
}

// gcpPorts matches the protocol and port of the query against the allow or deny blocks of the rule. A block
// without ports matches every port of its protocol.
func gcpPorts(firewall *plan.Resource, action string, query *Query, point *side) tri {
	result := triNo
	for i := range blockCount(firewall, action) {
		result = or(result, gcpBlock(firewall, fmt.Sprintf("%s[%d]", action, i), query, point))
	}

	return result
}

func gcpBlock(firewall *plan.Resource, path string, query *Query, point *side) tri {
	protocol, _ := attrString(firewall, path+".protocol")
	if matchProtocol(protocol, query) == triNo {
		return triNo
	}

	ports := attrStrings(firewall, path+".ports")
	if len(ports) == 0 {
		return triYes
	}

	return anyPortSpec(ports, query.Port, point)
}
