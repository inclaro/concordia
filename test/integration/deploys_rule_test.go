package integration_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// A43 (EVD-07): a rule that names the `deploys` token gets evidence from the native
// DEPLOYS edges of the infra recording (`deploys-artifact`). The rule gives `pass` or
// `violation`, never `unknown` for provider support, and CO-POL-009 is not raised.

const (
	deploysFixtureDir = "../fixtures/infra"

	deploysWorkspace = `apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: infra-deploys
architecture:
  source: architecture/infra.calm.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: concordia-fixture-infra
      root: .
      include: [scripts/**, k8s/**, charts/**]
  indexMode: fast
bindings:
  files: [bindings/concordia.bindings.yaml]
policies:
  files: [policies/**/*.policy.yaml]
check:
  minimumCoverage: 0
  unknown: fail
  failOn: error
`

	deploysArchitecture = `{
  "nodes": [
    {"unique-id": "ci-scripts", "node-type": "service", "name": "CI scripts", "description": "Deployment scripts."},
    {"unique-id": "prod-manifests", "node-type": "service", "name": "Production manifests", "description": "Production Kubernetes manifests."},
    {"unique-id": "helm-charts", "node-type": "service", "name": "Helm charts", "description": "Helm charts."}
  ],
  "relationships": []
}
`

	deploysBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-ci-scripts
    architectureRef: calm:node:ci-scripts
    exclusive: true
    match:
      any:
        - sourceRoot: scripts/**
  - id: bind-prod-manifests
    architectureRef: calm:node:prod-manifests
    exclusive: true
    match:
      any:
        - sourceRoot: k8s/prod/**
  - id: bind-helm-charts
    architectureRef: calm:node:helm-charts
    exclusive: true
    match:
      any:
        - sourceRoot: charts/**
`

	deploysPolicy = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: infra-deploys
  version: 1.0.0
rules:
  - id: CO-DEP-001
    name: Scripts do not deploy production manifests
    severity: error
    subject:
      calmNodes: [ci-scripts]
    forbid:
      target:
        calmNodes: [prod-manifests]
      edges: [deploys]
      traversal: direct
  - id: CO-DEP-002
    name: Manifests do not deploy scripts
    severity: error
    subject:
      calmNodes: [prod-manifests]
    forbid:
      target:
        calmNodes: [ci-scripts]
      edges: [DEPLOYS]
      traversal: direct
`
)

func writeDeploysFile(t *testing.T, dir, rel, text string) {
	t.Helper()

	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFoundationA43DeploysRuleOnTheInfraRecording(t *testing.T) {
	run := checkDeploysWorkspace(t, deploysWorkspace)

	for i := range run.Diagnostics {
		if run.Diagnostics[i].Code == diag.POL009 {
			t.Errorf("CO-POL-009 was raised: %s", run.Diagnostics[i].Message)
		}
	}

	verdicts := verdictsOf(run)

	// The script deploys the production deployment manifest (`kubectl apply -f`, O8), so the
	// forbidden dependency is a violation; the reverse direction has no deploy action.
	if verdicts["CO-DEP-001"] != conformance.VerdictViolation {
		t.Errorf("CO-DEP-001 verdict = %q, want violation", verdicts["CO-DEP-001"])
	}

	if verdicts["CO-DEP-002"] != conformance.VerdictPass {
		t.Errorf("CO-DEP-002 verdict = %q, want pass", verdicts["CO-DEP-002"])

		for i := range run.Rules {
			if run.Rules[i].RuleID == "CO-DEP-002" {
				t.Logf("CO-DEP-002 reasons %+v", run.Rules[i].Reasons)
			}
		}
	}

	for i := range run.Rules {
		for _, reason := range run.Rules[i].Reasons {
			if reason.Code == diag.POL009 {
				t.Errorf("%s has the reason CO-POL-009", run.Rules[i].RuleID)
			}
		}
	}

	findings := findingsOf(run, "CO-DEP-001")
	if len(findings) == 0 {
		t.Fatal("CO-DEP-001 has no finding")
	}

	for i := range findings {
		if findings[i].Edge == nil || findings[i].Edge.Kind != observation.KindDeploysArtifact {
			t.Errorf("finding %d edge = %+v, want deploys-artifact evidence", i, findings[i].Edge)
		}
	}
}
