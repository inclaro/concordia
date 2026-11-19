package conformance

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/workspace"
)

// The evaluators read plain structs that mirror the Kubernetes documents. This file turns the rendered evidence of one
// environment (internal/assurance/render) into those structs and maps the CALM elements of a rule onto rendered
// resources through the bindings. Selection is always read from the rendered documents, never from the code-graph
// selection edges (design D8, observations O6).

// envModel is the rendered evidence of one environment, indexed for the rule evaluation.
type envModel struct {
	environment string
	// overall is the worst coverage of every render of the environment: the coverage of cluster-scoped resources,
	// which can come from any source.
	overall assurance.Coverage
	// namespaces is the render coverage of each namespace. A namespace gets the worst coverage of the renders that
	// hold one of its resources.
	namespaces map[string]assurance.Coverage
	renders    []*render.Result
}

// coverageRank orders the coverage states; a higher rank is worse.
func coverageRank(c assurance.Coverage) int {
	switch c {
	case assurance.CoverageComplete:
		return 0
	case assurance.CoveragePartial:
		return 1
	case assurance.CoverageFailed:
		return 2
	}

	// A coverage that is not stated counts as failed: a forgotten field must never produce a pass.
	return 2
}

// worse gives the worse of two coverage states. The empty value of the first argument means that nothing is known yet.
func worse(a, b assurance.Coverage) assurance.Coverage {
	if a == "" || coverageRank(b) > coverageRank(a) {
		return b
	}

	return a
}

// newEnvModel indexes the renders of one environment. A source that failed has no resources, so the namespace it
// would have contributed is unknown: when the workspace declares its namespace (a Helm release) that namespace is
// failed, otherwise every namespace of the environment is, because the missing documents may select or allow anything.
// The other namespaces of a split environment keep their own coverage.
func newEnvModel(environment string, renders []*render.Result, cfg *workspace.Assurance) *envModel {
	model := &envModel{environment: environment, renders: renders, namespaces: map[string]assurance.Coverage{}, overall: assurance.CoverageComplete}

	var unattributed assurance.Coverage

	for _, result := range renders {
		model.overall = worse(model.overall, result.Coverage)
		model.noteResources(result)

		if !isResourcelessGap(result) {
			continue
		}

		if namespace := declaredNamespace(cfg, environment, result); namespace != "" {
			model.noteNamespace(namespace, result.Coverage)
		} else {
			unattributed = worse(unattributed, result.Coverage)
		}
	}

	model.degradeNamespaces(unattributed)

	return model
}

// noteNamespace records the coverage of a namespace; the worst coverage seen for it wins.
func (m *envModel) noteNamespace(namespace string, coverage assurance.Coverage) {
	if namespace == "" {
		return
	}

	if current, ok := m.namespaces[namespace]; ok {
		coverage = worse(current, coverage)
	}

	m.namespaces[namespace] = coverage
}

// noteResources records the coverage that a render gives to the namespace of each of its resources.
func (m *envModel) noteResources(result *render.Result) {
	for i := range result.Resources {
		coverage := result.Coverage
		if result.Resources[i].Conflict {
			// Two documents with one identity: claims about the resource are unknown, so the namespace is not complete.
			coverage = worse(coverage, assurance.CoveragePartial)
		}

		m.noteNamespace(result.Resources[i].ID.Namespace, coverage)
	}
}

// degradeNamespaces lowers every known namespace to the coverage of the sources that no namespace could be attributed
// to. An empty coverage means that there is no such source.
func (m *envModel) degradeNamespaces(unattributed assurance.Coverage) {
	if unattributed == "" {
		return
	}

	for namespace := range m.namespaces {
		m.namespaces[namespace] = worse(m.namespaces[namespace], unattributed)
	}
}

// isResourcelessGap says whether a render has a coverage gap and produced no resource to read the namespace from.
func isResourcelessGap(result *render.Result) bool {
	return coverageRank(result.Coverage) != 0 && len(result.Resources) == 0
}

// declaredNamespace gives the namespace that the workspace declares for the source of a render, "" when it declares none.
func declaredNamespace(cfg *workspace.Assurance, environment string, result *render.Result) string {
	if cfg == nil {
		return ""
	}

	if cfg.Rendering == nil {
		return ""
	}

	for i := range cfg.Rendering.Sources {
		if source := &cfg.Rendering.Sources[i]; sourceProducedRender(source, environment, result) {
			return source.Namespace
		}
	}

	return ""
}

// sourceProducedRender says whether a declared source of the environment is the one that a render came from.
func sourceProducedRender(source *workspace.RenderSource, environment string, result *render.Result) bool {
	return source.Environment == environment && source.Tool == result.Tool && source.Path == result.Path
}

// usable says whether the environment has rendered evidence that an evaluator may read: at least one render that
// produced resources. An environment whose sources all failed has only gaps.
func (m *envModel) usable() bool {
	return slices.ContainsFunc(m.renders, func(r *render.Result) bool { return len(r.Resources) > 0 })
}

// resources calls visit for every rendered resource of the environment, in the order of the renders.
func (m *envModel) resources(visit func(*render.Result, *render.Resource)) {
	for _, result := range m.renders {
		for i := range result.Resources {
			visit(result, &result.Resources[i])
		}
	}
}

// namespaceList gives the namespaces with their coverage, sorted.
func (m *envModel) namespaceList() []string {
	names := make([]string, 0, len(m.namespaces))
	for name := range m.namespaces {
		names = append(names, name)
	}

	slices.SortFunc(names, canonjson.CompareUTF16)

	return names
}

// resourcesOf gives the rendered resources of the environment that a CALM element is bound to, sorted. A `workload`
// binding names the resource by environment, kind, and name (and optionally namespace, API group, and cluster); an
// `image` binding selects the workloads that run the repository (and digest). Bindings that are only proposed do not
// count, as in the binding resolution. An element that no binding reaches has no resource: the caller reports it as a
// binding gap and never as an isolated or unreachable element.
func (m *envModel) resourcesOf(bindings []binding.Binding, calmID string) []*render.Resource {
	found := map[string]*render.Resource{}

	add := func(resource *render.Resource) { found[resource.ID.String()] = resource }

	for i := range bindings {
		if b := &bindings[i]; b.ArchitectureRef == calmID && b.Status != binding.StatusProposed {
			m.matchAll(b.Match, add)
		}
	}

	out := make([]*render.Resource, 0, len(found))
	for _, resource := range found {
		out = append(out, resource)
	}

	slices.SortFunc(out, func(a, b *render.Resource) int { return render.CompareResourceIDs(a.ID, b.ID) })

	return out
}

// matchAll adds the resources that the matches of one binding reach. Only the `workload` and `image` matches name
// rendered resources; every other kind reaches code-graph or telemetry nodes.
func (m *envModel) matchAll(matches []binding.Match, add func(*render.Resource)) {
	for j := range matches {
		match := &matches[j]

		if match.Kind == binding.MatchWorkload {
			m.matchWorkload(match, add)
		}

		if match.Kind == binding.MatchImage {
			m.matchImage(match, add)
		}
	}
}

func (m *envModel) matchWorkload(match *binding.Match, add func(*render.Resource)) {
	if match.Environment != m.environment {
		return
	}

	m.resources(func(_ *render.Result, resource *render.Resource) {
		if workloadMatchReaches(match, &resource.ID) {
			add(resource)
		}
	})
}

// workloadMatchReaches says whether a `workload` match names the resource: kind and name are required, the namespace,
// API group, and cluster only narrow the match when the binding states them.
func workloadMatchReaches(match *binding.Match, id *render.ResourceID) bool {
	if id.Kind != match.ResourceKind || id.Name != match.Name {
		return false
	}

	return matchScopeAllows(match.Namespace, id.Namespace) && matchScopeAllows(match.APIGroup, id.Group) && matchScopeAllows(match.Cluster, id.Cluster)
}

// matchScopeAllows says whether an optional scope of a match admits the value of a resource: an empty scope admits all.
func matchScopeAllows(scope, value string) bool {
	return scope == "" || scope == value
}

func (m *envModel) matchImage(match *binding.Match, add func(*render.Resource)) {
	if match.Environment != "" && match.Environment != m.environment {
		return
	}

	repository := match.Repository
	if ref, err := render.ParseImageRef(repository); err == nil {
		repository = ref.Repository
	}

	for _, result := range m.renders {
		for i := range result.Occurrences {
			if resource := imageOccurrenceWorkload(result, &result.Occurrences[i], repository, match.Digest); resource != nil {
				add(resource)
			}
		}
	}
}

// imageOccurrenceWorkload gives the workload resource of an occurrence that runs the repository (and the digest when
// the match states one), nil when the occurrence does not.
func imageOccurrenceWorkload(result *render.Result, occ *render.ContainerOccurrence, repository, digest string) *render.Resource {
	if occ.Repository != repository || !matchScopeAllows(digest, occ.Digest) {
		return nil
	}

	return result.Resource(&occ.Workload)
}

// resourceName is the `namespace/name` text of a resource, or the name of a cluster-scoped one.
func resourceName(id *render.ResourceID) string {
	if id.Namespace == "" {
		return id.Name
	}

	return id.Namespace + "/" + id.Name
}

// pluralOf gives the plural resource name of a kind for an RBAC request: the lower-case kind with the English plural
// that Kubernetes uses for its own kinds (`Secret` -> `secrets`, `Ingress` -> `ingresses`, `NetworkPolicy` ->
// `networkpolicies`). A custom resource with an irregular plural is not found by it and its result is `not-granted`
// only when no rule names the resource, so the limit is stated in the evaluation entry.
func pluralOf(kind string) string {
	lower := strings.ToLower(kind)

	// A kind whose name is already plural keeps it (`Endpoints` -> `endpoints`).
	if plural, ok := irregularPlurals[lower]; ok {
		return plural
	}

	switch {
	case hasSibilantEnding(lower):
		return lower + "es"
	case hasConsonantY(lower):
		// A consonant before the `y` (`NetworkPolicy` -> `networkpolicies`); a vowel keeps it (`Gateway` -> `gateways`).
		return strings.TrimSuffix(lower, "y") + "ies"
	}

	return lower + "s"
}

// hasSibilantEnding says whether a lower-case kind takes `es` as its plural.
func hasSibilantEnding(lower string) bool {
	for _, suffix := range []string{"s", "x", "ch", "sh"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

// hasConsonantY says whether a lower-case kind ends in a `y` that follows a consonant.
func hasConsonantY(lower string) bool {
	return len(lower) > 1 && strings.HasSuffix(lower, "y") && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2]))
}

// irregularPlurals are the Kubernetes kinds whose resource name the rules of pluralOf do not give. A wrong name
// would match no RBAC rule and so read as `not-granted`, a false pass for `expect: not-granted`.
var irregularPlurals = map[string]string{"endpoints": "endpoints"}
