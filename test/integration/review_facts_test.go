package integration_test

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	deploymentpkg "github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// These tests replay the CBM 0.12.1 recordings of test/fixtures/review/cbm-0.12.1 (the baseline is the infra recording,
// the variant is the same repository without the Secret manifest api-secrets) through the adapter, the manifest
// cross-check, the coverage records, and conformance.ReviewFacts. They need no CBM.

const (
	reviewFixtureDir    = "../fixtures/review/cbm-0.12.1"
	reviewBaselineFile  = infraRecordingFile
	reviewVariantFile   = reviewFixtureDir + "/secret-declaration-removed.json"
	reviewRepositoryDir = "../fixtures/infra"
	reviewSecretFile    = "k8s/prod/secret.yaml"
	reviewProdIdentity  = "k8s:production/~/apps/Deployment/payments/api"
	reviewSecretObjKey  = "core/Secret/payments/api-secrets"
	reviewSecretItem    = reviewProdIdentity + "#env:" + reviewSecretObjKey
)

// reviewRun is the replayed state of one recording.
type reviewRun struct {
	evidence *observation.EvidenceQueryResult
	facts    *conformance.ReviewFacts
	profile  *observation.CapabilityProfile
	coverage []observation.PredicateCoverage
	gaps     []observation.CoverageGap
}

func reviewAttribute(path string) []string {
	var out []string

	for env, sources := range map[string][]string{"production": {"k8s/prod/**"}, "staging": {"k8s/staging/**"}} {
		if fsutil.GlobMatcher(sources)(path) {
			out = append(out, env)
		}
	}

	return out
}

// reviewFiles lists the files of the repository of the variant (the removed files are not in it).
func reviewFiles(t *testing.T, removed []string) []string {
	t.Helper()

	var files []string

	err := fs.WalkDir(os.DirFS(reviewRepositoryDir), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() && !slices.Contains(removed, path) {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("list the repository: %v", err)
	}

	return files
}

// reviewFreeze replays the recording through the adapter and freezes its graph.
func reviewFreeze(t *testing.T, recording string) (*observation.CbmProvider, *observation.GraphSnapshotRef) {
	t.Helper()

	client, err := observation.LoadReplayClient(recording)
	if err != nil {
		t.Fatal(err)
	}

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: infraProject, GitRevision: client.HeadSHA()})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	return provider, ref
}

// reviewFetch fetches the edges of every edge type of the frozen graph and annotates them like the pipeline.
func reviewFetch(t *testing.T, provider *observation.CbmProvider, ref *observation.GraphSnapshotRef) (observation.EvidenceQueryResult, *observation.CapabilityProfile) {
	t.Helper()

	kinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, edgeType := range ref.Schema.EdgeTypes {
		kinds = append(kinds, edgeType.Type)
	}

	result, err := provider.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok {
		t.Fatal("no profile for CBM 0.12.1")
	}

	observation.AttributeEnvironments(&result, reviewAttribute)
	observation.MarkPossiblyLossy(result.Edges, profile)
	observation.AnnotateScopedIDs(result.Nodes, func(string) string { return "" })
	observation.DerivePlacement(&result, func(string) string { return "" })

	return result, profile
}

// reviewCrossCheck reads the committed manifests of the environment sources, like the pipeline.
func reviewCrossCheck(t *testing.T, result *observation.EvidenceQueryResult, removed []string) observation.CrossCheckResult {
	t.Helper()

	return observation.CrossCheck(&observation.CrossCheckInput{
		Files: reviewFiles(t, removed), Attribute: reviewAttribute, Evidence: result,
		Read: func(path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(reviewRepositoryDir, filepath.FromSlash(path)))
		},
	})
}

// reviewEnvironmentSources are the environment sources of the infra repository.
func reviewEnvironmentSources() []observation.EnvironmentSources {
	return []observation.EnvironmentSources{
		{ID: "production", Sources: []string{"k8s/prod/**"}}, {ID: "staging", Sources: []string{"k8s/staging/**"}},
	}
}

// reviewGapsOf gives the gaps of the index record and of the cross-check.
func reviewGapsOf(index *observation.IndexRecord, checked *observation.CrossCheckResult) []observation.CoverageGap {
	gaps := observation.IndexGaps(index, reviewEnvironmentSources())

	for i := range checked.Gaps {
		gap := checked.Gaps[i]
		gap.Project = infraProject
		gaps = append(gaps, gap)
	}

	return gaps
}

// reviewUnsupportedSourcesOf gives the paths of the unsupported sources of the environment.
func reviewUnsupportedSourcesOf(checked *observation.CrossCheckResult, environment string) []string {
	var paths []string

	for _, source := range checked.UnsupportedSources {
		if source.Environment == environment {
			paths = append(paths, source.Path)
		}
	}

	return paths
}

// reviewWorkloadsOf gives the workload documents of the environment.
func reviewWorkloadsOf(checked *observation.CrossCheckResult, environment string) []observation.WorkloadDocument {
	var workloads []observation.WorkloadDocument

	for _, workload := range checked.Workloads {
		if workload.Environment == environment {
			workloads = append(workloads, workload)
		}
	}

	return workloads
}

func reviewScopes(checked *observation.CrossCheckResult) []observation.EnvironmentScope {
	scopes := make([]observation.EnvironmentScope, 0, len(checked.Environments))

	for _, id := range []string{"production", "staging"} {
		scopes = append(scopes, observation.EnvironmentScope{
			ID: id, Projects: []string{infraProject}, CrossChecked: true,
			UnsupportedSources: reviewUnsupportedSourcesOf(checked, id), Workloads: reviewWorkloadsOf(checked, id),
		})
	}

	return scopes
}

// reviewBindings are the bindings of the review model: the production workload api is the CALM service api-service.
func reviewBindings() []binding.Binding {
	return []binding.Binding{{
		ID: "bind-api-workload", ArchitectureRef: "api-service",
		Match: []binding.Match{{Kind: binding.MatchWorkload, Environment: "production", ResourceKind: "Deployment", Name: "api", Namespace: "payments"}},
	}}
}

// reviewNewFacts gives the review facts of the evidence, the binding set, and the coverage.
func reviewNewFacts(result *observation.EvidenceQueryResult, profile *observation.CapabilityProfile, coverage []observation.PredicateCoverage) *conformance.ReviewFacts {
	bindings := reviewBindings()

	observed := make([]observation.ObservedNode, 0, len(result.Nodes))
	for id := range result.Nodes {
		observed = append(observed, result.Nodes[id])
	}

	set := binding.Resolve(bindings, observed, nil)

	return conformance.NewReviewFacts(&conformance.ReviewFactsInput{
		Evidence: result, BindingSet: &set, Profile: profile, Coverage: coverage, Bindings: bindings,
		Model:           &intent.ArchitectureModel{Nodes: []intent.NodeDeclaration{{ID: "api-service", NodeType: "service"}}},
		ProviderVersion: "0.12.1", AdapterSchema: observation.AdapterSchema,
		Environments: []deploymentpkg.Environment{{ID: "production"}, {ID: "staging"}},
	})
}

func replayReview(t *testing.T, recording string, removed []string) *reviewRun {
	t.Helper()

	provider, ref := reviewFreeze(t, recording)
	result, profile := reviewFetch(t, provider, ref)
	checked := reviewCrossCheck(t, &result, removed)

	index := replayInfra(t, recording).index.Record(infraProject, "gen", "rev")
	gaps := reviewGapsOf(&index, &checked)

	caps := provider.Capabilities(t.Context())
	coverage := observation.ComputePredicateCoverage(&observation.PredicateInput{
		Families: caps.Families, Projects: []observation.ProjectScope{{Name: infraProject, Index: &index}},
		Environments: reviewScopes(&checked), Gaps: gaps,
	})

	return &reviewRun{evidence: &result, facts: reviewNewFacts(&result, profile, coverage), coverage: coverage, gaps: gaps, profile: profile}
}

func (r *reviewRun) edges(kind observation.CanonicalEdgeKind, keep func(*observation.EvidenceEdge) bool) []*observation.EvidenceEdge {
	var out []*observation.EvidenceEdge

	for _, edge := range r.evidence.Edges {
		if edge.Kind != kind {
			continue
		}

		if keep == nil || keep(edge) {
			out = append(out, edge)
		}
	}

	return out
}

func (r *reviewRun) record(predicate, environment string) *observation.PredicateCoverage {
	for i := range r.coverage {
		if r.coverage[i].Predicate == predicate && r.coverage[i].Scope.Environment == environment {
			return &r.coverage[i]
		}
	}

	return nil
}

func (r *reviewRun) targetGaps(target string) []observation.CoverageGap {
	var out []observation.CoverageGap

	for i := range r.gaps {
		if r.gaps[i].Reason == observation.LossTargetNotDeclared && r.gaps[i].Target == target {
			out = append(out, r.gaps[i])
		}
	}

	return out
}

func reviewItems(t *testing.T, facts impact.FactSource, sel *impact.Selector) impact.Match {
	t.Helper()

	match, err := facts.Items(t.Context(), *sel)
	if err != nil {
		t.Fatalf("Items(%s): %v", sel.Kind, err)
	}

	return match
}

func reviewIdentities(match *impact.Match) []string {
	out := make([]string, 0, len(match.Items))
	for i := range match.Items {
		out = append(out, match.Items[i].Identity)
	}

	return out
}

// reviewMergedGaps gives the merged-occurrences gaps of the production workload Deployment/api.
func (r *reviewRun) reviewMergedGaps() []observation.CoverageGap {
	var lost []observation.CoverageGap

	for i := range r.gaps {
		gap := &r.gaps[i]
		if gap.Reason != observation.LossMergedOccurrences {
			continue
		}

		if gap.Workload == "Deployment/api" && gap.Environment == "production" {
			lost = append(lost, *gap)
		}
	}

	return lost
}

// reviewRequireMergedImage checks that the production api image fact is the merged one of the container sidecar, and
// that it is marked as lossy.
func reviewRequireMergedImage(t *testing.T, baseline *reviewRun) {
	t.Helper()

	images := baseline.edges(observation.KindUsesImage, func(e *observation.EvidenceEdge) bool {
		return e.Environment == "production" && strings.HasSuffix(e.Properties["image"], "payments/api:1.3.0")
	})
	if len(images) != 1 {
		t.Fatalf("production api image facts = %v", images)
	}

	if images[0].Properties["container"] != "sidecar" {
		t.Fatalf("production api image facts = %v", images)
	}

	// The edge carries no sign of the loss: the profile marks the family, the cross-check names the container.
	if !images[0].PossiblyLossy || !slices.Equal(images[0].LossModes, []string{observation.LossMergedOccurrences}) {
		t.Errorf("image fact: lossy %v, loss modes %v", images[0].PossiblyLossy, images[0].LossModes)
	}
}

// reviewRequireMergedGaps checks that the cross-check names the container that the merge lost, and that the coverage
// of the family is partial.
func reviewRequireMergedGaps(t *testing.T, baseline *reviewRun) {
	t.Helper()

	lost := baseline.reviewMergedGaps()
	if len(lost) != 1 {
		t.Fatalf("merged-occurrences gaps = %+v", lost)
	}

	if lost[0].Container != "app" {
		t.Fatalf("merged-occurrences gaps = %+v", lost)
	}

	rec := baseline.record(observation.FamilyContainerOccurrenceVersions, "production")
	if rec == nil {
		t.Fatal("container-occurrence-versions in production = <nil>")
	}

	if rec.Status != observation.CoveragePartial || !rec.HasLossMode(observation.LossMergedOccurrences) {
		t.Errorf("container-occurrence-versions in production = %+v", rec)
	}
}

// reviewReference is the reference edge that a declared target gives: the mode and the kind of the target.
type reviewReference struct {
	kind       observation.CanonicalEdgeKind
	mode       string
	targetKind string
}

// reviewDeclaredReferences are the references of the baseline whose targets are declared.
func reviewDeclaredReferences() []reviewReference {
	return []reviewReference{
		{observation.KindReferencesSecret, "env", "Secret"},
		{observation.KindReferencesConfiguration, "envFrom", "ConfigMap"},
		{observation.KindReferencesStorage, "volume", "PersistentVolumeClaim"},
		{observation.KindUsesRuntimeIdentity, "serviceAccountName", ""},
		{observation.KindReferencesCertificateSecret, "tls", ""},
		{observation.KindRouteAttachesToGateway, "parentRef", ""},
		{observation.KindBindingRefersToRole, "roleRef", ""},
		{observation.KindBindingAppliesToPrincipal, "subject", ""},
	}
}

func reviewRequireReferenceEdges(t *testing.T, baseline *reviewRun) {
	t.Helper()

	for _, want := range reviewDeclaredReferences() {
		edges := baseline.edges(want.kind, nil)
		if len(edges) != 1 {
			t.Errorf("%s: %v", want.kind, edges)

			continue
		}

		got := reviewReference{kind: want.kind, mode: edges[0].Facets["referenceMode"], targetKind: edges[0].Facets["targetKind"]}
		if got != want {
			t.Errorf("%s: %v", want.kind, edges)
		}
	}
}

// reviewRequireUndeclaredTargetGaps checks that the reference to an undeclared target has no edge and is a
// target-not-declared gap of the cross-check.
func reviewRequireUndeclaredTargetGaps(t *testing.T, baseline *reviewRun) {
	t.Helper()

	for target, kind := range map[string]string{
		"missing-secret": "Secret", "missing-config": "ConfigMap", "missing-pvc": "PersistentVolumeClaim",
		"ghost-sa": "ServiceAccount", "missing-tls": "Secret",
	} {
		gaps := baseline.targetGaps(target)
		if len(gaps) == 0 {
			t.Errorf("target-not-declared gaps of %s = %+v", target, gaps)

			continue
		}

		if gaps[0].TargetKind != kind || gaps[0].ReferenceMode == "" {
			t.Errorf("target-not-declared gaps of %s = %+v", target, gaps)
		}
	}
}

// reviewRequireTerraformRefs checks that Terraform USAGE edges are infrastructure dependencies of the family terraform
// with the native ref.
func reviewRequireTerraformRefs(t *testing.T, baseline *reviewRun) {
	t.Helper()

	refs := map[string]bool{}

	for _, edge := range baseline.edges(observation.KindInfrastructureDependency, func(e *observation.EvidenceEdge) bool { return e.Facets["family"] == "terraform" }) {
		refs[edge.Facets["terraformRef"]] = true
	}

	for _, ref := range []string{"var", "module", "output", "resource"} {
		if !refs[ref] {
			t.Errorf("no terraform fact with ref %s", ref)
		}
	}
}

func reviewRequireProfileLossModes(t *testing.T, baseline *reviewRun) {
	t.Helper()

	for _, family := range []string{"references-secret", "references-configuration", "references-storage", "uses-runtime-identity"} {
		state, ok := baseline.profile.Family(family)
		if !ok {
			t.Errorf("profile family %s = %+v", family, state)

			continue
		}

		if !slices.Contains(state.LossModes, observation.LossTargetNotDeclared) {
			t.Errorf("profile family %s = %+v", family, state)
		}
	}
}

// reviewSecretEdges gives the references to the Secret api-secrets.
func (r *reviewRun) reviewSecretEdges() []*observation.EvidenceEdge {
	return r.edges(observation.KindReferencesSecret, func(e *observation.EvidenceEdge) bool {
		return r.evidence.Nodes[e.To].Properties["obj_key"] == reviewSecretObjKey
	})
}

func reviewRequireRemovedDeclaration(t *testing.T, baseline, variant *reviewRun) {
	t.Helper()

	if len(baseline.reviewSecretEdges()) != 1 || len(baseline.targetGaps("api-secrets")) != 0 {
		t.Errorf("baseline: edges %d, gaps %v", len(baseline.reviewSecretEdges()), baseline.targetGaps("api-secrets"))
	}

	gaps := variant.targetGaps("api-secrets")
	if len(variant.reviewSecretEdges()) != 0 || len(gaps) != 1 {
		t.Fatalf("variant: edges %d, gaps %+v", len(variant.reviewSecretEdges()), gaps)
	}

	if gaps[0].Workload != "Deployment/api" || gaps[0].Environment != "production" {
		t.Errorf("variant: edges %d, gaps %+v", len(variant.reviewSecretEdges()), gaps)
	}
}

// reviewRouteNodes counts the Route nodes of the baseline by qualified name.
func reviewRouteNodes(baseline *reviewRun) map[string]int {
	routes := map[string]int{}

	for id := range baseline.evidence.Nodes {
		if node := baseline.evidence.Nodes[id]; node.Label == "Route" {
			routes[node.QualifiedName]++
		}
	}

	return routes
}

// reviewRequireRouteNodes checks the Route nodes: one for the shared host and path, one for the wildcard host.
func reviewRequireRouteNodes(t *testing.T, baseline *reviewRun) {
	t.Helper()

	routes := reviewRouteNodes(baseline)
	want := map[string]int{"__route__ingress__api.example.com/": 1, "__route__ingress__*/": 1}

	if !maps.Equal(routes, want) {
		t.Fatalf("Route nodes = %v", routes)
	}
}

// reviewRequireSharedRoute checks that the one Route node of the shared host and path routes to the Service of each
// environment.
func reviewRequireSharedRoute(t *testing.T, baseline *reviewRun) {
	t.Helper()

	envs := map[string]bool{}

	for _, edge := range baseline.edges(observation.KindRoutesTo, nil) {
		if baseline.evidence.Nodes[edge.From].QualifiedName == "__route__ingress__api.example.com/" {
			envs[edge.Environment] = true
		}
	}

	if !envs["production"] || !envs["staging"] {
		t.Errorf("the shared Route node routes to %v", envs)
	}

	rec := baseline.record(string(observation.KindRoutesTo), "production")
	if rec == nil {
		t.Fatal("routes-to in production = <nil>")
	}

	if !rec.HasLossMode(observation.LossRouteIdentityUnscoped) {
		t.Errorf("routes-to in production = %+v", rec)
	}
}

// reviewIngressItem gives the Ingress item of the match, or nil.
func reviewIngressItem(match *impact.Match) *impact.Item {
	var ingress *impact.Item

	for i := range match.Items {
		if strings.Contains(match.Items[i].Identity, "/Ingress/") {
			ingress = &match.Items[i]
		}
	}

	return ingress
}

// reviewRequireScopedIngress checks that the review item is the scoped Ingress of each environment, with host and path
// as facets.
func reviewRequireScopedIngress(t *testing.T, baseline *reviewRun) {
	t.Helper()

	for env, prefix := range map[string]string{"production": "k8s:production/", "staging": "k8s:staging/"} {
		match := reviewItems(t, baseline.facts, &impact.Selector{Kind: impact.KindPublicEntryPoint, Environment: env})

		ingress := reviewIngressItem(&match)
		if ingress == nil {
			t.Fatalf("%s entry points = %v", env, reviewIdentities(&match))
		}

		if !strings.HasPrefix(ingress.Identity, prefix) || strings.Contains(ingress.Identity, "__route__") {
			t.Fatalf("%s entry points = %v", env, reviewIdentities(&match))
		}

		if !slices.Equal(ingress.Facets["host"], []string{"api.example.com"}) || !slices.Equal(ingress.Facets["path"], []string{"/"}) {
			t.Errorf("%s facets = %v", env, ingress.Facets)
		}
	}
}

// TestReviewFixtureDiscriminators asserts the discriminators and loss modes of design D5 on the replayed recordings.
func TestReviewFixtureDiscriminators(t *testing.T) {
	t.Parallel()

	baseline := replayReview(t, reviewBaselineFile, nil)
	variant := replayReview(t, reviewVariantFile, []string{reviewSecretFile})

	t.Run("O4 merged container occurrences", func(t *testing.T) {
		t.Parallel()

		reviewRequireMergedImage(t, baseline)
		reviewRequireMergedGaps(t, baseline)
	})

	t.Run("O5 reference edges for declared targets only", func(t *testing.T) {
		t.Parallel()

		reviewRequireReferenceEdges(t, baseline)
		reviewRequireUndeclaredTargetGaps(t, baseline)
		reviewRequireTerraformRefs(t, baseline)
		reviewRequireProfileLossModes(t, baseline)
	})

	t.Run("O5 removed declaration leaves the reference without an edge", func(t *testing.T) {
		t.Parallel()

		reviewRequireRemovedDeclaration(t, baseline, variant)
	})

	t.Run("O7 shared Route node and scoped Ingress resources", func(t *testing.T) {
		t.Parallel()

		reviewRequireRouteNodes(t, baseline)
		reviewRequireSharedRoute(t, baseline)
		reviewRequireScopedIngress(t, baseline)
	})
}
