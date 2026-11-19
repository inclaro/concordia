package conformance

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// targetsInclude says whether one of the bindings maps to a target of the relation.
func (r *obligationRun) targetsInclude(bindingIDs []string) bool {
	return slices.ContainsFunc(bindingIDs, func(id string) bool { return slices.Contains(r.ob.Relation.Target, id) })
}

// reverse evaluates, separately, the facts of the relation that run the other way between the subject and a
// target. A reverse fact never satisfies the obligation. For a placement obligation, the CALM `deployed-in`
// direction (placed node to container) makes it an unexpected placement, which is a violation; otherwise it
// is not covered by the rule and is shown as not applicable.
func (r *obligationRun) reverse(anchor string) {
	if len(r.ob.Relation.Target) == 0 {
		return
	}

	for _, bound := range r.facts(anchor, true) {
		ids := r.e.input.BindingSet.ByObserved[r.otherEnd(bound.Edge, true)]
		if !r.targetsInclude(ids) || !r.sameEnvironment(anchor, bound) {
			continue
		}

		r.add(r.reverseResult(anchor, bound))
	}
}

// reverseResult gives the result of a fact that runs the other way between the subject and a target.
func (r *obligationRun) reverseResult(anchor string, bound *BoundEdge) OccurrenceResult {
	if r.ob.Placement != nil {
		return r.occurrenceOf(anchor, bound, OccurrenceWrongDirection, VerdictViolation, "",
			fmt.Sprintf("unexpected placement: %s runs from %s to %s, but the CALM deployed-in relationship %s places the subject in the container %s; the reverse direction never satisfies the obligation",
				bound.Edge.Kind, bound.From.QualifiedName, bound.To.QualifiedName, r.ob.Placement.RelationshipID, r.ob.Placement.Container))
	}

	occ := r.occurrenceOf(anchor, bound, OccurrenceWrongDirection, VerdictNotApplicable, "",
		fmt.Sprintf("the fact runs in the reverse direction (%s -> %s); the rule asks for %s and the fact never satisfies it", bound.From.QualifiedName, bound.To.QualifiedName, r.ob.Relation.Direction))
	occ.Decisive = false

	return occ
}

// sameEnvironment says whether a fact is in the environment of the obligation, or has none that an environment owns.
func (r *obligationRun) sameEnvironment(anchor string, bound *BoundEdge) bool {
	env := r.ob.Environment
	have := r.factEnvironment(anchor, bound)

	return env == "" || have == "" || have == env
}

// permission evaluates a `forbidden` or `allowed` obligation. Each admissible fact of the relation gives a result:
// forbidden is a violation, allowed is a pass. With no such fact, forbidden is a pass only when the coverage allows
// the claim, and allowed has nothing to check.
func (r *obligationRun) permission(anchors []string) {
	forbidden := r.ob.Type == policy.ObligationForbidden
	found := false

	for _, anchor := range anchors {
		if r.permissionAt(anchor, forbidden) {
			found = true
		}
	}

	if forbidden && r.reversePlacements() {
		found = true
	}

	if !found {
		r.permissionWithoutFacts(forbidden)
	}
}

// permissionAt adds the results of the facts of one anchor of a permission obligation. It says whether the anchor has
// a fact that is of the relation, admissible or not.
func (r *obligationRun) permissionAt(anchor string, forbidden bool) bool {
	seen := r.scan(anchor)

	for _, bound := range seen.valid {
		r.add(r.permittedFactResult(anchor, bound, forbidden))
	}

	r.addAll(seen.problems, true)
	r.addAll(seen.mismatch, true)
	r.addAll(seen.separate, false)

	return len(seen.valid)+len(seen.problems)+len(seen.mismatch) > 0
}

// permittedFactResult gives the result of an admissible fact: a violation when the relation is forbidden, else a pass.
func (r *obligationRun) permittedFactResult(anchor string, bound *BoundEdge, forbidden bool) OccurrenceResult {
	if forbidden {
		return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictViolation, "", "forbidden relation observed: "+factText(bound))
	}

	return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictPass, "", "allowed relation observed: "+factText(bound))
}

// permissionWithoutFacts adds the result of a permission obligation with no fact of the relation at any anchor.
func (r *obligationRun) permissionWithoutFacts(forbidden bool) {
	if occ := r.entitySelector(); occ != nil {
		r.add(*occ)

		return
	}

	if !forbidden {
		r.add(r.entity(OccurrenceAbsent, VerdictPass, "", "no allowed relation was observed; nothing to check"))

		return
	}

	if block := r.claimBlocked(r.spec.Completeness); block != nil {
		r.add(r.entity(OccurrenceAbsent, VerdictUnknown, block.reason, block.detail))

		return
	}

	r.add(r.entity(OccurrenceAbsent, VerdictPass, "", fmt.Sprintf("no %s relation was observed and the coverage of the predicate is complete", r.ob.Relation.Kind)))
}

// notPermitted finds the observed connection relations between the subject and its targets that policy does not
// allow: the relation is a connection, differs from the relation that this `allowed` obligation allows, and no
// obligation of the run decided it. Policy that allows one relation for a pair does not allow the others, so each one is a violation,
// and the allowed relation keeps its own `pass`.
func (e *ObligationEvaluator) notPermitted(obl *Obligation, plan *policy.RulePlan, decided map[string]struct{}) []OccurrenceResult {
	if !canHaveNotPermitted(obl, plan) {
		return nil
	}

	run := &obligationRun{e: e, ob: obl, plan: plan, spec: plan.Obligation}
	anchors, _ := run.anchorsOf()

	var out []OccurrenceResult

	for _, anchor := range anchors {
		out = append(out, run.undecidedAround(anchor, decided)...)
	}

	return out
}

// canHaveNotPermitted says whether an obligation has targets and a connection relation, which are what a relation that
// policy does not allow is measured against.
func canHaveNotPermitted(obl *Obligation, plan *policy.RulePlan) bool {
	if plan == nil || plan.Obligation == nil {
		return false
	}

	return len(obl.Relation.Target) > 0 && isConnectionKind(obl.Relation.Kind)
}

// undecidedAround gives the results of the connection facts at the anchor, of every kind and direction but the allowed
// relation itself, that no obligation decided.
func (r *obligationRun) undecidedAround(anchor string, decided map[string]struct{}) []OccurrenceResult {
	var out []OccurrenceResult

	for _, kind := range connectionKinds {
		for _, reversed := range []bool{false, true} {
			if kind == r.ob.Relation.Kind && !reversed {
				continue
			}

			out = append(out, r.undecided(anchor, kind, reversed, decided)...)
		}
	}

	return out
}

// undecided gives the results of the facts of one kind and direction between the anchor and the targets that no obligation decided.
func (r *obligationRun) undecided(anchor string, kind observation.CanonicalEdgeKind, reversed bool, decided map[string]struct{}) []OccurrenceResult {
	var out []OccurrenceResult

	probe := *r.ob
	probe.Relation.Kind = kind

	probeRun := *r
	probeRun.ob = &probe

	for _, bound := range probeRun.facts(anchor, reversed) {
		key := observation.EdgeKeyOf(bound.Edge).String()
		if _, done := decided[key]; done || !probeRun.sameEnvironment(anchor, bound) {
			continue
		}

		ids := r.e.input.BindingSet.ByObserved[probeRun.otherEnd(bound.Edge, reversed)]
		if !r.targetsInclude(ids) || !slices.Contains(r.spec.Roles, bound.Edge.Role) {
			continue
		}

		decided[key] = struct{}{}

		out = append(out, r.notPermittedResult(&probeRun, anchor, bound))
	}

	return out
}

// notPermittedResult gives the result of a relation that policy does not allow: unknown when the fact is resolved
// below the minimum resolution of the rule, else a violation.
func (r *obligationRun) notPermittedResult(probeRun *obligationRun, anchor string, bound *BoundEdge) OccurrenceResult {
	if reason, detail, low := r.resolutionProblem(bound.Edge); low {
		return probeRun.occurrenceOf(anchor, bound, OccurrenceNotPermitted, VerdictUnknown, reason, detail)
	}

	return probeRun.occurrenceOf(anchor, bound, OccurrenceNotPermitted, VerdictViolation, "",
		fmt.Sprintf("policy does not allow this relation: %s; the pair allows %s only, and a different relation is not allowed because one is", factText(bound), r.ob.Relation.Kind))
}
