package render_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
)

func selectionOf(t *testing.T, res *render.Result, kind, name string) *render.Selection {
	t.Helper()

	for i := range res.Selections {
		if res.Selections[i].Selector.Kind == kind && res.Selections[i].Selector.Name == name {
			return &res.Selections[i]
		}
	}

	t.Fatalf("no selection fact for %s %s", kind, name)

	return nil
}

func targetNames(sel *render.Selection) []string {
	out := make([]string, 0, len(sel.Targets))
	for _, id := range sel.Targets {
		out = append(out, id.Kind+"/"+id.Name)
	}

	return out
}

// TestSelectionFacts reproduces CBM 0.12.1 observation O6: an empty podSelector and a matchExpressions selector get
// no code-graph edge, and a selector never joins across directories. The facts come from the documents.
func TestSelectionFacts(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain")

	checkEmptySelector(t, res)
	checkExpressionSelector(t, res)

	// The Service is in net/, the Deployment in workloads/: the selector joins across directories.
	svc := selectionOf(t, res, "Service", "api")
	expectDeep(t, "service api selects", targetNames(svc), []string{"Deployment/api"})
	expectDeep(t, "service api selection kind", svc.Kind, render.SelectionService)

	// No workload carries the label: an empty result that is not a gap.
	worker := selectionOf(t, res, "Service", "worker")
	if len(worker.Targets) != 0 || worker.Unsupported {
		t.Errorf("service worker = %+v", worker)
	}
}

func checkEmptySelector(t *testing.T, res *render.Result) {
	t.Helper()

	deny := selectionOf(t, res, "NetworkPolicy", "default-deny")
	if !deny.Empty || deny.Unsupported {
		t.Errorf("default-deny = %+v, want an empty selector", deny)
	}

	want := []string{"Pod/debug", "Deployment/api", "Deployment/reports", "CronJob/nightly"} // sorted by group first
	expectDeep(t, "an empty selector selects every pod of the namespace", targetNames(deny), want)
}

func checkExpressionSelector(t *testing.T, res *render.Result) {
	t.Helper()

	allow := selectionOf(t, res, "NetworkPolicy", "api-allow-web")
	expectDeep(t, "api-allow-web selects", targetNames(allow), []string{"Deployment/api"})

	// The expression of the ingress peer is kept in the typed policy for the evaluator, not interpreted here.
	policy := res.Resource(&allow.Selector).NetworkPolicy
	peer := policy.Ingress[0].Peers[0]

	if peer.NamespaceSelector != nil {
		t.Errorf("peer = %+v, want no namespace selector", peer)
	}

	if peer.PodSelector == nil {
		t.Fatalf("peer = %+v, want a pod selector", peer)
	}

	wantExpressions := []render.SelectorRequirement{{Key: "tier", Operator: "In", Values: []string{"web", "edge"}}}
	expectDeep(t, "peer expressions", peer.PodSelector.MatchExpressions, wantExpressions)
	expectDeep(t, "policy types", policy.EffectiveTypes(), []string{"Ingress"})
}

func TestNoIsolationFactExists(t *testing.T) {
	t.Parallel()

	for _, typ := range []reflect.Type{reflect.TypeFor[render.Selection](), reflect.TypeFor[render.Result](), reflect.TypeFor[render.Resource](), reflect.TypeFor[render.NetworkPolicy]()} {
		for i := range typ.NumField() {
			if name := strings.ToLower(typ.Field(i).Name); strings.Contains(name, "isolat") {
				t.Errorf("%s has the field %s: selection is not isolation", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

func TestInitAndEphemeralContainersAreCounted(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain")
	pod := render.ResourceID{Environment: "production", Cluster: "prod-eu", Kind: "Pod", Namespace: "payments", Name: "debug"}

	occurrences := res.OccurrencesOf(&pod)
	kinds := make([]string, 0, len(occurrences))

	for i := range occurrences {
		kinds = append(kinds, occurrences[i].Kind+":"+occurrences[i].Container)
	}

	expectDeep(t, "occurrences", kinds, []string{"container:shell", "init:migrate", "ephemeral:debugger"})

	ports := res.OccurrencesOf(&pod)[0].Ports
	expectLen(t, "ports", ports, 1)
	expectDeep(t, "port", ports[0], render.ContainerPort{Name: "http", ContainerPort: 8080, Protocol: "TCP"})
}

type resourceGetter func(group, kind, ns, name string) *render.Resource

func plainGetter(t *testing.T, res *render.Result) resourceGetter {
	t.Helper()

	return func(group, kind, ns, name string) *render.Resource {
		t.Helper()

		r := res.Resource(&render.ResourceID{Environment: "production", Cluster: "prod-eu", Group: group, Kind: kind, Namespace: ns, Name: name})
		if r == nil {
			t.Fatalf("no resource %s/%s", kind, name)
		}

		return r
	}
}

func TestTypedKinds(t *testing.T) {
	t.Parallel()

	get := plainGetter(t, renderPlainFixture(t, "production", "plain"))

	checkTypedWorkloads(t, get)
	checkTypedService(t, get)
	checkTypedRoutes(t, get)
	checkTypedRBAC(t, get)

	if get("", "ServiceAccount", "payments", "api-sa").ServiceAccount == nil {
		t.Error("no service account facts")
	}

	// Other kinds are inventoried by identity only.
	if ns := get("", "Namespace", "", "payments"); hasTypedFacts(ns) {
		t.Errorf("namespace has typed facts: %+v", ns)
	}
}

func hasTypedFacts(r *render.Resource) bool {
	return r.Workload != nil || r.Service != nil || r.Role != nil
}

func checkTypedWorkloads(t *testing.T, get resourceGetter) {
	t.Helper()

	api := get("apps", "Deployment", "payments", "api").Workload
	expectDeep(t, "api account", api.ServiceAccountName, "api-sa")
	expectDeep(t, "api pod app label", api.PodLabels["app"], "api")

	reports := get("apps", "Deployment", "payments", "reports").Workload
	expectDeep(t, "reports account", reports.ServiceAccountName, "default")
}

func checkTypedService(t *testing.T, get resourceGetter) {
	t.Helper()

	svc := get("", "Service", "payments", "api").Service
	expectDeep(t, "service selector app", svc.Selector["app"], "api")
	expectLen(t, "service ports", svc.Ports, 1)
	expectDeep(t, "service target port", svc.Ports[0].TargetPort.Number, 8080)
	expectDeep(t, "service protocol", svc.Ports[0].Protocol, "TCP")
}

func checkTypedRoutes(t *testing.T, get resourceGetter) {
	t.Helper()

	backends := []render.Backend{{Service: "api", Port: render.PortRef{Number: 80}}}

	ingress := get("networking.k8s.io", "Ingress", "payments", "api").Route
	expectDeep(t, "ingress hosts", ingress.Hosts, []string{"api.example.com"})
	expectDeep(t, "ingress backends", ingress.Backends, backends)

	route := get("gateway.networking.k8s.io", "HTTPRoute", "payments", "api").Route
	expectLen(t, "httproute parent refs", route.ParentRefs, 2)
	expectDeep(t, "httproute first parent", route.ParentRefs[0], render.ParentRef{Kind: "Gateway", Name: "public-gateway"})
	expectDeep(t, "httproute backends", route.Backends, backends)

	gw := get("gateway.networking.k8s.io", "Gateway", "payments", "public-gateway").Gateway
	expectDeep(t, "gateway class", gw.ClassName, "standard")
	expectLen(t, "gateway listeners", gw.Listeners, 1)
	expectDeep(t, "gateway listener port", gw.Listeners[0].Port, 80)
}

func checkTypedRBAC(t *testing.T, get resourceGetter) {
	t.Helper()

	role := get("rbac.authorization.k8s.io", "Role", "payments", "api-role").Role
	expectLen(t, "role rules", role.Rules, 1)
	expectDeep(t, "role rule", role.Rules[0], render.PolicyRule{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}})

	binding := get("rbac.authorization.k8s.io", "RoleBinding", "payments", "api-binding").Binding
	expectDeep(t, "binding role", binding.RoleRef.Name, "api-role")
	expectLen(t, "binding subjects", binding.Subjects, 2)
	expectDeep(t, "binding second subject", binding.Subjects[1], render.Subject{Kind: "ServiceAccount", Name: "ghost-sa", Namespace: "payments"})

	clusterRole := get("rbac.authorization.k8s.io", "ClusterRole", "", "payments-reader")
	if clusterRole.Role == nil || clusterRole.ID.Namespace != "" {
		t.Errorf("clusterrole = %+v", clusterRole)
	}
}

// renderInline renders manifests written to a temporary directory.
func renderInline(t *testing.T, files map[string]string) *render.Result {
	t.Helper()

	root := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, err := render.Render(context.Background(), &render.Options{Root: root, Source: *plainSource("production", "."), Run: forbidden(t)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	return res
}

func TestUnsupportedSelectorIsAGap(t *testing.T) {
	t.Parallel()

	res := renderInline(t, map[string]string{
		"app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: app, image: "app:1"}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: odd, namespace: shop}
spec:
  podSelector:
    matchExpressions:
      - {key: tier, operator: GreaterThan, values: ["1"]}
`,
	})

	if res.Coverage != assurance.CoveragePartial {
		t.Errorf("coverage = %s, want partial", res.Coverage)
	}

	if got := gapKinds(res); !reflect.DeepEqual(got, []string{render.GapUnsupportedSelector}) {
		t.Errorf("gaps = %v", got)
	}

	sel := selectionOf(t, res, "NetworkPolicy", "odd")
	if !sel.Unsupported || len(sel.Targets) != 0 {
		t.Errorf("selection = %+v, want unsupported and not an empty result", sel)
	}
}

func TestTemplatedImageIsAGap(t *testing.T) {
	t.Parallel()

	res := renderInline(t, map[string]string{
		"app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    spec:
      containers: [{name: app, image: "{{ .Values.image }}"}]
`,
	})

	expectDeep(t, "coverage", res.Coverage, assurance.CoveragePartial)
	expectLen(t, "occurrences", res.Occurrences, 1)
	expectDeep(t, "repository", res.Occurrences[0].Repository, "")
	expectDeep(t, "gaps", gapKinds(res), []string{render.GapUnparseableImage})
}

func TestListKindIsExpanded(t *testing.T) {
	t.Parallel()

	res := renderInline(t, map[string]string{
		"list.yaml": `apiVersion: v1
kind: List
items:
  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a}}
  - {apiVersion: v1, kind: ConfigMap, metadata: {name: b}}
`,
	})

	if len(res.Resources) != 2 || res.Coverage != assurance.CoverageComplete {
		t.Errorf("resources %d, coverage %s", len(res.Resources), res.Coverage)
	}
}

func TestDocumentWithoutNameIsAGap(t *testing.T) {
	t.Parallel()

	res := renderInline(t, map[string]string{"x.yaml": "apiVersion: v1\nkind: ConfigMap\n"})

	if res.Coverage != assurance.CoveragePartial || !reflect.DeepEqual(gapKinds(res), []string{render.GapInvalidResource}) {
		t.Errorf("coverage %s, gaps %v", res.Coverage, gapKinds(res))
	}
}
