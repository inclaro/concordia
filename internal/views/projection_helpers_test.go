package views_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/views"
)

const updateGoldenEnv = "CONCORDIA_UPDATE_GOLDEN"

// golden compares data with testdata/<name>; CONCORDIA_UPDATE_GOLDEN=1 rewrites the file.
func golden(t *testing.T, name string, data []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if os.Getenv(updateGoldenEnv) != "" {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(want, data) {
		t.Errorf("%s differs from the golden file (CONCORDIA_UPDATE_GOLDEN=1 rewrites it):\ngot:  %s\nwant: %s", name, data, want)
	}
}

// projectionModel is a small architecture: a customer, the payments system (web UI, orders service with a
// handler component, database), an external ledger system, and a cluster that hosts two containers.
func projectionModel(protocol string) *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		SourceURI: "architecture/payments.calm.json",
		Nodes: []intent.NodeDeclaration{
			{ID: "customer", NodeType: "actor", Name: "Customer", Description: "buys"},
			{ID: "payments-system", NodeType: "system", Name: "Payments", Description: "takes payments"},
			{ID: "web-ui", NodeType: "webclient", Name: "Web UI", Tags: []string{"frontend"}},
			{ID: "orders-service", NodeType: "service", Name: "Orders"},
			{ID: "orders-handler", NodeType: "service", Name: "Handler", C4Level: intent.C4Component},
			{ID: "payments-db", NodeType: "database", Name: "Payments DB"},
			{ID: "audit-db", NodeType: "database", Name: "Audit DB"},
			{ID: "ledger", NodeType: "system", Name: "Ledger"},
			{ID: "reporting-service", NodeType: "service", Name: "Reporting"},
			{ID: "cluster-a", NodeType: "network", Name: "Cluster A"},
		},
		Relationships: []intent.RelationshipDeclaration{
			{ID: "payments-composition", Kind: intent.RelComposedOf, Container: "payments-system", Targets: []string{"web-ui", "orders-service", "payments-db", "audit-db"}},
			{ID: "orders-composition", Kind: intent.RelComposedOf, Container: "orders-service", Targets: []string{"orders-handler"}},
			{ID: "customer-uses-web", Kind: intent.RelInteracts, Source: "customer", Targets: []string{"web-ui"}},
			{ID: "web-to-orders", Kind: intent.RelConnects, Source: "web-ui", Targets: []string{"orders-service"}, Protocol: protocol},
			{ID: "orders-to-db", Kind: intent.RelConnects, Source: "orders-service", Targets: []string{"payments-db"}, Protocol: "SQL"},
			{ID: "orders-to-ledger", Kind: intent.RelConnects, Source: "orders-service", Targets: []string{"ledger"}, Protocol: "gRPC"},
			{ID: "cluster-hosts", Kind: intent.RelDeployedIn, Container: "cluster-a", Targets: []string{"orders-service", "payments-db"}},
		},
		Controls: []intent.ControlAttachment{
			{ControlID: "orders-authz", Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "orders-service"}, Pointer: "/nodes/3/controls"},
			{ControlID: "reporting-retention", Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "reporting-service"}, Pointer: "/nodes/8/controls"},
		},
	}
}

func rootBinding(id, element, glob string, exclusive bool) binding.Binding {
	return binding.Binding{
		ID: id, ArchitectureRef: element, Exclusive: exclusive, Required: true, Source: "architecture/payments.calm.json#/" + id,
		Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: glob}},
	}
}

func projectionBindings() []binding.Binding {
	return []binding.Binding{
		rootBinding("b-web", "web-ui", "services/web/**", true),
		rootBinding("b-orders", "orders-service", "services/orders/**", true),
		rootBinding("b-handler", "orders-handler", "services/orders/handler/**", true),
		rootBinding("b-db", "payments-db", "services/db/**", true),
		rootBinding("b-audit", "audit-db", "services/audit/**", true),
	}
}

func containerView(id string) views.Definition {
	return views.Definition{
		ID: id, Kind: views.KindContainer, Origin: views.OriginGenerated, Owner: "team", Scope: []string{"payments-system"},
		Abstraction: views.AbstractionContainer, Output: &views.Output{Path: "docs/architecture/views/" + id + ".dsl"},
	}
}

func contextView(id string) views.Definition {
	v := containerView(id)
	v.Kind, v.Abstraction = views.KindSystemContext, views.AbstractionSystem

	return v
}

func deploymentView(id, environment string) views.Definition {
	v := containerView(id)
	v.Kind, v.Abstraction, v.Environment = views.KindDeployment, views.AbstractionDeployment, environment

	return v
}

// fn is a function of the fake code graph.
type fn struct{ id, path string }

// call is a call between two functions, by ID.
type call struct {
	kind, from, to, confidence string
}

func calls(from, to string) call { return call{kind: "CALLS", from: from, to: to, confidence: "0.95"} }

// codeEvidence serves functions and calls through the fake graph and the CBM adapter, and resolves the bindings.
func codeEvidence(t *testing.T, fns []fn, edges []call, bindings []binding.Binding) *conformance.ReconcileInput {
	t.Helper()

	nodes := make([]testutil.FakeNode, len(fns))
	index := map[string]int{}

	for i, f := range fns {
		nodes[i] = testutil.FakeNode{ID: f.id, Label: "Function", QN: "pkg." + f.id, Path: f.path, Line: 1}
		index[f.id] = i
	}

	fakeEdges := make([]testutil.FakeEdge, len(edges))
	for i, e := range edges {
		props := map[string]string{"strategy": "lsp_direct", "confidence": e.confidence, "line": "3"}
		if e.confidence == "0.28" {
			props["strategy"] = "suffix_match"
		}

		fakeEdges[i] = testutil.NewEdge(&nodes[index[e.from]], &nodes[index[e.to]], e.kind, props)
	}

	graph := testutil.NewFakeGraph("p", nodes, fakeEdges)
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{
		Client: observation.NewFakeClient(graph.Handler, ""), ExecutableVersion: "0.12.1",
	})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: "p"})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"CALLS", "IMPORTS"})
	if err != nil {
		t.Fatalf("FetchEdges: %v", err)
	}

	list := make([]observation.ObservedNode, 0, len(res.Nodes))
	for id := range res.Nodes {
		list = append(list, res.Nodes[id])
	}

	return &conformance.ReconcileInput{
		Model:                   projectionModel("HTTPS"),
		BindingSet:              binding.Resolve(bindings, list, nil),
		Evidence:                conformance.EvidenceSet{Nodes: res.Nodes, Edges: res.Edges},
		SupportedCanonicalKinds: observation.SupportedCanonicalKinds(),
	}
}

// codeGraph is the standard code: two functions of the web UI, two of the orders service (one in its handler
// component), one of the database and the audit database, and the calls between them.
func codeGraph() ([]fn, []call) {
	return []fn{
		{"web1", "services/web/a.go"},
		{"web2", "services/web/b.go"},
		{"ord1", "services/orders/a.go"},
		{"ord2", "services/orders/b.go"},
		{"hdl1", "services/orders/handler/h.go"},
		{"db1", "services/db/a.go"},
		{"aud1", "services/audit/a.go"},
	}, []call{
		calls("web1", "ord1"), calls("web2", "hdl1"), calls("ord1", "ord2"), calls("hdl1", "db1"), calls("ord2", "aud1"),
	}
}

func observed(t *testing.T, view *views.Definition, in *conformance.ReconcileInput) *views.Projection {
	t.Helper()

	return views.ObserveProjection(view, &views.ObserveInput{Reconcile: in, Bindings: projectionBindings()})
}

// prodPlacement is the placement projection of the production environment of the model, from the records.
func placementOf(environment string, records ...deployment.Placement) views.PlacementProjection {
	set := deployment.PlacementSet{Placements: records, Gaps: []deployment.PlacementGap{}}

	return views.ProjectPlacement(projectionModel("HTTPS"), set, environment, nil)
}

// connects is a declared `connects` relationship.
func connects(id, from, to, protocol string) intent.RelationshipDeclaration {
	return intent.RelationshipDeclaration{ID: id, Kind: intent.RelConnects, Source: from, Targets: []string{to}, Protocol: protocol}
}
