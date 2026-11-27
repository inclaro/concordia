package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/workspace"
)

// A network rule asks cloud-firewall for each pair whose CALM elements are bound to planned resources of the
// environment (`infraResource` bindings), and network-policy for the pairs bound to rendered workloads. A pair that
// mixes the two has no evaluator: the network path between a cloud resource and a Kubernetes workload crosses the
// cluster network, which neither evaluator sees. The cloud pairs need a usable plan and no rendered resources. The
// precedence for an element bound both ways: both ends planned gives cloud-firewall; else both ends workloads gives
// network-policy, because the path stays inside the cluster; else the pair is mixed.

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

// locatePlan points each outcome of the cloud pairs at a plan file of the environment: the usable plan that holds a
// bound resource of the target, else of the subject, else the first usable plan, else the first declared plan.
// Without a declared plan the outcomes keep no location of their own.
func (a *accessAsk) locatePlan(outcomes []ExternalOutcome) []ExternalOutcome {
	plans := a.environmentPlans()
	if len(plans) == 0 {
		return outcomes
	}

	for i := range outcomes {
		outcomes[i].Location = &Location{Path: a.planFileOf(&outcomes[i], plans)}
	}

	return outcomes
}

// planFileOf is the plan file that the evidence of an outcome comes from (see locatePlan).
func (a *accessAsk) planFileOf(outcome *ExternalOutcome, plans []*evidence.Plan) string {
	for _, calmID := range []string{outcome.Target, outcome.Subject} {
		for _, address := range a.endpointsOf(calmID) {
			if declared := planHolding(plans, address); declared != nil {
				return declared.Declared.File
			}
		}
	}

	if i := slices.IndexFunc(plans, func(p *evidence.Plan) bool { return p.Plan != nil }); i >= 0 {
		return plans[i].Declared.File
	}

	return plans[0].Declared.File
}

// planHolding is the first usable plan that holds a live resource at the address, or nil.
func planHolding(plans []*evidence.Plan, address string) *evidence.Plan {
	for _, declared := range plans {
		if declared.Plan != nil && slices.ContainsFunc(declared.Plan.Resources, func(r plan.Resource) bool {
			return r.Address == address && isLive(&r)
		}) {
			return declared
		}
	}

	return nil
}

// isLive reports whether a planned resource exists after apply: it is not a deposed object and the plan does not
// destroy it (a destroyed instance has no planned values). The evaluator reads only these.
func isLive(r *plan.Resource) bool {
	return r.Deposed == "" && r.Values != nil
}

// cloudPair says whether the pair is for cloud-firewall or mixed (see the precedence at the top of the file). A pair
// with one planned end and another end bound to neither goes to cloud-firewall, which reports the binding gap of
// that end. The binding decides, not the plan: a missing plan or a bound address that the plan does not hold is the
// answer of cloud-firewall.
func (a *accessAsk) cloudPair(pair [2]string) (cloud, mixed bool) {
	fromPlanned, toPlanned := len(a.infraMatches(pair[0])) > 0, len(a.infraMatches(pair[1])) > 0

	switch planned := btoi(fromPlanned) + btoi(toPlanned); {
	case planned == 2:
		return true, false
	case planned == 0 || a.bothWorkloads(pair):
		return false, false
	}

	other := pair[0]
	if fromPlanned {
		other = pair[1]
	}

	mixed = len(a.workloadsOf(other)) > 0

	return !mixed, mixed
}

// bothWorkloads reports whether both elements of the pair are bound to rendered workloads.
func (a *accessAsk) bothWorkloads(pair [2]string) bool {
	return len(a.workloadsOf(pair[0])) > 0 && len(a.workloadsOf(pair[1])) > 0
}

// btoi is 1 for true and 0 for false.
func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
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

// infraMatches gives the `infraResource` matches of the environment that bind a CALM element. Bindings that are only
// proposed do not count.
func (a *accessAsk) infraMatches(calmID string) []*binding.Match {
	var out []*binding.Match

	for i := range a.run.bindings {
		if b := &a.run.bindings[i]; b.ArchitectureRef == calmID && b.Status != binding.StatusProposed {
			out = append(out, a.infraMatchesOf(b)...)
		}
	}

	return out
}

// infraMatchesOf gives the `infraResource` matches of one binding that name the environment.
func (a *accessAsk) infraMatchesOf(b *binding.Binding) []*binding.Match {
	var out []*binding.Match

	for j := range b.Match {
		if match := &b.Match[j]; match.Kind == binding.MatchInfraResource && match.Environment == a.model.environment {
			out = append(out, match)
		}
	}

	return out
}

// endpointsOf gives the plan addresses that a CALM element is bound to, sorted. An `infraResource` match names a
// resource by its address, or every instance of it by the address without the instance key. A match that no resource
// of the usable plans has stays as written, so that the evaluator reports the address as not found.
func (a *accessAsk) endpointsOf(calmID string) []string {
	var out []string

	for _, match := range a.infraMatches(calmID) {
		found := a.plannedMatching(match)
		if len(found) == 0 {
			out = append(out, match.Address)
		}

		for _, r := range found {
			out = append(out, r.Address)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}

// plannedMatching gives the resources of the usable plans of the environment that an `infraResource` match names.
func (a *accessAsk) plannedMatching(match *binding.Match) []*plan.Resource {
	var out []*plan.Resource

	for _, declared := range a.environmentPlans() {
		if declared.Plan != nil {
			out = append(out, resourcesAt(declared.Plan, match)...)
		}
	}

	return out
}

// resourcesAt gives the live resources of a plan at the address of the match, or whose address without the instance
// key is that address. A deposed object or an instance that the plan destroys is not the resource: the evaluator does
// not read it, and an explicit binding to it keeps the written address, which the evaluator reports as missing.
func resourcesAt(p *plan.Plan, match *binding.Match) []*plan.Resource {
	var out []*plan.Resource

	for i := range p.Resources {
		r := &p.Resources[i]
		if !isLive(r) {
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
	input := cloudInput(plans, ask.model.environment)

	ports := ask.rulePorts()
	if len(ports) == 0 {
		ports = planPorts(inputResources(input))
	}

	return &cloudAsker{ask: ask, evaluator: cloudfw.New(input), integrity: lowestIntegrity(plans), ports: ports}
}

// cloudInput builds the evaluator input from the plans of the environment: the resources of each usable plan, kept
// apart so that the evaluator resolves configuration references inside one plan and finds an address that several
// plans hold; the worst coverage of all of them (a plan that could not be read may hold rules); and the digest of
// the plan bytes.
func cloudInput(plans []*evidence.Plan, environment string) *cloudfw.Input {
	input := &cloudfw.Input{Environment: environment, Digest: plansDigest(plans)}

	for _, declared := range plans {
		input.Coverage = worse(input.Coverage, declared.Coverage)

		if declared.Plan != nil {
			input.Plans = append(input.Plans, cloudfw.Plan{File: declared.Declared.File, Resources: declared.Plan.Resources})
		}
	}

	return input
}

// inputResources gives the resources of all plans of the input.
func inputResources(input *cloudfw.Input) []plan.Resource {
	var out []plan.Resource
	for _, p := range input.Plans {
		out = append(out, p.Resources...)
	}

	return out
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
	sources := c.ask.endpointsOf(pair[0])
	if len(sources) == 0 {
		return []ExternalOutcome{c.ask.bindingGap(pair, unboundElement{pair[0], "a planned resource"})}
	}

	destinations := c.ask.endpointsOf(pair[1])
	if len(destinations) == 0 {
		return []ExternalOutcome{c.ask.bindingGap(pair, unboundElement{pair[1], "a planned resource"})}
	}

	probes := &portProbes{byPort: map[portKey][]probe{}}

	for _, destination := range destinations {
		for _, source := range sources {
			c.probePorts(probes, cloudfw.Query{From: cloudfw.Endpoint{Address: source}, To: cloudfw.Endpoint{Address: destination}})
		}
	}

	return c.ask.portOutcomes(pair, probes)
}

// probePorts asks the query of each port for the source and the destination of the query.
func (c *cloudAsker) probePorts(probes *portProbes, ends cloudfw.Query) {
	for _, port := range c.ports {
		query := ends
		query.Protocol, query.Port = port.protocol, port.number

		found := c.query(&query, port)
		probes.add(port, &found)
	}
}

// query asks the evaluator one question and records it.
func (c *cloudAsker) query(query *cloudfw.Query, port portKey) probe {
	result := c.evaluator.Evaluate(query)

	text := fmt.Sprintf("%s from %s to %s", port, query.From.Address, query.To.Address)

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
