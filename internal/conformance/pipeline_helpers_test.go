package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// A small in-memory workspace: three services, one declared relationship, three rules,
// a fake CBM graph and a fake git.

func wsText(extra string) string {
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

func calmNode(id string, extra map[string]any) map[string]any {
	n := map[string]any{"unique-id": id, "node-type": "service", "name": id, "description": id}
	for k, v := range extra {
		n[k] = v
	}

	return n
}

func controlOf(extra map[string]any) map[string]any {
	return map[string]any{
		"control-1": map[string]any{
			"description": "Billing boundary",
			"requirements": []any{map[string]any{
				// The CALM control-requirement schema validates the config (control-requirement-validation).
				"requirement-url": "https://calm.finos.org/release/1.2/meta/control-requirement.json",
				"config": merge(map[string]any{
					"control-id": "control-1", "name": "Billing boundary", "description": "Billing uses fulfillment contracts only",
					"engine": "concordia", "rule-ids": []any{"CO-BND-001"},
					"unknown-disposition": "fail", "minimum-policy-coverage": 0.5,
				}, extra),
			}},
		},
	}
}

func merge(base, over map[string]any) map[string]any {
	for k, v := range over {
		base[k] = v
	}

	return base
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func archText(nodeExtra map[string]any) string {
	billing := calmNode("billing-service", merge(map[string]any{
		"concordia": map[string]any{"owner": "team-billing"}, "controls": controlOf(nil),
	}, nodeExtra))

	return mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":   []any{billing, calmNode("fulfillment-service", nil), calmNode("legacy-service", nil)},
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

const bindingsText = `
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: b-billing, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "services/billing/**"}]}}
  - {id: b-fulfillment, architectureRef: "calm:node:fulfillment-service", match: {any: [{sourceRoot: "services/fulfillment/**"}]}}
  - {id: b-legacy, architectureRef: "calm:node:legacy-service", match: {any: [{sourceRoot: "services/legacy/**"}]}}
`

const policyText = `
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
	billingFile = "services/billing/src/Invoice.ts"
	refundFile  = "services/billing/src/Refund.ts"
	storeFile   = "services/fulfillment/src/Store.ts"
	legacyFile  = "services/legacy/Legacy.kt"
)

type graphOpts struct {
	refund  bool
	noEdges bool
}

// buildGraph: billing imports fulfillment (a forbidden dependency). refund adds a second importing file.
func buildGraph(o graphOpts) *testutil.FakeGraph {
	billing := testutil.FileNode("1", billingFile)
	store := testutil.FileNode("2", storeFile)
	legacy := testutil.FileNode("3", legacyFile)
	nodes := []testutil.FakeNode{billing, store, legacy}
	edges := []testutil.FakeEdge{{Type: "IMPORTS", From: &billing, To: &store}}

	if o.refund {
		refund := testutil.FileNode("4", refundFile)
		nodes = append(nodes, refund)
		edges = append(edges, testutil.FakeEdge{Type: "IMPORTS", From: &refund, To: &store})
	}

	if o.noEdges {
		edges = nil
	}

	return testutil.NewFakeGraph("demo", nodes, edges)
}

type setup struct {
	graph *testutil.FakeGraph
	deps  conformance.Deps
	dir   string
	env   conformance.Env
}

type setupOpts struct {
	graph    *testutil.FakeGraph
	ws       string
	revision string
	dirty    []string
	offline  bool
}

// renderStub stands in for the reporting package (which imports conformance).
func renderStub(format workspace.ReportFormat, run *conformance.Run) ([]byte, error) {
	if format == workspace.ReportJSON {
		return conformance.CanonicalJSON(run)
	}

	return []byte("stub " + string(format) + "\n"), nil
}

func newSetup(t *testing.T, files map[string]string, o *setupOpts) *setup {
	t.Helper()

	if o == nil {
		o = &setupOpts{}
	}

	dir := t.TempDir()
	tree := map[string]string{
		"concordia.yaml":                   wsText(""),
		"architecture/a.calm.json":         archText(nil),
		"bindings/concordia.bindings.yaml": bindingsText,
		"policies/demo.policy.yaml":        policyText,
	}

	if o.ws != "" {
		tree["concordia.yaml"] = o.ws
	}

	for k, v := range files {
		tree[k] = v
	}

	testutil.WriteTree(t, dir, tree)

	graph := o.graph
	if graph == nil {
		graph = buildGraph(graphOpts{})
	}

	revision := o.revision
	if revision == "" {
		revision = "abc123"
	}

	var gitFiles []string

	for _, n := range graph.Nodes {
		if n.Label == "File" {
			gitFiles = append(gitFiles, n.Path)
		}
	}

	return &setup{
		dir:   dir,
		graph: graph,
		env: conformance.Env{
			Logger:        logger.New(logger.Options{Out: discard{}, RunID: "run"}),
			RunID:         "20260601T000000Z-deadbeef",
			WorkspacePath: filepath.Join(dir, "concordia.yaml"),
			Cwd:           dir,
			ToolVersion:   "test",
			Offline:       o.offline,
		},
		deps: conformance.Deps{
			CreateClient: func(*workspace.Workspace, *logger.Logger) observation.Client {
				return observation.NewFakeClient(graph.Handler, "")
			},
			CreateGit: func(string) workspace.GitPort {
				return &workspace.FakeGit{Rev: revision, Files: gitFiles, Dirty: o.dirty}
			},
			RenderReport: renderStub,
		},
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (s *setup) check(t *testing.T, edit func(*conformance.CheckOptions)) (*conformance.Run, error) {
	t.Helper()

	opts := &conformance.CheckOptions{Env: s.env, Deps: s.deps, SkipReports: true}
	if edit != nil {
		edit(opts)
	}

	return conformance.RunCheck(context.Background(), opts)
}

func (s *setup) mustCheck(t *testing.T, edit func(*conformance.CheckOptions)) *conformance.Run {
	t.Helper()

	run, err := s.check(t, edit)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}

	return run
}

// failureOf gives the diagnostic error of a failed check.
func (s *setup) failureOf(t *testing.T, edit func(*conformance.CheckOptions)) *diag.Error {
	t.Helper()

	_, err := s.check(t, edit)

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("expected a *diag.Error, got %v", err)
	}

	return de
}

func stable(t *testing.T, run *conformance.Run) string {
	t.Helper()

	text, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	var tree map[string]any
	if err := json.Unmarshal(text, &tree); err != nil {
		t.Fatal(err)
	}

	info, _ := tree["run"].(map[string]any)
	for _, k := range []string{"id", "startedAt", "finishedAt", "host"} {
		delete(info, k)
	}

	out, err := canonjson.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

func sha(text string) string { return digest.SHA256HexString(text) }
