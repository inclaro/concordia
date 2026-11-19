package conformance

import (
	"github.com/inclaro/concordia/internal/trust"
)

// observedFlowSet collects the crossings of the observed flows, one set for each flow ID.
type observedFlowSet struct {
	d    *trustDerivation
	seen map[string]struct{}
	out  []trust.Crossing
}

// add makes the crossings of the flow, unless a flow with the same ID was added.
func (s *observedFlowSet) add(flow *trust.Flow) {
	if _, dup := s.seen[flow.ID]; dup {
		return
	}

	s.seen[flow.ID] = struct{}{}

	s.out = append(s.out, trust.Crossings(s.d.input.Vocab, flow)...)
}

// observedCrossings gives the crossings of the observed remote calls that no
// declared relationship covers: calls between two bound nodes, and calls from a bound
// node to a destination that no binding maps. The second kind names the destination
// as an external destination with unknown trust.
func (d *trustDerivation) observedCrossings(declared map[[2]string]struct{}) []trust.Crossing {
	scan := ScanEdges(d.input.Reconcile, remoteCallKinds)
	set := &observedFlowSet{d: d, seen: map[string]struct{}{}}

	for _, edge := range scan.Bound {
		if pair, ok := undeclaredBoundPair(edge, declared); ok {
			set.add(d.boundFlow(pair))
		}
	}

	for _, edge := range scan.HalfBound {
		if hasOneBoundSource(edge) {
			set.add(d.externalFlow(edge))
		}
	}

	return set.out
}

// undeclaredBoundPair gives the nodes of a call between two distinct bound nodes that no relationship declares.
func undeclaredBoundPair(edge *BoundEdge, declared map[[2]string]struct{}) ([2]string, bool) {
	if len(edge.FromIDs) != 1 || len(edge.ToIDs) != 1 || edge.FromIDs[0] == edge.ToIDs[0] {
		return [2]string{}, false
	}

	pair := [2]string{edge.FromIDs[0], edge.ToIDs[0]}
	_, isDeclared := declared[pair]

	return pair, !isDeclared
}

// hasOneBoundSource says whether the call comes from exactly one bound node and goes to none.
func hasOneBoundSource(edge *BoundEdge) bool {
	return len(edge.FromIDs) == 1 && len(edge.ToIDs) == 0
}

// boundFlow is the observed flow between two bound nodes.
func (d *trustDerivation) boundFlow(pair [2]string) *trust.Flow {
	return &trust.Flow{
		ID: "observed:" + pair[0] + "->" + pair[1], Kind: trust.FlowObserved,
		Source:      d.endpoint(pair[0]),
		Destination: d.endpoint(pair[1]),
	}
}

// externalFlow is the observed flow from a bound node to a destination that no binding maps.
func (d *trustDerivation) externalFlow(edge *BoundEdge) *trust.Flow {
	dest := externalID(&edge.To)

	return &trust.Flow{
		ID: "observed:" + edge.FromIDs[0] + "->" + dest, Kind: trust.FlowObserved,
		Source:      d.endpoint(edge.FromIDs[0]),
		Destination: trust.Endpoint{ID: dest, External: true},
	}
}
