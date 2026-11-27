package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/render"
)

// The network dimension of `require.effectiveAccess` for rendered workloads: the network-policy evaluator, the ports
// that a rule queries, and the answers of the queries.

// protocolOf gives the protocol of a port, TCP when none is written.
func protocolOf(protocol string) string {
	if protocol == "" {
		return netpol.ProtocolTCP
	}

	return strings.ToUpper(protocol)
}

// portKey is a destination port of a query.
type portKey struct {
	protocol string
	number   int
}

func (p portKey) String() string { return strings.ToLower(p.protocol) + "/" + strconv.Itoa(p.number) }

// maxPortNumber is the highest TCP, UDP, or SCTP port number.
const maxPortNumber = 65535

// portSet collects the ports that a rule without ports queries.
type portSet struct {
	seen map[portKey]bool
	// named are the port numbers that a container or a policy rule names.
	named  map[int]bool
	ranges [][2]int
}

func (s *portSet) add(protocol string, number int) {
	if number >= 1 && number <= maxPortNumber {
		s.seen[portKey{protocolOf(protocol), number}] = true
		s.named[number] = true
	}
}

// addRange adds both ends of a port range and the range itself. A range of one port is the port.
func (s *portSet) addRange(protocol string, from, to int) {
	s.add(protocol, from)

	if to != from {
		s.add(protocol, to)
		s.ranges = append(s.ranges, [2]int{from, to})
	}
}

// collect adds the numbers and the ranges of the rule ports. A named port is resolved against the container ports by
// the evaluator, so it adds nothing here.
func (s *portSet) collect(ports []netpol.Port) {
	for _, port := range ports {
		if port.Port == nil || port.Port.IsName {
			continue
		}

		end := port.Port.Number
		if port.EndPort != nil {
			end = *port.EndPort
		}

		s.addRange(port.Protocol, port.Port.Number, end)
	}
}

// destinationPorts gives the ports to query when the rule names none: the ports that the destination declares, every
// number that a policy rule names (the ends of ranges included), and one port that no rule names, which stands for
// "every other port". Without the last one a rule that allows all ports would not be seen when the destination
// declares none.
func destinationPorts(input *netpol.Input, destination *netpol.Workload) []portKey {
	set := &portSet{seen: map[portKey]bool{}, named: map[int]bool{}}

	for _, port := range destination.Ports {
		set.add(port.Protocol, port.ContainerPort)
	}

	for i := range input.Policies {
		set.collectPolicy(&input.Policies[i])
	}

	set.addUnnamedProbe()

	return set.sorted()
}

// collectPolicy adds the ports that the ingress and egress rules of a policy name.
func (s *portSet) collectPolicy(np *netpol.NetworkPolicy) {
	for _, rule := range np.Spec.Ingress {
		s.collect(rule.Ports)
	}

	for _, rule := range np.Spec.Egress {
		s.collect(rule.Ports)
	}
}

// addUnnamedProbe adds the lowest TCP port that no rule names and no range covers.
func (s *portSet) addUnnamedProbe() {
	s.addUnnamedProbeFor(netpol.ProtocolTCP)
}

// addUnnamedProbeFor adds the lowest port of the protocol that no rule names and no range covers.
func (s *portSet) addUnnamedProbeFor(protocol string) {
	if number, ok := s.firstUncovered(); ok {
		s.seen[portKey{protocol, number}] = true
	}
}

// firstUncovered gives the lowest port that no rule names and no range covers, and false when every port is covered.
// It walks the named ports and the ranges sorted by their first port, so its cost depends on the number of ranges,
// not on the number of ports.
func (s *portSet) firstUncovered() (int, bool) {
	spans := slices.Clone(s.ranges)
	for number := range s.named {
		spans = append(spans, [2]int{number, number})
	}

	slices.SortFunc(spans, func(a, b [2]int) int { return cmp.Compare(a[0], b[0]) })

	candidate := 1

	for _, span := range spans {
		if span[0] > candidate {
			break
		}

		candidate = max(candidate, span[1]+1)
	}

	return candidate, candidate <= maxPortNumber
}

// sorted lists the collected ports by protocol, then number.
func (s *portSet) sorted() []portKey {
	out := make([]portKey, 0, len(s.seen))
	for key := range s.seen {
		out = append(out, key)
	}

	slices.SortFunc(out, func(a, b portKey) int {
		return cmp.Or(cmp.Compare(a.protocol, b.protocol), cmp.Compare(a.number, b.number))
	})

	return out
}

// workloadsOf gives the workloads among the resources that a CALM element is bound to.
func (a *accessAsk) workloadsOf(calmID string) []*render.Resource {
	var out []*render.Resource

	for _, resource := range a.model.resourcesOf(a.run.bindings, calmID) {
		if resource.Workload != nil {
			out = append(out, resource)
		}
	}

	return out
}

// network answers the rule for the network-policy evaluator: for each pair of elements, each pair of their workloads,
// and each port. A rule with ports gives one outcome for each pair and port, so a denied port never hides an allowed
// one (spec: results are per pair and never aggregated away). A rule without ports gives one outcome for each pair,
// built from the queries of all ports that destinationPorts lists.
func (a *accessAsk) network(pairs [][2]string) []ExternalOutcome {
	input := a.model.netpolInput()
	evaluator := netpol.New(input)
	asker := &networkAsker{ask: a, input: input, evaluator: evaluator, digest: evaluator.InputDigest()}

	outcomes := make([]ExternalOutcome, 0, len(pairs))

	for _, pair := range pairs {
		outcomes = append(outcomes, asker.pair(pair)...)
	}

	return outcomes
}

// networkAsker holds the evaluator of one environment for the queries of one rule.
type networkAsker struct {
	ask       *accessAsk
	input     *netpol.Input
	evaluator *netpol.Evaluator
	digest    string
}

// pair asks the queries of one pair of elements and gives its outcomes.
func (n *networkAsker) pair(pair [2]string) []ExternalOutcome {
	sources := n.ask.workloadsOf(pair[0])
	if len(sources) == 0 {
		return []ExternalOutcome{n.ask.bindingGap(pair, unboundElement{pair[0], "a rendered workload"})}
	}

	destinations := n.ask.workloadsOf(pair[1])
	if len(destinations) == 0 {
		return []ExternalOutcome{n.ask.bindingGap(pair, unboundElement{pair[1], "a rendered workload"})}
	}

	return n.ask.portOutcomes(pair, n.probeAll(sources, destinations))
}

// portProbes holds the answers of the queries of one pair, grouped by port in the order the ports were first met.
type portProbes struct {
	byPort map[portKey][]probe
	order  []portKey
}

func (p *portProbes) add(port portKey, found *probe) {
	if _, seen := p.byPort[port]; !seen {
		p.order = append(p.order, port)
	}

	p.byPort[port] = append(p.byPort[port], *found)
}

// all lists the answers of every port, in the order of the ports.
func (p *portProbes) all() []probe {
	var all []probe
	for _, port := range p.order {
		all = append(all, p.byPort[port]...)
	}

	return all
}

// probeAll asks the query of each port for each pair of a source and a destination workload.
func (n *networkAsker) probeAll(sources, destinations []*render.Resource) *portProbes {
	probes := &portProbes{byPort: map[portKey][]probe{}}

	for _, destination := range destinations {
		ports := n.portsOf(destination)

		for _, source := range sources {
			for _, port := range ports {
				found := n.query(source, destination, port)
				probes.add(port, &found)
			}
		}
	}

	return probes
}

// portsOf gives the ports to query for a destination: the ports of the rule, else the ones that destinationPorts lists.
func (n *networkAsker) portsOf(destination *render.Resource) []portKey {
	if declared := n.ask.rulePorts(); len(declared) > 0 {
		return declared
	}

	return destinationPorts(n.input, &netpol.Workload{Ports: workloadPorts(n.input, destination)})
}

// rulePorts gives the TCP ports that the rule names, none when it names no port.
func (a *accessAsk) rulePorts() []portKey {
	declared := a.plan.Predicate.EffectiveAccess.Ports

	ports := make([]portKey, 0, len(declared))
	for _, number := range declared {
		ports = append(ports, portKey{netpol.ProtocolTCP, number})
	}

	return ports
}

// portOutcomes judges the answers of one pair: one outcome for the pair when the rule names no ports, else one for
// each port.
func (a *accessAsk) portOutcomes(pair [2]string, probes *portProbes) []ExternalOutcome {
	base := a.baseOutcome(pair)

	if len(a.plan.Predicate.EffectiveAccess.Ports) == 0 {
		base.Verdict, base.Reason, base.Detail = a.judge(probes.all())

		return []ExternalOutcome{base}
	}

	outcomes := make([]ExternalOutcome, 0, len(probes.order))

	for _, port := range probes.order {
		outcome := base
		outcome.Occurrence = port.String()
		outcome.Verdict, outcome.Reason, outcome.Detail = a.judge(probes.byPort[port])
		outcomes = append(outcomes, outcome)
	}

	return outcomes
}

// workloadKey identifies a workload of the evaluator input by namespace, name, and kind.
type workloadKey struct {
	namespace, name, kind string
}

// workloadPorts gives the declared container ports of a rendered workload from the evaluator input.
func workloadPorts(input *netpol.Input, resource *render.Resource) []netpol.ContainerPort {
	want := workloadKey{resource.ID.Namespace, resource.ID.Name, resource.ID.Kind}

	for i := range input.Workloads {
		w := &input.Workloads[i]
		if (workloadKey{w.Namespace, w.Name, w.Kind}) == want {
			return w.Ports
		}
	}

	return nil
}

// query asks the evaluator one question and records it.
func (n *networkAsker) query(source, destination *render.Resource, port portKey) probe {
	query := netpol.Query{
		From:     netpol.Endpoint{Namespace: source.ID.Namespace, Workload: source.ID.Name},
		To:       netpol.Endpoint{Namespace: destination.ID.Namespace, Workload: destination.ID.Name},
		Protocol: port.protocol, Port: port.number,
	}
	result := n.evaluator.Evaluate(&query)

	text := fmt.Sprintf("%s from %s to %s", port, resourceName(&source.ID), resourceName(&destination.ID))
	policies := policyNames(result.Policies)

	n.ask.run.recordEvaluation(n.ask.entryOf(&answerFacts{
		query: text, verdict: string(result.Verdict), reason: result.Reason, policies: slices.Clone(policies),
		assumptions: result.Assumptions, limits: result.Limits, gaps: result.Gaps,
		evaluator: result.Evaluator, version: result.Version, digest: n.digest,
	}))

	found := probe{label: text, assumptions: result.Assumptions, evidence: networkEvidence(&result, policies)}

	switch result.Verdict {
	case netpol.Allowed:
		found.state = stateOpen
	case netpol.Denied:
		found.state = stateClosed
	case netpol.Unknown:
		found.state, found.reason = stateUnknown, reasonOf(result.Reason)
		found.evidence = gapText(result.Gaps)
	}

	return found
}

// policyNames gives `Kind namespace/name (direction)` for each policy.
func policyNames(refs []netpol.PolicyRef) []string {
	out := make([]string, 0, len(refs))

	for _, ref := range refs {
		name := ref.Name
		if ref.Namespace != "" {
			name = ref.Namespace + "/" + ref.Name
		}

		out = append(out, fmt.Sprintf("%s %s (%s)", ref.Kind, name, ref.Direction))
	}

	return out
}

// networkEvidence names what decided an answer: the policies, or the fact that no policy selects an end for a
// direction (which leaves it open).
func networkEvidence(result *netpol.Result, policies []string) string {
	var open []string

	for _, d := range []*netpol.DirectionResult{result.Egress, result.Ingress} {
		if d != nil && d.Open {
			open = append(open, d.Direction)
		}
	}

	parts := slices.Clone(policies)
	if len(open) > 0 {
		parts = append(parts, "no policy selects the workload for "+strings.Join(open, " and "))
	}

	return strings.Join(parts, ", ")
}
