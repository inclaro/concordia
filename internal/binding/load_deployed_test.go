package binding_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

func loadWith(t *testing.T, file string, model *intent.ArchitectureModel, opts ...binding.LoadOption) binding.Loaded {
	t.Helper()

	return loadTree(t, map[string]string{"bindings/a.yaml": file}, model, opts...)
}

// deployedBody gives a BindingSet file with one binding, whose only match item is match.
func deployedBody(match string) string {
	const flow = "    match:\n      any:\n        - "

	return header + "  - id: b1\n    architectureRef: calm:node:billing\n    cardinality: one\n    agreement: any\n    status: proposed\n    authority: platform-team\n" + flow + match + "\n"
}

// validDeployedMatches gives the match items that load, each with the Match it becomes.
func validDeployedMatches() map[string]binding.Match {
	return map[string]binding.Match{
		"{kind: workload, environment: production, apiGroup: apps, resourceKind: Deployment, namespace: payments, name: billing}": {
			Kind: binding.MatchWorkload, Environment: "production", APIGroup: "apps", ResourceKind: "Deployment", Namespace: "payments", Name: "billing",
		},
		"{kind: image, repository: ghcr.io/acme/billing, digest: 'sha256:abc'}": {
			Kind: binding.MatchImage, Repository: "ghcr.io/acme/billing", Digest: "sha256:abc",
		},
		"{kind: route, environment: production, name: billing-ing}": {
			Kind: binding.MatchRoute, Environment: "production", Name: "billing-ing",
		},
		"{kind: infraResource, environment: production, address: 'kubernetes_namespace.payments'}": {
			Kind: binding.MatchInfraResource, Environment: "production", Address: "kubernetes_namespace.payments",
		},
		"{kind: composeService, file: compose.yaml, service: billing}": {
			Kind: binding.MatchComposeService, File: "compose.yaml", Service: "billing",
		},
		"{kind: sourceRoot, value: 'a/**'}": {Kind: binding.MatchSourceRoot, Value: "a/**"},
	}
}

// invalidDeployedMatches gives match items that a load reports as CO-BND-002, by name.
func invalidDeployedMatches() map[string]string {
	return map[string]string{
		"missing name":       "{kind: workload, environment: production, resourceKind: Deployment}",
		"unknown key":        "{kind: workload, environment: production, resourceKind: Deployment, name: x, bogus: y}",
		"foreign field":      "{kind: image, repository: r, name: x}",
		"undeclared env":     "{kind: route, environment: qa, name: x}",
		"no environment":     "{kind: infraResource, address: a}",
		"compose no service": "{kind: composeService, file: compose.yaml}",
		"unknown kind":       "{kind: nope, value: x}",
	}
}

// isOnlyMatch says whether l has no diagnostic and one binding that has the one match want.
func isOnlyMatch(l *binding.Loaded, want *binding.Match) bool {
	if len(l.Diagnostics) != 0 || len(l.Bindings) != 1 {
		return false
	}

	matches := l.Bindings[0].Match

	return len(matches) == 1 && matches[0] == *want
}

// checkValidDeployedMatch loads the binding of a match item that is valid in the environment production.
func checkValidDeployedMatch(t *testing.T, match string, want *binding.Match) {
	t.Helper()

	got := loadWith(t, deployedBody(match), modelWith(calmNode()), binding.WithEnvironments([]string{"production"}))
	if !isOnlyMatch(&got, want) {
		t.Errorf("%s: %+v", match, got)

		return
	}

	b := got.Bindings[0]
	if !slices.Equal([]string{b.Cardinality, b.Agreement, b.Status, b.Authority}, []string{"one", "any", "proposed", "platform-team"}) {
		t.Errorf("%s: binding fields %+v", match, b)
	}
}

func TestLoadDeployedResourceMatchKinds(t *testing.T) {
	t.Parallel()

	for match, want := range validDeployedMatches() {
		checkValidDeployedMatch(t, match, &want)
	}

	for name, match := range invalidDeployedMatches() {
		got := loadWith(t, deployedBody(match), modelWith(calmNode()), binding.WithEnvironments([]string{"production"}))
		if codes := codesOf(&got); !slices.Contains(codes, diag.BND002) {
			t.Errorf("%s: codes = %v", name, codes)
		}
	}

	// The workspace environments are the default; a bad match drops the match (not the binding).
	got := loadWith(t, deployedBody("{kind: route, environment: qa, name: x}"), modelWith(calmNode()))
	if len(got.Diagnostics) != 1 {
		t.Errorf("default environments: %+v", got.Diagnostics)
	}

	got = loadWith(t, deployedBody("{kind: route, environment: qa, name: x}"), modelWith(calmNode()), binding.WithEnvironments(nil))
	if len(got.Bindings) != 1 || len(got.Bindings[0].Match) != 0 {
		t.Errorf("undeclared env: %+v", got.Bindings)
	}
}

// checkFirstExtensionBinding checks the binding of the `infra-resource` extension item of the test below.
func checkFirstExtensionBinding(t *testing.T, first *binding.Binding) {
	t.Helper()

	want := binding.Match{Kind: binding.MatchInfraResource, Environment: "production", Address: "kubernetes_namespace.payments"}
	if first.Match[0] != want {
		t.Errorf("first = %+v", *first)
	}

	if !slices.Equal([]string{first.Cardinality, first.Agreement, first.Status, first.Authority}, []string{"one", "all", "active", "me"}) {
		t.Errorf("first = %+v", *first)
	}
}

func TestLoadDefaultsAndExtensionKinds(t *testing.T) {
	t.Parallel()

	model := modelWith(calmNode(
		intent.ExtBinding{Kind: "infra-resource", Environment: "production", Address: "kubernetes_namespace.payments", Cardinality: "one", Authority: "me"},
		intent.ExtBinding{Kind: "compose-service", File: "compose.yaml", Service: "billing"},
		intent.ExtBinding{Kind: "image", Repository: "r"},
		intent.ExtBinding{Kind: "workload", Environment: "production", ResourceKind: "Deployment"},
	))

	got := loadWith(t, "apiVersion: concordia.io/v1alpha1\nkind: BindingSet\nbindings: []\n", model, binding.WithEnvironments([]string{"production"}))
	if codes := codesOf(&got); !slices.Equal(codes, []string{diag.BND002}) {
		t.Fatalf("codes = %v (%+v)", codes, got.Diagnostics) // the workload has no name
	}

	checkFirstExtensionBinding(t, &got.Bindings[0])

	kinds := []binding.MatchKind{got.Bindings[1].Match[0].Kind, got.Bindings[2].Match[0].Kind}
	if !slices.Equal(kinds, []binding.MatchKind{binding.MatchComposeService, binding.MatchImage}) || got.Bindings[1].Cardinality != "many" {
		t.Errorf("bindings = %+v", got.Bindings)
	}
}
