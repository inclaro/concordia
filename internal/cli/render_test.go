package cli_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/testutil"
)

func testNow() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

const renderArch = `{"nodes":[
 {"unique-id":"billing-service","node-type":"service","name":"Billing","description":"d"},
 {"unique-id":"ledger","node-type":"database","name":"Ledger","description":"d"}],
 "relationships":[{"unique-id":"r1","relationship-type":{"connects":{"source":{"node":"billing-service"},"destination":{"node":"ledger"}}}}]}`

func TestRenderDSLWritesAWorkspaceAndExportNeedsTheCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml": "architecture:\n  source: arch.json\nobservation:\n  projects:\n    - {name: p, root: .}\n",
		"arch.json":      renderArch,
		"results.json":   `{"findings":[],"reconciliation":[],"rules":[]}`,
	})

	res := runIn(t, dir, "render", "--results", "results.json", "--workspace-out", "out.dsl")
	if res.Code != 0 || strings.Contains(res.Stderr, "CO-") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if dsl := readFile(t, dir, "out.dsl"); !strings.Contains(dsl, `"calm.unique-id" "billing-service"`) {
		t.Errorf("the DSL lacks the CALM ID:\n%s", dsl)
	}

	// The default output is <projection.output>/workspace.dsl.
	if res = runIn(t, dir, "render", "--results", "results.json"); res.Code != 0 {
		t.Fatalf("default output: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exists(dir, "build/concordia/structurizr/workspace.dsl") {
		t.Errorf("no workspace.dsl in the default output directory")
	}

	bad := runWith(t, &runConfig{Dir: dir, Env: map[string]string{"PATH": ""}}, "--format", "mermaid", "render", "--results", "results.json")
	if bad.Code != 5 || !containsEvery(bad.Stderr, "CO-PRJ-001: Structurizr CLI not available; it is needed for --format mermaid", "hint: install the Structurizr CLI") {
		t.Errorf("mermaid without the CLI: exit code = %d, stderr = %q", bad.Code, bad.Stderr)
	}
}

func TestRenderRejectsBadOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml": "architecture:\n  source: arch.json\nobservation:\n  projects:\n    - {name: p, root: .}\n",
		"arch.json":      renderArch,
	})

	for _, args := range [][]string{
		{"--format", "yaml", "render"},
		{"render", "--view", "nope"},
		{"render", "--focus", "missing", "--results", "none.json"},
		{"render", "--results", "none.json"},
	} {
		if res := runIn(t, dir, args...); res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}
}

func TestRenderRunsTheCheckWithoutResults(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "render", "--view", "landscape")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exists(s.dir, "build/concordia/structurizr/workspace.dsl") {
		t.Errorf("no workspace.dsl")
	}

	if exists(s.dir, "build/concordia/results.json") {
		t.Errorf("render wrote the results file")
	}
}

const dynamicBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: b-billing, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "services/billing/**"}]}}
  - {id: b-fulfillment, architectureRef: "calm:node:fulfillment-service", match: {any: [{sourceRoot: "services/fulfillment/**"}]}}
`

const dynamicArch = `{"nodes":[
 {"unique-id":"billing-service","node-type":"service","name":"Billing","description":"d"},
 {"unique-id":"fulfillment-service","node-type":"service","name":"Fulfillment","description":"d"}],
 "relationships":[]}`

func dynamicDir(t *testing.T) string {
	t.Helper()

	text, err := canonjson.Marshal(testutil.SampleRun())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml":                   "architecture:\n  source: arch.json\nobservation:\n  projects:\n    - {name: p, root: .}\n",
		"arch.json":                        dynamicArch,
		"bindings/concordia.bindings.yaml": dynamicBindings,
		"results.json":                     string(text),
	})

	return dir
}

func TestRenderFindingWritesADynamicView(t *testing.T) {
	t.Parallel()

	dir := dynamicDir(t)

	res := runIn(t, dir, "render", "--results", "results.json", "--finding", testutil.FPActive[7:15], "--workspace-out", "out.dsl")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	dsl := readFile(t, dir, "out.dsl")
	wantContains(t, dsl, "dynamic ", `"concordia:evidence"`, "IMPORTS syntactic services/billing/src/InvoiceService.ts:18")

	if bad := runIn(t, dir, "render", "--results", "results.json", "--view", "dynamic"); bad.Code != 2 || !strings.Contains(bad.Stderr, "CO-WRK-010") {
		t.Errorf("--view dynamic without --finding: exit code = %d, stderr = %q", bad.Code, bad.Stderr)
	}

	if bad := runIn(t, dir, "render", "--results", "results.json", "--finding", "zzzz1234"); bad.Code != 2 || !strings.Contains(bad.Stderr, "CO-WRK-010") {
		t.Errorf("unknown finding: exit code = %d, stderr = %q", bad.Code, bad.Stderr)
	}
}

func TestRenderOpenCallsDepsOpenWithTheDSLPath(t *testing.T) {
	t.Parallel()

	dir := dynamicDir(t)

	var opened []string

	cfg := &runConfig{Dir: dir, Deps: cli.Deps{Open: func(p string) error {
		opened = append(opened, p)

		return nil
	}}}

	res := runWith(t, cfg, "render", "--results", "results.json", "--workspace-out", "out.dsl", "--open")
	if res.Code != 0 || len(opened) != 1 || filepath.Base(opened[0]) != "out.dsl" {
		t.Fatalf("exit code = %d, opened = %v, stderr = %q", res.Code, opened, res.Stderr)
	}

	failing := &runConfig{Dir: dir, Deps: cli.Deps{Open: func(string) error { return errors.New("no viewer") }}}

	res = runWith(t, failing, "render", "--results", "results.json", "--workspace-out", "out.dsl", "--open")
	if res.Code != 0 || !strings.Contains(res.Stderr, "CO-PRJ-007") {
		t.Errorf("failing open: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
