package netpol

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/canonjson"
)

// Verdict is the result of one network query.
type Verdict string

// Verdicts.
const (
	Allowed Verdict = "allowed"
	Denied  Verdict = "denied"
	Unknown Verdict = "unknown"
)

// Directions of a policy.
const (
	DirectionIngress = "ingress"
	DirectionEgress  = "egress"
)

// metadataNameLabel is the label that Kubernetes sets on every namespace (since 1.21). Policies commonly select a
// namespace by it, and rendered Namespace documents do not carry it.
const metadataNameLabel = "kubernetes.io/metadata.name"

// Endpoint is one end of a query: a workload of the input, or an address outside the cluster's pod network.
type Endpoint struct {
	Namespace string
	Workload  string
	// Address is an IP address. A query has at least one workload end.
	Address string
}

// Query asks whether traffic from From to To on one destination port and protocol is allowed.
type Query struct {
	From Endpoint
	To   Endpoint
	// Protocol is TCP (the default), UDP, or SCTP.
	Protocol string
	// Port is the destination port number (1-65535).
	Port int
}

// PolicyRef names a policy that took part in a decision.
type PolicyRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

// DirectionResult is the result for one direction of one workload.
type DirectionResult struct {
	Direction string  `json:"direction"`
	Verdict   Verdict `json:"verdict"`
	// Policies are the policies that decided the direction: the ones that allow, or all selecting policies when the
	// direction is denied, or the ones that cannot be evaluated when it is unknown.
	Policies []PolicyRef     `json:"policies"`
	Gaps     []assurance.Gap `json:"gaps"`
	// Open is true when no policy selects the workload for this direction, so the direction allows everything.
	Open bool `json:"open"`
}

// Result is the answer to one Query. Egress is nil when the source is an address, Ingress when the destination is.
type Result struct {
	Evaluator string  `json:"evaluator"`
	Version   string  `json:"version"`
	Verdict   Verdict `json:"verdict"`
	// Reason is set when the verdict is `unknown`: one of the `evaluate.Reason*` values.
	Reason  string           `json:"reason,omitempty"`
	Egress  *DirectionResult `json:"egress,omitempty"`
	Ingress *DirectionResult `json:"ingress,omitempty"`
	// Policies are the policies that decided the result.
	Policies []PolicyRef `json:"policies"`
	// Assumptions are the enforcement assumptions the result depends on.
	Assumptions []string `json:"assumptions"`
	// Limits are what the evaluation does not cover.
	Limits []string `json:"limits"`
	// Gaps explain an `unknown` result: the construct or coverage gap, for the affected workloads only.
	Gaps []assurance.Gap `json:"gaps"`
}

// Evaluator answers queries over one Input. It is safe for concurrent use after New.
type Evaluator struct {
	namespaces map[string]*Namespace
	workloads  map[string]*Workload
	in         *Input
	podRanges  []netip.Prefix
	gate       []assurance.Gap
	rangeErr   bool
}

// New indexes the input. It does not fail: whatever the evaluator cannot use becomes a gap of the results that
// depend on it.
// The input must not be changed while the evaluator is in use.
func New(input *Input) *Evaluator {
	evaluator := &Evaluator{
		in:         input,
		namespaces: make(map[string]*Namespace, len(input.Namespaces)),
		workloads:  make(map[string]*Workload, len(input.Workloads)),
	}

	for i := range input.Namespaces {
		evaluator.namespaces[input.Namespaces[i].Name] = &input.Namespaces[i]
	}

	for i := range input.Workloads {
		work := &input.Workloads[i]
		evaluator.workloads[work.Namespace+"/"+work.Name] = work
	}

	for _, cidr := range input.PodRanges {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			// A pod range that cannot be read means that no ipBlock can be proved disjoint from the pod network.
			evaluator.rangeErr = true

			continue
		}

		evaluator.podRanges = append(evaluator.podRanges, prefix.Masked())
	}

	if input.Evidence != evaluate.EvidenceRendered {
		evaluator.gate = []assurance.Gap{{
			Kind:   evaluate.GapDeclaredOnly,
			Detail: fmt.Sprintf("the input has evidence level %q; the evaluator needs %q", input.Evidence, evaluate.EvidenceRendered),
		}}
	}

	return evaluator
}

// InputDigest is the digest of the evaluated input.
func (e *Evaluator) InputDigest() string { return e.in.Digest() }

// Evaluate answers one query.
func (e *Evaluator) Evaluate(query *Query) Result {
	res := Result{
		Evaluator:   Identifier,
		Version:     Version,
		Policies:    []PolicyRef{},
		Assumptions: []string{},
		Limits:      slices.Clone(Limits),
		Gaps:        []assurance.Gap{},
	}

	if len(e.gate) > 0 {
		return unknownResult(&res, evaluate.ReasonRenderedEvidenceMissing, e.gate)
	}

	proto := strings.ToUpper(query.Protocol)
	if proto == "" {
		proto = ProtocolTCP
	}

	if query.Port < 1 || query.Port > maxPort {
		return unknownResult(&res, evaluate.ReasonUnsupportedConstruct,
			[]assurance.Gap{{Kind: evaluate.GapInvalidPeer, Detail: fmt.Sprintf("query port %d is not in 1-65535", query.Port)}})
	}

	src, fromGaps := e.resolve(query.From)
	dest, toGaps := e.resolve(query.To)

	if src.invalid || dest.invalid {
		return unknownResult(&res, evaluate.ReasonSubjectNotFound, slices.Concat(fromGaps, toGaps))
	}

	if src.workload == nil && dest.workload == nil {
		return unknownResult(&res, evaluate.ReasonSubjectNotFound, []assurance.Gap{{
			Kind:   evaluate.GapWorkloadMissing,
			Detail: "a query needs a workload at one end; traffic between two addresses is not evaluated",
		}})
	}

	if gaps := slices.Concat(fromGaps, toGaps); len(gaps) > 0 {
		reason := evaluate.ReasonCoverageIncomplete
		if slices.ContainsFunc(gaps, func(g assurance.Gap) bool { return g.Kind == evaluate.GapWorkloadMissing }) {
			reason = evaluate.ReasonSubjectNotFound
		}

		return unknownResult(&res, reason, gaps)
	}

	res.Assumptions = []string{assumptionEnforced}

	var dirs []*DirectionResult

	if src.workload != nil {
		d := e.direction(&dirCtx{
			dir: DirectionEgress, pod: src.workload, podNS: src.ns, peer: dest, port: query.Port, proto: proto,
		})
		res.Egress = &d
		dirs = append(dirs, &d)
	}

	if dest.workload != nil {
		d := e.direction(&dirCtx{
			dir: DirectionIngress, pod: dest.workload, podNS: dest.ns, peer: src, port: query.Port, proto: proto,
		})
		res.Ingress = &d
		dirs = append(dirs, &d)
	}

	combine(&res, dirs)

	return res
}

// maxPort is the highest TCP, UDP, or SCTP port number.
const maxPort = 65535

// endpoint is a resolved end of a query.
type endpoint struct {
	workload *Workload
	ns       *Namespace
	addr     netip.Addr
	// invalid is set for an address that cannot be parsed or an end that names nothing.
	invalid bool
}

// resolve looks the end up and gates it on the coverage of its namespace.
func (e *Evaluator) resolve(end Endpoint) (endpoint, []assurance.Gap) {
	if end.Workload == "" {
		addr, err := netip.ParseAddr(end.Address)
		if err != nil {
			return endpoint{invalid: true}, []assurance.Gap{{
				Kind: evaluate.GapInvalidRange, Detail: fmt.Sprintf("query address %q is not an IP address", end.Address),
			}}
		}

		return endpoint{addr: addr.Unmap()}, nil
	}

	work, ok := e.workloads[end.Namespace+"/"+end.Workload]
	if !ok {
		return endpoint{invalid: true}, []assurance.Gap{{
			Kind: evaluate.GapWorkloadMissing, Detail: fmt.Sprintf("workload %s/%s is not in the rendered input", end.Namespace, end.Workload),
		}}
	}

	space, ok := e.namespaces[work.Namespace]
	if !ok {
		return endpoint{workload: work}, []assurance.Gap{{
			Kind: evaluate.GapNamespaceMissing, Detail: fmt.Sprintf("namespace %s is not in the rendered input", work.Namespace),
		}}
	}

	if space.Coverage != assurance.CoverageComplete {
		return endpoint{workload: work, ns: space}, []assurance.Gap{{
			Kind:   evaluate.GapNamespaceCoverage,
			Detail: fmt.Sprintf("namespace %s has render coverage %q", space.Name, coverageText(space.Coverage)),
		}}
	}

	return endpoint{workload: work, ns: space}, nil
}

func coverageText(c assurance.Coverage) string {
	if c == "" {
		return "not-stated"
	}

	return string(c)
}

func unknownResult(res *Result, reason string, gaps []assurance.Gap) Result {
	res.Verdict = Unknown
	res.Reason = reason
	res.Gaps = sortGaps(gaps)

	return *res
}

// combine joins the directions: traffic needs every direction to allow it, so one denied direction denies it even
// when the other is unknown, and unknown beats allowed.
func combine(res *Result, dirs []*DirectionResult) {
	verdict := Allowed

	for _, d := range dirs {
		switch {
		case d.Verdict == Denied:
			verdict = Denied
		case d.Verdict == Unknown && verdict != Denied:
			verdict = Unknown
		}
	}

	res.Verdict = verdict

	for _, d := range dirs {
		if d.Verdict != verdict {
			continue
		}

		res.Policies = append(res.Policies, d.Policies...)

		if verdict == Unknown {
			res.Gaps = append(res.Gaps, d.Gaps...)
		}
	}

	res.Policies = sortPolicies(res.Policies)
	res.Gaps = sortGaps(res.Gaps)

	if verdict == Unknown {
		res.Reason = evaluate.ReasonUnsupportedConstruct
	}
}

func sortPolicies(refs []PolicyRef) []PolicyRef {
	if refs == nil {
		return []PolicyRef{}
	}

	slices.SortFunc(refs, func(a, b PolicyRef) int {
		return cmpChain(
			canonjson.CompareUTF16(a.Namespace, b.Namespace),
			canonjson.CompareUTF16(a.Kind, b.Kind),
			canonjson.CompareUTF16(a.Name, b.Name),
			canonjson.CompareUTF16(a.Direction, b.Direction),
		)
	})

	return slices.Compact(refs)
}

func sortGaps(gaps []assurance.Gap) []assurance.Gap {
	if gaps == nil {
		return []assurance.Gap{}
	}

	slices.SortFunc(gaps, func(a, b assurance.Gap) int {
		return cmpChain(canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Detail, b.Detail))
	})

	return slices.Compact(gaps)
}

func cmpChain(cmps ...int) int {
	for _, c := range cmps {
		if c != 0 {
			return c
		}
	}

	return 0
}
