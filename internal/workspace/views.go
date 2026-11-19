package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// ViewKind is the kind of an architecture view.
type ViewKind string

// View kinds.
const (
	ViewSystemContext ViewKind = "system-context"
	ViewContainer     ViewKind = "container"
	ViewComponent     ViewKind = "component"
	ViewDeployment    ViewKind = "deployment"
	ViewTrust         ViewKind = "trust"
	ViewDataFlow      ViewKind = "data-flow"
)

// ViewOrigin says who writes the diagram of a view.
type ViewOrigin string

// View origins.
const (
	OriginGenerated ViewOrigin = "generated"
	OriginAuthored  ViewOrigin = "authored"
)

// ViewAbstraction is the abstraction level at which a view is compared.
type ViewAbstraction string

// View abstraction levels.
const (
	AbstractionSystem     ViewAbstraction = "system"
	AbstractionContainer  ViewAbstraction = "container"
	AbstractionComponent  ViewAbstraction = "component"
	AbstractionDeployment ViewAbstraction = "deployment"
)

// Export formats of a generated view. ExportStatic is declared only to be rejected: it has no canonicalization rule.
const (
	ExportStatic = "static"
)

// Defaults of the view projection and comparison.
const (
	SelfEdgesOmit      = "omit"
	ParallelEdgesMerge = "merge"
	ParallelEdgesSplit = "separate"
	CompareDeclared    = "declared"
	CompareObserved    = "observed"
	SeverityDefault    = "error"
)

// ViewCoverage lists the CALM element IDs and relationship IDs that a view must show.
type ViewCoverage struct {
	Elements      []string `json:"elements,omitempty"`
	Relationships []string `json:"relationships,omitempty"`
}

// ViewSelector picks one diagram block of an authored source. Exactly one field is set.
type ViewSelector struct {
	Title  string `json:"title,omitempty"`
	Anchor string `json:"anchor,omitempty"`
}

// ViewSource is the authored diagram of a view.
type ViewSource struct {
	Selector *ViewSelector `json:"selector,omitempty"`
	Path     string        `json:"path"`
	Format   string        `json:"format"`
}

// ViewExport is one export of a generated view.
type ViewExport struct {
	Format string `json:"format"`
	Path   string `json:"path"`
}

// ViewOutput is the committed output of a generated view.
type ViewOutput struct {
	Path    string       `json:"path"`
	Exports []ViewExport `json:"exports,omitempty"`
}

// ViewProjection holds the projection rules of a view. A zero field means the default.
type ViewProjection struct {
	Merge         string   `json:"merge,omitempty"`
	SelfEdges     string   `json:"selfEdges,omitempty"`
	ParallelEdges string   `json:"parallelEdges,omitempty"`
	EdgeKinds     []string `json:"edgeKinds,omitempty"`
	Semantic      []string `json:"semantic,omitempty"`
}

// ViewDepicts is the supplemental mapping of a raster view, bound to the image digest.
type ViewDepicts struct {
	Digest        string   `json:"digest"`
	Elements      []string `json:"elements,omitempty"`
	Relationships []string `json:"relationships,omitempty"`
}

// ViewMapping maps the elements of an authored diagram to CALM IDs.
type ViewMapping struct {
	// Elements maps a diagram element identifier (the Mermaid node ID) to a CALM node ID.
	Elements map[string]string `json:"elements,omitempty"`
	Depicts  *ViewDepicts      `json:"depicts,omitempty"`
}

// ViewComparison says what the correspondence check compares and how severe a difference is.
type ViewComparison struct {
	MinimumConfidence string   `json:"minimumConfidence,omitempty"`
	Severity          string   `json:"severity,omitempty"`
	Against           []string `json:"against,omitempty"`
}

// View is one declared architecture view (the `views` list of the workspace).
type View struct {
	Coverage    *ViewCoverage   `json:"coverage,omitempty"`
	Source      *ViewSource     `json:"source,omitempty"`
	Output      *ViewOutput     `json:"output,omitempty"`
	Projection  *ViewProjection `json:"projection,omitempty"`
	Mapping     *ViewMapping    `json:"mapping,omitempty"`
	Comparison  *ViewComparison `json:"comparison,omitempty"`
	ID          string          `json:"id"`
	Kind        ViewKind        `json:"kind"`
	Origin      ViewOrigin      `json:"origin"`
	Owner       string          `json:"owner"`
	Abstraction ViewAbstraction `json:"abstraction"`
	Environment string          `json:"environment,omitempty"`
	Scope       []string        `json:"scope"`
}

// rasterFormats are the source formats that CBM does not index and that carry no structure.
var rasterFormats = []string{"png", "jpeg", "gif"}

// IsRaster says whether the authored source of the view is a raster image.
func (v *View) IsRaster() bool {
	return v.Source != nil && slices.Contains(rasterFormats, v.Source.Format)
}

// EffectiveAgainst is `comparison.against`, or both comparisons when it is not given.
func (v *View) EffectiveAgainst() []string {
	if v.Comparison == nil || len(v.Comparison.Against) == 0 {
		return []string{CompareDeclared, CompareObserved}
	}

	return slices.Clone(v.Comparison.Against)
}

// EffectiveSeverity is `comparison.severity`, or "error".
func (v *View) EffectiveSeverity() string {
	if v.Comparison == nil || v.Comparison.Severity == "" {
		return SeverityDefault
	}

	return v.Comparison.Severity
}

// EffectiveSelfEdges is `projection.selfEdges`, or "omit".
func (v *View) EffectiveSelfEdges() string {
	if v.Projection == nil || v.Projection.SelfEdges == "" {
		return SelfEdgesOmit
	}

	return v.Projection.SelfEdges
}

// EffectiveParallelEdges is `projection.parallelEdges`, or "merge".
func (v *View) EffectiveParallelEdges() string {
	if v.Projection == nil || v.Projection.ParallelEdges == "" {
		return ParallelEdgesMerge
	}

	return v.Projection.ParallelEdges
}

// OutputPaths gives the path of the generated view and the paths of its exports, in declaration order.
func (v *View) OutputPaths() []string {
	if v.Output == nil {
		return nil
	}

	out := []string{v.Output.Path}
	for _, e := range v.Output.Exports {
		out = append(out, e.Path)
	}

	return out
}

// ViewManifestSuffix is appended to `output.path` to give the path of the view manifest.
const ViewManifestSuffix = ".manifest.json"

// ManifestPath gives the path of the manifest of a generated view, "" for a view without an output.
func (v *View) ManifestPath() string {
	if v.Output == nil {
		return ""
	}

	return v.Output.Path + ViewManifestSuffix
}

// GeneratedPaths gives every file that `render --views` writes for the view: the output, the exports and the
// manifest. A view without an output has none.
func (v *View) GeneratedPaths() []string {
	if v.Output == nil {
		return nil
	}

	return append(v.OutputPaths(), v.ManifestPath())
}

var viewIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// kindAbstraction is the abstraction level that a kind fixes. trust and data-flow are not in the table: they declare it.
var kindAbstraction = map[ViewKind]ViewAbstraction{
	ViewSystemContext: AbstractionSystem,
	ViewContainer:     AbstractionContainer,
	ViewComponent:     AbstractionComponent,
	ViewDeployment:    AbstractionDeployment,
}

// ValidateViews checks the definitions that need no other input: the ID pattern and
// uniqueness, the kind, abstraction and origin matrix, the environment of a deployment
// view, the output of a generated view and the source of an authored one, the selector,
// the export formats and the raster rules. Each problem is a CO-DGM-001 diagnostic that
// names the view; uri is the workspace file for the source of the diagnostics.
func ValidateViews(defs []View, uri string) []diag.Diagnostic {
	var out []diag.Diagnostic

	first := map[string]int{}

	for i := range defs {
		view := &defs[i]
		report := func(detail string) {
			out = append(out, viewProblem(view.ID, uri, i, detail))
		}

		if !viewIDPattern.MatchString(view.ID) {
			report(fmt.Sprintf("id '%s' must match [a-z][a-z0-9-]*", view.ID))
		}

		if j, dup := first[view.ID]; dup {
			report(fmt.Sprintf("id '%s' is declared by views[%d] and views[%d]", view.ID, j, i))
		} else {
			first[view.ID] = i
		}

		for _, detail := range definitionProblems(view) {
			report(detail)
		}
	}

	return out
}

// depictsOutsideRaster says whether the mapping depicts something while the source is not a raster.
func depictsOutsideRaster(view *View) bool {
	if view.Mapping == nil || view.Mapping.Depicts == nil {
		return false
	}

	return !view.IsRaster()
}

// definitionProblems are the problems of one definition apart from its ID.
func definitionProblems(view *View) []string {
	problems := kindProblems(view)

	if view.Kind == ViewDeployment && view.Environment == "" {
		problems = append(problems, "a deployment view must declare environment")
	}

	switch view.Origin {
	case OriginGenerated:
		problems = append(problems, generatedProblems(view)...)
	case OriginAuthored:
		problems = append(problems, authoredProblems(view)...)
	}

	if depictsOutsideRaster(view) {
		problems = append(problems, "mapping.depicts applies to a raster source (png, jpeg, gif) only")
	}

	return problems
}

// hasNoGenerator says whether no generator exists for the kind.
func hasNoGenerator(kind ViewKind) bool {
	return kind == ViewTrust || kind == ViewDataFlow
}

func kindProblems(v *View) []string {
	var problems []string

	if want, fixed := kindAbstraction[v.Kind]; fixed && v.Abstraction != want {
		problems = append(problems, fmt.Sprintf("kind '%s' requires abstraction '%s', not '%s'", v.Kind, want, v.Abstraction))
	}

	if v.Origin == OriginGenerated && hasNoGenerator(v.Kind) {
		problems = append(problems, fmt.Sprintf("kind '%s' cannot be generated: no generator exists for this kind, declare origin authored", v.Kind))
	}

	return problems
}

func generatedProblems(v *View) []string {
	if v.Output == nil || v.Output.Path == "" {
		return []string{"a generated view must declare output.path"}
	}

	var problems []string

	for _, e := range v.Output.Exports {
		if e.Format == ExportStatic {
			problems = append(problems, "export format 'static' has no canonicalization rule: use json, mermaid, plantuml or dot")
		}
	}

	return problems
}

// selectorIsAmbiguous says whether a selector names its diagram block by both or by none of title and anchor.
func selectorIsAmbiguous(sel *ViewSelector) bool {
	return sel != nil && (sel.Title == "") == (sel.Anchor == "")
}

// hasMappingElements says whether the view maps at least one element.
func hasMappingElements(view *View) bool {
	return view.Mapping != nil && len(view.Mapping.Elements) > 0
}

func authoredProblems(view *View) []string {
	if view.Source == nil || view.Source.Path == "" {
		return []string{"an authored view must declare source"}
	}

	var problems []string

	if selectorIsAmbiguous(view.Source.Selector) {
		problems = append(problems, "source.selector must name the diagram block by exactly one of title or anchor, not by index or ordinal")
	}

	if !view.IsRaster() && !hasMappingElements(view) {
		problems = append(problems, "an authored view with a structured source must declare mapping.elements")
	}

	return problems
}

func viewProblem(id, uri string, index int, detail string) diag.Diagnostic {
	return diag.New(diag.DGM001, diag.Params{"view": viewLabel(id, index), "detail": detail},
		diag.WithSource(diag.Source{URI: uri, Pointer: fmt.Sprintf("/views/%d", index)}))
}

func viewLabel(id string, index int) string {
	if id == "" {
		return fmt.Sprintf("views[%d]", index)
	}

	return id
}

// viewSchemaProblem makes a CO-DGM-001 diagnostic from a schema error below /views/<n>.
// It returns false for an error elsewhere.
func viewSchemaProblem(root object, uri, pointer, message string) (diag.Diagnostic, bool) {
	rest, ok := strings.CutPrefix(pointer, "/views")
	if !ok {
		return diag.Diagnostic{}, false
	}

	var index int

	tail := ""

	if rest != "" {
		numText, after, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		if _, err := fmt.Sscanf(numText, "%d", &index); err != nil {
			return diag.Diagnostic{}, false
		}

		tail = after
	}

	viewID := ""

	if list, _ := root["views"].([]any); index < len(list) {
		item, _ := list[index].(object)
		viewID, _ = item["id"].(string)
	}

	detail := message
	if tail != "" {
		detail = tail + " " + message
	}

	if strings.HasSuffix(tail, "selector") || strings.Contains(tail, "selector/") {
		detail += "; the selector must be a title or an anchor, never an index or an ordinal"
	}

	return viewProblem(viewID, uri, index, detail), true
}

// viewsOf reads the `views` list. The document is schema-valid, so the conversion cannot fail.
func viewsOf(merged object) []View {
	list, ok := merged["views"].([]any)
	if !ok {
		return nil
	}

	raw, err := json.Marshal(list)
	if err != nil {
		return nil
	}

	var out []View
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}

	return out
}

// checkViews rejects a definition that ValidateViews finds not valid. It is a CO-DGM-001 error,
// with the other problems as its cause.
func checkViews(space *Workspace, path string) *diag.Error {
	problems := ValidateViews(space.Views, path)
	if len(problems) == 0 {
		return nil
	}

	first := problems[0]
	failure := &diag.Error{Diagnostic: first, ExitCode: diag.InvalidInput}

	if len(problems) > 1 {
		failure.Diagnostic.Cause = problems[1:]
	}

	return failure
}
