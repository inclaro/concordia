package conformance

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/policy"
)

// reversePlacements reports the reverse placement of a placement fact, for a `forbidden` obligation whose target is
// `deployedIn: false`. A `placed-in` fact ends at the hosting target, so the evaluation that reads the workload side
// does not see a constellation where the workload is bound to a container of the subject and the hosting target is bound
// to the subject. Each such fact in the environment of the obligation is a violation of class wrong-direction (unexpected
// placement) with the location of the fact. It says whether it reported one. An entity with ambiguous bindings is left out:
// no binding wins by order of declaration.
func (r *obligationRun) reversePlacements() bool {
	if !r.readsHostingSide() {
		return false
	}

	reported := false

	for _, host := range r.e.anchors[r.ob.Subject] {
		if r.reportHostPlacements(host) {
			reported = true
		}
	}

	return reported
}

// readsHostingSide says whether the obligation is a placement one whose target is `deployedIn: false` and whose facts
// run away from the subject.
func (r *obligationRun) readsHostingSide() bool {
	return r.ob.Placement != nil && r.ob.Relation.TargetMode == policy.TargetOtherHostingTargets && r.outbound()
}

// reportHostPlacements reports the reverse placements of the facts that end at one hosting entity, and says whether it
// reported one.
func (r *obligationRun) reportHostPlacements(host string) bool {
	node, ok := r.e.nodes[host]
	if !ok {
		return false
	}

	if len(r.e.input.BindingSet.Ambiguous[host]) > 0 || r.outsideRepositoryScope(&node) {
		return false
	}

	reported := false

	for _, bound := range r.bind(r.e.to[factSlot{kind: r.ob.Relation.Kind, id: host}]) {
		if r.reversePlacement(host, bound) {
			reported = true
		}
	}

	return reported
}

// isHostedFact says whether a fact of the hosting side is in the environment of the obligation and starts at an
// entity that is bound to one of the targets, without an ambiguous binding.
func (r *obligationRun) isHostedFact(host string, bound *BoundEdge) bool {
	if len(r.e.input.BindingSet.Ambiguous[bound.Edge.From]) > 0 || !r.sameEnvironment(host, bound) {
		return false
	}

	return r.targetsInclude(bound.FromIDs)
}

// reversePlacement reports one fact of the hosting side, and says whether it did. The fact goes through the checks of
// the normal path after the environment: the applicability selector, the evidence role, and the minimum resolution.
// A fact that fails the role or the resolution check is `unknown`, not a violation; an inapplicable fact is left out.
func (r *obligationRun) reversePlacement(host string, bound *BoundEdge) bool {
	if !r.isHostedFact(host, bound) {
		return false
	}

	if occ, outcome := r.eligible(host, bound); outcome != outValid {
		if outcome == outInapplicable {
			return false
		}

		r.add(occ)

		return true
	}

	r.add(r.occurrenceOf(host, bound, OccurrenceWrongDirection, VerdictViolation, "",
		fmt.Sprintf("unexpected placement: %s runs from %s to %s, but the workload is bound to the container %s of the subject and the hosting target is bound to the subject, "+
			"while the CALM deployed-in relationship %s places the subject in %s",
			bound.Edge.Kind, bound.From.QualifiedName, bound.To.QualifiedName, strings.Join(r.ob.Relation.Target, ", "), r.ob.Placement.RelationshipID, r.ob.Placement.Container)))

	return true
}
