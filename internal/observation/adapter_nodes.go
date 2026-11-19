package observation

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

// nativeSupersedes is the native edge type of a decision superseding another one. A fetch that asks for it is a
// decision-governance fetch, which also needs the decision files that no edge touches.
const nativeSupersedes = "SUPERSEDES"

// nodePropertyColumns are the node properties that the adapter keeps
// verbatim on the observed node, by label: the decision metadata (O12), the
// diagram identity, and the object key of a resource (O2).
var nodePropertyColumns = map[string][]string{
	LabelDecision:       {"adr_id", "title", "status", "status_text", "date", "deciders", "superseded_by"},
	LabelDiagram:        {"diagram_kind", "format"},
	LabelDiagramElement: {"element_id", "element_kind"},
	"Resource":          {"obj_key"},
}

// nodeNameLabels are the labels whose `name` column the adapter keeps too. The
// name is not in `properties(n)`. A `DiagramElement` gives the label text of
// the node there (`Orders API` for `api[Orders API]`), and the node ID when the
// author wrote no label (CBM 0.12.1, O12).
var nodeNameLabels = map[string]bool{LabelDiagramElement: true}

// propertyName is the key of the `name` column in the properties of a node.
const propertyName = "name"

// presentLabels gives the labels of the collected nodes.
func (f *edgeFetch) presentLabels() map[string]bool {
	present := map[string]bool{}
	for nodeID := range f.nodes {
		present[f.nodes[nodeID].Label] = true
	}

	return present
}

// fetchDiagramNodes adds all `Diagram` and `DiagramElement` nodes of the graph
// to the collected nodes when the fetch collected a `DiagramElement`. No edge
// of the graph ends at a `Diagram` node, and an element without an arrow ends
// no `DIAGRAM_ARROW`, so the edge queries never collect them; without them
// the `diagram_kind` and `format` of a diagram block, and the elements that no
// arrow touches, are not in the evidence. The row count of each label is
// checked against the count of the schema.
func (f *edgeFetch) fetchDiagramNodes(ctx context.Context) error {
	if !f.presentLabels()[LabelDiagramElement] {
		return nil
	}

	for _, label := range []string{LabelDiagram, LabelDiagramElement} {
		if err := f.fetchLabelNodes(ctx, label); err != nil {
			return err
		}
	}

	return nil
}

// fetchDecisionNodes adds all `ADR` nodes of the graph to the collected nodes. The edge queries collect an ADR
// node only as an endpoint of an edge, so a decision file without a `SUPERSEDES`, `MENTIONS` or
// `REFERENCES_FILE` edge would not be in the evidence, and the committed presence of an indexed decision, and a
// `superseded` decision with no successor, would not be visible (O12). The row count is checked against the
// count of the schema.
func (f *edgeFetch) fetchDecisionNodes(ctx context.Context) error {
	return f.fetchLabelNodes(ctx, LabelDecision)
}

// schemaCount gives the node count of a label in the schema.
func (f *edgeFetch) schemaCount(label string) int {
	count := 0

	for _, l := range f.ref.Schema.NodeLabels {
		if l.Label == label {
			count = int(l.Count)
		}
	}

	return count
}

// fetchLabelNodes adds the nodes of one label that the edge queries did not collect.
func (f *edgeFetch) fetchLabelNodes(ctx context.Context, label string) error {
	expected := f.schemaCount(label)
	if expected == 0 {
		return nil
	}

	res, err := f.query(ctx, "MATCH (n:"+label+") RETURN id(n), labels(n), n.qualified_name, n.file_path, n.start_line", f.p.maxRows)
	if err != nil {
		return err
	}

	if len(res.Rows) != expected {
		return diag.Fail(diag.OBS010, diag.Detail(fmt.Sprintf("%s nodes: got %d rows, count is %d", label, len(res.Rows), expected)))
	}

	for _, row := range res.Rows {
		node := ToObservedNode(f.ref.Project, nodeColumnsAt(row, 0), label)
		if _, collected := f.nodes[node.ObservedID]; !collected {
			f.nodes[node.ObservedID] = node
		}
	}

	return nil
}

// fetchNodeProperties reads the properties of nodePropertyColumns for each
// label that has a node in the collected nodes. The column `properties(n)`
// holds all properties; the row count is checked against `count(n)`.
func (f *edgeFetch) fetchNodeProperties(ctx context.Context) error {
	present := f.presentLabels()

	for _, label := range sortedKeys(nodePropertyColumns) {
		if !present[label] {
			continue
		}

		if err := f.fetchLabelProperties(ctx, label); err != nil {
			return err
		}
	}

	return nil
}

// fetchLabelProperties keeps the properties of the collected nodes of one label.
func (f *edgeFetch) fetchLabelProperties(ctx context.Context, label string) error {
	match := fmt.Sprintf("MATCH (n:%s)", label)

	expected, err := f.countQuery(ctx, match+" RETURN count(n)")
	if err != nil || expected == 0 {
		return err
	}

	returns := "id(n), properties(n)"
	if nodeNameLabels[label] {
		returns += ", n." + propertyName
	}

	res, err := f.query(ctx, match+" RETURN "+returns, f.p.maxRows)
	if err != nil {
		return err
	}

	if len(res.Rows) != expected {
		return diag.Fail(diag.OBS010, diag.Detail(fmt.Sprintf("%s node properties: got %d rows, count is %d",
			label, len(res.Rows), expected)))
	}

	for _, row := range res.Rows {
		if err := f.applyNodeProperties(label, row); err != nil {
			return err
		}
	}

	return nil
}

// nodeNameProperty gives the `name` column of a row of a label that keeps it.
func nodeNameProperty(label string, row []any) string {
	if !nodeNameLabels[label] {
		return ""
	}

	return stringOf(cell(row, 2))
}

// applyNodeProperties keeps the properties of one row on its collected node.
// A row of a node that no query collected is ignored.
func (f *edgeFetch) applyNodeProperties(label string, row []any) error {
	nodeID := observedID(f.ref.Project, stringOf(cell(row, 0)))

	node, ok := f.nodes[nodeID]
	if !ok {
		return nil
	}

	props := map[string]string{}
	if err := readJSONProps(props, nodePropertyColumns[label], cell(row, 1)); err != nil {
		return diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s node: %v", label, err)))
	}

	if name := nodeNameProperty(label, row); name != "" {
		props[propertyName] = name
	}

	if len(props) > 0 {
		node.Properties = props
		f.nodes[nodeID] = node
	}

	return nil
}

// ambiguityGroup is the ambiguous facts of one native type and reason.
type ambiguityGroup struct {
	example *EvidenceEdge
	count   int
}

// unmappedDiagnostics gives one warning for each native type that is kept as
// unsupported evidence (CO-OBS-016).
func unmappedDiagnostics(unmapped map[string]int) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0, len(unmapped))

	for _, kind := range sortedKeys(unmapped) {
		out = append(out, diag.New(diag.OBS016, diag.Params{paramKind: kind, paramCount: unmapped[kind]}))
	}

	return out
}

// groupAmbiguousEdges groups the ambiguous edges by native type and reason.
func groupAmbiguousEdges(edges []*EvidenceEdge) map[string]*ambiguityGroup {
	groups := map[string]*ambiguityGroup{}

	for _, e := range edges {
		if e.Kind != KindAmbiguous {
			continue
		}

		key := e.NativeKind + ": " + e.Facets[facetAmbiguity]
		if groups[key] == nil {
			groups[key] = &ambiguityGroup{example: e}
		}

		groups[key].count++
	}

	return groups
}

// ambiguityDetail describes the reason of a group of ambiguous facts, with the first fact as example.
func ambiguityDetail(example *EvidenceEdge, nodes map[string]ObservedNode) string {
	detail := fmt.Sprintf("%s; for example %s to %s", example.Facets[facetAmbiguity],
		nodes[example.From].QualifiedName, nodes[example.To].QualifiedName)

	if example.SourceSpan != nil {
		detail += " at " + example.SourceSpan.Path + ":" + strconv.Itoa(example.SourceSpan.Line)
	}

	return detail
}

// fetchDiagnostics gives the warnings of a fetch: one for each native type
// that is kept as unsupported evidence (CO-OBS-016), and one for each native
// type and reason of ambiguous facts (CO-OBS-019).
func fetchDiagnostics(unmapped map[string]int, edges []*EvidenceEdge, nodes map[string]ObservedNode) []diag.Diagnostic {
	out := unmappedDiagnostics(unmapped)
	groups := groupAmbiguousEdges(edges)

	for _, key := range sortedKeys(groups) {
		g := groups[key]
		out = append(out, diag.New(diag.OBS019, diag.Params{
			paramKind:   g.example.NativeKind,
			paramCount:  g.count,
			paramDetail: ambiguityDetail(g.example, nodes),
		}))
	}

	return out
}

func nonNilEdges(edges []*EvidenceEdge) []*EvidenceEdge {
	if edges == nil {
		return []*EvidenceEdge{}
	}

	return edges
}

func edgeLine(e *EvidenceEdge) int {
	if e.SourceSpan == nil {
		return 0
	}

	return e.SourceSpan.Line
}

func compareEdges(left, right *EvidenceEdge, nodes map[string]ObservedNode) int {
	if c := canonjson.CompareUTF16(left.NativeKind, right.NativeKind); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(nodes[left.From].QualifiedName, nodes[right.From].QualifiedName); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(nodes[left.To].QualifiedName, nodes[right.To].QualifiedName); c != 0 {
		return c
	}

	if c := edgeLine(left) - edgeLine(right); c != 0 {
		return c
	}

	return canonjson.CompareUTF16(left.ID, right.ID)
}

// SortEdges sorts edges by (nativeKind, from QN, to QN, line, id), the order of FetchEdges.
func SortEdges(edges []*EvidenceEdge, nodes map[string]ObservedNode) {
	sort.SliceStable(edges, func(i, j int) bool { return compareEdges(edges[i], edges[j], nodes) < 0 })
}
