package cloudfw

import (
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// azureFixedPrefixes are the prefix values whose match does not depend on the endpoints. Both endpoints are private
// addresses in a virtual network, so `Internet` and `AzureLoadBalancer` never match them.
var azureFixedPrefixes = map[string]tri{
	"*":                 triYes,
	"internet":          triNo,
	"azureloadbalancer": triNo,
}

// azurePeerFields are the attributes of the source or the destination of a rule. destination selects the endpoint
// that they match: the destination of the query, else its source.
type azurePeerFields struct {
	prefix, prefixes, groups string
	destination              bool
}

var (
	azureSourceFields = azurePeerFields{
		prefix: "source_address_prefix", prefixes: "source_address_prefixes",
		groups: "source_application_security_group_ids",
	}
	azureDestinationFields = azurePeerFields{
		prefix: "destination_address_prefix", prefixes: "destination_address_prefixes",
		groups: "destination_application_security_group_ids", destination: true,
	}
)

// azureMatch matches one rule against the query: protocol, destination port, source, and destination. The gaps of a
// rule go to s only when the rule may match, because a rule that does not match cannot change the result.
func (e *Evaluator) azureMatch(rule *azureRule, pair *azurePair, s *side) tri {
	scratch := &side{gaps: append([]assurance.Gap(nil), rule.fixedGaps...)}
	checks := []func() tri{
		func() tri { return rule.fixed },
		func() tri { return rule.protocol(pair.q, scratch) },
		func() tri { return rule.port(pair.q.Port, scratch) },
		func() tri { return e.azurePeer(rule, &azureSourceFields, pair, scratch) },
		func() tri { return e.azurePeer(rule, &azureDestinationFields, pair, scratch) },
	}

	result := triYes
	for _, check := range checks {
		if result = and(result, check()); result == triNo {
			return triNo
		}
	}

	if result == triMaybe {
		s.gaps = append(s.gaps, scratch.gaps...)
	}

	return result
}

// unknownField reports, with a gap, whether one of the fields of the rule is unknown until apply.
func (r *azureRule) unknownField(s *side, fields ...string) bool {
	for _, field := range fields {
		if unknownAt(r.res, r.base+field) {
			s.gap(evaluate.GapUnknownAfterApply, r.res.Address+"."+planPath(r.base+field))

			return true
		}
	}

	return false
}

func (r *azureRule) protocol(q *Query, s *side) tri {
	if r.unknownField(s, "protocol") {
		return triMaybe
	}

	return matchProtocol(r.text("protocol"), q)
}

// port matches the destination port against `destination_port_range` and `destination_port_ranges`.
func (r *azureRule) port(port int, s *side) tri {
	if r.unknownField(s, "destination_port_range", "destination_port_ranges") {
		return triMaybe
	}

	specs := append(attrStrings(r.res, r.base+"destination_port_range"), attrStrings(r.res, r.base+"destination_port_ranges")...)

	return anyPortSpec(specs, port, s)
}

// azurePeer matches one end of the rule (its prefixes and application security groups) against the endpoint.
func (e *Evaluator) azurePeer(rule *azureRule, fields *azurePeerFields, pair *azurePair, s *side) tri {
	if rule.unknownField(s, fields.prefix, fields.prefixes) {
		return triMaybe
	}

	ep := pair.from
	if fields.destination {
		ep = pair.to
	}

	result := triNo

	prefixes := append(attrStrings(rule.res, rule.base+fields.prefix), attrStrings(rule.res, rule.base+fields.prefixes)...)
	for _, value := range prefixes {
		result = or(result, azurePrefix(value, ep, pair, s))
	}

	return or(result, e.azureGroups(rule, fields.groups, ep, s))
}

// azureGroups matches the application security groups of one end of the rule against the endpoint.
func (e *Evaluator) azureGroups(rule *azureRule, path string, ep *azureEndpoint, s *side) tri {
	asgs, ok := e.refs(rule.res, rule.base+path, s)

	switch {
	case !ok:
		return triMaybe
	case len(asgs) == 0:
		return triNo
	}

	return azureInGroups(ep, asgs, s)
}

// azurePrefix matches one prefix value: `*`, a CIDR or IP, or a service tag.
func azurePrefix(value string, ep *azureEndpoint, pair *azurePair, s *side) tri {
	if fixed, ok := azureFixedPrefixes[strings.ToLower(value)]; ok {
		return fixed
	}

	if strings.EqualFold(value, "VirtualNetwork") {
		return azureSameVNet(pair, s)
	}

	if azureIsAddress(value) {
		return matchPrefix(value, ep.addrs, s)
	}

	s.gap(evaluate.GapUnsupportedKind, "service tag "+value)

	return triMaybe
}
