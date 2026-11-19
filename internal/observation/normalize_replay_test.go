package observation_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

// These tests replay the recording of the infra fixture (test/fixtures/infra),
// made with codebase-memory-mcp 0.12.1 by `mise run record-cbm`, through the
// adapter. They check each row of the normalization table on real native edges.
const (
	infraRecording = "../../test/fixtures/cbm-recordings/infra.json"
	infraProject   = "concordia-fixture-infra"
)

// infraResult is the result of FetchEdges for every native type of the recording.
type infraResult struct {
	ref *observation.GraphSnapshotRef
	observation.EvidenceQueryResult
}

func fetchInfra(t *testing.T) *infraResult {
	t.Helper()

	client, err := observation.LoadReplayClient(infraRecording)
	if err != nil {
		t.Fatal(err)
	}

	p := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := p.Freeze(t.Context(), observation.FreezeRequest{Project: infraProject, GitRevision: client.HeadSHA()})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	kinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, e := range ref.Schema.EdgeTypes {
		kinds = append(kinds, e.Type)
	}

	res, err := p.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	return &infraResult{EvidenceQueryResult: res, ref: ref}
}

func (r *infraResult) kinds() map[observation.CanonicalEdgeKind]int {
	out := map[observation.CanonicalEdgeKind]int{}
	for _, e := range r.Edges {
		out[e.Kind]++
	}

	return out
}

// where gives the edges for which keep is true.
func (r *infraResult) where(keep func(*observation.EvidenceEdge) bool) []*observation.EvidenceEdge {
	var out []*observation.EvidenceEdge

	for _, e := range r.Edges {
		if keep(e) {
			out = append(out, e)
		}
	}

	return out
}

// ofKind gives the edges of a canonical kind.
func (r *infraResult) ofKind(kind observation.CanonicalEdgeKind) []*observation.EvidenceEdge {
	return r.where(func(e *observation.EvidenceEdge) bool { return e.Kind == kind })
}

// infraKindCounts are the numbers of facts of each canonical kind that the recording gives.
var infraKindCounts = map[observation.CanonicalEdgeKind]int{
	"calls": 2, "contains": 57, "defines": 136, "ambiguous-usage": 2,
	"deploys-artifact": 3, "base-image": 2, "copies-from-image": 1, "built-from": 2,
	"infrastructure-dependency": 9, "selects-workload": 3, "routes-to": 6, "configured-connection": 1,
	"launches": 5, "overrides": 3, "includes-infrastructure": 4, "uses-image": 7, "uses-runtime-identity": 1,
	"references-secret": 1, "references-configuration": 1, "references-storage": 1, "references-certificate-secret": 1,
	"route-attaches-to-gateway": 1, "binding-refers-to-role": 1, "binding-applies-to-principal": 1,
	"policy-selects-workload": 1, "disruption-policy-target": 1, "scaling-target": 1,
	"configures-env-access": 1, "configures-env-set": 8, "configures-env-read": 2, "configures-helm-values": 1,
	"supersedes": 1, "mentions": 40, "references-file": 37, "diagram-arrow": 3, "tests": 1, "tests-file": 1,
}

// reportKindCountDifferences reports each kind whose number of facts differs from the expected one.
func reportKindCountDifferences(t *testing.T, got, want map[observation.CanonicalEdgeKind]int) {
	t.Helper()

	for k, n := range want {
		if got[k] != n {
			t.Errorf("kind %s: got %d, want %d", k, got[k], n)
		}
	}

	for k, n := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("kind %s: unexpected %d", k, n)
		}
	}
}

// isIncompleteFact says whether a normalized fact lacks its normalization version, role, resolution, or endpoint labels.
func isIncompleteFact(e *observation.EvidenceEdge) bool {
	if e.NormalizationVersion != "2.1" {
		return true
	}

	return slices.Contains([]string{string(e.Role), string(e.Resolution), e.SourceLabel, e.TargetLabel}, "")
}

func TestReplayNormalizesEveryNativeEdgeOfTheInfraRecording(t *testing.T) {
	r := fetchInfra(t)

	if got := r.kinds(); !maps.Equal(got, infraKindCounts) {
		reportKindCountDifferences(t, got, infraKindCounts)
	}

	// Nothing in the recording is ambiguous, imports included: the code `IMPORTS` are absent and the other two shapes are told apart.
	if n := len(r.where(func(e *observation.EvidenceEdge) bool { return e.Resolution == observation.ResolutionAmbiguous })); n != 0 {
		t.Errorf("%d ambiguous facts", n)
	}

	for _, e := range r.Edges {
		if isIncompleteFact(e) {
			t.Fatalf("incomplete fact %+v", e)
		}
	}
}

// deploysArtifactByVia gives the deploys-artifact fact of each `via` property.
func deploysArtifactByVia(r *infraResult) map[string]*observation.EvidenceEdge {
	byVia := map[string]*observation.EvidenceEdge{}
	for _, e := range r.ofKind(observation.KindDeploysArtifact) {
		byVia[e.Properties["via"]] = e
	}

	return byVia
}

// requireNoPlacementFact fails the test when a fact of the recording is a placement.
func requireNoPlacementFact(t *testing.T, r *infraResult) {
	t.Helper()

	for _, e := range r.Edges {
		if e.Kind == "deployed-in" || e.Kind == "placement" {
			t.Fatalf("placement fact %+v", e)
		}
	}
}

func TestReplayDeploysArtifactHasNoPlacement(t *testing.T) {
	r := fetchInfra(t)

	byVia := deploysArtifactByVia(r)

	for _, via := range []string{"kubectl", "helm", "chart"} {
		if byVia[via] == nil {
			t.Fatalf("no deploys-artifact with via=%s", via)
		}
	}

	// kubectl deploys the applied file, not the resources inside it (O8).
	if to := r.Nodes[byVia["kubectl"].To]; to.Label != "File" || to.FilePath != "k8s/prod/deployment.yaml" {
		t.Fatalf("kubectl target = %+v", to)
	}

	if to := r.Nodes[byVia["helm"].To]; to.Label != "Chart" {
		t.Fatalf("helm target = %+v", to)
	}

	// No kubectl fact starts at the resources of the file, and nothing is placement.
	if n := len(r.where(func(e *observation.EvidenceEdge) bool {
		return e.NativeKind == "DEPLOYS" && e.Properties["via"] == "kubectl"
	})); n != 1 {
		t.Fatalf("%d kubectl facts", n)
	}

	requireNoPlacementFact(t, r)
}

// dependencyFamilyCounts counts the infrastructure-dependency facts by family.
func dependencyFamilyCounts(r *infraResult) map[string]int {
	families := map[string]int{}
	for _, e := range r.ofKind(observation.KindInfrastructureDependency) {
		families[e.Facets["family"]]++
	}

	return families
}

// isChartDependency says whether a fact is a chart dependency as the provider gives it: a DEPENDS_ON with a repository and no via.
func isChartDependency(e *observation.EvidenceEdge) bool {
	if e.Properties["via"] != "" || e.Properties["repository"] == "" {
		return false
	}

	return e.NativeKind == "DEPENDS_ON"
}

// terraformRefCounts counts the Terraform facts by the reference that they keep as a facet.
func terraformRefCounts(t *testing.T, r *infraResult) map[string]int {
	t.Helper()

	refs := map[string]int{}

	for _, e := range r.Edges {
		if e.Facets["family"] != "terraform" {
			continue
		}

		if e.NativeKind != "USAGE" || e.Properties["via"] != "terraform" {
			t.Fatalf("terraform fact = %+v", e)
		}

		refs[e.Facets["terraformRef"]]++
	}

	return refs
}

func TestReplayInfrastructureDependencyFamiliesStayApart(t *testing.T) {
	r := fetchInfra(t)

	if families, want := dependencyFamilyCounts(r), map[string]int{"chart": 1, "compose": 1, "terraform": 7}; !maps.Equal(families, want) {
		t.Fatalf("families = %v, want %v", families, want)
	}

	// The chart dependency has no via and has the property repository (O9).
	chart := r.where(func(e *observation.EvidenceEdge) bool { return e.Facets["family"] == "chart" })[0]
	if !isChartDependency(chart) {
		t.Fatalf("chart dependency = %+v", chart)
	}

	// A Terraform USAGE keeps its reference as a facet.
	if refs, want := terraformRefCounts(t, r), map[string]int{"var": 3, "module": 1, "output": 1, "resource": 2}; !maps.Equal(refs, want) {
		t.Fatalf("terraform refs = %v, want %v", refs, want)
	}
}

// configuresByKind groups the CONFIGURES facts by canonical kind.
func configuresByKind(r *infraResult) map[observation.CanonicalEdgeKind][]*observation.EvidenceEdge {
	byKind := map[observation.CanonicalEdgeKind][]*observation.EvidenceEdge{}

	for _, e := range r.Edges {
		if e.NativeKind == "CONFIGURES" {
			byKind[e.Kind] = append(byKind[e.Kind], e)
		}
	}

	return byKind
}

// requireConfiguresDirection checks the labels of the endpoints of the facts of one kind.
func requireConfiguresDirection(t *testing.T, kind observation.CanonicalEdgeKind, edges []*observation.EvidenceEdge, want [2]string) {
	t.Helper()

	for _, e := range edges {
		if e.SourceLabel != want[0] || e.TargetLabel != want[1] {
			t.Errorf("%s: %s -> %s, want %s -> %s", kind, e.SourceLabel, e.TargetLabel, want[0], want[1])
		}
	}
}

// requireEnvSetKeepsScopeAndSource checks that each env_set fact keeps its scope and source and targets an EnvVar.
func requireEnvSetKeepsScopeAndSource(t *testing.T, edges []*observation.EvidenceEdge) {
	t.Helper()

	for _, e := range edges {
		if e.Properties["scope"] == "" || e.Properties["source"] == "" {
			t.Errorf("env_set lost scope or source: %v", e.Properties)
		}

		if e.TargetLabel != "EnvVar" {
			t.Errorf("env_set target = %s", e.TargetLabel)
		}
	}
}

// requireConfiguresStoresNoValue checks that a CONFIGURES fact keeps only the properties that cannot hold a value, and no facets.
func requireConfiguresStoresNoValue(t *testing.T, e *observation.EvidenceEdge) {
	t.Helper()

	allowed := []string{"container", "dockerfile", "line", "scope", "source", "strategy"}

	for name := range e.Properties {
		if !slices.Contains(allowed, name) {
			t.Errorf("property %q on a CONFIGURES fact: a value could be stored", name)
		}
	}

	if len(e.Facets) != 0 {
		t.Errorf("facets on a CONFIGURES fact: %v", e.Facets)
	}
}

func TestReplayConfigurationStrategiesStoreNoValues(t *testing.T) {
	r := fetchInfra(t)

	byKind := configuresByKind(r)

	// Direction: env_access code to EnvVar, env_set workload to EnvVar, env_read EnvVar to code, helm_values values.yaml to Chart.
	directions := map[observation.CanonicalEdgeKind][2]string{
		observation.KindConfiguresEnvAccess:  {"Function", "EnvVar"},
		observation.KindConfiguresEnvRead:    {"EnvVar", "Function"},
		observation.KindConfiguresHelmValues: {"File", "Chart"},
	}

	for kind, want := range directions {
		requireConfiguresDirection(t, kind, byKind[kind], want)
	}

	requireEnvSetKeepsScopeAndSource(t, byKind[observation.KindConfiguresEnvSet])

	for _, edges := range byKind {
		for _, e := range edges {
			requireConfiguresStoresNoValue(t, e)
		}
	}

	if len(byKind[observation.KindConfiguresHelmValues]) != 1 {
		t.Fatalf("helm values facts = %d", len(byKind[observation.KindConfiguresHelmValues]))
	}
}

func TestReplaySecretAndConfigMapAreToldApartByTargetKind(t *testing.T) {
	r := fetchInfra(t)

	secret := r.ofKind(observation.KindReferencesSecret)
	config := r.ofKind(observation.KindReferencesConfiguration)

	if len(secret) != 1 || len(config) != 1 {
		t.Fatalf("secret facts %d, configuration facts %d", len(secret), len(config))
	}

	secretFacets := [3]string{secret[0].Facets["referenceMode"], secret[0].Facets["targetKind"], r.Nodes[secret[0].To].Kind}
	if secretFacets != [3]string{"env", "Secret", "Secret"} {
		t.Fatalf("secret fact = %+v target %+v", secret[0], r.Nodes[secret[0].To])
	}

	if config[0].Facets["referenceMode"] != "envFrom" || config[0].Facets["targetKind"] != "ConfigMap" {
		t.Fatalf("configuration fact = %+v", config[0])
	}

	// The reference mode and the endpoint kind are two facets, never one.
	if secret[0].Facets["referenceMode"] == secret[0].Facets["targetKind"] {
		t.Fatal("reference mode and target kind are the same facet")
	}
}

// isMalformedAmbiguousUsage says whether an ambiguous-usage fact is not a code USAGE that has a callee and no ref.
func isMalformedAmbiguousUsage(e *observation.EvidenceEdge) bool {
	if e.Properties["ref"] != "" || e.Properties["callee"] == "" {
		return true
	}

	return e.NativeKind != "USAGE"
}

// isAmbiguousUsageWithRef says whether a USAGE with a ref became an ambiguous-usage fact.
func isAmbiguousUsageWithRef(e *observation.EvidenceEdge) bool {
	if e.Kind != observation.KindAmbiguousUsage {
		return false
	}

	return e.NativeKind == "USAGE" && e.Properties["ref"] != ""
}

func TestReplayCodeUsageIsTheOnlyAmbiguousUsage(t *testing.T) {
	r := fetchInfra(t)

	usage := r.ofKind(observation.KindAmbiguousUsage)
	if len(usage) != 2 {
		t.Fatalf("ambiguous-usage facts = %d", len(usage))
	}

	for _, e := range usage {
		if isMalformedAmbiguousUsage(e) {
			t.Fatalf("ambiguous-usage = %+v", e)
		}
	}

	for _, e := range r.Edges {
		if isAmbiguousUsageWithRef(e) {
			t.Fatalf("USAGE with a ref became ambiguous-usage: %+v", e)
		}
	}
}

// requireTerraformImport checks an IMPORTS fact of a Terraform file.
func requireTerraformImport(t *testing.T, r *infraResult, e *observation.EvidenceEdge) {
	t.Helper()

	if e.SourceLabel != "Class" || r.Nodes[e.From].FilePath != "terraform/main.tf" {
		t.Errorf("terraform import = %+v", e)
	}
}

// requireKustomizeImport checks an IMPORTS fact of a Kustomize file.
func requireKustomizeImport(t *testing.T, r *infraResult, e *observation.EvidenceEdge) {
	t.Helper()

	if base := r.Nodes[e.From].FilePath; base != "kustomize/base/kustomization.yaml" && base != "kustomize/overlays/prod/kustomization.yaml" {
		t.Errorf("kustomize import from %s", base)
	}

	if e.Properties["via"] != "" {
		t.Errorf("kustomize import has via: %v", e.Properties)
	}
}

func TestReplayImportsAreSplitByViaAndFileType(t *testing.T) {
	r := fetchInfra(t)

	imports := r.where(func(e *observation.EvidenceEdge) bool { return e.NativeKind == "IMPORTS" })
	if len(imports) != 4 {
		t.Fatalf("IMPORTS facts = %d", len(imports))
	}

	for _, e := range imports {
		if e.Kind != observation.KindIncludesInfrastructure {
			t.Errorf("IMPORTS from %s (%s) is %s", r.Nodes[e.From].FilePath, e.Properties, e.Kind)
		}

		if e.Properties["via"] == "terraform" {
			requireTerraformImport(t, r, e)
		} else {
			requireKustomizeImport(t, r, e)
		}
	}
}
