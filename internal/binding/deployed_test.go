package binding_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// digest is the image digest that the tests use.
const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// k8sRef is the identity of a Kubernetes object in a manifest: its API group, kind, namespace and name.
type k8sRef struct {
	group, kind, namespace, name string
}

// key gives the `obj_key` of the object.
func (r k8sRef) key() string {
	return r.group + "/" + r.kind + "/" + r.namespace + "/" + r.name
}

// resource makes a `Resource` node of a manifest document in an environment.
func resource(id, env, file string, ref k8sRef) observation.ObservedNode {
	return observation.ObservedNode{
		ObservedID: "cbm:p:" + id, Label: "Resource", Environment: env, FilePath: file,
		QualifiedName: strings.ReplaceAll(file, "/", ".") + "::" + ref.key(),
		Properties:    map[string]string{"obj_key": ref.key()},
	}
}

func imageNode(id, repository string) observation.ObservedNode {
	return observation.ObservedNode{ObservedID: "cbm:p:" + id, Label: "Image", QualifiedName: "__image__" + repository}
}

func workloadBinding(id, ref, namespace, name string) binding.Binding {
	return binding.Binding{
		ID: id, ArchitectureRef: ref, Exclusive: true, Required: true, Source: "t",
		Match: []binding.Match{{
			Kind: binding.MatchWorkload, Environment: "production", APIGroup: "apps", ResourceKind: "Deployment", Namespace: namespace, Name: name,
		}},
	}
}

func matchBinding(id, ref string, m *binding.Match) binding.Binding {
	return binding.Binding{ID: id, ArchitectureRef: ref, Exclusive: true, Source: "t", Match: []binding.Match{*m}}
}

func TestWorkloadMatchesOnlyItsExactIdentity(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		resource("1", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		resource("2", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "payments", "invoicing"}),
		resource("3", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "StatefulSet", "payments", "billing"}),
		resource("4", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "other", "billing"}),
	}

	set := binding.Resolve([]binding.Binding{workloadBinding("w", "billing", "payments", "billing")}, nodes, nil)

	if len(set.ByObserved) != 1 || !slices.Equal(set.ByObserved["cbm:p:1"], []string{"billing"}) {
		t.Fatalf("bound = %v", set.ByObserved)
	}
}

func TestA04WorkloadBindingIsScopedToItsEnvironment(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		resource("stg", "staging", "k8s/staging/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		resource("prd", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		resource("unr", observation.EnvironmentUnresolved, "k8s/other/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		resource("none", "", "k8s/none/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
	}

	set := binding.Resolve([]binding.Binding{workloadBinding("w", "billing", "payments", "billing")}, nodes, nil)

	if len(set.ByObserved) != 1 || len(set.ByObserved["cbm:p:prd"]) != 1 {
		t.Fatalf("bound = %v", set.ByObserved)
	}
}

func TestWorkloadBindingWithoutNamespaceMatchesAnyNamespace(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		resource("1", "production", "a.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		resource("2", "production", "b.yaml", k8sRef{"apps", "Deployment", "other", "billing"}),
	}

	set := binding.Resolve([]binding.Binding{workloadBinding("w", "billing", "", "billing")}, nodes, nil)
	if len(set.ByObserved) != 2 {
		t.Fatalf("bound = %v", set.ByObserved)
	}
}

func TestWorkloadClusterNeedsTheClusterOfTheEnvironment(t *testing.T) {
	t.Parallel()

	b := workloadBinding("w", "billing", "payments", "billing")
	b.Match[0].Cluster = "prod-eu"

	nodes := []observation.ObservedNode{resource("1", "production", "a.yaml", k8sRef{"apps", "Deployment", "payments", "billing"})}

	if set := binding.Resolve([]binding.Binding{b}, nodes, nil); len(set.ByObserved) != 0 {
		t.Errorf("no declared cluster: bound = %v", set.ByObserved)
	}

	clusters := map[string]string{"production": "prod-eu"}
	set := binding.Resolve([]binding.Binding{b}, nodes, nil, binding.WithClusterOf(func(env string) string { return clusters[env] }))

	if len(set.ByObserved) != 1 {
		t.Errorf("matching cluster: bound = %v", set.ByObserved)
	}

	clusters["production"] = "prod-us"

	set = binding.Resolve([]binding.Binding{b}, nodes, nil, binding.WithClusterOf(func(env string) string { return clusters[env] }))
	if len(set.ByObserved) != 0 {
		t.Errorf("other cluster: bound = %v", set.ByObserved)
	}
}

func TestWorkloadOutranksSourceRootAndNoWinnerIsChosenByOrder(t *testing.T) {
	t.Parallel()

	node := resource("1", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"})

	set := binding.Resolve([]binding.Binding{
		bind("root", "by-root", binding.MatchSourceRoot, "k8s/**"),
		workloadBinding("w", "billing", "payments", "billing"),
	}, []observation.ObservedNode{node}, nil)

	if got := set.ByObserved[node.ObservedID]; !slices.Equal(got, []string{"billing"}) || len(set.Conflicts) != 0 {
		t.Fatalf("bound = %v conflicts = %v", got, set.Conflicts)
	}

	if got := set.UsedBy[node.ObservedID]; !slices.Equal(got, []string{"w"}) {
		t.Errorf("used by = %v", got)
	}
}

func TestEachExactKindOutranksTheLowerKinds(t *testing.T) {
	t.Parallel()

	// One resource node that matches several kinds: the highest tier wins.
	node := resource("1", "production", "k8s/prod/ingress.yaml", k8sRef{"networking.k8s.io", "Ingress", "payments", "api"})
	address := "networking.k8s.io/Ingress/payments/api"

	cases := []struct {
		name  string
		lower binding.Binding
		upper binding.Binding
	}{
		{
			"infraResource over sourceRoot",
			bind("lo", "lo", binding.MatchSourceRoot, "k8s/**"),
			matchBinding("hi", "hi", &binding.Match{Kind: binding.MatchInfraResource, Environment: "production", Address: address}),
		},
		{
			"infraResource over route",
			matchBinding("lo", "lo", &binding.Match{Kind: binding.MatchRoute, Environment: "production", Name: "api"}),
			matchBinding("hi", "hi", &binding.Match{Kind: binding.MatchInfraResource, Environment: "production", Address: address}),
		},
		{
			"workload over infraResource",
			matchBinding("lo", "lo", &binding.Match{Kind: binding.MatchInfraResource, Environment: "production", Address: address}),
			matchBinding("hi", "hi", &binding.Match{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Ingress", Name: "api"}),
		},
	}

	for _, tc := range cases {
		for _, order := range [][]binding.Binding{{tc.lower, tc.upper}, {tc.upper, tc.lower}} {
			set := binding.Resolve(order, []observation.ObservedNode{node}, nil)
			if got := set.ByObserved[node.ObservedID]; !slices.Equal(got, []string{"hi"}) {
				t.Errorf("%s: bound = %v", tc.name, got)
			}
		}
	}
}

func TestComposeServiceMatchesFileAndService(t *testing.T) {
	t.Parallel()

	api := observation.ObservedNode{
		ObservedID: "cbm:p:api", Label: "Resource", FilePath: "compose.yaml", QualifiedName: "compose::compose/Service/-/api",
	}
	db := observation.ObservedNode{
		ObservedID: "cbm:p:db", Label: "Resource", FilePath: "compose.yaml", QualifiedName: "compose::compose/Service/-/db",
	}
	other := observation.ObservedNode{
		ObservedID: "cbm:p:other", Label: "Resource", FilePath: "deploy/compose.yaml", QualifiedName: "deploy.compose::compose/Service/-/api",
	}

	set := binding.Resolve([]binding.Binding{
		matchBinding("c", "svc", &binding.Match{Kind: binding.MatchComposeService, File: "compose.yaml", Service: "api"}),
		bind("root", "root", binding.MatchSourceRoot, "**"),
	}, []observation.ObservedNode{api, db, other}, nil)

	if got := set.ByObserved["cbm:p:api"]; !slices.Equal(got, []string{"svc"}) {
		t.Errorf("api = %v", got)
	}

	if got := set.ByObserved["cbm:p:db"]; !slices.Equal(got, []string{"root"}) {
		t.Errorf("db = %v", got)
	}

	if got := set.ByObserved["cbm:p:other"]; !slices.Equal(got, []string{"root"}) {
		t.Errorf("other = %v", got)
	}
}

func TestInfraResourceBindsANamespaceAsAHostingTarget(t *testing.T) {
	t.Parallel()

	prod := resource("prod", "production", "k8s/prod/namespace.yaml", k8sRef{"core", "Namespace", "_", "payments"})
	staging := resource("stg", "staging", "k8s/staging/namespace.yaml", k8sRef{"core", "Namespace", "_", "payments"})
	other := resource("oth", "production", "k8s/prod/namespace.yaml", k8sRef{"core", "Namespace", "_", "orders"})

	set := binding.Resolve([]binding.Binding{
		matchBinding("ns", "payments-container", &binding.Match{Kind: binding.MatchInfraResource, Environment: "production", Address: "core/Namespace/_/payments"}),
	}, []observation.ObservedNode{prod, staging, other}, nil)

	if len(set.ByObserved) != 1 || !slices.Equal(set.ByObserved["cbm:p:prod"], []string{"payments-container"}) {
		t.Fatalf("bound = %v", set.ByObserved)
	}
}

func TestImageTiers(t *testing.T) {
	t.Parallel()

	img := imageNode("img", "registry.example.com/payments/api")
	pinned := observation.ObservedNode{
		ObservedID: "synth:image:registry.example.com/payments/api@" + digest, Label: "Image", QualifiedName: "registry.example.com/payments/api",
		Properties: map[string]string{"repository": "registry.example.com/payments/api", "digest": digest}, Environment: "production",
	}
	repoBinding := matchBinding("repo", "by-repo", &binding.Match{Kind: binding.MatchImage, Repository: "registry.example.com/payments/api:1.2"})
	digestBinding := matchBinding("dig", "by-digest", &binding.Match{Kind: binding.MatchImage, Repository: "registry.example.com/payments/api", Digest: digest})

	set := binding.Resolve([]binding.Binding{
		bind("root", "root", binding.MatchSourceRoot, "**"),
		bind("pkg", "pkg", binding.MatchPackage, "registry.example.."),
		repoBinding, digestBinding,
	}, []observation.ObservedNode{img, pinned}, nil)

	if got := set.ByObserved["cbm:p:img"]; !slices.Equal(got, []string{"by-repo"}) {
		t.Errorf("provider image node = %v", got)
	}

	if got := set.ByObserved[pinned.ObservedID]; !slices.Equal(got, []string{"by-digest"}) {
		t.Errorf("pinned image = %v", got)
	}

	// A binding that names an environment does not match an image with none.
	scoped := matchBinding("env", "scoped", &binding.Match{Kind: binding.MatchImage, Repository: "registry.example.com/payments/api", Environment: "production"})

	set = binding.Resolve([]binding.Binding{scoped}, []observation.ObservedNode{img, pinned}, nil)
	if len(set.ByObserved) != 1 || len(set.ByObserved[pinned.ObservedID]) != 1 {
		t.Errorf("scoped image binding bound = %v", set.ByObserved)
	}
}

func TestAsBuiltKindsDoNotMatchSynthesizedEntities(t *testing.T) {
	t.Parallel()

	entity := observation.ObservedNode{
		ObservedID: "synth:image:app", Label: "Image", QualifiedName: "app", FilePath: "services/billing/Dockerfile",
		Resolution: observation.ResolutionUnbound,
	}

	set := binding.Resolve([]binding.Binding{bind("root", "root", binding.MatchSourceRoot, "services/**")}, []observation.ObservedNode{entity}, nil)
	if len(set.ByObserved) != 0 || set.UnboundObserved != 1 {
		t.Fatalf("set = %+v", set)
	}
}

func TestExclusiveConflictBetweenTwoWorkloadBindingsAndRequiredMiss(t *testing.T) {
	t.Parallel()

	node := resource("1", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"})
	missing := workloadBinding("missing", "ghost", "payments", "ghost")
	bindings := []binding.Binding{
		workloadBinding("a", "x", "payments", "billing"),
		workloadBinding("b", "y", "payments", "billing"),
		missing,
	}

	set := binding.Resolve(bindings, []observation.ObservedNode{node}, nil)
	if len(set.Conflicts) != 1 || len(set.ByObserved) != 0 {
		t.Fatalf("set = %+v", set)
	}

	problems := binding.Problems(&binding.Loaded{Bindings: bindings}, &set)

	codes := make([]string, 0, len(problems))
	for _, p := range problems {
		codes = append(codes, p.Code)
	}

	if !slices.Equal(codes, []string{diag.BND003, diag.BND004}) {
		t.Fatalf("codes = %v", codes)
	}
}

func TestProposedBindingIsSkippedAndNeverRequired(t *testing.T) {
	t.Parallel()

	node := resource("1", "production", "k8s/prod/billing.yaml", k8sRef{"apps", "Deployment", "payments", "billing"})
	proposed := workloadBinding("p", "billing", "payments", "billing")
	proposed.Status = binding.StatusProposed

	set := binding.Resolve([]binding.Binding{proposed}, []observation.ObservedNode{node}, nil)
	if len(set.ByObserved) != 0 || set.MatchCounts["p"] != 0 {
		t.Fatalf("set = %+v", set)
	}

	if problems := binding.Problems(&binding.Loaded{Bindings: []binding.Binding{proposed}}, &set); len(problems) != 0 {
		t.Errorf("problems = %v", problems)
	}

	proposed.Status = binding.StatusActive

	set = binding.Resolve([]binding.Binding{proposed}, []observation.ObservedNode{node}, nil)
	if len(set.ByObserved) != 1 {
		t.Errorf("active binding: bound = %v", set.ByObserved)
	}
}

// checkCardinalityBreach checks the breach of the binding `one` in production, with the two workloads.
func checkCardinalityBreach(t *testing.T, breach *binding.CardinalityBreach) {
	t.Helper()

	if breach.BindingID != "one" || breach.Environment != "production" {
		t.Errorf("breach = %+v", *breach)
	}

	if len(breach.Entities) != 2 || !slices.Equal(breach.ObservedIDs, []string{"cbm:p:1", "cbm:p:2"}) {
		t.Errorf("breach = %+v", *breach)
	}
}

func TestCardinalityOneWithTwoWorkloadsInOneEnvironmentIsAnError(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		resource("1", "production", "a.yaml", k8sRef{"apps", "Deployment", "payments", "billing-a"}),
		resource("2", "production", "b.yaml", k8sRef{"apps", "Deployment", "payments", "billing-b"}),
		resource("3", "staging", "c.yaml", k8sRef{"apps", "Deployment", "payments", "billing-a"}),
	}
	one := matchBinding("one", "billing", &binding.Match{Kind: binding.MatchWorkload, Environment: "production", APIGroup: "apps", ResourceKind: "Deployment", Name: "billing-a"})
	one.Cardinality = binding.CardinalityOne
	one.Match = append(one.Match, binding.Match{Kind: binding.MatchWorkload, Environment: "production", APIGroup: "apps", ResourceKind: "Deployment", Name: "billing-b"})

	set := binding.Resolve([]binding.Binding{one}, nodes, nil)
	if len(set.CardinalityBreaches) != 1 {
		t.Fatalf("breaches = %+v", set.CardinalityBreaches)
	}

	checkCardinalityBreach(t, &set.CardinalityBreaches[0])

	problems := binding.Problems(&binding.Loaded{Bindings: []binding.Binding{one}}, &set)
	if len(problems) != 1 {
		t.Fatalf("problems = %+v", problems)
	}

	if problems[0].Code != diag.BND008 || problems[0].Severity != diag.SeverityError {
		t.Fatalf("problems = %+v", problems)
	}

	for _, want := range []string{"billing-a", "billing-b", "production"} {
		if !strings.Contains(problems[0].Message, want) {
			t.Errorf("message %q lacks %q", problems[0].Message, want)
		}
	}

	if code, _ := diag.ExitCodeOf(diag.BND008); code != diag.InvalidInput {
		t.Errorf("exit code = %d", code)
	}
}

func TestCardinalityOneCountsEntitiesNotDeclarationsOrImageNodes(t *testing.T) {
	t.Parallel()

	// One workload declared in two files is one entity; so are the provider image node and its entity.
	workloadBind := workloadBinding("w", "billing", "payments", "billing")
	workloadBind.Cardinality = binding.CardinalityOne

	imageBind := matchBinding("i", "img", &binding.Match{Kind: binding.MatchImage, Repository: "app"})
	imageBind.Cardinality = binding.CardinalityOne

	nodes := []observation.ObservedNode{
		resource("1", "production", "a.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		resource("2", "production", "b.yaml", k8sRef{"apps", "Deployment", "payments", "billing"}),
		imageNode("img", "app"),
		{ObservedID: "synth:image:app", Label: "Image", QualifiedName: "app", Properties: map[string]string{"repository": "app"}},
	}

	set := binding.Resolve([]binding.Binding{workloadBind, imageBind}, nodes, nil)
	if len(set.CardinalityBreaches) != 0 {
		t.Fatalf("breaches = %+v", set.CardinalityBreaches)
	}
}

func TestManyIsTheDefaultCardinality(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		resource("1", "production", "a.yaml", k8sRef{"apps", "Deployment", "payments", "billing-a"}),
		resource("2", "production", "b.yaml", k8sRef{"apps", "Deployment", "payments", "billing-b"}),
	}
	many := matchBinding("many", "billing", &binding.Match{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "billing-a"})
	many.Match = append(many.Match, binding.Match{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "billing-b"})

	set := binding.Resolve([]binding.Binding{many}, nodes, nil)
	if len(set.CardinalityBreaches) != 0 || len(set.ByObserved) != 2 {
		t.Fatalf("set = %+v", set)
	}
}

// sharedImageBinding makes a non-exclusive binding of the image repository app.
func sharedImageBinding(id, ref string) binding.Binding {
	b := matchBinding(id, ref, &binding.Match{Kind: binding.MatchImage, Repository: "app"})
	b.Exclusive = false

	return b
}

// checkAmbiguityWarning checks that warnings is the one CO-BND-009 warning about the bindings a and b.
func checkAmbiguityWarning(t *testing.T, warnings []diag.Diagnostic) {
	t.Helper()

	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v", warnings)
	}

	if warnings[0].Code != diag.BND009 || warnings[0].Severity != diag.SeverityWarning {
		t.Fatalf("warnings = %+v", warnings)
	}

	if !strings.Contains(warnings[0].Message, "a, b") {
		t.Fatalf("warnings = %+v", warnings)
	}
}

func TestA08NonExclusiveBindingsAtOneTierAreAmbiguous(t *testing.T) {
	t.Parallel()

	img := imageNode("img", "app")
	a := sharedImageBinding("a", "x")
	b := sharedImageBinding("b", "y")
	same := sharedImageBinding("c", "x")

	set := binding.Resolve([]binding.Binding{b, a}, []observation.ObservedNode{img}, nil)
	if len(set.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v", set.Conflicts)
	}

	if got := set.Ambiguous["cbm:p:img"]; !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("ambiguous = %v", set.Ambiguous)
	}

	checkAmbiguityWarning(t, binding.Warnings(&set))

	// Two bindings of one CALM element are not ambiguous.
	set = binding.Resolve([]binding.Binding{a, same}, []observation.ObservedNode{img}, nil)
	if len(set.Ambiguous) != 0 || len(binding.Warnings(&set)) != 0 {
		t.Errorf("same calm element: ambiguous = %v", set.Ambiguous)
	}
}

func TestAgreementOfDeployedBindings(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		resource("1", "production", "a.yaml", k8sRef{"apps", "Deployment", "payments", "billing-a"}),
		resource("2", "production", "b.yaml", k8sRef{"apps", "Deployment", "payments", "billing-b"}),
	}

	build := func(first, second string) binding.Set {
		a := matchBinding("a", "billing", &binding.Match{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "billing-a"})
		a.Agreement = first
		b := matchBinding("b", "billing", &binding.Match{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "billing-b"})
		b.Agreement = second

		return binding.Resolve([]binding.Binding{a, b, bind("root", "code", binding.MatchSourceRoot, "**")}, nodes, nil)
	}

	for _, tc := range []struct{ first, second, want string }{
		{binding.AgreementAny, binding.AgreementAny, binding.AgreementAny},
		{binding.AgreementAny, binding.AgreementAll, binding.AgreementAll},
		{binding.AgreementAll, binding.AgreementAny, binding.AgreementAll},
		{"", "", binding.AgreementAll},
	} {
		set := build(tc.first, tc.second)
		if got := set.AgreementOf("billing"); got != tc.want {
			t.Errorf("%q,%q: agreement = %s, want %s", tc.first, tc.second, got, tc.want)
		}
	}

	set := build(binding.AgreementAny, binding.AgreementAny)
	if got := set.AgreementOf("code"); got != binding.AgreementAll {
		t.Errorf("code binding agreement = %s", got)
	}

	if got := set.AgreementOf("unknown"); got != binding.AgreementAll {
		t.Errorf("unbound agreement = %s", got)
	}
}

// checkDigestNode checks the synthesized node of the digest that the edge `k8s/prod/a.yaml:4` gives.
func checkDigestNode(t *testing.T, n *observation.ObservedNode) {
	t.Helper()

	if n.ObservedID != "synth:image:reg.example.com/app@"+digest+"#production" || n.Environment != "production" {
		t.Errorf("node = %+v", *n)
	}

	if n.FilePath != "k8s/prod/a.yaml" || n.StartLine != 4 {
		t.Errorf("node = %+v", *n)
	}

	if n.Properties["digest"] != digest {
		t.Errorf("node = %+v", *n)
	}
}

func TestImageDigestNodesComeFromTheEdgeImageReference(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{
		{Properties: map[string]string{"image": "reg.example.com/app:1.0@" + digest}, Environment: "production", SourceSpan: &observation.SourceSpan{Path: "k8s/prod/b.yaml", Line: 9}},
		{Properties: map[string]string{"image": "reg.example.com/app@" + digest}, Environment: "production", SourceSpan: &observation.SourceSpan{Path: "k8s/prod/a.yaml", Line: 4}},
		{Properties: map[string]string{"image": "reg.example.com/app:1.0"}, Environment: "production"},
		{Properties: map[string]string{"image": "${IMAGE}"}},
	}

	nodes := binding.ImageDigestNodes(edges)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v", nodes)
	}

	checkDigestNode(t, &nodes[0])

	b := matchBinding("d", "pinned", &binding.Match{Kind: binding.MatchImage, Repository: "reg.example.com/app", Digest: digest, Environment: "production"})

	set := binding.Resolve([]binding.Binding{b}, nodes, nil)
	if len(set.ByObserved) != 1 {
		t.Errorf("bound = %v", set.ByObserved)
	}
}

func TestImageDigestNodesKeepOneDigestApartInTwoEnvironments(t *testing.T) {
	t.Parallel()

	image := map[string]string{"image": "reg.example.com/app@" + digest}
	edges := []*observation.EvidenceEdge{
		{Properties: image, Environment: "staging", SourceSpan: &observation.SourceSpan{Path: "k8s/staging/a.yaml", Line: 2}},
		{Properties: image, Environment: "production", SourceSpan: &observation.SourceSpan{Path: "k8s/prod/a.yaml", Line: 3}},
	}

	nodes := binding.ImageDigestNodes(edges)
	if len(nodes) != 2 || nodes[0].ObservedID == nodes[1].ObservedID {
		t.Fatalf("one digest in two environments needs two distinct IDs: %+v", nodes)
	}

	// Only production has a binding: staging stays unbound.
	b := matchBinding("d", "pinned", &binding.Match{Kind: binding.MatchImage, Repository: "reg.example.com/app", Digest: digest, Environment: "production"})
	set := binding.Resolve([]binding.Binding{b}, nodes, nil)

	for i := range nodes {
		_, bound := set.ByObserved[nodes[i].ObservedID]
		if bound != (nodes[i].Environment == "production") {
			t.Errorf("%s (%s): bound = %v", nodes[i].ObservedID, nodes[i].Environment, bound)
		}
	}
}
