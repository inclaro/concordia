package deployment_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

func TestLineageSingleCandidate(t *testing.T) {
	t.Parallel()

	_, in := a10()
	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	build := stepOf(t, sub, deployment.StepBuildDefinition)

	dockerfiles := entitiesOf(build, deployment.KindDockerfile)
	lineageRequireCount(t, "dockerfiles", dockerfiles, 1)
	lineageWant(t, "dockerfile lineage", dockerfiles[0].Lineage, deployment.LineageCandidate)
	lineageWant(t, "build resolution", build.Resolution, observation.ResolutionInferred)
	lineageWant(t, "build gaps", len(build.Gaps), 0)
	lineageWant(t, "role", build.Role, observation.RoleInferredAssociation)

	if !lineageHasLimit(build.Limits, deployment.LimitImageProvenance) {
		t.Errorf("limits = %+v, want the image provenance limit", build.Limits)
	}
}

func TestLineageTwoCandidateDockerfiles(t *testing.T) {
	t.Parallel()

	w, in := a10()
	second := w.dockerfile("df2", "other/Dockerfile")
	w.builtFrom("img", second, 9)
	in.Evidence.Edges = w.edges

	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	build := stepOf(t, sub, deployment.StepBuildDefinition)

	dockerfiles := entitiesOf(build, deployment.KindDockerfile)
	lineageRequireCount(t, "dockerfiles", dockerfiles, 2)
	lineageWant(t, "build resolution", build.Resolution, observation.ResolutionAmbiguous)
	lineageRequireCount(t, "ambiguous build gaps", gapsWith(build.Gaps, deployment.ReasonAmbiguousBuildDefinition), 1)

	for _, entity := range dockerfiles {
		lineageWant(t, "candidate resolution", entity.Resolution, observation.ResolutionAmbiguous)
		lineageWant(t, "candidate lineage", entity.Lineage, deployment.LineageCandidate)
	}

	lineageWant(t, "status", sub.Status, deployment.StatusPartial)
}

func TestLineageMatchesTheRepositoryAndIgnoresTheTag(t *testing.T) {
	t.Parallel()

	w, in := a10()
	// The workload uses another tag of the repository, and a second image repository has no link.
	lineageDropUsesImage(w)
	w.uses("w", "img", "app", ordersRepository+":9.9")
	other := w.image("img2", "registry.example/shop/other")
	w.uses("w", other, "other", "registry.example/shop/other:1")
	in.Evidence.Edges = w.edges

	build := lineageTraceStep(t, in, deployment.StepBuildDefinition)

	lineageWant(t, "dockerfiles", len(entitiesOf(build, deployment.KindDockerfile)), 1)
	lineageWant(t, "unlinked images", len(entitiesOf(build, deployment.KindUnlinkedImage)), 1)
	lineageWant(t, "build gaps", len(build.Gaps), 1)
}

func TestLineageMutableTag(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{ordersRepository + ":latest", ordersRepository, ordersRepository + ":1.4"} {
		w, in := a10()
		lineageDropUsesImage(w)
		w.uses("w", "img", "app", ref)
		in.Evidence.Edges = w.edges

		sub := deployment.BuildTrace(in).Subject("orders-service", "production")
		image := stepOf(t, sub, deployment.StepContainerImage)

		entities := entitiesOf(image, deployment.KindContainerImage)
		lineageRequireCount(t, ref+": images", entities, 1)
		lineageWant(t, ref+": reference", entities[0].Properties["reference"], ref)
		lineageWant(t, ref+": mutable tag gaps", len(gapsWith(image.Gaps, deployment.ReasonMutableTag)), 1)
		lineageWant(t, ref+": status", sub.Status, deployment.StatusPartial)
	}
}

func TestLineageDigestPinnedReferenceHasNoMutableGap(t *testing.T) {
	t.Parallel()

	_, in := a10()
	image := lineageTraceStep(t, in, deployment.StepContainerImage)

	entities := entitiesOf(image, deployment.KindContainerImage)
	lineageRequireCount(t, "images", entities, 1)

	if entities[0].Properties["digest"] == "" {
		t.Errorf("image = %+v, want a digest", entities[0])
	}

	lineageWant(t, "mutable tag gaps", len(gapsWith(image.Gaps, deployment.ReasonMutableTag)), 0)
}

func TestLineageUnresolvedBaseImageIsAGap(t *testing.T) {
	t.Parallel()

	w, in := a10()
	w.baseImage("df", "1", "${BASE}", 7)
	in.Evidence.Edges = w.edges

	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	build := stepOf(t, sub, deployment.StepBuildDefinition)

	gaps := gapsWith(build.Gaps, deployment.ReasonCoverageIncomplete)
	lineageRequireCount(t, "build gaps", gaps, 1)
	lineageWantList(t, "loss modes", gaps[0].LossModes, []string{observation.LossUnrenderedTemplate})
	lineageWant(t, "status", sub.Status, deployment.StatusPartial)

	for _, base := range entitiesOf(build, deployment.KindBaseImage) {
		if base.Properties["stage"] == "1" {
			lineageWant(t, "unresolved base resolution", base.Resolution, observation.ResolutionUnresolved)
		}
	}
}

func TestOccurrenceMergedOccurrencesGap(t *testing.T) {
	t.Parallel()

	in := infraInput(t)
	merged := record(observation.FamilyContainerOccurrenceVersions, "production", observation.CoveragePartial)
	merged.LossModes = []observation.LossMode{{ID: observation.LossMergedOccurrences, Gaps: []observation.CoverageGap{
		{Reason: observation.LossMergedOccurrences, Path: prodDeployment, Workload: "Deployment/api", Container: "app", Line: 1},
	}}}
	in.Coverage = slices.Concat(complete("staging"), []observation.PredicateCoverage{
		record(observation.FamilyDeploymentWorkloadSet, "production", observation.CoverageComplete), merged,
	})

	sub := deployment.BuildTrace(in).Subject("api-service", "production")
	image := stepOf(t, sub, deployment.StepContainerImage)

	gaps := gapsWith(image.Gaps, deployment.ReasonCoverageIncomplete)
	lineageRequireCount(t, "image gaps", gaps, 1)
	lineageWantList(t, "loss modes", gaps[0].LossModes, []string{observation.LossMergedOccurrences})

	if !strings.Contains(gaps[0].Detail, "container app") || strings.Contains(gaps[0].Detail, "no image,") {
		t.Errorf("detail = %q", gaps[0].Detail)
	}

	// The surviving occurrence shows, with its tag, and the trace is not complete.
	entities := entitiesOf(image, deployment.KindContainerImage)
	lineageRequireCount(t, "images", entities, 1)
	lineageWant(t, "container", entities[0].Properties["container"], "sidecar")
	lineageWant(t, "tag", entities[0].Properties["tag"], "1.3.0")

	if sub.Status == deployment.StatusComplete {
		t.Errorf("status = %s, want an incomplete trace", sub.Status)
	}
}

func TestOccurrenceDifferentRepositoriesHaveNoMergedGap(t *testing.T) {
	t.Parallel()

	w, workload := lineageOrdersWorld()
	w.uses(workload, w.image("i1", ordersRepository), "app", pinned)
	w.uses(workload, w.image("i2", "registry.example/shop/audit"), "audit", "registry.example/shop/audit@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	image := lineageTraceStep(t, lineageOrdersInput(w), deployment.StepContainerImage)

	lineageWant(t, "images", len(entitiesOf(image, deployment.KindContainerImage)), 2)
	lineageWant(t, "image gaps", len(image.Gaps), 0)
}

func TestOccurrenceTemplatedImageIsAGapNotAGuess(t *testing.T) {
	t.Parallel()

	w, workload := lineageOrdersWorld()
	w.uses(workload, w.image("i1", ordersRepository), "app", "{{ .Values.image }}")

	image := lineageTraceStep(t, lineageOrdersInput(w), deployment.StepContainerImage)

	lineageWant(t, "entities", len(image.Entities), 0)
	lineageWant(t, "incomplete coverage gaps", len(gapsWith(image.Gaps, deployment.ReasonCoverageIncomplete)), 1)
}

func TestSharedImageShowsItsConsumers(t *testing.T) {
	t.Parallel()

	in := lineageSharedImageInput(t)
	trace := deployment.BuildTrace(in)

	for _, env := range []string{"production", "staging"} {
		sub := subjectOf(t, trace, "api-service", env)
		image := stepOf(t, sub, deployment.StepContainerImage)

		entities := entitiesOf(image, deployment.KindContainerImage)
		lineageRequireCount(t, env+": images", entities, 1)
		lineageWant(t, env+": consumers", entities[0].Consumers, 2)

		if !lineageHasLimit(image.Limits, deployment.LimitOtherConsumers) {
			t.Errorf("%s: limits = %+v", env, image.Limits)
		}
	}
}

func TestTwoWorkloadsOfOneSubjectSharingAnImageHaveNoOtherConsumers(t *testing.T) {
	t.Parallel()

	w := newWorld()
	image := w.image("i1", ordersRepository)
	first := w.workload("w1", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	second := w.workload("w2", "production", "k8s/prod/orders-worker.yaml", "apps/Deployment/shop/orders-worker")

	w.bind(first, "orders-service", "b1")
	w.bind(second, "orders-service", "b2")
	w.uses(first, image, "app", pinned)
	w.uses(second, image, "app", pinned)

	step := lineageTraceStep(t, lineageOrdersInput(w), deployment.StepContainerImage)
	for _, entity := range entitiesOf(step, deployment.KindContainerImage) {
		lineageWant(t, "consumers", entity.Consumers, 2)
	}

	if lineageHasLimit(step.Limits, deployment.LimitOtherConsumers) {
		t.Errorf("limits = %+v: both consumers belong to the trace", step.Limits)
	}
}

func TestActionKubectlTargetsAFile(t *testing.T) {
	t.Parallel()

	trace := deployment.BuildTrace(lineageSharedImageInput(t))

	step := stepOf(t, subjectOf(t, trace, "api-service", "production"), deployment.StepWorkload)
	actions := entitiesOf(step, deployment.KindDeploymentAction)

	lineageRequireCount(t, "actions", actions, 1)
	lineageWant(t, "tool", actions[0].Properties["tool"], "kubectl")
	lineageWant(t, "target", actions[0].Properties["target"], prodDeployment)
	lineageWant(t, "target kind", actions[0].Properties["targetKind"], "file")
	lineageWant(t, "location", actions[0].Locations[0], deployment.Location{Path: "scripts/deploy.sh", Line: 6})

	// The action is not a placement: the placement entity is the hosting target.
	for _, entity := range step.Entities {
		if entity.Kind == deployment.KindHostingTarget {
			lineageWant(t, "tool of a placement", entity.Properties["tool"], "")
		}
	}

	// It does not name the staging workload.
	staging := stepOf(t, subjectOf(t, trace, "api-service", "staging"), deployment.StepWorkload)
	lineageWant(t, "actions of production shown for staging", len(entitiesOf(staging, deployment.KindDeploymentAction)), 0)
}

// lineageHelmAction gives the last helm action of an environment, or nil.
func lineageHelmAction(env *deployment.EnvironmentTrace) *deployment.Entity {
	for i := len(env.Actions) - 1; i >= 0; i-- {
		if env.Actions[i].Properties["tool"] == "helm" {
			return &env.Actions[i]
		}
	}

	return nil
}

func TestActionHelmTargetsAChart(t *testing.T) {
	t.Parallel()

	production := deployment.BuildTrace(lineageSharedImageInput(t)).Environments[0]

	chart := lineageHelmAction(&production)
	if chart == nil {
		t.Fatalf("actions = %+v", production.Actions)
	}

	lineageWant(t, "target", chart.Properties["target"], "charts/api")
	lineageWant(t, "target kind", chart.Properties["targetKind"], "chart")
	lineageWant(t, "line", chart.Locations[0].Line, 7)

	// A chart action names no workload of a service: it shows in the environment only.
	for i := range production.Subjects {
		sub := &production.Subjects[i]
		step := stepOf(t, sub, deployment.StepWorkload)

		for _, action := range entitiesOf(step, deployment.KindDeploymentAction) {
			if action.Properties["tool"] == "helm" {
				t.Errorf("the chart action is shown for %s", sub.ArchitectureID)
			}
		}
	}
}
