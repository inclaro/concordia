package projection

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// DefaultWorkspaceName names the workspace when DSLOptions.WorkspaceName is empty.
const DefaultWorkspaceName = "Concordia"

const (
	// maxBMP is the last code point that is one UTF-16 code unit.
	maxBMP = 0xFFFF
	// bodyDepth is the indent depth of the body of a relationship or view block.
	bodyDepth = 3
	// propUniqueID is the DSL property that holds the CALM unique ID.
	propUniqueID = "calm.unique-id"
)

// RunView is the part of a conformance run that the projection reads. The
// JSON keys are the keys of `results.json`, so a results file decodes into it.
type RunView struct {
	Findings       []conformance.Finding             `json:"findings"`
	Reconciliation []conformance.ReconciliationEntry `json:"reconciliation"`
	Rules          []conformance.RuleResult          `json:"rules"`
}

// NewRunView gives the view of a run. A nil run gives nil.
func NewRunView(run *conformance.Run) *RunView {
	if run == nil {
		return nil
	}

	return &RunView{Findings: run.Findings, Reconciliation: run.Reconciliation, Rules: run.Rules}
}

// ViewKind is a kind of Structurizr view.
type ViewKind string

// View kinds.
const (
	ViewLandscape  ViewKind = "landscape"
	ViewContext    ViewKind = "context"
	ViewContainer  ViewKind = "container"
	ViewComponent  ViewKind = "component"
	ViewDrift      ViewKind = "drift"
	ViewDeployment ViewKind = "deployment"
	ViewDynamic    ViewKind = "dynamic"
)

// relStatus is the status of a relationship in the DSL.
type relStatus string

const (
	statusConforming   relStatus = "conforming"
	statusDeclaredOnly relStatus = "declared-only"
	statusObservedOnly relStatus = "observed-only"
	statusViolating    relStatus = "violating"
	statusUnknown      relStatus = "unknown"
)

// DSLOptions are the settings of ToStructurizrDSL.
type DSLOptions struct {
	// Views is the set of views to make. A nil set means all views.
	Views map[ViewKind]bool
	// ViewIDs gives, for a kind, the key of its Structurizr view in committed mode: the ID of the declared view.
	ViewIDs map[ViewKind]string
	// Diagnostics receives the diagnostics that are not errors (CO-PRJ-006).
	Diagnostics *[]diag.Diagnostic
	// Dynamic is the evidence path of the dynamic view. Nil means no dynamic view.
	Dynamic *DynamicPath
	// Focus is a CALM ID: views are made only for the system or container that
	// contains it. Empty means no focus.
	Focus string
	// WorkspaceName is the name of the workspace and of the synthetic system.
	// Empty gives DefaultWorkspaceName.
	WorkspaceName string
	Bindings      []binding.Binding
	// Environments are the declared environment IDs. Each one gets a deployment environment (in ID order) whose
	// instances carry status tags from Placements and Coverage. Empty means the one `Default` environment.
	Environments []string
	// Coverage is the coverage of the run; the `placed-in` record of an environment decides `declared-only`
	// against `unknown`.
	Coverage []observation.PredicateCoverage
	// Placements are the placement records and gaps of the environments (deployment.Placements).
	Placements deployment.PlacementSet
	// Committed makes the DSL of a view that Git tracks (design D6): the status of a relationship comes from the
	// reconciliation classification only (no finding, disposition, waiver, or expiry); the `concordia.status`
	// element property and the finding fingerprint properties are omitted; Structurizr views are named with ViewIDs.
	Committed bool
}

// DSLString escapes a value for a DSL string and puts it in quotes.
func DSLString(s string) string {
	return `"` + dslEscaper.Replace(s) + `"`
}

var dslEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r\n", " ", "\r", " ", "\n", " ")

// MakeIdentifiers makes the DSL identifiers: `gu_` + the sanitized ID, with `_2`,
// `_3`... for collisions, in sorted ID order.
func MakeIdentifiers(ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	used := make(map[string]bool, len(ids))

	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, canonjson.CompareUTF16)

	for _, id := range sorted {
		base := "gu_" + sanitize(id)
		candidate := base

		for n := 2; used[candidate]; n++ {
			candidate = base + "_" + strconv.Itoa(n)
		}

		used[candidate] = true
		out[id] = candidate
	}

	return out
}

// sanitize replaces each UTF-16 code unit that is not [A-Za-z0-9_] with "_".
func sanitize(id string) string {
	var builder strings.Builder

	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			builder.WriteRune(r)
		case r > maxBMP:
			builder.WriteString("__") // A surrogate pair is two code units.
		default:
			builder.WriteByte('_')
		}
	}

	return builder.String()
}

var verdictOrder = []conformance.RuleVerdict{
	conformance.VerdictError,
	conformance.VerdictViolation,
	conformance.VerdictUnknown,
	conformance.VerdictPass,
	conformance.VerdictNotApplicable,
}

func elementStatus(id string, run *RunView) string {
	if run == nil {
		return "none"
	}

	worst := -1

	for i := range run.Rules {
		rule := &run.Rules[i]
		if !slices.Contains(rule.Subjects, id) {
			continue
		}

		if idx := slices.Index(verdictOrder, rule.Verdict); worst == -1 || idx < worst {
			worst = idx
		}
	}

	if worst == -1 {
		return "none"
	}

	return string(verdictOrder[worst])
}

// dslWriter collects the lines of the DSL.
type dslWriter struct {
	buf strings.Builder
}

func (out *dslWriter) line(depth int, text string) {
	out.buf.WriteString(strings.Repeat("  ", depth))
	out.buf.WriteString(text)
	out.buf.WriteByte('\n')
}

type property struct{ key, value string }

// props writes a `properties` block. Structurizr rejects empty property values, so
// the empty ones are omitted.
func (out *dslWriter) props(depth int, entries ...property) {
	kept := make([]property, 0, len(entries))

	for _, e := range entries {
		if e.value != "" {
			kept = append(kept, e)
		}
	}

	if len(kept) == 0 {
		return
	}

	out.line(depth, "properties {")

	for _, e := range kept {
		out.line(depth+1, DSLString(e.key)+" "+DSLString(e.value))
	}

	out.line(depth, "}")
}

// dslModel holds the data that the DSL writer needs.
type dslModel struct {
	run   *RunView
	byID  map[string]*C4Element
	ident map[string]string
	els   []C4Element
	// viewIDs and keyUsed name the Structurizr views of a committed DSL.
	viewIDs map[ViewKind]string
	keyUsed map[ViewKind]int
	// deployments are the names of the deployment environments that the DSL has.
	deployments []string
	// evidence is true when a dynamic view adds evidence relationships.
	evidence bool
	// committed is true for a view that Git tracks: no run state in the output.
	committed bool
	// statusTagged is true when instances carry status tags, so that the element styles are written.
	statusTagged bool
}

func (m *dslModel) children(parent string) []*C4Element {
	var out []*C4Element

	for i := range m.els {
		if m.els[i].Parent == parent {
			out = append(out, &m.els[i])
		}
	}

	return out
}

// dslNodeAttrs are the attributes of the CALM node of an element that the DSL writes.
type dslNodeAttrs struct {
	desc, nodeType, owner string
	tags                  []string
}

// nodeAttrs gives the attributes of the node of an element; the synthetic system has no node.
func nodeAttrs(elem *C4Element) dslNodeAttrs {
	attrs := dslNodeAttrs{tags: []string{"CALM"}}

	node := elem.Node
	if node == nil {
		return attrs
	}

	attrs.desc, attrs.nodeType, attrs.owner = node.Description, node.NodeType, node.Owner
	if attrs.nodeType != "" {
		attrs.tags = append(attrs.tags, attrs.nodeType)
	}

	for _, tag := range node.Tags {
		if tag != "" {
			attrs.tags = append(attrs.tags, tag)
		}
	}

	return attrs
}

// emitHead gives the opening line of an element block, without the indent.
func (m *dslModel) emitHead(elem *C4Element, attrs *dslNodeAttrs) string {
	head := m.ident[elem.ID] + " = " + string(elem.Level) + " " + DSLString(elem.Name) + " " + DSLString(attrs.desc) + " "

	if elem.Level != intent.C4Person && elem.Level != intent.C4SoftwareSystem {
		head += DSLString(attrs.nodeType) + " "
	}

	return head + DSLString(strings.Join(attrs.tags, ",")) + " {"
}

// elementProps gives the properties of an element in the DSL.
func (m *dslModel) elementProps(elem *C4Element, attrs *dslNodeAttrs) []property {
	if elem.Synthetic {
		return []property{{"concordia.synthetic", "true"}}
	}

	props := []property{
		{propUniqueID, elem.ID},
		{"calm.node-type", attrs.nodeType},
		{"concordia.owner", attrs.owner},
	}
	if !m.committed {
		props = append(props, property{"concordia.status", elementStatus(elem.ID, m.run)})
	}

	return props
}

func (m *dslModel) emit(out *dslWriter, elem *C4Element, depth int) {
	attrs := nodeAttrs(elem)

	out.line(depth, m.emitHead(elem, &attrs))
	out.props(depth+1, m.elementProps(elem, &attrs)...)

	for _, child := range m.children(elem.ID) {
		m.emit(out, child, depth+1)
	}

	out.line(depth, "}")
}

// mapForDSL maps the model to C4. It gives an error for the first error
// diagnostic, and appends the other diagnostics to diagnostics when it is not nil.
func mapForDSL(model *intent.ArchitectureModel, bindings []binding.Binding, wsName string, diagnostics *[]diag.Diagnostic) (C4Map, error) {
	mapped := MapC4(model, bindings, wsName)

	if i := dslFirstError(mapped.Diagnostics); i >= 0 {
		return C4Map{}, &diag.Error{Diagnostic: mapped.Diagnostics[i], ExitCode: diag.InvalidInput}
	}

	if diagnostics != nil {
		*diagnostics = append(*diagnostics, mapped.Diagnostics...)
	}

	return mapped, nil
}

// dslFirstError gives the index of the first diagnostic of error severity, or -1 when there is none.
func dslFirstError(diagnostics []diag.Diagnostic) int {
	for i := range diagnostics {
		if diagnostics[i].Severity == diag.SeverityError {
			return i
		}
	}

	return -1
}

// newDSLModel makes the DSL model of the elements, with their identifiers.
func newDSLModel(elements []C4Element, run *RunView, evidence bool) *dslModel {
	dmodel := &dslModel{
		els:      elements,
		byID:     make(map[string]*C4Element, len(elements)),
		ident:    nil,
		run:      run,
		evidence: evidence,
		keyUsed:  map[ViewKind]int{},
	}

	ids := make([]string, len(elements))

	for i := range dmodel.els {
		dmodel.byID[dmodel.els[i].ID] = &dmodel.els[i]
		ids[i] = dmodel.els[i].ID
	}

	dmodel.ident = MakeIdentifiers(ids)

	return dmodel
}

// focusElement gives the element of the focus ID. An empty ID gives no element and true; an ID that is not an
// element gives false.
func (m *dslModel) focusElement(id string) (*C4Element, bool) {
	if id == "" {
		return nil, true
	}

	focus := m.byID[id]

	return focus, focus != nil
}

// writeModel writes the `model` block: the elements, the deployment, and the relationships.
func (m *dslModel) writeModel(out *dslWriter, model *intent.ArchitectureModel, opts *DSLOptions) {
	rels := append(m.declaredRelationships(model), m.observedOnly()...)

	out.line(1, "model {")

	for i := range m.els {
		if m.els[i].Parent == "" {
			m.emit(out, &m.els[i], 2)
		}
	}

	m.deployments = m.writeDeployment(out, model, opts)

	for i := range rels {
		m.writeRelationship(out, &rels[i])
	}

	if opts.Dynamic != nil {
		for _, pair := range m.evidenceRelationships(opts.Dynamic, rels) {
			m.writeEvidenceRelationship(out, pair)
		}
	}

	out.line(1, "}")
}

// ToStructurizrDSL makes the Structurizr DSL workspace. The output is
// deterministic: elements and relationships are sorted by CALM ID. It gives an
// error with CO-PRJ-005 (exit 2) when the C4 mapping is not valid, and CO-WRK-010
// when the focus is not an element.
func ToStructurizrDSL(model *intent.ArchitectureModel, run *RunView, opts DSLOptions) (string, error) { //nolint:gocritic // ToStructurizrDSL is public API and takes the options by value.
	wsName := cmp.Or(opts.WorkspaceName, DefaultWorkspaceName)

	mapped, err := mapForDSL(model, opts.Bindings, wsName, opts.Diagnostics)
	if err != nil {
		return "", err
	}

	dmodel := newDSLModel(mapped.Elements, run, opts.Dynamic != nil)
	dmodel.committed = opts.Committed
	dmodel.viewIDs = opts.ViewIDs

	focus, ok := dmodel.focusElement(opts.Focus)
	if !ok {
		return "", diag.Fail(diag.WRK010, diag.Detail("unknown --focus '"+opts.Focus+"'"))
	}

	var out dslWriter

	out.line(0, "workspace "+DSLString(wsName)+" "+DSLString("Generated by Concordia from CALM architecture "+model.SourceURI)+" {")
	dmodel.writeModel(&out, model, &opts)
	dmodel.writeViews(&out, &opts, focus)
	out.line(0, "}")

	return out.buf.String(), nil
}
