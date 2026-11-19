package observation

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/logger"
)

const (
	// argProject is the argument that names the CBM project.
	argProject = "project"
	// ArgFormat is the argument that selects the output format of a CBM tool.
	// The CBM 0.12 CLI gives text unless the arguments have `"format": "json"`.
	ArgFormat = "format"
	// FormatJSON is the ArgFormat value for JSON output.
	FormatJSON = "json"
	// cbmMaxRows is the largest max_rows of one `query_graph` page that CBM 0.12 accepts.
	cbmMaxRows = 99_998
	// outputBudget is max_output_tokens of a query: CBM cuts a page at this
	// sizing hint, and the default (3200 tokens) cuts large results.
	outputBudget = 10_000_000
	// schemaPage is the number of labels and edge types of one `get_graph_schema` page.
	schemaPage = 200
	// fileQuery lists the paths of all File nodes.
	fileQuery = "MATCH (f:File) RETURN f.file_path"
	// totalExact is the `total_relation` of an exact total.
	totalExact = "eq"
	// Offsets of the columns of one node: id, labels, qualified name, file path, start line.
	colFilePath         = 3
	colStartLine        = 4
	generationHexLength = 16

	// The first columns of an edge query: id, labels, qualified name, file path
	// and start line of each of the two nodes. The edge properties follow.
	nodeColumnCount = 5
	toNodeOffset    = nodeColumnCount
	propsOffset     = 2 * nodeColumnCount
)

// flexNumber is a JSON number that can also be written as a string. Other
// values (and null) are 0.
type flexNumber float64

// UnmarshalJSON reads a number or a numeric string.
func (n *flexNumber) UnmarshalJSON(data []byte) error {
	*n = 0

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("read number: %w", err)
	}

	*n = flexNumber(numberOf(v))

	return nil
}

// numberOf converts a JSON value to a finite number. Everything else is 0.
func numberOf(v any) float64 {
	var num float64

	switch val := v.(type) {
	case float64:
		num = val
	case json.Number:
		num, _ = val.Float64()
	case string:
		text := strings.TrimSpace(val)
		if text == "" {
			return 0
		}

		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0
		}

		num = parsed
	default:
		return 0
	}

	if num-num != 0 { // NaN or infinite
		return 0
	}

	return num
}

type gitStatus struct {
	HeadSHA *string `json:"head_sha"`
}

type indexStatus struct {
	Git     *gitStatus `json:"git"`
	Project string     `json:"project"`
	Status  string     `json:"status"`
	Nodes   flexNumber `json:"nodes"`
	Edges   flexNumber `json:"edges"`
}

type rawLabel struct {
	Label      string     `json:"label"`
	Properties []string   `json:"properties"`
	Count      flexNumber `json:"count"`
}

type rawEdgeType struct {
	Type       string     `json:"type"`
	Properties []string   `json:"properties"`
	Count      flexNumber `json:"count"`
}

type rawSchema struct {
	NodeLabels []rawLabel    `json:"node_labels"`
	EdgeTypes  []rawEdgeType `json:"edge_types"`
	// HasMore and NextOffset page the sequence of labels followed by edge types.
	HasMore    bool       `json:"has_more"`
	NextOffset flexNumber `json:"next_offset"`
}

// queryResult is one page of a `query_graph` result; runQuery gives the pages joined.
type queryResult struct {
	// TotalRelation says whether Total is exact ("eq") or a lower bound.
	TotalRelation    string     `json:"total_relation"`
	TruncationReason string     `json:"truncation_reason"`
	NextCursor       string     `json:"next_cursor"`
	Columns          []string   `json:"columns"`
	Rows             [][]any    `json:"rows"`
	Total            flexNumber `json:"total"`
	HasMore          bool       `json:"has_more"`
	Truncated        bool       `json:"truncated"`
}

// CbmProviderOptions are the settings of a CbmProvider.
type CbmProviderOptions struct {
	Client Client
	// Logger gets trace events. May be nil.
	Logger *logger.Logger
	// ExecutableVersion is the version text of the executable (`0.12.1`).
	ExecutableVersion string
	// MaxRows is the maximum number of rows of one query, and the number of
	// rows of one page. Default 99998 (the CBM limit). Tests make it smaller.
	MaxRows int
}

type fileCache struct {
	generation string
	files      []string
}

// CbmProvider is the observation provider on top of codebase-memory-mcp
// (adapter schema `concordia-cbm/v2`).
type CbmProvider struct {
	client Client
	log    *logger.Logger
	cache  *fileCache
	// schemas are the graph schemas of the frozen projects, for VerifyProfile.
	schemas map[string]GraphSchema
	version string
	maxRows int

	schemaMu sync.Mutex
}

var _ Provider = (*CbmProvider)(nil)

// NewCbmProvider makes a CbmProvider.
func NewCbmProvider(opts CbmProviderOptions) *CbmProvider {
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = cbmMaxRows
	}

	return &CbmProvider{client: opts.Client, log: opts.Logger, version: opts.ExecutableVersion, maxRows: maxRows}
}

func (p *CbmProvider) trace(ev *logger.TraceEvent) {
	if p.log != nil {
		p.log.Trace(*ev)
	}
}

// recordSchema keeps the graph schema of a frozen project.
func (p *CbmProvider) recordSchema(project string, schema GraphSchema) {
	p.schemaMu.Lock()
	defer p.schemaMu.Unlock()

	if p.schemas == nil {
		p.schemas = map[string]GraphSchema{}
	}

	p.schemas[project] = schema
}

// Capabilities gives the version, the adapter schema, the canonical kinds that
// the adapter supplies, and the capability profile of the build with the
// support of each family. The families come from the profile that is pinned
// to this build; VerifyProfile compares it with the graph schemas of the
// projects frozen so far (call Capabilities again after Freeze to include
// them). A build without a profile supplies the code families only and gets a
// CO-OBS-017 warning.
func (p *CbmProvider) Capabilities(context.Context) Capabilities {
	caps := Capabilities{
		ExecutableVersion:       p.version,
		AdapterSchema:           AdapterSchema,
		SupportedCanonicalKinds: SupportedCanonicalKinds(),
	}

	profile, ok := ProfileFor(p.version, AdapterSchema)
	if !ok {
		caps.Families = NoProfileFamilies()
		caps.Diagnostics = []diag.Diagnostic{diag.New(diag.OBS017, diag.Params{
			"profile": "none",
			paramDetail: fmt.Sprintf("no capability profile for codebase-memory-mcp %s with adapter schema %s; infrastructure, documentation, diagram and test families are unsupported",
				p.version, AdapterSchema),
		})}

		return caps
	}

	caps.Profile = profile.Ref()

	p.schemaMu.Lock()
	schemas := make([]GraphSchema, 0, len(p.schemas))

	for _, name := range slices.Sorted(maps.Keys(p.schemas)) {
		schemas = append(schemas, p.schemas[name])
	}
	p.schemaMu.Unlock()

	if len(schemas) == 0 {
		caps.Families = DeclaredFamilies(profile)

		return caps
	}

	verified := VerifyProfile(profile, MergeSchemas(schemas...))
	caps.Families = verified.Families
	caps.Diagnostics = verified.Diagnostics

	return caps
}

func normalizeSchema(raw rawSchema) GraphSchema {
	schema := GraphSchema{
		NodeLabels: make([]SchemaLabel, len(raw.NodeLabels)),
		EdgeTypes:  make([]SchemaEdgeType, len(raw.EdgeTypes)),
	}

	for i, l := range raw.NodeLabels {
		schema.NodeLabels[i] = SchemaLabel{Label: l.Label, Count: float64(l.Count), Properties: nonNil(l.Properties)}
	}

	for i, t := range raw.EdgeTypes {
		schema.EdgeTypes[i] = SchemaEdgeType{Type: t.Type, Count: float64(t.Count), Properties: nonNil(t.Properties)}
	}

	slices.SortStableFunc(schema.NodeLabels, func(a, b SchemaLabel) int { return canonjson.CompareUTF16(a.Label, b.Label) })
	slices.SortStableFunc(schema.EdgeTypes, func(a, b SchemaEdgeType) int { return canonjson.CompareUTF16(a.Type, b.Type) })

	return schema
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}

	return list
}

func generationOf(project string, status *indexStatus, schema GraphSchema) string {
	labels := make([][]any, len(schema.NodeLabels))
	for i, l := range schema.NodeLabels {
		labels[i] = []any{l.Label, l.Count}
	}

	types := make([][]any, len(schema.EdgeTypes))
	for i, t := range schema.EdgeTypes {
		types[i] = []any{t.Type, t.Count}
	}

	var headSHA any
	if status.Git != nil && status.Git.HeadSHA != nil {
		headSHA = *status.Git.HeadSHA
	}

	sum := digest.SHA256HexString(canonjson.MustString(map[string]any{
		"nodes":    float64(status.Nodes),
		"edges":    float64(status.Edges),
		"head_sha": headSHA,
		"schema":   map[string]any{"node_labels": labels, "edge_types": types},
	}))

	return "cbm:" + project + ":" + sum[:generationHexLength]
}

type state struct {
	status     *indexStatus
	generation string
	schema     GraphSchema
}

// readSchema reads all pages of `get_graph_schema`. The pages hold the node
// labels first and the edge types after them, in one sequence.
func (p *CbmProvider) readSchema(ctx context.Context, project string) (rawSchema, error) {
	var all rawSchema

	for offset := 0; ; {
		page, err := CallAs[rawSchema](ctx, p.client, "get_graph_schema", map[string]any{
			argProject:    project,
			ArgFormat:     FormatJSON,
			"diagnostics": "full", // adds the properties of each label and edge type
			"limit":       schemaPage,
			"offset":      offset,
		})
		if err != nil {
			return rawSchema{}, err
		}

		all.NodeLabels = append(all.NodeLabels, page.NodeLabels...)
		all.EdgeTypes = append(all.EdgeTypes, page.EdgeTypes...)

		if !page.HasMore {
			return all, nil
		}

		next := int(page.NextOffset)
		if next <= offset {
			return rawSchema{}, diag.Fail(diag.OBS001, diag.Detail("get_graph_schema: unexpected response: the next page does not advance"))
		}

		offset = next
	}
}

func (p *CbmProvider) readState(ctx context.Context, project string) (state, error) {
	// verbose adds the git block with head_sha.
	status, err := CallAs[indexStatus](ctx, p.client, "index_status", map[string]any{
		argProject: project,
		ArgFormat:  FormatJSON,
		"verbose":  true,
	})
	if err != nil {
		return state{}, err
	}

	if status.Status != "ready" {
		shown := status.Status
		if shown == "" {
			shown = "unknown"
		}

		return state{}, diag.Fail(diag.OBS002, diag.Detail(fmt.Sprintf("project '%s' has status '%s'", project, shown)))
	}

	raw, err := p.readSchema(ctx, project)
	if err != nil {
		return state{}, err
	}

	schema := normalizeSchema(raw)

	return state{status: &status, schema: schema, generation: generationOf(project, &status, schema)}, nil
}

// headSHAOf gives the git head of the index status, or "" when it has none.
func headSHAOf(status *indexStatus) string {
	if status.Git == nil || status.Git.HeadSHA == nil {
		return ""
	}

	return *status.Git.HeadSHA
}

// checkIndexedRevision gives CO-OBS-008 when the graph was indexed at another
// git revision than the workspace. An unknown revision cannot be compared.
func checkIndexedRevision(req *FreezeRequest, headSHA string) error {
	indexedAt := cmp.Or(req.IndexedRevision, headSHA)
	if req.GitRevision == "" || indexedAt == "" {
		return nil
	}

	if indexedAt == req.GitRevision {
		return nil
	}

	return diag.Fail(diag.OBS008, diag.Detail(
		fmt.Sprintf("graph indexed at %s, workspace at %s; run concordia index", indexedAt, req.GitRevision)))
}

// Freeze freezes the graph generation for one run. It gives CO-OBS-002 when
// the project is missing or not ready, and CO-OBS-008 when the graph was
// indexed at another git revision.
func (p *CbmProvider) Freeze(ctx context.Context, req FreezeRequest) (*GraphSnapshotRef, error) {
	snap, err := p.readState(ctx, req.Project)
	if err != nil {
		return nil, err
	}

	headSHA := headSHAOf(snap.status)
	if err := checkIndexedRevision(&req, headSHA); err != nil {
		return nil, err
	}

	p.recordSchema(req.Project, snap.schema)

	nodes, edges := int(snap.status.Nodes), int(snap.status.Edges)
	p.trace(&logger.TraceEvent{
		Phase:  "observation",
		Event:  "freeze",
		Counts: map[string]int64{"nodes": int64(nodes), "edges": int64(edges)},
	})

	return &GraphSnapshotRef{
		Project:         req.Project,
		GraphGeneration: snap.generation,
		HeadSHA:         headSHA,
		Schema:          snap.schema,
		Nodes:           nodes,
		Edges:           edges,
	}, nil
}

// VerifyFrozen checks again, after all queries, that the graph generation did not change.
func (p *CbmProvider) VerifyFrozen(ctx context.Context, ref *GraphSnapshotRef) error {
	snap, err := p.readState(ctx, ref.Project)
	if err != nil {
		return err
	}

	if snap.generation != ref.GraphGeneration {
		return diag.Fail(diag.OBS008, diag.Detail(
			fmt.Sprintf("graph changed during the run: %s -> %s", ref.GraphGeneration, snap.generation)))
	}

	return nil
}

// ListFiles gives all File node paths (sorted, "/" separators), read with
// one paged `query_graph`. It gives CO-OBS-010 when the count does not agree.
func (p *CbmProvider) ListFiles(ctx context.Context, ref *GraphSnapshotRef) ([]string, error) {
	if p.cache != nil && p.cache.generation == ref.GraphGeneration {
		return slices.Clone(p.cache.files), nil
	}

	res, err := p.runQuery(ctx, ref.Project, fileQuery, p.maxRows)
	if err != nil {
		return nil, err
	}

	files := filePathsOf(res.Rows)
	if err := checkFileTotal(files, &res); err != nil {
		return nil, err
	}

	slices.SortFunc(files, canonjson.CompareUTF16)
	p.cache = &fileCache{generation: ref.GraphGeneration, files: files}

	return slices.Clone(files), nil
}

// filePathsOf gives the non-empty file paths of the rows of fileQuery.
func filePathsOf(rows [][]any) []string {
	files := make([]string, 0, len(rows))

	for _, row := range rows {
		if filePath, _ := cell(row, 0).(string); filePath != "" {
			files = append(files, filePath)
		}
	}

	return files
}

// checkFileTotal gives CO-OBS-010 when the files do not agree with the total
// of the result. A total that is only a lower bound cannot be compared.
func checkFileTotal(files []string, res *queryResult) error {
	if res.TotalRelation != "" && res.TotalRelation != totalExact {
		return nil
	}

	if len(files) == int(res.Total) {
		return nil
	}

	return diag.Fail(diag.OBS010, diag.Detail(
		fmt.Sprintf("listFiles collected %d File nodes, CBM reports %d", len(files), int(res.Total))))
}

// Coverage gives the file coverage of a scope against the File nodes of the frozen graph.
func (p *CbmProvider) Coverage(ctx context.Context, ref *GraphSnapshotRef, scope *Scope) (FileCoverage, error) {
	cbmFiles, err := p.ListFiles(ctx, ref)
	if err != nil {
		return FileCoverage{}, err
	}

	return ComputeFileCoverage(&FileCoverageInput{
		WorkspaceFiles: scope.WorkspaceFiles,
		Include:        scope.Include,
		Exclude:        scope.Exclude,
		CbmFiles:       cbmFiles,
		DirtyFiles:     scope.DirtyFiles,
		Sizes:          scope.Sizes,
		ScopeGlobs:     scope.ScopeGlobs,
	}), nil
}

// Query gives the edges of the plan.
func (p *CbmProvider) Query(ctx context.Context, ref *GraphSnapshotRef, plan EdgeQueryPlan) ([]*EvidenceEdge, error) {
	res, err := p.FetchEdges(ctx, ref, plan.NativeKinds)
	if err != nil {
		return nil, err
	}

	return res.Edges, nil
}

// queryArgs are the arguments of a `query_graph` call.
func queryArgs(project, cypher string, maxRows int) map[string]any {
	args := map[string]any{
		argProject:          project,
		"query":             cypher,
		ArgFormat:           FormatJSON,
		"max_output_tokens": outputBudget,
	}
	if maxRows > 0 {
		args["max_rows"] = maxRows
	}

	return args
}

// cursorAdvances says whether a cut page gives a cursor that continues the
// result. A cursor that does not advance would repeat the same page forever.
func cursorAdvances(page *queryResult, previous any) bool {
	if page.NextCursor == "" || len(page.Rows) == 0 {
		return false
	}

	return page.NextCursor != previous
}

// runQuery runs a read-only Cypher query with `query_graph` and collects all
// pages of the result. CBM cuts a page at max_rows and at its output budget;
// a cut page has `has_more` and a `next_cursor`, and the next call with the
// cursor continues the same snapshot. A cut page without a cursor cannot be
// continued: the result is incomplete and gives CO-OBS-010. maxRows 0 leaves
// max_rows to CBM (default 200).
func (p *CbmProvider) runQuery(ctx context.Context, project, cypher string, maxRows int) (queryResult, error) {
	args := queryArgs(project, cypher, maxRows)

	var all queryResult

	for {
		page, err := CallAs[queryResult](ctx, p.client, "query_graph", args)
		if err != nil {
			return queryResult{}, err
		}

		all.Columns = page.Columns
		all.Rows = append(all.Rows, page.Rows...)
		all.Total, all.TotalRelation = page.Total, page.TotalRelation

		if !page.HasMore && !page.Truncated {
			return all, nil
		}

		if !cursorAdvances(&page, args["cursor"]) {
			return queryResult{}, diag.Fail(diag.OBS010, diag.Detail(fmt.Sprintf(
				"query result cut (%s) after %d of %d rows and CBM gives no way to continue: %s",
				page.TruncationReason, len(all.Rows), int(page.Total), shortQuery(cypher))))
		}

		// A new map: a FakeClient keeps the arguments of each call.
		args = maps.Clone(args)
		args["cursor"] = page.NextCursor
	}
}

const (
	queryShown = 80
	// paramKind and paramCount are the parameters of the diagnostics of a fetch.
	paramKind  = "kind"
	paramCount = "count"
	// paramDetail is the free-text parameter of the OBS-017 and OBS-019 diagnostics.
	paramDetail = "detail"
)

func shortQuery(q string) string {
	if runes := []rune(q); len(runes) > queryShown {
		return string(runes[:queryShown]) + "…"
	}

	return q
}

func cell(row []any, i int) any {
	if i < len(row) {
		return row[i]
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	return keys
}
