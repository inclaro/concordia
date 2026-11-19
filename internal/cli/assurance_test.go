package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance/renderer"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// assuranceNow is the CONCORDIA_NOW of newChkSetup.
var assuranceNow = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// assuranceTree makes a workspace with the check fixtures plus an `assurance` block, and the files.
func assuranceTree(t *testing.T, block string, files map[string]string) *chkSetup {
	t.Helper()

	tree := map[string]string{
		"concordia.yaml": chkWorkspaceText("environments:\n  - {id: staging, sources: [deploy/**]}\n" + block),
	}

	for name, text := range files {
		tree[name] = text
	}

	return newChkSetup(t, tree, nil)
}

// fixtureText reads a file of test/fixtures/assurance.
func fixtureText(t *testing.T, rel string) string {
	t.Helper()

	return testutil.ReadFile(t, filepath.Join("..", "..", "test", "fixtures", "assurance", filepath.FromSlash(rel)))
}

// checkResultStatement is a bare check-result statement written `age` before CONCORDIA_NOW.
func checkResultStatement(t *testing.T, check string, age time.Duration) string {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": statement.PredicateCheckResult,
		"subject": []map[string]any{{"name": "subject", "digest": map[string]string{"sha256": strings.Repeat("ab", 32)}}},
		"predicate": map[string]any{
			"check": check, "outcome": "passed", "revision": "abc123", "environment": "staging",
			"evaluator": map[string]string{"name": "go-test", "version": "1.0.0"},
			"time":      assuranceNow.Add(-age).Format(time.RFC3339), "covers": []string{"shop"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// treeFiles lists every file below dir (slash paths), to see that a command writes none.
func treeFiles(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(dir, path)
		out = append(out, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(out)

	return out
}

const validateBlock = `assurance:
  rendering:
    sources:
      - {environment: staging, tool: plain, path: deploy/plain}
  plans:
    - {environment: staging, tool: terraform, file: build/plans/create.json}
  verification:
    results: ["build/results/*.json"]
    maxAgeDays: 14
`

// A stale statement exits 0 and prints `stale`; the plan without a statement is shown as not authenticated; the
// command writes no file. The terminal output is the golden test/golden/assurance/validate-terminal.txt.
func TestAssuranceValidatePrintsEveryInputAndWritesNothing(t *testing.T) {
	t.Parallel()

	setup := assuranceTree(t, validateBlock, map[string]string{
		"deploy/plain/deployment.yaml": fixtureText(t, "plain/workloads/deployment.yaml"),
		"build/plans/create.json":      fixtureText(t, "plans/terraform-1.2-create.json"),
		"build/results/fresh.json":     checkResultStatement(t, "unit-tests", time.Hour),
		"build/results/stale.json":     checkResultStatement(t, "lint", 100*24*time.Hour),
	})
	before := treeFiles(t, setup.dir)

	res := setup.run(t, "assurance", "validate")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "check-result build/results/stale.json\n  integrity: content not verified\n  admissible: no\n  reasons: ") ||
		!strings.Contains(res.Stdout, "stale") {
		t.Errorf("stdout lacks the stale statement:\n%s", res.Stdout)
	}

	if after := treeFiles(t, setup.dir); !slices.Equal(before, after) {
		t.Errorf("files changed: before %v, after %v", before, after)
	}

	golden, err := os.ReadFile(filepath.Join("..", "..", "test", "golden", "assurance", "validate-terminal.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if res.Stdout != string(golden) {
		t.Errorf("stdout:\n%s\nwant:\n%s", res.Stdout, golden)
	}
}

// assuranceValidateDoc is the JSON document of `assurance validate`, as far as the tests read it.
type assuranceValidateDoc struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Inputs        []assuranceValidateInput  `json:"inputs"`
	Environments  []assuranceValidateEnvDoc `json:"environments"`
}

type assuranceValidateInput struct {
	Kind       string   `json:"kind"`
	Path       string   `json:"path"`
	Integrity  string   `json:"integrity"`
	Reasons    []string `json:"reasons"`
	Admissible bool     `json:"admissible"`
}

type assuranceValidateEnvDoc struct {
	Environment string `json:"environment"`
	Coverage    string `json:"coverage"`
	Resources   int    `json:"resources"`
}

// isStaleCheckResult reports whether an input is a check-result that is inadmissible because it is stale.
func (in *assuranceValidateInput) isStaleCheckResult() bool {
	return in.Kind == "check-result" && !in.Admissible && slices.Contains(in.Reasons, "stale")
}

// describesStaging reports whether the document is the one of the single staging environment of the fixture.
func (doc *assuranceValidateDoc) describesStaging() bool {
	if doc.SchemaVersion != "1.0" || len(doc.Environments) != 1 {
		return false
	}

	return doc.Environments[0].Environment == "staging" && doc.Environments[0].Resources != 0
}

func TestAssuranceValidateJSON(t *testing.T) {
	t.Parallel()

	setup := assuranceTree(t, validateBlock, map[string]string{
		"deploy/plain/deployment.yaml": fixtureText(t, "plain/workloads/deployment.yaml"),
		"build/plans/create.json":      fixtureText(t, "plans/terraform-1.2-create.json"),
		"build/results/stale.json":     checkResultStatement(t, "lint", 100*24*time.Hour),
	})

	res := setup.run(t, "assurance", "validate", "--json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var doc assuranceValidateDoc
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, res.Stdout)
	}

	kinds := make([]string, len(doc.Inputs))
	for i, in := range doc.Inputs {
		kinds[i] = in.Kind

		if in.Kind == "check-result" && !in.isStaleCheckResult() {
			t.Errorf("stale statement = %+v", in)
		}
	}

	if want := []string{"rendering", "plan", "check-result"}; !slices.Equal(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}

	if !doc.describesStaging() {
		t.Errorf("document = %+v", doc)
	}
}

func TestAssuranceValidateWithoutAnAssuranceKey(t *testing.T) {
	t.Parallel()

	setup := newChkSetup(t, nil, nil)

	res := setup.run(t, "assurance", "validate")
	if res.Code != 0 || res.Stdout != "no assurance inputs declared\n" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestAssuranceValidateExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		files  map[string]string
		env    map[string]string
		name   string
		block  string
		stderr string
		code   int
	}{
		{
			name: "unknown property in the declaration", block: "assurance:\n  surprise: true\n",
			code: 2, stderr: "CO-PRV-001",
		},
		{
			name: "path outside the root", block: "assurance:\n  plans:\n    - {environment: staging, tool: terraform, file: ../plan.json}\n",
			code: 2, stderr: "CO-PRV-002",
		},
		{
			name: "scenario glob that matches nothing", block: "assurance:\n  quality:\n    scenarios: [\"quality/*.yaml\"]\n",
			code: 2, stderr: "CO-QUA-001",
		},
		{
			name: "renderer missing", block: "assurance:\n  rendering:\n    sources:\n      - {environment: staging, tool: kustomize, path: deploy/overlay}\n",
			files: map[string]string{"deploy/overlay/kustomization.yaml": "resources: []\n"},
			env:   map[string]string{"KUSTOMIZE": "/nonexistent/kustomize"}, code: 5, stderr: "CO-PRV-003",
		},
		{
			name: "malformed statement is a warning", block: "assurance:\n  verification:\n    results: [\"build/results/*.json\"]\n",
			files: map[string]string{"build/results/bad.json": "{not json"}, code: 0, stderr: "CO-PRV-009",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := assuranceTree(t, tc.block, tc.files)
			for k, v := range tc.env {
				setup.cfg.Env[k] = v
			}

			res := setup.run(t, "assurance", "validate")
			if res.Code != tc.code || !strings.Contains(res.Stderr, tc.stderr) {
				t.Fatalf("exit code = %d, want %d; stderr = %q", res.Code, tc.code, res.Stderr)
			}
		})
	}
}

// fakeHelm writes a helm that answers `version` and runs body for any other call.
func fakeHelm(t *testing.T, body string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake renderer is a shell script")
	}

	path := filepath.Join(t.TempDir(), "helm")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo v4.3.1; exit 0; fi\n" + body + "\n"

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// pinHelm writes the lockfile with the pin of the helm that the environment names.
func pinHelm(t *testing.T, dir string, env map[string]string) {
	t.Helper()

	found, err := renderer.Probe(t.Context(), workspace.RenderToolHelm, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	if err := workspace.WriteLock(filepath.Join(dir, workspace.LockFileName), workspace.NewLock("0.12.1", "", found.Pin)); err != nil {
		t.Fatal(err)
	}
}

const helmBlock = `assurance:
  rendering:
    sources:
      - {environment: staging, tool: helm, path: deploy/chart, release: shop, namespace: shop}
`

const (
	// interruptAfter is when the interrupt tests cancel the run: after the workspace is loaded and the renderer runs.
	interruptAfter = 2 * time.Second
	// renderLimit is far below the sleep of the fake renderer, so a run that waited for it fails.
	renderLimit = 30 * time.Second

	// randomManifest is the output of a renderer like a chart that calls randAlphaNum: two renders differ. /dev/urandom
	// is a device that the sandbox of the render binds.
	randomManifest = `printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: random-%s\n  namespace: default\n' "$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')"`
)

func TestAssuranceValidateRendersTwiceWithAFakeRenderer(t *testing.T) {
	t.Parallel()

	setup := assuranceTree(t, helmBlock, map[string]string{"deploy/chart/Chart.yaml": fixtureText(t, "helm-random/chart/Chart.yaml")})
	setup.cfg.Env["HELM"] = fakeHelm(t, randomManifest)
	pinHelm(t, setup.dir, setup.cfg.Env)

	res := setup.run(t, "assurance", "validate")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !strings.Contains(res.Stderr, "CO-PRV-014") || !strings.Contains(res.Stderr, "staging") {
		t.Errorf("no non-determinism warning, stderr = %q", res.Stderr)
	}

	if !strings.Contains(res.Stdout, "renderers\n  helm v4.3.1\n") {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestAssuranceValidateDeterministicFakeRendererGivesNoWarning(t *testing.T) {
	t.Parallel()

	setup := assuranceTree(t, helmBlock, map[string]string{"deploy/chart/Chart.yaml": fixtureText(t, "helm-random/chart/Chart.yaml")})
	setup.cfg.Env["HELM"] = fakeHelm(t, `printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: fixed\n  namespace: default\n'`)
	pinHelm(t, setup.dir, setup.cfg.Env)

	res := setup.run(t, "assurance", "validate")
	if res.Code != 0 || strings.Contains(res.Stderr, "CO-PRV-014") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// The chart of test/fixtures/assurance/helm-random calls randAlphaNum: with the real helm, the two renders differ.
func TestAssuranceValidateRendersTwiceWithTheRealHelm(t *testing.T) {
	t.Parallel()

	helm := realHelm(t)
	if helm == "" {
		t.Skip("helm is not installed")
	}

	setup := assuranceTree(t, helmBlock, map[string]string{
		"deploy/chart/Chart.yaml":            fixtureText(t, "helm-random/chart/Chart.yaml"),
		"deploy/chart/templates/secret.yaml": fixtureText(t, "helm-random/chart/templates/secret.yaml"),
	})
	setup.cfg.Env["HELM"] = helm
	pinHelm(t, setup.dir, setup.cfg.Env)

	res := setup.run(t, "assurance", "validate")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !strings.Contains(res.Stderr, "CO-PRV-014") {
		t.Errorf("no non-determinism warning, stderr = %q", res.Stderr)
	}
}

// realHelm finds the helm of the repository (`.bin/helm`) or on PATH. "" when there is none.
func realHelm(t *testing.T) string {
	t.Helper()

	if path, err := exec.LookPath("helm"); err == nil {
		return path
	}

	bin, err := filepath.Abs(filepath.Join("..", "..", ".bin", "helm"))
	if err != nil {
		return ""
	}

	if _, err := os.Stat(bin); err != nil {
		return ""
	}

	return bin
}

// A renderer that is still running when the run is interrupted gives exit 130 and no output, for `check` and for
// `assurance validate`: a cut render never gives complete coverage.
func TestInterruptedRenderGivesExit130(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"check"}, {"assurance", "validate"}} {
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()

			setup := assuranceTree(t, helmBlock, map[string]string{"deploy/chart/Chart.yaml": fixtureText(t, "helm-random/chart/Chart.yaml")})
			// `exec sleep` makes the renderer one process that the interrupt kills; the sandbox of the render cannot
			// write outside its directories, so the test cannot ask the renderer to say that it started.
			setup.cfg.Env["HELM"] = fakeHelm(t, "exec sleep 60")
			pinHelm(t, setup.dir, setup.cfg.Env)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			setup.cfg.Ctx = ctx

			time.AfterFunc(interruptAfter, cancel)

			began := time.Now()

			res := setup.run(t, args...)
			if res.Code != 130 {
				t.Fatalf("exit code = %d, want 130; stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
			}

			if took := time.Since(began); took > renderLimit {
				t.Errorf("the run took %s: the interrupt did not stop the renderer", took)
			}

			if res.Stdout != "" {
				t.Errorf("stdout = %q, want no output", res.Stdout)
			}

			if exists(setup.dir, "build/concordia/results.json") {
				t.Error("a run that was interrupted wrote results.json")
			}
		})
	}
}

// The bundle of a run with assurance evidence holds the render, the statements, and the contracts, and the manifest
// lists them with their redaction records.
func TestExportBundleHoldsTheAssuranceFiles(t *testing.T) {
	t.Parallel()

	setup := assuranceTree(t, validateBlock, map[string]string{
		"deploy/plain/deployment.yaml": fixtureText(t, "plain/workloads/deployment.yaml"),
		"build/plans/create.json":      fixtureText(t, "plans/terraform-1.2-create.json"),
		"build/results/stale.json":     checkResultStatement(t, "lint", 100*24*time.Hour),
	})

	res := setup.run(t, "export", "bundle")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	manifest, err := bundle.Open(filepath.Join(setup.dir, "build", "concordia", "conformance.tar.gz"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	have := map[string]bool{}
	for _, f := range manifest.Files {
		have[f.Path] = true
	}

	for _, want := range []string{"assurance/render/staging.json", "assurance/plans/staging.json", "assurance/contracts.json"} {
		if !have[want] {
			t.Errorf("bundle lacks %s: %v", want, have)
		}
	}

	if !slices.ContainsFunc(manifest.Files, func(f bundle.ManifestFile) bool { return strings.HasPrefix(f.Path, "assurance/statements/") }) {
		t.Errorf("bundle lacks a statement file: %v", have)
	}
}

const checkRule = `
  - id: CO-CHK-001
    name: Unit tests have a passing result
    severity: warning
    require:
      checkResult:
        check: unit-tests
        environment: staging
        subject: {calmNodes: [billing-service]}
`

// A TESTS_FILE edge of the code graph is a candidate: `explain` of a check rule and `assurance validate` show it with
// its source, its target, and the suggested mapping, and say that it is not evidence. The rule stays `unknown`.
func TestTestAssociationsAreCandidatesInExplainAndValidate(t *testing.T) {
	t.Parallel()

	const testFile = "services/billing/test/Invoice.test.ts"

	graph := chkGraph()
	test := testutil.FileNode("9", testFile)
	billing := testutil.FileNode("1", chkBillingFile)

	graph.Nodes = append(graph.Nodes, test)
	graph.Edges = append(graph.Edges, testutil.FakeEdge{Type: "TESTS_FILE", From: &test, To: &billing})

	tree := map[string]string{
		"concordia.yaml":            chkWorkspaceText("environments:\n  - {id: staging, sources: [deploy/**]}\nassurance:\n  verification:\n    results: [\"build/results/*.json\"]\n"),
		"policies/demo.policy.yaml": chkPolicy + checkRule,
		"build/results/unit.json":   checkResultStatement(t, "unit-tests", time.Hour),
	}

	setup := newChkSetup(t, tree, graph)
	setup.cfg.Deps.NewGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{Rev: "abc123", Files: []string{chkBillingFile, chkStoreFile, chkLegacyFile, testFile}}
	}

	if res := setup.run(t, "check"); res.Code == 5 || res.Code == 7 {
		t.Fatalf("check: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	want := "tests-file " + testFile + " -> " + chkBillingFile + " (candidate, not evidence); suggested mapping to billing-service"

	explain := setup.run(t, "explain", "CO-CHK-001")
	if explain.Code != 0 || !strings.Contains(explain.Stdout, "Candidate test associations (not evidence, never a verdict):\n  "+want) {
		t.Fatalf("explain: exit code = %d, stdout = %q, stderr = %q", explain.Code, explain.Stdout, explain.Stderr)
	}

	if !strings.Contains(explain.Stdout, "Verdict: unknown") {
		t.Errorf("a candidate must not give a verdict:\n%s", explain.Stdout)
	}

	validate := setup.run(t, "assurance", "validate")
	// The candidates come from the last `check`, which may be older than the tree: the output names that file.
	if validate.Code != 0 || !strings.Contains(validate.Stdout, "\nfrom the last check run (build/concordia/results.json):\nCandidate test associations (not evidence, never a verdict):\n  "+want+"\n") {
		t.Fatalf("validate: exit code = %d, stdout = %q, stderr = %q", validate.Code, validate.Stdout, validate.Stderr)
	}
}
