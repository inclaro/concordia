package conformance_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// The pair reconciliation considers only code kinds (design D3): infrastructure facts between
// bound nodes never create or change a `reconciliation[]` entry.
func TestReconciliationIgnoresInfrastructureKinds(t *testing.T) {
	t.Parallel()

	ledger := obs("services.ledger.api", "services/ledger/api.py")
	nodes := append(slices.Clone(observed), ledger)
	codeEdges := func() []*observation.EvidenceEdge {
		return []*observation.EvidenceEdge{edge(inv, store), edgeOf(inv, ledger, observation.KindCalls, observation.ConfidenceResolved, 3)}
	}
	relationships := []intent.RelationshipDeclaration{
		connects("c1", "billing", "fulfillment", &intent.RelationshipObservation{
			AllowedEdgeKinds: []observation.CanonicalEdgeKind{observation.KindImports}, Required: true,
		}),
		connects("c2", "ledger", "billing", nil),
	}

	reconcile := func(extra ...*observation.EvidenceEdge) []conformance.ReconciliationEntry {
		input := build(&scenario{
			observed: nodes, edges: append(codeEdges(), extra...), relationships: relationships,
			plans: []policy.RulePlan{boundary(nil)},
		})

		return conformance.Evaluate(input).Reconciliation
	}

	want := reconcile()

	infrastructure := []*observation.EvidenceEdge{
		edgeOf(inv, store, observation.KindDeploysArtifact, observation.ConfidenceSyntactic, 4),
		edgeOf(store, inv, observation.KindSelectsWorkload, observation.ConfidenceSyntactic, 5),
		edgeOf(ledger, inv, observation.KindUsesImage, observation.ConfidenceSyntactic, 6),
		edgeOf(inv, ledger, observation.KindAmbiguous, observation.ConfidenceHeuristic, 7),
		edgeOf(store, ledger, observation.KindRoutesTo, observation.ConfidenceSyntactic, 8),
		// A derived placement between bound nodes, in both directions, is no code fact.
		edgeOf(inv, store, observation.KindPlacedIn, observation.ConfidenceSyntactic, 9),
		edgeOf(store, inv, observation.KindPlacedIn, observation.ConfidenceSyntactic, 10),
		edgeOf(ledger, store, observation.KindPlacedIn, observation.ConfidenceSyntactic, 11),
	}

	if got := reconcile(infrastructure...); !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciliation changed by infrastructure facts:\n got %+v\nwant %+v", got, want)
	}

	// With only infrastructure facts the observed pair stays unobserved: no entry is created.
	for _, e := range conformance.Reconcile(&build(&scenario{
		observed: nodes, edges: infrastructure, relationships: relationships,
	}).ReconcileInput) {
		if e.EdgeCount != 0 || len(e.ObservedKinds) != 0 {
			t.Errorf("infrastructure facts created an observed entry: %+v", e)
		}
	}
}
