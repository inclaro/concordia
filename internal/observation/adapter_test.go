package observation_test

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

func setup(g *testutil.FakeGraph, maxRows int) (*observation.FakeClient, *observation.CbmProvider) {
	client := observation.NewFakeClient(g.Handler, "")
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1", MaxRows: maxRows})

	return client, provider
}

func smallGraph() *testutil.FakeGraph {
	a := testutil.FakeNode{ID: "1", Label: "File", QN: "src.a.__file__", Path: "src/a.ts"}
	b := testutil.FakeNode{ID: "2", Label: "Module", QN: "src.b", Path: "src/b.ts", Line: 1}
	f := testutil.FakeNode{ID: "3", Label: "Function", QN: "src.a.f", Path: "src/a.ts", Line: 2}
	g := testutil.FakeNode{ID: "4", Label: "Function", QN: "src.b.g", Path: "src/b.ts", Line: 5}
	edges := []testutil.FakeEdge{
		{Type: "IMPORTS", From: &a, To: &b, Props: map[string]string{"local_name": "b"}},
		{Type: "CALLS", From: &f, To: &g, Props: map[string]string{"strategy": "lsp_direct", "confidence": "0.95", "line": "3", "callee": "g"}},
		{Type: "CALLS", From: &f, To: &g, Props: map[string]string{"strategy": "suffix_match", "confidence": "0.28", "line": "4", "callee": "g"}},
		{Type: "HAS_BRANCH", From: &a, To: &b},
	}

	return testutil.NewFakeGraph("p", []testutil.FakeNode{testutil.FileNode("1", "src/a.ts"), testutil.FileNode("5", "src/b.ts"), b, f, g}, edges)
}

func freeze(t *testing.T, p *observation.CbmProvider) *observation.GraphSnapshotRef {
	t.Helper()

	ref, err := p.Freeze(t.Context(), observation.FreezeRequest{Project: "p"})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	return ref
}

func TestFreeze(t *testing.T) {
	t.Run("gives a generation that starts with cbm:<project>: and is stable", freezeCaseStableGeneration)
	t.Run("a revision mismatch gives CO-OBS-008 (exit 3)", freezeCaseRevisionMismatch)
	t.Run("the indexed revision takes precedence over the head of CBM", freezeCaseIndexedRevisionWins)
	t.Run("a missing git revision skips the revision check", freezeCaseMissingRevision)
	t.Run("a project that is not ready or missing gives CO-OBS-002", freezeCaseProjectNotReady)
	t.Run("verifyFrozen passes for the same generation and gives CO-OBS-008 after a change", freezeCaseVerifyFrozen)
	t.Run("capabilities", freezeCaseCapabilities)
}

func freezeCaseStableGeneration(t *testing.T) {
	_, p := setup(smallGraph(), 0)

	r1, err1 := p.Freeze(t.Context(), observation.FreezeRequest{Project: "p", GitRevision: "abc123"})
	r2, err2 := p.Freeze(t.Context(), observation.FreezeRequest{Project: "p", GitRevision: "abc123"})

	if err1 != nil || err2 != nil {
		t.Fatalf("freeze: %v %v", err1, err2)
	}

	if !regexp.MustCompile(`^cbm:p:[0-9a-f]{16}$`).MatchString(r1.GraphGeneration) {
		t.Fatalf("generation = %s", r1.GraphGeneration)
	}

	if r1.GraphGeneration != r2.GraphGeneration || r1.HeadSHA != "abc123" {
		t.Fatalf("refs = %+v %+v", r1, r2)
	}
}

func freezeCaseRevisionMismatch(t *testing.T) {
	_, p := setup(smallGraph(), 0)

	_, err := p.Freeze(t.Context(), observation.FreezeRequest{Project: "p", GitRevision: "zzz"})

	de := diagOf(t, err, "CO-OBS-008", diag.AdapterFailure)
	if !strings.Contains(de.Diagnostic.Message, "graph indexed at abc123, workspace at zzz; run concordia index") {
		t.Fatalf("message = %s", de.Diagnostic.Message)
	}
}

func freezeCaseIndexedRevisionWins(t *testing.T) {
	_, p := setup(smallGraph(), 0)

	if _, err := p.Freeze(t.Context(), observation.FreezeRequest{Project: "p", GitRevision: "zzz", IndexedRevision: "zzz"}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
}

func freezeCaseMissingRevision(t *testing.T) {
	_, p := setup(smallGraph(), 0)
	freeze(t, p)
}

func freezeCaseProjectNotReady(t *testing.T) {
	g := smallGraph()
	g.Status = "indexing"
	_, p := setup(g, 0)

	_, err := p.Freeze(t.Context(), observation.FreezeRequest{Project: "p"})
	wantDiag(t, err, "CO-OBS-002", diag.AdapterFailure)

	_, p = setup(smallGraph(), 0)
	_, err = p.Freeze(t.Context(), observation.FreezeRequest{Project: "other"})
	wantDiag(t, err, "CO-OBS-002", diag.NoExit)
}

func freezeCaseVerifyFrozen(t *testing.T) {
	g := smallGraph()
	_, p := setup(g, 0)
	ref := freeze(t, p)

	if err := p.VerifyFrozen(t.Context(), ref); err != nil {
		t.Fatalf("verifyFrozen: %v", err)
	}

	g.NodeBump = 1

	wantDiag(t, p.VerifyFrozen(t.Context(), ref), "CO-OBS-008", diag.AdapterFailure)
}

func freezeCaseCapabilities(t *testing.T) {
	_, p := setup(smallGraph(), 0)

	caps := p.Capabilities(t.Context())
	if caps.ExecutableVersion != "0.12.1" || caps.AdapterSchema != "concordia-cbm/v2" {
		t.Fatalf("caps = %+v", caps)
	}

	if !reflect.DeepEqual(caps.SupportedCanonicalKinds, observation.SupportedCanonicalKinds()) {
		t.Fatalf("kinds = %v", caps.SupportedCanonicalKinds)
	}
}

func fileNodes(n int) []testutil.FakeNode {
	nodes := make([]testutil.FakeNode, n)
	for i := range nodes {
		nodes[i] = testutil.FileNode(strconv.Itoa(i), fmt.Sprintf("d/f%04d.ts", i))
	}

	return nodes
}

func queryCalls(client *observation.FakeClient) []observation.FakeCall {
	return callsOfTool(client, "query_graph")
}

// callsOfTool gives the calls the fake client saw for one CBM tool.
func callsOfTool(client *observation.FakeClient, tool string) []observation.FakeCall {
	var calls []observation.FakeCall

	for _, c := range client.Calls() {
		if c.Tool == tool {
			calls = append(calls, c)
		}
	}

	return calls
}

// callHasArgs says whether the call carries every wanted argument.
func callHasArgs(call observation.FakeCall, want map[string]any) bool {
	for key, value := range want {
		if call.Args[key] != value {
			return false
		}
	}

	return true
}

// singleCallHasArgs says whether calls is exactly one call that carries every wanted argument.
func singleCallHasArgs(calls []observation.FakeCall, want map[string]any) bool {
	return len(calls) == 1 && callHasArgs(calls[0], want)
}

// mustListFilesCount lists the files of the snapshot and fails unless there are want of them.
func mustListFilesCount(t *testing.T, p *observation.CbmProvider, ref *observation.GraphSnapshotRef, want int) {
	t.Helper()

	files, err := p.ListFiles(t.Context(), ref)
	if err != nil || len(files) != want {
		t.Fatalf("listFiles: %d files, %v", len(files), err)
	}
}

// twoFileGraph is a graph of the two files a.ts and b.ts.
func twoFileGraph() *testutil.FakeGraph {
	return testutil.NewFakeGraph("p", []testutil.FakeNode{testutil.FileNode("1", "a.ts"), testutil.FileNode("2", "b.ts")}, nil)
}

// providerRewritingQueryResults is a provider over g whose query_graph results
// pass through rewrite, which gets the call arguments and the result map.
func providerRewritingQueryResults(g *testutil.FakeGraph, rewrite func(args, result map[string]any)) *observation.CbmProvider {
	client := observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		res, err := g.Handler(tool, args)
		if m, ok := res.(map[string]any); ok && tool == "query_graph" {
			rewrite(args, m)
		}

		return res, err
	}, "")

	return observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})
}

func TestListFiles(t *testing.T) {
	t.Run("one query lists all files with a large output budget and json", listFilesCaseOneQuery)
	t.Run("follows the cursor when max_rows cuts the result", listFilesCaseMaxRowsCursor)
	t.Run("follows the cursor when CBM cuts a page for another reason", listFilesCasePageCutCursor)
	t.Run("a cut page without a cursor gives CO-OBS-010", listFilesCaseCutWithoutCursor)
	t.Run("a count that does not agree gives CO-OBS-010", listFilesCaseCountDisagrees)
	t.Run("a total that is only a lower bound is not compared", listFilesCaseLowerBoundTotal)
}

func listFilesCaseOneQuery(t *testing.T) {
	client, p := setup(testutil.NewFakeGraph("p", fileNodes(2500), nil), 0)
	ref := freeze(t, p)

	mustListFilesCount(t, p, ref, 2500)

	// The fake cuts a page after 100 rows unless max_output_tokens is enlarged.
	calls := queryCalls(client)
	if len(calls) != 1 {
		t.Fatalf("query_graph calls = %d", len(calls))
	}

	if calls[0].Args["format"] != "json" || calls[0].Args["query"] != "MATCH (f:File) RETURN f.file_path" {
		t.Fatalf("args = %v", calls[0].Args)
	}

	// The second call uses the cache.
	if _, err := p.ListFiles(t.Context(), ref); err != nil {
		t.Fatalf("listFiles again: %v", err)
	}

	if n := len(queryCalls(client)); n != 1 {
		t.Fatalf("query_graph calls after the cached call = %d", n)
	}
}

func listFilesCaseMaxRowsCursor(t *testing.T) {
	client, p := setup(testutil.NewFakeGraph("p", fileNodes(2500), nil), 1000)
	ref := freeze(t, p)

	mustListFilesCount(t, p, ref, 2500)

	calls := queryCalls(client)
	if len(calls) != 3 {
		t.Fatalf("query_graph calls = %d", len(calls))
	}

	cursors := []any{calls[0].Args["cursor"], calls[1].Args["cursor"], calls[2].Args["cursor"]}
	if !reflect.DeepEqual(cursors, []any{nil, "q1.1000", "q1.2000"}) {
		t.Fatalf("cursors = %v %v %v", cursors...)
	}

	// Each page of the cursor chain asks for the same query, project and format.
	for _, c := range calls {
		if !callHasArgs(c, map[string]any{"query": calls[0].Args["query"], "project": "p", "format": "json"}) {
			t.Fatalf("args = %v", c.Args)
		}
	}
}

func listFilesCasePageCutCursor(t *testing.T) {
	g := testutil.NewFakeGraph("p", fileNodes(25), nil)
	g.PageRows = 7
	_, p := setup(g, 0)
	ref := freeze(t, p)

	mustListFilesCount(t, p, ref, 25)
}

func listFilesCaseCutWithoutCursor(t *testing.T) {
	g := testutil.NewFakeGraph("p", fileNodes(25), nil)
	g.PageRows, g.NoCursor = 7, true
	_, p := setup(g, 0)
	ref := freeze(t, p)

	_, err := p.ListFiles(t.Context(), ref)
	wantDiag(t, err, "CO-OBS-010", diag.AdapterFailure)
}

func listFilesCaseCountDisagrees(t *testing.T) {
	g := twoFileGraph()
	_, honest := setup(g, 0)
	ref := freeze(t, honest)

	lying := providerRewritingQueryResults(g, func(_, result map[string]any) {
		result["total"] = 3
	})

	_, err := lying.ListFiles(t.Context(), ref)
	wantDiag(t, err, "CO-OBS-010", diag.AdapterFailure)
}

func listFilesCaseLowerBoundTotal(t *testing.T) {
	g := twoFileGraph()
	_, honest := setup(g, 0)
	ref := freeze(t, honest)

	bounded := providerRewritingQueryResults(g, func(_, result map[string]any) {
		result["total"], result["total_relation"] = 3, "gte"
	})

	files, err := bounded.ListFiles(t.Context(), ref)
	if err != nil || len(files) != 2 {
		t.Fatalf("listFiles: %v %v", files, err)
	}
}

func TestSchemaAndStatusRequests(t *testing.T) {
	t.Run("freeze asks index_status with verbose json and pages get_graph_schema with full diagnostics", schemaCaseFreezeRequests)
	t.Run("the schema is read over all pages", schemaCaseReadOverAllPages)
}

func schemaCaseFreezeRequests(t *testing.T) {
	client, p := setup(smallGraph(), 0)

	ref := freeze(t, p)
	if ref.HeadSHA != "abc123" {
		t.Fatalf("ref = %+v", ref)
	}

	if ref.Nodes != 5 || ref.Edges != 4 {
		t.Fatalf("ref = %+v", ref)
	}

	status := callsOfTool(client, "index_status")
	if !singleCallHasArgs(status, map[string]any{"format": "json", "verbose": true}) {
		t.Fatalf("index_status calls = %+v", status)
	}

	schema := callsOfTool(client, "get_graph_schema")
	if !singleCallHasArgs(schema, map[string]any{"format": "json", "diagnostics": "full"}) {
		t.Fatalf("get_graph_schema calls = %+v", schema)
	}
}

func schemaCaseReadOverAllPages(t *testing.T) {
	g := smallGraph()
	_, whole := setup(g, 0)
	want := freeze(t, whole).Schema

	// A page of one entry: 3 labels (File, Function, Module) and 3 edge types are read in 6 pages.
	client := observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		if tool == "get_graph_schema" {
			args = map[string]any{"project": args["project"], "format": args["format"], "diagnostics": args["diagnostics"], "limit": 1, "offset": args["offset"]}
		}

		return g.Handler(tool, args)
	}, "")
	paged := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	got := freeze(t, paged)
	if !reflect.DeepEqual(got.Schema, want) || got.GraphGeneration == "" {
		t.Fatalf("paged schema = %+v, want %+v", got.Schema, want)
	}

	if pages := len(callsOfTool(client, "get_graph_schema")); pages != len(want.NodeLabels)+len(want.EdgeTypes) {
		t.Fatalf("pages = %d, want %d", pages, len(want.NodeLabels)+len(want.EdgeTypes))
	}
}

func TestCoverage(t *testing.T) {
	_, p := setup(smallGraph(), 0)
	ref := freeze(t, p)

	cov, err := p.Coverage(t.Context(), ref, &observation.Scope{
		WorkspaceFiles: []string{"src/a.ts", "src/b.ts", "src/c.ts"},
		Include:        []string{"src/**"},
	})
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}

	if cov.Total != 3 || cov.Covered != 2 {
		t.Fatalf("coverage = %+v", cov)
	}

	if !reflect.DeepEqual(cov.Unindexed, []string{"src/c.ts"}) {
		t.Fatalf("coverage = %+v", cov)
	}
}

// mustFetchEdges fetches the edges of the native kinds and fails on an error.
func mustFetchEdges(t *testing.T, p *observation.CbmProvider, ref *observation.GraphSnapshotRef, kinds ...string) observation.EvidenceQueryResult {
	t.Helper()

	r, err := p.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetchEdges: %v", err)
	}

	return r
}

func TestFetchEdges(t *testing.T) {
	t.Run("normalizes edges, sorts them, and counts unmapped native types", fetchEdgesCaseNormalizes)
	t.Run("always labels the source node and never uses ORDER BY, SKIP, or LIMIT", fetchEdgesCaseQueryShape)
	t.Run("only fetches the requested native types", fetchEdgesCaseRequestedTypes)
	t.Run("is deterministic: the same query plan digest each time", fetchEdgesCaseDeterministic)
	t.Run("a row count that does not agree with count(r) gives CO-OBS-010", fetchEdgesCaseRowCountDisagrees)
}

func fetchEdgesCaseNormalizes(t *testing.T) {
	_, p := setup(smallGraph(), 0)
	ref := freeze(t, p)

	r := mustFetchEdges(t, p, ref, "CALLS", "IMPORTS")

	requireEdgeSummaries(t, &r, []string{"CALLS:resolved:3", "CALLS:heuristic:4", "IMPORTS:syntactic:0"})

	if !reflect.DeepEqual(r.UnmappedNativeEdges, map[string]int{"HAS_BRANCH": 1}) {
		t.Fatalf("unmapped = %v", r.UnmappedNativeEdges)
	}

	if r.Edges[0].GraphGeneration != ref.GraphGeneration {
		t.Fatalf("generation = %s", r.Edges[0].GraphGeneration)
	}

	requireImportEndpoints(t, &r)

	if props := r.Edges[0].Properties; props["callee"] != "g" || props["strategy"] != "lsp_direct" {
		t.Fatalf("properties = %v", props)
	}

	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(r.QueryPlanDigest) {
		t.Fatalf("digest = %s", r.QueryPlanDigest)
	}
}

// requireEdgeSummaries fails unless the edges read `<native kind>:<confidence>:<line>` as wanted.
func requireEdgeSummaries(t *testing.T, r *observation.EvidenceQueryResult, want []string) {
	t.Helper()

	got := make([]string, len(r.Edges))
	for i, e := range r.Edges {
		got[i] = fmt.Sprintf("%s:%s:%d", e.NativeKind, e.Confidence, e.SourceSpan.Line)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
}

// requireImportEndpoints fails unless the third edge imports src/b.ts into src/a.ts.
func requireImportEndpoints(t *testing.T, r *observation.EvidenceQueryResult) {
	t.Helper()

	imp := r.Edges[2]

	from := r.Nodes[imp.From]
	if got := [3]string{from.Label, from.QualifiedName, from.FilePath}; got != [3]string{"File", "src.a.__file__", "src/a.ts"} {
		t.Fatalf("from = %+v", from)
	}

	if to := r.Nodes[imp.To]; to.Label != "Module" || to.QualifiedName != "src.b" {
		t.Fatalf("to = %+v", to)
	}
}

func fetchEdgesCaseQueryShape(t *testing.T) {
	g := smallGraph()
	_, p := setup(g, 0)
	ref := freeze(t, p)

	mustFetchEdges(t, p, ref, "CALLS", "IMPORTS")

	if len(g.Queries) == 0 {
		t.Fatal("no queries")
	}

	start := regexp.MustCompile(`^MATCH \(a:\w+\)-\[r:\w+\]->\(b\)`)
	banned := regexp.MustCompile(`ORDER BY|SKIP|LIMIT`)

	for _, q := range g.Queries {
		if !start.MatchString(q) || banned.MatchString(q) {
			t.Errorf("bad query: %s", q)
		}
	}
}

func fetchEdgesCaseRequestedTypes(t *testing.T) {
	g := smallGraph()
	_, p := setup(g, 0)
	ref := freeze(t, p)

	r := mustFetchEdges(t, p, ref, "IMPORTS")

	for _, e := range r.Edges {
		if e.NativeKind != "IMPORTS" {
			t.Errorf("edge %s", e.NativeKind)
		}
	}

	if queryContains(g, "r:CALLS") {
		t.Error("CALLS was queried")
	}
}

func fetchEdgesCaseDeterministic(t *testing.T) {
	_, p := setup(smallGraph(), 0)
	ref := freeze(t, p)

	a := mustFetchEdges(t, p, ref, "CALLS", "IMPORTS")
	b := mustFetchEdges(t, p, ref, "IMPORTS", "CALLS")

	if a.QueryPlanDigest != b.QueryPlanDigest {
		t.Fatal("digests differ")
	}

	if !reflect.DeepEqual(edgeIDs(&a), edgeIDs(&b)) {
		t.Fatal("edge IDs differ")
	}
}

func fetchEdgesCaseRowCountDisagrees(t *testing.T) {
	g := smallGraph()
	p := providerRewritingQueryResults(g, func(args, result map[string]any) {
		if strings.Contains(fmt.Sprint(args["query"]), "count(r)") {
			return
		}

		rows, _ := result["rows"].([][]any)
		result["rows"] = rows[1:]
	})
	ref := freeze(t, p)

	_, err := p.FetchEdges(t.Context(), ref, []string{"CALLS"})
	wantDiag(t, err, "CO-OBS-010", diag.NoExit)
}

func edgeIDs(r *observation.EvidenceQueryResult) []string {
	ids := make([]string, len(r.Edges))
	for i, e := range r.Edges {
		ids[i] = e.ID
	}

	return ids
}

// bigGraphDir is a top-level directory of bigGraph: its sub-directories hold the files that have count CALLS edges each.
type bigGraphDir struct {
	name  string
	subs  []string
	count int
}

// filePath gives the file of the sub-directory; a name that is a file stays as it is.
func (d bigGraphDir) filePath(sub string) string {
	if strings.HasSuffix(d.name, ".ts") {
		return d.name
	}

	return d.name + "/" + sub + "file.ts"
}

func bigGraphDirs(deep bool) []bigGraphDir {
	pkgA := bigGraphDir{name: "pkg-a", subs: []string{""}, count: 50}
	if deep {
		pkgA.subs, pkgA.count = []string{"x/", "y/"}, 60
	}

	return []bigGraphDir{
		pkgA,
		{name: "pkg-b", subs: []string{""}, count: 50},
		{name: "root.ts", subs: []string{""}, count: 50},
	}
}

// calls makes the CALLS edges of the file n in the sub-directory, from Function nodes to target.
func (d bigGraphDir) calls(n int, sub string, target *testutil.FakeNode) []testutil.FakeEdge {
	filePath := d.filePath(sub)
	edges := make([]testutil.FakeEdge, 0, d.count)

	for i := range d.count {
		src := testutil.FakeNode{ID: fmt.Sprintf("n%d-%d", n, i), Label: "Function", QN: fmt.Sprintf("%s.%sfn%d", d.name, sub, i), Path: filePath, Line: i + 1}
		edges = append(edges, testutil.FakeEdge{
			Type: "CALLS", From: &src, To: target,
			Props: map[string]string{"strategy": "lsp_direct", "confidence": "0.9", "line": strconv.Itoa(i + 1)},
		})
	}

	return edges
}

// bigGraph has CALLS edges from Function nodes in 3 top-level directories.
func bigGraph(deep bool) *testutil.FakeGraph {
	target := testutil.FakeNode{ID: "t", Label: "Function", QN: "t", Path: "t/t.ts", Line: 1}
	nodes := make([]testutil.FakeNode, 0, 8)
	nodes = append(nodes, target, testutil.FileNode("fa", "t/t.ts"))

	edges := make([]testutil.FakeEdge, 0, 256)

	n := 0

	for _, dir := range bigGraphDirs(deep) {
		for _, sub := range dir.subs {
			nodes = append(nodes, testutil.FileNode(fmt.Sprintf("f%d", n), dir.filePath(sub)))
			edges = append(edges, dir.calls(n, sub, &target)...)

			n++
		}
	}

	return testutil.NewFakeGraph("p", nodes, edges)
}

func queryContains(g *testutil.FakeGraph, part string) bool {
	return slices.ContainsFunc(g.Queries, func(q string) bool { return strings.Contains(q, part) })
}

func TestFetchEdgesDividePath(t *testing.T) {
	t.Run("divides by the first path segment and checks the sum", dividePathCaseFirstSegment)
	t.Run("divides again at the next segment when a part is still too large", dividePathCaseNextSegment)
	t.Run("a sum that does not agree gives CO-OBS-010", dividePathCaseSumDisagrees)
	t.Run("a path with a quote gives CO-OBS-011", dividePathCaseQuotedPath)
}

// fetchCallsDivided fetches the CALLS edges of g with a row budget of 100.
func fetchCallsDivided(t *testing.T, g *testutil.FakeGraph) (observation.EvidenceQueryResult, error) {
	t.Helper()

	_, p := setup(g, 100)
	ref := freeze(t, p)

	return p.FetchEdges(t.Context(), ref, []string{"CALLS"})
}

func dividePathCaseFirstSegment(t *testing.T) {
	g := bigGraph(false)

	r, err := fetchCallsDivided(t, g)
	if err != nil {
		t.Fatalf("fetchEdges: %v", err)
	}

	if len(r.Edges) != 150 {
		t.Fatalf("edges = %d", len(r.Edges))
	}

	if !queryContains(g, "STARTS WITH 'pkg-a/'") || !queryContains(g, "a.file_path = 'root.ts'") {
		t.Fatalf("queries = %v", g.Queries)
	}
}

func dividePathCaseNextSegment(t *testing.T) {
	g := bigGraph(true) // pkg-a has 120 edges in x/ and y/

	r, err := fetchCallsDivided(t, g)
	if err != nil {
		t.Fatalf("fetchEdges: %v", err)
	}

	if len(r.Edges) != 220 {
		t.Fatalf("edges = %d", len(r.Edges))
	}

	if !queryContains(g, "STARTS WITH 'pkg-a/x/'") {
		t.Fatalf("queries = %v", g.Queries)
	}
}

func dividePathCaseSumDisagrees(t *testing.T) {
	g := bigGraph(false)
	// An edge whose source file is not a File node of the graph: no part contains it.
	ghost := testutil.FakeNode{ID: "gx", Label: "Function", QN: "ghost", Path: "ghost/g.ts", Line: 1}
	g.Edges = append(g.Edges, testutil.FakeEdge{
		Type: "CALLS", From: &ghost, To: &g.Nodes[0],
		Props: map[string]string{"strategy": "lsp_direct", "confidence": "0.9", "line": "1"},
	})

	_, err := fetchCallsDivided(t, g)
	wantDiag(t, err, "CO-OBS-010", diag.AdapterFailure)
}

func dividePathCaseQuotedPath(t *testing.T) {
	g := bigGraph(false)
	g.Nodes = append(g.Nodes, testutil.FileNode("q", "it's/x.ts"))

	_, err := fetchCallsDivided(t, g)
	wantDiag(t, err, "CO-OBS-011", diag.AdapterFailure)
}
