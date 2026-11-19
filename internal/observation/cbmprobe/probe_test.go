package cbmprobe_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

const project = "p"

// fakeCBM answers the calls of cbmprobe with a two-node graph. The node query
// is cut in two pages to test the cursor.
type fakeCBM struct {
	queries []string
	notYet  int
}

func (f *fakeCBM) handle(tool string, args map[string]any) (any, error) {
	switch tool {
	case "index_repository":
		return indexRepositoryReply(), nil
	case "index_status":
		return f.statusReply(), nil
	case "get_graph_schema":
		return schemaReply(), nil
	case "query_graph":
		return f.queryReply(args)
	default:
		return nil, errors.New("unexpected tool " + tool)
	}
}

func indexRepositoryReply() map[string]any {
	return map[string]any{
		"project":  project,
		"status":   "indexed",
		"excluded": map[string]any{"dirs": []string{".git", "deploy"}, "count": 2},
		"not_indexed_files": map[string]any{
			"files": []map[string]any{{"path": "a.png", "reason": "ignored-suffix"}}, "count": 1,
		},
		"parse_partial": map[string]any{
			"files": []map[string]any{{"path": "t.yaml", "error_ranges": "1-4"}}, "count": 1,
		},
		"nodes": 2, "edges": 1, "adr_present": false,
	}
}

func (f *fakeCBM) statusReply() map[string]any {
	if f.notYet > 0 {
		f.notYet--

		return map[string]any{"status": "indexing"}
	}

	return map[string]any{"status": "ready"}
}

func schemaReply() map[string]any {
	return map[string]any{
		"node_labels": []map[string]any{{"label": "Resource", "count": 1}, {"label": "Image", "count": 1}},
		"edge_types":  []map[string]any{{"type": "USAGE", "count": 1, "properties": []string{"source_id", "target_id", "ref"}}},
		"has_more":    false,
	}
}

func (f *fakeCBM) queryReply(args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	f.queries = append(f.queries, query)

	cursor, _ := args["cursor"].(string)

	switch {
	case strings.HasPrefix(query, "MATCH (n)") && cursor == "":
		return map[string]any{
			"rows": [][]any{{
				"1", `["Resource"]`, "api", "p.k8s.deployment.yaml::apps/Deployment/payments/api", "k8s/deployment.yaml", "3",
				`{"kind":"Deployment","obj_key":"apps/Deployment/payments/api","lines":0}`,
			}},
			"has_more": true, "truncated": true, "next_cursor": "c1",
		}, nil
	case strings.HasPrefix(query, "MATCH (n)"):
		return map[string]any{
			"rows": [][]any{{"2", `["Image"]`, "", "__image__registry.example.com/api", "", "0", `{"registry":"registry.example.com","repository":"registry.example.com/api"}`}},
		}, nil
	case strings.Contains(query, "[r:USAGE]"):
		return map[string]any{"rows": [][]any{{"1", "2", `{"ref":"image","image":"registry.example.com/api:1.0","line":7,"confidence":0.85,"flag":true}`}}}, nil
	default:
		return nil, errors.New("unexpected query " + query)
	}
}

func TestIndexGivesTheCoverageLossAndWaitsUntilReady(t *testing.T) {
	cbm := &fakeCBM{notYet: 1}
	client := observation.NewFakeClient(cbm.handle, "")

	res, err := cbmprobe.Index(t.Context(), client, "/repo", project)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	checkCoverageLoss(t, &res)

	if statusCalls := countCalls(client, "index_status"); statusCalls != 2 {
		t.Errorf("index_status was called %d times, want 2 (not ready, then ready)", statusCalls)
	}
}

// checkCoverageLoss checks the coverage loss that the fake index_repository reply gives.
func checkCoverageLoss(t *testing.T, res *cbmprobe.IndexResult) {
	t.Helper()

	if len(res.Excluded.Dirs) != 2 || res.Excluded.Dirs[1] != "deploy" {
		t.Errorf("excluded.dirs = %v", res.Excluded.Dirs)
	}

	if len(res.NotIndexedFiles.Files) != 1 || res.NotIndexedFiles.Files[0].Reason != "ignored-suffix" {
		t.Errorf("not_indexed_files = %v", res.NotIndexedFiles.Files)
	}

	if len(res.ParsePartial.Files) != 1 || res.ParsePartial.Files[0].ErrorRanges != "1-4" {
		t.Errorf("parse_partial = %v", res.ParsePartial.Files)
	}
}

// countCalls gives how many times the client got a call of a tool.
func countCalls(client *observation.FakeClient, tool string) int {
	var count int

	for _, call := range client.Calls() {
		if call.Tool == tool {
			count++
		}
	}

	return count
}

func TestIndexKeyDoesNotDependOnTheRepositoryPath(t *testing.T) {
	first := observation.CallKey("index_repository", cbmprobe.IndexArgs("/a", project))
	second := observation.CallKey("index_repository", cbmprobe.IndexArgs("/b/c", project))

	if first != second {
		t.Fatalf("the index_repository key depends on the repository path")
	}
}

func TestReadGivesNodesAndEdgesWithAllProperties(t *testing.T) {
	cbm := &fakeCBM{}
	client := observation.NewFakeClient(cbm.handle, "")

	graph, err := cbmprobe.Read(t.Context(), client, project)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(graph.NodeLabels) != 2 || len(graph.EdgeTypes) != 1 {
		t.Fatalf("schema = %v %v", graph.NodeLabels, graph.EdgeTypes)
	}

	if len(graph.Nodes) != 2 {
		t.Fatalf("the cursor must give the second page: %d nodes", len(graph.Nodes))
	}

	deployment := requireDeployment(t, graph)

	image := requireImage(t, graph)

	checkEdgeProps(t, requireUsageEdge(t, graph, deployment, image))

	if graph.Node("2") != image || graph.Node("9") != nil {
		t.Error("Node by ID")
	}

	checkQueriesReadProperties(t, cbm.queries)
}

// checkQueriesReadProperties checks that every query reads properties with properties(x), the only way to
// read a property named `count`.
func checkQueriesReadProperties(t *testing.T, queries []string) {
	t.Helper()

	for _, query := range queries {
		if !strings.Contains(query, "properties(") {
			t.Errorf("query without properties(): %s", query)
		}
	}
}

// requireImage gives the image node of the fake graph.
func requireImage(t *testing.T, graph *cbmprobe.Graph) *cbmprobe.Node {
	t.Helper()

	image := graph.FindNode(project, "__image__registry.example.com/api")
	if image == nil || len(graph.NodesOf("Image")) != 1 {
		t.Fatalf("image node = %+v", image)
	}

	return image
}

// requireUsageEdge gives the only USAGE edge of the fake graph and checks that it joins from to to.
func requireUsageEdge(t *testing.T, graph *cbmprobe.Graph, from, to *cbmprobe.Node) *cbmprobe.Edge {
	t.Helper()

	usage := graph.EdgesOf("USAGE")
	if len(usage) != 1 {
		t.Fatalf("USAGE edges = %+v", usage)
	}

	if usage[0].From != from || usage[0].To != to {
		t.Fatalf("USAGE edges = %+v", usage)
	}

	return usage[0]
}

// requireDeployment gives the deployment node of the fake graph and checks its properties.
func requireDeployment(t *testing.T, graph *cbmprobe.Graph) *cbmprobe.Node {
	t.Helper()

	deployment := graph.FindNode(project, "k8s.deployment.yaml::apps/Deployment/payments/api")
	if deployment == nil {
		t.Fatalf("deployment node = %+v", deployment)
	}

	if deployment.Label() != "Resource" {
		t.Fatalf("deployment node = %+v", deployment)
	}

	if deployment.Prop("kind") != "Deployment" || deployment.StartLine != 3 {
		t.Fatalf("deployment node = %+v", deployment)
	}

	if deployment.Prop("lines") != "0" || deployment.Prop("absent") != "" {
		t.Errorf("number and absent properties: %q, %q", deployment.Prop("lines"), deployment.Prop("absent"))
	}

	return deployment
}

// checkEdgeProps checks the text form of every property of the usage edge of the fake graph.
func checkEdgeProps(t *testing.T, edge *cbmprobe.Edge) {
	t.Helper()

	for name, want := range map[string]string{"ref": "image", "line": "7", "confidence": "0.85", "flag": "true", "absent": ""} {
		if got := edge.Prop(name); got != want {
			t.Errorf("edge property %s = %q, want %q", name, got, want)
		}
	}
}

func TestReadRefusesACursorThatDoesNotAdvance(t *testing.T) {
	stuck := func(tool string, args map[string]any) (any, error) {
		if tool == "query_graph" {
			return map[string]any{
				"rows":     [][]any{{"1", `["Resource"]`, "api", "p.a", "a.yaml", "1", `{}`}},
				"has_more": true, "truncated": true, "next_cursor": "c1",
			}, nil
		}

		return (&fakeCBM{}).handle(tool, args)
	}

	_, err := cbmprobe.Read(t.Context(), observation.NewFakeClient(stuck, ""), project)
	if err == nil || !strings.Contains(err.Error(), "cursor does not advance") {
		t.Fatalf("a repeated cursor must stop the read, got %v", err)
	}
}
