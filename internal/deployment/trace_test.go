package deployment_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

const (
	ordersRepository = "registry.example/shop/orders"
	pinned           = ordersRepository + "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// complete gives the coverage records of an environment with every predicate of the trace complete.
func complete(env string) []observation.PredicateCoverage {
	return []observation.PredicateCoverage{
		record(observation.FamilyDeploymentWorkloadSet, env, observation.CoverageComplete),
		record(observation.FamilyContainerOccurrenceVersions, env, observation.CoverageComplete),
	}
}

// a10 is the chain of the spec: a service bound to a production workload with a digest-pinned image, a build
// command that links the image repository to a Dockerfile, and a bound source root.
func a10() (*world, *deployment.Input) {
	w := newWorld()
	workload := w.workload("w", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	w.bind(workload, "orders-service", "bind-orders-workload")

	image := w.image("img", ordersRepository)
	w.uses(workload, image, "app", pinned)

	dockerfile := w.dockerfile("df", "build/Dockerfile")
	w.builtFrom(image, dockerfile, 3)
	w.baseImage(dockerfile, "0", "golang:1.22", 1)

	in := w.input(envs("production"), "orders-service")
	in.Bindings = []binding.Binding{{
		ID: "bind-orders-source", ArchitectureRef: "orders-service", Status: binding.StatusActive,
		Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "src/orders/**"}},
	}}
	in.Coverage = complete("production")

	return w, in
}

func TestTraceA10CompleteChain(t *testing.T) {
	t.Parallel()

	_, in := a10()
	trace := deployment.BuildTrace(in)

	traceWantEqual(t, "configured", trace.Configured(), true)
	traceWantEqual(t, "environments", len(trace.Environments), 1)

	sub := subjectOf(t, trace, "orders-service", "production")
	traceWantEqual(t, "status", sub.Status, deployment.StatusComplete)
	traceWantEqual(t, "level", sub.Level, deployment.LevelDeclared)
	traceWantEqual(t, "gap count", len(sub.Gaps), 0)

	traceWantSlice(t, "steps", stepsOf(sub), []deployment.Step{
		deployment.StepService, deployment.StepWorkload, deployment.StepContainerImage, deployment.StepBuildDefinition, deployment.StepSourceEvidence,
	})

	wantA10Workload(t, sub)
	wantA10Image(t, sub)
	wantA10Build(t, sub)
	wantA10Source(t, sub)
}

func wantA10Workload(t *testing.T, sub *deployment.SubjectTrace) {
	t.Helper()

	manifest := entitiesOf(stepOf(t, sub, deployment.StepWorkload), deployment.KindWorkload)
	traceWantSlice(t, "workload step", firstLocationsOf(manifest), []deployment.Location{{Path: "k8s/prod/orders.yaml", Line: 1}})
}

func wantA10Image(t *testing.T, sub *deployment.SubjectTrace) {
	t.Helper()

	image := entitiesOf(stepOf(t, sub, deployment.StepContainerImage), deployment.KindContainerImage)
	traceWantSlice(t, "image digests set", hasPropertyOf(image, "digest"), []bool{true})
	traceWantSlice(t, "image tags", propertiesOf(image, "tag"), []string{""})
	traceWantSlice(t, "image repositories", propertiesOf(image, "repository"), []string{ordersRepository})
}

func wantA10Build(t *testing.T, sub *deployment.SubjectTrace) {
	t.Helper()

	build := entitiesOf(stepOf(t, sub, deployment.StepBuildDefinition), deployment.KindDockerfile)
	traceWantSlice(t, "build step", firstLocationsOf(build), []deployment.Location{{Path: "build/Dockerfile", Line: 1}})
	traceWantSlice(t, "build lineage", lineagesOf(build), []deployment.Lineage{deployment.LineageCandidate})

	// The step shows the build command too: the place that names the image.
	traceWantContains(t, "build locations", allLocationsOf(build), deployment.Location{Path: "Makefile", Line: 3})
}

// The source step shows the bound path, the manifest, and the Dockerfile.
func wantA10Source(t *testing.T, sub *deployment.SubjectTrace) {
	t.Helper()

	source := stepOf(t, sub, deployment.StepSourceEvidence)
	traceWantSlice(t, "source paths", identitiesOf(entitiesOf(source, deployment.KindSourcePath)), []string{"src/orders/**"})
	traceWantSlice(t, "source manifests", firstPathsOf(entitiesOf(source, deployment.KindManifest)), []string{"k8s/prod/orders.yaml"})
	traceWantSlice(t, "source Dockerfiles", firstPathsOf(entitiesOf(source, deployment.KindDockerfile)), []string{"build/Dockerfile"})
}

func TestTraceProvenanceLimitDoesNotChangeTheStatus(t *testing.T) {
	t.Parallel()

	_, in := a10()
	sub := deployment.BuildTrace(in).Subject("orders-service", "production")

	traceWantEqual(t, "status", sub.Status, deployment.StatusComplete)

	for _, tc := range []struct {
		name  string
		step  deployment.Step
		limit string
	}{
		{"build", deployment.StepBuildDefinition, deployment.LimitImageProvenance},
		{"source", deployment.StepSourceEvidence, deployment.LimitSourceProvenance},
		// The provider may merge occurrences: the limit always shows, even when every container has an occurrence.
		{"image", deployment.StepContainerImage, deployment.LimitMergedOccurrences},
	} {
		traceWantSlice(t, tc.name+" limits", limitIDsOf(stepOf(t, sub, tc.step).Limits), []string{tc.limit})
	}
}

func TestTraceA10MissingBuildDefinition(t *testing.T) {
	t.Parallel()

	w, in := a10()
	w.edges = slices.DeleteFunc(w.edges, func(e *observation.EvidenceEdge) bool { return e.Kind == observation.KindBuiltFrom })
	in.Evidence.Edges = w.edges

	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	build := stepOf(t, sub, deployment.StepBuildDefinition)

	gaps := gapsWith(build.Gaps, deployment.ReasonCoverageIncomplete)
	traceWantEqual(t, "build resolution", build.Resolution, observation.ResolutionUnresolved)
	traceWantEqual(t, "coverage gaps", len(gaps), 1)
	traceWantSlice(t, "loss modes", firstLossModes(gaps), []string{observation.LossBuildLinkNeedsBuildCommand})
	traceWantEqual(t, "status", sub.Status, deployment.StatusPartial)

	// The image repository has the lineage `unknown`, and the source step is still shown.
	traceWantSlice(t, "unlinked lineage", lineagesOf(entitiesOf(build, deployment.KindUnlinkedImage)), []deployment.Lineage{deployment.LineageUnknown})
	traceWantEqual(t, "source step has entities", len(stepOf(t, sub, deployment.StepSourceEvidence).Entities) > 0, true)

	// A Dockerfile set that is not complete adds its loss modes: the trace never says that no Dockerfile builds the image.
	partial := record(observation.FamilyDockerfileSet, "", observation.CoveragePartial, observation.LossDefaultExcludedDirectory)
	partial.Scope = observation.CoverageScope{Project: "p"}
	in.Coverage = append(in.Coverage, partial)

	build = stepOf(t, deployment.BuildTrace(in).Subject("orders-service", "production"), deployment.StepBuildDefinition)
	traceWantSlice(t, "loss modes", firstLossModes(build.Gaps), []string{observation.LossBuildLinkNeedsBuildCommand, observation.LossDefaultExcludedDirectory})
}

func TestTraceOneServiceInTwoEnvironments(t *testing.T) {
	t.Parallel()

	w := newWorld()
	prod := w.workload("p", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	stage := w.workload("s", "staging", "k8s/staging/orders.yaml", "apps/Deployment/shop/orders")
	w.bind(prod, "orders-service", "b")
	w.bind(stage, "orders-service", "b")

	image := w.image("img", ordersRepository)
	w.uses(prod, image, "app", ordersRepository+":1.0")
	w.uses(stage, image, "app", ordersRepository+":1.1")

	in := w.input(envs("production", "staging"), "orders-service")
	in.Coverage = slices.Concat(complete("production"), complete("staging"))
	trace := deployment.BuildTrace(in)

	production := subjectOf(t, trace, "orders-service", "production")
	staging := subjectOf(t, trace, "orders-service", "staging")

	for _, tc := range []struct {
		sub  *deployment.SubjectTrace
		file string
		tag  string
	}{{production, "k8s/prod/orders.yaml", "1.0"}, {staging, "k8s/staging/orders.yaml", "1.1"}} {
		workloads := entitiesOf(stepOf(t, tc.sub, deployment.StepWorkload), deployment.KindWorkload)
		images := entitiesOf(stepOf(t, tc.sub, deployment.StepContainerImage), deployment.KindContainerImage)

		traceWantSlice(t, tc.sub.Environment+" workload files", firstPathsOf(workloads), []string{tc.file})
		traceWantSlice(t, tc.sub.Environment+" image tags", propertiesOf(images, "tag"), []string{tc.tag})
	}
}

func TestTraceEnvironmentNotSelected(t *testing.T) {
	t.Parallel()

	_, in := a10()
	in.Declared = envs("production", "staging")
	in.Selected = []string{"production"}
	in.Coverage = slices.Concat(complete("production"), complete("staging"))

	trace := deployment.BuildTrace(in)
	names := traceProject(trace.Environments, func(e *deployment.EnvironmentTrace) string { return e.Name })

	traceWantSlice(t, "environments", names, []string{"production"})
	traceWantEqual(t, "summary environments", trace.Summary.Environments, 1)
	traceWantEqual(t, "a trace of the excluded environment", trace.Subject("orders-service", "staging") == nil, true)
}

func TestTraceRolesByStep(t *testing.T) {
	t.Parallel()

	_, in := a10()
	sub := deployment.BuildTrace(in).Subject("orders-service", "production")

	want := map[deployment.Step]observation.EvidenceRole{
		deployment.StepService:         observation.RoleDeclaredIntent,
		deployment.StepWorkload:        observation.RoleParsedSourceFact,
		deployment.StepContainerImage:  observation.RoleParsedSourceFact,
		deployment.StepBuildDefinition: observation.RoleInferredAssociation,
		deployment.StepSourceEvidence:  observation.RoleInferredAssociation,
	}

	for i := range sub.Steps {
		traceWantEqual(t, string(sub.Steps[i].Step)+" role", sub.Steps[i].Role, want[sub.Steps[i].Step])
	}

	// The binding that links the service to the workload shows `explicitly bound`, and the dimensions stay apart.
	workload := stepOf(t, sub, deployment.StepWorkload)
	traceWantEqual(t, "workload resolution", workload.Resolution, observation.ResolutionExplicitlyBound)
	first := firstEntityOf(workload)
	traceWantEqual(t, "workload entity resolution", first.Resolution, observation.ResolutionExplicitlyBound)
	traceWantSlice(t, "workload entity bindings", first.Bindings, []string{"bind-orders-workload"})

	traceWantEqual(t, "image resolution", stepOf(t, sub, deployment.StepContainerImage).Resolution, observation.ResolutionExact)
	traceWantEqual(t, "build resolution", stepOf(t, sub, deployment.StepBuildDefinition).Resolution, observation.ResolutionInferred)
}

func TestTraceServiceWithNoWorkload(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status observation.CoverageStatus
		want   deployment.Status
		reason deployment.Reason
		modes  []string
	}{
		{"complete coverage", observation.CoverageComplete, deployment.StatusUnresolved, deployment.ReasonAbsent, nil},
		{"partial coverage", observation.CoveragePartial, deployment.StatusUnknown, deployment.ReasonCoverageIncomplete, []string{observation.LossDefaultExcludedDirectory}},
		{"unknown coverage", observation.CoverageUnknown, deployment.StatusUnknown, deployment.ReasonCoverageIncomplete, nil},
		{"failed coverage", observation.CoverageFailed, deployment.StatusUnknown, deployment.ReasonCoverageIncomplete, nil},
		{"unsupported coverage", observation.CoverageUnsupported, deployment.StatusUnknown, deployment.ReasonUnsupportedFamily, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			in := w.input(envs("production"), "orders-service")
			in.Coverage = []observation.PredicateCoverage{record(observation.FamilyDeploymentWorkloadSet, "production", tc.status, tc.modes...)}

			sub := deployment.BuildTrace(in).Subject("orders-service", "production")
			gaps := stepOf(t, sub, deployment.StepWorkload).Gaps

			traceWantEqual(t, "status", sub.Status, tc.want)
			traceWantSlice(t, "gap reasons", reasonsOf(gaps), []deployment.Reason{tc.reason})
			traceWantSlice(t, "loss modes", firstLossModes(gaps), tc.modes)

			// The five steps are still there, with no entity after the service.
			traceWantEqual(t, "steps", len(sub.Steps), 5)
			traceWantEqual(t, "service entities", len(stepOf(t, sub, deployment.StepService).Entities), 1)
			traceWantEqual(t, "workload entities", len(stepOf(t, sub, deployment.StepWorkload).Entities), 0)
		})
	}
}

func TestTraceNoRecordIsUnknown(t *testing.T) {
	t.Parallel()

	w := newWorld()
	sub := deployment.BuildTrace(w.input(envs("production"), "orders-service")).Subject("orders-service", "production")

	traceWantEqual(t, "status", sub.Status, deployment.StatusUnknown)
}

func TestTraceCompetingDeclarationsGiveAnEvidenceConflict(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.workload("a", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	w.workload("b", "production", "k8s/prod/extra/orders.yaml", "apps/Deployment/shop/orders")
	w.bind("a", "orders-service", "bind")
	w.bind("b", "orders-service", "bind")

	in := w.input(envs("production"), "orders-service")
	in.Coverage = complete("production")

	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	step := stepOf(t, sub, deployment.StepWorkload)

	workloads := entitiesOf(step, deployment.KindWorkload)
	traceWantEqual(t, "workloads", len(workloads), 1)
	traceWantEqual(t, "workload locations", len(allLocationsOf(workloads)), 2)
	traceWantEqual(t, "evidence conflicts", len(gapsWith(step.Gaps, deployment.ReasonEvidenceConflict)), 1)
	traceWantEqual(t, "status", sub.Status, deployment.StatusPartial)
}

func TestTraceAmbiguousBindingIsAGap(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.workload("a", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	w.bind("a", "orders-service", "one")
	w.bind("a", "billing-service", "two")

	in := w.input(envs("production"), "orders-service", "billing-service")
	in.BindingSet.Ambiguous = map[string][]string{"a": {"one", "two"}}
	in.Coverage = complete("production")

	step := stepOf(t, deployment.BuildTrace(in).Subject("orders-service", "production"), deployment.StepWorkload)

	traceWantEqual(t, "ambiguous gaps", len(gapsWith(step.Gaps, deployment.ReasonAmbiguousBinding)), 1)
	traceWantEqual(t, "resolution", step.Resolution, observation.ResolutionAmbiguous)
}

func TestTraceWorkloadOfNoEnvironmentIsListedAndKeepsTheServiceUnknown(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.workload("a", observation.EnvironmentUnresolved, "k8s/other/orders.yaml", "apps/Deployment/shop/orders")
	w.bind("a", "orders-service", "bind")

	in := w.input(envs("production"), "orders-service")
	in.Coverage = complete("production")

	trace := deployment.BuildTrace(in)
	sub := subjectOf(t, trace, "orders-service", "production")

	// The workload of no environment is not in the trace of production, and it keeps the absence from being definite.
	step := stepOf(t, sub, deployment.StepWorkload)
	traceWantEqual(t, "status", sub.Status, deployment.StatusUnknown)
	traceWantEqual(t, "workload entities", len(step.Entities), 0)
	traceWantEqual(t, "environment-unresolved gaps", len(gapsWith(step.Gaps, deployment.ReasonEnvironmentUnresolved)), 1)

	unresolved := traceProject(trace.UnresolvedEnvironmentWorkloads, func(u *deployment.UnresolvedWorkload) string {
		return u.Kind + " " + firstPathOf(u.Locations)
	})
	traceWantSlice(t, "unresolved", unresolved, []string{"Deployment k8s/other/orders.yaml"})
}

func TestTraceUnboundWorkloadStaysVisibleWithItsImages(t *testing.T) {
	t.Parallel()

	w := newWorld()
	workload := w.workload("a", "production", "k8s/prod/reports.yaml", "apps/Deployment/shop/reports")
	image := w.image("img", ordersRepository)
	w.uses(workload, image, "app", ordersRepository+":1.0")
	w.builtFrom(image, w.dockerfile("df", "build/Dockerfile"), 3)

	in := w.input(envs("production"), "orders-service")
	in.Coverage = complete("production")
	environment := deployment.BuildTrace(in).Environments[0]

	if len(environment.UnboundWorkloads) != 1 {
		t.Fatalf("unbound = %+v", environment.UnboundWorkloads)
	}

	unbound := &environment.UnboundWorkloads[0]
	traceWantEqual(t, "unbound path", firstPathOf(unbound.Workload.Locations), "k8s/prod/reports.yaml")
	traceWantEqual(t, "unbound resolution", unbound.Workload.Resolution, observation.ResolutionUnbound)
	traceWantEqual(t, "unbound bindings", len(unbound.Workload.Bindings), 0)
	traceWantEqual(t, "unbound images", len(unbound.Images), 1)
	traceWantEqual(t, "unbound build definitions", len(unbound.BuildDefinitions), 1)

	// Nothing attaches it to the service by name.
	sub := &environment.Subjects[0]
	traceWantEqual(t, "subject is not complete", sub.Status != deployment.StatusComplete, true)
	traceWantEqual(t, "workloads of the subject", entityCountOf(t, sub, deployment.StepWorkload, deployment.KindWorkload), 0)
}

func TestTraceNotConfiguredWithoutAnEnvironment(t *testing.T) {
	t.Parallel()

	_, in := a10()
	in.Declared = nil

	trace := deployment.BuildTrace(in)

	traceWantEqual(t, "configured", trace.Configured(), false)
	traceWantEqual(t, "status", trace.Status, deployment.StatusNotConfigured)
	traceWantEqual(t, "environments", len(trace.Environments), 0)
	traceWantEqual(t, "subjects", trace.Summary.Subjects, 0)
	traceWantEqual(t, "levels", len(trace.Levels), 4)

	for _, status := range deployment.Statuses() {
		if _, ok := trace.Summary.ByStatus[status]; !ok {
			t.Errorf("summary lacks %s", status)
		}
	}
}

func TestTraceLevels(t *testing.T) {
	t.Parallel()

	for _, level := range deployment.Levels() {
		want := "not-available"
		if level.Level == deployment.LevelDeclared {
			want = "evaluated"
		}

		if level.Status != want {
			t.Errorf("%s = %s", level.Level, level.Status)
		}
	}
}

func TestTraceSummaryCountsEverySubject(t *testing.T) {
	t.Parallel()

	w := newWorld()
	ids := make([]string, 0, 10)

	for i := range 10 {
		id := fmt.Sprintf("svc-%d", i)
		ids = append(ids, id)

		if i < 7 {
			workload := w.workload("w"+id, "production", fmt.Sprintf("k8s/prod/%s.yaml", id), "apps/Deployment/shop/"+id)
			w.bind(workload, id, "bind-"+id)
			w.uses(workload, w.image("i"+id, "registry.example/shop/"+id), "app", "registry.example/shop/"+id+"@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		}
	}

	in := w.input(envs("production"), ids...)
	in.Coverage = []observation.PredicateCoverage{record(observation.FamilyDeploymentWorkloadSet, "production", observation.CoveragePartial, observation.LossParsePartial)}

	trace := deployment.BuildTrace(in)

	traceWantEqual(t, "subjects", trace.Summary.Subjects, 10)

	for status, want := range map[deployment.Status]int{
		deployment.StatusUnknown: 3, deployment.StatusPartial: 7, deployment.StatusComplete: 0, deployment.StatusUnresolved: 0,
	} {
		traceWantEqual(t, "subjects "+string(status), trace.Summary.ByStatus[status], want)
	}
}

func TestTraceListsTheRuleResultsOfItsSubjectAndEnvironment(t *testing.T) {
	t.Parallel()

	_, in := a10()
	in.Declared = envs("production", "staging")
	in.Rules = []deployment.RuleLink{
		{RuleID: "INFRA-TRC-002", ObligationID: "b", Verdict: "pass", Subject: "orders-service", Environment: "production"},
		{RuleID: "INFRA-TRC-001", ObligationID: "a", Verdict: "unknown", Subject: "orders-service", Environment: "production"},
		{RuleID: "INFRA-TRC-001", ObligationID: "c", Verdict: "pass", Subject: "orders-service", Environment: "staging"},
		{RuleID: "INFRA-TRC-001", ObligationID: "d", Verdict: "pass", Subject: "other-service", Environment: "production"},
	}

	trace := deployment.BuildTrace(in)
	sub := subjectOf(t, trace, "orders-service", "production")

	rules := traceProject(sub.Rules, func(r *deployment.RuleLink) string { return r.RuleID + "/" + r.ObligationID + "/" + r.Verdict })
	traceWantSlice(t, "rules", rules, []string{"INFRA-TRC-001/a/unknown", "INFRA-TRC-002/b/pass"})

	// The status comes from the evidence, never from a rule result.
	traceWantEqual(t, "status", sub.Status, deployment.StatusComplete)
}

func TestTraceSubjectsAreTheDeployableDeployedAndBoundNodes(t *testing.T) {
	t.Parallel()

	_, in := a10()
	in.Model.Nodes = append(in.Model.Nodes,
		nodeOf("plain-service"), nodeOf("placed-service"), nodeOf("bound-service"), nodeOf("source-only-service"), nodeOf("proposed-service"))
	in.Model.Relationships = append(in.Model.Relationships, deployedIn("placed-service"))
	in.Bindings = append(in.Bindings,
		binding.Binding{ID: "b1", ArchitectureRef: "bound-service", Status: binding.StatusActive, Match: []binding.Match{{Kind: binding.MatchImage, Repository: "x"}}},
		binding.Binding{ID: "b2", ArchitectureRef: "source-only-service", Status: binding.StatusActive, Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "src/**"}}},
		binding.Binding{ID: "b3", ArchitectureRef: "proposed-service", Status: binding.StatusProposed, Match: []binding.Match{{Kind: binding.MatchWorkload, Name: "x"}}},
		binding.Binding{ID: "b4", ArchitectureRef: "unknown-node", Status: binding.StatusActive, Match: []binding.Match{{Kind: binding.MatchWorkload, Name: "x"}}},
	)

	got := traceProject(deployment.BuildTrace(in).Environments[0].Subjects, func(s *deployment.SubjectTrace) string { return s.ArchitectureID })

	// A tagged node (orders-service), a node that a deployed-in relationship lists, and a node with an active
	// workload or image binding. A source binding alone, a proposed binding, and an unknown node do not make a subject.
	traceWantSlice(t, "subjects", got, []string{"bound-service", "orders-service", "placed-service"})
}

func TestTraceIsDeterministic(t *testing.T) {
	t.Parallel()

	var first []byte

	for range 5 {
		in := infraInput(t)
		in.Coverage = slices.Concat(complete("production"), complete("staging"))

		text, err := canonjson.Marshal(deployment.BuildTrace(in))
		if err != nil {
			t.Fatal(err)
		}

		if first == nil {
			first = text

			continue
		}

		if !bytes.Equal(text, first) {
			t.Fatal("the trace differs between two builds of the same input")
		}
	}
}

// stepEntities gives the entities of the steps.
func stepEntities(steps []deployment.StepTrace) []*deployment.Entity {
	var out []*deployment.Entity

	for i := range steps {
		for j := range steps[i].Entities {
			out = append(out, &steps[i].Entities[j])
		}
	}

	return out
}

// environmentEntities gives the entities of the steps of the subjects of an environment, then its unbound workloads.
func environmentEntities(env *deployment.EnvironmentTrace) []*deployment.Entity {
	out := make([]*deployment.Entity, 0, len(env.UnboundWorkloads))

	for i := range env.Subjects {
		out = append(out, stepEntities(env.Subjects[i].Steps)...)
	}

	for i := range env.UnboundWorkloads {
		out = append(out, &env.UnboundWorkloads[i].Workload)
	}

	return out
}

func wantAllowedProperties(t *testing.T, allowed []string, entity *deployment.Entity) {
	t.Helper()

	for key := range entity.Properties {
		if !slices.Contains(allowed, key) {
			t.Errorf("property %q of %s is not on the allow-list", key, entity.Identity)
		}
	}
}

func wantNoSecrets(t *testing.T, text string, secrets []string) {
	t.Helper()

	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Errorf("the trace holds %q", secret)
		}
	}
}

func TestTraceEntityPropertiesAreAnAllowList(t *testing.T) {
	t.Parallel()

	in := infraInput(t)
	in.Coverage = slices.Concat(complete("production"), complete("staging"))

	text, err := json.Marshal(deployment.BuildTrace(in))
	if err != nil {
		t.Fatal(err)
	}

	var decoded deployment.Trace
	if err = json.Unmarshal(text, &decoded); err != nil {
		t.Fatal(err)
	}

	allowed := deployment.AllowedProperties()

	for i := range decoded.Environments {
		for _, entity := range environmentEntities(&decoded.Environments[i]) {
			wantAllowedProperties(t, allowed, entity)
		}
	}

	// The seeded value of the manifest never reaches the trace.
	wantNoSecrets(t, string(text), []string{"CONCORDIA-SEED", "DB_PASSWORD", "API_TOKEN", "--sidecar"})
}

func TestTraceSaysNothingAboutRuntime(t *testing.T) {
	t.Parallel()

	in := infraInput(t)
	in.Coverage = slices.Concat(complete("production"), complete("staging"))

	text, err := json.Marshal(deployment.BuildTrace(in))
	if err != nil {
		t.Fatal(err)
	}

	for _, word := range []string{"running", "deployed", "succeeded"} {
		if strings.Contains(strings.ToLower(string(text)), word) {
			t.Errorf("the trace says %q", word)
		}
	}
}

func TestGapReasonsAreTheClosedSet(t *testing.T) {
	t.Parallel()

	// Each group is one change; a later change appends one group.
	foundation := []string{
		"coverage-incomplete", "unsupported-family", "capability-regression", "environment-unresolved", "ambiguous-binding",
		"binding-gap", "evidence-conflict", "selector-property-unknown", "limit-exceeded",
	}
	reviewCurrency := []string{"target-not-declared", "trust-anchor-unreadable", "verification-unavailable", "reference-unresolved"}
	assuranceAdapters := []string{
		"claim-not-enabled", "no-evidence-supplied", "no-evaluator-available", "rendered-evidence-missing",
		"enforcement-not-declared", "reviewed-mode-not-available",
	}
	trace := []string{"absent", "ambiguous-build-definition", "mutable-tag"}
	want := slices.Concat(foundation, reviewCurrency, assuranceAdapters, trace)

	seen := map[string]bool{}

	for _, r := range want {
		if seen[r] {
			t.Errorf("%s is listed twice", r)
		}

		seen[r] = true
	}

	got := make([]string, 0, len(deployment.Reasons()))
	for _, reason := range deployment.Reasons() {
		got = append(got, string(reason))
	}

	if !slices.Equal(got, want) {
		t.Errorf("reasons = %v", got)
	}
}

func TestTraceSharedBaseFileGivesEachEnvironmentItsOwnWorkload(t *testing.T) {
	t.Parallel()

	w := newWorld()
	// One manifest in the sources of both environments: attribution makes one node for each (`@staging` suffix).
	w.workload("base", "production", "k8s/base/orders.yaml", "apps/Deployment/shop/orders")
	w.workload("base@staging", "staging", "k8s/base/orders.yaml", "apps/Deployment/shop/orders")
	w.bind("base", "orders-service", "bind")
	w.bind("base@staging", "orders-service", "bind")

	in := w.input(envs("production", "staging"), "orders-service")
	in.Coverage = slices.Concat(complete("production"), complete("staging"))
	trace := deployment.BuildTrace(in)

	identities := make([]string, 0, 2)

	for _, env := range []string{"production", "staging"} {
		workloads := entitiesOf(stepOf(t, subjectOf(t, trace, "orders-service", env), deployment.StepWorkload), deployment.KindWorkload)
		if len(workloads) != 1 || !strings.HasPrefix(workloads[0].Identity, "k8s:"+env+"/") {
			t.Fatalf("%s: workloads = %+v", env, workloads)
		}

		identities = append(identities, workloads[0].Identity)
	}

	if identities[0] == identities[1] {
		t.Errorf("the two environments share the workload %s", identities[0])
	}
}
