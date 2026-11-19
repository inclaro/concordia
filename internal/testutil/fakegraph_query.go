package testutil

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

var fakeNodeQueryPattern = regexp.MustCompile(
	`^MATCH \(n:(\w+)\) RETURN (count\(n\)|id\(n\), properties\(n\)(?:, n\.name)?|id\(n\), labels\(n\), n\.qualified_name, n\.file_path, n\.start_line)$`)

var fakeQueryPattern = regexp.MustCompile(
	`^MATCH \(a:(\w+)\)-\[r:(\w+)\]->\(b\)(?: WHERE a\.file_path (STARTS WITH|=) '([^']*)')? RETURN (.+)$`)

// fakeIDColumn is the column of the node ID of a node query.
const fakeIDColumn = "id(n)"

// outputBudgetReason is the truncation reason of a page that the output budget cuts.
const outputBudgetReason = "output_budget"

// fakeResult is the columns and all rows of a query, before paging.
type fakeResult struct {
	columns []string
	rows    [][]any
}

// query runs a `query_graph` call: a snapshot of all matching rows, cut into
// pages by max_rows, by the output budget and by PageRows. A cut page has
// has_more, truncated and, unless NoCursor is set, a next_cursor.
func (g *FakeGraph) query(args map[string]any) (any, error) {
	q, _ := args["query"].(string)

	cursor, _ := args["cursor"].(string)
	if cursor == "" {
		g.Queries = append(g.Queries, q)
	}

	result, err := g.matchRows(q)
	if err != nil {
		return nil, err
	}

	offset, err := cursorOffset(cursor)
	if err != nil {
		return nil, err
	}

	pageSize, reason := g.pageSize(args, len(result.rows)-offset)

	return g.queryPage(result, offset, pageSize, reason), nil
}

// cursorOffset gives the row offset of a `query_graph` cursor; no cursor is offset 0.
func cursorOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(strings.TrimPrefix(cursor, cursorPrefix))
	if err != nil || !strings.HasPrefix(cursor, cursorPrefix) {
		return 0, diag.Fail(diag.OBS001, diag.Detail("query_graph: bad cursor "+cursor))
	}

	return n, nil
}

// pageSize gives the number of rows of a page and the reason for a cut, when
// remaining rows are left: max_rows, then the output budget, then PageRows.
func (g *FakeGraph) pageSize(args map[string]any, remaining int) (size int, reason string) {
	size = remaining

	if limit := intArg(args["max_rows"], defaultFakeMaxRows); limit > 0 && limit < size {
		size, reason = limit, "page_limit"
	}

	if g.budgetCuts(args, size) {
		size, reason = g.BudgetRows, outputBudgetReason
	}

	if g.PageRows > 0 && g.PageRows < size {
		size, reason = g.PageRows, outputBudgetReason
	}

	return size, reason
}

// budgetCuts tells if the output budget cuts a page of size rows.
func (g *FakeGraph) budgetCuts(args map[string]any, size int) bool {
	if g.BudgetRows <= 0 || g.BudgetRows >= size {
		return false
	}

	return intArg(args["max_output_tokens"], 0) < fakeLargeBudget
}

// queryPage makes the response for the page of pageSize rows that starts at offset.
func (g *FakeGraph) queryPage(result fakeResult, offset, pageSize int, reason string) map[string]any {
	total := len(result.rows)
	end := min(offset+pageSize, total)
	rows := result.rows[min(offset, total):end]
	more := end < total

	res := map[string]any{
		"columns":        result.columns,
		"rows":           rows,
		"returned":       len(rows),
		totalKey:         total,
		"total_relation": "eq",
		"has_more":       more,
		"truncated":      more,
	}

	if more {
		res["truncation_reason"] = reason
		res["next_offset"] = end

		if !g.NoCursor {
			res["next_cursor"] = cursorPrefix + strconv.Itoa(end)
		}
	}

	return res
}

// matchRows gives the columns and all rows of a query that the fake knows: the
// File listing, a node query, an edge query, or the count of an edge query.
func (g *FakeGraph) matchRows(cypher string) (fakeResult, error) {
	if cypher == fileQuery {
		return g.fileRows(), nil
	}

	if nm := fakeNodeQueryPattern.FindStringSubmatch(cypher); nm != nil {
		return g.matchNodes(nm[1], nm[2])
	}

	m := fakeQueryPattern.FindStringSubmatch(cypher)
	if m == nil {
		return fakeResult{}, diag.Fail(diag.OBS001, diag.Detail("fake cannot parse: "+cypher))
	}

	label, edgeType, op, value, ret := m[1], m[2], m[3], m[4], m[5]
	matching := g.matchingEdges(label, edgeType, op, value)

	if ret == "count(r)" {
		return countResult("COUNT(r)", len(matching)), nil
	}

	return g.edgeRows(matching, strings.Split(ret, ", ")), nil
}

// fileRows lists the file path of each `File` node.
func (g *FakeGraph) fileRows() fakeResult {
	rows := [][]any{}

	for i := range g.Nodes {
		if g.Nodes[i].Label == fileLabel {
			rows = append(rows, []any{g.Nodes[i].Path})
		}
	}

	return fakeResult{columns: []string{"f.file_path"}, rows: rows}
}

// countResult is the result of a count query: no row for a count of 0, as CBM gives it.
func countResult(column string, n int) fakeResult {
	rows := [][]any{}
	if n > 0 {
		rows = [][]any{{strconv.Itoa(n)}}
	}

	return fakeResult{columns: []string{column}, rows: rows}
}

// edgeRows gives one row for each edge, with the requested columns.
func (g *FakeGraph) edgeRows(edges []FakeEdge, columns []string) fakeResult {
	rows := make([][]any, 0, len(edges))

	for _, e := range edges {
		row := make([]any, len(columns))
		for i, c := range columns {
			row[i] = g.column(e, c)
		}

		rows = append(rows, row)
	}

	return fakeResult{columns: columns, rows: rows}
}

// matchNodes answers `MATCH (n:<label>) RETURN count(n)` and
// `MATCH (n:<label>) RETURN id(n), properties(n)`, with the column `n.name`
// when asked. The properties are a JSON object in a string, as CBM gives them;
// as in CBM, the node property `name` is not in them, only in its own column.
func (g *FakeGraph) matchNodes(label, ret string) (fakeResult, error) {
	var matching []FakeNode

	for i := range g.Nodes {
		if g.Nodes[i].Label == label {
			matching = append(matching, g.Nodes[i])
		}
	}

	if ret == "count(n)" {
		return countResult("COUNT(n)", len(matching)), nil
	}

	if strings.HasPrefix(ret, "id(n), labels(n)") {
		return g.nodeLabelRows(matching), nil
	}

	return g.nodePropertyRows(matching, strings.HasSuffix(ret, ", n.name"))
}

// nodeLabelRows gives the ID, labels, qualified name, file path and start line of each node.
func (g *FakeGraph) nodeLabelRows(nodes []FakeNode) fakeResult {
	rows := make([][]any, 0, len(nodes))

	for i := range nodes {
		rows = append(rows, []any{
			nodes[i].ID, labelsJSON(nodes[i].Label), g.qualifiedName(&nodes[i]), nodes[i].Path, strconv.Itoa(nodes[i].Line),
		})
	}

	return fakeResult{
		columns: []string{fakeIDColumn, "labels(n)", "n.qualified_name", "n.file_path", "n.start_line"},
		rows:    rows,
	}
}

// nodePropertyRows gives the ID and the properties of each node, and the name when withName is set.
func (g *FakeGraph) nodePropertyRows(nodes []FakeNode, withName bool) (fakeResult, error) {
	columns := []string{fakeIDColumn, "properties(n)"}
	if withName {
		columns = append(columns, "n.name")
	}

	rows := make([][]any, 0, len(nodes))

	for i := range nodes {
		data, err := g.nodePropertiesJSON(&nodes[i])
		if err != nil {
			return fakeResult{}, err
		}

		row := []any{nodes[i].ID, data}
		if withName {
			row = append(row, nodes[i].Props[fakeNameProp])
		}

		rows = append(rows, row)
	}

	return fakeResult{columns: columns, rows: rows}, nil
}

// nodePropertiesJSON gives the value of `properties(n)`: the qualified name, the
// file path and the properties of the node, without `name`, as a JSON object.
func (g *FakeGraph) nodePropertiesJSON(n *FakeNode) (string, error) {
	props := map[string]string{qualifiedNameProp: g.qualifiedName(n), filePathProp: n.Path}
	maps.Copy(props, n.Props)
	delete(props, fakeNameProp)

	data, err := json.Marshal(props)
	if err != nil {
		return "", fmt.Errorf("fake node properties: %w", err)
	}

	return string(data), nil
}

// matchingEdges gives the edges of one type from nodes of one label, optionally
// limited to a source file path (op "=") or path prefix (op "STARTS WITH").
func (g *FakeGraph) matchingEdges(label, edgeType, op, value string) []FakeEdge {
	var matching []FakeEdge

	for _, edge := range g.Edges {
		if edge.From.Label != label || edge.Type != edgeType {
			continue
		}

		if pathMatches(edge.From.Path, op, value) {
			matching = append(matching, edge)
		}
	}

	return matching
}

// pathMatches tells if a file path passes the WHERE clause of a query: equal
// for op "=", a prefix for op "STARTS WITH", and any path for no clause.
func pathMatches(filePath, op, value string) bool {
	switch op {
	case "STARTS WITH":
		return strings.HasPrefix(filePath, value)
	case "=":
		return filePath == value
	default:
		return true
	}
}

// column gives the value of one column of an edge query for an edge.
func (g *FakeGraph) column(edge FakeEdge, col string) string {
	switch col {
	case "id(a)":
		return edge.From.ID
	case "labels(a)":
		return labelsJSON(edge.From.Label)
	case "id(b)":
		return edge.To.ID
	case "labels(b)":
		return labelsJSON(edge.To.Label)
	case "properties(r)":
		return propsJSON(edge.Props)
	}

	if name, ok := strings.CutPrefix(col, "a."); ok {
		return g.nodeColumn(edge.From, name)
	}

	if name, ok := strings.CutPrefix(col, "b."); ok {
		return g.nodeColumn(edge.To, name)
	}

	return edge.Props[strings.TrimPrefix(col, "r.")]
}

// nodeColumn gives the value of the column `<node>.<field>` for a node.
func (g *FakeGraph) nodeColumn(n *FakeNode, field string) string {
	switch field {
	case qualifiedNameProp:
		return g.qualifiedName(n)
	case filePathProp:
		return n.Path
	case "start_line":
		return strconv.Itoa(n.Line)
	default:
		return n.Props[field]
	}
}

// propsJSON gives edge properties as a JSON object.
func propsJSON(props map[string]string) string {
	data, err := json.Marshal(props)
	if err != nil {
		return "{}"
	}

	return string(data)
}

// qualifiedName gives the qualified name of a node as CBM does: a project
// prefix, except for the pseudo nodes that start with `__` (`__image__<repo>`,
// `__route__...`, `__env__<name>`, `__chart__...`), which are shared by the whole project.
func (g *FakeGraph) qualifiedName(n *FakeNode) string {
	if strings.HasPrefix(n.QN, "__") {
		return n.QN
	}

	return g.Project + "." + n.QN
}

func labelsJSON(label string) string {
	return `["` + label + `"]`
}
