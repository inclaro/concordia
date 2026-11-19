package integration_test

import (
	"bytes"
	"cmp"
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// The workspace of the deployment tests (INFRA-PLC and INFRA-TRC rules of the `infrastructure` pack): the infra
// fixture with two declared environments, a CALM model with a `deployed-in` relationship, bindings, and the
// built-in pack. A test changes one part of it through deploymentSpec.

const (
	deploymentWorkspaceHead = `apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: deployment
architecture:
  source: architecture/infra.calm.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: concordia-fixture-infra
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

	// deploymentCheck is the default `check` section. The enforcement entries of a test are written after failOn.
	deploymentCheck = `check:
  minimumCoverage: 0
  unknown: fail
  failOn: error
`

	// deploymentEnvironments declares production (cluster prod-eu) and staging.
	deploymentEnvironments = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**"]
  - id: staging
    sources: ["k8s/staging/**"]
`

	// deploymentArchitecture has api-service and worker-service deployed in prod-cluster; both are `deployable`.
	deploymentArchitecture = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "worker-service", "node-type": "service", "name": "Worker service", "description": "The payments worker.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "prod-cluster", "node-type": "system", "name": "Production cluster", "description": "The production hosting target."}
  ],
  "relationships": [
    {"unique-id": "services-in-prod", "relationship-type": {"deployed-in": {"container": "prod-cluster", "nodes": ["api-service", "worker-service"]}}}
  ]
}
`

	// deploymentArchitectureWithoutPlacement is the same model with no deployed-in relationship.
	deploymentArchitectureWithoutPlacement = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "prod-cluster", "node-type": "system", "name": "Production cluster", "description": "The production hosting target."}
  ],
  "relationships": []
}
`

	// deploymentBindings binds the production workload api, its image, and the namespace payments of production to the
	// container, and the source root app/ to api-service. The staging worker is bound to worker-service.
	deploymentBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-api-workload
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: payments}
        - {kind: workload, environment: staging, resourceKind: Deployment, name: api, namespace: payments-staging}
  - id: bind-api-image
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: image, repository: registry.example.com/payments/api}
  - id: bind-api-source
    architectureRef: calm:node:api-service
    match:
      any:
        - sourceRoot: app/**
  - id: bind-worker-workload
    architectureRef: calm:node:worker-service
    match:
      any:
        - {kind: workload, environment: staging, resourceKind: Deployment, name: worker, namespace: payments}
  - id: bind-payments-namespace
    architectureRef: calm:node:prod-cluster
    match:
      any:
        - {kind: infraResource, environment: production, address: core/Namespace/_/payments}
`
)

// deploymentSpec is the workspace of one test. An empty field takes the default.
type deploymentSpec struct {
	// files replace or add files of the workspace (path relative to its root).
	files map[string]string
	// edit changes the options of the check.
	edit func(*conformance.CheckOptions)
	// environments replaces the declared environments.
	environments string
	// architecture replaces the CALM model.
	architecture string
	// bindings replaces the binding file text.
	bindings string
	// extraBindings is appended to the binding list (a list item per binding, indented by two).
	extraBindings string
	// check replaces the `check` section of the workspace text (deploymentCheck is the default).
	check string
	// recording is the recording file. The default is the infra recording.
	recording string
	// withoutPack leaves the `infrastructure` pack out of the workspace: the same run without the INFRA rules.
	withoutPack bool
	// noIndex skips the index result that `concordia index` writes first. Without it the coverage of
	// `default-excluded-directory`, `dockerfile-set`, and the other index-dependent gaps is unknown.
	noIndex bool
}

// deployment is the checked workspace of a deploymentSpec.
type deployment struct {
	w    *infraWorkspace
	run  *conformance.Run
	logs *bytes.Buffer
	rec  observation.Recording
}

// newDeploymentWorkspace copies the infra fixture and writes the workspace files of the spec on top of newInfraWorkspace.
// It runs no check.
func newDeploymentWorkspace(t *testing.T, spec *deploymentSpec) *infraWorkspace {
	t.Helper()

	head := deploymentWorkspaceHead
	if spec.withoutPack {
		head = strings.Replace(head, "  packs:\n    - use: builtin:infrastructure\n", "", 1)
	}

	files := map[string]string{
		"concordia.yaml":                   head + cmp.Or(spec.check, deploymentCheck) + cmp.Or(spec.environments, deploymentEnvironments),
		"architecture/infra.calm.json":     cmp.Or(spec.architecture, deploymentArchitecture),
		"bindings/concordia.bindings.yaml": cmp.Or(spec.bindings, deploymentBindings) + spec.extraBindings,
	}

	maps.Copy(files, spec.files)

	return newInfraWorkspace(t, cmp.Or(spec.recording, infraRecordingFile), files)
}

func (d *deployment) checkOptions(edit func(*conformance.CheckOptions)) *conformance.CheckOptions {
	opts := d.w.checkOptions(logger.New(logger.Options{Out: d.logs, RunID: "run", Verbose: true, Trace: true}))
	opts.SkipReports = true
	opts.KeepEvidence = true

	if edit != nil {
		edit(opts)
	}

	return opts
}

// deploymentRun checks the workspace of the spec. A failed check fails the test. Without noIndex the index result of
// the recording is written first, as `concordia index` does, and the check runs again.
func deploymentRun(t *testing.T, spec *deploymentSpec) *deployment {
	t.Helper()

	d := &deployment{w: newDeploymentWorkspace(t, spec), logs: &bytes.Buffer{}}
	d.rec = d.w.rec

	run, err := conformance.RunCheck(context.Background(), d.checkOptions(spec.edit))
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}

	if !spec.noIndex {
		snap := replayInfra(t, cmp.Or(spec.recording, infraRecordingFile))
		record := snap.index.Record(infraProject, run.Run.GraphGeneration, d.rec.HeadSHA)

		if err = conformance.WriteIndexRecord(d.w.dir, conformance.IndexRecordFile, &record); err != nil {
			t.Fatal(err)
		}

		if run, err = conformance.RunCheck(context.Background(), d.checkOptions(spec.edit)); err != nil {
			t.Fatalf("check failed: %v", err)
		}
	}

	d.run = run

	return d
}

// deploymentCheckError is the error of a check that stops before it gives a run.
func deploymentCheckError(t *testing.T, spec *deploymentSpec) error {
	t.Helper()

	w := newDeploymentWorkspace(t, spec)
	d := &deployment{w: w, logs: &bytes.Buffer{}, rec: w.rec}

	_, err := conformance.RunCheck(context.Background(), d.checkOptions(spec.edit))

	return err
}

// cli runs the CLI in the workspace copy. The CBM client replays the recording.
func (d *deployment) cli(t *testing.T, args ...string) cliResult {
	t.Helper()

	var stdout, stderr bytes.Buffer

	stdio := cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Env: map[string]string{"CONCORDIA_NOW": fixedNow}, Cwd: d.w.dir}
	deps := cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return observation.NewReplayClient(d.rec) },
		NewGit:    func(string) workspace.GitPort { return &workspace.FakeGit{Rev: d.rec.HeadSHA, Files: d.w.files} },
	}

	code := cli.Run(t.Context(), args, &stdio, deps, cli.BuildInfo{Version: "test"})

	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// obligationOf gives the obligation of the rule for the subject in the environment.
func (d *deployment) obligationOf(t *testing.T, rule, subject, env string) *conformance.ObligationResult {
	t.Helper()

	return obligationFor(t, ruleOf(t, d.run, rule), subject, env)
}
