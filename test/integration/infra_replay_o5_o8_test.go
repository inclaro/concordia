package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

// The observations O5 to O8 of TestInfraObservations: reference USAGE edges, selector joins, routes, and
// deployment actions.

// edgesPropEq gives the edges of one type whose property has the value.
func (s *infraSnapshot) edgesPropEq(edgeType, prop, value string) []*cbmprobe.Edge {
	return s.edgesWhere(edgeType, func(e *cbmprobe.Edge) bool { return e.Prop(prop) == value })
}

// infraO5WantKind is the kind of the target node of a USAGE edge, by reference mode.
var infraO5WantKind = map[string]string{
	"serviceAccountName": "ServiceAccount", "env": "Secret", "envFrom": "ConfigMap", "volume": "PersistentVolumeClaim",
	"tls": "Secret", "parentRef": "Gateway", "roleRef": "Role", "subject": "ServiceAccount",
	"podSelector": "Deployment", "selector": "Deployment", "scaleTarget": "Deployment",
}

// requireInfraO5ReferenceUsage checks the reference USAGE edges and the undeclared targets.
func requireInfraO5ReferenceUsage(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireInfraO5TargetKinds(t, snap)
	requireInfraO5OneEdgePerMode(t, snap)
	requireInfraO5UndeclaredHaveNoNode(t, snap)
	requireInfraO5NamedEdges(t, snap)
	requireInfraO5TerraformRefs(t, snap)
	requireInfraO5CodeUsage(t, snap)
}

// requireInfraO5TargetKinds checks that the reference mode is `ref` and the endpoint kind is the kind of the target
// node, and that every mode occurs.
func requireInfraO5TargetKinds(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	seen := map[string]bool{}

	for _, e := range snap.graph.EdgesOf(infraEdgeUsage) {
		ref := e.Prop("ref")
		if want, ok := infraO5WantKind[ref]; ok {
			requireProp(t, "target kind of "+ref, e.To.Prop("kind"), want)

			seen[ref] = true
		}

		if ref == "image" {
			requireProp(t, "image target label", e.To.Label(), infraLabelImage)

			seen[ref] = true
		}
	}

	for ref := range infraO5WantKind {
		if !seen[ref] {
			t.Errorf("no USAGE edge with ref %s", ref)
		}
	}

	if !seen["image"] {
		t.Error("no USAGE edge with ref image")
	}
}

// countInfraEdgesWithRef counts the edges with the reference mode.
func countInfraEdgesWithRef(edges []*cbmprobe.Edge, ref string) int {
	n := 0

	for _, e := range edges {
		if e.Prop("ref") == ref {
			n++
		}
	}

	return n
}

// requireInfraO5OneEdgePerMode checks that a Secret and a ConfigMap differ by the target kind, not by `ref`, and that
// only declared targets give an edge: one per reference mode, none for the undeclared ones.
func requireInfraO5OneEdgePerMode(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	deployment := snap.resource(t, infraProdDeploy, "apps/Deployment/payments/api")

	for ref, want := range map[string]int{"serviceAccountName": 1, "env": 1, "envFrom": 1, "volume": 1} {
		requireCount(t, "edges with ref "+ref+" (the second target is undeclared)", countInfraEdgesWithRef(snap.edgesFrom(infraEdgeUsage, deployment), ref), want)
	}
}

func requireInfraO5UndeclaredHaveNoNode(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	for _, undeclared := range []string{
		"core/Secret/payments/missing-secret", "core/ConfigMap/payments/missing-config", "core/PersistentVolumeClaim/payments/missing-pvc",
		"core/ServiceAccount/payments/ghost-sa", "core/Secret/payments/missing-tls", "gateway.networking.k8s.io/Gateway/payments/missing-gateway",
		"core/ServiceAccount/payments-staging/staging-sa",
	} {
		requireCount(t, "nodes of the undeclared "+undeclared, len(snap.resourcesByKey(undeclared)), 0)
	}
}

func requireInfraO5NamedEdges(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireCount(t, "tls edges of the Ingress", len(snap.edgesPropEq(infraEdgeUsage, "ref", "tls")), 1)
	requireCount(t, "parentRef edges of the HTTPRoute", len(snap.edgesPropEq(infraEdgeUsage, "ref", "parentRef")), 1)
	requireCount(t, "subject edges of the RoleBinding", len(snap.edgesPropEq(infraEdgeUsage, "ref", "subject")), 1)
}

// requireInfraO5TerraformRefs checks that Terraform references are USAGE with via terraform and ref var, module,
// output or resource.
func requireInfraO5TerraformRefs(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	refs := map[string]bool{}

	for _, e := range snap.edgesPropEq(infraEdgeUsage, "via", "terraform") {
		refs[e.Prop("ref")] = true
	}

	for _, ref := range []string{"var", "module", "output", "resource"} {
		if !refs[ref] {
			t.Errorf("no Terraform USAGE edge with ref %s", ref)
		}
	}
}

// requireInfraO5CodeUsage checks that code usage is USAGE with only `callee`.
func requireInfraO5CodeUsage(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	code := snap.edgesFrom(infraEdgeUsage, snap.fileNode(t, "app/main.go"))
	requireCount(t, "code USAGE edges", len(code), 1)

	if got := propNames(code); !slices.Equal(got, []string{"callee"}) {
		t.Errorf("code USAGE properties = %v, want [callee]", got)
	}
}

// requireInfraO6SelectorJoins checks that selector joins are scoped to the directory and silent about gaps.
func requireInfraO6SelectorJoins(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireInfraO6JoinsInDirectory(t, snap)
	requireInfraO6OtherDirectoryNotJoined(t, snap)
	requireInfraO6CompetingDeclaration(t, snap)
	requireInfraO6EmptySelector(t, snap)
}

// requireInfraO6JoinsInDirectory checks that a Service, a NetworkPolicy and a PDB join a workload in another file of
// the same directory.
func requireInfraO6JoinsInDirectory(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	api := snap.resource(t, infraProdDeploy, "apps/Deployment/payments/api")
	apiService := snap.resource(t, "k8s/prod/service.yaml", "core/Service/payments/api")

	selector := snap.edgesFrom(infraEdgeMaps, apiService)
	requireCount(t, "INFRA_MAPS edges of the Service", len(selector), 1)
	requireProp(t, "kind", selector[0].Prop("kind"), "selector")
	requireProp(t, "service", selector[0].Prop("service"), "api")
	requireProp(t, "workload", selector[0].Prop("workload"), "api")

	if selector[0].To != api {
		t.Errorf("the Service joined %s", selector[0].To.QualifiedName)
	}

	policy := snap.resource(t, "k8s/prod/networkpolicy.yaml", "networking.k8s.io/NetworkPolicy/payments/api-allow-web")
	podSelector := snap.edgesFrom(infraEdgeUsage, policy)

	// The `matchExpressions` of the ingress `from` rule gave no further edge.
	requireCount(t, "edges of the NetworkPolicy with matchExpressions", len(podSelector), 1)
	requireProp(t, "podSelector ref", podSelector[0].Prop("ref"), "podSelector")

	if podSelector[0].To != api {
		t.Errorf("the NetworkPolicy joined %s", podSelector[0].To.QualifiedName)
	}

	pdb := snap.edgesFrom(infraEdgeUsage, snap.resource(t, "k8s/prod/pdb.yaml", "policy/PodDisruptionBudget/payments/api-pdb"))
	requireCount(t, "edges of the PDB", len(pdb), 1)
	requireProp(t, "selector ref", pdb[0].Prop("ref"), "selector")
}

// requireInfraO6OtherDirectoryNotJoined checks that a labelled workload in another directory (k8s/staging/), in the
// same namespace, is not joined.
func requireInfraO6OtherDirectoryNotJoined(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	worker := snap.resource(t, "k8s/staging/worker.yaml", "apps/Deployment/payments/worker")
	requireProp(t, "worker namespace", worker.Prop("namespace"), "payments")
	requireProp(t, "worker labels", worker.Prop("labels"), "app=worker")

	for _, e := range snap.graph.Edges {
		if e.To == worker && slices.Contains([]string{infraEdgeUsage, infraEdgeMaps}, e.Type) {
			t.Errorf("the worker in another directory was joined by %s from %s", e.Type, e.From.QualifiedName)
		}
	}
}

// requireInfraO6CompetingDeclaration checks that when two files declare one obj_key, the selector joins only one of
// them.
func requireInfraO6CompetingDeclaration(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	reportsService := snap.resource(t, "k8s/prod/service.yaml", "core/Service/payments/reports")
	joined := snap.edgesFrom(infraEdgeMaps, reportsService)
	requireCount(t, "reports workloads joined", len(joined), 1)
	requireProp(t, "joined competing declaration", joined[0].To.FilePath, "k8s/prod/reports.yaml")
}

// infraNodePropNames gives the sorted property names of a node.
func infraNodePropNames(n *cbmprobe.Node) []string {
	names := make([]string, 0, len(n.Props))
	for name := range n.Props {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// requireInfraO6EmptySelector checks that an empty `podSelector: {}` gives no edge, and that no node property records
// the selector content.
func requireInfraO6EmptySelector(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	policy := snap.resource(t, "k8s/prod/networkpolicy.yaml", "networking.k8s.io/NetworkPolicy/payments/api-allow-web")
	denyAll := snap.resource(t, "k8s/prod/networkpolicy.yaml", "networking.k8s.io/NetworkPolicy/payments/default-deny")
	requireCount(t, "edges of the empty selector", len(snap.edgesFrom(infraEdgeUsage, denyAll))+len(snap.edgesFrom(infraEdgeMaps, denyAll)), 0)

	if !slices.Equal(infraNodePropNames(denyAll), infraNodePropNames(policy)) {
		t.Errorf("the empty selector and the selector differ by node properties: %v, %v", infraNodePropNames(denyAll), infraNodePropNames(policy))
	}

	for _, name := range infraNodePropNames(policy) {
		if strings.Contains(strings.ToLower(name), "selector") || strings.Contains(name, "match") {
			t.Errorf("a node property records the selector: %s", name)
		}
	}
}

// requireInfraO7Routes checks the routes.
func requireInfraO7Routes(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireInfraO7Mappings(t, snap)

	toService := requireInfraO7HostedRoute(t, snap)

	requireInfraO7WildcardRoute(t, snap)
	requireInfraO7SharedRoute(t, snap, toService)
}

func requireInfraO7Mappings(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	service := snap.resource(t, "k8s/prod/service.yaml", "core/Service/payments/api")
	ingress := snap.resource(t, "k8s/prod/ingress.yaml", "networking.k8s.io/Ingress/payments/api")
	httpRoute := snap.resource(t, "k8s/prod/httproute.yaml", "gateway.networking.k8s.io/HTTPRoute/payments/api")

	from := snap.edgesFrom(infraEdgeMaps, ingress)
	requireCount(t, "INFRA_MAPS edges of the Ingress", len(from), 1)
	requireProp(t, "Ingress kind", from[0].Prop("kind"), "ingress")

	if from[0].To != service {
		t.Errorf("the Ingress maps to %s", from[0].To.QualifiedName)
	}

	from = snap.edgesFrom(infraEdgeMaps, httpRoute)
	requireCount(t, "INFRA_MAPS edges of the HTTPRoute", len(from), 1)
	requireProp(t, "HTTPRoute kind", from[0].Prop("kind"), "route")
}

// requireInfraO7HostedRoute checks that a Route node has host, path, source and via, and a qualified name with no
// project and no namespace. It gives the edges of the Route node.
func requireInfraO7HostedRoute(t *testing.T, snap *infraSnapshot) []*cbmprobe.Edge {
	t.Helper()

	hosted := snap.nodeByQN(t, "__route__ingress__api.example.com/")
	requireProp(t, "Route label", hosted.Label(), "Route")

	for name, want := range map[string]string{"host": "api.example.com", "path": "/", "source": "k8s", "via": "ingress"} {
		requireProp(t, "Route "+name, hosted.Prop(name), want)
	}

	toService := snap.edgesFrom(infraEdgeMaps, hosted)
	requireCount(t, "INFRA_MAPS edges of the Route node", len(toService), 2)

	kinds := []string{toService[0].Prop("kind"), toService[1].Prop("kind")}
	slices.Sort(kinds)

	if !slices.Equal(kinds, []string{"ingress", "ingress"}) {
		t.Errorf("Route node edge kinds = %v", kinds)
	}

	return toService
}

// requireInfraO7WildcardRoute checks that an HTTPRoute without hostname has the host `*`.
func requireInfraO7WildcardRoute(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	wild := snap.nodeByQN(t, "__route__ingress__*/")
	requireProp(t, "wildcard host", wild.Prop("host"), "*")
	requireProp(t, "wildcard via", wild.Prop("via"), "httproute")

	wildEdges := snap.edgesFrom(infraEdgeMaps, wild)
	requireCount(t, "INFRA_MAPS edges of the wildcard Route", len(wildEdges), 1)
	requireProp(t, "wildcard kind", wildEdges[0].Prop("kind"), "httproute")
}

// requireInfraO7SharedRoute checks that two environments with the same host and path share one Route node; the
// Ingress Resource is the scoped identity.
func requireInfraO7SharedRoute(t *testing.T, snap *infraSnapshot, toService []*cbmprobe.Edge) {
	t.Helper()

	requireCount(t, "Route nodes of the shared host", len(snap.graph.NodesOf("Route")), 2)

	ingresses := 0

	for _, n := range snap.graph.NodesOf(infraKindResource) {
		if n.Prop("kind") == "Ingress" {
			ingresses++
		}
	}

	requireCount(t, "Ingress Resource nodes behind the Route nodes", ingresses, 2)

	for _, e := range toService {
		if e.To.Prop("kind") != "Service" {
			t.Errorf("the Route node maps to %s", e.To.Prop("kind"))
		}
	}

	namespaces := map[string]bool{}
	for _, e := range toService {
		namespaces[e.To.Prop("namespace")] = true
	}

	if !namespaces["payments"] || !namespaces["payments-staging"] {
		t.Errorf("the shared Route node must map to the Services of both environments, got %v", namespaces)
	}
}

// requireInfraO8DeploymentActions checks the DEPLOYS edges of the deployment script.
func requireInfraO8DeploymentActions(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	script := snap.nodeByQN(t, qn("scripts.deploy"))

	byVia := map[string]*cbmprobe.Edge{}

	for _, e := range snap.edgesFrom(infraEdgeDeploys, script) {
		byVia[e.Prop("via")] = e
	}

	if byVia["kubectl"] == nil {
		t.Fatal("no DEPLOYS edge with via kubectl")
	}

	requireInfraO8Kubectl(t, byVia["kubectl"])

	if byVia["helm"] == nil {
		t.Fatal("no DEPLOYS edge with via helm")
	}

	requireInfraO8Helm(t, snap, byVia["helm"])

	// The edges from the script go to no Resource directly.
	for _, e := range snap.edgesFrom(infraEdgeDeploys, script) {
		if e.To.Label() == infraKindResource {
			t.Errorf("a script DEPLOYS edge goes to a Resource: %s", e.To.QualifiedName)
		}
	}
}

// requireInfraO8Kubectl checks that the target of `kubectl apply -f` is a file, not the resources in that file.
func requireInfraO8Kubectl(t *testing.T, kubectl *cbmprobe.Edge) {
	t.Helper()

	requireProp(t, "kubectl target label", kubectl.To.Label(), infraLabelFile)
	requireProp(t, "kubectl target path", kubectl.To.FilePath, infraProdDeploy)
	requireProp(t, "kubectl path", kubectl.Prop("path"), infraProdDeploy)
	requireProp(t, "kubectl line", kubectl.Prop("line"), "6")
}

func requireInfraO8Helm(t *testing.T, snap *infraSnapshot, helm *cbmprobe.Edge) {
	t.Helper()

	requireProp(t, "helm target label", helm.To.Label(), infraLabelChart)
	requireProp(t, "helm chart", helm.Prop("chart"), "charts/api")
	requireProp(t, "helm line", helm.Prop("line"), "7")

	chart := snap.edgesFrom(infraEdgeDeploys, helm.To)
	requireCount(t, "DEPLOYS edges of the Chart", len(chart), 1)
	requireProp(t, "chart via", chart[0].Prop("via"), "chart")
	requireProp(t, "chart target label", chart[0].To.Label(), infraKindResource)
	requireProp(t, "chart target obj_key", chart[0].To.Prop("obj_key"), "apps/Deployment/?/<.Release.Name>-api")
}
