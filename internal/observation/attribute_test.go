package observation_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

// attrEqual reports an error when got is not want.
func attrEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// attrNotEqual reports an error when got is want.
func attrNotEqual[T comparable](t *testing.T, what string, got, other T) {
	t.Helper()

	if got == other {
		t.Errorf("%s = %v, want it to differ from %v", what, got, other)
	}
}

// attrRequireEqual stops the test when got is not want.
func attrRequireEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// attrScopedID gives the scoped identity of a node that must have one.
func attrScopedID(t *testing.T, node *observation.ObservedNode, cluster string) observation.ScopedID {
	t.Helper()

	id, ok, err := observation.ScopedIDOf(node, cluster)
	if err != nil {
		t.Fatalf("ScopedIDOf(%s): %v", node.QualifiedName, err)
	}

	if !ok {
		t.Fatalf("ScopedIDOf(%s): the node has no obj_key", node.QualifiedName)
	}

	return id
}

// attributeByPrefix attributes a path to each environment with a source prefix, in declaration order.
func attributeByPrefix(envs ...[2]string) func(string) []string {
	return func(path string) []string {
		var out []string

		for _, e := range envs {
			if strings.HasPrefix(path, e[1]) {
				out = append(out, e[0])
			}
		}

		return out
	}
}

var testEnvs = attributeByPrefix(
	[2]string{"production", "k8s/prod/"}, [2]string{"production", "k8s/base/"},
	[2]string{"staging", "k8s/staging/"}, [2]string{"staging", "k8s/base/"},
)

func resourceNode(id, file, objKey string) observation.ObservedNode {
	return observation.ObservedNode{
		ObservedID: id, Label: "Resource", FilePath: file,
		QualifiedName: strings.ReplaceAll(file, "/", ".") + "::" + objKey,
		Properties:    map[string]string{"obj_key": objKey},
	}
}

func infraEdge(id, from, to string, kind observation.CanonicalEdgeKind, line int) *observation.EvidenceEdge {
	return &observation.EvidenceEdge{
		ID: id, From: from, To: to, Kind: kind, Properties: map[string]string{"line": "1"},
		SourceSpan: &observation.SourceSpan{Path: "unused", Line: line},
	}
}

func newResult(nodes []observation.ObservedNode, edges ...*observation.EvidenceEdge) *observation.EvidenceQueryResult {
	res := &observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{}, Edges: edges}
	for i := range nodes {
		res.Nodes[nodes[i].ObservedID] = nodes[i]
	}

	return res
}

func TestAttributeSingleEnvironmentKeepsIDsAndSetsEnvironment(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("n1", "k8s/staging/deployment.yaml", "apps/Deployment/payments/api"),
			resourceNode("n2", "k8s/staging/service.yaml", "core/Service/payments/api"),
		},
		infraEdge("e1", "n2", "n1", observation.KindSelectsWorkload, 3),
	)

	sum := observation.AttributeEnvironments(res, testEnvs)

	edge := res.Edges[0]
	attrEqual(t, "edge environment", edge.Environment, "staging")
	attrEqual(t, "edge ID", edge.ID, "e1")
	attrEqual(t, "edge from", edge.From, "n2")
	attrEqual(t, "edge to", edge.To, "n1")

	attrEqual(t, "n1 environment", res.Nodes["n1"].Environment, "staging")
	attrEqual(t, "n2 environment", res.Nodes["n2"].Environment, "staging")
	attrEqual(t, "node count", len(res.Nodes), 2)

	attrEqual(t, "summary", sum, observation.AttributionSummary{Attributed: 1})
}

// requireFactPerEnvironment gives the fact of the production and of the staging instance of a shared file.
func requireFactPerEnvironment(t *testing.T, res *observation.EvidenceQueryResult) (prod, stage *observation.EvidenceEdge) {
	t.Helper()

	byEnv := map[string]*observation.EvidenceEdge{}
	for _, e := range res.Edges {
		byEnv[e.Environment] = e
	}

	prod, stage = byEnv["production"], byEnv["staging"]
	if prod == nil || stage == nil {
		t.Fatalf("environments: %v", byEnv)
	}

	return prod, stage
}

// checkInstanceFacts checks that each fact of a shared file refers to its own node instance.
func checkInstanceFacts(t *testing.T, res *observation.EvidenceQueryResult, prod, stage *observation.EvidenceEdge) {
	t.Helper()

	attrNotEqual(t, "fact IDs of the instances", prod.ID, stage.ID)
	attrNotEqual(t, "source nodes of the instances", prod.From, stage.From)
	attrEqual(t, "production target", prod.To, "img")
	attrEqual(t, "staging target", stage.To, "img")

	if observation.EdgeKeyOf(prod) == observation.EdgeKeyOf(stage) {
		t.Error("the fact identity must include the environment")
	}

	for env, e := range map[string]*observation.EvidenceEdge{"production": prod, "staging": stage} {
		n, ok := res.Nodes[e.From]
		attrEqual(t, env+": node found", ok, true)
		attrEqual(t, env+": node environment", n.Environment, env)
		attrEqual(t, env+": node qualified name", n.QualifiedName, res.Nodes["base"].QualifiedName)
	}
}

func TestAttributeSharedBaseFileGivesOneFactPerEnvironment(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("base", "k8s/base/deployment.yaml", "apps/Deployment/?/gateway"),
			{ObservedID: "img", Label: "Image", QualifiedName: "__image__registry.example.com/gw", FilePath: ""},
		},
		infraEdge("e1", "base", "img", observation.KindUsesImage, 3),
	)

	sum := observation.AttributeEnvironments(res, testEnvs)

	attrRequireEqual(t, "edge count", len(res.Edges), 2)
	attrRequireEqual(t, "attributed", sum.Attributed, 2)
	attrRequireEqual(t, "fanned out", sum.FannedOut, 2)

	prod, stage := requireFactPerEnvironment(t, res)
	checkInstanceFacts(t, res, prod, stage)

	// The identity of the two instances differs only by environment.
	a := attrScopedID(t, new(res.Nodes[prod.From]), "c1")
	b := attrScopedID(t, new(res.Nodes[stage.From]), "c2")

	attrEqual(t, "production instance environment", a.Environment, "production")
	attrEqual(t, "staging instance environment", b.Environment, "staging")
	attrEqual(t, "instance names", a.Name, b.Name)
	attrNotEqual(t, "instance identities", a, b)

	// The Image node has no environment.
	attrEqual(t, "environment of the unscoped Image node", res.Nodes["img"].Environment, "")

	// The properties are not shared between instances.
	prod.Properties["x"] = "1"

	attrEqual(t, "property of the other instance", stage.Properties["x"], "")
}

func TestAttributeUnmatchedFileIsUnresolved(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("n1", "manifests/loose.yaml", "apps/Deployment/payments/api"),
			resourceNode("n2", "manifests/other.yaml", "core/Service/payments/api"),
		},
		infraEdge("e1", "n2", "n1", observation.KindSelectsWorkload, 1),
	)

	sum := observation.AttributeEnvironments(res, testEnvs)

	attrEqual(t, "edge environment", res.Edges[0].Environment, observation.EnvironmentUnresolved)
	attrEqual(t, "unresolved", sum.Unresolved, 1)
	attrEqual(t, "attributed", sum.Attributed, 0)
	attrEqual(t, "node environment", res.Nodes["n1"].Environment, observation.EnvironmentUnresolved)

	id := attrScopedID(t, new(res.Nodes["n1"]), "")

	attrEqual(t, "environment resolved", id.EnvironmentResolved(), false)
	attrEqual(t, "environment part", id.Environment, observation.UnresolvedPart)

	// No environment is a default: nothing says production.
	if strings.Contains(id.String(), "production") {
		t.Errorf("default environment: %s", id)
	}
}

func TestAttributeA04SameNamesInTwoEnvironments(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("p", "k8s/prod/billing.yaml", "apps/Deployment/payments/billing"),
			resourceNode("s", "k8s/staging/billing.yaml", "apps/Deployment/payments/billing"),
			{ObservedID: "img", Label: "Image", QualifiedName: "__image__registry.example.com/billing"},
		},
		infraEdge("e1", "p", "img", observation.KindUsesImage, 1),
		infraEdge("e2", "s", "img", observation.KindUsesImage, 1),
	)

	observation.AttributeEnvironments(res, testEnvs)

	decls, err := observation.DeclarationsOf(res.Nodes, func(env string) string { return "cluster-" + env })
	if err != nil {
		t.Fatal(err)
	}

	attrRequireEqual(t, "declaration count", len(decls), 2)
	attrNotEqual(t, "declaration IDs", decls[0].ID, decls[1].ID)

	want := []string{
		"k8s:production/cluster-production/apps/Deployment/payments/billing",
		"k8s:staging/cluster-staging/apps/Deployment/payments/billing",
	}

	got := []string{decls[0].ID.String(), decls[1].ID.String()}

	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("identities %v, want %v", got, want)
	}

	if conflicts := observation.Conflicts(observation.GroupDeclarations(decls)); len(conflicts) != 0 {
		t.Errorf("the same name in two environments is no conflict: %+v", conflicts)
	}
}

func TestAttributeUnscopedEndpointTakesTheEnvironmentOfTheScopedOne(t *testing.T) {
	t.Parallel()

	// The Route node has a file path (the first Ingress that CBM saw) that must be ignored.
	route := observation.ObservedNode{ObservedID: "route", Label: "Route", QualifiedName: "__route__ingress__api.example.com/", FilePath: "k8s/prod/ingress.yaml"}
	res := newResult(
		[]observation.ObservedNode{
			route,
			resourceNode("svcp", "k8s/prod/service.yaml", "core/Service/payments/api"),
			resourceNode("svcs", "k8s/staging/service.yaml", "core/Service/payments-staging/api"),
		},
		infraEdge("r1", "route", "svcp", observation.KindRoutesTo, 1),
		infraEdge("r2", "route", "svcs", observation.KindRoutesTo, 1),
	)

	observation.AttributeEnvironments(res, testEnvs)

	envs := map[string]string{}
	for _, e := range res.Edges {
		envs[e.To] = e.Environment
	}

	attrEqual(t, "O7: routes-to environment of svcp", envs["svcp"], "production")
	attrEqual(t, "O7: routes-to environment of svcs", envs["svcs"], "staging")
	attrEqual(t, "environment of the unscoped Route node", res.Nodes["route"].Environment, "")
	attrEqual(t, "node count (unscoped nodes must not be cloned)", len(res.Nodes), 3)
}

func TestAttributeEdgeBetweenDisjointEnvironmentsIsUnresolved(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("p", "k8s/prod/a.yaml", "core/Service/ns/a"),
			resourceNode("s", "k8s/staging/b.yaml", "apps/Deployment/ns/b"),
		},
		infraEdge("x", "p", "s", observation.KindSelectsWorkload, 1),
	)

	sum := observation.AttributeEnvironments(res, testEnvs)

	attrRequireEqual(t, "edge count", len(res.Edges), 1)
	attrEqual(t, "edge environment", res.Edges[0].Environment, observation.EnvironmentUnresolved)
	attrEqual(t, "unresolved", sum.Unresolved, 1)
}

func TestAttributeIntersectsSharedEnvironments(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("shared", "k8s/base/app.yaml", "apps/Deployment/?/app"),
			resourceNode("prod", "k8s/prod/svc.yaml", "core/Service/payments/app"),
		},
		infraEdge("x", "prod", "shared", observation.KindSelectsWorkload, 1),
	)

	observation.AttributeEnvironments(res, testEnvs)

	attrRequireEqual(t, "edge count", len(res.Edges), 1)
	attrRequireEqual(t, "edge environment", res.Edges[0].Environment, "production")

	// Production is the first environment of the shared node, the instance keeps the ID.
	attrEqual(t, "edge target", res.Edges[0].To, "shared")

	// The staging instance of the shared node exists but has no fact with the production service.
	n, ok := res.Nodes["shared@staging"]
	attrEqual(t, "staging instance found", ok, true)
	attrEqual(t, "staging instance environment", n.Environment, "staging")
}

func TestAttributeFactInSecondSharedEnvironmentRefersToItsInstance(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			resourceNode("shared", "k8s/base/app.yaml", "apps/Deployment/?/app"),
			resourceNode("stg", "k8s/staging/svc.yaml", "core/Service/payments/app"),
		},
		infraEdge("y", "stg", "shared", observation.KindSelectsWorkload, 1),
	)

	observation.AttributeEnvironments(res, testEnvs)

	attrEqual(t, "edge environment", res.Edges[0].Environment, "staging")
	attrEqual(t, "edge target", res.Edges[0].To, "shared@staging")
}

func TestAttributeLeavesCodeFactsAndTheirNodesAlone(t *testing.T) {
	t.Parallel()

	res := newResult(
		[]observation.ObservedNode{
			{ObservedID: "f1", Label: "Function", QualifiedName: "a.F", FilePath: "src/a.go"},
			{ObservedID: "f2", Label: "Function", QualifiedName: "b.G", FilePath: "k8s/prod/b.go"},
		},
		infraEdge("c1", "f1", "f2", observation.KindCalls, 1),
	)

	sum := observation.AttributeEnvironments(res, testEnvs)

	attrEqual(t, "code edge environment", res.Edges[0].Environment, "")
	attrEqual(t, "code node environment", res.Nodes["f2"].Environment, "")
	attrEqual(t, "summary", sum, observation.AttributionSummary{})
}

func TestAttributeUnsupportedFactAtInfrastructureNodeAndSourcePathFallback(t *testing.T) {
	t.Parallel()

	res := newResult([]observation.ObservedNode{
		resourceNode("p", "k8s/prod/a.yaml", "core/Service/ns/a"),
		{ObservedID: "chart", Label: "Chart", QualifiedName: "__chart__x", FilePath: ""},
	})
	res.Unsupported = []*observation.EvidenceEdge{
		{ID: "u1", From: "p", To: "chart", Kind: observation.KindUnsupported, NativeKind: "FOO"},
	}
	res.Edges = []*observation.EvidenceEdge{
		// Both endpoints unscoped: the source span path decides.
		{
			ID: "i1", From: "chart", To: "chart", Kind: observation.KindInfrastructureDependency,
			SourceSpan: &observation.SourceSpan{Path: "k8s/staging/chart.yaml"},
		},
	}

	observation.AttributeEnvironments(res, testEnvs)

	attrEqual(t, "unsupported fact environment", res.Unsupported[0].Environment, "production")
	attrEqual(t, "source path fallback environment", res.Edges[0].Environment, "staging")
}

func TestScopedIDOfNodeFallsBackToTheQualifiedName(t *testing.T) {
	t.Parallel()

	namespaced := observation.ObservedNode{Label: "Resource", Environment: "staging", QualifiedName: "k8s.staging.d.yaml::apps/Deployment/payments/api"}

	id := attrScopedID(t, &namespaced, "c")

	attrEqual(t, "namespace", id.Namespace, "payments")
	attrEqual(t, "environment", id.Environment, "staging")
}

func TestScopedIDOfDashInTheQualifiedNameIsNotClusterScoped(t *testing.T) {
	t.Parallel()

	// Without the property, `-` in the qualified name never reads as cluster scoped.
	dash := observation.ObservedNode{Label: "Resource", QualifiedName: "k8s.prod.namespace.yaml::core/Namespace/-/payments"}

	id := attrScopedID(t, &dash, "c")

	attrEqual(t, "cluster scoped", id.ClusterScoped(), false)
	attrEqual(t, "namespace resolved", id.NamespaceResolved(), false)
}

func TestScopedIDOfObjKeyPropertyMakesTheNodeClusterScoped(t *testing.T) {
	t.Parallel()

	// With the property it is cluster scoped, even when the file context has a namespace.
	dash := observation.ObservedNode{
		Label: "Resource", QualifiedName: "k8s.prod.namespace.yaml::core/Namespace/-/payments",
		Properties: map[string]string{"obj_key": "core/Namespace/_/payments"},
	}

	id := attrScopedID(t, &dash, "c")

	attrEqual(t, "cluster scoped", id.ClusterScoped(), true)
}

func TestScopedIDOfNodeWithoutObjKeyIsNotScoped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node observation.ObservedNode
	}{
		{name: "kustomization", node: observation.ObservedNode{Label: "Resource", QualifiedName: "kustomize.base.kustomization.Kustomization"}},
		{name: "function", node: observation.ObservedNode{Label: "Function", QualifiedName: "a::apps/Deployment/x/y"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok, _ := observation.ScopedIDOf(&tt.node, ""); ok {
				t.Errorf("%s has no obj_key", tt.node.QualifiedName)
			}
		})
	}
}

func TestScopedIDOfMalformedObjKeyIsAnError(t *testing.T) {
	t.Parallel()

	bad := observation.ObservedNode{Label: "Resource", Properties: map[string]string{"obj_key": "nonsense"}}
	if _, _, err := observation.ScopedIDOf(&bad, ""); err == nil {
		t.Error("a malformed obj_key must be an error")
	}
}
