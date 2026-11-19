package deployment_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// plcWorld is a hand-made evidence graph for the placement helper: Deployment and Namespace resources, the derived
// `placed-in` facts, and the binding set that maps them to CALM nodes.
type plcWorld struct {
	result   observation.EvidenceQueryResult
	bindings []binding.Binding
	set      binding.Set
}

func newPlcWorld() *plcWorld {
	return &plcWorld{
		result: observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{}},
		set: binding.Set{
			ByObserved: map[string][]string{}, UsedBy: map[string][]string{}, Ambiguous: map[string][]string{},
		},
	}
}

// workload adds the Deployment payments/name of an environment, declared in file.
func (w *plcWorld) workload(id, env, file, name string) string {
	const namespace = "payments"

	w.result.Nodes[id] = observation.ObservedNode{
		ObservedID: id, Label: "Resource", Kind: "Deployment", QualifiedName: "k8s::apps/Deployment/" + namespace + "/" + name,
		FilePath: file, StartLine: 3, Environment: env,
		Properties: map[string]string{"obj_key": "apps/Deployment/" + namespace + "/" + name},
	}

	return id
}

// namespace adds the Namespace resource `payments` that a manifest of the environment declares.
func (w *plcWorld) namespace(id, env, file string) string {
	const name = "payments"

	w.result.Nodes[id] = observation.ObservedNode{
		ObservedID: id, Label: "Resource", Kind: "Namespace", QualifiedName: "k8s::core/Namespace/_/" + name,
		FilePath: file, StartLine: 1, Environment: env,
		Properties: map[string]string{"obj_key": "core/Namespace/_/" + name},
	}

	return id
}

// productionPair adds the workload `api` and the namespace `ns` of the production environment.
func (w *plcWorld) productionPair() (api, ns string) {
	api = w.workload("api", "production", "k8s/prod/api.yaml", "api")
	ns = w.namespace("ns", "production", "k8s/prod/ns.yaml")

	return api, ns
}

// bindAPI maps the workload to the node `orders-service`.
func (w *plcWorld) bindAPI(api string) {
	w.bind(api, "orders-service", "bind-api", binding.MatchWorkload)
}

// bindNamespace maps the namespace to the container `prod-cluster`.
func (w *plcWorld) bindNamespace(ns string) {
	w.bind(ns, "prod-cluster", "bind-ns", binding.MatchInfraResource)
}

// bind maps an observed ID to a CALM node through a binding of the match kind.
func (w *plcWorld) bind(observed, calm, bindingID string, kind binding.MatchKind) {
	w.set.ByObserved[observed] = append(w.set.ByObserved[observed], calm)
	w.set.UsedBy[observed] = append(w.set.UsedBy[observed], bindingID)
	w.bindings = append(w.bindings, binding.Binding{
		ID: bindingID, ArchitectureRef: calm, Status: binding.StatusActive, Match: []binding.Match{{Kind: kind}},
	})
}

// derive adds the `placed-in` facts, as a check does.
func (w *plcWorld) derive() *observation.EvidenceQueryResult {
	observation.DerivePlacement(&w.result, nil)

	return &w.result
}

func (w *plcWorld) placements(model *intent.ArchitectureModel, env ...string) deployment.PlacementSet {
	return deployment.Placements(w.derive(), &w.set, w.bindings, envs(env...), model)
}

// modelDeployedIn declares that the node is deployed in the container.
func modelDeployedIn(node, container string) *intent.ArchitectureModel {
	return &intent.ArchitectureModel{Relationships: []intent.RelationshipDeclaration{
		{ID: "rel-" + node, Kind: intent.RelDeployedIn, Targets: []string{node}, Container: container},
	}}
}

// plcGapShape is the part of a gap that the tests compare, apart from its identity and locations.
type plcGapShape struct {
	Environment string
	Reason      deployment.Reason
	Side        string
	Node        string
}

// plcPlacementShape is the part of a placement that the tests compare, apart from its locations.
type plcPlacementShape struct {
	Environment, Node, Container, HostingTarget string
	Reverse                                     bool
}

func wantNoGaps(t *testing.T, got *deployment.PlacementSet) {
	t.Helper()

	if len(got.Gaps) != 0 {
		t.Errorf("gaps = %+v", got.Gaps)
	}
}

func wantNoPlacements(t *testing.T, got *deployment.PlacementSet) {
	t.Helper()

	if len(got.Placements) != 0 {
		t.Errorf("placements = %+v", got.Placements)
	}
}

// wantGapShapes checks the gaps of the set, in order.
func wantGapShapes(t *testing.T, got *deployment.PlacementSet, want ...plcGapShape) {
	t.Helper()

	have := make([]plcGapShape, 0, len(got.Gaps))
	for i := range got.Gaps {
		g := &got.Gaps[i]
		have = append(have, plcGapShape{Environment: g.Environment, Reason: g.Reason, Side: g.Side, Node: g.Node})
	}

	if !slices.Equal(have, want) {
		t.Errorf("gaps = %+v, want %+v", got.Gaps, want)
	}
}

// wantPlacementShapes checks the placements of the set, in order.
func wantPlacementShapes(t *testing.T, got *deployment.PlacementSet, want ...plcPlacementShape) {
	t.Helper()

	have := make([]plcPlacementShape, 0, len(got.Placements))
	for i := range got.Placements {
		p := &got.Placements[i]
		have = append(have, plcPlacementShape{
			Environment: p.Environment, Node: p.Node, Container: p.Container, HostingTarget: p.HostingTarget, Reverse: p.Reverse,
		})
	}

	if !slices.Equal(have, want) {
		t.Errorf("placements = %+v, want %+v", got.Placements, want)
	}
}

// Scenario: Records without the pack. The record has the manifest location of the workload.
func TestPlacementsRecordsTheNodeContainerAndEnvironment(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	api, ns := w.productionPair()
	w.bindAPI(api)
	w.bindNamespace(ns)

	got := w.placements(nil, "production")

	want := []deployment.Placement{{
		Node: "orders-service", Container: "prod-cluster", Environment: "production", Workload: api, HostingTarget: ns,
		Locations: []deployment.Location{{Path: "k8s/prod/api.yaml", Line: 3}},
	}}
	if !reflect.DeepEqual(got.Placements, want) {
		t.Errorf("placements = %+v, want %+v", got.Placements, want)
	}

	wantNoGaps(t, &got)
}

// Two environments declare the namespace `payments`; each workload is placed in the namespace of its own environment.
func TestPlacementsKeepsEnvironmentsWithTheSameNamespaceNameApart(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	prodAPI := w.workload("api@production", "production", "k8s/prod/api.yaml", "api")
	stageAPI := w.workload("api@staging", "staging", "k8s/staging/api.yaml", "api")
	prodNS := w.namespace("ns@production", "production", "k8s/prod/ns.yaml")
	stageNS := w.namespace("ns@staging", "staging", "k8s/staging/ns.yaml")

	w.bind(prodAPI, "orders-service", "bind-api-prod", binding.MatchWorkload)
	w.bind(stageAPI, "orders-service", "bind-api-staging", binding.MatchWorkload)
	w.bind(prodNS, "prod-cluster", "bind-ns-prod", binding.MatchInfraResource)
	w.bind(stageNS, "staging-cluster", "bind-ns-staging", binding.MatchInfraResource)

	got := w.placements(nil, "production", "staging")

	// Sorted by environment: production before staging.
	wantPlacementShapes(t, &got,
		plcPlacementShape{Environment: "production", Node: "orders-service", Container: "prod-cluster", HostingTarget: prodNS},
		plcPlacementShape{Environment: "staging", Node: "orders-service", Container: "staging-cluster", HostingTarget: stageNS},
	)
	wantNoGaps(t, &got)

	// A declared environment list selects: the facts of another environment are in no environment.
	only := w.placements(nil, "staging")

	wantPlacementShapes(t, &only,
		plcPlacementShape{Environment: "staging", Node: "orders-service", Container: "staging-cluster", HostingTarget: stageNS},
	)
	wantNoGaps(t, &only)
}

// Scenario: Unbound hosting target is a gap. A namespace that no manifest declares is a synthesized entity.
func TestPlacementsUnboundHostingTargetIsAGap(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	api := w.workload("api", "production", "k8s/prod/api.yaml", "api")
	w.bindAPI(api)

	got := w.placements(nil, "production")

	wantNoPlacements(t, &got)

	want := []deployment.PlacementGap{{
		Environment: "production", Reason: deployment.ReasonBindingGap, Side: deployment.SideHostingTarget, Node: "orders-service",
		Identity: "k8s:production/~/core/Namespace//payments", Locations: []deployment.Location{{Path: "k8s/prod/api.yaml", Line: 3}},
	}}
	if !reflect.DeepEqual(got.Gaps, want) {
		t.Errorf("gaps = %+v, want %+v", got.Gaps, want)
	}
}

// An entity that another kind of binding maps does not take the place of the `infraResource` binding.
func TestPlacementsReadsTheBindingOfTheKindOfTheEnd(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	api, ns := w.productionPair()
	w.bindAPI(api)
	w.bind(ns, "prod-cluster", "bind-ns", binding.MatchWorkload)

	got := w.placements(nil, "production")

	wantNoPlacements(t, &got)
	wantGapShapes(t, &got, plcGapShape{
		Environment: "production", Reason: deployment.ReasonBindingGap, Side: deployment.SideHostingTarget, Node: "orders-service",
	})
}

// An unbound workload stays visible as a gap and is never attached to a node by its name (EVD-05).
func TestPlacementsUnboundWorkloadIsAGapWithoutNode(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	w.workload("orders-service", "production", "k8s/prod/orders.yaml", "orders-service")
	ns := w.namespace("ns", "production", "k8s/prod/ns.yaml")
	w.bindNamespace(ns)

	got := w.placements(nil, "production")

	wantNoPlacements(t, &got)

	want := []deployment.PlacementGap{{
		Environment: "production", Reason: deployment.ReasonBindingGap, Side: deployment.SideWorkload, Node: "",
		Identity:  "k8s:production/~/apps/Deployment/payments/orders-service",
		Locations: []deployment.Location{{Path: "k8s/prod/orders.yaml", Line: 3}},
	}}
	if !reflect.DeepEqual(got.Gaps, want) {
		t.Errorf("gaps = %+v, want %+v", got.Gaps, want)
	}
}

// Scenario: Ambiguous workload is a gap, one for each node that a binding names. No binding wins by order.
func TestPlacementsAmbiguousWorkloadIsAGap(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	api, ns := w.productionPair()
	w.bind(api, "orders-service", "bind-orders", binding.MatchWorkload)
	w.bind(api, "billing-service", "bind-billing", binding.MatchWorkload)
	w.bindNamespace(ns)
	w.set.Ambiguous[api] = []string{"bind-billing", "bind-orders"}

	got := w.placements(nil, "production")

	wantNoPlacements(t, &got)

	ambiguousWorkload := func(node string) plcGapShape {
		return plcGapShape{Environment: "production", Reason: deployment.ReasonAmbiguousBinding, Side: deployment.SideWorkload, Node: node}
	}

	wantGapShapes(t, &got, ambiguousWorkload("billing-service"), ambiguousWorkload("orders-service"))
}

func TestPlacementsAmbiguousHostingTargetIsAGap(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	api, ns := w.productionPair()
	w.bindAPI(api)
	w.bind(ns, "prod-cluster", "bind-ns-a", binding.MatchInfraResource)
	w.bind(ns, "edge-cluster", "bind-ns-b", binding.MatchInfraResource)
	w.set.Ambiguous[ns] = []string{"bind-ns-a", "bind-ns-b"}

	got := w.placements(nil, "production")

	wantNoPlacements(t, &got)
	wantGapShapes(t, &got, plcGapShape{
		Environment: "production", Reason: deployment.ReasonAmbiguousBinding, Side: deployment.SideHostingTarget, Node: "orders-service",
	})
}

// Scenario: Reverse direction. The workload is bound to the container and the hosting target to the deployed node.
func TestPlacementsReverseRecord(t *testing.T) {
	t.Parallel()

	build := func() *plcWorld {
		w := newPlcWorld()
		reports := w.workload("reports", "production", "k8s/prod/reports.yaml", "reports")
		ns := w.namespace("ns", "production", "k8s/prod/ns.yaml")
		w.bind(reports, "prod-cluster", "bind-reports", binding.MatchWorkload)
		w.bind(ns, "api-service", "bind-ns", binding.MatchInfraResource)

		return w
	}

	t.Run("with_the_declared_relationship", func(t *testing.T) {
		t.Parallel()

		got := build().placements(modelDeployedIn("api-service", "prod-cluster"), "production")

		want := []deployment.Placement{{
			Node: "api-service", Container: "prod-cluster", Environment: "production", Workload: "reports", HostingTarget: "ns", Reverse: true,
			Locations: []deployment.Location{{Path: "k8s/prod/reports.yaml", Line: 3}},
		}}
		if !reflect.DeepEqual(got.Placements, want) {
			t.Errorf("placements = %+v, want %+v", got.Placements, want)
		}

		wantNoGaps(t, &got)
	})

	t.Run("without_a_declared_relationship_the_fact_is_forward", func(t *testing.T) {
		t.Parallel()

		got := build().placements(nil, "production")

		wantPlacementShapes(t, &got, plcPlacementShape{
			Environment: "production", Node: "prod-cluster", Container: "api-service", HostingTarget: "ns",
		})
	})
}

// A fact that no declared environment owns gives the gap environment-unresolved and no record.
func TestPlacementsFactWithoutEnvironmentIsAGap(t *testing.T) {
	t.Parallel()

	for _, env := range []string{"", observation.EnvironmentUnresolved} {
		t.Run("environment_"+env, func(t *testing.T) {
			t.Parallel()

			w := newPlcWorld()
			api := w.workload("api", env, "k8s/other/api.yaml", "api")
			ns := w.namespace("ns", env, "k8s/other/ns.yaml")
			w.bindAPI(api)
			w.bindNamespace(ns)

			got := w.placements(nil, "production", "staging")

			wantNoPlacements(t, &got)
			wantGapShapes(t, &got, plcGapShape{
				Reason: deployment.ReasonEnvironmentUnresolved, Side: deployment.SideWorkload, Node: "orders-service",
			})
		})
	}
}

// A workspace without declared environments shows no observed placement: its facts have no environment.
func TestPlacementsWithoutDeclaredEnvironmentsHaveNoRecords(t *testing.T) {
	t.Parallel()

	w := newPlcWorld()
	api := w.workload("api", "", "k8s/api.yaml", "api")
	ns := w.namespace("ns", "", "k8s/ns.yaml")
	w.bindAPI(api)
	w.bindNamespace(ns)

	got := w.placements(nil)

	wantNoPlacements(t, &got)
	wantGapShapes(t, &got, plcGapShape{
		Reason: deployment.ReasonEnvironmentUnresolved, Side: deployment.SideWorkload, Node: "orders-service",
	})
}

// plcSortingWorld declares the workloads of two nodes in two environments, in an order that is not the sorted one.
func plcSortingWorld() *plcWorld {
	w := newPlcWorld()

	for _, c := range []struct{ id, env, file, node string }{
		{"w-b-staging", "staging", "k8s/staging/b.yaml", "b-service"},
		{"w-a-staging", "staging", "k8s/staging/a.yaml", "a-service"},
		{"w-b-prod", "production", "k8s/prod/b.yaml", "b-service"},
		{"w-a-prod", "production", "k8s/prod/a.yaml", "a-service"},
	} {
		w.workload(c.id, c.env, c.file, c.id)
		w.bind(c.id, c.node, "bind-"+c.id, binding.MatchWorkload)
	}

	for _, env := range []string{"production", "staging"} {
		id := "ns-" + env
		w.namespace(id, env, "k8s/"+env+"/ns.yaml")
		w.bind(id, env+"-cluster", "bind-"+id, binding.MatchInfraResource)
	}

	return w
}

// wantNonNilLists checks that neither list of the set is nil.
func wantNonNilLists(t *testing.T, got *deployment.PlacementSet) {
	t.Helper()

	if got.Placements == nil || got.Gaps == nil {
		t.Errorf("a nil list in %#v", got)
	}
}

// wantNonNilEmpty checks that both lists of the set are empty and not nil.
func wantNonNilEmpty(t *testing.T, got *deployment.PlacementSet) {
	t.Helper()

	wantNonNilLists(t, got)
	wantNoPlacements(t, got)
	wantNoGaps(t, got)
}

// The result is sorted by environment, node, and container, and never nil.
func TestPlacementsAreSortedAndNeverNil(t *testing.T) {
	t.Parallel()

	empty := deployment.Placements(nil, nil, nil, nil, nil)
	wantNonNilEmpty(t, &empty)

	got := plcSortingWorld().placements(nil, "staging", "production")

	order := make([]string, 0, len(got.Placements))
	for _, p := range got.Placements {
		order = append(order, p.Environment+"/"+p.Node+"/"+p.Container)
	}

	want := []string{
		"production/a-service/production-cluster", "production/b-service/production-cluster",
		"staging/a-service/staging-cluster", "staging/b-service/staging-cluster",
	}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}

	wantNonNilLists(t, &got)
	wantNoGaps(t, &got)
}

// wantInfraGap checks a gap of the infra recording and says whether it is a gap of the hosting target.
func wantInfraGap(t *testing.T, g *deployment.PlacementGap) (hosting bool) {
	t.Helper()

	if g.Identity == "" {
		t.Errorf("gap = %+v", g)
	}

	if len(g.Locations) == 0 {
		t.Errorf("gap = %+v", g)
	}

	if g.Side != deployment.SideHostingTarget {
		if g.Node != "" {
			t.Errorf("an unbound or env-less workload has a node: %+v", g)
		}

		return false
	}

	if g.Reason != deployment.ReasonBindingGap {
		t.Errorf("hosting-target gap = %+v", g)
	}

	if g.Node == "" {
		t.Errorf("hosting-target gap = %+v", g)
	}

	return true
}

// The helper reads no rule result: the rule links of the input do not change the result.
func TestPlacementsIgnoreTheRulesOfTheInput(t *testing.T) {
	t.Parallel()

	model := deployableModel("api-service", "worker-service")

	plain := infraInput(t)
	linked := infraInput(t)
	linked.Rules = []deployment.RuleLink{
		{RuleID: "INFRA-PLC-001", ObligationID: "ob-1", Verdict: "violation", Subject: "api-service", Environment: "production"},
		{RuleID: "INFRA-PLC-002", ObligationID: "ob-2", Verdict: "pass", Subject: "api-service", Environment: "staging"},
	}

	got := deployment.Placements(plain.Evidence, plain.BindingSet, plain.Bindings, plain.Declared, model)
	again := deployment.Placements(linked.Evidence, linked.BindingSet, linked.Bindings, linked.Declared, model)

	if !reflect.DeepEqual(got, again) {
		t.Errorf("with rules = %+v\nwithout rules = %+v", again, got)
	}

	// The infra recording binds the api workloads, not their namespaces: the placements are gaps of the hosting target.
	if len(got.Placements) != 0 || len(got.Gaps) == 0 {
		t.Fatalf("infra placements = %+v", got)
	}

	hosting := 0

	for i := range got.Gaps {
		if wantInfraGap(t, &got.Gaps[i]) {
			hosting++
		}
	}

	if hosting == 0 {
		t.Errorf("no gap of an unbound hosting target: %+v", got.Gaps)
	}
}
