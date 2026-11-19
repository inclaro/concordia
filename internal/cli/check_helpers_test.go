package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// A small in-memory workspace: three services, one declared relationship, three rules,
// a fake CBM graph and a fake git.

func chkWorkspaceText(extra string) string {
	return `
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata: {name: demo}
architecture: {source: architecture/a.calm.json}
observation:
  projects:
    - {name: demo, root: ., include: ["services/**"], exclude: ["services/legacy/**"]}
check: {minimumCoverage: 0.5, unknown: fail, failOn: error}
` + extra
}

func chkNode(id string, extra map[string]any) map[string]any {
	n := map[string]any{"unique-id": id, "node-type": "service", "name": id, "description": id}
	for k, v := range extra {
		n[k] = v
	}

	return n
}

func chkControl(extra map[string]any) map[string]any {
	return map[string]any{
		"control-1": map[string]any{
			"description": "Billing boundary",
			"requirements": []any{map[string]any{
				// The CALM control-requirement schema validates the config (control-requirement-validation).
				"requirement-url": "https://calm.finos.org/release/1.2/meta/control-requirement.json",
				"config": chkMerge(map[string]any{
					"control-id": "control-1", "name": "Billing boundary", "description": "Billing uses fulfillment contracts only",
					"engine": "concordia", "rule-ids": []any{"CO-BND-001"},
					"unknown-disposition": "fail", "minimum-policy-coverage": 0.5,
				}, extra),
			}},
		},
	}
}

func chkMerge(base, over map[string]any) map[string]any {
	for k, v := range over {
		base[k] = v
	}

	return base
}

func chkJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func chkArch() string {
	billing := chkNode("billing-service", map[string]any{
		"concordia": map[string]any{"owner": "team-billing"}, "controls": chkControl(nil),
	})

	return chkJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":   []any{billing, chkNode("fulfillment-service", nil), chkNode("legacy-service", nil)},
		"relationships": []any{map[string]any{
			"unique-id": "billing-to-fulfillment",
			"relationship-type": map[string]any{"connects": map[string]any{
				"source": map[string]any{"node": "billing-service"}, "destination": map[string]any{"node": "fulfillment-service"},
			}},
			"concordia": map[string]any{"observation": map[string]any{
				"allowed-edge-kinds": []any{"IMPORTS"}, "minimum-confidence": "syntactic", "required": true,
			}},
		}},
	})
}

const chkBindings = `
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: b-billing, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "services/billing/**"}]}}
  - {id: b-fulfillment, architectureRef: "calm:node:fulfillment-service", match: {any: [{sourceRoot: "services/fulfillment/**"}]}}
  - {id: b-legacy, architectureRef: "calm:node:legacy-service", match: {any: [{sourceRoot: "services/legacy/**"}]}}
`

const chkPolicy = `
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: demo-pack, version: 1.0.0}
rules:
  - id: CO-BND-001
    name: Billing uses fulfillment contracts only
    severity: error
    owner: team-billing
    rationale: Billing must not import fulfillment internals.
    remediation: Use the fulfillment contract.
    subject: {calmNodes: [billing-service]}
    forbid:
      target: {calmNodes: [fulfillment-service]}
      edges: [IMPORTS, CALLS]
      traversal: direct
  - id: CO-DECL-001
    name: Dependencies are declared
    severity: error
    subject: {nodeTypes: [service]}
    declaredOnly: {edges: [IMPORTS]}
  - id: CO-COV-001
    name: Legacy must not call Billing
    severity: error
    subject: {calmNodes: [legacy-service]}
    forbid:
      target: {calmNodes: [billing-service]}
      edges: [CALLS]
      traversal: direct
    evidence: {minimumConfidence: resolved, minimumCoverage: 0.9, onUnknown: fail}
`

const (
	chkBillingFile = "services/billing/src/Invoice.ts"
	chkStoreFile   = "services/fulfillment/src/Store.ts"
	chkLegacyFile  = "services/legacy/Legacy.kt"
)

// chkGraph: billing imports fulfillment (a forbidden dependency).
func chkGraph() *testutil.FakeGraph {
	billing := testutil.FileNode("1", chkBillingFile)
	store := testutil.FileNode("2", chkStoreFile)
	legacy := testutil.FileNode("3", chkLegacyFile)
	nodes := []testutil.FakeNode{billing, store, legacy}
	edges := []testutil.FakeEdge{{Type: "IMPORTS", From: &billing, To: &store}}

	return testutil.NewFakeGraph("demo", nodes, edges)
}

// chkSetup is a workspace on disk with a fake CBM graph and a fake git.
type chkSetup struct {
	graph *testutil.FakeGraph
	cfg   *runConfig
	dir   string
}

func newChkSetup(t *testing.T, files map[string]string, graph *testutil.FakeGraph) *chkSetup {
	t.Helper()

	dir := t.TempDir()
	tree := map[string]string{
		"concordia.yaml":                   chkWorkspaceText(""),
		"architecture/a.calm.json":         chkArch(),
		"bindings/concordia.bindings.yaml": chkBindings,
		"policies/demo.policy.yaml":        chkPolicy,
	}

	for k, v := range files {
		tree[k] = v
	}

	testutil.WriteTree(t, dir, tree)

	if graph == nil {
		graph = chkGraph()
	}

	var gitFiles []string

	for _, n := range graph.Nodes {
		if n.Label == "File" {
			gitFiles = append(gitFiles, n.Path)
		}
	}

	return &chkSetup{
		dir:   dir,
		graph: graph,
		cfg: &runConfig{Dir: dir, Env: map[string]string{"CONCORDIA_NOW": "2026-06-01T00:00:00Z"}, Deps: cli.Deps{
			NewClient: func(*workspace.Workspace, *cli.Context) observation.Client {
				return observation.NewFakeClient(graph.Handler, "")
			},
			NewGit: func(string) workspace.GitPort { return &workspace.FakeGit{Rev: "abc123", Files: gitFiles} },
		}},
	}
}

func (s *chkSetup) run(t *testing.T, args ...string) result {
	t.Helper()

	return runWith(t, s.cfg, args...)
}
