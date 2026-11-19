package views_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/views"
)

const diagramFile = "docs/arch/payments.md"

type block struct {
	section string
	kind    string
	format  string
	// elements are the element IDs, or `id=label` for a node with a label text; arrows are "from>to:label".
	elements []string
	arrows   []string
	ordinal  int
}

// diagramArrowSpec is an arrow of a block, with the keys of its two ends: "section/element".
type diagramArrowSpec struct {
	from, to, label string
	line            int
}

// diagramGraphBuilder collects the fake nodes and arrows of diagram blocks.
type diagramGraphBuilder struct {
	elementIdx map[string]int
	nodes      []testutil.FakeNode
	arrows     []diagramArrowSpec
	lastID     int
}

func (g *diagramGraphBuilder) nextID() string {
	g.lastID++

	return strconv.Itoa(g.lastID)
}

// addBlock adds the diagram node of a block, its elements and its arrows.
func (g *diagramGraphBuilder) addBlock(b *block) {
	diagram := testutil.DiagramNode(g.nextID(), diagramFile, b.section, b.ordinal, b.kind, b.format)
	g.nodes = append(g.nodes, diagram)

	for _, e := range b.elements {
		g.addElement(b.section, &diagram, e)
	}

	for i, a := range b.arrows {
		g.arrows = append(g.arrows, parseDiagramArrow(b.section, a, 10+i))
	}
}

// addElement adds an element node: "id", or "id=label" for a node with a label text.
func (g *diagramGraphBuilder) addElement(section string, diagram *testutil.FakeNode, spec string) {
	elementID, text, labeled := strings.Cut(spec, "=")

	g.elementIdx[section+"/"+elementID] = len(g.nodes)

	if labeled {
		g.nodes = append(g.nodes, testutil.DiagramElementLabeled(g.nextID(), diagram, elementID, "node", text))

		return
	}

	g.nodes = append(g.nodes, testutil.DiagramElementNode(g.nextID(), diagram, elementID, "node"))
}

// parseDiagramArrow reads "from>to:label"; the label is everything after the first colon.
func parseDiagramArrow(section, spec string, line int) diagramArrowSpec {
	label := ""
	if rest, l, ok := strings.Cut(spec, ":"); ok {
		spec, label = rest, l
	}

	from, to, _ := strings.Cut(spec, ">")

	return diagramArrowSpec{from: section + "/" + from, to: section + "/" + to, label: label, line: line}
}

func (g *diagramGraphBuilder) edges() []testutil.FakeEdge {
	edges := make([]testutil.FakeEdge, 0, len(g.arrows))
	for _, a := range g.arrows {
		edges = append(edges, testutil.DiagramArrowEdge(&g.nodes[g.elementIdx[a.from]], &g.nodes[g.elementIdx[a.to]], a.label, "arrow", a.line))
	}

	return edges
}

// evidenceOf serves the blocks through the fake graph and the CBM adapter.
func evidenceOf(t *testing.T, blocks []block) *views.DiagramEvidence {
	t.Helper()

	builder := &diagramGraphBuilder{elementIdx: map[string]int{}}
	for i := range blocks {
		builder.addBlock(&blocks[i])
	}

	graph := testutil.NewFakeGraph("p", builder.nodes, builder.edges())
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{
		Client: observation.NewFakeClient(graph.Handler, ""), ExecutableVersion: "0.12.1",
	})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: "p"})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"DIAGRAM_ARROW"})
	if err != nil {
		t.Fatalf("FetchEdges: %v", err)
	}

	return views.DiagramEvidenceOf(&res)
}

func standard(ordinal int) block {
	return block{
		section: "Payments-containers", ordinal: ordinal, kind: "flowchart", format: "mermaid",
		elements: []string{"api", "db", "docs"},
		arrows:   []string{"api>db:sql", "api>docs:https://example.test/run?cmd=rm"},
	}
}

// isPaymentsFlowchart says whether the structure is the standard block, read as a Mermaid flowchart.
func isPaymentsFlowchart(s *views.DiagramStructure) bool {
	return s.DiagramKind == "flowchart" && s.Format == "mermaid" && s.Section == "Payments-containers" &&
		strings.HasSuffix(s.QualifiedName, "#diagram-1")
}

// isFirstStandardArrow says whether the arrow is the sql arrow from api to db on line 10.
func isFirstStandardArrow(a *views.DiagramArrow) bool {
	return a.From.ElementID == "api" && a.To.ElementID == "db" && a.Label == "sql" && a.Style == "arrow" && a.Line == 10
}

func TestReadDiagramElementsAndArrows(t *testing.T) {
	read := views.ReadDiagram(diagramFile, views.DiagramSelector{Title: "Payments containers"}, evidenceOf(t, []block{standard(1)}))
	if !read.Available() {
		t.Fatalf("reason = %q", read.Reason)
	}

	s := &read.Structure
	if !isPaymentsFlowchart(s) {
		t.Errorf("structure = %+v", *s)
	}

	ids := make([]string, 0, len(s.Elements))
	for _, e := range s.Elements {
		ids = append(ids, e.ID.ElementID)
	}

	if !slices.Equal(ids, []string{"api", "db", "docs"}) {
		t.Errorf("elements = %v", ids)
	}

	if len(s.Arrows) != 2 || !isFirstStandardArrow(&s.Arrows[0]) {
		t.Errorf("arrows = %+v", s.Arrows)
	}
}

func TestReadDiagramProviderSuppliesNoArrows(t *testing.T) {
	b := standard(1)
	b.arrows = nil

	read := views.ReadDiagram(diagramFile, views.DiagramSelector{}, evidenceOf(t, []block{b}))
	if read.Available() || read.Reason != "structure not available" {
		t.Errorf("read = %+v", read)
	}
}

func TestReadDiagramUnsupportedKindOrFormat(t *testing.T) {
	for _, kv := range [][2]string{{"sequence", "mermaid"}, {"flowchart", "plantuml"}} {
		b := standard(1)
		b.kind, b.format = kv[0], kv[1]

		if read := views.ReadDiagram(diagramFile, views.DiagramSelector{}, evidenceOf(t, []block{b})); read.Reason != views.ReasonStructureNotAvailable {
			t.Errorf("%v: reason = %q", kv, read.Reason)
		}
	}
}

func TestReadDiagramLinkKeepsLabelOnly(t *testing.T) {
	read := views.ReadDiagram(diagramFile, views.DiagramSelector{}, evidenceOf(t, []block{standard(1)}))

	var found bool

	for _, a := range read.Structure.Arrows {
		if a.To.ElementID == "docs" {
			found = a.Label == "https://example.test/run?cmd=rm"
		}
	}

	if !found {
		t.Errorf("the link label is not kept as text: %+v", read.Structure.Arrows)
	}
}

func TestReadDiagramBlockReorderKeepsMappingKeys(t *testing.T) {
	other := block{section: "Other", ordinal: 1, kind: "flowchart", format: "mermaid", elements: []string{"x", "y"}, arrows: []string{"x>y"}}
	selector := views.DiagramSelector{Anchor: "payments-containers"}

	first := standard(2)
	before := views.ReadDiagram(diagramFile, selector, evidenceOf(t, []block{other, first}))

	second := standard(1)
	other.ordinal = 2
	after := views.ReadDiagram(diagramFile, selector, evidenceOf(t, []block{second, other}))

	if !before.Available() || !after.Available() {
		t.Fatalf("reasons = %q, %q", before.Reason, after.Reason)
	}

	if before.Structure.QualifiedName == after.Structure.QualifiedName {
		t.Errorf("the qualified name did not change: %s", before.Structure.QualifiedName)
	}

	if !slices.EqualFunc(before.Structure.Elements, after.Structure.Elements, func(a, b views.DiagramElement) bool { return a.ID == b.ID }) {
		t.Errorf("element keys changed: %+v vs %+v", before.Structure.Elements, after.Structure.Elements)
	}
}

func TestReadDiagramSelectorAmbiguousOrMissing(t *testing.T) {
	other := block{section: "Other", ordinal: 2, kind: "flowchart", format: "mermaid", elements: []string{"x", "y"}, arrows: []string{"x>y"}}
	ev := evidenceOf(t, []block{standard(1), other})

	if read := views.ReadDiagram(diagramFile, views.DiagramSelector{}, ev); read.Reason != views.ReasonSelectorAmbiguous {
		t.Errorf("no selector: reason = %q", read.Reason)
	}

	if read := views.ReadDiagram(diagramFile, views.DiagramSelector{Title: "Nope"}, ev); read.Reason != views.ReasonStructureNotAvailable {
		t.Errorf("missing section: reason = %q", read.Reason)
	}

	if read := views.ReadDiagram("docs/other.md", views.DiagramSelector{}, ev); read.Reason != views.ReasonStructureNotAvailable {
		t.Errorf("other file: reason = %q", read.Reason)
	}
}
