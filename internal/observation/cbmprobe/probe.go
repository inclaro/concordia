// Package cbmprobe reads a whole codebase-memory-mcp graph with a small, fixed
// set of `query_graph` calls, and keeps every node and edge property as it is.
// It is the shared reader of `cmd/record-cbm` (live CBM, through a
// RecordingClient) and of the replay tests (through a ReplayClient). Because
// both sides run the same code, a recording always holds the calls that a test
// replays.
//
// The queries use `properties(n)` and `properties(r)`: one column holds all
// properties of a node or an edge. This is the only way to read a property
// with a reserved name such as `count` (`r.count` does not parse in CBM 0.12.1).
package cbmprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/observation"
)

const (
	argProject = "project"
	argQuery   = "query"

	// IndexMode is the index mode of the recordings. Infrastructure evidence needs `full`.
	IndexMode = "full"

	// maxRows is the largest max_rows of one page that CBM 0.12 accepts.
	maxRows = 99_998
	// outputBudget is max_output_tokens of a query: CBM cuts a page at this budget.
	outputBudget = 10_000_000
	schemaPage   = 200

	statusReady  = "ready"
	pollInterval = 200 * time.Millisecond
	// indexTimeout is the longest wait for `status: ready` after an index call.
	indexTimeout = 10 * time.Minute

	nodeQuery = "MATCH (n) RETURN id(n), labels(n), n.name, n.qualified_name, n.file_path, n.start_line, properties(n)"
	edgeQuery = "MATCH (a)-[r:%s]->(b) RETURN id(a), id(b), properties(r)"
)

// PathReason is a file that CBM did not index, with the reason.
type PathReason = observation.PathReason

// PartialFile is a file that CBM parsed in part, with the line ranges it could not parse.
type PartialFile = observation.PartialFile

// IndexResult is the part of the `index_repository` result that records coverage loss.
type IndexResult = observation.IndexResult

// IndexArgs gives the arguments of the `index_repository` call. The key of a
// recorded `index_repository` call does not contain repoPath.
func IndexArgs(repoPath, project string) map[string]any {
	return map[string]any{"repo_path": repoPath, "mode": IndexMode, "name": project}
}

// Index indexes a repository and waits until the project is ready.
func Index(ctx context.Context, client observation.Client, repoPath, project string) (IndexResult, error) {
	result, err := observation.CallAs[IndexResult](ctx, client, "index_repository", IndexArgs(repoPath, project))
	if err != nil {
		return IndexResult{}, fmt.Errorf("index_repository: %w", err)
	}

	if result.Status == "error" {
		return IndexResult{}, errors.New("index_repository reported an error")
	}

	if waitErr := waitReady(ctx, client, project); waitErr != nil {
		return IndexResult{}, waitErr
	}

	return result, nil
}

// waitReady polls `index_status` until the project is ready, the wait times out or ctx ends.
func waitReady(ctx context.Context, client observation.Client, project string) error {
	deadline := time.Now().Add(indexTimeout)

	for {
		status, err := observation.CallAs[struct {
			Status string `json:"status"`
		}](ctx, client, "index_status", map[string]any{
			argProject: project, observation.ArgFormat: observation.FormatJSON, "verbose": true,
		})
		if err != nil {
			return fmt.Errorf("index_status: %w", err)
		}

		if status.Status == statusReady {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("project %s is not ready: status %q", project, status.Status)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the index: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// SchemaLabel is a node label of `get_graph_schema`.
type SchemaLabel struct {
	Label      string   `json:"label"`
	Properties []string `json:"properties"`
	Count      int      `json:"count"`
}

// SchemaEdgeType is an edge type of `get_graph_schema`.
type SchemaEdgeType struct {
	Type       string   `json:"type"`
	Properties []string `json:"properties"`
	Count      int      `json:"count"`
}

// Node is a node of the graph. Props holds `properties(n)` without the columns
// that have their own field (name, qualified_name, file_path, start_line).
type Node struct {
	Props         map[string]any
	ID            string
	Name          string
	QualifiedName string
	FilePath      string
	Labels        []string
	StartLine     int
}

// Label gives the first label of the node.
func (n *Node) Label() string {
	if len(n.Labels) == 0 {
		return ""
	}

	return n.Labels[0]
}

// Prop gives a node property as text. It gives "" when the property is absent.
func (n *Node) Prop(name string) string {
	return PropText(n.Props, name)
}

// Edge is an edge of the graph. Props holds `properties(r)`.
type Edge struct {
	Props map[string]any
	From  *Node
	To    *Node
	Type  string
}

// Prop gives an edge property as text. It gives "" when the property is absent.
func (e *Edge) Prop(name string) string {
	return PropText(e.Props, name)
}

// PropText gives a property value as text: a string as is, a number or a
// boolean in JSON form. It gives "" when the property is absent.
func PropText(props map[string]any, name string) string {
	value, ok := props[name]
	if !ok {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// Graph is a whole CBM graph.
type Graph struct {
	byID       map[string]*Node
	Nodes      []*Node
	Edges      []*Edge
	NodeLabels []SchemaLabel
	EdgeTypes  []SchemaEdgeType
}

// Node gives the node with an ID, or nil.
func (g *Graph) Node(id string) *Node {
	return g.byID[id]
}

// EdgesOf gives the edges of one type in the order of the query.
func (g *Graph) EdgesOf(edgeType string) []*Edge {
	var out []*Edge

	for _, e := range g.Edges {
		if e.Type == edgeType {
			out = append(out, e)
		}
	}

	return out
}

// NodesOf gives the nodes with a label.
func (g *Graph) NodesOf(label string) []*Node {
	var out []*Node

	for _, n := range g.Nodes {
		if slices.Contains(n.Labels, label) {
			out = append(out, n)
		}
	}

	return out
}

// FindNode gives the node whose qualified name, without the project prefix
// `<project>.`, is qn. Pseudo nodes such as `__image__<repo>` have no prefix and match as they are.
// It gives nil when there is no such node.
func (g *Graph) FindNode(project, qn string) *Node {
	for _, n := range g.Nodes {
		if n.QualifiedName == qn || n.QualifiedName == project+"."+qn {
			return n
		}
	}

	return nil
}

type queryPage struct {
	NextCursor string   `json:"next_cursor"`
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	HasMore    bool     `json:"has_more"`
	Truncated  bool     `json:"truncated"`
}

type schemaPageResult struct {
	NodeLabels []SchemaLabel    `json:"node_labels"`
	EdgeTypes  []SchemaEdgeType `json:"edge_types"`
	NextOffset int              `json:"next_offset"`
	HasMore    bool             `json:"has_more"`
}

// Read reads the schema, all nodes and all edges of a ready project.
func Read(ctx context.Context, client observation.Client, project string) (*Graph, error) {
	graph := &Graph{byID: map[string]*Node{}}

	if err := readSchema(ctx, client, project, graph); err != nil {
		return nil, err
	}

	if err := readNodes(ctx, client, project, graph); err != nil {
		return nil, err
	}

	if err := readEdges(ctx, client, project, graph); err != nil {
		return nil, err
	}

	return graph, nil
}

// readNodes runs the node query and adds every node to the graph.
func readNodes(ctx context.Context, client observation.Client, project string, graph *Graph) error {
	rows, err := runQuery(ctx, client, project, nodeQuery)
	if err != nil {
		return err
	}

	for _, row := range rows {
		node, nodeErr := decodeNode(row)
		if nodeErr != nil {
			return nodeErr
		}

		graph.Nodes = append(graph.Nodes, node)
		graph.byID[node.ID] = node
	}

	return nil
}

// readEdges runs the edge query of every edge type of the schema and adds every edge to the graph.
func readEdges(ctx context.Context, client observation.Client, project string, graph *Graph) error {
	for _, edgeType := range graph.EdgeTypes {
		rows, err := runQuery(ctx, client, project, fmt.Sprintf(edgeQuery, edgeType.Type))
		if err != nil {
			return err
		}

		if addErr := addEdges(graph, edgeType.Type, rows); addErr != nil {
			return addErr
		}
	}

	return nil
}

// addEdges decodes the rows of one edge type and adds the edges to the graph.
func addEdges(graph *Graph, edgeType string, rows [][]any) error {
	for _, row := range rows {
		edge, err := decodeEdge(graph, edgeType, row)
		if err != nil {
			return err
		}

		graph.Edges = append(graph.Edges, edge)
	}

	return nil
}

func readSchema(ctx context.Context, client observation.Client, project string, graph *Graph) error {
	for offset := 0; ; {
		page, err := observation.CallAs[schemaPageResult](ctx, client, "get_graph_schema", map[string]any{
			argProject:            project,
			observation.ArgFormat: observation.FormatJSON,
			"diagnostics":         "full",
			"limit":               schemaPage,
			"offset":              offset,
		})
		if err != nil {
			return fmt.Errorf("get_graph_schema: %w", err)
		}

		graph.NodeLabels = append(graph.NodeLabels, page.NodeLabels...)
		graph.EdgeTypes = append(graph.EdgeTypes, page.EdgeTypes...)

		if !page.HasMore {
			return nil
		}

		if page.NextOffset <= offset {
			return errors.New("get_graph_schema: the next page does not advance")
		}

		offset = page.NextOffset
	}
}

// runQuery runs a query and follows the cursor of every cut page.
func runQuery(ctx context.Context, client observation.Client, project, cypher string) ([][]any, error) {
	args := map[string]any{
		argProject:            project,
		argQuery:              cypher,
		observation.ArgFormat: observation.FormatJSON,
		"max_rows":            maxRows,
		"max_output_tokens":   outputBudget,
	}

	var rows [][]any

	for {
		page, err := observation.CallAs[queryPage](ctx, client, "query_graph", args)
		if err != nil {
			return nil, fmt.Errorf("query_graph %q: %w", cypher, err)
		}

		rows = append(rows, page.Rows...)

		if !page.HasMore && !page.Truncated {
			return rows, nil
		}

		if cursorStalls(page, args) {
			return nil, fmt.Errorf("query_graph %q: the result is cut and the cursor does not advance", cypher)
		}

		args = maps.Clone(args)
		args["cursor"] = page.NextCursor
	}
}

// cursorStalls reports whether a cut page gives no way to the next page: the cursor is empty, the
// page has no rows, or the cursor is the one the call used.
func cursorStalls(page queryPage, args map[string]any) bool {
	return page.NextCursor == "" || len(page.Rows) == 0 || page.NextCursor == args["cursor"]
}

func cellText(row []any, i int) string {
	if i >= len(row) || row[i] == nil {
		return ""
	}

	switch v := row[i].(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func cellInt(row []any, i int) int {
	n, err := strconv.Atoi(cellText(row, i))
	if err != nil {
		return 0
	}

	return n
}

// decodeProps reads a `properties(x)` cell: a JSON object in a string.
func decodeProps(text string) (map[string]any, error) {
	props := map[string]any{}
	if text == "" {
		return props, nil
	}

	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()

	if err := dec.Decode(&props); err != nil {
		return nil, fmt.Errorf("decode properties %q: %w", text, err)
	}

	return props, nil
}

// Columns of the node query, in order.
const (
	nodeColID = iota
	nodeColLabels
	nodeColName
	nodeColQualifiedName
	nodeColFilePath
	nodeColStartLine
	nodeColProps
)

func decodeNode(row []any) (*Node, error) {
	var labels []string

	if err := json.Unmarshal([]byte(cellText(row, nodeColLabels)), &labels); err != nil {
		return nil, fmt.Errorf("decode labels %q: %w", cellText(row, nodeColLabels), err)
	}

	props, err := decodeProps(cellText(row, nodeColProps))
	if err != nil {
		return nil, err
	}

	return &Node{
		ID:            cellText(row, nodeColID),
		Labels:        labels,
		Name:          cellText(row, nodeColName),
		QualifiedName: cellText(row, nodeColQualifiedName),
		FilePath:      cellText(row, nodeColFilePath),
		StartLine:     cellInt(row, nodeColStartLine),
		Props:         props,
	}, nil
}

func decodeEdge(graph *Graph, edgeType string, row []any) (*Edge, error) {
	from, to := graph.byID[cellText(row, 0)], graph.byID[cellText(row, 1)]
	if from == nil || to == nil {
		return nil, fmt.Errorf("%s edge %s -> %s: a node is not in the graph", edgeType, cellText(row, 0), cellText(row, 1))
	}

	props, err := decodeProps(cellText(row, 2))
	if err != nil {
		return nil, err
	}

	return &Edge{Type: edgeType, From: from, To: to, Props: props}, nil
}
