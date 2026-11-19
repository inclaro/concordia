package integration_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	deptrace "github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

// These tests check the `deploymentTrace` section of `results.json` on the infra recording: the A10 chain of the
// production `api` workload, the areas object, the language rule (DEP-02), the A06 deployment action, and the
// redaction of manifest values.

const noEnvironmentBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-api-source
    architectureRef: calm:node:api-service
    match:
      any:
        - sourceRoot: app/**
`

func subjectTrace(t *testing.T, d *deployment, id, env string) *deptrace.SubjectTrace {
	t.Helper()

	sub := d.run.DeploymentTrace.Subject(id, env)
	if sub == nil {
		t.Fatalf("no trace of %s in %s", id, env)
	}

	return sub
}

func stepNamed(t *testing.T, sub *deptrace.SubjectTrace, step deptrace.Step) *deptrace.StepTrace {
	t.Helper()

	for i := range sub.Steps {
		if sub.Steps[i].Step == step {
			return &sub.Steps[i]
		}
	}

	t.Fatalf("no step %s", step)

	return nil
}

func entitiesNamed(step *deptrace.StepTrace, kind string) []deptrace.Entity {
	return slices.DeleteFunc(slices.Clone(step.Entities), func(e deptrace.Entity) bool { return e.Kind != kind })
}

func environmentTraceNamed(trace *deptrace.Trace, name string) *deptrace.EnvironmentTrace {
	var found *deptrace.EnvironmentTrace

	for i := range trace.Environments {
		if trace.Environments[i].Name == name {
			found = &trace.Environments[i]
		}
	}

	return found
}

func isProductionAPIWorkload(w *deptrace.Entity) bool {
	if w.Identity != "k8s:production/prod-eu/apps/Deployment/payments/api" {
		return false
	}

	if w.Locations[0] != (deptrace.Location{Path: "k8s/prod/deployment.yaml", Line: 1}) {
		return false
	}

	return slices.Equal(w.Bindings, []string{"bind-api-workload"})
}

func isCandidateDockerfile(e *deptrace.Entity) bool {
	if e.Locations[0] != (deptrace.Location{Path: "docker/Dockerfile", Line: 1}) || e.Lineage != deptrace.LineageCandidate {
		return false
	}

	return slices.Contains(e.Locations, deptrace.Location{Path: "scripts/build.sh", Line: 4})
}

func hasAppContainerMergeGap(image *deptrace.StepTrace) bool {
	merged := slices.IndexFunc(image.Gaps, func(g deptrace.Gap) bool { return slices.Contains(g.LossModes, observation.LossMergedOccurrences) })
	if merged < 0 {
		return false
	}

	return strings.Contains(image.Gaps[merged].Detail, "container app")
}

func hasSourceEvidenceEntities(source *deptrace.StepTrace) bool {
	paths := entitiesNamed(source, deptrace.KindSourcePath)
	if len(paths) != 1 || paths[0].Identity != "app/**" {
		return false
	}

	return len(entitiesNamed(source, deptrace.KindManifest)) == 1 && len(entitiesNamed(source, deptrace.KindDockerfile)) == 1
}

// The five steps show the manifest, the Dockerfile, and the source path with file and line.
func checkTraceSteps(t *testing.T, sub *deptrace.SubjectTrace) {
	t.Helper()

	workload := entitiesNamed(stepNamed(t, sub, deptrace.StepWorkload), deptrace.KindWorkload)
	if len(workload) != 1 || !isProductionAPIWorkload(&workload[0]) {
		t.Errorf("workload = %+v", workload)
	}

	build := entitiesNamed(stepNamed(t, sub, deptrace.StepBuildDefinition), deptrace.KindDockerfile)
	if len(build) != 1 || !isCandidateDockerfile(&build[0]) {
		t.Errorf("build = %+v", build)
	}

	source := stepNamed(t, sub, deptrace.StepSourceEvidence)
	if !hasSourceEvidenceEntities(source) {
		t.Errorf("source step = %+v", source.Entities)
	}

	// The provider keeps one occurrence for the two containers: the `app` container is a named gap (DEP-05), and the
	// trace is not complete.
	image := stepNamed(t, sub, deptrace.StepContainerImage)
	if !hasAppContainerMergeGap(image) || sub.Status == deptrace.StatusComplete {
		t.Errorf("image step = %+v, status = %s", image, sub.Status)
	}
}

func TestDeploymentTraceA10ChainOnTheInfraRecording(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})
	sub := subjectTrace(t, d, "api-service", "production")

	if sub.Level != deptrace.LevelDeclared || len(sub.Steps) != 5 {
		t.Fatalf("subject = %+v", sub)
	}

	checkTraceSteps(t, sub)

	// The staging trace is separate.
	staging := subjectTrace(t, d, "api-service", "staging")
	workloads := entitiesNamed(stepNamed(t, staging, deptrace.StepWorkload), deptrace.KindWorkload)

	if len(workloads) != 1 || workloads[0].Locations[0].Path != "k8s/staging/deployment.yaml" {
		t.Errorf("staging workloads = %+v", workloads)
	}

	// The rule results of the pack are listed next to the status.
	if len(sub.Rules) != 6 {
		t.Errorf("rules = %+v", sub.Rules)
	}
}

func isUnboundReportsWorkload(w *deptrace.UnboundWorkload) bool {
	loc := w.Workload.Locations[0]

	return loc.Path == "k8s/prod/reports.yaml" && loc.Line > 0
}

func isCompleteProductionEU(production *deptrace.EnvironmentTrace) bool {
	if production == nil {
		return false
	}

	return production.Cluster == "prod-eu" && production.Coverage.Status == observation.CoverageComplete
}

func TestDeploymentTraceUnboundAndUnresolvedWorkloadsStayVisible(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})
	trace := &d.run.DeploymentTrace
	production := environmentTraceNamed(trace, "production")

	if !isCompleteProductionEU(production) {
		t.Fatalf("production = %+v", production)
	}

	if !slices.ContainsFunc(production.UnboundWorkloads, func(w deptrace.UnboundWorkload) bool { return isUnboundReportsWorkload(&w) }) {
		t.Errorf("unbound = %+v", production.UnboundWorkloads)
	}

	// The kustomize base and the override file are in no environment source.
	var paths []string

	for _, workload := range trace.UnresolvedEnvironmentWorkloads {
		for _, loc := range workload.Locations {
			paths = append(paths, loc.Path)
		}
	}

	if !slices.Contains(paths, "k8s/overrides/reports.yaml") || !slices.Contains(paths, "kustomize/base/deployment.yaml") {
		t.Errorf("unresolved workloads = %v", paths)
	}
}

// The script scripts/deploy.sh applies a file and installs a chart. Both show as actions with their target, and no
// action is a placement. The script is in no environment source, so the chart action is not in an environment.
func checkTraceDeploymentActions(t *testing.T, d *deployment, production *deptrace.EnvironmentTrace) {
	t.Helper()

	targets := map[string]string{}

	actions := slices.Concat(production.Actions, d.run.DeploymentTrace.UnresolvedEnvironmentActions)
	for i := range actions {
		action := &actions[i]
		if action.Kind != deptrace.KindDeploymentAction || action.Locations[0].Path != "scripts/deploy.sh" {
			t.Errorf("action = %+v", *action)
		}

		targets[action.Properties["tool"]] = action.Properties["targetKind"] + " " + action.Properties["target"]
	}

	if targets["kubectl"] != "file k8s/prod/deployment.yaml" || targets["helm"] != "chart charts/api" {
		t.Errorf("actions = %v", targets)
	}
}

// The worker has no workload in production: INFRA-PLC-001 is not a pass, and the actions give it no placement.
func checkTraceWorkerHasNoPlacement(t *testing.T, d *deployment) {
	t.Helper()

	worker := subjectTrace(t, d, "worker-service", "production")
	step := stepNamed(t, worker, deptrace.StepWorkload)

	if len(step.Entities) != 0 || worker.Status != deptrace.StatusUnresolved {
		t.Errorf("worker step = %+v status = %s", step, worker.Status)
	}

	if ob := d.obligationOf(t, "INFRA-PLC-001", "worker-service", "production"); ob.Verdict == conformance.VerdictPass {
		t.Errorf("INFRA-PLC-001 = %s", ob.Verdict)
	}
}

func TestDeploymentTraceA06DeploysFactShowsAsAnAction(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	production := environmentTraceNamed(&d.run.DeploymentTrace, "production")
	if production == nil {
		t.Fatal("no production trace")
	}

	checkTraceDeploymentActions(t, d, production)
	checkTraceWorkerHasNoPlacement(t, d)

	// The action is on the workload step of the service that owns the manifest it names.
	api := stepNamed(t, subjectTrace(t, d, "api-service", "production"), deptrace.StepWorkload)
	if actions := entitiesNamed(api, deptrace.KindDeploymentAction); len(actions) != 1 || actions[0].Properties["target"] != "k8s/prod/deployment.yaml" {
		t.Errorf("api actions = %+v", actions)
	}
}

func requireTraceAreaCounts(t *testing.T, trace *deptrace.Trace, area conformance.AreaResult) {
	t.Helper()

	by := trace.Summary.ByStatus
	want := map[string]int{
		"subjects": trace.Summary.Subjects, "complete": by[deptrace.StatusComplete], "partial": by[deptrace.StatusPartial],
		"unresolved": by[deptrace.StatusUnresolved], "unknown": by[deptrace.StatusUnknown],
	}

	for key, count := range want {
		if area.Counts[key] != count {
			t.Errorf("area count %s = %d, section = %d", key, area.Counts[key], count)
		}
	}
}

func isFourSubjectsInTwoEnvironments(summary *deptrace.Summary) bool {
	return summary.Subjects == 4 && summary.Environments == 2
}

func TestDeploymentTraceAreaMatchesTheSection(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})
	trace := &d.run.DeploymentTrace
	area := d.run.Areas.DeploymentTrace

	if !isFourSubjectsInTwoEnvironments(&trace.Summary) || !area.Verdict.Configured() {
		t.Fatalf("summary = %+v, area = %+v", trace.Summary, area)
	}

	requireTraceAreaCounts(t, trace, area)

	// An unresolved subject (the worker has no workload in production) gives the worst verdict of the area.
	by := trace.Summary.ByStatus
	if by[deptrace.StatusUnresolved] == 0 || string(area.Verdict) != string(conformance.VerdictViolation) {
		t.Errorf("area verdict = %s, by status = %v", area.Verdict, by)
	}

	// The area joins the summary verdict and never changes the exit code (advisory rules gate nothing).
	if d.run.Summary.Verdict == conformance.VerdictPass || d.run.Summary.ExitCode != 0 {
		t.Errorf("summary verdict = %s, exit code = %d", d.run.Summary.Verdict, d.run.Summary.ExitCode)
	}
}

func TestDeploymentTraceNoEnvironmentDeclared(t *testing.T) {
	t.Parallel()

	head := deploymentWorkspaceHead + deploymentCheck
	d := deploymentRun(t, &deploymentSpec{
		files:    map[string]string{"concordia.yaml": head},
		bindings: noEnvironmentBindings,
	})

	trace := &d.run.DeploymentTrace
	if !isEmptyNotConfiguredTrace(trace) {
		t.Errorf("trace = %+v", trace)
	}

	if d.run.Areas.DeploymentTrace.Verdict != conformance.AreaNotConfigured || d.run.Areas.DeploymentTrace.Verdict == "pass" {
		t.Errorf("area = %+v", d.run.Areas.DeploymentTrace)
	}
}

func isEmptyNotConfiguredTrace(trace *deptrace.Trace) bool {
	if trace.Status != deptrace.StatusNotConfigured || len(trace.Environments) != 0 {
		return false
	}

	return trace.Summary.Subjects == 0 && len(trace.Levels) == 4
}

func isOnlyProductionTrace(trace *deptrace.Trace) bool {
	if len(trace.Environments) != 1 || trace.Environments[0].Name != "production" {
		return false
	}

	return trace.Subject("api-service", "staging") == nil
}

func TestDeploymentTraceOnlySelectedEnvironments(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{edit: func(opts *conformance.CheckOptions) { opts.Environments = []string{"production"} }})

	trace := &d.run.DeploymentTrace
	if !isOnlyProductionTrace(trace) {
		t.Errorf("environments = %+v", trace.Environments)
	}
}

func TestDeploymentTraceSectionShapeAndRepeatedRun(t *testing.T) {
	t.Parallel()

	first := deploymentRun(t, &deploymentSpec{})
	second := deploymentRun(t, &deploymentSpec{})

	text, err := conformance.CanonicalJSON(first.run)
	if err != nil {
		t.Fatal(err)
	}

	if errs := conformance.ValidateResultJSON(text); len(errs) > 0 {
		t.Fatalf("results do not match the schema: %+v", errs)
	}

	var decoded map[string]json.RawMessage
	if err = json.Unmarshal(text, &decoded); err != nil || decoded["deploymentTrace"] == nil {
		t.Fatalf("no deploymentTrace section: %v", err)
	}

	var version string

	_ = json.Unmarshal(decoded["schemaVersion"], &version)

	if version != "1.1" {
		t.Errorf("schemaVersion = %s", version)
	}

	requireSameTraceSection(t, first, second)
}

func requireSameTraceSection(t *testing.T, first, second *deployment) {
	t.Helper()

	one, err := canonjson.Marshal(first.run.DeploymentTrace)
	if err != nil {
		t.Fatal(err)
	}

	two, err := canonjson.Marshal(second.run.DeploymentTrace)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(one, two) {
		t.Error("the deploymentTrace section differs between two runs of the same inputs")
	}

	if first.run.Summary.ResultDigest != second.run.Summary.ResultDigest {
		t.Errorf("result digests differ: %s %s", first.run.Summary.ResultDigest, second.run.Summary.ResultDigest)
	}
}

// renderedTrace renders the trace of a subject in every environment with the renderer of `trace`.
func renderedTrace(t *testing.T, d *deployment, subject string) string {
	t.Helper()

	var out strings.Builder

	if found, err := deptrace.Render(&out, &d.run.DeploymentTrace, subject, deptrace.RenderOptions{}); err != nil || !found {
		t.Fatalf("render: found = %v err = %v", found, err)
	}

	return out.String()
}

func TestDeploymentTraceMakesNoRuntimeClaim(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	section, err := json.Marshal(d.run.DeploymentTrace)
	if err != nil {
		t.Fatal(err)
	}

	for name, text := range map[string]string{
		"deploymentTrace section": string(section),
		"trace output":            renderedTrace(t, d, "api-service"),
	} {
		lower := strings.ToLower(text)

		for _, word := range []string{"running", "deployed", "succeeded"} {
			if strings.Contains(lower, word) {
				t.Errorf("%s says %q", name, word)
			}
		}
	}

	// The level shows in the section and in the text.
	if !strings.Contains(renderedTrace(t, d, "api-service"), "Evidence: declared configuration only. Not traced: deployment execution and runtime.") {
		t.Error("the trace output does not state its level")
	}
}

func TestDeploymentTraceKeepsManifestValuesOut(t *testing.T) {
	t.Parallel()

	seeds := readInfraSeedEntries(t)
	d := deploymentRun(t, &deploymentSpec{})

	results, err := conformance.CanonicalJSON(d.run)
	if err != nil {
		t.Fatal(err)
	}

	texts := map[string][]byte{
		"results.json": results,
		"trace output": []byte(renderedTrace(t, d, "api-service") + renderedTrace(t, d, "worker-service")),
	}

	if !slices.ContainsFunc(seeds, func(s infraSeedEntry) bool { return s.Value == "CONCORDIA-SEED-env-value-5d1e" }) {
		t.Fatal("the seed of DB_PASSWORD is not in the seeds file")
	}

	for _, seed := range seeds {
		for name, text := range texts {
			if bytes.Contains(text, []byte(seed.Value)) {
				t.Errorf("%s contains the seed %s", name, seed.Class)
			}
		}
	}

	// The manifest still holds the value, so the check above can fail.
	if !bytes.Contains(mustRead(t, "../fixtures/infra/k8s/prod/deployment.yaml"), []byte("CONCORDIA-SEED-env-value-5d1e")) {
		t.Fatal("the fixture manifest lost its seeded value")
	}
}
