package testutil_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

const (
	typeUsage     = "USAGE"
	typeDependsOn = "DEPENDS_ON"
	labelResource = "Resource"
	labelModule   = "Module"
	labelImage    = "Image"
	apiImage      = "registry.example.com/payments/api"
)

func TestObjKey(t *testing.T) {
	tests := []struct {
		name                         string
		group, kind, namespace, want string
		resource                     string
	}{
		{"namespaced", "apps", "Deployment", "payments", "apps/Deployment/payments/api", "api"},
		{"core group", "core", "Secret", "payments", "core/Secret/payments/api", "api"},
		{"cluster scoped", "core", "Namespace", testutil.ClusterScoped, "core/Namespace/_/payments", "payments"},
		{"no namespace", "apps", "Deployment", testutil.NamespaceUnknown, "apps/Deployment/?/<.Release.Name>-api", "<.Release.Name>-api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testutil.ObjKey(tt.group, tt.kind, tt.namespace, tt.resource); got != tt.want {
				t.Fatalf("ObjKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResourceNode(t *testing.T) {
	n := testutil.ResourceNode("7", "k8s/prod/deployment.yaml", "apps", "Deployment", "payments", "api")

	if n.Label != labelResource || n.Path != "k8s/prod/deployment.yaml" {
		t.Fatalf("label or path: %+v", n)
	}

	if want := "k8s.prod.deployment.yaml::apps/Deployment/payments/api"; n.QN != want {
		t.Fatalf("QN = %q, want %q", n.QN, want)
	}

	for name, want := range map[string]string{
		"kind": "Deployment", "group": "apps", "namespace": "payments", "obj_key": "apps/Deployment/payments/api", "source": "k8s",
	} {
		if n.Props[name] != want {
			t.Errorf("Props[%s] = %q, want %q", name, n.Props[name], want)
		}
	}

	other := testutil.ResourceNode("8", "k8s/prod/other.yaml", "apps", "Deployment", "payments", "api")
	if other.QN == n.QN || other.Props["obj_key"] != n.Props["obj_key"] {
		t.Fatalf("one obj_key in two files must give two qualified names: %q %q", n.QN, other.QN)
	}
}

func TestImageNode(t *testing.T) {
	full := testutil.ImageNode("1", apiImage)
	if full.QN != "__image__"+apiImage || full.Path != "" {
		t.Fatalf("QN or path: %+v", full)
	}

	if full.Props["registry"] != "registry.example.com" || full.Props["repository"] != apiImage {
		t.Fatalf("props = %v", full.Props)
	}

	if _, hasTag := full.Props["tag"]; hasTag {
		t.Fatalf("an Image node has no tag: %v", full.Props)
	}

	if short := testutil.ImageNode("2", "postgres"); short.Props["registry"] != "unknown" {
		t.Fatalf("a short Docker Hub name has registry unknown: %v", short.Props)
	}

	if hub := testutil.ImageNode("3", "library/postgres"); hub.Props["registry"] != "unknown" {
		t.Fatalf("a Docker Hub path has registry unknown: %v", hub.Props)
	}
}

// sameEnds tells if an edge goes from one node to another, by identity.
func sameEnds(e *testutil.FakeEdge, from, to *testutil.FakeNode) bool {
	return e.From == from && e.To == to
}

func TestDiscriminatorsProps(t *testing.T) {
	got := testutil.Discriminators{Via: "terraform", Ref: "var"}.Props()
	if !maps.Equal(got, map[string]string{"via": "terraform", "ref": "var"}) {
		t.Fatalf("Props = %v", got)
	}

	all := testutil.Discriminators{Via: "v", Ref: "r", Kind: "k", Strategy: "s", Syntax: "x", Tier: "t"}.Props()
	if len(all) != 6 {
		t.Fatalf("all six discriminators: %v", all)
	}

	if empty := (testutil.Discriminators{}).Props(); len(empty) != 0 {
		t.Fatalf("empty discriminators give no property: %v", empty)
	}
}

func TestInfraEdge(t *testing.T) {
	from := testutil.ResourceNode("1", "k8s/prod/deployment.yaml", "apps", "Deployment", "payments", "api")
	to := testutil.ImageNode("2", apiImage)

	e := testutil.InfraEdge(&from, &to, typeUsage, testutil.Discriminators{Ref: "image"}, map[string]string{
		"container": "sidecar", "image": apiImage + ":1.3.0",
	})

	if e.Type != typeUsage || !sameEnds(&e, &from, &to) {
		t.Fatalf("edge = %+v", e)
	}

	if e.Props["ref"] != "image" || e.Props["container"] != "sidecar" {
		t.Fatalf("props = %v", e.Props)
	}

	// An extra property wins over a discriminator.
	won := testutil.InfraEdge(&from, &to, typeUsage, testutil.Discriminators{Ref: "image"}, map[string]string{"ref": "env"})
	if won.Props["ref"] != "env" {
		t.Fatalf("extra must win: %v", won.Props)
	}

	plain := testutil.NewEdge(&from, &to, typeUsage, map[string]string{"line": "3"})
	if plain.Props["line"] != "3" {
		t.Fatalf("NewEdge props = %v", plain.Props)
	}
}

// infraGraph is a graph with a Dockerfile Module, three Image nodes, a Resource
// and the edges of them, built only with the helpers.
func infraGraph() *testutil.FakeGraph {
	dockerfile := testutil.InfraNode("10", labelModule, "docker.Dockerfile", "docker/Dockerfile", nil)
	golang := testutil.ImageNode("11", "golang")
	api := testutil.ImageNode("12", apiImage)
	deployment := testutil.ResourceNode("13", "k8s/prod/deployment.yaml", "apps", "Deployment", "payments", "api")

	edges := []testutil.FakeEdge{
		testutil.InfraEdge(&dockerfile, &golang, typeDependsOn, testutil.Discriminators{Via: "from"},
			map[string]string{"image": "golang:1.22", "stage": "0", "line": "1"}),
		testutil.InfraEdge(&deployment, &api, typeUsage, testutil.Discriminators{Ref: "image"},
			map[string]string{"container": "sidecar", "image": apiImage + ":1.3.0"}),
	}

	return testutil.NewFakeGraph("p", []testutil.FakeNode{
		testutil.FileNode("1", "docker/Dockerfile"), dockerfile, golang, api, deployment,
	}, edges)
}

type queryResponse struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

func runQuery(t *testing.T, client *observation.FakeClient, cypher string) queryResponse {
	t.Helper()

	raw, err := client.Call(t.Context(), "query_graph", map[string]any{
		"project": "p", "query": cypher, "format": "json", "max_output_tokens": 10_000_000,
	})
	if err != nil {
		t.Fatalf("query_graph %q: %v", cypher, err)
	}

	var res queryResponse
	if err = json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return res
}

// schemaProperties are the property names of the node labels and edge types of a schema page.
type schemaProperties struct {
	node map[string][]string
	edge map[string][]string
}

// fullSchemaProperties reads the schema with diagnostics "full" and gives the property names by label and by type.
func fullSchemaProperties(t *testing.T, client *observation.FakeClient) schemaProperties {
	t.Helper()

	raw, err := client.Call(t.Context(), "get_graph_schema", map[string]any{
		"project": "p", "format": "json", "diagnostics": "full", "limit": 50,
	})
	if err != nil {
		t.Fatalf("get_graph_schema: %v", err)
	}

	var schema struct {
		NodeLabels []struct {
			Label      string   `json:"label"`
			Properties []string `json:"properties"`
		} `json:"node_labels"` //nolint:tagliatelle // the CBM wire format is snake_case.
		EdgeTypes []struct {
			Type       string   `json:"type"`
			Properties []string `json:"properties"`
			Count      int      `json:"count"`
		} `json:"edge_types"` //nolint:tagliatelle // the CBM wire format is snake_case.
	}

	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode: %v", err)
	}

	props := schemaProperties{node: map[string][]string{}, edge: map[string][]string{}}

	for _, l := range schema.NodeLabels {
		props.node[l.Label] = l.Properties
	}

	for _, e := range schema.EdgeTypes {
		props.edge[e.Type] = e.Properties
	}

	return props
}

// requireContains reports each name that the property names lack. The what is
// the label or type that the names belong to.
func requireContains(t *testing.T, what string, props []string, names ...string) {
	t.Helper()

	for _, name := range names {
		if !slices.Contains(props, name) {
			t.Errorf("%s properties %v lack %s", what, props, name)
		}
	}
}

func TestFakeGraphServesInfrastructureProperties(t *testing.T) {
	graph := infraGraph()
	client := observation.NewFakeClient(graph.Handler, "")

	t.Run("serves node properties and edge discriminators as columns", func(t *testing.T) {
		res := runQuery(t, client, "MATCH (a:Resource)-[r:USAGE]->(b) RETURN a.obj_key, a.kind, b.repository, b.registry, r.ref, r.container, r.image")
		if len(res.Rows) != 1 {
			t.Fatalf("rows = %v", res.Rows)
		}

		want := []any{"apps/Deployment/payments/api", "Deployment", apiImage, "registry.example.com", "image", "sidecar", apiImage + ":1.3.0"}
		if !slices.Equal(res.Rows[0], want) {
			t.Fatalf("row = %v, want %v", res.Rows[0], want)
		}
	})

	t.Run("gives no project prefix to a pseudo node", func(t *testing.T) {
		res := runQuery(t, client, "MATCH (a:Resource)-[r:USAGE]->(b) RETURN a.qualified_name, b.qualified_name, b.file_path")

		want := []any{"p.k8s.prod.deployment.yaml::apps/Deployment/payments/api", "__image__" + apiImage, ""}
		if len(res.Rows) != 1 || !slices.Equal(res.Rows[0], want) {
			t.Fatalf("rows = %v, want %v", res.Rows, want)
		}
	})

	t.Run("lists the node and edge properties in the full schema", func(t *testing.T) {
		props := fullSchemaProperties(t, client)

		requireContains(t, "Resource", props.node[labelResource], "obj_key", "kind", "group", "namespace")
		requireContains(t, "Image", props.node[labelImage], "repository")
		requireContains(t, "DEPENDS_ON", props.edge[typeDependsOn], "via", "image", "stage", "line", "source_id", "target_id")
		requireContains(t, "USAGE", props.edge[typeUsage], "ref", "container")
	})
}

// TestDependsOnRoundTripsThroughFetchEdges checks that a fake graph with a
// DEPENDS_ON edge serves it to the adapter. The adapter normalizes only the
// native types of its table. While DEPENDS_ON is not in the table (HEAD of
// task 2.4), FetchEdges counts the edge as an unmapped native edge; once the
// normalization table maps it (task 4.2), FetchEdges gives the edge with its
// discriminator. The test accepts both and fails when the edge is lost.
func TestDependsOnRoundTripsThroughFetchEdges(t *testing.T) {
	graph := infraGraph()
	client := observation.NewFakeClient(graph.Handler, "")
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: "p"})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	schemaEdge := findSchemaEdge(ref.Schema.EdgeTypes, typeDependsOn)
	if schemaEdge == nil || schemaEdge.Count != 1 {
		t.Fatalf("the schema must list one DEPENDS_ON edge: %+v", ref.Schema.EdgeTypes)
	}

	requireContains(t, "schema DEPENDS_ON", schemaEdge.Properties, "via", "image", "stage", "line")

	res, err := provider.FetchEdges(t.Context(), ref, []string{typeDependsOn})
	if err != nil {
		t.Fatalf("FetchEdges: %v", err)
	}

	unmapped := res.UnmappedNativeEdges[typeDependsOn]
	served := servedEdges(res.Edges, typeDependsOn)

	if len(served)+unmapped != 1 {
		t.Fatalf("the DEPENDS_ON edge must be served once: %d normalized, %d unmapped", len(served), unmapped)
	}

	for _, e := range served {
		if !hasEntries(e.Properties, map[string]string{"via": "from", "image": "golang:1.22", "stage": "0"}) {
			t.Errorf("normalized DEPENDS_ON edge lost its properties: %v", e.Properties)
		}
	}

	// The same edge is served by a direct query, whatever the table says.
	requireOneRow(t, client,
		"MATCH (a:Module)-[r:DEPENDS_ON]->(b) RETURN a.qualified_name, b.qualified_name, r.via, r.image, r.stage",
		[]any{"p.docker.Dockerfile", "__image__golang", "from", "golang:1.22", "0"})
}

// findSchemaEdge gives the schema entry of an edge type, or nil.
func findSchemaEdge(types []observation.SchemaEdgeType, edgeType string) *observation.SchemaEdgeType {
	for i := range types {
		if types[i].Type == edgeType {
			return &types[i]
		}
	}

	return nil
}

// servedEdges gives the edges of a native kind.
func servedEdges(edges []*observation.EvidenceEdge, nativeKind string) []*observation.EvidenceEdge {
	var served []*observation.EvidenceEdge

	for _, e := range edges {
		if e.NativeKind == nativeKind {
			served = append(served, e)
		}
	}

	return served
}

// hasEntries tells if got has every key of want with the same value.
func hasEntries(got, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}

	return true
}

// requireOneRow runs a query and checks that it gives exactly one row, equal to want.
func requireOneRow(t *testing.T, client *observation.FakeClient, cypher string, want []any) {
	t.Helper()

	res := runQuery(t, client, cypher)
	if len(res.Rows) != 1 {
		t.Fatalf("direct query rows = %v", res.Rows)
	}

	if !slices.Equal(res.Rows[0], want) {
		t.Fatalf("direct row = %v, want %v", res.Rows[0], want)
	}
}
