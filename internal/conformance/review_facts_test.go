package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

// These tests build the facts of a small fake graph (a production and a staging Deployment with references, an
// Ingress with the same host and path in both environments) and read them through impact.FactSource.

const (
	rvProject    = "rv"
	rvProdDeploy = "k8s/prod/deployment.yaml"
	rvIdentity   = "k8s:production/~/apps/Deployment/payments/api"
)

// rvWorld is the graph of a test and the coverage gaps that the manifest cross-check would add.
type rvWorld struct {
	// assurance is the assurance evidence of the run; nil means the workspace has no `assurance` key.
	assurance *conformance.ReviewAssurance
	git       workspace.GitPort
	// root is the workspace root for FileDigest.
	root string
	// stagingBackend is the name of the Service that the staging Ingress routes to.
	stagingBackend string
	// gaps are the coverage gaps of the run.
	gaps []observation.CoverageGap
	// noProfile builds the facts with no capability profile.
	noProfile bool
	// gatewayRoute adds an HTTPRoute of production that attaches to a Gateway through a `parentRef` and routes to the
	// Service.
	gatewayRoute bool
}

func rvID(n string) string { return "cbm:" + rvProject + ":" + n }

func (w *rvWorld) graph() *testutil.FakeGraph {
	backend := w.stagingBackend
	if backend == "" {
		backend = "api"
	}

	node := testutil.ResourceNode
	ns := node("1", "k8s/prod/namespace.yaml", "core", "Namespace", testutil.ClusterScoped, "payments")
	deploy := node("2", rvProdDeploy, "apps", "Deployment", "payments", "api")
	secret := node("3", "k8s/prod/secret.yaml", "core", "Secret", "payments", "api-secrets")
	config := node("4", "k8s/prod/configmap.yaml", "core", "ConfigMap", "payments", "api-config")
	claim := node("5", "k8s/prod/pvc.yaml", "core", "PersistentVolumeClaim", "payments", "api-data")
	account := node("6", "k8s/prod/rbac.yaml", "core", "ServiceAccount", "payments", "api-sa")
	role := node("7", "k8s/prod/rbac.yaml", "rbac.authorization.k8s.io", "Role", "payments", "api-role")
	roleBinding := node("8", "k8s/prod/rbac.yaml", "rbac.authorization.k8s.io", "RoleBinding", "payments", "api-binding")
	service := node("9", "k8s/prod/service.yaml", "core", "Service", "payments", "api")
	ingress := node("10", "k8s/prod/ingress.yaml", "networking.k8s.io", "Ingress", "payments", "api")
	tlsSecret := node("11", "k8s/prod/secret.yaml", "core", "Secret", "payments", "api-tls")
	image := testutil.ImageNode("12", "registry.example.com/payments/api")
	stagingDeploy := node("13", "k8s/staging/deployment.yaml", "apps", "Deployment", "payments-staging", "api")
	stagingService := node("14", "k8s/staging/service.yaml", "core", "Service", "payments-staging", backend)
	stagingIngress := node("15", "k8s/staging/ingress.yaml", "networking.k8s.io", "Ingress", "payments-staging", "api")
	route := testutil.InfraNode("16", "Route", "__route__ingress__api.example.com/", "", nil)
	caller := testutil.InfraNode("17", "Function", "services.a.main.Call", "services/a/main.go", nil)
	external := testutil.InfraNode("18", "Function", "vendor.ext.Serve", "vendor/ext.go", nil)

	ref := func(mode string) testutil.Discriminators { return testutil.Discriminators{Ref: mode} }
	usage := func(from, to *testutil.FakeNode, mode string, extra map[string]string) testutil.FakeEdge {
		return testutil.InfraEdge(from, to, "USAGE", ref(mode), extra)
	}
	maps := func(from, to *testutil.FakeNode) testutil.FakeEdge {
		return testutil.InfraEdge(from, to, "INFRA_MAPS", testutil.Discriminators{Kind: "ingress"}, nil)
	}

	nodes := []testutil.FakeNode{
		ns, deploy, secret, config, claim, account, role, roleBinding, service, ingress, tlsSecret, image, stagingDeploy,
		stagingService, stagingIngress, route, caller, external,
	}
	edges := []testutil.FakeEdge{
		usage(&deploy, &secret, "env", nil), usage(&deploy, &config, "envFrom", nil), usage(&deploy, &claim, "volume", nil),
		usage(&deploy, &account, "serviceAccountName", nil), usage(&roleBinding, &account, "subject", nil),
		usage(&roleBinding, &role, "roleRef", nil), usage(&ingress, &tlsSecret, "tls", nil),
		usage(&deploy, &image, "image", map[string]string{"container": "app", "image": "registry.example.com/payments/api:1.2.0"}),
		usage(&stagingDeploy, &image, "image", map[string]string{"container": "app", "image": "registry.example.com/payments/api:1.3.0-rc1"}),
		maps(&ingress, &service), maps(&route, &service), maps(&route, &stagingService), maps(&stagingIngress, &stagingService),
		testutil.NewEdge(&caller, &external, "HTTP_CALLS", nil),
	}

	if w.gatewayRoute {
		httpRoute := node("19", "k8s/prod/httproute.yaml", "gateway.networking.k8s.io", "HTTPRoute", "payments", "api-route")
		gateway := node("20", "k8s/prod/gateway.yaml", "gateway.networking.k8s.io", "Gateway", "payments", "api-gw")
		nodes = append(nodes, httpRoute, gateway)
		edges = append(edges,
			usage(&httpRoute, &gateway, "parentRef", nil),
			testutil.InfraEdge(&httpRoute, &service, "INFRA_MAPS", testutil.Discriminators{Kind: "httproute"}, nil),
		)
	}

	return testutil.NewFakeGraph(rvProject, nodes, edges)
}

func rvAttribute(path string) []string {
	switch {
	case strings.HasPrefix(path, "k8s/prod/"):
		return []string{"production"}
	case strings.HasPrefix(path, "k8s/staging/"):
		return []string{"staging"}
	default:
		return nil
	}
}

func rvModel() *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		Nodes: []intent.NodeDeclaration{
			{ID: "api-service", NodeType: "service", Trust: &trust.Declaration{Dimensions: map[string]string{"zone": "dmz", "region": "eu-west-1"}, Classification: "confidential"}},
			{ID: "a-service", NodeType: "service"},
		},
		Relationships: []intent.RelationshipDeclaration{
			{ID: "a-to-api", Kind: intent.RelConnects, Source: "a-service", Targets: []string{"api-service"}, Protocol: "HTTPS"},
		},
		Controls: []intent.ControlAttachment{
			{ControlID: "ctl-1", Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "api-service"}, Engine: "concordia"},
		},
	}
}

// facts builds the review facts of the world.
func (w *rvWorld) facts(t *testing.T) *conformance.ReviewFacts {
	t.Helper()

	client := observation.NewFakeClient(w.graph().Handler, "")
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: rvProject, GitRevision: "abc123"})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	kinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, edgeType := range ref.Schema.EdgeTypes {
		kinds = append(kinds, edgeType.Type)
	}

	result, err := provider.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok {
		t.Fatal("no profile for CBM 0.12.1")
	}

	observation.AttributeEnvironments(&result, rvAttribute)
	observation.MarkPossiblyLossy(result.Edges, profile)
	observation.AnnotateScopedIDs(result.Nodes, func(string) string { return "" })
	observation.DerivePlacement(&result, func(string) string { return "" })

	caps := provider.Capabilities(t.Context())
	index := observation.IndexRecord{}

	coverage := observation.ComputePredicateCoverage(&observation.PredicateInput{
		Families: caps.Families,
		Projects: []observation.ProjectScope{{Name: rvProject, Index: &index}},
		Environments: []observation.EnvironmentScope{
			{ID: "production", Projects: []string{rvProject}, CrossChecked: true},
			{ID: "staging", Projects: []string{rvProject}, CrossChecked: true},
		},
		Gaps: w.gaps,
	})

	in := &conformance.ReviewFactsInput{
		Evidence: &result,
		BindingSet: &binding.Set{ByObserved: map[string][]string{
			rvID("2"): {"api-service"}, rvID("10"): {"api-service"}, rvID("17"): {"a-service"},
		}},
		Model: rvModel(), Coverage: coverage, Root: w.root, Git: w.git, ProviderVersion: "0.12.1", AdapterSchema: observation.AdapterSchema,
		Bindings: []binding.Binding{{
			ID: "b-api", ArchitectureRef: "api-service",
			Match: []binding.Match{{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "api", Namespace: "payments"}},
		}},
		Environments: []deployment.Environment{{ID: "production"}, {ID: "staging"}},
	}

	in.Assurance = w.assurance

	if !w.noProfile {
		in.Profile = profile
	}

	return conformance.NewReviewFacts(in)
}

func items(t *testing.T, facts impact.FactSource, sel *impact.Selector) impact.Match {
	t.Helper()

	match, err := facts.Items(t.Context(), *sel)
	if err != nil {
		t.Fatalf("Items(%s): %v", sel.Kind, err)
	}

	return match
}

func identities(match *impact.Match) []string {
	out := make([]string, 0, len(match.Items))
	for i := range match.Items {
		out = append(out, match.Items[i].Identity)
	}

	return out
}

// rvAnyOf says whether any of the checks of a failure condition holds.
func rvAnyOf(checks ...bool) bool { return slices.Contains(checks, true) }

// rvIsUnsupportedFamily says whether the match is `unsupported-family` under the profile of the fake provider.
func rvIsUnsupportedFamily(match *impact.Match) bool {
	if match.State != impact.MatchUnsupported || match.Reason != impact.ReasonUnsupportedFamily {
		return false
	}

	return match.Profile == "cbm-0.12.1"
}

// rvIsGatewayAttachment says whether the exposure class is the one of the Gateway that the HTTPRoute attaches to.
func rvIsGatewayAttachment(exposureClass []string) bool {
	if len(exposureClass) != 1 {
		return false
	}

	return strings.HasPrefix(exposureClass[0], "gateway:") && strings.Contains(exposureClass[0], "Gateway/payments/api-gw")
}

// Scenario: Reference kinds map to dependency kinds.
// The `ref` values serviceAccountName, roleRef, env, envFrom, volume, and tls give the kinds of the table below; the
// `parentRef` gateway attachment is covered by TestReviewFactsParentRefIsTheGatewayAttachmentOfAnEntryPoint.
func TestReviewFactsEachKindYieldsItems(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)

	want := map[impact.Kind]string{
		impact.KindRuntimeIdentity:        rvIdentity + "#serviceAccountName:core/ServiceAccount/payments/api-sa",
		impact.KindRoleBinding:            "k8s:production/~/rbac.authorization.k8s.io/RoleBinding/payments/api-binding#roleRef:rbac.authorization.k8s.io/Role/payments/api-role",
		impact.KindSecretReference:        rvIdentity + "#env:core/Secret/payments/api-secrets",
		impact.KindConfigurationReference: rvIdentity + "#envFrom:core/ConfigMap/payments/api-config",
		impact.KindStorageReference:       rvIdentity + "#volume:core/PersistentVolumeClaim/payments/api-data",
		impact.KindPublicEntryPoint:       "k8s:production/~/networking.k8s.io/Ingress/payments/api",
		impact.KindCertificateReference:   "k8s:production/~/networking.k8s.io/Ingress/payments/api#tls:core/Secret/payments/api-tls",
		impact.KindPlacement:              rvIdentity,
		impact.KindImplementation:         rvIdentity + "#image:app",
		impact.KindProtocol:               "calm:relationship:a-to-api",
		impact.KindTrustContext:           "calm:node:api-service#trust",
		impact.KindDataClassification:     "calm:node:api-service#classification",
		impact.KindBinding:                "binding:b-api",
		impact.KindControl:                "calm:control:node:api-service:ctl-1",
		impact.KindExternalDestination:    "",
	}

	for kind, identity := range want {
		match := items(t, facts, &impact.Selector{Kind: kind})
		if match.State != impact.MatchFound {
			t.Errorf("%s: state %s (%s: %s), want matched", kind, match.State, match.Reason, match.Detail)

			continue
		}

		if identity != "" && !slices.Contains(identities(&match), identity) {
			t.Errorf("%s: identities %v lack %s", kind, identities(&match), identity)
		}
	}
}

// Scenario: Reference kinds map to dependency kinds.
// A `parentRef` to a declared Gateway becomes the exposure class `gateway:<Gateway>` of the public entry point of the
// HTTPRoute.
func TestReviewFactsParentRefIsTheGatewayAttachmentOfAnEntryPoint(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{gatewayRoute: true}).facts(t)
	match := items(t, facts, &impact.Selector{Kind: impact.KindPublicEntryPoint, Environment: "production"})

	const route = "k8s:production/~/gateway.networking.k8s.io/HTTPRoute/payments/api-route"

	for i := range match.Items {
		item := &match.Items[i]
		if item.Identity != route {
			continue
		}

		if got := item.Facets["exposureClass"]; !rvIsGatewayAttachment(got) {
			t.Errorf("exposureClass = %v, want the attached Gateway", got)
		}

		return
	}

	t.Fatalf("no entry point %s in %v (%s: %s)", route, identities(&match), match.Reason, match.Detail)
}

func TestReviewFactsItemsHoldIdentifiersOnly(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)
	match := items(t, facts, &impact.Selector{Kind: impact.KindSecretReference, Environment: "production"})

	if len(match.Items) != 1 {
		t.Fatalf("secret items = %v", identities(&match))
	}

	item := match.Items[0]

	if got := item.Facets["reference"]; !slices.Equal(got, []string{"core/Secret/payments/api-secrets"}) {
		t.Errorf("reference = %v", got)
	}

	if got := item.Facets["role"]; !slices.Equal(got, []string{"env"}) {
		t.Errorf("role = %v", got)
	}

	if len(item.Redactions) != 0 {
		t.Errorf("redactions = %v", item.Redactions)
	}
}

// Scenario: Secret and ConfigMap distinguished.
func TestReviewFactsTargetKindTellsSecretFromConfigMap(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)
	secrets := items(t, facts, &impact.Selector{Kind: impact.KindSecretReference, Environment: "production"})
	configs := items(t, facts, &impact.Selector{Kind: impact.KindConfigurationReference, Environment: "production"})

	if got := secrets.Items[0].Facets["role"]; !slices.Equal(got, []string{"env"}) || len(secrets.Items) != 1 {
		t.Errorf("secret items = %v", identities(&secrets))
	}

	if got := configs.Items[0].Facets["role"]; !slices.Equal(got, []string{"envFrom"}) || len(configs.Items) != 1 {
		t.Errorf("configuration items = %v", identities(&configs))
	}
}

// Scenario: Assurance kinds without an assurance key. The workspace has no `assurance` key, so the capability profile
// does not map the kinds and a selector of either is `unsupported-family`, not an empty match.
func TestReviewFactsAssuranceKindsAreUnsupportedWithoutAnAssuranceKey(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)

	for _, kind := range []impact.Kind{impact.KindVerification, impact.KindQualityScenario} {
		match := items(t, facts, &impact.Selector{Kind: kind})
		if !rvIsUnsupportedFamily(&match) {
			t.Errorf("%s: %+v", kind, match)
		}
	}
}

// Scenario: SEC-03 certificate references not extractable.
func TestReviewFactsUnsupportedKindIsNotEmpty(t *testing.T) {
	t.Parallel()

	bare := (&rvWorld{noProfile: true}).facts(t)

	match := items(t, bare, &impact.Selector{Kind: impact.KindCertificateReference})
	if rvAnyOf(match.State != impact.MatchUnsupported, match.Profile != "none", len(match.Items) != 0) {
		t.Errorf("certificate-reference without a profile: %+v", match)
	}

	// The model-based kinds do not need the provider profile.
	if match = items(t, bare, &impact.Selector{Kind: impact.KindControl}); match.State != impact.MatchFound {
		t.Errorf("control without a profile: %+v", match)
	}
}

// Scenario: Unmapped Terraform reference.
func TestReviewFactsTerraformReferenceIsUnsupported(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)

	match := items(t, facts, &impact.Selector{Kind: impact.KindStorageReference, Match: map[string][]string{"via": {"terraform"}}})
	if !rvIsUnsupportedFamily(&match) {
		t.Fatalf("terraform storage-reference: %+v", match)
	}

	if !strings.Contains(match.Detail, "Terraform") {
		t.Errorf("detail = %q", match.Detail)
	}
}

func TestReviewFactsProfileAndVersions(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)

	profile := facts.Profile()
	if rvAnyOf(profile.ID != "cbm-0.12.1", !profile.Supports(impact.KindSecretReference), profile.Supports(impact.KindVerification)) {
		t.Errorf("profile = %+v", profile)
	}

	if !profile.SupportsSelector(impact.KindPublicEntryPoint, "host") || profile.SupportsSelector(impact.KindPublicEntryPoint, "via") {
		t.Errorf("selectors = %v", profile.Selectors[impact.KindPublicEntryPoint])
	}

	if got := facts.EvaluatorVersions()[impact.EvaluatorNormalization]; got != observation.NormalizationVersion {
		t.Errorf("normalization = %q", got)
	}
}
