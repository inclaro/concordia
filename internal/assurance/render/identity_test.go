package render_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
)

const duplicateDeployments = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: app, image: "registry.example.com/api:1"}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: app, image: "registry.example.com/api:2"}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: worker, namespace: shop}
spec:
  template:
    spec:
      containers: [{name: app, image: "registry.example.com/worker:1"}]
`

func TestDuplicateIdentityIsAConflict(t *testing.T) {
	t.Parallel()

	res := renderInline(t, map[string]string{"app.yaml": duplicateDeployments})

	if res.Coverage != assurance.CoveragePartial {
		t.Errorf("coverage = %s, want partial: claims about the workload are unknown", res.Coverage)
	}

	expectDeep(t, "gaps", gapKinds(res), []string{render.GapDuplicateIdentity})

	found := anyDiagnostic(res, isDuplicateWarning)
	if !found {
		t.Errorf("no CO-PRV-007 warning: %+v", res.Diagnostics)
	}

	if len(res.Resources) != 2 {
		t.Fatalf("two documents with one identity are not two instances: %d resources", len(res.Resources))
	}

	checkConflictMarks(t, res)
}

// checkConflictMarks checks that the first api Deployment is the conflict, and that only the worker is listed.
func checkConflictMarks(t *testing.T, res *render.Result) {
	t.Helper()

	api := res.Resources[0]
	if api.ID.Name != "api" || !api.Conflict {
		t.Errorf("resources = %+v, %+v", api.ID, res.Resources[1].ID)
	}

	if res.Resources[1].Conflict {
		t.Errorf("resources = %+v, %+v", api.ID, res.Resources[1].ID)
	}

	// The occurrences of the conflicting workload are not listed, the others are.
	if len(res.Occurrences) != 1 || res.Occurrences[0].Workload.Name != "worker" {
		t.Errorf("occurrences = %+v", res.Occurrences)
	}
}

// isDuplicateWarning tells whether d is the warning about the duplicate Deployment shop/api.
func isDuplicateWarning(d *diag.Diagnostic) bool {
	if d.Code != diag.PRV007 || d.Severity != diag.SeverityWarning {
		return false
	}

	return strings.Contains(d.Message, "Deployment/shop/api")
}

func TestDuplicateChangesTheOutputDigest(t *testing.T) {
	t.Parallel()

	one := renderInline(t, map[string]string{"app.yaml": duplicateDeployments})
	two := renderInline(t, map[string]string{"app.yaml": strings.Replace(duplicateDeployments, "api:2", "api:3", 1)})

	if one.OutputDigest == two.OutputDigest {
		t.Error("a changed duplicate must change the output digest")
	}
}

func TestClusterScopedKindHasNoNamespace(t *testing.T) {
	t.Parallel()

	res := renderInline(t, map[string]string{"rbac.yaml": `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: reader, namespace: ignored}
rules: []
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec: {template: {spec: {containers: []}}}
---
apiVersion: example.com/v1
kind: Widget
metadata: {name: custom}
`})

	byKind := map[string]render.ResourceID{}
	for _, r := range res.Resources {
		byKind[r.ID.Kind] = r.ID
	}

	if byKind["ClusterRole"].Namespace != "" {
		t.Errorf("ClusterRole namespace = %q", byKind["ClusterRole"].Namespace)
	}

	if byKind["Deployment"].Namespace != "default" {
		t.Errorf("a namespaced kind with no namespace is in default: %q", byKind["Deployment"].Namespace)
	}

	if byKind["Widget"].Namespace != "" {
		t.Errorf("a custom kind keeps the namespace as written: %q", byKind["Widget"].Namespace)
	}
}

func TestEnvironmentsKeepSeparateScopedIdentities(t *testing.T) {
	t.Parallel()

	staging := renderPlainFixture(t, "staging", "plain")
	production := renderPlainFixture(t, "production", "plain")

	if len(staging.Resources) != len(production.Resources) || len(staging.Resources) == 0 {
		t.Fatalf("%d and %d resources", len(staging.Resources), len(production.Resources))
	}

	for i := range staging.Resources {
		checkSeparateEnvironments(t, &staging.Resources[i].ID, &production.Resources[i].ID)
	}

	// The documents are the same, and the digest covers identities, which differ: the digests must differ.
	if staging.OutputDigest == production.OutputDigest {
		t.Error("the output digest must cover the scoped identity")
	}
}

// checkSeparateEnvironments checks that two identities differ in their environment and in nothing else.
func checkSeparateEnvironments(t *testing.T, staging, production *render.ResourceID) {
	t.Helper()

	if *staging == *production {
		t.Errorf("identities %v and %v must differ in their environment", *staging, *production)
	}

	if staging.Environment != "staging" || production.Environment != "production" {
		t.Errorf("identities %v and %v must differ in their environment", *staging, *production)
	}

	same := *staging
	same.Environment = production.Environment

	if same != *production {
		t.Errorf("identities %v and %v differ in more than the environment", *staging, *production)
	}
}
