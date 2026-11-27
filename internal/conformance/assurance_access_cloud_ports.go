package conformance

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// A cloud-firewall rule without ports queries, for each protocol that a firewall rule of the plans names, every port
// at which a rule of that protocol starts or stops to match: for every port and range of a rule, both ends and the
// ports next to them, plus one port inside the range, and one port that no rule of the protocol names. The answer of
// the evaluator depends on the port only through these ranges, so every class of ports that the rules treat alike
// has a query: the first port of each class is a start, a port after an end, or the lowest port that no rule names,
// and the last port of the class before it is an end or a port before a start. A rule that opens every port of a
// protocol (`*`, a GCP block without ports, AWS `-1`) names no range, so its protocol still gets the port that no
// rule names. Each protocol keeps its own ports and ranges: a UDP range never hides a TCP port. A plan with Azure
// resources also queries UDP: the Azure default rules, which are not in the plan, allow every protocol inside a
// virtual network and out to the internet, and an endpoint without an NSG allows every protocol.

// firewallRuleTypes are the resource types whose values name the ports of firewall rules.
var firewallRuleTypes = []string{
	"aws_security_group", "aws_default_security_group", "aws_security_group_rule", "aws_vpc_security_group_ingress_rule",
	"aws_vpc_security_group_egress_rule", "azurerm_network_security_group", "azurerm_network_security_rule",
	"google_compute_firewall",
}

// azureTypePrefix is the type prefix of the Azure resources, whose default rules open UDP as well as TCP.
const azureTypePrefix = "azurerm_"

// Attributes of a rule object that name its protocol.
const (
	protocolKey   = "protocol"
	ipProtocolKey = "ip_protocol"
)

// ruleKeys are the attributes that make an object of a planned value a firewall rule: its protocol or its ports.
var ruleKeys = []string{
	protocolKey, ipProtocolKey, "from_port", "to_port", "destination_port_range", "destination_port_ranges", "ports",
}

// portSpecKeys are the attributes that hold port texts: Azure destination ports and GCP `ports`.
var portSpecKeys = []string{"destination_port_range", "destination_port_ranges", "ports"}

// probeProtocols are the protocols that a query can ask about.
var probeProtocols = []string{netpol.ProtocolTCP, netpol.ProtocolUDP}

// cloudPortSet collects the ports to query from the firewall rules of the plans, with one port set for each
// protocol, so that the ports and ranges of one protocol never cover the ports of another.
type cloudPortSet struct {
	// byProtocol holds the ports of each protocol that a rule of the plans names; TCP is always queried.
	byProtocol map[string]*portSet
}

// planPorts gives the ports to query when the rule names none (see the comment at the top of the file).
func planPorts(resources []plan.Resource) []portKey {
	set := &cloudPortSet{byProtocol: map[string]*portSet{}}
	set.of(netpol.ProtocolTCP)

	if slices.ContainsFunc(resources, func(r plan.Resource) bool { return strings.HasPrefix(r.Type, azureTypePrefix) }) {
		set.of(netpol.ProtocolUDP)
	}

	set.collectResources(resources)

	var out []portKey

	for _, protocol := range probeProtocols {
		if ports, used := set.byProtocol[protocol]; used {
			ports.addUnnamedProbeFor(protocol)
			out = append(out, ports.sorted()...)
		}
	}

	return out
}

// of is the port set of the protocol, made when the protocol is first met.
func (s *cloudPortSet) of(protocol string) *portSet {
	ports, ok := s.byProtocol[protocol]
	if !ok {
		ports = &portSet{seen: map[portKey]bool{}, named: map[int]bool{}}
		s.byProtocol[protocol] = ports
	}

	return ports
}

// collectResources adds the samples of the firewall rule resources.
func (s *cloudPortSet) collectResources(resources []plan.Resource) {
	for i := range resources {
		if slices.Contains(firewallRuleTypes, resources[i].Type) {
			s.collectValue(resources[i].Values)
		}
	}
}

// collectValue adds the ports of every rule object in a planned value: the resource itself, and its nested blocks.
func (s *cloudPortSet) collectValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		s.collectRule(typed)

		for _, nested := range typed {
			s.collectValue(nested)
		}
	case []any:
		for _, nested := range typed {
			s.collectValue(nested)
		}
	}
}

// collectRule adds the samples of one rule object for each protocol that the rule applies to.
func (s *cloudPortSet) collectRule(rule map[string]any) {
	if !slices.ContainsFunc(ruleKeys, func(key string) bool { return rule[key] != nil }) {
		return
	}

	protocols := ruleProtocols(rule)
	ranges := ruleRanges(rule)

	for _, protocol := range protocols {
		ports := s.of(protocol)

		for _, r := range ranges {
			addSamples(ports, protocol, r)
		}
	}
}

// addSamples adds the ends of a range, the ports next to them, and one port inside it.
func addSamples(ports *portSet, protocol string, r [2]int) {
	from, to := r[0], r[1]

	ports.addRange(protocol, from, to)
	ports.add(protocol, from-1)
	ports.add(protocol, to+1)

	if to-from >= 2 {
		ports.add(protocol, from+(to-from)/2)
	}
}

// ruleProtocols gives the protocols that a rule applies to. A protocol that is absent or unknown, or that names every
// protocol (`-1`, `all`, `*`), applies to TCP and UDP; ICMP and the other protocols apply to neither.
func ruleProtocols(rule map[string]any) []string {
	value := rule[protocolKey]
	if value == nil {
		value = rule[ipProtocolKey]
	}

	if value == nil {
		return probeProtocols
	}

	switch strings.ToLower(fmt.Sprint(value)) {
	case "tcp", "6":
		return []string{netpol.ProtocolTCP}
	case "udp", "17":
		return []string{netpol.ProtocolUDP}
	case "-1", "all", "*":
		return probeProtocols
	}

	return nil
}

// ruleRanges gives the port ranges of a rule object: an AWS `from_port` to `to_port` range, Azure destination port
// ranges, and GCP `ports`. `*` names no range.
func ruleRanges(rule map[string]any) [][2]int {
	var out [][2]int

	from, fromKnown := portNumber(rule["from_port"])
	to, toKnown := portNumber(rule["to_port"])

	if fromKnown && toKnown {
		out = append(out, [2]int{from, to})
	}

	for _, key := range portSpecKeys {
		out = append(out, specRanges(rule[key])...)
	}

	return out
}

// specRanges gives the ranges of a port text or a list of them.
func specRanges(value any) [][2]int {
	texts := specTexts(value)
	out := make([][2]int, 0, len(texts))

	for _, text := range texts {
		if r, ok := parsePortRange(text); ok {
			out = append(out, r)
		}
	}

	return out
}

// specTexts gives the port texts of a value: one text, or the texts of a list.
func specTexts(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		texts := make([]string, 0, len(typed))

		for _, item := range typed {
			if text, ok := item.(string); ok {
				texts = append(texts, text)
			}
		}

		return texts
	}

	return nil
}

// parsePortRange reads a port text as the evaluator does: `80`, `1000-2000`, or `1000 - 2000`.
func parsePortRange(spec string) ([2]int, bool) {
	low, high, isRange := strings.Cut(spec, "-")

	from, err := strconv.Atoi(strings.TrimSpace(low))
	if err != nil {
		return [2]int{}, false
	}

	if !isRange {
		return [2]int{from, from}, true
	}

	to, err := strconv.Atoi(strings.TrimSpace(high))

	return [2]int{from, to}, err == nil
}

// portNumber reads a port number of a planned value.
func portNumber(value any) (int, bool) {
	if value == nil {
		return 0, false
	}

	n, err := strconv.Atoi(fmt.Sprint(value))

	return n, err == nil
}
