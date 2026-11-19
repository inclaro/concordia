package integration_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/observation/cbmprobe"
	"github.com/inclaro/concordia/internal/workspace"
)

// These tests replay the infra recording and check the identity rules of the
// observed entities (tasks 6.2 and 7.1 to 7.4): scoped resource identity,
// competing declarations, unscoped route nodes, decision properties, diagram
// element identity, and lossy marking of merged occurrences.

// identityEnvironments declare production over the prod and override
// manifests, staging over the staging manifests, and a shared Kustomize base.
func identityEnvironments() *workspace.Workspace {
	return &workspace.Workspace{Environments: []workspace.Environment{
		{ID: "production", Cluster: "prod-eu", Sources: []string{"k8s/prod/**", "k8s/overrides/**", "kustomize/base/**", "kustomize/overlays/prod/**", "charts/**"}},
		{ID: "staging", Cluster: "stg-eu", Sources: []string{"k8s/staging/**", "kustomize/base/**"}},
	}}
}

func clusterOfIdentityEnvironment(env string) string {
	for _, e := range identityEnvironments().Environments {
		if e.ID == env {
			return e.Cluster
		}
	}

	return ""
}

// observedID is the observed ID of a recorded node.
func observedID(n *cbmprobe.Node) string {
	return "cbm:" + infraProject + ":" + n.ID
}

// toObservedNode converts a recorded node with all its properties kept verbatim.
func toObservedNode(n *cbmprobe.Node) observation.ObservedNode {
	props := make(map[string]string, len(n.Props))
	for name := range n.Props {
		props[name] = n.Prop(name)
	}

	return observation.ObservedNode{
		ObservedID: observedID(n), Label: n.Label(), QualifiedName: observation.StripProjectPrefix(infraProject, n.QualifiedName),
		FilePath: n.FilePath, StartLine: n.StartLine, Properties: props,
	}
}

// identityEdgeKind gives the canonical kind of the recorded edges that these tests need; other edges give false.
func identityEdgeKind(e *cbmprobe.Edge) (observation.CanonicalEdgeKind, bool) {
	switch e.Type {
	case infraEdgeUsage:
		return observation.KindUsesImage, e.Prop("ref") == "image"
	case infraEdgeMaps:
		return identityMapsEdgeKind(e)
	}

	return "", false
}

func identityMapsEdgeKind(e *cbmprobe.Edge) (observation.CanonicalEdgeKind, bool) {
	if e.From.Label() == "Route" {
		return observation.KindRoutesTo, true
	}

	return observation.KindSelectsWorkload, e.Prop("kind") == "selector"
}

// toEvidenceEdge converts a recorded edge of the given kind with all its properties kept verbatim.
func toEvidenceEdge(e *cbmprobe.Edge, kind observation.CanonicalEdgeKind) *observation.EvidenceEdge {
	props := map[string]string{}
	for name := range e.Props {
		props[name] = e.Prop(name)
	}

	edge := &observation.EvidenceEdge{
		ID: "e" + observedID(e.From) + ">" + observedID(e.To) + ":" + string(kind), From: observedID(e.From), To: observedID(e.To),
		Kind: kind, NativeKind: e.Type, Properties: props, Occurrence: props["container"],
	}
	if e.From.FilePath != "" {
		edge.SourceSpan = &observation.SourceSpan{Path: e.From.FilePath}
	}

	return edge
}

// toEvidence converts the recorded graph to evidence with the edge kinds that these tests need.
func (s *infraSnapshot) toEvidence() *observation.EvidenceQueryResult {
	res := &observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{}}

	for _, n := range s.graph.Nodes {
		res.Nodes[observedID(n)] = toObservedNode(n)
	}

	for _, e := range s.graph.Edges {
		if kind, ok := identityEdgeKind(e); ok {
			res.Edges = append(res.Edges, toEvidenceEdge(e, kind))
		}
	}

	return res
}

func (s *infraSnapshot) attributed() *observation.EvidenceQueryResult {
	res := s.toEvidence()
	observation.AttributeEnvironments(res, identityEnvironments().Attribute)

	return res
}

// identityDeclares reports whether the node is the declaration of the object key in the file.
func identityDeclares(n *observation.ObservedNode, file, objKey string) bool {
	key, ok := observation.ObjKeyOf(n)
	if !ok {
		return false
	}

	return n.FilePath == file && key == objKey
}

func nodeByFileAndKey(t *testing.T, res *observation.EvidenceQueryResult, file, objKey string) []*observation.ObservedNode {
	t.Helper()

	var out []*observation.ObservedNode

	for id := range res.Nodes {
		if n := res.Nodes[id]; identityDeclares(&n, file, objKey) {
			out = append(out, &n)
		}
	}

	if len(out) == 0 {
		t.Fatalf("no node %s in %s", objKey, file)
	}

	slices.SortFunc(out, func(a, b *observation.ObservedNode) int { return strings.Compare(a.ObservedID, b.ObservedID) })

	return out
}

// identityScopedID gives the scoped ID of the declaration of the object key in the file, in the environment.
func identityScopedID(t *testing.T, res *observation.EvidenceQueryResult, c *identityScopedIDCase) string {
	t.Helper()

	file, objKey, env := c.file, c.objKey, c.env

	for _, n := range nodeByFileAndKey(t, res, file, objKey) {
		if n.Environment != env {
			continue
		}

		scoped, ok, err := observation.ScopedIDOf(n, clusterOfIdentityEnvironment(n.Environment))
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", objKey, ok, err)
		}

		return scoped.String()
	}

	t.Fatalf("no %s instance of %s in %s", env, objKey, file)

	return ""
}

// identityScopedIDCase is the scoped ID that a recorded declaration must get in an environment.
type identityScopedIDCase struct {
	name, file, objKey, env, want string
}

func identityScopedIDCases() []identityScopedIDCase {
	return []identityScopedIDCase{
		// Namespaced, with the environment and cluster from the declared sources.
		{"Deployment", infraProdDeploy, "apps/Deployment/payments/api", "production", "k8s:production/prod-eu/apps/Deployment/payments/api"},
		{"core group", "k8s/prod/secret.yaml", "core/Secret/payments/api-secrets", "production", "k8s:production/prod-eu/core/Secret/payments/api-secrets"},
		// Cluster-scoped: the namespace component is empty (O2 `_`), also for the ClusterRole.
		{"Namespace", "k8s/prod/namespace.yaml", "core/Namespace/_/payments", "production", "k8s:production/prod-eu/core/Namespace//payments"},
		{"ClusterRole", "k8s/prod/namespace.yaml", "rbac.authorization.k8s.io/ClusterRole/_/payments-reader", "production", "k8s:production/prod-eu/rbac.authorization.k8s.io/ClusterRole//payments-reader"},
		// `?` stays unresolved in the Helm template and the Kustomize base: no `default`, no neighbour namespace.
		{"Helm", "charts/api/templates/deployment.yaml", "apps/Deployment/?/<.Release.Name>-api", "production", "k8s:production/prod-eu/apps/Deployment/~/<.Release.Name>-api"},
		{"Kustomize base production", "kustomize/base/deployment.yaml", "apps/Deployment/?/gateway", "production", "k8s:production/prod-eu/apps/Deployment/~/gateway"},
		{"Kustomize base staging", "kustomize/base/deployment.yaml", "apps/Deployment/?/gateway", "staging", "k8s:staging/stg-eu/apps/Deployment/~/gateway"},
	}
}

func TestIdentityScopedIDsOfRecordedResources(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	res := snap.attributed()

	for _, c := range identityScopedIDCases() {
		requireProp(t, c.name, identityScopedID(t, res, &c), c.want)
	}

	assertKustomizationsHaveNoObjKey(t, res)

	// The staging manifests are outside production.
	for _, n := range nodeByFileAndKey(t, res, "k8s/staging/worker.yaml", "apps/Deployment/payments/worker") {
		requireProp(t, "worker environment", n.Environment, "staging")
	}

	assertComposeFilesUnresolved(t, res)
}

// assertKustomizationsHaveNoObjKey checks that a Kustomization document has no obj_key, so it has no resource identity.
func assertKustomizationsHaveNoObjKey(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	for id := range res.Nodes {
		n := res.Nodes[id]
		if _, ok := observation.ObjKeyOf(&n); ok && strings.HasSuffix(n.QualifiedName, ".Kustomization") {
			t.Errorf("%s has an obj_key", id)
		}
	}
}

// assertComposeFilesUnresolved checks that every file that no environment owns is unresolved, never a default.
func assertComposeFilesUnresolved(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	for id := range res.Nodes {
		if n := res.Nodes[id]; n.FilePath == "compose.yaml" && n.Label == "Resource" {
			requireProp(t, "compose environment", n.Environment, observation.EnvironmentUnresolved)
		}
	}
}

// identityConflictNamed gives the last conflict of the resource name, or nil.
func identityConflictNamed(conflicts []observation.Conflict, name string) *observation.Conflict {
	var found *observation.Conflict

	for i := range conflicts {
		if conflicts[i].ID.Name == name {
			found = &conflicts[i]
		}
	}

	return found
}

func identityConflictPaths(conflict *observation.Conflict) []string {
	paths := make([]string, 0, len(conflict.Locations))
	for _, loc := range conflict.Locations {
		paths = append(paths, loc.Path)
	}

	return paths
}

// assertReportsConflict checks the certain conflict of the two files that declare the same obj_key in one environment.
func assertReportsConflict(t *testing.T, conflicts []observation.Conflict) {
	t.Helper()

	conflict := identityConflictNamed(conflicts, "reports")
	if conflict == nil {
		t.Fatalf("no conflict for the competing declarations: %+v", conflicts)
	}

	if !conflict.Certain || conflict.ID.String() != "k8s:production/prod-eu/apps/Deployment/payments/reports" {
		t.Errorf("conflict %+v", conflict)
	}

	if paths := identityConflictPaths(conflict); !slices.Equal(paths, []string{"k8s/overrides/reports.yaml", "k8s/prod/reports.yaml"}) {
		t.Errorf("locations = %v, want both files", paths)
	}

	// No other resource of the recording competes with certainty (the Kustomize base and its patch have no namespace).
	for i := range conflicts {
		if c := &conflicts[i]; c.ID.Name != "reports" && c.Certain {
			t.Errorf("unexpected certain conflict: %+v", *c)
		}
	}
}

// assertNoConflictAcrossEnvironments declares the overrides in staging: the same obj_key in two environments is no conflict.
func assertNoConflictAcrossEnvironments(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	split := &workspace.Workspace{Environments: []workspace.Environment{
		{ID: "production", Sources: []string{"k8s/prod/**"}}, {ID: "staging", Sources: []string{"k8s/overrides/**", "k8s/staging/**"}},
	}}

	res := snap.toEvidence()
	observation.AttributeEnvironments(res, split.Attribute)

	decls, err := observation.DeclarationsOf(res.Nodes, nil)
	if err != nil {
		t.Fatal(err)
	}

	crossEnvironment := observation.Conflicts(observation.GroupDeclarations(decls))
	for i := range crossEnvironment {
		if crossEnvironment[i].ID.Name == "reports" {
			t.Errorf("the same name in two environments is a conflict: %+v", crossEnvironment[i])
		}
	}
}

func TestIdentityA08TwoFilesDeclareOneObjKey(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)

	// The recording has the same obj_key in two files (O2).
	requireCount(t, "nodes of the competing obj_key", len(snap.resourcesByKey("apps/Deployment/payments/reports")), 2)

	res := snap.attributed()

	decls, err := observation.DeclarationsOf(res.Nodes, clusterOfIdentityEnvironment)
	if err != nil {
		t.Fatal(err)
	}

	assertReportsConflict(t, observation.Conflicts(observation.GroupDeclarations(decls)))

	// The same obj_key in two environments is no conflict: declare the overrides in staging.
	assertNoConflictAcrossEnvironments(t, snap)
}

const identitySharedRouteName = "__route__ingress__api.example.com/"

// identitySharedRouteID gives the ID of the Route node of the shared host, or the empty ID.
func identitySharedRouteID(res *observation.EvidenceQueryResult) string {
	routeID := ""

	for id := range res.Nodes {
		if res.Nodes[id].QualifiedName == identitySharedRouteName {
			routeID = id
		}
	}

	return routeID
}

// identityRoutesToFrom lists the routes-to facts that start at the Route node.
func identityRoutesToFrom(res *observation.EvidenceQueryResult, routeID string) []*observation.EvidenceEdge {
	var out []*observation.EvidenceEdge

	for _, e := range res.Edges {
		if e.Kind == observation.KindRoutesTo && e.From == routeID {
			out = append(out, e)
		}
	}

	return out
}

func identityRouteNodeCount(res *observation.EvidenceQueryResult) int {
	routes := 0

	for id := range res.Nodes {
		if n := res.Nodes[id]; n.Label == "Route" && n.QualifiedName == identitySharedRouteName {
			routes++
		}
	}

	return routes
}

func TestIdentityO7SameHostAndPathInTwoEnvironments(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	res := snap.attributed()

	routeID := identitySharedRouteID(res)
	if routeID == "" {
		t.Fatal("no Route node for api.example.com/")
	}

	route := res.Nodes[routeID]
	if route.Environment != "" || !observation.IsUnscopedNode(route.Label, route.QualifiedName) {
		t.Errorf("the Route node must be unscoped: %+v", route)
	}

	// One Route node, two routes-to facts, each with the environment of its Service endpoint.
	facts := identityRoutesToFrom(res, routeID)
	envs := map[string]string{}

	for _, e := range facts {
		envs[res.Nodes[e.To].Properties["namespace"]] = e.Environment
	}

	if !maps.Equal(envs, map[string]string{"payments": "production", "payments-staging": "staging"}) {
		t.Fatalf("routes-to environments = %v", envs)
	}

	// An obligation for production uses only the production fact.
	production := slices.DeleteFunc(slices.Clone(facts), func(e *observation.EvidenceEdge) bool { return e.Environment != "production" })

	requireCount(t, "production routes-to facts of the shared host", len(production), 1)
	requireCount(t, "Route nodes of the shared host", identityRouteNodeCount(res), 1)
}

func TestIdentityO12DecisionPropertiesStayVerbatim(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	res := snap.toEvidence()

	first := identityDecisionNamed(t, res, "ADR-1")
	if first == nil {
		t.Fatal("no ADR-1")
	}

	requireProp(t, "path", first.Path, "docs/adr/0001-use-self-hosted-postgres.md")
	requireProp(t, "adr_id", first.AdrID, "ADR-1")
	requireProp(t, "superseded_by link text", first.SupersededBy, "0002-use-managed-postgres.md")
	requireProp(t, "identity", first.SynthID(), "synth:adr:docs/adr/0001-use-self-hosted-postgres.md")
}

// identityDecisionNamed gives the last decision of the ADR nodes with the ADR ID, or nil.
func identityDecisionNamed(t *testing.T, res *observation.EvidenceQueryResult, adrID string) *observation.Decision {
	t.Helper()

	var found *observation.Decision

	for id := range res.Nodes {
		n := res.Nodes[id]
		if n.Label != "ADR" {
			continue
		}

		d, err := observation.DecisionOfNode(&n)
		if err != nil {
			t.Fatal(err)
		}

		if d.AdrID == adrID {
			found = &d
		}
	}

	return found
}

func identityOfDiagramNode(t *testing.T, n *observation.ObservedNode) observation.DiagramElementID {
	t.Helper()

	got, err := observation.DiagramElementOfNode(n)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// identityDiagramElements gives the identity of each diagram element by its element ID, and the identity of the same
// element after a reorder of the block: another ordinal in the qualified name.
func identityDiagramElements(t *testing.T, res *observation.EvidenceQueryResult) (elements, reordered map[string]observation.DiagramElementID) {
	t.Helper()

	elements = map[string]observation.DiagramElementID{}
	reordered = map[string]observation.DiagramElementID{}

	for id := range res.Nodes {
		n := res.Nodes[id]
		if n.Label != "DiagramElement" {
			continue
		}

		elements[n.Properties["element_id"]] = identityOfDiagramNode(t, &n)

		moved := n
		moved.QualifiedName = strings.Replace(n.QualifiedName, "#diagram-1.", "#diagram-4.", 1)
		reordered[n.Properties["element_id"]] = identityOfDiagramNode(t, &moved)
	}

	return elements, reordered
}

func identityDiagramChanged(a, b observation.DiagramElementID) bool {
	if a != b {
		return true
	}

	return a.SynthID() != b.SynthID()
}

func identityDiagramInContextDiagram(got observation.DiagramElementID, elementID string) bool {
	if got.File != "docs/architecture.md" || got.Section != "Context-diagram" {
		return false
	}

	return got.ElementID == elementID
}

func TestIdentityDiagramElementKeepsIdentityWhenTheBlockIsReordered(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	res := snap.toEvidence()

	elements, reordered := identityDiagramElements(t, res)

	requireCount(t, "diagram elements", len(elements), 4)

	for elementID, got := range elements {
		if identityDiagramChanged(got, reordered[elementID]) {
			t.Errorf("%s: identity changed by a reorder: %+v, %+v", elementID, got, reordered[elementID])
		}

		if !identityDiagramInContextDiagram(got, elementID) {
			t.Errorf("%s: %+v", elementID, got)
		}
	}
}

// identityIsMutableSidecarOccurrence reports whether the occurrence is the sidecar image of the repository at its
// tag, marked lossy.
func identityIsMutableSidecarOccurrence(occ *observation.ContainerOccurrence) bool {
	if occ.Container != "sidecar" || occ.Image.Tag != "1.3.0" {
		return false
	}

	if occ.Image.Repository != "registry.example.com/payments/api" || !occ.PossiblyLossy {
		return false
	}

	return occ.Image.Mutable()
}

// identityIsStagingDigestOccurrence reports whether the occurrence keeps a digest, has no tag, and is in staging.
func identityIsStagingDigestOccurrence(worker *observation.ContainerOccurrence) bool {
	if worker.Image.Tag != "" || !strings.HasPrefix(worker.Image.Digest, "sha256:") {
		return false
	}

	return !worker.Image.Mutable() && worker.Environment == "staging"
}

// identitySidecarImageEdge gives the last uses-image fact of the sidecar container, or nil.
func identitySidecarImageEdge(res *observation.EvidenceQueryResult) *observation.EvidenceEdge {
	var merged *observation.EvidenceEdge

	for _, e := range res.Edges {
		if e.Kind == observation.KindUsesImage && e.Properties["container"] == "sidecar" {
			merged = e
		}
	}

	return merged
}

// assertMergedSidecarOccurrence checks the occurrence that the marked merged fact gives.
func assertMergedSidecarOccurrence(t *testing.T, merged *observation.EvidenceEdge) {
	t.Helper()

	occ, ok, err := observation.ContainerOccurrenceOf(merged)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}

	// The tag comes from the edge: the Image node of the repository has none.
	if !identityIsMutableSidecarOccurrence(&occ) {
		t.Errorf("occurrence %+v", occ)
	}

	if occ.Environment != "production" {
		t.Errorf("occurrence environment %q", occ.Environment)
	}
}

// assertDigestOccurrencesOfStagingWorker checks the digest reference of the staging worker.
func assertDigestOccurrencesOfStagingWorker(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	for _, e := range res.Edges {
		if e.Kind != observation.KindUsesImage || !strings.HasPrefix(e.Properties["image"], "registry.example.com/payments/worker@") {
			continue
		}

		worker, _, err := observation.ContainerOccurrenceOf(e)
		if err != nil || !identityIsStagingDigestOccurrence(&worker) {
			t.Errorf("digest occurrence %+v %v", worker, err)
		}
	}
}

func TestIdentityO4MergedImageFactIsPossiblyLossy(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	res := snap.attributed()

	profile, ok := observation.ProfileFor("0.12.1", "concordia-cbm/v2")
	if !ok {
		t.Fatal("no capability profile for CBM 0.12.1")
	}

	merged := identitySidecarImageEdge(res)
	if merged == nil {
		t.Fatal("no merged uses-image fact of the sidecar")
	}

	if got := observation.MarkPossiblyLossy(res.Edges, profile); got == 0 || !merged.PossiblyLossy {
		t.Fatalf("marked %d, merged lossy = %v", got, merged.PossiblyLossy)
	}

	assertMergedSidecarOccurrence(t, merged)

	// The digest reference of the staging worker keeps the digest and has no tag.
	assertDigestOccurrencesOfStagingWorker(t, res)

	// A family that the profile does not mark is not touched.
	for _, e := range res.Edges {
		if e.Kind == observation.KindRoutesTo && e.PossiblyLossy {
			t.Errorf("a routes-to fact is lossy: %+v", e)
		}
	}
}
