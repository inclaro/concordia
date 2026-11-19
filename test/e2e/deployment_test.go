package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The deployment trace against the real CBM: `check` and `trace` on a git copy of test/fixtures/infra with the deployment
// workspace of test/integration/deployment_workspace_test.go (two environments, a CALM model with a `deployed-in`
// relationship, bindings, and the `infrastructure` pack). The goldens are in test/golden/deployment/<scenario>/.
// A second run of `check` and of each `trace` must give the same bytes.

const (
	infraFixtureDir = "../fixtures/infra"
	infraProjectOld = "concordia-fixture-infra"
	resultsRel      = "build/concordia/results.json"

	deploymentWorkspaceHead = `apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: deployment
architecture:
  source: architecture/infra.calm.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: ` + infraProjectOld + `
      root: .
      include: ["**"]
  indexMode: fast
bindings:
  files: [bindings/concordia.bindings.yaml]
policies:
  files: []
  packs:
    - use: builtin:infrastructure
`

	deploymentCheck = `check:
  minimumCoverage: 0
  unknown: fail
  failOn: error
`

	deploymentMandatory = `  enforcement:
    - {family: deployment-trace, environment: production, mode: mandatory}
`

	deploymentEnvironments = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**"]
  - id: staging
    sources: ["k8s/staging/**"]
`

	deploymentOverridesEnvironments = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**", "k8s/overrides/**"]
  - id: staging
    sources: ["k8s/staging/**"]
`

	deploymentChartEnvironments = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**", "charts/**"]
  - id: staging
    sources: ["k8s/staging/**"]
`

	deploymentNodes = `    {"unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "worker-service", "node-type": "service", "name": "Worker service", "description": "The payments worker.", "concordia": {"tags": ["deployable"]}},
`

	deploymentClusterNode = `    {"unique-id": "prod-cluster", "node-type": "system", "name": "Production cluster", "description": "The production hosting target."}`

	deploymentBindingsHead = "apiVersion: concordia.io/v1alpha1\nkind: BindingSet\nbindings:\n"

	deploymentAPIImageBinding = `  - id: bind-api-image
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: image, repository: registry.example.com/payments/api}
  - id: bind-api-source
    architectureRef: calm:node:api-service
    match:
      any:
        - sourceRoot: app/**
`

	deploymentChartNode = `    {"unique-id": "chart-service", "node-type": "service", "name": "Chart service", "description": "The Helm chart API.", "concordia": {"tags": ["deployable"]}},
`

	deploymentReportsNode = `    {"unique-id": "reports-service", "node-type": "service", "name": "Reports service", "description": "The reports service.", "concordia": {"tags": ["deployable"]}},
`
)

var runID = regexp.MustCompile(`\d{8}T\d{6}Z-[0-9a-f]{8}`)

// deploymentArchitectureOf is the CALM model with the nodes, the hosting target nodes, and a deployed-in relationship
// for the services.
func deploymentArchitectureOf(extraNodes string, services []string, extraClusters ...string) string {
	quoted := make([]string, len(services))
	for i, s := range services {
		quoted[i] = `"` + s + `"`
	}

	var clusters strings.Builder

	clusters.WriteString(deploymentClusterNode)

	for _, c := range extraClusters {
		clusters.WriteString(",\n    {\"unique-id\": \"" + c + "\", \"node-type\": \"system\", \"name\": \"" + c + "\", \"description\": \"An extra hosting target.\"}")
	}

	return `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
` + deploymentNodes + extraNodes + clusters.String() + `
  ],
  "relationships": [
    {"unique-id": "services-in-prod", "relationship-type": {"deployed-in": {"container": "prod-cluster", "nodes": [` + strings.Join(quoted, ", ") + `]}}}
  ]
}
`
}

// deploymentBindingTarget names a binding entry: its id, the model node it binds, and the environment it matches in.
type deploymentBindingTarget struct {
	id   string
	node string
	env  string
}

func deploymentWorkload(target deploymentBindingTarget, name, namespace string) string {
	return "  - id: " + target.id + "\n    architectureRef: calm:node:" + target.node + "\n    match:\n      any:\n        - {kind: workload, environment: " + target.env +
		", resourceKind: Deployment, name: " + name + ", namespace: " + namespace + "}\n"
}

func deploymentNamespace(target deploymentBindingTarget, namespace string) string {
	return "  - id: " + target.id + "\n    architectureRef: calm:node:" + target.node + "\n    match:\n      any:\n        - {kind: infraResource, environment: " + target.env +
		", address: core/Namespace/_/" + namespace + "}\n"
}

// deploymentDefaultBindings is the binding list of the integration tests.
func deploymentDefaultBindings() string {
	return deploymentBindingsHead +
		"  - id: bind-api-workload\n    architectureRef: calm:node:api-service\n    match:\n      any:\n" +
		"        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: payments}\n" +
		"        - {kind: workload, environment: staging, resourceKind: Deployment, name: api, namespace: payments-staging}\n" +
		deploymentAPIImageBinding +
		deploymentWorkload(deploymentBindingTarget{"bind-worker-workload", "worker-service", "staging"}, "worker", "payments") +
		deploymentNamespace(deploymentBindingTarget{"bind-payments-namespace", "prod-cluster", "production"}, "payments")
}

// deploymentCase is one scenario: the workspace files over the fixture and the services that `trace` shows.
type deploymentCase struct {
	name         string
	environments string
	architecture string
	bindings     string
	enforcement  string
	subjects     []string
	// checkCode is the exit code of `check`.
	checkCode int
}

func (c *deploymentCase) files(project string) map[string]string {
	head := strings.Replace(deploymentWorkspaceHead, "name: "+infraProjectOld, "name: "+project, 1)

	return map[string]string{
		"concordia.yaml":                   head + deploymentCheck + c.enforcement + c.environments,
		"architecture/infra.calm.json":     c.architecture,
		"bindings/concordia.bindings.yaml": c.bindings,
	}
}

// newDeploymentRepo copies the infra fixture with the files of the case over it and makes the first commit.
func newDeploymentRepo(t *testing.T, project, dirName string, files map[string]string) *repo {
	t.Helper()

	dir := filepath.Join(t.TempDir(), dirName)
	if err := os.CopyFS(dir, os.DirFS(infraFixtureDir)); err != nil {
		t.Fatalf("copy %s: %v", infraFixtureDir, err)
	}

	r := &repo{t: t, dir: dir, project: project}

	for rel, text := range files {
		r.write(rel, text)
	}

	if _, err := os.Stat(r.path(".gitignore")); err != nil {
		r.write(".gitignore", defaultIgnore)
	}

	r.git("init", "-q", "-b", "main")
	r.commitAll("Initial infra fixture")
	t.Cleanup(r.dispose)

	return r
}

// keepStdout records the stdout of the latest step as a golden file.
func (s *scenario) keepStdout(name string) {
	s.t.Helper()

	s.files[s.stepKey(name)] = s.last.stdout
}

func deploymentCases() []deploymentCase {
	apiAndWorker := []string{"api-service", "worker-service"}

	return []deploymentCase{
		// A10: api-service with all five steps in both environments (the image is shared by production and staging);
		// A11: worker-service has a workload in staging only (wrong environment for the placement in prod-cluster).
		{
			name: "trace", environments: deploymentEnvironments, architecture: deploymentArchitectureOf("", apiAndWorker),
			bindings: deploymentDefaultBindings(), subjects: apiAndWorker,
		},
		// A11 reverse constellation: the workload of the container side is bound to prod-cluster and the namespace to api-service.
		{
			name: "reverse", environments: deploymentEnvironments, architecture: deploymentArchitectureOf("", apiAndWorker),
			bindings: deploymentBindingsHead + deploymentWorkload(deploymentBindingTarget{"bind-reports-workload", "prod-cluster", "production"}, "reports", "payments") +
				deploymentNamespace(deploymentBindingTarget{"bind-ns-prod", "api-service", "production"}, "payments"),
			subjects: apiAndWorker,
		},
		// A11 wrong environment: api-service is placed in staging only, in a node that is not its model container.
		{
			name: "wrong-environment", environments: deploymentEnvironments, architecture: deploymentArchitectureOf("", apiAndWorker, "dev-cluster"),
			bindings: deploymentBindingsHead + deploymentWorkload(deploymentBindingTarget{"bind-api-staging", "api-service", "staging"}, "api", "payments-staging") +
				deploymentNamespace(deploymentBindingTarget{"bind-ns-staging", "dev-cluster", "staging"}, "payments-staging"),
			subjects: apiAndWorker,
		},
		// Namespace payments exists in both environments; only the staging one is bound.
		{
			name: "same-names", environments: deploymentEnvironments, architecture: deploymentArchitectureOf("", apiAndWorker),
			bindings: deploymentBindingsHead + deploymentWorkload(deploymentBindingTarget{"bind-api-workload", "api-service", "production"}, "api", "payments") +
				deploymentWorkload(deploymentBindingTarget{"bind-worker-staging", "worker-service", "staging"}, "worker", "payments") +
				deploymentNamespace(deploymentBindingTarget{"bind-ns-staging", "prod-cluster", "staging"}, "payments"),
			subjects: apiAndWorker,
		},
		// k8s/prod/reports.yaml and k8s/overrides/reports.yaml declare Deployment payments/reports in production.
		{
			name: "competing", environments: deploymentOverridesEnvironments, architecture: deploymentArchitectureOf(deploymentReportsNode, []string{"api-service", "worker-service", "reports-service"}),
			bindings: deploymentBindingsHead + deploymentWorkload(deploymentBindingTarget{"bind-reports-workload", "reports-service", "production"}, "reports", "payments") +
				deploymentNamespace(deploymentBindingTarget{"bind-ns-prod", "prod-cluster", "production"}, "payments"),
			subjects: []string{"reports-service"},
		},
		// The Helm chart of the fixture has a templated name and image: the extraction of production is partial.
		{
			name: "templated", environments: deploymentChartEnvironments,
			architecture: deploymentArchitectureOf(deploymentChartNode+deploymentReportsNode, []string{"api-service", "worker-service", "reports-service", "chart-service"}),
			bindings: deploymentDefaultBindings() + deploymentWorkload(deploymentBindingTarget{"bind-reports-workload", "reports-service", "production"}, "reports", "payments") +
				"  - id: bind-reports-image\n    architectureRef: calm:node:reports-service\n    match:\n      any:\n        - {kind: image, repository: registry.example.com/payments/reports}\n" +
				"  - id: bind-chart-workload\n    architectureRef: calm:node:chart-service\n    match:\n      any:\n        - {kind: workload, environment: production, resourceKind: Deployment, name: \"<.Release.Name>-api\"}\n",
			subjects: []string{"chart-service", "reports-service", "worker-service"},
		},
		// Unrendered: the same chart is in the sources but no binding names its workload, and the worker has none.
		{
			name: "unrendered", environments: deploymentChartEnvironments, architecture: deploymentArchitectureOf("", apiAndWorker),
			bindings: deploymentBindingsHead + deploymentWorkload(deploymentBindingTarget{"bind-api-workload", "api-service", "production"}, "api", "payments") +
				deploymentNamespace(deploymentBindingTarget{"bind-ns-prod", "prod-cluster", "production"}, "payments"),
			subjects: apiAndWorker,
		},
		// A mandatory entry for a family of advisory maturity is refused: exit 2.
		{
			name: "mandatory", environments: deploymentEnvironments, architecture: deploymentArchitectureOf("", apiAndWorker),
			bindings: deploymentDefaultBindings(), enforcement: deploymentMandatory, subjects: nil, checkCode: 2,
		},
	}
}

func TestGoldenDeployment(t *testing.T) {
	requireE2E(t)

	for _, c := range deploymentCases() {
		t.Run(c.name, func(t *testing.T) {
			project := "golden-deployment-" + c.name
			r := newDeploymentRepo(t, project, c.name, c.files(project))
			s := newScenario(t, c.name, r)
			s.root = deploymentRoot

			run := &deploymentRun{c: &c, r: r, s: s, stdout: map[string]string{}}
			run.execute(t)
		})
	}
}

// deploymentRun is one scenario while it runs: the repository, the golden recorder, and the first outputs.
type deploymentRun struct {
	c      *deploymentCase
	r      *repo
	s      *scenario
	stdout map[string]string
	// firstResults is the results.json of the first check.
	firstResults string
}

func (d *deploymentRun) execute(t *testing.T) {
	t.Helper()

	d.s.step(nil, "index", "index")

	d.s.step(nil, "check-json", "check", "--format", "json")

	if d.s.last.code != d.c.checkCode {
		t.Fatalf("check exit %d, want %d: %s", d.s.last.code, d.c.checkCode, d.s.last.stderr)
	}

	d.s.grab(resultsRel)

	if d.c.checkCode != 0 {
		if !strings.Contains(d.s.last.output(), "CO-POL-018") {
			t.Errorf("a mandatory entry is not refused with CO-POL-018: %s", d.s.last.output())
		}

		d.s.compare()

		return
	}

	d.firstResults = d.s.files[d.s.stepKey(strings.ReplaceAll(resultsRel, "/", "_"))]
	d.traceSubjects()
	d.s.step(nil, "trace-ghost", "trace", "ghost-service")
	d.requireSecondRunIdentical(t)
	d.s.compare()
}

// traceSubjects runs `trace` as text and as JSON for each subject and keeps the output.
func (d *deploymentRun) traceSubjects() {
	for _, subject := range d.c.subjects {
		d.s.step(nil, "trace-"+subject, "trace", subject)
		d.s.keepStdout("trace-" + subject + ".txt")
		d.stdout[subject] = d.s.last.stdout

		d.s.step(nil, "trace-"+subject+"-json", "--format", "json", "trace", subject)
		d.s.keepStdout("trace-" + subject + ".json")
		d.stdout[subject+"-json"] = d.s.last.stdout
	}
}

// requireSecondRunIdentical runs `check` and each `trace` again and expects the same bytes.
func (d *deploymentRun) requireSecondRunIdentical(t *testing.T) {
	t.Helper()

	env := []string{"CONCORDIA_NOW=" + goldenNow}

	if res := d.r.cli(env, "--format", "json", "check"); res.code != d.c.checkCode {
		t.Fatalf("second check exit %d: %s", res.code, res.stderr)
	}

	// The run id has a random suffix by design: everything else is byte-identical.
	if second := d.r.read(resultsRel); runID.ReplaceAllString(second, "RUN") != runID.ReplaceAllString(d.firstResults, "RUN") {
		t.Errorf("results.json differs between two runs of check: %s", firstDifference(d.firstResults, second))
	}

	for _, subject := range d.c.subjects {
		if res := d.r.cli(env, "trace", subject); res.stdout != d.stdout[subject] {
			t.Errorf("trace %s differs between two runs:\n%s\n---\n%s", subject, d.stdout[subject], res.stdout)
		}

		if res := d.r.cli(env, "--format", "json", "trace", subject); res.stdout != d.stdout[subject+"-json"] {
			t.Errorf("trace %s --format json differs between two runs", subject)
		}
	}
}

// firstDifference shows the text around the first byte that differs.
func firstDifference(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}

	from := max(i-80, 0)

	return fmt.Sprintf("byte %d: %q != %q", i, a[from:min(i+80, len(a))], b[from:min(i+80, len(b))])
}
