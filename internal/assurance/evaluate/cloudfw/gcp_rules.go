package cloudfw

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

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
			point.verdict = e.gcpDecision(&rule, check, point)
		}

		return point
	}

	return point
}

// gcpMatch matches one rule against the traffic of the check point. An uncertain rule that may match gives maybe.
func (e *Evaluator) gcpMatch(rule *gcpRule, check *gcpCheck, point *side) tri {
	if rule.res == nil {
		return triYes
	}

	firewall := rule.res
	applies := and(e.gcpInNetwork(firewall, check.target.network, point), e.gcpTargets(firewall, check, point))
	traffic := and(gcpRulePorts(rule, check.query, point), e.gcpPeer(firewall, check, point))

	match := and(applies, traffic)
	if match == triNo || !rule.uncertain() {
		return match
	}

	point.gaps = append(point.gaps, rule.gaps...)

	return triMaybe
}

// gcpRulePorts matches the protocol and port of the query; a rule whose action the plan does not give may match.
func gcpRulePorts(rule *gcpRule, query *Query, point *side) tri {
	if rule.actionUnknown {
		return triMaybe
	}

	return gcpPorts(rule.res, rule.action(), query, point)
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

// gcpTargets is whether the rule applies to the instance of the check point: it has no targets, or a target tag or
// target service account of the instance; and the instance address is in the ranges of its side of the rule
// (`destination_ranges` for ingress, `source_ranges` for egress), when the rule has them.
func (e *Evaluator) gcpTargets(firewall *plan.Resource, check *gcpCheck, point *side) tri {
	endpoint := check.target
	tags := e.gcpSetAt(firewall, "target_tags", "")
	accounts := e.gcpSetAt(firewall, "target_service_accounts", "email")

	targeted := triYes
	if !tags.empty() || !accounts.empty() {
		targeted = or(gcpOverlap(&tags, &endpoint.tags, point), gcpOverlap(&accounts, &endpoint.accounts, point))
	}

	return and(targeted, e.gcpOwnRanges(firewall, check, point))
}

// gcpOwnRanges matches the ranges of the rule for the instance of the check point. No ranges means every address. The
// provider computes these ranges when the configuration does not set them; the plan then shows them unknown, and GCP
// applies the rule to every address.
func (e *Evaluator) gcpOwnRanges(firewall *plan.Resource, check *gcpCheck, point *side) tri {
	path := "source_ranges"
	if check.ingress {
		path = "destination_ranges"
	}

	if unsetComputed(firewall, path) {
		return triYes
	}

	ranges := e.gcpSetAt(firewall, path, "")
	if ranges.empty() {
		return triYes
	}

	return gcpRanges(&ranges, check.target.addrs, point)
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
	if gcpUnknownField(firewall, path+".protocol", point) {
		return triMaybe
	}

	protocol, _ := attrString(firewall, path+".protocol")
	if matchProtocol(protocol, query) == triNo {
		return triNo
	}

	if gcpUnknownField(firewall, path+".ports", point) {
		return triMaybe
	}

	ports := attrStrings(firewall, path+".ports")
	if len(ports) == 0 {
		return triYes
	}

	return anyPortSpec(ports, query.Port, point)
}

// gcpUnknownField reports whether the attribute is unknown until apply, and records the gap when it is.
func gcpUnknownField(firewall *plan.Resource, path string, point *side) bool {
	if !unknownAt(firewall, path) {
		return false
	}

	point.gaps = append(point.gaps, gcpUnknownGap(firewall, path))

	return true
}
