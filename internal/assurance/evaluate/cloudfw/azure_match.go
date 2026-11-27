package cloudfw

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
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

// azureMatch matches one rule against the query: protocol, destination port, source, destination, and source port.
// The gaps of a rule go to report only when the rule may match, because a rule that does not match cannot change the
// result.
func (e *Evaluator) azureMatch(rule *azureRule, pair *azurePair, report *side) tri {
	scratch := &side{gaps: append([]assurance.Gap(nil), rule.fixedGaps...)}
	checks := []func() tri{
		func() tri { return rule.fixed },
		func() tri { return rule.protocol(pair.q, scratch) },
		func() tri { return rule.port(pair.q.Port, scratch) },
		func() tri { return e.azurePeer(rule, &azureSourceFields, pair, scratch) },
		func() tri { return e.azurePeer(rule, &azureDestinationFields, pair, scratch) },
		func() tri { return rule.sourcePort(scratch) },
	}

	result := triYes
	for _, check := range checks {
		if result = and(result, check()); result == triNo {
			return triNo
		}
	}

	if result == triMaybe {
		report.gaps = append(report.gaps, scratch.gaps...)
	}

	return result
}

// unknownField reports, with a gap, whether one of the fields of the rule is unknown until apply.
func (r *azureRule) unknownField(report *side, fields ...string) bool {
	for _, field := range fields {
		if unknownAt(r.res, r.base+field) {
			report.gap(evaluate.GapUnknownAfterApply, r.res.Address+"."+planPath(r.base+field))

			return true
		}
	}

	return false
}

// sourcePort is maybe, with a gap, when the rule is limited to source ports: a query has no source port, so such a
// rule may or may not apply. An absent or empty value is every port.
func (r *azureRule) sourcePort(report *side) tri {
	if r.unknownField(report, "source_port_range", "source_port_ranges") {
		return triMaybe
	}

	specs := append(attrStrings(r.res, r.base+"source_port_range"), attrStrings(r.res, r.base+"source_port_ranges")...)
	if slices.ContainsFunc(specs, func(spec string) bool { return strings.TrimSpace(spec) != protocolAny }) {
		report.gap(evaluate.GapUnsupportedKind, "source port range")

		return triMaybe
	}

	return triYes
}

func (r *azureRule) protocol(q *Query, report *side) tri {
	if r.unknownField(report, azureProtocolAttr) {
		return triMaybe
	}

	return matchProtocol(r.text(azureProtocolAttr), q)
}

// port matches the destination port against `destination_port_range` and `destination_port_ranges`.
func (r *azureRule) port(port int, report *side) tri {
	if r.unknownField(report, "destination_port_range", "destination_port_ranges") {
		return triMaybe
	}

	specs := append(attrStrings(r.res, r.base+"destination_port_range"), attrStrings(r.res, r.base+"destination_port_ranges")...)

	return anyPortSpec(specs, port, report)
}

// azurePeer matches one end of the rule (its prefixes and application security groups) against the endpoint.
func (e *Evaluator) azurePeer(rule *azureRule, fields *azurePeerFields, pair *azurePair, report *side) tri {
	if rule.unknownField(report, fields.prefix, fields.prefixes) {
		return triMaybe
	}

	end := pair.from
	if fields.destination {
		end = pair.to
	}

	result := triNo

	prefixes := append(attrStrings(rule.res, rule.base+fields.prefix), attrStrings(rule.res, rule.base+fields.prefixes)...)
	for _, value := range prefixes {
		result = or(result, e.azurePrefix(value, end, pair, report))
	}

	return or(result, e.azureGroups(rule, fields.groups, end, report))
}

// azureGroups matches the application security groups of one end of the rule against the endpoint. An endpoint
// whose NIC is known to be in no ASG never matches, whichever ASGs the rule names.
func (e *Evaluator) azureGroups(rule *azureRule, path string, end *azureEndpoint, report *side) tri {
	if end.inNoASG() {
		return triNo
	}

	asgs, ok := e.azureRuleASGs(rule, path, report)

	switch {
	case !ok:
		return triMaybe
	case len(asgs) == 0:
		return triNo
	}

	return azureInGroups(end, asgs, report)
}

// inNoASG reports whether the endpoint has a NIC that is known to be in no application security group.
func (end *azureEndpoint) inNoASG() bool {
	return end.nic != nil && len(end.asgs) == 0 && len(end.asgGaps) == 0
}

// azureRuleASGs resolves the ASG list of a rule. The list of an inline rule resolves through the references of
// all inline rules, because the plan keeps the inline rules in set order and not in configuration order.
func (e *Evaluator) azureRuleASGs(rule *azureRule, path string, report *side) ([]*plan.Resource, bool) {
	if rule.inline {
		return e.blockRefsOfType(rule.res, blockElement{list: azureRulesAttr, field: path, index: rule.index}, report, azureASG)
	}

	return e.refsOfType(rule.res, rule.base+path, report, azureASG)
}

// azurePrefix matches one prefix value: `*`, a CIDR or IP, or a service tag.
func (e *Evaluator) azurePrefix(value string, end *azureEndpoint, pair *azurePair, report *side) tri {
	if fixed, ok := azureFixedPrefixes[strings.ToLower(value)]; ok {
		return fixed
	}

	if strings.EqualFold(value, azureVirtualNetwork) {
		return e.azureSameVNet(pair, report)
	}

	if azureIsAddress(value) {
		return matchPrefix(value, end.addrs, report)
	}

	report.gap(evaluate.GapUnsupportedKind, "service tag "+value)

	return triMaybe
}
