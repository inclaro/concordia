package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/workspace"
)

// A network rule asks cloud-firewall for each pair whose CALM elements are bound to planned resources of the
// environment (`infraResource` bindings), and network-policy for the pairs bound to rendered workloads. A pair that
// mixes the two has no evaluator: the network path between a cloud resource and a Kubernetes workload crosses the
// cluster network, which neither evaluator sees. The cloud pairs need a usable plan and no rendered resources.

// mixedPairDetail is the detail of the `binding-gap` of a pair that mixes a planned resource and a workload.
const mixedPairDetail = "the pair mixes planned cloud resources and Kubernetes workloads"

// splitCloud answers the cloud pairs and the mixed pairs of a network rule, and gives the other pairs back for the
// rendered evidence.
func (a *accessAsk) splitCloud(pairs [][2]string) (outcomes []ExternalOutcome, rest [][2]string) {
	var cloud [][2]string

	for _, pair := range pairs {
		switch isCloud, mixed := a.cloudPair(pair); {
		case mixed:
			outcomes = append(outcomes, a.mixedGap(pair))
		case isCloud:
			cloud = append(cloud, pair)
		default:
			rest = append(rest, pair)
		}
	}

	if len(cloud) > 0 {
		outcomes = append(outcomes, a.locatePlan(a.cloud(cloud))...)
	}

	return outcomes, rest
}

// locatePlan points the outcomes of the cloud pairs at the first declared plan file of the environment, the evidence
// that cloud-firewall reads. Without a declared plan the outcomes keep no location of their own.
func (a *accessAsk) locatePlan(outcomes []ExternalOutcome) []ExternalOutcome {
	plans := a.environmentPlans()
	if len(plans) == 0 {
		return outcomes
	}

	for i := range outcomes {
		outcomes[i].Location = &Location{Path: plans[0].Declared.File}
	}

	return outcomes
}

// cloudPair says whether the pair is for cloud-firewall (both ends are planned resources) or mixed (one end is a
// planned resource and the other a workload). A pair with one planned end and another end bound to neither goes to
// cloud-firewall, which reports the binding gap of that end.
func (a *accessAsk) cloudPair(pair [2]string) (cloud, mixed bool) {
	fromPlanned, toPlanned := len(a.plannedOf(pair[0])) > 0, len(a.plannedOf(pair[1])) > 0

	switch {
	case fromPlanned == toPlanned:
		return fromPlanned, false
	case fromPlanned:
		mixed = len(a.workloadsOf(pair[1])) > 0
	default:
		mixed = len(a.workloadsOf(pair[0])) > 0
	}

	return !mixed, mixed
}

// mixedGap is the `binding-gap` outcome of a mixed pair.
func (a *accessAsk) mixedGap(pair [2]string) ExternalOutcome {
	out := a.bindingGap(pair, unboundElement{calmID: pair[0]})
	out.Detail = mixedPairDetail

	return out
}

// environmentPlans gives the declared plans of the environment, usable or not.
func (a *accessAsk) environmentPlans() []*evidence.Plan {
	var out []*evidence.Plan

	for i := range a.run.ev.Plans {
		if declared := &a.run.ev.Plans[i]; declared.Declared.Environment == a.model.environment {
			out = append(out, declared)
		}
	}

	return out
}

// plannedOf gives the planned resources of the environment that a CALM element is bound to, sorted by address. An
// `infraResource` match names a resource by its address, or every instance of it by the address without the instance
// key. Bindings that are only proposed do not count.
func (a *accessAsk) plannedOf(calmID string) []*plan.Resource {
	var out []*plan.Resource

	for i := range a.run.bindings {
		b := &a.run.bindings[i]
		if b.ArchitectureRef != calmID || b.Status == binding.StatusProposed {
			continue
		}

		for j := range b.Match {
			out = append(out, a.plannedMatching(&b.Match[j])...)
		}
	}

	slices.SortFunc(out, func(x, y *plan.Resource) int { return canonjson.CompareUTF16(x.Address, y.Address) })

	return slices.CompactFunc(out, func(x, y *plan.Resource) bool { return x.Address == y.Address })
}

// plannedMatching gives the resources of the usable plans of the environment that an `infraResource` match names.
func (a *accessAsk) plannedMatching(match *binding.Match) []*plan.Resource {
	if match.Kind != binding.MatchInfraResource || match.Environment != a.model.environment {
		return nil
	}

	var out []*plan.Resource

	for _, declared := range a.environmentPlans() {
		if declared.Plan != nil {
			out = append(out, resourcesAt(declared.Plan, match)...)
		}
	}

	return out
}

// resourcesAt gives the resources of a plan at the address of the match, or whose address without the instance key
// is that address. A deposed object is not the resource.
func resourcesAt(p *plan.Plan, match *binding.Match) []*plan.Resource {
	var out []*plan.Resource

	for i := range p.Resources {
		r := &p.Resources[i]
		if r.Deposed != "" {
			continue
		}

		if r.Address == match.Address || plan.ConfigAddress(r.Address) == match.Address {
			out = append(out, r)
		}
	}

	return out
}

// cloud answers the cloud pairs for the cloud-firewall evaluator, after the gates of the evaluator declaration and
// the plan.
func (a *accessAsk) cloud(pairs [][2]string) []ExternalOutcome {
	scope := evaluatorScope{kind: workspace.EvaluatorCloudFirewall, environment: a.model.environment}

	declaration := evaluatorFor(a.run.cfg, scope)
	if declaration == nil {
		return a.unknownAll(pairs, unknownCause{ReasonNoEvaluatorAvailable, scope.noEvaluatorDetail()})
	}

	plans := a.environmentPlans()
	if !slices.ContainsFunc(plans, func(p *evidence.Plan) bool { return p.Plan != nil }) {
		return a.unknownAll(pairs, unknownCause{ReasonRenderedEvidenceMissing,
			fmt.Sprintf("environment %s has no usable plan (assurance.plans)", scope.environment)})
	}

	ask := *a
	ask.kind, ask.declaration = workspace.EvaluatorCloudFirewall, declaration
	asker := newCloudAsker(&ask, plans)

	outcomes := make([]ExternalOutcome, 0, len(pairs))
	for _, pair := range pairs {
		outcomes = append(outcomes, asker.pair(pair)...)
	}

	return outcomes
}

// cloudAsker holds the evaluator of one environment for the queries of one rule.
type cloudAsker struct {
	ask       *accessAsk
	evaluator *cloudfw.Evaluator
	integrity string
	// ports are the ports to query: the ports of the rule, else the ones that planPorts lists.
	ports []portKey
}

func newCloudAsker(ask *accessAsk, plans []*evidence.Plan) *cloudAsker {
	input := cloudInput(plans)

	ports := ask.rulePorts()
	if len(ports) == 0 {
		ports = planPorts(input.Resources)
	}

	return &cloudAsker{ask: ask, evaluator: cloudfw.New(input), integrity: lowestIntegrity(plans), ports: ports}
}

// cloudInput builds the evaluator input from the plans of the environment: the resources of the usable plans, the
// worst coverage of all of them (a plan that could not be read may hold rules), and the digest of the plan bytes.
func cloudInput(plans []*evidence.Plan) *cloudfw.Input {
	input := &cloudfw.Input{Digest: plansDigest(plans)}

	for _, declared := range plans {
		input.Coverage = worse(input.Coverage, declared.Coverage)

		if declared.Plan != nil {
			input.Resources = append(input.Resources, declared.Plan.Resources...)
		}
	}

	slices.SortStableFunc(input.Resources, func(x, y plan.Resource) int { return canonjson.CompareUTF16(x.Address, y.Address) })

	return input
}

// plansDigest is the digest of the one usable plan, or the digest of the sorted digests of more usable plans.
func plansDigest(plans []*evidence.Plan) string {
	var digests []string

	for _, declared := range plans {
		if declared.Plan != nil {
			digests = append(digests, declared.Plan.Digest)
		}
	}

	if len(digests) == 1 {
		return digests[0]
	}

	slices.Sort(digests)

	return evaluate.DigestOf(digests)
}

// lowestIntegrity is the text of the lowest integrity of the bindings of the usable plans. There is at least one.
func lowestIntegrity(plans []*evidence.Plan) string {
	var levels []assurance.Integrity

	for _, declared := range plans {
		if declared.Plan != nil {
			levels = append(levels, declared.Binding.Admission.Integrity)
		}
	}

	lowest := slices.MinFunc(levels, func(x, y assurance.Integrity) int { return cmp.Compare(x.Rank(), y.Rank()) })

	return lowest.Text()
}

// pair asks the queries of one pair of elements and gives its outcomes.
func (c *cloudAsker) pair(pair [2]string) []ExternalOutcome {
	sources := c.ask.plannedOf(pair[0])
	if len(sources) == 0 {
		return []ExternalOutcome{c.ask.bindingGap(pair, unboundElement{pair[0], "a planned resource"})}
	}

	destinations := c.ask.plannedOf(pair[1])
	if len(destinations) == 0 {
		return []ExternalOutcome{c.ask.bindingGap(pair, unboundElement{pair[1], "a planned resource"})}
	}

	probes := &portProbes{byPort: map[portKey][]probe{}}

	for _, destination := range destinations {
		for _, source := range sources {
			c.probePorts(probes, source, destination)
		}
	}

	return c.ask.portOutcomes(pair, probes)
}

// probePorts asks the query of each port for one source and one destination.
func (c *cloudAsker) probePorts(probes *portProbes, source, destination *plan.Resource) {
	for _, port := range c.ports {
		found := c.query(source, destination, port)
		probes.add(port, &found)
	}
}

// query asks the evaluator one question and records it.
func (c *cloudAsker) query(source, destination *plan.Resource, port portKey) probe {
	query := cloudfw.Query{
		From: cloudfw.Endpoint{Address: source.Address}, To: cloudfw.Endpoint{Address: destination.Address},
		Protocol: port.protocol, Port: port.number,
	}
	result := c.evaluator.Evaluate(&query)

	text := fmt.Sprintf("%s from %s to %s", port, source.Address, destination.Address)

	c.ask.run.recordEvaluation(c.ask.entryOf(&answerFacts{
		query: text, verdict: string(result.Verdict), reason: result.Reason, policies: ruleNames(result.Rules),
		assumptions: result.Assumptions, limits: result.Limits, gaps: result.Gaps,
		evaluator: cloudfw.Identifier, version: cloudfw.Version, digest: c.evaluator.InputDigest(), integrity: c.integrity,
	}))

	return cloudProbe(text, &result)
}

// cloudProbe reduces a result of the evaluator to a probe. The evidence names the rules that decided it.
func cloudProbe(label string, result *cloudfw.Result) probe {
	found := probe{label: label, assumptions: result.Assumptions, evidence: strings.Join(ruleNames(result.Rules), ", ")}

	switch result.Verdict {
	case cloudfw.VerdictAllowed:
		found.state = stateOpen
	case cloudfw.VerdictDenied:
		found.state = stateClosed
		if found.evidence == "" {
			found.evidence = "no rule of the plan allows it"
		}
	case cloudfw.VerdictUnknown:
		found.state, found.reason, found.evidence = stateUnknown, reasonOf(result.Reason), gapText(result.Gaps)
	}

	return found
}

// ruleNames gives `<address> <rule>` for each rule.
func ruleNames(refs []cloudfw.RuleRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.Address+" "+ref.Rule)
	}

	return out
}

// firewallRuleTypes are the resource types whose values name the ports of firewall rules.
var firewallRuleTypes = []string{
	"aws_security_group", "aws_security_group_rule", "aws_vpc_security_group_ingress_rule",
	"aws_vpc_security_group_egress_rule", "azurerm_network_security_group", "azurerm_network_security_rule",
	"google_compute_firewall",
}

// planPorts gives the TCP ports to query when the rule names none: every number that a firewall rule of the plans
// names (the ends of ranges included), and one port that no rule names, which stands for "every other port".
func planPorts(resources []plan.Resource) []portKey {
	set := &portSet{seen: map[portKey]bool{}, named: map[int]bool{}}

	for i := range resources {
		if slices.Contains(firewallRuleTypes, resources[i].Type) {
			set.collectValue(resources[i].Values)
		}
	}

	set.addUnnamedProbe()

	return set.sorted()
}

// collectValue adds the ports of every rule object in a planned value: the resource itself, and its nested blocks.
func (s *portSet) collectValue(value any) {
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

// collectRule adds the ports of one rule object: an AWS `from_port` to `to_port` range, Azure destination port
// ranges, and GCP `ports`.
func (s *portSet) collectRule(rule map[string]any) {
	from, fromKnown := portNumber(rule["from_port"])
	to, toKnown := portNumber(rule["to_port"])

	if fromKnown && toKnown {
		s.addRange(netpol.ProtocolTCP, from, to)
	}

	for _, key := range []string{"destination_port_range", "destination_port_ranges", "ports"} {
		s.collectSpecs(rule[key])
	}
}

// collectSpecs adds the ports of a port text or a list of them.
func (s *portSet) collectSpecs(value any) {
	switch typed := value.(type) {
	case string:
		s.collectSpec(typed)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				s.collectSpec(text)
			}
		}
	}
}

// collectSpec adds a port text: `80` or `1000-2000`. `*` names no port.
func (s *portSet) collectSpec(spec string) {
	low, high, isRange := strings.Cut(strings.TrimSpace(spec), "-")

	from, err := strconv.Atoi(low)
	if err != nil {
		return
	}

	to := from
	if isRange {
		if to, err = strconv.Atoi(high); err != nil {
			return
		}
	}

	s.addRange(netpol.ProtocolTCP, from, to)
}

// portNumber reads a port number of a planned value.
func portNumber(value any) (int, bool) {
	if value == nil {
		return 0, false
	}

	n, err := strconv.Atoi(fmt.Sprint(value))

	return n, err == nil
}
