package deployment_test

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

const (
	infraRecording = "../../test/fixtures/cbm-recordings/infra.json"
	infraProject   = "concordia-fixture-infra"
	prodDeployment = "k8s/prod/deployment.yaml"
	apiRepository  = "registry.example.com/payments/api"
)

// infraEnvironments are the environments of the infra recording tests: production (cluster prod-eu) and staging.
func infraEnvironments() []deployment.Environment {
	return []deployment.Environment{{ID: "production", Cluster: "prod-eu"}, {ID: "staging"}}
}

func infraSources() map[string][]string {
	return map[string][]string{
		"production": {"k8s/prod/**", "charts/**", "scripts/**"},
		"staging":    {"k8s/staging/**"},
	}
}

// infraEvidence replays the infra recording and normalizes it as a check does: environment attribution, the
// possibly-lossy mark, scoped identities, and the derived placement facts.
func infraEvidence(t *testing.T) *observation.EvidenceQueryResult {
	t.Helper()

	client, err := observation.LoadReplayClient(infraRecording)
	if err != nil {
		t.Fatal(err)
	}

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: infraProject, GitRevision: client.HeadSHA()})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	kinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, edgeType := range ref.Schema.EdgeTypes {
		kinds = append(kinds, edgeType.Type)
	}

	result, err := provider.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	sources := infraSources()
	clusters := map[string]string{"production": "prod-eu"}

	observation.AttributeEnvironments(&result, func(path string) []string {
		var out []string

		for _, env := range infraEnvironments() {
			if fsutil.GlobMatcher(sources[env.ID])(path) {
				out = append(out, env.ID)
			}
		}

		return out
	})

	if profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema); ok {
		observation.MarkPossiblyLossy(result.Edges, profile)
	}

	observation.AnnotateScopedIDs(result.Nodes, func(env string) string { return clusters[env] })
	observation.DerivePlacement(&result, func(env string) string { return clusters[env] })

	return &result
}

// infraBindings binds the production and staging `api` workloads, its image repository, and its source root to
// api-service, and the staging worker to worker-service.
func infraBindings() []binding.Binding {
	return []binding.Binding{
		{
			ID: "bind-api-workload", ArchitectureRef: "api-service", Status: binding.StatusActive,
			Match: []binding.Match{
				{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "api", Namespace: "payments"},
				{Kind: binding.MatchWorkload, Environment: "staging", ResourceKind: "Deployment", Name: "api", Namespace: "payments-staging"},
			},
		},
		{ID: "bind-api-image", ArchitectureRef: "api-service", Status: binding.StatusActive, Match: []binding.Match{{Kind: binding.MatchImage, Repository: apiRepository}}},
		{ID: "bind-api-source", ArchitectureRef: "api-service", Status: binding.StatusActive, Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "app/**"}}},
		{
			ID: "bind-worker-workload", ArchitectureRef: "worker-service", Status: binding.StatusActive,
			Match: []binding.Match{{Kind: binding.MatchWorkload, Environment: "staging", ResourceKind: "Deployment", Name: "worker", Namespace: "payments"}},
		},
	}
}

func deployableModel(ids ...string) *intent.ArchitectureModel {
	model := &intent.ArchitectureModel{}

	for _, id := range ids {
		model.Nodes = append(model.Nodes, intent.NodeDeclaration{ID: id, NodeType: "service", Name: id, Tags: []string{"deployable"}, Bindings: []intent.ExtBinding{}})
	}

	return model
}

// infraInput is the input of a trace on the infra recording with the bindings of infraBindings.
func infraInput(t *testing.T) *deployment.Input {
	t.Helper()

	evidence := infraEvidence(t)

	observed := slices.Collect(maps.Values(evidence.Nodes))
	observed = append(observed, evidence.Entities...)
	observed = append(observed, binding.ImageDigestNodes(evidence.Edges)...)

	clusters := map[string]string{"production": "prod-eu"}
	bindings := infraBindings()
	set := binding.Resolve(bindings, observed, nil, binding.WithClusterOf(func(env string) string { return clusters[env] }))

	return &deployment.Input{
		Model: deployableModel("api-service", "worker-service"), Evidence: evidence, BindingSet: &set, Bindings: bindings,
		Declared: infraEnvironments(),
	}
}

// world is a small hand-made evidence graph: the nodes and facts of the test, with the binding set that maps the
// workloads to services.
type world struct {
	nodes    map[string]observation.ObservedNode
	subjects map[string][]string
	used     map[string][]string
	edges    []*observation.EvidenceEdge
	next     int
}

func newWorld() *world {
	return &world{nodes: map[string]observation.ObservedNode{}, subjects: map[string][]string{}, used: map[string][]string{}}
}

// workload adds a Deployment resource. objKey is `apps/Deployment/<namespace>/<name>`.
func (w *world) workload(id, env, file, objKey string) string {
	w.nodes[id] = observation.ObservedNode{
		ObservedID: id, Label: "Resource", QualifiedName: "k8s::" + objKey, FilePath: file, StartLine: 1, Environment: env,
		Properties: map[string]string{"obj_key": objKey},
	}

	return id
}

// bind maps an observed ID to a service through a binding.
func (w *world) bind(observed, service, bindingID string) {
	w.subjects[observed] = append(w.subjects[observed], service)
	w.used[observed] = append(w.used[observed], bindingID)
}

func (w *world) image(id, repository string) string {
	w.nodes[id] = observation.ObservedNode{ObservedID: id, Label: observation.LabelImage, QualifiedName: "__image__" + repository}

	return id
}

func (w *world) dockerfile(id, file string) string {
	w.nodes[id] = observation.ObservedNode{ObservedID: id, Label: "Module", QualifiedName: "docker.Dockerfile", FilePath: file}

	return id
}

func (w *world) edge(kind observation.CanonicalEdgeKind, from, to, file string, line int, props map[string]string) {
	w.next++

	edge := &observation.EvidenceEdge{
		ID: "edge-" + strconv.Itoa(w.next), From: from, To: to, Kind: kind, Properties: props, Environment: w.nodes[from].Environment,
		SourceSpan: &observation.SourceSpan{Path: file, Line: line},
	}
	w.edges = append(w.edges, edge)
}

// uses adds a `uses-image` fact of a container.
func (w *world) uses(workload, image, container, ref string) {
	node := w.nodes[workload]

	w.edge(observation.KindUsesImage, workload, image, node.FilePath, 1, map[string]string{"container": container, "image": ref, "ref": "image"})
}

// builtFrom adds a `built-from` fact from an image to a Dockerfile, named by a build command at file:line.
func (w *world) builtFrom(image, dockerfile string, line int) {
	w.edge(observation.KindBuiltFrom, image, dockerfile, "", 0, map[string]string{
		"via": "build", "tool": "docker-build", "file": "Makefile", "line": strconv.Itoa(line),
	})
}

func (w *world) baseImage(dockerfile, stage, ref string, line int) {
	w.edge(observation.KindBaseImage, dockerfile, "", w.nodes[dockerfile].FilePath, line, map[string]string{"image": ref, "stage": stage, "via": "from"})
}

func (w *world) input(declared []deployment.Environment, ids ...string) *deployment.Input {
	set := binding.Set{ByObserved: w.subjects, UsedBy: w.used}

	return &deployment.Input{
		Model: deployableModel(ids...), Evidence: &observation.EvidenceQueryResult{Nodes: w.nodes, Edges: w.edges}, BindingSet: &set, Declared: declared,
	}
}

func envs(ids ...string) []deployment.Environment {
	out := make([]deployment.Environment, 0, len(ids))
	for _, id := range ids {
		out = append(out, deployment.Environment{ID: id})
	}

	return out
}

// complete is a coverage record with the status complete.
func record(predicate, env string, status observation.CoverageStatus, modes ...string) observation.PredicateCoverage {
	rec := observation.PredicateCoverage{
		Predicate: predicate, Scope: observation.CoverageScope{Environment: env}, Status: status, LossModes: []observation.LossMode{}, Reasons: []string{},
	}

	for _, mode := range modes {
		rec.LossModes = append(rec.LossModes, observation.LossMode{ID: mode})
	}

	if status != observation.CoverageComplete {
		rec.Reasons = []string{"test: " + strings.Join(modes, ",")}
	}

	return rec
}

func entitiesOf(step *deployment.StepTrace, kind string) []deployment.Entity {
	var out []deployment.Entity

	for i := range step.Entities {
		if step.Entities[i].Kind == kind {
			out = append(out, step.Entities[i])
		}
	}

	return out
}

func stepOf(t *testing.T, trace *deployment.SubjectTrace, step deployment.Step) *deployment.StepTrace {
	t.Helper()

	for i := range trace.Steps {
		if trace.Steps[i].Step == step {
			return &trace.Steps[i]
		}
	}

	t.Fatalf("no step %s", step)

	return nil
}

func gapsWith(gaps []deployment.Gap, reason deployment.Reason) []deployment.Gap {
	return slices.DeleteFunc(slices.Clone(gaps), func(g deployment.Gap) bool { return g.Reason != reason })
}

func subjectOf(t *testing.T, trace *deployment.Trace, id, env string) *deployment.SubjectTrace {
	t.Helper()

	sub := trace.Subject(id, env)
	if sub == nil {
		t.Fatalf("no trace of %s in %s", id, env)
	}

	return sub
}

// nodeOf is a service node with no tag.
func nodeOf(id string) intent.NodeDeclaration {
	return intent.NodeDeclaration{ID: id, NodeType: "service", Name: id, Tags: []string{}, Bindings: []intent.ExtBinding{}}
}

// deployedIn is a deployed-in relationship that lists the nodes.
func deployedIn(nodes ...string) intent.RelationshipDeclaration {
	return intent.RelationshipDeclaration{ID: "placement", Kind: intent.RelDeployedIn, Container: "cluster", Targets: nodes}
}
