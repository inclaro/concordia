package workspace_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

const assuranceFull = `
environments:
  - {id: production, sources: [deploy/**]}
  - {id: staging, sources: [deploy/**]}
  - {id: dev, sources: [deploy/**]}
assurance:
  rendering:
    requireOsSandbox: false
    limits: {timeoutSeconds: 60}
    sources:
      - {environment: production, tool: helm, path: deploy/chart, valuesFiles: [deploy/values/prod.yaml], release: shop, namespace: shop}
      - {environment: staging, tool: kustomize, path: deploy/overlays/staging}
      - {environment: dev, tool: plain, path: deploy/plain}
  plans:
    - {environment: production, tool: terraform, file: build/plans/prod.json, statement: build/plans/prod.intoto.json, maxAgeDays: 7}
  producers:
    - id: ci-build
      claims: [build-provenance, check-result, measurement, plan, deployment-record]
      authentication:
        method: cosign-keyless
        issuer: "https://token.actions.githubusercontent.com"
        identity: "https://github.com/acme/shop/.github/workflows/build.yml@refs/heads/main"
    - id: platform
      claims: [workload-observation]
      authentication: {method: cosign-key, key: "awskms:///alias/platform"}
  provenance:
    statements: ["build/attestations/*.json"]
    builders: ["https://github.com/acme/shop/.github/workflows/build.yml"]
    sourceRepositories: [shop]
    maxAgeDays: 30
  records: {deployments: ["build/records/deploy-*.json"], observations: ["build/records/observed-*.json"], maxAgeDays: 7}
  evaluators:
    - {environment: production, kinds: [network-policy, rbac], assumes: [network-policy-enforced, rbac-authorizer-enabled]}
  verification: {results: ["build/results/*.json"], maxAgeDays: 14, trustedRoot: .sigstore/trusted_root.json}
  quality: {scenarios: ["quality/*.yaml"], measurements: ["build/measurements/*.json"], requiredAreas: [operability]}
`

func assuranceWorkspace(assuranceBlock string) string {
	return baseWorkspace + "\n" + assuranceBlock
}

func TestLoadAssuranceFullExampleWithDefaults(t *testing.T) {
	t.Parallel()

	space, err := workspace.Load(writeWorkspace(t, assuranceWorkspace(assuranceFull)), "")
	if err != nil {
		t.Fatal(err)
	}

	got := space.Assurance
	if got == nil || got.Rendering == nil || got.Provenance == nil || got.Records == nil || got.Verification == nil || got.Quality == nil {
		t.Fatalf("assurance not loaded: %+v", got)
	}

	wantLimits := workspace.RenderingLimits{TimeoutSeconds: 60, MaxOutputBytes: 64 << 20, MaxResources: 20000}
	if got.Rendering.Limits != wantLimits {
		t.Errorf("limits %+v, want %+v", got.Rendering.Limits, wantLimits)
	}

	if want := (workspace.RenderSource{
		Environment: "production", Tool: workspace.RenderToolHelm, Path: "deploy/chart",
		ValuesFiles: []string{"deploy/values/prod.yaml"}, Release: "shop", Namespace: "shop",
	}); !reflect.DeepEqual(got.Rendering.Sources[0], want) {
		t.Errorf("source %+v", got.Rendering.Sources[0])
	}

	if got.Producers[1].Authentication.Key != "awskms:///alias/platform" || got.Producers[0].Authentication.Method != workspace.AuthCosignKeyless {
		t.Errorf("producers %+v", got.Producers)
	}

	if got.Verification.TrustedRoot != ".sigstore/trusted_root.json" || got.Evaluators[0].Assumes[1] != workspace.AssumeRBACAuthorizerEnabled {
		t.Errorf("verification %+v evaluators %+v", got.Verification, got.Evaluators)
	}

	if !got.UsesRenderer(workspace.RenderToolHelm) || !got.UsesRenderer(workspace.RenderToolKustomize) {
		t.Error("renderers in use not found")
	}
}

func TestLoadWithoutAssuranceKeyGivesNil(t *testing.T) {
	t.Parallel()

	space, err := workspace.Load(writeWorkspace(t, baseWorkspace), "")
	if err != nil {
		t.Fatal(err)
	}

	if space.Assurance != nil || space.Assurance.UsesRenderer(workspace.RenderToolHelm) {
		t.Errorf("assurance %+v", space.Assurance)
	}
}

func TestLoadAssuranceRenderingDefaults(t *testing.T) {
	t.Parallel()

	space, err := workspace.Load(writeWorkspace(t, assuranceWorkspace(`
environments:
  - {id: dev, sources: [deploy/**]}
assurance:
  rendering:
    sources: [{environment: dev, tool: plain, path: deploy/plain}]
`)), "")
	if err != nil {
		t.Fatal(err)
	}

	want := workspace.RenderingLimits{
		TimeoutSeconds: workspace.DefaultRenderTimeoutSeconds, MaxOutputBytes: workspace.DefaultRenderMaxOutputBytes, MaxResources: workspace.DefaultRenderMaxResources,
	}
	if space.Assurance.Rendering.Limits != want || want.TimeoutSeconds != 120 || want.MaxOutputBytes != 64<<20 || want.MaxResources != 20000 {
		t.Errorf("limits %+v", space.Assurance.Rendering.Limits)
	}
}

func TestLoadAssuranceRejections(t *testing.T) {
	t.Parallel()

	const envs = "environments:\n  - {id: production, sources: [deploy/**]}\n"

	tests := []struct {
		name  string
		block string
		code  string
		text  string
	}{
		{"unknown property", envs + "assurance:\n  surprise: true\n", diag.PRV001, "surprise"},
		{"unknown property in rendering", envs + "assurance:\n  rendering:\n    sources: [{environment: production, tool: plain, path: a}]\n    other: 1\n", diag.PRV001, "other"},
		{"unknown environment in a rendering source", envs + "assurance:\n  rendering:\n    sources: [{environment: qa, tool: plain, path: a}]\n", diag.WRK017, "'qa'"},
		{"unknown environment in a plan", envs + "assurance:\n  plans: [{environment: qa, tool: terraform, file: p.json}]\n", diag.WRK017, "'qa'"},
		{"unknown environment in an evaluator", envs + "assurance:\n  evaluators: [{environment: qa, kinds: [rbac]}]\n", diag.WRK017, "'qa'"},
		{"no environments declared", "assurance:\n  evaluators: [{environment: qa, kinds: [rbac]}]\n", diag.WRK017, "'qa'"},
		{"producer without authentication", envs + "assurance:\n  producers: [{id: ci, claims: [plan]}]\n", diag.PRV001, "authentication"},
		{"cosign-key without key", envs + "assurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: cosign-key}}]\n", diag.PRV001, "key"},
		{"cosign-key with issuer", envs + "assurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: cosign-key, key: k.pub, issuer: x}}]\n", diag.PRV001, "issuer"},
		{"keyless without identity", envs + "assurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: cosign-keyless, issuer: x}}]\n", diag.PRV001, "identity"},
		{"keyless with key", envs + "assurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: cosign-keyless, issuer: x, identity: y, key: k.pub}}]\n", diag.PRV001, "key"},
		{"unknown authentication method", envs + "assurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: password}}]\n", diag.PRV001, "method"},
		{"unknown producer claim", envs + "assurance:\n  producers: [{id: ci, claims: [coffee], authentication: {method: cosign-key, key: k.pub}}]\n", diag.PRV001, "claims"},
		{"helm field on a kustomize source", envs + "assurance:\n  rendering:\n    sources: [{environment: production, tool: kustomize, path: a, release: shop}]\n", diag.PRV001, "release"},
		{"duplicate producer", envs + "assurance:\n  producers:\n    - {id: ci, claims: [plan], authentication: {method: cosign-key, key: k.pub}}\n    - {id: ci, claims: [plan], authentication: {method: cosign-key, key: k.pub}}\n", diag.PRV001, "'ci' appears two times"},
		{"duplicate evaluator environment", envs + "assurance:\n  evaluators:\n    - {environment: production, kinds: [rbac]}\n    - {environment: production, kinds: [network-policy]}\n", diag.PRV001, "appears two times"},
		{"duplicate source", envs + "assurance:\n  rendering:\n    sources:\n      - {environment: production, tool: plain, path: a}\n      - {environment: production, tool: plain, path: a}\n", diag.PRV001, "appears two times"},
		{"unknown evaluator kind", envs + "assurance:\n  evaluators: [{environment: production, kinds: [opa]}]\n", diag.PRV001, "kinds"},
		{"zero timeout", envs + "assurance:\n  rendering:\n    limits: {timeoutSeconds: 0}\n    sources: [{environment: production, tool: plain, path: a}]\n", diag.PRV001, "timeoutSeconds"},
		{"trusted root outside the root", envs + "assurance:\n  verification: {results: [r/*.json], trustedRoot: ../root.json}\n", diag.PRV002, "../root.json"},
		{"absolute trusted root", envs + "assurance:\n  verification: {results: [r/*.json], trustedRoot: /etc/root.json}\n", diag.PRV002, "/etc/root.json"},
		{"rendering path outside", envs + "assurance:\n  rendering:\n    sources: [{environment: production, tool: plain, path: ../elsewhere}]\n", diag.PRV002, "../elsewhere"},
		{"values file outside", envs + "assurance:\n  rendering:\n    sources: [{environment: production, tool: helm, path: c, valuesFiles: [a/../../v.yaml]}]\n", diag.PRV002, "v.yaml"},
		{"plan file outside", envs + "assurance:\n  plans: [{environment: production, tool: terraform, file: ../p.json}]\n", diag.PRV002, "../p.json"},
		{"statement glob outside", envs + "assurance:\n  provenance: {statements: ['../**/*.json']}\n", diag.PRV002, "../**/*.json"},
		{"records glob outside", envs + "assurance:\n  records: {deployments: ['/var/records/*.json']}\n", diag.PRV002, "/var/records"},
		{"quality glob outside", envs + "assurance:\n  quality: {scenarios: ['q/../../*.yaml']}\n", diag.PRV002, "q/../../*.yaml"},
		{"producer key file outside", envs + "assurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: cosign-key, key: ../k.pub}}]\n", diag.PRV002, "../k.pub"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.Load(writeWorkspace(t, assuranceWorkspace(tc.block)), "")
			if err == nil {
				t.Fatal("expected an error")
			}

			de := failure(t, err)
			if de.Diagnostic.Code != tc.code || de.ExitCode != diag.InvalidInput {
				t.Fatalf("code %s exit %d: %v", de.Diagnostic.Code, de.ExitCode, err)
			}

			texts := []string{de.Error()}
			for _, cause := range de.Diagnostic.Cause {
				texts = append(texts, cause.Message)
			}

			all := strings.Join(texts, " ")

			if !strings.Contains(all, tc.text) {
				t.Errorf("%q not in %q", tc.text, all)
			}
		})
	}
}

func TestLoadAssuranceKMSKeyReferenceIsNotAPath(t *testing.T) {
	t.Parallel()

	block := "environments:\n  - {id: production, sources: [deploy/**]}\nassurance:\n  producers: [{id: ci, claims: [plan], authentication: {method: cosign-key, key: 'env://COSIGN_PUBLIC_KEY'}}]\n"
	if _, err := workspace.Load(writeWorkspace(t, assuranceWorkspace(block)), ""); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAssuranceInAProfile(t *testing.T) {
	t.Parallel()

	yaml := baseWorkspace + `
environments:
  - {id: production, sources: [deploy/**]}
`
	yaml = strings.Replace(yaml, "profiles:\n  ci:\n", "profiles:\n  ci:\n    assurance:\n      verification: {results: [r/*.json]}\n", 1)

	space, err := workspace.Load(writeWorkspace(t, yaml), "ci")
	if err != nil {
		t.Fatal(err)
	}

	if space.Assurance == nil || space.Assurance.Verification == nil {
		t.Fatalf("assurance %+v", space.Assurance)
	}
}
