package deployment_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

func workloadByName(t *testing.T, view *deployment.View, identity string) *deployment.Workload {
	t.Helper()

	for i := range view.Workloads {
		if view.Workloads[i].Identity == identity {
			return &view.Workloads[i]
		}
	}

	t.Fatalf("no workload %s in %d workloads", identity, len(view.Workloads))

	return nil
}

func TestExtractSameNameInTwoEnvironments(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.workload("p", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	w.workload("s", "staging", "k8s/staging/orders.yaml", "apps/Deployment/shop/orders")
	w.bind("p", "orders-service", "bind-p")
	w.bind("s", "orders-service", "bind-s")

	view := deployment.Extract(w.input(envs("production", "staging"), "orders-service"))
	extractRequireLen(t, "workloads", view.Workloads, 2)

	production := workloadByName(t, view, "k8s:production/~/apps/Deployment/shop/orders")
	staging := workloadByName(t, view, "k8s:staging/~/apps/Deployment/shop/orders")

	extractWant(t, "production environment", production.Environment, "production")
	extractWant(t, "staging environment", staging.Environment, "staging")

	if production.Identity == staging.Identity {
		t.Errorf("production = %+v, staging = %+v", production, staging)
	}

	// A workload must not take the node of the other environment.
	extractWantList(t, "production nodes", production.NodeIDs, []string{"p"})
	extractWantList(t, "staging nodes", staging.NodeIDs, []string{"s"})
}

func TestExtractInfraRecordingHasTheWorkloadsOfBothEnvironments(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))

	api := workloadByName(t, view, "k8s:production/prod-eu/apps/Deployment/payments/api")
	stagingAPI := workloadByName(t, view, "k8s:staging/~/apps/Deployment/payments-staging/api")

	// The cluster of the environment is in the identity; an environment with no cluster has the part `~`.
	extractWant(t, "api cluster", api.Cluster, "prod-eu")
	extractWant(t, "staging api cluster", stagingAPI.Cluster, "")

	if !api.EnvironmentResolved() {
		t.Errorf("api = %+v, want a resolved environment", api)
	}

	extractWant(t, "api kind", api.Kind, "Deployment")
	extractWant(t, "api namespace", api.Namespace, "payments")
	extractWant(t, "api name", api.Name, "api")
	extractWant(t, "api group", api.APIGroup, "apps")
	extractWantList(t, "api subjects", api.Subjects, []string{"api-service"})
	extractWantList(t, "api bindings", api.BindingIDs, []string{"bind-api-workload"})
}

func TestExtractTwoContainersTwoTagsOneRepository(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))
	api := workloadByName(t, view, "k8s:production/prod-eu/apps/Deployment/payments/api")

	// The provider keeps one fact for the pair (O4): the surviving occurrence is `sidecar`, and no tag of `app` shows.
	extractRequireLen(t, "occurrences", api.Occurrences, 1)

	occurrence := api.Occurrences[0]
	extractWant(t, "container", occurrence.Container, "sidecar")
	extractWantImage(t, &occurrence.Image, apiRepository, "1.3.0")

	if !occurrence.Lossy {
		t.Errorf("occurrence = %+v, want a lossy one", occurrence)
	}

	extractWant(t, "location path", occurrence.Location.Path, prodDeployment)
}

func TestExtractContainersWithDifferentRepositories(t *testing.T) {
	t.Parallel()

	w := newWorld()
	workload := w.workload("p", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	w.uses(workload, w.image("i1", "registry.example/shop/orders"), "app", "registry.example/shop/orders:1.4")
	w.uses(workload, w.image("i2", "registry.example/shop/audit"), "audit", "registry.example/shop/audit:2.0")

	view := deployment.Extract(w.input(envs("production"), "orders-service"))
	occurrences := view.Workloads[0].Occurrences

	extractRequireLen(t, "occurrences", occurrences, 2)
	extractWant(t, "first container", occurrences[0].Container, "app")
	extractWant(t, "second container", occurrences[1].Container, "audit")

	if occurrences[0].Image.Repository == occurrences[1].Image.Repository {
		t.Errorf("occurrences = %+v, want different repositories", occurrences)
	}

	extractWant(t, "first tag", occurrences[0].Image.Tag, "1.4")
	extractWant(t, "second tag", occurrences[1].Image.Tag, "2.0")
}

func TestExtractDigestPinnedReference(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))
	worker := workloadByName(t, view, "k8s:staging/~/apps/Deployment/payments/worker")

	extractRequireLen(t, "occurrences", worker.Occurrences, 1)

	image := &worker.Occurrences[0].Image
	extractWantImage(t, image, "registry.example.com/payments/worker", "")

	if image.Digest == "" || image.Mutable() {
		t.Errorf("image = %+v, want a digest and an immutable reference", image)
	}
}

func TestExtractWorkloadWithNoBindingIsUnbound(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))
	reports := workloadByName(t, view, "k8s:production/prod-eu/apps/Deployment/payments/reports")

	extractRequireLen(t, "subjects", reports.Subjects, 0)
	extractWant(t, "location path", reports.Locations[0].Path, "k8s/prod/reports.yaml")

	if reports.Locations[0].Line == 0 {
		t.Errorf("reports = %+v, want a line", reports)
	}
}

func TestExtractKeepsWorkloadsOfNoEnvironment(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))
	gateway := workloadByName(t, view, "k8s:~/~/apps/Deployment/~/gateway")

	if gateway.EnvironmentResolved() {
		t.Errorf("gateway = %+v, want an unresolved environment", gateway)
	}

	extractWant(t, "gateway environment", gateway.Environment, observation.EnvironmentUnresolved)

	if !gateway.Templated {
		t.Errorf("gateway = %+v, want a templated one", gateway)
	}

	// The Helm template keeps its literal name: it is a workload of no environment and is not rendered.
	helm := extractHelmTemplate(view)
	if helm == nil || !helm.Templated {
		t.Errorf("helm template = %+v", helm)
	}
}

func TestExtractCompetingDeclarationsStayOneWorkload(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.workload("a", "production", "k8s/prod/reports.yaml", "apps/Deployment/payments/reports")
	w.workload("b", "production", "k8s/prod/more/reports.yaml", "apps/Deployment/payments/reports")

	view := deployment.Extract(w.input(envs("production")))
	extractRequireLen(t, "workloads", view.Workloads, 1)

	workload := &view.Workloads[0]
	if !workload.Competing {
		t.Errorf("workload = %+v, want a competing one", workload)
	}

	extractWant(t, "locations", len(workload.Locations), 2)
	extractWantList(t, "nodes", workload.NodeIDs, []string{"a", "b"})
}

func TestExtractBuildDefinitionOfAMultiStageDockerfile(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))
	defs := view.BuildDefinitions(apiRepository)

	extractRequireLen(t, "definitions", defs, 1)

	def := &defs[0]
	extractWant(t, "path", def.Path, "docker/Dockerfile")
	extractWantDockerfileBases(t, def)
	extractWantDockerfileCopies(t, def)
	extractWantDockerfileCommands(t, def)
	extractWant(t, "line", def.Line, 1)
}

func TestExtractBaseImageFromABuildArgumentIsUnresolved(t *testing.T) {
	t.Parallel()

	w := newWorld()
	dockerfile := w.dockerfile("d", "build/Dockerfile")
	image := w.image("i", "registry.example/shop/orders")
	w.builtFrom(image, dockerfile, 3)
	w.baseImage(dockerfile, "0", "${BASE}", 1)
	w.baseImage(dockerfile, "1", "gcr.io/distroless/static:nonroot", 5)

	view := deployment.Extract(w.input(envs("production")))
	defs := view.BuildDefinitions("registry.example/shop/orders")

	extractRequireLen(t, "definitions", defs, 1)
	extractRequireLen(t, "bases", defs[0].Bases, 2)

	extractWant(t, "unresolved of the build argument", defs[0].Bases[0].Unresolved, true)
	extractWant(t, "unresolved of the literal", defs[0].Bases[1].Unresolved, false)
	extractWant(t, "literal repository", defs[0].Bases[1].Image.Repository, "gcr.io/distroless/static")
}

func TestExtractActions(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))
	byTool := extractActionsByTool(view)

	kubectl := byTool["kubectl"]
	extractWant(t, "kubectl target", kubectl.Target, prodDeployment)
	extractWant(t, "kubectl target kind", kubectl.TargetKind, deployment.TargetFile)
	extractWant(t, "kubectl location", kubectl.Location, deployment.Location{Path: "scripts/deploy.sh", Line: 6})

	helm := byTool["helm"]
	extractWant(t, "helm target", helm.Target, "charts/api")
	extractWant(t, "helm target kind", helm.TargetKind, deployment.TargetChart)
	extractWant(t, "helm line", helm.Location.Line, 7)

	// A chart does not name a workload that it does not contain; a file names itself only.
	coverage := []struct {
		name   string
		action *deployment.Action
		file   string
		want   bool
	}{
		{"a file names itself", &kubectl, prodDeployment, true},
		{"a file names no other file", &kubectl, "k8s/prod/reports.yaml", false},
		{"a chart names its template", &helm, "charts/api/templates/deployment.yaml", true},
		{"a chart names no sibling directory", &helm, "charts/apix/a.yaml", false},
	}

	for _, c := range coverage {
		extractWant(t, "action coverage: "+c.name, c.action.Covers(c.file), c.want)
	}

	if _, ok := byTool["chart"]; ok {
		t.Errorf("a chart-to-template fact is an action: %+v", byTool["chart"])
	}
}

func TestExtractHostingTargets(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))

	api := workloadByName(t, view, "k8s:production/prod-eu/apps/Deployment/payments/api")
	extractRequireLen(t, "api placements", api.Placements, 1)
	extractWant(t, "api placement name", api.Placements[0].Name, "payments")
	extractWant(t, "api placement declared", api.Placements[0].Declared, true)
	extractWant(t, "api placement path", api.Placements[0].Locations[0].Path, "k8s/prod/namespace.yaml")

	// The staging worker is in a namespace that only production declares: its target is synthesized in staging.
	worker := workloadByName(t, view, "k8s:staging/~/apps/Deployment/payments/worker")
	extractRequireLen(t, "worker placements", worker.Placements, 1)
	extractWant(t, "worker placement declared", worker.Placements[0].Declared, false)
	extractRequireLen(t, "worker placement locations", worker.Placements[0].Locations, 0)
	extractWant(t, "worker placement identity", worker.Placements[0].Identity, "k8s:staging/~/core/Namespace//payments")
}

func TestExtractConsumersCountWorkloadsOfAllEnvironments(t *testing.T) {
	t.Parallel()

	view := deployment.Extract(infraInput(t))

	// payments/api (production, sidecar) and payments-staging/api (staging) use the repository.
	extractWant(t, "consumers of "+apiRepository, view.Consumers(apiRepository), 2)
	extractWant(t, "consumers of the worker image", view.Consumers("registry.example.com/payments/worker"), 1)
}
