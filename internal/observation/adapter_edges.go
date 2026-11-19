package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/logger"
)

// quotePath quotes a path for a Cypher string. A path with `'` or `\` cannot
// be put in a query (CO-OBS-011).
func quotePath(p string) (string, error) {
	if strings.ContainsAny(p, `'\`) {
		return "", diag.Fail(diag.OBS011, diag.Params{"path": p})
	}

	return "'" + p + "'", nil
}

// edgeFetch is the state of one FetchEdges call.
type edgeFetch struct {
	p           *CbmProvider
	ref         *GraphSnapshotRef
	nodes       map[string]ObservedNode
	analyzer    AnalyzerDescriptor
	digests     []string
	edges       []*EvidenceEdge
	unsupported []*EvidenceEdge
}

// edgePart is the query of one native edge type from one node label.
type edgePart struct {
	nativeKind string
	nodeLabel  string
	match      string
	returns    string
	// propNames are the edge properties that have a column `r.<name>`, in column order.
	propNames []string
	// jsonProps are the edge properties that have no column of their own (see
	// unqueryableProps) and are read from the column `properties(r)`.
	jsonProps []string
	// targetKind says that the query has the column `b.kind`.
	targetKind bool
	// mapped is false for a native type that the rule table does not know.
	mapped bool
}

// targetKindColumn is the column of `b.kind`, after the property columns.
func (part *edgePart) targetKindColumn() int {
	return propsOffset + len(part.propNames)
}

// jsonPropsColumn is the column of `properties(r)`, after `b.kind` when present.
func (part *edgePart) jsonPropsColumn() int {
	col := part.targetKindColumn()
	if part.targetKind {
		col++
	}

	return col
}

// readProps reads the edge properties of one row: the columns of their own and
// the ones that come from `properties(r)`.
func (part *edgePart) readProps(row []any) (map[string]string, error) {
	props := make(map[string]string, len(part.propNames)+len(part.jsonProps))

	for i, name := range part.propNames {
		if v := cell(row, propsOffset+i); v != nil {
			props[name] = stringOf(v)
		}
	}

	if len(part.jsonProps) == 0 {
		return props, nil
	}

	if err := readJSONProps(props, part.jsonProps, cell(row, part.jsonPropsColumn())); err != nil {
		return nil, diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s from %s: %v", part.nativeKind, part.nodeLabel, err)))
	}

	return props, nil
}

// pathScope is a set of source files below a path prefix.
type pathScope struct {
	prefix string
	files  []string
}

// pathSegment is the files of a scope below one path segment: a directory
// (path ends with "/") or one file.
type pathSegment struct {
	path  string
	files []string
	dir   bool
}

// whereClause restricts a query to the files of the segment.
func (s *pathSegment) whereClause() (string, error) {
	quoted, err := quotePath(s.path)
	if err != nil {
		return "", err
	}

	if s.dir {
		return " WHERE a.file_path STARTS WITH " + quoted, nil
	}

	return " WHERE a.file_path = " + quoted, nil
}

// segmentKey is the first path segment of rest, with its "/" for a directory.
func segmentKey(rest string) string {
	if slash := strings.Index(rest, "/"); slash != -1 {
		return rest[:slash+1]
	}

	return rest
}

// splitByPathSegment groups the files of a scope by their next path segment,
// in UTF-16 order of the segment.
func splitByPathSegment(scope pathScope) []pathSegment {
	groups := map[string][]string{}

	for _, file := range scope.files {
		key := segmentKey(strings.TrimPrefix(file, scope.prefix))
		groups[key] = append(groups[key], file)
	}

	segments := make([]pathSegment, 0, len(groups))

	for _, key := range sortedKeys(groups) {
		segments = append(segments, pathSegment{
			path:  scope.prefix + key,
			files: groups[key],
			dir:   strings.HasSuffix(key, "/"),
		})
	}

	return segments
}

func (f *edgeFetch) query(ctx context.Context, q string, maxRows int) (queryResult, error) {
	sum := digest.SHA256HexString(q)
	f.digests = append(f.digests, sum)

	res, err := f.p.runQuery(ctx, f.ref.Project, q, maxRows)
	if err != nil {
		return queryResult{}, err
	}

	f.p.trace(&logger.TraceEvent{
		Phase:       "observation",
		Event:       "query",
		QueryDigest: sum,
		Counts:      map[string]int64{"rows": int64(len(res.Rows))},
	})

	return res, nil
}

// countQuery runs a query that returns one count and gives that count. A
// result without a cell counts 0.
func (f *edgeFetch) countQuery(ctx context.Context, q string) (int, error) {
	res, err := f.query(ctx, q, 0)
	if err != nil {
		return 0, err
	}

	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return 0, nil
	}

	return int(numberOf(res.Rows[0][0])), nil
}

func (f *edgeFetch) countOf(ctx context.Context, match, where string) (int, error) {
	return f.countQuery(ctx, match+where+" RETURN count(r)")
}

func nodeColumnsAt(row []any, offset int) *NodeColumns {
	return &NodeColumns{
		ID:            cell(row, offset),
		Labels:        cell(row, offset+1),
		QualifiedName: cell(row, offset+2),
		FilePath:      cell(row, offset+colFilePath),
		StartLine:     cell(row, offset+colStartLine),
	}
}

// partRowMismatch is the CO-OBS-010 error of a part query that gave another number of rows than its count.
func partRowMismatch(part *edgePart, where string, got, expected int) error {
	shown := ""
	if where != "" {
		shown = " " + strings.TrimSpace(where)
	}

	return diag.Fail(diag.OBS010, diag.Detail(fmt.Sprintf("%s from %s%s: got %d rows, count is %d",
		part.nativeKind, part.nodeLabel, shown, got, expected)))
}

// fetchPart runs one part query and checks the row count against expected.
func (f *edgeFetch) fetchPart(ctx context.Context, part *edgePart, where string, expected int) (int, error) {
	res, err := f.query(ctx, part.match+where+" RETURN "+part.returns, f.p.maxRows)
	if err != nil {
		return 0, err
	}

	if len(res.Rows) != expected {
		return 0, partRowMismatch(part, where, len(res.Rows), expected)
	}

	for _, row := range res.Rows {
		if err := f.addRow(part, row); err != nil {
			return 0, err
		}
	}

	return len(res.Rows), nil
}

// addRow turns one row of a part query into a normalized fact, or an unsupported fact.
func (f *edgeFetch) addRow(part *edgePart, row []any) error {
	from := ToObservedNode(f.ref.Project, nodeColumnsAt(row, 0), part.nodeLabel)
	target := ToObservedNode(f.ref.Project, nodeColumnsAt(row, toNodeOffset), "")

	if part.targetKind {
		target.Kind = stringOf(cell(row, part.targetKindColumn()))
	}

	f.putNode(&from)
	f.putNode(&target)

	props, err := part.readProps(row)
	if err != nil {
		return err
	}

	f.record(part, &NormalizeInput{
		NativeKind:      part.nativeKind,
		From:            from,
		To:              target,
		Properties:      props,
		GraphGeneration: f.ref.GraphGeneration,
		Analyzer:        f.analyzer,
	})

	return nil
}

// record keeps the normalized fact of a row: an unsupported fact for a native
// type that the rule table does not know.
func (f *edgeFetch) record(part *edgePart, input *NormalizeInput) {
	if !part.mapped {
		f.unsupported = append(f.unsupported, NormalizeUnsupported(input))

		return
	}

	if edge, ok := NormalizeEdge(input); ok {
		f.edges = append(f.edges, edge)
	}
}

// divide splits a part with more rows than one query can give by the path
// segments of the source files. It gives the number of collected rows.
func (f *edgeFetch) divide(ctx context.Context, part *edgePart, scope pathScope) (int, error) {
	collected := 0

	for _, segment := range splitByPathSegment(scope) {
		got, err := f.collectSegment(ctx, part, &segment)
		if err != nil {
			return 0, err
		}

		collected += got
	}

	return collected, nil
}

// collectSegment collects the rows of one path segment: at once when they fit
// in one query, else by dividing a directory further.
func (f *edgeFetch) collectSegment(ctx context.Context, part *edgePart, segment *pathSegment) (int, error) {
	where, err := segment.whereClause()
	if err != nil {
		return 0, err
	}

	count, err := f.countOf(ctx, part.match, where)
	if err != nil {
		return 0, err
	}

	switch {
	case count == 0:
		return 0, nil
	case count <= f.p.maxRows:
		return f.fetchPart(ctx, part, where, count)
	case segment.dir:
		return f.divide(ctx, part, pathScope{prefix: segment.path, files: segment.files})
	default:
		return 0, diag.Fail(diag.OBS010, diag.Detail(fmt.Sprintf("%s from %s in %s: %d rows, more than %d in one file",
			part.nativeKind, part.nodeLabel, segment.path, count, f.p.maxRows)))
	}
}

// fetchLabel fetches the edges of one native type from one node label.
func (f *edgeFetch) fetchLabel(ctx context.Context, part *edgePart) error {
	total, err := f.countOf(ctx, part.match, "")
	if err != nil || total == 0 {
		return err
	}

	if total <= f.p.maxRows {
		_, err = f.fetchPart(ctx, part, "", total)

		return err
	}

	// More rows than one query can give: divide by path segments of the source file.
	files, err := f.p.ListFiles(ctx, f.ref)
	if err != nil {
		return err
	}

	collected, err := f.divide(ctx, part, pathScope{files: files})
	if err != nil {
		return err
	}

	if collected != total {
		return diag.Fail(diag.OBS010, diag.Detail(fmt.Sprintf("%s from %s: parts give %d rows, count is %d",
			part.nativeKind, part.nodeLabel, collected, total)))
	}

	return nil
}

func isIDColumn(name string) bool {
	return name == "source_id" || name == "target_id"
}

// unqueryableProps are edge properties that CBM 0.12.1 cannot read with
// `r.<name>`: the query does not parse (`count` is a function name). The
// adapter reads them from the column `properties(r)`.
var unqueryableProps = []string{paramCount}

// returnColumns gives the columns of an edge query: the two nodes, the edge
// properties with a column of their own, `b.kind` when targetKind is set, and
// `properties(r)` when jsonProps is set.
func returnColumns(propNames []string, targetKind, jsonProps bool) string {
	cols := make([]string, 0, nodeColumnCount*2+len(propNames)+2)
	cols = append(cols,
		"id(a)", "labels(a)", "a.qualified_name", "a.file_path", "a.start_line",
		"id(b)", "labels(b)", "b.qualified_name", "b.file_path", "b.start_line",
	)

	for _, p := range propNames {
		cols = append(cols, "r."+p)
	}

	if targetKind {
		cols = append(cols, "b.kind")
	}

	if jsonProps {
		cols = append(cols, "properties(r)")
	}

	return strings.Join(cols, ", ")
}

// readJSONProps reads the named properties from a `properties(r)` cell (a JSON object in a string).
func readJSONProps(props map[string]string, names []string, raw any) error {
	text, ok := raw.(string)
	if !ok || text == "" {
		return nil
	}

	var all map[string]any

	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()

	if err := decoder.Decode(&all); err != nil {
		return fmt.Errorf("edge properties are not a JSON object: %w", err)
	}

	for _, name := range names {
		if v, present := all[name]; present && v != nil {
			props[name] = stringOf(v)
		}
	}

	return nil
}

// putNode keeps a node of a row. A node can be the source of one row (no kind
// column) and the target of another: it keeps the kind that it already has.
func (f *edgeFetch) putNode(node *ObservedNode) {
	if node.Kind == "" {
		node.Kind = f.nodes[node.ObservedID].Kind
	}

	f.nodes[node.ObservedID] = *node
}

// hasKindProperty says whether a node label of the schema has the property `kind`.
func hasKindProperty(schema *GraphSchema) bool {
	return slices.ContainsFunc(schema.NodeLabels, func(l SchemaLabel) bool { return slices.Contains(l.Properties, propKind) })
}

// needsTargetKind says whether a rule of the native type looks at the target node kind.
func needsTargetKind(native string) bool {
	return slices.ContainsFunc(rulesByNative()[native], func(r *NormalizationRule) bool {
		return len(r.Discriminators.TargetKinds) > 0
	})
}

// splitEdgeProps divides the properties of an edge type into the ones that
// have a column of their own and the ones that are read from `properties(r)`.
func splitEdgeProps(props []string) (propNames, jsonProps []string) {
	for _, name := range props {
		switch {
		case isIDColumn(name):
		case slices.Contains(unqueryableProps, name):
			jsonProps = append(jsonProps, name)
		default:
			propNames = append(propNames, name)
		}
	}

	return propNames, jsonProps
}

// partsOf gives the queries of one native edge type: one for each node label of the graph.
func partsOf(schema *GraphSchema, edgeType *SchemaEdgeType) []*edgePart {
	propNames, jsonProps := splitEdgeProps(edgeType.Properties)
	targetKind := needsTargetKind(edgeType.Type) && hasKindProperty(schema)
	returns := returnColumns(propNames, targetKind, len(jsonProps) > 0)
	mapped := IsMappedNative(edgeType.Type)

	parts := make([]*edgePart, 0, len(schema.NodeLabels))

	for _, label := range schema.NodeLabels {
		if label.Count <= 0 {
			continue
		}

		parts = append(parts, &edgePart{
			nativeKind: edgeType.Type,
			nodeLabel:  label.Label,
			match:      fmt.Sprintf("MATCH (a:%s)-[r:%s]->(b)", label.Label, edgeType.Type),
			returns:    returns,
			propNames:  propNames,
			jsonProps:  jsonProps,
			targetKind: targetKind,
			mapped:     mapped,
		})
	}

	return parts
}

func newEdgeFetch(p *CbmProvider, ref *GraphSnapshotRef) *edgeFetch {
	return &edgeFetch{
		p:     p,
		ref:   ref,
		nodes: map[string]ObservedNode{},
		analyzer: AnalyzerDescriptor{
			Name:          "codebase-memory-mcp",
			Version:       p.version,
			AdapterSchema: AdapterSchema,
		},
	}
}

// isWantedEdgeType says whether a fetch of nativeKinds reads an edge type: a
// type that the rule table does not know is always read (as unsupported facts).
func isWantedEdgeType(edgeType *SchemaEdgeType, nativeKinds []string) bool {
	return !IsMappedNative(edgeType.Type) || slices.Contains(nativeKinds, edgeType.Type)
}

// fetchEdgeTypes fetches the edges of the wanted native types of the schema.
// It gives the row count of each native type that the rule table does not map.
func (f *edgeFetch) fetchEdgeTypes(ctx context.Context, nativeKinds []string) (map[string]int, error) {
	unmapped := map[string]int{}

	for i := range f.ref.Schema.EdgeTypes {
		edgeType := &f.ref.Schema.EdgeTypes[i]
		if edgeType.Count <= 0 {
			continue
		}

		if !IsMappedNative(edgeType.Type) {
			unmapped[edgeType.Type] = int(edgeType.Count)
		}

		if !isWantedEdgeType(edgeType, nativeKinds) {
			continue
		}

		if err := f.fetchEdgeType(ctx, edgeType); err != nil {
			return nil, err
		}
	}

	return unmapped, nil
}

// fetchEdgeType fetches the edges of one native type from each node label.
func (f *edgeFetch) fetchEdgeType(ctx context.Context, edgeType *SchemaEdgeType) error {
	for _, part := range partsOf(&f.ref.Schema, edgeType) {
		if err := f.fetchLabel(ctx, part); err != nil {
			return err
		}
	}

	return nil
}

// fetchExtraNodes adds the nodes that no edge query collects, and the
// properties that Concordia keeps verbatim.
func (f *edgeFetch) fetchExtraNodes(ctx context.Context, nativeKinds []string) error {
	if slices.Contains(nativeKinds, nativeSupersedes) {
		if err := f.fetchDecisionNodes(ctx); err != nil {
			return err
		}
	}

	if err := f.fetchDiagramNodes(ctx); err != nil {
		return err
	}

	return f.fetchNodeProperties(ctx)
}

// result sorts the collected facts and gives the result of the fetch.
func (f *edgeFetch) result(unmapped map[string]int) EvidenceQueryResult {
	SortEdges(f.edges, f.nodes)
	SortEdges(f.unsupported, f.nodes)
	slices.Sort(f.digests)

	return EvidenceQueryResult{
		Nodes:               f.nodes,
		Edges:               nonNilEdges(f.edges),
		UnmappedNativeEdges: unmapped,
		Unsupported:         nonNilEdges(f.unsupported),
		Entities:            SynthesizeEntities(f.nodes, f.edges),
		Diagnostics:         fetchDiagnostics(unmapped, f.edges, f.nodes),
		QueryPlanDigest:     digest.SHA256HexString(strings.Join(f.digests, "\n")),
	}
}

// FetchEdges fetches and normalizes the edges of the given native types, and
// all edges of native types that the rule table does not map (they come back
// as `unsupported` facts). Queries have a labeled source node (CBM gives 0
// rows without a label), never use ORDER BY with SKIP/LIMIT, and each part is
// checked against its `count(r)`. Node properties that Concordia keeps
// verbatim (see nodePropertyColumns) come from one more query for each label.
func (p *CbmProvider) FetchEdges(ctx context.Context, ref *GraphSnapshotRef, nativeKinds []string) (EvidenceQueryResult, error) {
	fetch := newEdgeFetch(p, ref)

	unmapped, err := fetch.fetchEdgeTypes(ctx, nativeKinds)
	if err != nil {
		return EvidenceQueryResult{}, err
	}

	if err := fetch.fetchExtraNodes(ctx, nativeKinds); err != nil {
		return EvidenceQueryResult{}, err
	}

	return fetch.result(unmapped), nil
}
