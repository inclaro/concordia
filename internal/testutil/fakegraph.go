package testutil

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/inclaro/concordia/internal/diag"
)

const (
	// defaultFakeMaxRows is max_rows of `query_graph` when a call does not give it (CBM 0.12.1: 200).
	defaultFakeMaxRows = 200
	// defaultSchemaLimit is the page size of `get_graph_schema` when a call does not give it.
	defaultSchemaLimit = 50
	// fakeLargeBudget is the smallest max_output_tokens that lifts the output budget of a page.
	fakeLargeBudget = 1_000_000
	// defaultBudgetRows is the number of rows of a page under the default output budget.
	defaultBudgetRows = 100
	fileLabel         = "File"
	fileQuery         = "MATCH (f:File) RETURN f.file_path"
	formatJSON        = "json"
	projectKey        = "project"
	totalKey          = "total"
	cursorPrefix      = "q1."
	filePathProp      = "file_path"
	qualifiedNameProp = "qualified_name"
)

// FakeNode is a node of a FakeGraph.
type FakeNode struct {
	// Props are the node properties other than the qualified name, file path
	// and start line (`kind`, `obj_key`, `image`, ...). A query column `a.<name>`
	// or `b.<name>` reads one of them.
	Props map[string]string
	ID    string
	Label string
	// QN is the qualified name without the project prefix.
	QN   string
	Path string
	Line int
}

// FakeEdge is an edge of a FakeGraph.
type FakeEdge struct {
	Props map[string]string
	From  *FakeNode
	To    *FakeNode
	Type  string
}

// FakeGraph is an in-memory stand-in for the CBM 0.12.1 tools that the
// adapter uses (`index_status`, `get_graph_schema`, `query_graph`). Use
// Handler with observation.NewFakeClient. Like the CBM CLI, it gives text
// instead of JSON unless the call has `"format": "json"`: the fake gives an
// error for such a call, so a call without it is found by the tests.
//
// Handler gives maps with these types, so that a test can change a response:
// `query_graph` gives a `map[string]any` with "rows" of type `[][]any`.
type FakeGraph struct {
	Project string
	// HeadSHA is the git head in `index_status`. Empty gives no git block.
	HeadSHA string
	// Status is the status in `index_status`.
	Status string
	Nodes  []FakeNode
	Edges  []FakeEdge
	// Queries are the Cypher queries of `query_graph` calls (the first page of each), in order.
	Queries []string
	// NodeBump is added to the node count of `index_status`; a change makes a new generation.
	NodeBump int
	// BudgetRows is the number of rows of a `query_graph` page when the call does
	// not enlarge max_output_tokens (CBM cuts the page at its output budget).
	// NewFakeGraph sets 100.
	BudgetRows int
	// PageRows, when greater than 0, cuts every `query_graph` page after this
	// many rows, whatever the budget: the caller must follow the cursor.
	PageRows int
	// NoCursor makes a cut page without a cursor (an incomplete result).
	NoCursor bool

	mu sync.Mutex
}

// NewFakeGraph makes a graph with head `abc123` and status `ready`.
func NewFakeGraph(project string, nodes []FakeNode, edges []FakeEdge) *FakeGraph {
	return &FakeGraph{Project: project, HeadSHA: "abc123", Status: "ready", Nodes: nodes, Edges: edges, BudgetRows: defaultBudgetRows}
}

// Handler is the handler of the tool calls. Its signature fits observation.FakeHandler.
func (g *FakeGraph) Handler(tool string, args map[string]any) (any, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if project, _ := args[projectKey].(string); project != g.Project {
		return nil, diag.Fail(diag.OBS002, diag.Detail(fmt.Sprintf("project not found: %v", args[projectKey])))
	}

	if format, _ := args["format"].(string); format != formatJSON {
		return nil, diag.Fail(diag.OBS001, diag.Detail(tool+": the fake needs \"format\": \"json\" (CBM 0.12.1 gives text otherwise)"))
	}

	switch tool {
	case "index_status":
		return g.status(args["verbose"] == true), nil
	case "get_graph_schema":
		return g.schema(intArg(args["limit"], defaultSchemaLimit), intArg(args["offset"], 0), args["diagnostics"] == "full"), nil
	case "query_graph":
		return g.query(args)
	default:
		return nil, diag.Fail(diag.OBS001, diag.Detail("unknown tool "+tool))
	}
}

func (g *FakeGraph) status(verbose bool) map[string]any {
	res := map[string]any{
		projectKey: g.Project,
		"nodes":    len(g.Nodes) + g.NodeBump,
		"edges":    len(g.Edges),
		"status":   g.Status,
	}

	// CBM 0.12.1 gives the git block with head_sha only with verbose.
	if verbose && g.HeadSHA != "" {
		res["git"] = map[string]any{"is_git": true, "head_sha": g.HeadSHA}
	}

	return res
}

func intArg(v any, fallback int) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	case string:
		n, err := strconv.Atoi(x)
		if err == nil {
			return n
		}
	}

	return fallback
}

// MultiHandler serves several graphs, one for each project: a call goes to the graph whose
// Project is the `project` argument. A call for another project gets the answer of the first
// graph (CO-OBS-002, project not found). Its signature fits observation.FakeHandler.
func MultiHandler(graphs ...*FakeGraph) func(tool string, args map[string]any) (any, error) {
	return func(tool string, args map[string]any) (any, error) {
		project, _ := args[projectKey].(string)

		for _, g := range graphs {
			if g.Project == project {
				return g.Handler(tool, args)
			}
		}

		return graphs[0].Handler(tool, args)
	}
}
