package observation_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

func TestParseObjKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
		want observation.ObjKey
	}{
		{"namespaced", "apps/Deployment/payments/api", observation.ObjKey{Group: "apps", Kind: "Deployment", Namespace: "payments", Name: "api"}},
		{"core group", "core/Secret/payments/api-secrets", observation.ObjKey{Group: "core", Kind: "Secret", Namespace: "payments", Name: "api-secrets"}},
		{"cluster scoped", "core/Namespace/_/payments", observation.ObjKey{Group: "core", Kind: "Namespace", Name: "payments", ClusterScoped: true}},
		{"cluster scoped rbac", "rbac.authorization.k8s.io/ClusterRole/_/reader", observation.ObjKey{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "reader", ClusterScoped: true}},
		{"undeclared namespace", "apps/Deployment/?/gateway", observation.ObjKey{Group: "apps", Kind: "Deployment", Name: "gateway", NamespaceUnresolved: true}},
		{"helm template name", "apps/Deployment/?/<.Release.Name>-api", observation.ObjKey{Group: "apps", Kind: "Deployment", Name: "<.Release.Name>-api", NamespaceUnresolved: true}},
		{"name with slash", "apps/Deployment/payments/a/b", observation.ObjKey{Group: "apps", Kind: "Deployment", Namespace: "payments", Name: "a/b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := observation.ParseObjKey(tc.key)
			if err != nil {
				t.Fatal(err)
			}

			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}

	for _, bad := range []string{"", "apps", "apps/Deployment/payments", "apps//payments/api", "apps/Deployment//api", "apps/Deployment/payments/"} {
		if _, err := observation.ParseObjKey(bad); !errors.Is(err, observation.ErrInvalidObjKey) {
			t.Errorf("%q: err = %v, want ErrInvalidObjKey", bad, err)
		}
	}
}

func TestScopedIDCanonicalString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		env, cluster string
		key          string
		want         string
		resolved     bool
	}{
		{"namespaced", "production", "prod-eu", "apps/Deployment/payments/billing", "k8s:production/prod-eu/apps/Deployment/payments/billing", true},
		{"cluster scoped has no namespace", "production", "prod-eu", "core/Namespace/_/payments", "k8s:production/prod-eu/core/Namespace//payments", true},
		{"clusterrole", "production", "prod-eu", "rbac.authorization.k8s.io/ClusterRole/_/reader", "k8s:production/prod-eu/rbac.authorization.k8s.io/ClusterRole//reader", true},
		{"undeclared namespace", "production", "prod-eu", "apps/Deployment/?/api", "k8s:production/prod-eu/apps/Deployment/~/api", false},
		{"unresolved environment", "", "prod-eu", "apps/Deployment/payments/api", "k8s:~/prod-eu/apps/Deployment/payments/api", false},
		{"unresolved environment word", observation.EnvironmentUnresolved, "c", "apps/Deployment/payments/api", "k8s:~/c/apps/Deployment/payments/api", false},
		{"unknown cluster", "production", "", "apps/Deployment/payments/api", "k8s:production/~/apps/Deployment/payments/api", false},
		{"nothing known", "", "", "apps/Deployment/?/api", "k8s:~/~/apps/Deployment/~/api", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, err := observation.ScopedIDFromObjKey(tc.env, tc.cluster, tc.key)
			if err != nil {
				t.Fatal(err)
			}

			if got := id.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}

			if id.Resolved() != tc.resolved {
				t.Errorf("Resolved() = %v, want %v", id.Resolved(), tc.resolved)
			}

			back, err := observation.ParseScopedID(id.String())
			if err != nil || back != id {
				t.Errorf("round trip: %+v, %v, want %+v", back, err, id)
			}
		})
	}

	if _, err := observation.ScopedIDFromObjKey("p", "c", "bogus"); !errors.Is(err, observation.ErrInvalidObjKey) {
		t.Errorf("err = %v", err)
	}
}

func TestScopedIDEscapesSeparators(t *testing.T) {
	t.Parallel()

	id := observation.ScopedID{Environment: "prod", Cluster: "arn:aws:eks:eu:1:cluster/main", APIGroup: "apps", Kind: "Deployment", Namespace: "a", Name: "100%"}

	text := id.String()
	if strings.Count(text, "/") != 5 {
		t.Errorf("a separator in a part must be escaped: %q", text)
	}

	back, err := observation.ParseScopedID(text)
	if err != nil || back != id {
		t.Errorf("round trip: %+v, %v", back, err)
	}

	// A part that is exactly `~` must not read as unresolved.
	literal := observation.ScopedID{Environment: "~", Cluster: "c", APIGroup: "g", Kind: "K", Namespace: "n", Name: "x"}
	if observation.NewScopedID("prod", "c", observation.ObjKey{Group: "g", Kind: "K", Namespace: "n", Name: "~"}).String() == literal.String() {
		t.Error("a name `~` collides with an unresolved part")
	}

	for _, bad := range []string{"", "x:a/b", "k8s:a/b/c", "k8s:a/b/c/d/e/f/g"} {
		if _, err := observation.ParseScopedID(bad); !errors.Is(err, observation.ErrInvalidScopedID) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// mustProductionScopedID makes the scoped id of an object key in the production environment of cluster c1.
func mustProductionScopedID(t *testing.T, objKey string) observation.ScopedID {
	t.Helper()

	id, err := observation.ScopedIDFromObjKey("production", "c1", objKey)
	if err != nil {
		t.Fatal(err)
	}

	return id
}

func TestNamespaceIsNeverDefaultedOrAddedToClusterScopedKind(t *testing.T) {
	t.Parallel()

	cluster := mustProductionScopedID(t, "core/Namespace/_/payments")

	if !cluster.ClusterScoped() || cluster.Namespace != "" {
		t.Fatalf("cluster-scoped id has a namespace: %+v", cluster)
	}

	// A namespace from the surrounding file or an overlay does not change it.
	if got := cluster.WithNamespace("default"); got != cluster {
		t.Errorf("a namespace was added to a cluster-scoped kind: %+v", got)
	}

	// The same name in a namespaced and in a cluster-scoped form are different identities.
	namespaced := mustProductionScopedID(t, "core/Namespace/payments/payments")
	if namespaced == cluster {
		t.Error("namespaced and cluster-scoped identities are equal")
	}
}

func TestNamespaceOfUndeclaredKindIsNeverDefaultedAndIsSuppliedOnce(t *testing.T) {
	t.Parallel()

	undeclared := mustProductionScopedID(t, "apps/Deployment/?/api")

	if undeclared.NamespaceResolved() || undeclared.Namespace == "default" {
		t.Errorf("the undeclared namespace was defaulted: %+v", undeclared)
	}

	// Rendering or a binding can supply it, once.
	supplied := undeclared.WithNamespace("payments")
	if supplied.Namespace != "payments" || !supplied.Resolved() {
		t.Errorf("supplied: %+v", supplied)
	}

	if again := supplied.WithNamespace("other"); again != supplied {
		t.Errorf("a resolved namespace was replaced: %+v", again)
	}

	if same := undeclared.WithNamespace(""); same != undeclared {
		t.Errorf("an empty namespace resolved the identity: %+v", same)
	}
}

func TestObjKeyFromQualifiedName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		qn   string
		want string
		ok   bool
	}{
		{"k8s.prod.deployment.yaml::apps/Deployment/payments/api", "apps/Deployment/payments/api", true},
		// The qualified name writes `-` for a cluster-scoped kind and for an undeclared
		// namespace; it cannot tell them apart, so the namespace is unresolved.
		{"kustomize.base.deployment.yaml::apps/Deployment/-/gateway", "apps/Deployment/?/gateway", true},
		{"k8s.prod.namespace.yaml::core/Namespace/-/payments", "core/Namespace/?/payments", true},
		{"k8s.prod.deployment.yaml::apps/Deployment/payments/api#env/app/DB_PASSWORD", "apps/Deployment/payments/api", true},
		{"kustomize.overlays.prod.kustomization.Kustomization", "", false},
		{"scripts.deploy", "", false},
	}

	for _, tc := range cases {
		got, ok := observation.ObjKeyFromQualifiedName(tc.qn)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q = %q, %v, want %q, %v", tc.qn, got, ok, tc.want, tc.ok)
		}
	}
}

// declWhere is the node, file, and line of a declaration.
type declWhere struct {
	nodeID, path string
	line         int
}

func decl(t *testing.T, env, key string, where declWhere) observation.Declaration {
	t.Helper()

	id, err := observation.ScopedIDFromObjKey(env, "c-"+env, key)
	if err != nil {
		t.Fatal(err)
	}

	return observation.Declaration{ID: id, NodeID: where.nodeID, Path: where.path, Line: where.line}
}

func TestGroupDeclarationsA08TwoManifestsOneResource(t *testing.T) {
	t.Parallel()

	const key = "apps/Deployment/payments/reports"

	groups := observation.GroupDeclarations([]observation.Declaration{
		decl(t, "production", key, declWhere{"cbm:p:2", "k8s/overrides/reports.yaml", 4}),
		decl(t, "production", key, declWhere{"cbm:p:1", "k8s/prod/reports.yaml", 4}),
		decl(t, "production", "apps/Deployment/payments/api", declWhere{"cbm:p:3", "k8s/prod/deployment.yaml", 1}),
	})

	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}

	conflicts := observation.Conflicts(groups)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v", conflicts)
	}

	c := conflicts[0]
	if !c.Certain || c.ID.Name != "reports" {
		t.Errorf("conflict = %+v", c)
	}

	want := []observation.Location{
		{NodeID: "cbm:p:2", Path: "k8s/overrides/reports.yaml", Line: 4},
		{NodeID: "cbm:p:1", Path: "k8s/prod/reports.yaml", Line: 4},
	}
	if !reflect.DeepEqual(c.Locations, want) {
		t.Errorf("locations = %+v, want both, ordered by path: %+v", c.Locations, want)
	}
}

func TestGroupDeclarationsSameNameInTwoEnvironmentsIsNoConflict(t *testing.T) {
	t.Parallel()

	const key = "apps/Deployment/payments/billing"

	groups := observation.GroupDeclarations([]observation.Declaration{
		decl(t, "production", key, declWhere{"cbm:p:1", "k8s/prod/billing.yaml", 1}),
		decl(t, "staging", key, declWhere{"cbm:p:2", "k8s/staging/billing.yaml", 1}),
	})

	if len(groups) != 2 || groups[0].ID == groups[1].ID {
		t.Fatalf("A04: groups = %+v", groups)
	}

	if got := observation.Conflicts(groups); len(got) != 0 {
		t.Errorf("conflicts = %+v, want none", got)
	}
}

// isOneUncertainConflictWithTwoLocations says whether the conflicts are exactly one that is not certain and has two locations.
func isOneUncertainConflictWithTwoLocations(conflicts []observation.Conflict) bool {
	return len(conflicts) == 1 && !conflicts[0].Certain && len(conflicts[0].Locations) == 2
}

func TestConflictIsNotCertainWithUnresolvedParts(t *testing.T) {
	t.Parallel()

	undeclared := observation.GroupDeclarations([]observation.Declaration{
		decl(t, "production", "apps/Deployment/?/gateway", declWhere{"a", "kustomize/base/deployment.yaml", 1}),
		decl(t, "production", "apps/Deployment/?/gateway", declWhere{"b", "kustomize/overlays/prod/patch.yaml", 1}),
	})

	noEnv := observation.GroupDeclarations([]observation.Declaration{
		decl(t, "", "apps/Deployment/payments/x", declWhere{"a", "one/x.yaml", 1}),
		decl(t, "", "apps/Deployment/payments/x", declWhere{"b", "two/x.yaml", 1}),
	})

	for name, groups := range map[string][]observation.DeclarationGroup{"namespace": undeclared, "environment": noEnv} {
		got := observation.Conflicts(groups)
		if !isOneUncertainConflictWithTwoLocations(got) {
			t.Errorf("%s: %+v, want one uncertain conflict with both locations", name, got)
		}
	}
}

func TestUnscopedNodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label, qn string
		want      bool
	}{
		{"Image", "__image__registry.example.com/payments/api", true},
		{"Route", "__route__ingress__api.example.com/", true},
		{"EnvVar", "__env__LISTEN_ADDR", true},
		{"Chart", "__chart__https://charts.example.com/stable/postgresql", true},
		{"Resource", "k8s.prod.deployment.yaml::apps/Deployment/payments/api", false},
		{"EnvVar", "k8s.prod.deployment.yaml::apps/Deployment/payments/api#env/app/DB_PASSWORD", false},
		{"Chart", "charts.api.Chart.__infra__", false},
		{"File", "", false},
	}

	for _, tc := range cases {
		if got := observation.IsUnscopedNode(tc.label, tc.qn); got != tc.want {
			t.Errorf("IsUnscopedNode(%s, %s) = %v", tc.label, tc.qn, got)
		}
	}
}

func TestDecisionKeepsPropertiesVerbatim(t *testing.T) {
	t.Parallel()

	d, err := observation.DecisionOf("docs/adr/0001-use-self-hosted-postgres.md", map[string]string{
		"adr_id": "ADR-1", "title": "ADR-0001: Use self-hosted Postgres", "status": "superseded",
		"status_text": "Superseded by ADR-0002", "date": "2026-01-10", "superseded_by": "0002-use-managed-postgres.md",
	})
	if err != nil {
		t.Fatal(err)
	}

	gotProps := [3]string{d.AdrID, d.SupersededBy, d.StatusText}
	if wantProps := [3]string{"ADR-1", "0002-use-managed-postgres.md", "Superseded by ADR-0002"}; gotProps != wantProps {
		t.Errorf("properties were rewritten: %+v", d)
	}

	if d.Path != "docs/adr/0001-use-self-hosted-postgres.md" || d.SynthID() != "synth:adr:docs/adr/0001-use-self-hosted-postgres.md" {
		t.Errorf("identity is not the file path: %s", d.SynthID())
	}

	if _, err := observation.DecisionOf("", nil); !errors.Is(err, observation.ErrNoDecisionPath) {
		t.Errorf("err = %v", err)
	}
}

func TestDiagramElementIdentityIgnoresTheOrdinal(t *testing.T) {
	t.Parallel()

	first, err := observation.DiagramElementOf("docs/architecture.md", "docs.architecture.Context-diagram#diagram-1", "gateway")
	if err != nil {
		t.Fatal(err)
	}

	// The block moved: the ordinal changed, the element did not.
	reordered, err := observation.DiagramElementOf("docs/architecture.md", "docs.architecture.Context-diagram#diagram-3", "gateway")
	if err != nil {
		t.Fatal(err)
	}

	if first != reordered || first.SynthID() != reordered.SynthID() {
		t.Errorf("identity changed: %+v, %+v", first, reordered)
	}

	gotParts := [3]string{first.File, first.Section, first.ElementID}
	if wantParts := [3]string{"docs/architecture.md", "Context-diagram", "gateway"}; gotParts != wantParts {
		t.Errorf("parts: %+v", first)
	}

	if first.SynthID() != "synth:diagram-element:docs/architecture.md#Context-diagram#gateway" {
		t.Errorf("SynthID = %s", first.SynthID())
	}

	other, _ := observation.DiagramElementOf("docs/architecture.md", "docs.architecture.Other-section#diagram-1", "gateway")
	if other == first {
		t.Error("two sections must give two identities")
	}
}

func TestDiagramElementOfRejectsMalformedQualifiedNames(t *testing.T) {
	t.Parallel()

	for _, bad := range [][3]string{
		{"docs/architecture.md", "other.file.Section#diagram-1", "x"},
		{"docs/architecture.md", "docs.architecture.Section", "x"},
		{"docs/architecture.md", "docs.architecture.Section#diagram-x", "x"},
		{"docs/architecture.md", "docs.architecture.#diagram-1", "x"},
		{"docs/architecture.md", "docs.architecture.Section#diagram-1", ""},
	} {
		if _, err := observation.DiagramElementOf(bad[0], bad[1], bad[2]); !errors.Is(err, observation.ErrDiagramQualifiedName) {
			t.Errorf("%v: err = %v", bad, err)
		}
	}
}

func TestSynthIDs(t *testing.T) {
	t.Parallel()

	if observation.ImageSynthID("registry.example.com/payments/api") != "synth:image:registry.example.com/payments/api" {
		t.Error("image synth id")
	}

	if !strings.HasPrefix(observation.ImageSynthID("x"), observation.SynthPrefix()) {
		t.Error("synth prefix")
	}
}

func TestResourceKindOf(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want string
		node observation.ObservedNode
	}{
		{"the provider kind wins", "Secret", observation.ObservedNode{Label: "Resource", Kind: "Secret", Properties: map[string]string{"obj_key": "core/Secret/payments/s"}}},
		{"a workload nothing references has no kind: the obj_key gives it", "Deployment", observation.ObservedNode{Label: "Resource", Properties: map[string]string{"obj_key": "apps/Deployment/payments/worker"}}},
		{"a cluster-scoped kind", "Namespace", observation.ObservedNode{Label: "Resource", Properties: map[string]string{"obj_key": "core/Namespace/_/payments"}}},
		{"no obj_key (Kustomization)", "", observation.ObservedNode{Label: "Resource", QualifiedName: "kustomize.overlays.prod.kustomization.Kustomization"}},
		{"not a Resource", "", observation.ObservedNode{Label: "File", Properties: map[string]string{"obj_key": "apps/Deployment/payments/worker"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := observation.ResourceKindOf(&tc.node); got != tc.want {
				t.Fatalf("ResourceKindOf = %q, want %q", got, tc.want)
			}
		})
	}
}
