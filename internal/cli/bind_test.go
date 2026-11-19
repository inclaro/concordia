package cli_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const bindWorkspaceYAML = `
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata: {name: demo}
architecture: {source: architecture/a.calm.json}
observation:
  projects:
    - {name: demo, root: ., include: ["services/**"], exclude: []}
`

var bindFiles = []string{"services/billing/src/Invoice.ts", "services/fulfillment/src/Ship.ts", "services/other/x.ts"}

func bindNode(id string, extra map[string]any) map[string]any {
	node := map[string]any{"unique-id": id, "node-type": "service", "name": id, "description": id}
	for k, v := range extra {
		node[k] = v
	}

	return node
}

func bindArch(t *testing.T, extra map[string]any) string {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"$schema":       "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":         []any{bindNode("billing-service", extra), bindNode("fulfillment-service", nil)},
		"relationships": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func bindingsYAML(match string) string {
	return fmt.Sprintf(`
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: b1
    architectureRef: calm:node:billing-service
    match: {any: [%s]}
  - id: b2
    architectureRef: calm:node:fulfillment-service
    match: {any: [{sourceRoot: "services/fulfillment/**"}]}
`, match)
}

// bindSetup writes a workspace and gives its directory and the Deps with the fake graph.
func bindSetup(t *testing.T, files map[string]string) (string, cli.Deps) {
	t.Helper()

	dir := t.TempDir()
	tree := map[string]string{"concordia.yaml": bindWorkspaceYAML, "architecture/a.calm.json": bindArch(t, nil)}

	for k, v := range files {
		tree[k] = v
	}

	testutil.WriteTree(t, dir, tree)

	nodes := make([]testutil.FakeNode, len(bindFiles))
	for i, f := range bindFiles {
		nodes[i] = testutil.FileNode(strconv.Itoa(i), f)
	}

	graph := testutil.NewFakeGraph("demo", nodes, nil)
	client := observation.NewFakeClient(graph.Handler, "")

	return dir, cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return client },
		NewGit:    func(string) workspace.GitPort { return &workspace.FakeGit{Rev: "abc123", Files: bindFiles} },
	}
}

func TestBindValidatePassesAndStrictShowUnboundReportsTheUnboundFile(t *testing.T) {
	t.Parallel()

	dir, deps := bindSetup(t, map[string]string{"bindings/concordia.bindings.yaml": bindingsYAML(`{sourceRoot: "services/billing/**"}`)})

	if res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "validate"); res.Code != 0 {
		t.Fatalf("plain: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "validate", "--strict", "--show-unbound")
	if res.Code != 2 {
		t.Errorf("strict: exit code = %d, want 2", res.Code)
	}

	for _, want := range []string{"CO-BND-005", "services/other/x.ts"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, res.Stderr)
		}
	}
}

func TestBindValidateJSONHasTheSummaryKeys(t *testing.T) {
	t.Parallel()

	dir, deps := bindSetup(t, map[string]string{"bindings/concordia.bindings.yaml": bindingsYAML(`{sourceRoot: "services/billing/**"}`)})

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "validate", "--format", "json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"project", "graphGeneration", "bindings", "observedNodes", "boundNodes", "unboundFiles", "warnings"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing key %q in %v", key, doc)
		}
	}
}

func TestBindValidateBadBuildTargetGivesBND002(t *testing.T) {
	t.Parallel()

	dir, deps := bindSetup(t, map[string]string{
		"bindings/concordia.bindings.yaml": bindingsYAML(`{sourceRoot: "services/billing/**"}, {buildTarget: "a/b"}`),
	})

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "validate")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-BND-002") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestBindValidateRequiredBindingWithZeroMatchesGivesBND004(t *testing.T) {
	t.Parallel()

	dir, deps := bindSetup(t, map[string]string{"bindings/concordia.bindings.yaml": bindingsYAML(`{sourceRoot: "nothing/**"}`)})

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "validate")
	if !strings.Contains(res.Stderr, "CO-BND-004") || res.Code == 0 {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func loadBindingsOf(t *testing.T, files map[string]string, arch string) binding.Loaded {
	t.Helper()

	dir, _ := bindSetup(t, files)
	testutil.WriteTree(t, dir, map[string]string{"architecture/a.calm.json": arch})

	wsp, err := workspace.Load(filepath.Join(dir, "concordia.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}

	loaded := intent.LoadArchitecture(filepath.Join(dir, "architecture", "a.calm.json"), intent.LoadContext{Root: dir})
	if loaded.Model == nil {
		t.Fatalf("no model: %v", loaded.Diagnostics)
	}

	res, err := binding.Load(wsp, loaded.Model)
	if err != nil {
		t.Fatal(err)
	}

	return res
}

func bindDiagCodes(res *binding.Loaded) []string {
	codes := make([]string, len(res.Diagnostics))
	for i := range res.Diagnostics {
		codes[i] = res.Diagnostics[i].Code
	}

	slices.Sort(codes)

	return codes
}

func TestLoadBindingsPackageWithoutDoubleDotGivesBND002(t *testing.T) {
	t.Parallel()

	res := loadBindingsOf(t, map[string]string{"bindings/concordia.bindings.yaml": bindingsYAML("{package: com.acme.billing}")}, bindArch(t, nil))
	if got := bindDiagCodes(&res); !slices.Equal(got, []string{"CO-BND-002"}) {
		t.Errorf("codes = %v", got)
	}
}

func TestLoadBindingsUnknownCalmIDGivesBND001AndDuplicateIDGivesBND006(t *testing.T) {
	t.Parallel()

	yamlText := `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - {id: d, architectureRef: "calm:node:ghost", match: {any: [{sourceRoot: "a/**"}]}}
  - {id: d, architectureRef: "calm:node:billing-service", match: {any: [{sourceRoot: "b/**"}]}}
`

	res := loadBindingsOf(t, map[string]string{"bindings/concordia.bindings.yaml": yamlText}, bindArch(t, nil))
	if got := bindDiagCodes(&res); !slices.Equal(got, []string{"CO-BND-001", "CO-BND-006"}) {
		t.Errorf("codes = %v", got)
	}
}

func TestLoadBindingsConvertsCalmExtensionBindingsAndRejectsBadBuildTarget(t *testing.T) {
	t.Parallel()

	arch := bindArch(t, map[string]any{"concordia": map[string]any{"bindings": []any{
		map[string]any{"kind": "source-root", "value": "services/billing/**"},
		map[string]any{"kind": "build-target", "value": "x"},
	}}})

	res := loadBindingsOf(t, map[string]string{
		"bindings/concordia.bindings.yaml": "apiVersion: concordia.io/v1alpha1\nkind: BindingSet\nbindings: []\n",
	}, arch)

	ids := make([]string, len(res.Bindings))
	for i := range res.Bindings {
		ids[i] = res.Bindings[i].ID
	}

	if !slices.Contains(ids, "calm-ext:billing-service:0") {
		t.Errorf("binding IDs = %v", ids)
	}

	if got := bindDiagCodes(&res); !slices.Equal(got, []string{"CO-BND-002"}) {
		t.Errorf("codes = %v", got)
	}
}

func TestBindDiscoverWritesCandidatesToOutputAndRejectsUnknownStrategies(t *testing.T) {
	t.Parallel()

	dir, deps := bindSetup(t, nil)

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "discover", "--strategy", "path", "--output", "cand.yaml")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	text := readFile(t, dir, "cand.yaml")
	for _, want := range []string{"kind: BindingCandidates", "calm:node:billing-service", "services/billing/**", "reviewStatus: unreviewed"} {
		if !strings.Contains(text, want) {
			t.Errorf("candidates lack %q:\n%s", want, text)
		}
	}

	bogus := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "discover", "--strategy", "bogus", "--output", "cand.yaml")
	if bogus.Code != 2 || !strings.Contains(bogus.Stderr, "CO-WRK-010") {
		t.Errorf("bogus: exit code = %d, stderr = %q", bogus.Code, bogus.Stderr)
	}
}

func TestBindDiscoverWritesTheDefaultCandidateFile(t *testing.T) {
	t.Parallel()

	dir, deps := bindSetup(t, nil)

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "discover")
	if res.Code != 0 || !exists(dir, "bindings/concordia.bindings.candidates.yaml") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

const deployWorkspaceYAML = `
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata: {name: demo}
architecture: {source: architecture/a.calm.json}
observation:
  projects:
    - {name: demo, root: ., include: ["services/**", "k8s/**"], exclude: []}
environments:
  - id: production
    sources: ["k8s/prod/**"]
  - id: staging
    sources: ["k8s/staging/**"]
`

// deploySetup writes a workspace with two environments and a graph with a billing Deployment in each,
// the namespace `billing` in production, and the image of the service.
func deploySetup(t *testing.T, files map[string]string) (string, cli.Deps) {
	t.Helper()

	dir := t.TempDir()
	tree := map[string]string{
		"concordia.yaml":           deployWorkspaceYAML,
		"architecture/a.calm.json": bindArch(t, nil),
		"bindings/b.yaml":          bindingsYAML(`{sourceRoot: "services/billing/**"}`),
	}

	for k, v := range files {
		tree[k] = v
	}

	testutil.WriteTree(t, dir, tree)

	prod := testutil.ResourceNode("10", "k8s/prod/billing.yaml", "apps", "Deployment", "payments", "billing-api")
	staging := testutil.ResourceNode("11", "k8s/staging/billing.yaml", "apps", "Deployment", "payments", "billing-api")
	namespace := testutil.ResourceNode("12", "k8s/prod/namespace.yaml", "core", "Namespace", testutil.ClusterScoped, "billing")
	unrelated := testutil.ResourceNode("13", "k8s/prod/other.yaml", "apps", "Deployment", "payments", "ledger")
	image := testutil.ImageNode("14", "registry.example.com/shop/billing")

	nodes := make([]testutil.FakeNode, 0, 5+len(bindFiles))
	nodes = append(nodes, prod, staging, namespace, unrelated, image)

	for i, f := range bindFiles {
		nodes = append(nodes, testutil.FileNode(strconv.Itoa(i), f))
	}

	edges := make([]testutil.FakeEdge, 0, 3)
	edges = append(edges,
		testutil.InfraEdge(&prod, &image, "USES_IMAGE", testutil.Discriminators{}, map[string]string{"container": "api", "image": "registry.example.com/shop/billing:1.0"}),
		testutil.InfraEdge(&staging, &image, "USES_IMAGE", testutil.Discriminators{}, map[string]string{"container": "api", "image": "registry.example.com/shop/billing:1.0"}),
		testutil.InfraEdge(&prod, &namespace, "CONFIGURES", testutil.Discriminators{Via: "k8s"}, nil),
	)

	client := observation.NewFakeClient(testutil.NewFakeGraph("demo", nodes, edges).Handler, "")

	return dir, cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return client },
		NewGit:    func(string) workspace.GitPort { return &workspace.FakeGit{Rev: "abc123", Files: bindFiles} },
	}
}

func TestBindDiscoverDeploymentWritesProposedCandidatesOnly(t *testing.T) {
	t.Parallel()

	dir, deps := deploySetup(t, nil)
	before := readFile(t, dir, "bindings/b.yaml")

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "discover", "--strategy", "deployment")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	text := readFile(t, dir, "bindings/concordia.bindings.candidates.yaml")

	for _, want := range []string{
		"calm:node:billing-service", "status: proposed", "kind: workload", "environment: production", "environment: staging",
		"resourceKind: Deployment", "namespace: payments", "name: billing-api",
		"kind: infraResource", "address: core/Namespace/_/billing",
		"kind: image", "repository: registry.example.com/shop/billing",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("candidates lack %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "name: ledger") || strings.Contains(text, "status: active") {
		t.Errorf("unrelated or active candidate:\n%s", text)
	}

	if after := readFile(t, dir, "bindings/b.yaml"); after != before {
		t.Errorf("a binding file changed:\n%s", after)
	}
}

func TestBindDiscoverDeploymentCandidatesDoNotChangeValidateAndProposedBindingsAreIgnored(t *testing.T) {
	t.Parallel()

	proposed := `
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: p1
    architectureRef: calm:node:billing-service
    status: proposed
    required: true
    match: {any: [{kind: workload, environment: production, apiGroup: apps, resourceKind: Deployment, namespace: payments, name: billing-api}]}
`

	dir, deps := deploySetup(t, map[string]string{"bindings/proposed.yaml": proposed})

	if res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "discover", "--strategy", "deployment"); res.Code != 0 {
		t.Fatalf("discover: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	// A required binding that is proposed is not an error even though it is not used.
	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "bind", "validate")
	if res.Code != 0 {
		t.Fatalf("validate: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
