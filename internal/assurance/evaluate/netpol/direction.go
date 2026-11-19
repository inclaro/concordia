package netpol

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// tri is a three-valued truth value. Every rule and selector check gives one, and `maybe` is what an unsupported or
// unresolvable construct gives: it is neither allow nor deny. `and` is the minimum and `or` the maximum, so
// allowing rules can only raise a result (the monotonicity that the property test checks).
type tri int8

const (
	triNo    tri = 0
	triMaybe tri = 1
	triYes   tri = 2
)

func and(a, b tri) tri { return min(a, b) }
func or(a, b tri) tri  { return max(a, b) }
func not(a tri) tri    { return triYes - a }

// dirCtx is the state of the evaluation of one direction of one workload.
type dirCtx struct {
	pod   *Workload
	podNS *Namespace
	// peer is the other end of the traffic.
	peer  endpoint
	dir   string
	proto string
	gaps  []assurance.Gap
	port  int
}

// candidate is a policy that may select the workload.
type candidate struct {
	policy *NetworkPolicy
	// selects is whether the policy selects the workload.
	selects tri
	// allows is whether one of its rules for this direction allows the traffic.
	allows tri
}

// direction evaluates one direction (egress of the source or ingress of the destination).
func (e *Evaluator) direction(ctx *dirCtx) DirectionResult {
	out := DirectionResult{Direction: ctx.dir, Policies: []PolicyRef{}, Gaps: []assurance.Gap{}}

	if foreign := e.foreignAffecting(ctx); len(foreign) > 0 {
		// A policy of another kind may allow or deny anything for this workload. The native policies cannot make the
		// result definite either way, so the direction is unknown.
		out.Verdict = Unknown
		out.Gaps = sortGaps(foreign)

		return out
	}

	cands := e.candidates(ctx)
	if len(cands) == 0 {
		// No policy selects the workload for this direction: it is open.
		out.Verdict = Allowed
		out.Open = true

		return out
	}

	e.decide(ctx, cands, &out)
	out.Policies = sortPolicies(out.Policies)

	return out
}

// candidates lists the policies of the workload namespace that govern the direction and may select the workload.
func (e *Evaluator) candidates(ctx *dirCtx) []candidate {
	var cands []candidate

	for i := range e.in.Policies {
		pol := &e.in.Policies[i]
		if pol.Metadata.Namespace != ctx.pod.Namespace || !appliesTo(pol, ctx.dir) {
			continue
		}

		sel := e.matchSelector(ctx, &pol.Spec.PodSelector, ctx.pod.Labels, "podSelector of NetworkPolicy "+refText(pol))
		if sel == triNo {
			continue
		}

		cands = append(cands, candidate{policy: pol, selects: sel, allows: e.policyAllows(ctx, pol)})
	}

	return cands
}

// decide sets the verdict, the policies behind it and the gaps of a direction that has candidate policies.
// Traffic is allowed when no policy selects the workload, or when a selecting policy allows it.
func (e *Evaluator) decide(ctx *dirCtx, cands []candidate, out *DirectionResult) {
	anySelect, anyAllow := triNo, triNo
	for _, cd := range cands {
		anySelect = or(anySelect, cd.selects)
		anyAllow = or(anyAllow, and(cd.selects, cd.allows))
	}

	switch or(not(anySelect), anyAllow) {
	case triYes:
		out.Verdict = Allowed
		out.Policies = ctx.refs(cands, func(cd candidate) bool { return and(cd.selects, cd.allows) == triYes })
	case triNo:
		out.Verdict = Denied
		out.Policies = ctx.refs(cands, func(cd candidate) bool { return cd.selects == triYes })
	case triMaybe:
		out.Verdict = Unknown
		out.Policies = ctx.refs(cands, func(cd candidate) bool { return cd.selects == triMaybe || cd.allows == triMaybe })
		out.Gaps = sortGaps(ctx.gaps)
	}
}

func (ctx *dirCtx) refs(cands []candidate, keep func(candidate) bool) []PolicyRef {
	var refs []PolicyRef

	for _, cd := range cands {
		if keep(cd) {
			refs = append(refs, PolicyRef{
				Kind: "NetworkPolicy", Namespace: cd.policy.Metadata.Namespace, Name: cd.policy.Metadata.Name, Direction: ctx.dir,
			})
		}
	}

	return refs
}

// addGap records a gap of the evaluation.
func (ctx *dirCtx) addGap(kind, detail string) {
	ctx.gaps = append(ctx.gaps, assurance.Gap{Kind: kind, Detail: detail})
}

// appliesTo reports whether the policy governs the direction: `policyTypes` when given, else Ingress always and
// Egress when the policy has egress rules (the Kubernetes defaulting).
func appliesTo(pol *NetworkPolicy, dir string) bool {
	types := pol.Spec.PolicyTypes
	if len(types) == 0 {
		if dir == DirectionIngress {
			return true
		}

		return len(pol.Spec.Egress) > 0
	}

	want := PolicyTypeIngress
	if dir == DirectionEgress {
		want = PolicyTypeEgress
	}

	return slices.Contains(types, want)
}

func refText(pol *NetworkPolicy) string { return pol.Metadata.Namespace + "/" + pol.Metadata.Name }

// foreignAffecting lists a gap for each unsupported policy that may apply to the workload.
func (e *Evaluator) foreignAffecting(ctx *dirCtx) []assurance.Gap {
	var gaps []assurance.Gap

	for i := range e.in.Foreign {
		foreign := &e.in.Foreign[i]
		if !e.foreignMayApply(ctx, foreign) {
			continue
		}

		gaps = append(gaps, assurance.Gap{
			Kind: evaluate.GapUnsupportedKind,
			Detail: fmt.Sprintf("%s %s is not supported and applies to workload %s/%s",
				foreign.Kind, foreignScope(foreign), ctx.pod.Namespace, ctx.pod.Name),
		})
	}

	return gaps
}

// foreignMayApply reports whether the unsupported policy is in scope of the workload and may select it.
func (e *Evaluator) foreignMayApply(ctx *dirCtx, foreign *ForeignPolicy) bool {
	if foreign.Namespace != "" && foreign.Namespace != ctx.pod.Namespace {
		return false
	}

	if foreign.Selector == nil {
		return true
	}

	what := foreign.Kind + " selector of " + foreign.Namespace + "/" + foreign.Name

	return e.matchSelector(ctx, foreign.Selector, ctx.pod.Labels, what) != triNo
}

// foreignScope is the name of the policy qualified with its namespace, when it has one.
func foreignScope(foreign *ForeignPolicy) string {
	if foreign.Namespace == "" {
		return foreign.Name
	}

	return foreign.Namespace + "/" + foreign.Name
}

// matchSelector matches a label selector and records a gap when Kubernetes would reject the selector.
func (e *Evaluator) matchSelector(ctx *dirCtx, sel *evaluate.LabelSelector, labels map[string]string, what string) tri {
	ok, err := sel.Matches(labels)
	if err != nil {
		ctx.addGap(evaluate.GapInvalidSelector, what+": "+err.Error())

		return triMaybe
	}

	if ok {
		return triYes
	}

	return triNo
}

// policyAllows is whether any rule of the policy for the direction allows the traffic.
func (e *Evaluator) policyAllows(ctx *dirCtx, pol *NetworkPolicy) tri {
	result := triNo

	if ctx.dir == DirectionIngress {
		for i := range pol.Spec.Ingress {
			r := &pol.Spec.Ingress[i]
			result = or(result, and(e.peersMatch(ctx, pol, r.From), e.portsMatch(ctx, r.Ports)))
		}

		return result
	}

	for i := range pol.Spec.Egress {
		r := &pol.Spec.Egress[i]
		result = or(result, and(e.peersMatch(ctx, pol, r.To), e.portsMatch(ctx, r.Ports)))
	}

	return result
}

// anyEntryMatches is the or of the match of every entry. An empty list matches everything.
func anyEntryMatches[T any](entries []T, match func(*T) tri) tri {
	if len(entries) == 0 {
		return triYes
	}

	result := triNo
	for i := range entries {
		result = or(result, match(&entries[i]))
	}

	return result
}
