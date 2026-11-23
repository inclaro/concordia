package cloudfw

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// awsDirection is one direction of security group rules: the inline block, the value of `type` of an
// aws_security_group_rule, and the type of the VPC rule resource.
type awsDirection struct {
	block, vpcType string
}

var (
	awsIngress = awsDirection{block: "ingress", vpcType: "aws_vpc_security_group_ingress_rule"}
	awsEgress  = awsDirection{block: "egress", vpcType: "aws_vpc_security_group_egress_rule"}
)

// awsGroupRule is the type of the standalone rule resource that has a `type` of ingress or egress.
const awsGroupRule = "aws_security_group_rule"

// awsFields are the attribute names of one rule source.
type awsFields struct {
	protocol, groups, cidr4, cidr6, prefixLists string
}

var (
	awsInlineFields = awsFields{
		protocol: "protocol", groups: "security_groups", cidr4: "cidr_blocks", cidr6: "ipv6_cidr_blocks",
		prefixLists: "prefix_list_ids",
	}
	awsGroupRuleFields = awsFields{
		protocol: "protocol", groups: "source_security_group_id", cidr4: "cidr_blocks", cidr6: "ipv6_cidr_blocks",
		prefixLists: "prefix_list_ids",
	}
	awsVPCRuleFields = awsFields{
		protocol: "ip_protocol", groups: "referenced_security_group_id", cidr4: "cidr_ipv4", cidr6: "cidr_ipv6",
		prefixLists: "prefix_list_id",
	}
)

// awsRule is one rule that may apply to an endpoint in one direction.
type awsRule struct {
	res    *plan.Resource
	fields *awsFields
	owner  *plan.Resource
	// path is the prefix of the attribute paths: `ingress[0].` for an inline rule, empty for a rule resource.
	path string
	name string
	// gaps explain an owner group that does not resolve; applies is then maybe.
	gaps    []assurance.Gap
	applies tri
}

func (r *awsRule) attr(field string) string { return r.path + field }

// awsSide is the check of one endpoint in one direction against the peer: the union of the rules of all groups of
// the endpoint. A group without a rule for the direction allows nothing; a group whose block list for the direction
// is unknown until apply makes a side that no rule allows unknown.
func (e *Evaluator) awsSide(q *Query, self, peer *awsEndpoint, dir *awsDirection) *side {
	var yes, maybe side

	verdict := triNo

	for _, rule := range e.awsRules(self, dir) {
		scratch := &side{gaps: rule.gaps}
		t := and(rule.applies, e.awsMatch(q, &rule, peer, scratch))
		verdict = or(verdict, t)

		switch t {
		case triYes:
			yes.rule(rule.res.Address, rule.name)
		case triMaybe:
			maybe.rule(rule.res.Address, rule.name)
			maybe.gaps = append(maybe.gaps, scratch.gaps...)
		}
	}

	return awsDecided(verdict, &yes, &maybe)
}

// awsDecided is the side with the verdict and the rules (and gaps) that gave it.
func awsDecided(verdict tri, yes, maybe *side) *side {
	switch verdict {
	case triYes:
		return &side{verdict: verdict, rules: yes.rules}
	case triMaybe:
		return &side{verdict: verdict, rules: maybe.rules, gaps: maybe.gaps}
	}

	return &side{verdict: verdict}
}

// awsRules lists the rules of the direction that may apply to the endpoint: the inline blocks of its groups, and
// the rule resources whose owner group is one of its groups or does not resolve.
func (e *Evaluator) awsRules(ep *awsEndpoint, dir *awsDirection) []awsRule {
	var rules []awsRule

	for _, group := range ep.groups {
		rules = append(rules, awsInlineRules(group, dir)...)
	}

	for _, res := range e.ofType(awsGroupRule, dir.vpcType) {
		if rule, ok := e.awsStandaloneRule(ep, res, dir); ok {
			rules = append(rules, rule)
		}
	}

	return rules
}

// awsInlineRules are the inline blocks of the direction of a group. A block list that is unknown until apply (a
// dynamic block, or the computed copy of the rule resources) may hold a rule that allows the traffic: it is one rule
// that applies maybe, without fields, with the gap unknown-after-apply.
func awsInlineRules(group *plan.Resource, dir *awsDirection) []awsRule {
	if slices.Contains(group.Unknown, dir.block) {
		return []awsRule{{
			res: group, owner: group, name: dir.block, applies: triMaybe,
			gaps: []assurance.Gap{{Kind: evaluate.GapUnknownAfterApply, Detail: group.Address + "." + dir.block}},
		}}
	}

	count := blockCount(group, dir.block)
	rules := make([]awsRule, 0, count)

	for i := range count {
		name := fmt.Sprintf("%s[%d]", dir.block, i)
		rules = append(rules, awsRule{
			res: group, fields: &awsInlineFields, owner: group, path: name + ".", name: name, applies: triYes,
		})
	}

	return rules
}

// awsStandaloneRule reads a rule resource of the direction. ok is false when it does not apply to the endpoint.
func (e *Evaluator) awsStandaloneRule(ep *awsEndpoint, res *plan.Resource, dir *awsDirection) (awsRule, bool) {
	fields := &awsVPCRuleFields
	if res.Type == awsGroupRule {
		if kind, _ := attrString(res, "type"); kind != dir.block {
			return awsRule{}, false
		}

		fields = &awsGroupRuleFields
	}

	rule := awsRule{res: res, fields: fields, name: "rule"}
	s := &side{}

	owner, ok := e.ref(res, "security_group_id", s)

	switch {
	case !ok:
		rule.applies, rule.gaps = triMaybe, s.gaps
	case owner != nil && ep.hasGroup(owner):
		rule.applies, rule.owner = triYes, owner
	default:
		return awsRule{}, false
	}

	return rule, true
}

// awsMatch matches one rule against the query and the peer endpoint. An unknown block list has no fields to match:
// it may allow anything.
func (e *Evaluator) awsMatch(q *Query, rule *awsRule, peer *awsEndpoint, s *side) tri {
	if rule.fields == nil {
		return triMaybe
	}

	traffic := awsTraffic(q, rule, s)
	if traffic == triNo {
		return triNo
	}

	return and(traffic, e.awsPeer(rule, peer, s))
}

// awsTraffic matches the protocol and the port range of a rule.
func awsTraffic(q *Query, rule *awsRule, s *side) tri {
	protocol, ok := attrString(rule.res, rule.attr(rule.fields.protocol))
	if !ok {
		return awsUnknownAttr(rule, rule.fields.protocol, s)
	}

	if matchProtocol(protocol, q) == triNo {
		return triNo
	}

	if normProtocol(protocol) == protocolAny {
		return triYes
	}

	return awsPorts(q, rule, protocol, s)
}

// awsPorts matches the destination port of the query against the port range of the rule.
func awsPorts(q *Query, rule *awsRule, protocol string, s *side) tri {
	from, fromOK := attrInt(rule.res, rule.attr("from_port"))
	to, toOK := attrInt(rule.res, rule.attr("to_port"))

	if !fromOK || !toOK {
		return or(awsUnknownAttr(rule, "from_port", s), awsUnknownAttr(rule, "to_port", s))
	}

	return matchPortRange(protocol, from, to, q.Port)
}

// awsUnknownAttr is maybe, with a gap, when the attribute of the rule is unknown until apply, and no when it is
// absent.
func awsUnknownAttr(rule *awsRule, field string, s *side) tri {
	path := rule.attr(field)
	if !unknownAt(rule.res, path) {
		return triNo
	}

	s.gap(evaluate.GapUnknownAfterApply, rule.res.Address+"."+planPath(path))

	return triMaybe
}

// awsPeer matches the peer endpoint against the sources (ingress) or destinations (egress) of a rule: a referenced
// group, `self`, a CIDR, or a prefix list.
func (e *Evaluator) awsPeer(rule *awsRule, peer *awsEndpoint, s *side) tri {
	return or(
		or(e.awsGroupPeer(rule, peer, s), awsSelfPeer(rule, peer)),
		or(awsCIDRPeer(rule, peer, s), awsPrefixListPeer(rule, s)),
	)
}

// awsGroupPeer is yes when a group that the rule references is a group of the peer.
func (e *Evaluator) awsGroupPeer(rule *awsRule, peer *awsEndpoint, s *side) tri {
	groups, ok := e.refs(rule.res, rule.attr(rule.fields.groups), s)

	switch {
	case slices.ContainsFunc(groups, peer.hasGroup):
		return triYes
	case !ok:
		return triMaybe
	}

	return triNo
}

// awsSelfPeer is yes when the rule names its own group and that group is a group of the peer.
func awsSelfPeer(rule *awsRule, peer *awsEndpoint) tri {
	// A rule whose owner does not resolve has no owner, which no peer has as a group.
	if attrBool(rule.res, rule.attr("self")) && peer.hasGroup(rule.owner) {
		return triYes
	}

	return triNo
}

// awsCIDRPeer matches the IPv4 and IPv6 ranges of the rule against the addresses of the peer.
func awsCIDRPeer(rule *awsRule, peer *awsEndpoint, s *side) tri {
	ranges := slices.Concat(
		attrStrings(rule.res, rule.attr(rule.fields.cidr4)), attrStrings(rule.res, rule.attr(rule.fields.cidr6)),
	)
	unknown := or(awsUnknownAttr(rule, rule.fields.cidr4, s), awsUnknownAttr(rule, rule.fields.cidr6, s))

	return or(anyPrefix(ranges, peer.addrs, s), unknown)
}

// awsPrefixListPeer is maybe, with a gap, when the rule names a prefix list: the evaluator does not read the entries
// of prefix lists.
func awsPrefixListPeer(rule *awsRule, s *side) tri {
	path := rule.attr(rule.fields.prefixLists)
	if len(attrStrings(rule.res, path)) == 0 && !unknownAt(rule.res, path) {
		return triNo
	}

	s.gap(evaluate.GapUnsupportedKind, "prefix list")

	return triMaybe
}
