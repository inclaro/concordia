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

func (r *gcpRule) record(s *side) {
	owner := gcpImpliedRuleOwner
	if r.res != nil {
		owner = r.res.Address
	}

	s.rule(owner, fmt.Sprintf("priority %d %s", r.priority, r.action()))
}

// gcpCheck is one check point: egress at the source, or ingress at the destination. target is the instance that
// the rules apply to, peer the other end.
type gcpCheck struct {
	q       *Query
	target  *gcpEndpoint
	peer    *gcpEndpoint
	ingress bool
}

// gcpSide evaluates the rules of the check point in order: the first rule that matches decides, and a rule that may
// match before a decision makes the side unknown.
func (e *Evaluator) gcpSide(c *gcpCheck) *side {
	s := &side{}

	for _, rule := range e.gcpRules(c.ingress) {
		scratch := &side{}

		switch e.gcpMatch(&rule, c, scratch) {
		case triNo:
			continue
		case triMaybe:
			rule.record(s)
			s.gaps = append(s.gaps, scratch.gaps...)
			s.verdict = triMaybe
		default:
			rule.record(s)
			s.verdict = rule.verdict()
		}

		return s
	}

	return s
}

// gcpRules are the enabled firewall rules of the direction and the implied rule, by ascending priority, deny before
// allow at equal priority.
func (e *Evaluator) gcpRules(ingress bool) []gcpRule {
	direction, impliedDeny := gcpEgress, false
	if ingress {
		direction, impliedDeny = gcpIngress, true
	}

	rules := []gcpRule{{priority: gcpImpliedPriority, deny: impliedDeny}}

	for _, fw := range e.ofType(gcpFirewallType) {
		if attrBool(fw, "disabled") || gcpDirection(fw) != direction {
			continue
		}

		priority, ok := attrInt(fw, "priority")
		if !ok {
			priority = gcpDefaultPriority
		}

		rules = append(rules, gcpRule{res: fw, priority: priority, deny: blockCount(fw, gcpDeny) > 0})
	}

	slices.SortStableFunc(rules, func(a, b gcpRule) int {
		return cmpChain(cmp.Compare(a.priority, b.priority), gcpDenyFirst(a.deny, b.deny))
	})

	return rules
}

func gcpDirection(fw *plan.Resource) string {
	if d, ok := attrString(fw, "direction"); ok && d != "" {
		return d
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
func (e *Evaluator) gcpMatch(rule *gcpRule, c *gcpCheck, s *side) tri {
	if rule.res == nil {
		return triYes
	}

	fw := rule.res
	applies := and(e.gcpInNetwork(fw, c.target.network, s), e.gcpTargets(fw, c.target, s))

	return and(and(gcpPorts(fw, rule.action(), c.q, s), applies), e.gcpPeer(fw, c, s))
}

// gcpInNetwork is whether the firewall rule belongs to the network.
func (e *Evaluator) gcpInNetwork(fw *plan.Resource, network string, s *side) tri {
	key, ok := e.gcpNetworkKey(fw, "network", s)

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
func (e *Evaluator) gcpTargets(fw *plan.Resource, ep *gcpEndpoint, s *side) tri {
	tags := e.gcpSetAt(fw, "target_tags", "")
	accounts := e.gcpSetAt(fw, "target_service_accounts", "email")

	if tags.empty() && accounts.empty() {
		return triYes
	}

	return or(gcpOverlap(&tags, &ep.tags, s), gcpOverlap(&accounts, &ep.accounts, s))
}

// gcpPeer matches the other end: the destination ranges for egress, and the source ranges, tags, and service
// accounts for ingress. A rule without a peer matches every address.
func (e *Evaluator) gcpPeer(fw *plan.Resource, c *gcpCheck, s *side) tri {
	if !c.ingress {
		ranges := e.gcpSetAt(fw, "destination_ranges", "")
		if ranges.empty() {
			ranges.vals = []string{gcpAllAddresses}
		}

		return gcpRanges(&ranges, c.peer.addrs, s)
	}

	ranges := e.gcpSetAt(fw, "source_ranges", "")
	tags := e.gcpSetAt(fw, "source_tags", "")
	accounts := e.gcpSetAt(fw, "source_service_accounts", "email")

	if gcpAllEmpty(&ranges, &tags, &accounts) {
		ranges.vals = []string{gcpAllAddresses}
	}

	identity := or(gcpOverlap(&tags, &c.peer.tags, s), gcpOverlap(&accounts, &c.peer.accounts, s))

	return or(gcpRanges(&ranges, c.peer.addrs, s), identity)
}

func gcpAllEmpty(sets ...*gcpSet) bool {
	return !slices.ContainsFunc(sets, func(g *gcpSet) bool { return !g.empty() })
}

// gcpRanges matches address ranges against the addresses of an endpoint. An empty list matches nothing.
func gcpRanges(ranges *gcpSet, addrs addrSet, s *side) tri {
	result := anyPrefix(ranges.vals, addrs, s)
	if result != triYes && len(ranges.gaps) > 0 {
		s.gaps = append(s.gaps, ranges.gaps...)

		return triMaybe
	}

	return result
}

// gcpOverlap is yes when the rule values and the endpoint values share one, no when both are known and share none,
// and maybe otherwise. An empty rule list matches nothing.
func gcpOverlap(rule, ep *gcpSet, s *side) tri {
	switch {
	case rule.empty():
		return triNo
	case slices.ContainsFunc(rule.vals, func(v string) bool { return slices.Contains(ep.vals, v) }):
		return triYes
	case len(rule.gaps)+len(ep.gaps) == 0:
		return triNo
	}

	s.gaps = append(append(s.gaps, rule.gaps...), ep.gaps...)

	return triMaybe
}

// gcpPorts matches the protocol and port of the query against the allow or deny blocks of the rule. A block
// without ports matches every port of its protocol.
func gcpPorts(fw *plan.Resource, action string, q *Query, s *side) tri {
	result := triNo
	for i := range blockCount(fw, action) {
		result = or(result, gcpBlock(fw, fmt.Sprintf("%s[%d]", action, i), q, s))
	}

	return result
}

func gcpBlock(fw *plan.Resource, path string, q *Query, s *side) tri {
	protocol, _ := attrString(fw, path+".protocol")
	if matchProtocol(protocol, q) == triNo {
		return triNo
	}

	ports := attrStrings(fw, path+".ports")
	if len(ports) == 0 {
		return triYes
	}

	return anyPortSpec(ports, q.Port, s)
}
