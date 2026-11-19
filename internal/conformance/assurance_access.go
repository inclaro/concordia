package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/evaluate/rbac"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// A rule of the family `effective-access` asks the built-in evaluators (network-policy, rbac) what an environment
// allows between two sets of workloads. The evaluators read the rendered documents of the environment; the code graph
// has no say, because it records no selection edge for an empty `podSelector` or a `matchExpressions` selector (O6),
// and absence of an edge is never isolation (A35). Three gates stand before a verdict:
//
//  1. the workspace must enable an evaluator of the kind for the environment (`assurance.evaluators`), else the result
//     is `unknown` with `no-evaluator-available`;
//  2. the environment must have rendered resources, else `rendered-evidence-missing`; and a namespace whose render is
//     not complete gives `coverage-incomplete` for the results that involve it;
//  3. a result that is `denied` (or `not-granted`) holds only if the enforcement it assumes is declared in
//     `assurance.evaluators[].assumes`, else `unknown` with `enforcement-not-declared`. A result that is `allowed`
//     (or `granted`) needs no assumption: an unenforced policy only allows more.

// EvaluatorIdentity is the identity of a built-in evaluator that the run used: the manifest toolchain and the bundle
// record it, so that an evaluator upgrade that changes results is visible.
type EvaluatorIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// ContractDigest is the digest of the evaluator contract (`sha256:<hex>`).
	ContractDigest string `json:"contractDigest"`
}

// evaluatorIdentities lists the evaluators that the workspace enables for at least one environment, sorted by id.
func evaluatorIdentities(cfg *workspace.Assurance) []EvaluatorIdentity {
	if cfg == nil {
		return nil
	}

	var out []EvaluatorIdentity

	enabled := func(kind string) bool {
		return slices.ContainsFunc(cfg.Evaluators, func(e workspace.AssuranceEvaluator) bool { return slices.Contains(e.Kinds, kind) })
	}

	if enabled(workspace.EvaluatorNetworkPolicy) {
		contract := netpol.Contract()
		out = append(out, EvaluatorIdentity{ID: netpol.Identifier, Version: netpol.Version, ContractDigest: contract.Digest()})
	}

	if enabled(workspace.EvaluatorRBAC) {
		contract := rbac.Contract()
		out = append(out, EvaluatorIdentity{ID: rbac.Identifier, Version: rbac.Version, ContractDigest: contract.Digest()})
	}

	slices.SortFunc(out, func(a, b EvaluatorIdentity) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return out
}

// EvaluationEntry is one effective-policy evaluation of `assurance.evaluations`: one query that a rule used, with the
// evaluator that answered it and what the answer rests on.
type EvaluationEntry struct {
	Environment string `json:"environment"`
	// Kind is the evaluator kind: `network-policy` or `rbac`.
	Kind string `json:"kind"`
	// Query says what was asked, for example `tcp/8080 from shop/web to shop/db` or
	// `get secrets/db in shop for ServiceAccount shop/api`.
	Query string `json:"query"`
	// Verdict is the answer of the evaluator: `allowed`, `denied`, `granted`, `not-granted`, or `unknown`.
	Verdict string `json:"verdict"`
	// Reason is the reason of the evaluator for an `unknown` answer.
	Reason string `json:"reason,omitempty"`
	// Evaluator, EvaluatorVersion, and InputDigest identify the evaluator and the rendered input that it read.
	Evaluator        string `json:"evaluator"`
	EvaluatorVersion string `json:"evaluatorVersion"`
	InputDigest      string `json:"inputDigest"`
	// Policies are the policies, bindings, and roles that decided the answer, sorted.
	Policies []string `json:"policies"`
	// Assumptions are the enforcement assumptions that the answer rests on, and DeclaredAssumptions the ones of them that
	// the workspace declares for the environment.
	Assumptions         []string `json:"assumptions"`
	DeclaredAssumptions []string `json:"declaredAssumptions"`
	// Limits are what the evaluator does not evaluate.
	Limits []string        `json:"limits"`
	Gaps   []assurance.Gap `json:"gaps"`
}

// recordEvaluation adds an entry to the report. The list stays sorted by environment, kind, and query, and an entry
// that two rules asked for is kept once, so the list does not depend on the order of the rules.
func (r *assuranceRun) recordEvaluation(entry *EvaluationEntry) {
	if r.report == nil {
		return
	}

	index, found := slices.BinarySearchFunc(r.report.Evaluations, entry, func(a EvaluationEntry, b *EvaluationEntry) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Environment, b.Environment), canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Query, b.Query),
		)
	})
	if found {
		return
	}

	r.report.Evaluations = slices.Insert(r.report.Evaluations, index, *entry)
}

// accessKind gives the evaluator kind that answers a dimension.
func accessKind(dimension string) string {
	if dimension == policy.DimensionAuthorization {
		return workspace.EvaluatorRBAC
	}

	return workspace.EvaluatorNetworkPolicy
}

// evaluatorFor gives the declaration that enables an evaluator of the kind for the environment, or nil.
func evaluatorFor(cfg *workspace.Assurance, environment, kind string) *workspace.AssuranceEvaluator {
	if cfg == nil {
		return nil
	}

	for i := range cfg.Evaluators {
		if cfg.Evaluators[i].Environment == environment && slices.Contains(cfg.Evaluators[i].Kinds, kind) {
			return &cfg.Evaluators[i]
		}
	}

	return nil
}

// accessEnvironments gives the environments that an effective-access rule is evaluated in: the selected environments
// of the run, else (no environment is declared) the ones that have rendered evidence or an evaluator.
func (c *checker) accessEnvironments(run *assuranceRun) []string {
	if len(c.environments.Selected) > 0 {
		return c.environments.Selected
	}

	var environments []string

	for _, result := range run.ev.Renders {
		environments = append(environments, result.Environment)
	}

	if run.cfg != nil {
		for i := range run.cfg.Evaluators {
			environments = append(environments, run.cfg.Evaluators[i].Environment)
		}
	}

	slices.SortFunc(environments, canonjson.CompareUTF16)

	return slices.Compact(environments)
}

// accessOutcomes evaluates a rule of the family `effective-access`: for each environment, each subject and target pair
// (CALM elements mapped to rendered resources by the bindings), and each port (network) or verb (authorization).
func (c *checker) accessOutcomes(plan *policy.RulePlan) ([]ExternalOutcome, bool) {
	run := c.assurance
	if run == nil || run.ev == nil {
		return nil, false
	}

	if len(plan.Subjects) == 0 || len(plan.Targets) == 0 {
		return nil, true
	}

	environments := c.accessEnvironments(run)
	if len(environments) == 0 {
		return nil, false
	}

	access := plan.Predicate.EffectiveAccess
	kind := accessKind(access.Dimension)

	var outcomes []ExternalOutcome

	for _, environment := range environments {
		outcomes = append(outcomes, run.accessIn(plan, kind, environment)...)
	}

	c.warnEvaluatorGaps(run)

	return outcomes, true
}

// warnEvaluatorGaps adds a CO-PRV-013 warning for each kind of gap of an evaluation that the run has not warned about
// yet in the environment and evaluator kind: an unsupported construct (a CNI-specific policy kind, an unresolvable
// named port, a cluster-dependent range) or incomplete input of a namespace. The warning names the first gap of the
// kind that the run met; every gap is in `assurance.evaluations`. However many rules and queries meet a gap kind, the
// run warns once. The evaluations are sorted, so the warnings do not depend on the order of the rules.
func (c *checker) warnEvaluatorGaps(run *assuranceRun) {
	if run.report == nil {
		return
	}

	for i := range run.report.Evaluations {
		entry := &run.report.Evaluations[i]

		for _, gap := range entry.Gaps {
			key := fmt.Sprintf("environment %s, evaluator %s: %s: ", entry.Environment, entry.Kind, gap.Kind)

			seen := slices.ContainsFunc(c.diags, func(d diag.Diagnostic) bool {
				return d.Code == diag.PRV013 && strings.Contains(d.Message, key)
			})
			if !seen {
				c.diags = append(c.diags, diag.New(diag.PRV013, diag.Detail(key+gap.Detail)))
			}
		}
	}
}

// pairsOf lists the subject and target pairs of a rule. A pair of one element with itself says nothing about access
// between workloads, so it is left out.
func pairsOf(plan *policy.RulePlan) [][2]string {
	var pairs [][2]string

	for _, subject := range plan.Subjects {
		for _, target := range plan.Targets {
			if subject != target {
				pairs = append(pairs, [2]string{subject, target})
			}
		}
	}

	return pairs
}

// unknownPairs gives one `unknown` outcome for each pair, with the same reason and detail.
func unknownPairs(pairs [][2]string, environment string, reason UnknownReason, detail string) []ExternalOutcome {
	out := make([]ExternalOutcome, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, ExternalOutcome{Subject: pair[0], Target: pair[1], Environment: environment, Verdict: VerdictUnknown, Reason: reason, Detail: detail})
	}

	return out
}

// accessIn evaluates the rule in one environment.
func (r *assuranceRun) accessIn(plan *policy.RulePlan, kind, environment string) []ExternalOutcome {
	pairs := pairsOf(plan)
	declaration := evaluatorFor(r.cfg, environment, kind)

	if declaration == nil {
		return unknownPairs(pairs, environment, ReasonNoEvaluatorAvailable,
			fmt.Sprintf("no %s evaluator is enabled for environment %s (assurance.evaluators)", kind, environment))
	}

	var renders []*render.Result

	for _, result := range r.ev.Renders {
		if result.Environment == environment {
			renders = append(renders, result)
		}
	}

	model := newEnvModel(environment, renders, r.cfg)
	if !model.usable() {
		return unknownPairs(pairs, environment, ReasonRenderedEvidenceMissing,
			fmt.Sprintf("environment %s has no rendered resources (assurance.rendering.sources)", environment))
	}

	ask := &accessAsk{run: r, plan: plan, model: model, declaration: declaration, kind: kind}

	if kind == workspace.EvaluatorRBAC {
		return ask.authorization(pairs)
	}

	return ask.network(pairs)
}

// accessAsk is one environment and one rule: the evaluator input, the declaration that enables the evaluator, and the
// state of the queries that are made.
type accessAsk struct {
	run         *assuranceRun
	plan        *policy.RulePlan
	model       *envModel
	declaration *workspace.AssuranceEvaluator
	kind        string
}

// accessState is the answer of one query, reduced to what the expectation compares: access exists (`allowed` or
// `granted`), none does (`denied` or `not-granted`), or the evaluator cannot say.
type accessState int

const (
	stateUnknown accessState = iota
	stateOpen
	stateClosed
)

// probe is the answer of one query with what a verdict needs of it.
type probe struct {
	// reason says why an answer is unknown.
	reason UnknownReason
	// label names the query inside the outcome, for example `tcp/8080` or `get`.
	label string
	// evidence names the policies, bindings, and roles that decided the answer.
	evidence    string
	assumptions []string
	state       accessState
}

// unmappedDetail is the detail of a CALM element that no binding reaches in the environment.
func unmappedDetail(calmID, environment, what string) string {
	return fmt.Sprintf("CALM element %s has no binding to %s of environment %s", calmID, what, environment)
}

// judge turns the answers for one subject and target into a verdict against the expectation of the rule. Access
// between the two elements exists when any query is open and is absent when every query is closed: `expect: denied`
// is violated by one open port, `expect: allowed` is met by one open port. A closed answer is only as good as the
// enforcement it assumes, so it needs the declaration (gate 3).
func (a *accessAsk) judge(probes []probe) (RuleVerdict, UnknownReason, string) {
	access := a.plan.Predicate.EffectiveAccess
	wantClosed := access.Expect == policy.ExpectDenied || access.Expect == policy.ExpectNotGranted
	open, unknown, closed := partitionProbes(probes)

	switch {
	case len(open) > 0:
		return expectationVerdict(wantClosed, true), "", describe("open", open)
	case len(unknown) > 0:
		return VerdictUnknown, unknown[0].reason, describe("unknown", unknown)
	case len(closed) == 0:
		return VerdictUnknown, ReasonNoEvidenceSupplied, "the evaluator gave no result"
	}

	if missing := a.undeclared(closed); len(missing) > 0 {
		return VerdictUnknown, ReasonEnforcementNotDeclared,
			fmt.Sprintf("%s; the result assumes %s, which assurance.evaluators does not declare for the environment", describe("closed", closed), strings.Join(missing, ", "))
	}

	detail := describe("closed", closed)
	if assumed := assumptionsOf(closed); len(assumed) > 0 {
		detail += "; assumes " + strings.Join(assumed, ", ") + " (declared)"
	}

	return expectationVerdict(wantClosed, false), "", detail
}

// partitionProbes splits the answers by state, keeping their order.
func partitionProbes(probes []probe) (open, unknown, closed []probe) {
	for _, p := range probes {
		switch p.state {
		case stateOpen:
			open = append(open, p)
		case stateClosed:
			closed = append(closed, p)
		case stateUnknown:
			unknown = append(unknown, p)
		}
	}

	return open, unknown, closed
}

// expectationVerdict compares what the answers say with what the rule expects: an open access violates `expect:
// denied` and meets `expect: allowed`; a closed access does the reverse.
func expectationVerdict(wantClosed, accessOpen bool) RuleVerdict {
	if accessOpen == wantClosed {
		return VerdictViolation
	}

	return VerdictPass
}

// assumptionsOf lists the assumptions of the probes, sorted and without duplicates.
func assumptionsOf(probes []probe) []string {
	var out []string

	for _, p := range probes {
		out = append(out, p.assumptions...)
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// undeclared lists the assumptions of the probes that the workspace does not declare for the environment.
func (a *accessAsk) undeclared(probes []probe) []string {
	var missing []string

	for _, assumption := range assumptionsOf(probes) {
		if !slices.Contains(a.declaration.Assumes, assumption) {
			missing = append(missing, assumption)
		}
	}

	return missing
}

// describeLimit is the number of probes that a detail names; the evaluations of the report hold all of them.
const describeLimit = 3

// describe lists the probes of one state with their labels and the evidence that decided them.
func describe(state string, probes []probe) string {
	shown := probes
	if len(shown) > describeLimit {
		shown = shown[:describeLimit]
	}

	parts := make([]string, 0, len(shown)+1)

	for _, p := range shown {
		text := p.label
		if p.evidence != "" {
			text += " (" + p.evidence + ")"
		}

		parts = append(parts, text)
	}

	if rest := len(probes) - len(shown); rest > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", rest))
	}

	return state + ": " + strings.Join(parts, ", ")
}

// reasonOf maps a reason of the evaluators onto the closed set of the results. The evaluators run only on rendered
// evidence, so a workload, namespace, or role that the rendered input does not hold is missing rendered evidence; a
// construct that the evaluator does not support is a gap in the coverage of the input.
func reasonOf(reason string) UnknownReason {
	switch reason {
	case evaluate.ReasonRenderedEvidenceMissing, evaluate.ReasonSubjectNotFound, evaluate.ReasonRoleTargetMissing:
		return ReasonRenderedEvidenceMissing
	case evaluate.ReasonCoverageIncomplete, evaluate.ReasonUnsupportedConstruct:
		return ReasonCoverageIncomplete
	}

	return ReasonCoverageIncomplete
}

// gapText lists gaps as `kind: detail`.
func gapText(gaps []assurance.Gap) string {
	parts := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		parts = append(parts, gap.Kind+": "+gap.Detail)
	}

	return strings.Join(parts, "; ")
}

// answerFacts are the facts of one evaluator answer that go into the report.
type answerFacts struct {
	query, verdict, reason string
	evaluator, version     string
	digest                 string
	policies               []string
	assumptions, limits    []string
	gaps                   []assurance.Gap
}

// entryOf builds the entry of the report for one answer.
func (a *accessAsk) entryOf(facts *answerFacts) *EvaluationEntry {
	declared := []string{}

	for _, assumption := range facts.assumptions {
		if slices.Contains(a.declaration.Assumes, assumption) {
			declared = append(declared, assumption)
		}
	}

	slices.Sort(facts.policies)

	return &EvaluationEntry{
		Environment: a.model.environment, Kind: a.kind, Query: facts.query, Verdict: facts.verdict, Reason: facts.reason,
		Policies: nonNil(slices.Compact(facts.policies)), Assumptions: nonNil(facts.assumptions), DeclaredAssumptions: declared,
		Limits: nonNil(facts.limits), Gaps: gapsOrEmpty(facts.gaps), Evaluator: facts.evaluator, EvaluatorVersion: facts.version,
		InputDigest: facts.digest,
	}
}

// ---- network ----

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

// collect adds the numbers and the ranges of the rule ports. A named port is resolved against the container ports by
// the evaluator, so it adds nothing here.
func (s *portSet) collect(ports []netpol.Port) {
	for _, port := range ports {
		if port.Port == nil || port.Port.IsName {
			continue
		}

		s.add(port.Protocol, port.Port.Number)

		if port.EndPort != nil {
			s.add(port.Protocol, *port.EndPort)
			s.ranges = append(s.ranges, [2]int{port.Port.Number, *port.EndPort})
		}
	}
}

// covered says whether a rule names the number or a range holds it.
func (s *portSet) covered(number int) bool {
	return s.named[number] || slices.ContainsFunc(s.ranges, func(r [2]int) bool { return number >= r[0] && number <= r[1] })
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

// addUnnamedProbe adds the lowest port that no rule names and no range covers.
func (s *portSet) addUnnamedProbe() {
	for number := 1; number <= maxPortNumber; number++ {
		if !s.covered(number) {
			s.seen[portKey{netpol.ProtocolTCP, number}] = true

			return
		}
	}
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
		return []ExternalOutcome{n.ask.bindingGap(pair, pair[0], "a rendered workload")}
	}

	destinations := n.ask.workloadsOf(pair[1])
	if len(destinations) == 0 {
		return []ExternalOutcome{n.ask.bindingGap(pair, pair[1], "a rendered workload")}
	}

	return n.outcomesOf(pair, n.probeAll(sources, destinations))
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
	declared := n.ask.plan.Predicate.EffectiveAccess.Ports
	if len(declared) == 0 {
		return destinationPorts(n.input, &netpol.Workload{Ports: workloadPorts(n.input, destination)})
	}

	ports := make([]portKey, 0, len(declared))
	for _, number := range declared {
		ports = append(ports, portKey{netpol.ProtocolTCP, number})
	}

	return ports
}

// outcomesOf judges the answers of one pair: one outcome for the pair when the rule names no ports, else one for each
// port.
func (n *networkAsker) outcomesOf(pair [2]string, probes *portProbes) []ExternalOutcome {
	base := n.ask.baseOutcome(pair)

	if len(n.ask.plan.Predicate.EffectiveAccess.Ports) == 0 {
		base.Verdict, base.Reason, base.Detail = n.ask.judge(probes.all())

		return []ExternalOutcome{base}
	}

	outcomes := make([]ExternalOutcome, 0, len(probes.order))

	for _, port := range probes.order {
		outcome := base
		outcome.Occurrence = port.String()
		outcome.Verdict, outcome.Reason, outcome.Detail = n.ask.judge(probes.byPort[port])
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

// ---- authorization ----

// accessVerbs are the verbs that an authorization rule asks about for each target: the verbs by which an object can be
// read or changed. An element is accessible when any of them is granted.
var accessVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete"}

// serviceAccountOf gives the ServiceAccount that a rendered resource acts as: the account that a workload runs as, or
// the account itself.
func serviceAccountOf(resource *render.Resource) (rbac.Subject, bool) {
	switch {
	case resource.Workload != nil:
		return rbac.Subject{Kind: rbac.KindServiceAccount, Name: resource.Workload.ServiceAccountName, Namespace: resource.ID.Namespace}, true
	case resource.ID.Kind == "ServiceAccount":
		return rbac.Subject{Kind: rbac.KindServiceAccount, Name: resource.ID.Name, Namespace: resource.ID.Namespace}, true
	}

	return rbac.Subject{}, false
}

// authorization answers the rule for the rbac evaluator. The subject is the ServiceAccount of the `from` element and
// the object is each rendered resource that the `to` element is bound to (the `workload` binding names any kind, for
// example a Secret); the verbs are accessVerbs. The outcome is one for each pair of elements.
func (a *accessAsk) authorization(pairs [][2]string) []ExternalOutcome {
	evaluator := rbac.New(a.model.rbacInput())
	asker := &rbacAsker{ask: a, evaluator: evaluator, digest: evaluator.InputDigest()}

	outcomes := make([]ExternalOutcome, 0, len(pairs))

	for _, pair := range pairs {
		outcomes = append(outcomes, asker.pair(pair))
	}

	return outcomes
}

// rbacAsker holds the evaluator of one environment for the queries of one rule.
type rbacAsker struct {
	ask       *accessAsk
	evaluator *rbac.Evaluator
	digest    string
}

// pair asks the queries of one pair of elements and gives its outcome.
func (r *rbacAsker) pair(pair [2]string) ExternalOutcome {
	subjects := r.ask.subjectsOf(pair[0])
	if len(subjects) == 0 {
		return r.ask.bindingGap(pair, pair[0], "a rendered workload or ServiceAccount")
	}

	targets := r.ask.model.resourcesOf(r.ask.run.bindings, pair[1])
	if len(targets) == 0 {
		return r.ask.bindingGap(pair, pair[1], "a rendered resource")
	}

	var probes []probe

	for _, subject := range subjects {
		for _, target := range targets {
			for _, verb := range accessVerbs {
				probes = append(probes, r.query(subject, target, verb))
			}
		}
	}

	out := r.ask.baseOutcome(pair)
	out.Verdict, out.Reason, out.Detail = r.ask.judge(probes)

	return out
}

// subjectsOf gives the ServiceAccounts that the resources of a CALM element act as.
func (a *accessAsk) subjectsOf(calmID string) []rbac.Subject {
	var subjects []rbac.Subject

	for _, resource := range a.model.resourcesOf(a.run.bindings, calmID) {
		if subject, ok := serviceAccountOf(resource); ok {
			subjects = append(subjects, subject)
		}
	}

	return subjects
}

// baseOutcome is the outcome of a pair before any verdict is set.
func (a *accessAsk) baseOutcome(pair [2]string) ExternalOutcome {
	return ExternalOutcome{Subject: pair[0], Target: pair[1], Environment: a.model.environment}
}

// bindingGap gives the `unknown` outcome of a pair because a CALM element has no binding in the environment.
func (a *accessAsk) bindingGap(pair [2]string, calmID, what string) ExternalOutcome {
	out := a.baseOutcome(pair)
	out.Verdict, out.Reason, out.Detail = VerdictUnknown, ReasonBindingGap, unmappedDetail(calmID, a.model.environment, what)

	return out
}

// query asks the evaluator whether the subject may do the verb to the object and records the answer.
func (r *rbacAsker) query(subject rbac.Subject, target *render.Resource, verb string) probe {
	request := rbac.Request{
		Verb: verb, APIGroup: target.ID.Group, Resource: pluralOf(target.ID.Kind), Name: target.ID.Name, Namespace: target.ID.Namespace,
	}
	result := r.evaluator.Evaluate(&rbac.Query{Request: request, Subject: subject})

	text := rbacQueryText(&request, subject)
	names := rbacEvidenceNames(&result)
	limits := append(slices.Clone(result.Limits), "the plural resource name is derived from the kind of the bound resource")

	r.ask.run.recordEvaluation(r.ask.entryOf(&answerFacts{
		query: text, verdict: string(result.Verdict), reason: result.Reason, policies: slices.Clone(names),
		assumptions: result.Assumptions, limits: limits, gaps: result.Gaps,
		evaluator: result.Evaluator, version: result.Version, digest: r.digest,
	}))

	found := probe{label: text, assumptions: result.Assumptions, evidence: strings.Join(slices.Compact(names), ", ")}

	switch result.Verdict {
	case rbac.Granted:
		found.state = stateOpen
	case rbac.NotGranted:
		found.state = stateClosed
	case rbac.Unknown:
		found.state, found.reason = stateUnknown, reasonOf(result.Reason)
		found.evidence = joinDetails(found.evidence, gapText(result.Gaps))
	}

	return found
}

// rbacQueryText names one rbac query inside the outcome.
func rbacQueryText(request *rbac.Request, subject rbac.Subject) string {
	where := "all namespaces"
	if request.Namespace != "" {
		where = request.Namespace
	}

	return fmt.Sprintf("%s %s/%s in %s for ServiceAccount %s/%s", request.Verb, request.Resource, request.Name, where, subject.Namespace, subject.Name)
}

// rbacEvidenceNames names the bindings and roles that decided an rbac answer, then the roles that are missing.
func rbacEvidenceNames(result *rbac.Result) []string {
	names := make([]string, 0, len(result.Grants)+len(result.Missing))

	for i := range result.Grants {
		grant := &result.Grants[i]
		names = append(names, fmt.Sprintf("%s %s grants %s %s", grant.Binding.Kind, refName(grant.Binding), grant.Role.Kind, refName(grant.Role)))
	}

	for _, missing := range result.Missing {
		names = append(names, fmt.Sprintf("missing %s %s", missing.Kind, refName(missing)))
	}

	return names
}

// refName is `namespace/name`, or the name of a cluster-scoped object.
func refName(ref rbac.Ref) string {
	if ref.Namespace == "" {
		return ref.Name
	}

	return ref.Namespace + "/" + ref.Name
}
