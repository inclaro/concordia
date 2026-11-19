package observation_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// a01Graph has a manifest that uses an image no binding refers to, an edge of
// a native type that the table does not map (twice, in two files), and a
// MENTIONS edge whose property `count` CBM 0.12.1 cannot read with `r.count`.
func a01Graph() *testutil.FakeGraph {
	deployment := testutil.ResourceNode("1", "k8s/prod/deployment.yaml", "apps", "Deployment", "payments", "api")
	image := testutil.ImageNode("2", "registry.example.com/payments/api")
	secret := testutil.ResourceNode("3", "k8s/prod/secret.yaml", "core", "Secret", "payments", "api-secrets")
	section := testutil.InfraNode("4", "Section", "docs.a.intro", "docs/a.md", nil)
	target := testutil.FileNode("5", "docs/b.md")
	module := testutil.InfraNode("6", "Module", "scripts.deploy", "scripts/deploy.sh", nil)
	other := testutil.InfraNode("7", "Module", "scripts.build", "scripts/build.sh", nil)

	edges := []testutil.FakeEdge{
		testutil.NewEdge(&deployment, &image, "USAGE", map[string]string{"ref": "image", "container": "api", "image": "registry.example.com/payments/api:1.2.0", "line": "12"}),
		testutil.NewEdge(&deployment, &secret, "USAGE", map[string]string{"ref": "env"}),
		testutil.NewEdge(&section, &target, "MENTIONS", map[string]string{"syntax": "link", "count": "3", "line": "4", "via": "markdown", "tier": "exact"}),
		testutil.NewEdge(&module, &image, "PROVENANCE", map[string]string{"line": "6", "builder": "kaniko"}),
		testutil.NewEdge(&other, &image, "PROVENANCE", map[string]string{"line": "3"}),
	}

	nodes := []testutil.FakeNode{
		deployment, image, secret, section, target, module, other,
		testutil.FileNode("10", "k8s/prod/deployment.yaml"), testutil.FileNode("11", "scripts/deploy.sh"), testutil.FileNode("12", "scripts/build.sh"),
	}

	return testutil.NewFakeGraph("p", nodes, edges)
}

func fetchAll(t *testing.T, g *testutil.FakeGraph, maxRows int) observation.EvidenceQueryResult {
	t.Helper()

	_, p := setup(g, maxRows)
	ref := freeze(t, p)

	kinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, e := range ref.Schema.EdgeTypes {
		kinds = append(kinds, e.Type)
	}

	res, err := p.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	return res
}

// evFetch fetches the edges of the given kinds from a frozen graph.
func evFetch(t *testing.T, p *observation.CbmProvider, ref *observation.GraphSnapshotRef, kinds ...string) observation.EvidenceQueryResult {
	t.Helper()

	res, err := p.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	return res
}

// evLastEdge gives the last edge of the result that keep accepts, or nil.
func evLastEdge(res *observation.EvidenceQueryResult, keep func(*observation.EvidenceEdge) bool) *observation.EvidenceEdge {
	var found *observation.EvidenceEdge

	for _, e := range res.Edges {
		if keep(e) {
			found = e
		}
	}

	return found
}

// evDiagnostics gives the diagnostics of the result with the given code.
func evDiagnostics(res *observation.EvidenceQueryResult, code string) []diag.Diagnostic {
	var found []diag.Diagnostic

	for i := range res.Diagnostics {
		if res.Diagnostics[i].Code == code {
			found = append(found, res.Diagnostics[i])
		}
	}

	return found
}

func evQueryMatches(g *testutil.FakeGraph, match func(string) bool) bool {
	return slices.ContainsFunc(g.Queries, match)
}

func evIsKanikoFact(f *observation.EvidenceEdge) bool {
	return f.Kind == observation.KindUnsupported && f.NativeKind == "PROVENANCE" && f.Properties["builder"] == "kaniko"
}

func evRequireKanikoFact(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	obsEqual(t, "unsupported facts", len(res.Unsupported), 2)

	first := res.Unsupported[0]
	if !evIsKanikoFact(first) {
		// The order is (nativeKind, from QN, to QN, line, id): scripts.build comes first.
		first = res.Unsupported[1]
	}

	obsRequire(t, evIsKanikoFact(first), fmt.Sprintf("unsupported fact = %+v", first))
	obsRequire(t, first.SourceSpan != nil, fmt.Sprintf("unsupported fact has no source span: %+v", first))
	obsEqual(t, "unsupported fact path", first.SourceSpan.Path, "scripts/deploy.sh")
	obsEqual(t, "unsupported fact line", first.SourceSpan.Line, 6)
	obsEqual(t, "unsupported target", res.Nodes[first.To].QualifiedName, "__image__registry.example.com/payments/api")
}

func evRequireUnmappedProvenance(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	obsEqual(t, "unmapped PROVENANCE", res.UnmappedNativeEdges["PROVENANCE"], 2)
	obsEqual(t, "unmapped native kinds", len(res.UnmappedNativeEdges), 1)

	warnings := evDiagnostics(res, diag.OBS016)
	obsRequire(t, len(warnings) > 0, fmt.Sprintf("diagnostics = %+v", res.Diagnostics))

	warning := warnings[len(warnings)-1]
	obsEqual(t, "warning severity", warning.Severity, diag.SeverityWarning)
	obsContains(t, "warning message", warning.Message, "PROVENANCE")
	obsContains(t, "warning message", warning.Message, "2 edges")
}

func evRequireUnsupportedOutsideEdges(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	for _, e := range res.Edges {
		if e.NativeKind == "PROVENANCE" {
			t.Fatalf("unsupported edge in Edges: %+v", e)
		}
	}
}

// evRequireUnboundImage checks the image endpoint that has no binding: it is
// an unbound entity that keeps its location.
func evRequireUnboundImage(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	wantID := observation.ImageSynthID("registry.example.com/payments/api")
	i := slices.IndexFunc(res.Entities, func(n observation.ObservedNode) bool { return n.ObservedID == wantID })
	obsRequire(t, i >= 0, "the image entity is missing")

	image := res.Entities[i]
	obsEqual(t, "image resolution", image.Resolution, observation.ResolutionUnbound)
	obsEqual(t, "image file path", image.FilePath, "k8s/prod/deployment.yaml")
	obsEqual(t, "image start line", image.StartLine, 12)
}

func evUnknownNativeEdgeAndUnboundImage(t *testing.T) {
	_, p := setup(a01Graph(), 0)
	ref := freeze(t, p)
	generation := ref.GraphGeneration

	res := evFetch(t, p, ref, "USAGE", "MENTIONS")

	// The generation digest is not changed by fetching the unsupported edges.
	obsEqual(t, "generation", ref.GraphGeneration, generation)

	if err := p.VerifyFrozen(t.Context(), ref); err != nil {
		t.Fatalf("the graph generation changed: %v", err)
	}

	evRequireKanikoFact(t, &res)
	evRequireUnmappedProvenance(t, &res)
	evRequireUnsupportedOutsideEdges(t, &res)
	evRequireUnboundImage(t, &res)
}

func evUnmappedEdgesPaging(t *testing.T) {
	g := a01Graph()
	third := testutil.InfraNode("8", "Module", "scripts.test", "scripts/test.sh", nil)
	image := g.Edges[0].To

	g.Nodes = append(g.Nodes, third, testutil.FileNode("13", "scripts/test.sh"))
	g.Edges = append(g.Edges, testutil.NewEdge(&third, image, "PROVENANCE", map[string]string{"line": "9"}))
	g.PageRows = 1

	// Three unmapped rows and a page of two: the part is divided by source file.
	res := fetchAll(t, g, 2)

	obsEqual(t, "unsupported facts", len(res.Unsupported), 3)
	obsEqual(t, "unmapped PROVENANCE", res.UnmappedNativeEdges["PROVENANCE"], 3)
}

func evRowCountMismatch(t *testing.T) {
	g := a01Graph()
	g.NoCursor = true
	g.PageRows = 1

	_, p := setup(g, 0)
	ref := freeze(t, p)

	_, err := p.FetchEdges(t.Context(), ref, nil)
	wantDiag(t, err, "CO-OBS-010", diag.AdapterFailure)
}

func evUnmappedTypesFetchedUnasked(t *testing.T) {
	_, p := setup(smallGraph(), 0)
	ref := freeze(t, p)

	res := evFetch(t, p, ref, "IMPORTS")

	obsEqual(t, "unsupported facts", len(res.Unsupported), 1)
	obsEqual(t, "unsupported native kind", res.Unsupported[0].NativeKind, "HAS_BRANCH")
}

func TestFetchEdgesKeepsUnmappedNativeEdgesAsUnsupportedFacts(t *testing.T) {
	t.Run("A01 an unknown native edge and an unbound image endpoint", evUnknownNativeEdgeAndUnboundImage)
	t.Run("fetches unmapped edges with paging and checks the row count", evUnmappedEdgesPaging)
	t.Run("a row count that does not agree is CO-OBS-010", evRowCountMismatch)
	t.Run("unmapped native types are fetched although no kind asks for them", evUnmappedTypesFetchedUnasked)
}

func TestFetchEdgesReadsPropertiesThatCbmCannotQuery(t *testing.T) {
	g := a01Graph()
	res := fetchAll(t, g, 0)

	mention := evLastEdge(&res, func(e *observation.EvidenceEdge) bool { return e.NativeKind == "MENTIONS" })
	obsRequire(t, mention != nil, "no MENTIONS edge")
	obsEqual(t, "mention kind", mention.Kind, observation.KindMentions)
	obsEqual(t, "mention count", mention.Properties["count"], "3")
	obsEqual(t, "mention line", mention.Properties["line"], "4")

	for _, q := range g.Queries {
		if strings.Contains(q, "r.count") {
			t.Fatalf("query reads r.count: %s", q)
		}
	}

	obsRequire(t, evQueryMatches(g, func(q string) bool { return strings.Contains(q, "properties(r)") }), "no query reads properties(r)")
}

func evReadsTargetKindOutsideUsage(q string) bool {
	return strings.Contains(q, "b.kind") && !strings.Contains(q, "r:USAGE")
}

func TestFetchEdgesReadsTheTargetKindOnlyWhereARuleNeedsIt(t *testing.T) {
	g := a01Graph()
	res := fetchAll(t, g, 0)

	secret := evLastEdge(&res, func(e *observation.EvidenceEdge) bool {
		return e.NativeKind == "USAGE" && e.Properties["ref"] == "env"
	})
	obsRequire(t, secret != nil, "no secret reference")
	obsEqual(t, "secret kind", secret.Kind, observation.KindReferencesSecret)
	obsEqual(t, "secret target kind facet", secret.Facets["targetKind"], "Secret")
	obsEqual(t, "secret target node kind", res.Nodes[secret.To].Kind, "Secret")

	i := slices.IndexFunc(g.Queries, evReadsTargetKindOutsideUsage)
	obsRequire(t, i < 0, "a query that no rule needs reads the target kind")
}

// evSupersedesGraph has two decisions, the newer one superseding the older
// one, and a resource node with an edge that carries the OVERRIDE type.
func evSupersedesGraph() *testutil.FakeGraph {
	older := testutil.InfraNode("1", "ADR", "docs.adr.0001.__adr__", "docs/adr/0001-use-self-hosted-postgres.md", map[string]string{
		"adr_id": "ADR-1", "title": "Use self-hosted Postgres", "status": "superseded", "status_text": "Superseded by [0002](0002-use-managed-postgres.md)",
		"date": "2025-01-10", "superseded_by": "0002-use-managed-postgres.md", "docstring": "do not keep me",
	})
	newer := testutil.InfraNode("2", "ADR", "docs.adr.0002.__adr__", "docs/adr/0002-use-managed-postgres.md", map[string]string{
		"adr_id": "ADR-2", "title": "Use managed Postgres", "status": "accepted", "status_text": "Accepted", "date": "2025-06-01",
	})
	deployment := testutil.ResourceNode("3", "k8s/prod/deployment.yaml", "apps", "Deployment", "payments", "api")

	edges := []testutil.FakeEdge{
		testutil.NewEdge(&newer, &older, "SUPERSEDES", map[string]string{"via": "markdown", "syntax": "supersedes", "tier": "exact", "line": "7", "count": "1"}),
		testutil.NewEdge(&deployment, &deployment, "OVERRIDE", map[string]string{"via": "patch"}),
	}

	return testutil.NewFakeGraph("p", []testutil.FakeNode{older, newer, deployment}, edges)
}

func evRequireSupersededProperties(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	supersedes := res.Edges[1]
	if supersedes.NativeKind != "SUPERSEDES" || supersedes.Kind != observation.KindSupersedes {
		supersedes = res.Edges[0]
	}

	// The direction is new to old.
	obsEqual(t, "supersedes from", res.Nodes[supersedes.From].Properties["adr_id"], "ADR-2")
	obsEqual(t, "supersedes to", res.Nodes[supersedes.To].Properties["adr_id"], "ADR-1")

	to := res.Nodes[supersedes.To]
	obsEqual(t, "superseded_by", to.Properties["superseded_by"], "0002-use-managed-postgres.md")
	obsEqual(t, "status_text", to.Properties["status_text"], "Superseded by [0002](0002-use-managed-postgres.md)")

	_, kept := to.Properties["docstring"]
	obsRequire(t, !kept, fmt.Sprintf("an unrelated property was kept: %v", to.Properties))
}

func evRequireDecisionEntities(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	// The decision entity is identified by its file path, not by adr_id.
	ids := make([]string, 0, len(res.Entities))
	for i := range res.Entities {
		ids = append(ids, res.Entities[i].ObservedID)
	}

	want := []string{
		observation.ADRSynthID("docs/adr/0001-use-self-hosted-postgres.md"),
		observation.ADRSynthID("docs/adr/0002-use-managed-postgres.md"),
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("entities = %v, want %v", ids, want)
	}
}

func TestFetchEdgesKeepsNodePropertiesVerbatim(t *testing.T) {
	res := fetchAll(t, evSupersedesGraph(), 0)

	obsEqual(t, "edges", len(res.Edges), 2)
	evRequireSupersededProperties(t, &res)

	// Resource nodes keep obj_key.
	obsEqual(t, "obj_key", res.Nodes["cbm:p:3"].Properties["obj_key"], "apps/Deployment/payments/api")

	evRequireDecisionEntities(t, &res)
}

func TestFetchEdgesAmbiguousFactsGiveADiagnostic(t *testing.T) {
	deployment := testutil.ResourceNode("1", "k8s/prod/deployment.yaml", "apps", "Deployment", "payments", "api")
	target := testutil.ResourceNode("2", "k8s/prod/secret.yaml", "core", "Secret", "payments", "s")
	edges := []testutil.FakeEdge{
		testutil.NewEdge(&deployment, &target, "USAGE", map[string]string{"ref": "projected"}),
		testutil.NewEdge(&deployment, &target, "DEPENDS_ON", map[string]string{"tool": "x"}),
	}

	res := fetchAll(t, testutil.NewFakeGraph("p", []testutil.FakeNode{deployment, target}, edges), 0)

	for _, e := range res.Edges {
		obsEqual(t, "edge kind", e.Kind, observation.KindAmbiguous)
		obsEqual(t, "edge resolution", e.Resolution, observation.ResolutionAmbiguous)
	}

	ambiguity := evDiagnostics(&res, diag.OBS019)
	obsRequire(t, len(ambiguity) == 2, fmt.Sprintf("diagnostics = %+v", res.Diagnostics))

	messages := ambiguity[0].Message + ambiguity[1].Message
	obsContains(t, "ambiguity messages", messages, "USAGE")
	obsContains(t, "ambiguity messages", messages, "DEPENDS_ON")
}

func evDiagramGraph() *testutil.FakeGraph {
	diagram := testutil.DiagramNode("1", "docs/arch.md", "Containers", 2, "flowchart", "mermaid")
	sequence := testutil.DiagramNode("2", "docs/arch.md", "Containers", 1, "sequence", "mermaid")
	web := testutil.DiagramElementLabeled("3", &diagram, "web", "node", "Web App")
	orders := testutil.DiagramElementNode("4", &diagram, "orders", "node")
	lonely := testutil.DiagramElementLabeled("5", &diagram, "cache", "node", "Cache")
	edges := []testutil.FakeEdge{testutil.DiagramArrowEdge(&web, &orders, "HTTPS", "arrow", 9)}

	return testutil.NewFakeGraph("p", []testutil.FakeNode{diagram, sequence, web, orders, lonely}, edges)
}

func evRequireDiagramBlocks(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	// No arrow ends at a Diagram node: the adapter fetches the blocks itself, with kind and format.
	block, ok := res.Nodes["cbm:p:1"]
	obsRequire(t, ok, fmt.Sprintf("diagram block = %+v", block))
	obsEqual(t, "block label", block.Label, observation.LabelDiagram)
	obsEqual(t, "block diagram_kind", block.Properties["diagram_kind"], "flowchart")
	obsEqual(t, "block format", block.Properties["format"], "mermaid")

	obsEqual(t, "second block diagram_kind", res.Nodes["cbm:p:2"].Properties["diagram_kind"], "sequence")
}

func evRequireElementLabels(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	// The label text is the provider `name`; the node ID is the name of a node without a label.
	obsEqual(t, "label of web", res.Nodes["cbm:p:3"].Properties["name"], "Web App")
	obsEqual(t, "name of orders", res.Nodes["cbm:p:4"].Properties["name"], "orders")

	// An element that no arrow touches is still evidence.
	cache, kept := res.Nodes["cbm:p:5"]
	obsRequire(t, kept, "the element without an arrow is missing")
	obsEqual(t, "cache element_id", cache.Properties["element_id"], "cache")
	obsEqual(t, "cache name", cache.Properties["name"], "Cache")
}

func evRequireNoDiagramNodes(t *testing.T, res *observation.EvidenceQueryResult) {
	t.Helper()

	for id := range res.Nodes {
		label := res.Nodes[id].Label
		if label == observation.LabelDiagram || label == observation.LabelDiagramElement {
			t.Fatalf("node %s has the label %s", id, label)
		}
	}
}

func TestFetchEdgesKeepsDiagramBlocksAndElementLabels(t *testing.T) {
	res := fetchAll(t, evDiagramGraph(), 0)

	evRequireDiagramBlocks(t, &res)
	evRequireElementLabels(t, &res)

	// A graph without diagram elements fetches no diagram nodes.
	plain := fetchAll(t, a01Graph(), 0)
	evRequireNoDiagramNodes(t, &plain)
}

// evLoneDecisionGraph has a decision node that no edge touches, next to a
// file node with a DEFINES edge.
func evLoneDecisionGraph() (graph *testutil.FakeGraph, props map[string]string) {
	lone := testutil.ADRNode("1", "docs/adr/0009-lone.md", map[string]string{
		"adr_id": "ADR-9", "title": "ADR-0009: Lone", "status": "superseded", "status_text": "Superseded",
		"date": "2026-01-01", "deciders": "Ada, Bob", "superseded_by": "0010-next.md",
	})
	other := testutil.ADRNode("2", "docs/adr/0010-next.md", map[string]string{"adr_id": "ADR-10", "status": "accepted"})
	file := testutil.FileNode("3", "docs/adr/0009-lone.md")
	edge := testutil.NewEdge(&file, &file, "DEFINES", nil)

	return testutil.NewFakeGraph("p", []testutil.FakeNode{lone, other, file}, []testutil.FakeEdge{edge}), lone.Props
}

func TestFetchEdgesReadsDecisionNodesThatNoEdgeTouches(t *testing.T) {
	g, props := evLoneDecisionGraph()
	_, p := setup(g, 0)
	ref := freeze(t, p)

	without := evFetch(t, p, ref, "DEFINES")
	_, found := without.Nodes["cbm:p:1"]
	obsRequire(t, !found, fmt.Sprintf("a fetch that does not ask for SUPERSEDES collected a decision node: %v", without.Nodes))

	with := evFetch(t, p, ref, "DEFINES", "SUPERSEDES")

	node, found := with.Nodes["cbm:p:1"]
	obsRequire(t, found, "the decision node is missing")
	obsEqual(t, "decision label", node.Label, "ADR")
	obsEqual(t, "decision file path", node.FilePath, "docs/adr/0009-lone.md")
	obsEqual(t, "decision start line", node.StartLine, 1)

	for key, want := range props {
		obsEqual(t, "property "+key, node.Properties[key], want)
	}

	_, found = with.Nodes["cbm:p:2"]
	obsRequire(t, found, "the second decision node is missing")
}
