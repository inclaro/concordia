package conformance

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/trust"
)

func trustModel() *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		Nodes: []intent.NodeDeclaration{
			{ID: "web", Trust: &trust.Declaration{Dimensions: map[string]string{"zone": "internet", "account": "a1"}}},
			{ID: "api", Trust: &trust.Declaration{Dimensions: map[string]string{"zone": "internal", "account": "a2"}}},
			{ID: "ns-a"},
			{ID: "ns-b"},
		},
		Relationships: []intent.RelationshipDeclaration{
			{ID: "web-to-api", Kind: intent.RelConnects, Source: "web", Targets: []string{"api"}},
			{ID: "a-to-b", Kind: intent.RelConnects, Source: "ns-a", Targets: []string{"ns-b"}},
			{ID: "host", Kind: intent.RelDeployedIn, Container: "web", Targets: []string{"api"}},
		},
		Flows: []intent.FlowDeclaration{{ID: "checkout", Transitions: []intent.FlowTransition{
			{RelationshipID: "web-to-api", Sequence: 1, Direction: "source-to-destination"},
			{RelationshipID: "web-to-api", Sequence: 2, Direction: "destination-to-source"},
		}}},
	}
}

func crossingsOf(t *testing.T, res *TrustResult, flow string) map[string]trust.Crossing {
	t.Helper()

	if res == nil {
		t.Fatal("no trust section")
	}

	out := map[string]trust.Crossing{}

	for i := range res.Crossings {
		if res.Crossings[i].Flow == flow {
			out[res.Crossings[i].Dimension] = res.Crossings[i]
		}
	}

	return out
}

func TestDeriveTrustGivesNothingWithoutTrustValues(t *testing.T) {
	model := trustModel()
	for i := range model.Nodes {
		model.Nodes[i].Trust = nil
	}

	if res := deriveTrust(&trustInput{Model: model, Vocab: trust.NewVocabulary(nil, nil)}); res != nil {
		t.Fatalf("trust = %+v, want nil", res)
	}
}

func trustDefaultResult() *TrustResult {
	return deriveTrust(&trustInput{Model: trustModel(), Vocab: trust.NewVocabulary(nil, nil)})
}

// Scenario: SEC-01 one flow with many boundaries.
func TestDeriveTrustCrossingsPerRelationship(t *testing.T) {
	rel := crossingsOf(t, trustDefaultResult(), "web-to-api")

	if len(rel) != 5 {
		t.Fatalf("relationship crossings = %+v", rel)
	}

	if rel["zone"].State != trust.StateCrossing || rel["account"].State != trust.StateCrossing {
		t.Fatalf("relationship crossings = %+v", rel)
	}

	if rel["region"].State != trust.StateUnknown {
		t.Fatalf("relationship crossings = %+v", rel)
	}
}

// Scenario: SEC-02 namespace name does not prove isolation: nodes without values give unknown.
func TestDeriveTrustNodesWithoutValuesGiveUnknown(t *testing.T) {
	for _, c := range crossingsOf(t, trustDefaultResult(), "a-to-b") {
		if c.State != trust.StateUnknown {
			t.Fatalf("a-to-b crossing = %+v, want unknown", c)
		}
	}
}

// Structure relationships are not flows.
func TestDeriveTrustStructureRelationshipsGiveNoCrossings(t *testing.T) {
	if len(crossingsOf(t, trustDefaultResult(), "host")) != 0 {
		t.Fatal("deployed-in must not give crossings")
	}
}

// Scenario: SEC-02 direction is kept (flow steps, one per direction).
func TestDeriveTrustKeepsFlowStepDirection(t *testing.T) {
	res := trustDefaultResult()
	forward := crossingsOf(t, res, "checkout/1")["zone"]
	reverse := crossingsOf(t, res, "checkout/2")["zone"]

	if forward.Direction != "internet → internal" || reverse.Direction != "internal → internet" {
		t.Fatalf("flow step crossings = %+v / %+v", forward, reverse)
	}

	if forward.Kind != trust.FlowStep || forward.Relationship != "web-to-api" {
		t.Fatalf("flow step crossings = %+v / %+v", forward, reverse)
	}
}

// Sorted by flow, then dimension.
func TestDeriveTrustSortsCrossingsByFlowThenDimension(t *testing.T) {
	res := trustDefaultResult()

	if !slices.IsSortedFunc(res.Crossings, func(a, b trust.Crossing) int {
		if a.Flow != b.Flow {
			return compareStrings(a.Flow, b.Flow)
		}

		return compareStrings(a.Dimension, b.Dimension)
	}) {
		t.Fatal("crossings are not sorted")
	}
}

func TestDeriveTrustTakesRelationshipClassificationAndKeepsMissingUnknown(t *testing.T) {
	model := trustModel()
	model.Relationships[0].Trust = &trust.Declaration{Classification: "restricted"}

	res := deriveTrust(&trustInput{Model: model, Vocab: trust.NewVocabulary(nil, []string{"restricted"})})

	if got := crossingsOf(t, res, "web-to-api")["zone"].Classification; got != "restricted" {
		t.Errorf("classification = %q", got)
	}

	// Scenario: SEC-01 no classification declared.
	if got := crossingsOf(t, res, "a-to-b")["zone"].Classification; got != "unknown" {
		t.Errorf("classification = %q, want unknown", got)
	}
}

func TestDeriveTrustMergesBindingEntryValues(t *testing.T) {
	model := trustModel()
	model.Nodes[2].Trust = nil

	res := deriveTrust(&trustInput{
		Model: model, Vocab: trust.NewVocabulary(nil, nil),
		Bindings: []binding.Binding{
			{ID: "b1", ArchitectureRef: "ns-a", Trust: &trust.Declaration{Dimensions: map[string]string{"zone": "dmz"}}},
			{ID: "b2", ArchitectureRef: "ns-b", Trust: &trust.Declaration{Dimensions: map[string]string{"zone": "dmz"}}},
			// Two entries for one node that disagree leave the dimension undeclared.
			{ID: "b3", ArchitectureRef: "web", Trust: &trust.Declaration{Dimensions: map[string]string{"zone": "dmz"}}},
		},
	})

	if got := crossingsOf(t, res, "a-to-b")["zone"]; got.State != trust.StateSame {
		t.Errorf("a-to-b zone = %+v, want same", got)
	}

	if got := crossingsOf(t, res, "web-to-api")["zone"]; got.State != trust.StateUnknown {
		t.Errorf("web-to-api zone = %+v, want unknown (node and binding disagree)", got)
	}
}

// observedFlowsResult derives the trust section of the model with the observed edges of the scenarios below.
func observedFlowsResult() *TrustResult {
	web := observation.ObservedNode{ObservedID: "w", QualifiedName: "web.Client"}
	api := observation.ObservedNode{ObservedID: "a", QualifiedName: "api.Server"}
	other := observation.ObservedNode{ObservedID: "x", QualifiedName: "ns-a.Thing"}
	out := observation.ObservedNode{ObservedID: "e", QualifiedName: "payments.example.com"}

	return deriveTrust(&trustInput{
		Model: trustModel(), Vocab: trust.NewVocabulary(nil, nil),
		Reconcile: &ReconcileInput{
			BindingSet: binding.Set{ByObserved: map[string][]string{"w": {"web"}, "a": {"api"}, "x": {"ns-a"}}},
			Evidence: EvidenceSet{
				Nodes: map[string]observation.ObservedNode{"w": web, "a": api, "x": other, "e": out},
				Edges: []*observation.EvidenceEdge{
					{ID: "e1", Kind: observation.KindCallsService, From: "w", To: "a"}, // declared: web-to-api
					{ID: "e2", Kind: observation.KindCallsService, From: "a", To: "w"}, // undeclared reverse call
					{ID: "e3", Kind: observation.KindCallsService, From: "w", To: "e"}, // external destination
					{ID: "e4", Kind: observation.KindCalls, From: "w", To: "e"},        // in-process call: no flow
					{ID: "e5", Kind: observation.KindCallsService, From: "x", To: "e"}, // external, no trust on the source either
				},
			},
		},
	})
}

// Scenario: SEC-02 observed flow without trust context.
func TestDeriveTrustObservedFlowToExternalDestinationIsUnknown(t *testing.T) {
	res := observedFlowsResult()

	external := crossingsOf(t, res, "observed:web->payments.example.com")
	if len(external) != 5 {
		t.Fatalf("external crossings = %+v", external)
	}

	for _, c := range external {
		if c.State != trust.StateUnknown || !c.ExternalDestination {
			t.Fatalf("external crossing = %+v", c)
		}

		if c.Kind != trust.FlowObserved || c.Destination != "payments.example.com" {
			t.Fatalf("external crossing = %+v", c)
		}
	}

	if z := external["zone"]; z.From != "internet" || z.To != "unknown" {
		t.Fatalf("zone = %+v", z)
	}

	for _, c := range res.Crossings {
		if c.Flow == "observed:web->payments.example.com" && c.Kind != trust.FlowObserved {
			t.Fatal("kind")
		}
	}
}

// Scenario: SEC-02 direction is kept: the undeclared reverse call is a separate crossing.
func TestDeriveTrustObservedReverseCallIsASeparateCrossing(t *testing.T) {
	z := crossingsOf(t, observedFlowsResult(), "observed:api->web")["zone"]

	if z.Direction != "internal → internet" || z.State != trust.StateCrossing {
		t.Fatalf("reverse crossing = %+v", z)
	}
}

// The declared pair is reported once, as the relationship.
func TestDeriveTrustObservedDeclaredPairIsNotRepeated(t *testing.T) {
	if len(crossingsOf(t, observedFlowsResult(), "observed:web->api")) != 0 {
		t.Fatal("a declared pair must not repeat as an observed flow")
	}
}

func TestDeriveTrustObservedFlowFromSourceWithoutTrust(t *testing.T) {
	if got := crossingsOf(t, observedFlowsResult(), "observed:ns-a->payments.example.com"); len(got) != 5 {
		t.Fatalf("source without trust: %+v", got)
	}
}
