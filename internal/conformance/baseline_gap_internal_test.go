package conformance

import (
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// Characterization tests that pin the reconciliation at the baseline of
// change add-evidence-foundation. The missing-endpoint gap is closed: the skip is counted.

func TestScanEdgesCountsAnEdgeWithAMissingEndpoint(t *testing.T) {
	a := observation.ObservedNode{ObservedID: "a", FilePath: "src/a.ts", QualifiedName: "a"}
	input := &ReconcileInput{
		BindingSet: binding.Set{ByObserved: map[string][]string{"a": {"A"}}},
		Evidence: EvidenceSet{
			Nodes: map[string]observation.ObservedNode{"a": a},
			Edges: []*observation.EvidenceEdge{{ID: "e1", Kind: observation.KindCalls, From: "a", To: "missing"}},
		},
	}

	scan := scanEdges(input, nil)
	if len(scan.Bound) != 0 || scan.Skipped != (SkipCounts{EndpointMissing: 1}) {
		t.Fatalf("scan = %+v; the edge with a missing endpoint is not bound and is counted with the reason endpoint missing", scan)
	}

	if reason := scan.Skipped.Reasons()[SkipEndpointMissing]; reason == "" {
		t.Error("the count has no reason")
	}

	if all := RunSkipCounts(input, 0); all.EndpointMissing != 1 {
		t.Errorf("run skip counts = %+v", all)
	}
}

func TestBaselineGapDeployedInPermitsBothDirections(t *testing.T) {
	model := &intent.ArchitectureModel{Relationships: []intent.RelationshipDeclaration{
		{ID: "r1", Kind: intent.RelDeployedIn, Container: "host", Targets: []string{"svc"}},
	}}

	permitting := permittingRelationships(model)
	if len(permitting[pairKey("host", "svc")]) != 1 || len(permitting[pairKey("svc", "host")]) != 1 {
		t.Fatalf("permitting = %v", permitting)
	}
}
