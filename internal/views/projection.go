package views

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/projection"
)

// ProjectionContract is the schema tag of the canonical JSON of a view projection (design D5). A change of
// the shape or of the rules that fill it is a new contract: a manifest with another contract is stale
// (`contractChanged`).
const ProjectionContract = "concordia-view-projection/1"

// The basis of a projection: where its relationships come from.
const (
	// BasisDeclared is the projection of the CALM model (and the declared placement).
	BasisDeclared = "declared"
	// BasisObserved is the projection of the observed graph through the bindings.
	BasisObserved = "observed"
)

// DirectionForward is the direction of every relationship of this release: from `from` to `to`. A reversed
// relationship is a different (from, to) pair.
const DirectionForward = "forward"

// LevelDeploymentNode is the level of an element that a deployment view shows as the target of a placement.
const LevelDeploymentNode = "deployment-node"

// Inputs that the projection cannot fill in this release. They stay `null` in each relationship and are
// named in `missing`, so that a later release that fills them changes the contract and not a value silently.
const (
	MissingRelationshipInterface = "relationship.interface"
	MissingRelationshipContext   = "relationship.context"
	MissingPlacement             = "placement"
)

// Tokens of `projection.semantic`: display text that a view declares significant.
const (
	SemanticName        = "name"
	SemanticDescription = "description"
	SemanticTags        = "tags"
)

// Projection is the canonical, sorted projection of a view (design D5). The model projection (basis
// `declared`) and the observed projection (basis `observed`) have the same shape. Names, descriptions, tags,
// colors, layout, timestamps, source lines, edge counts, and function bodies are not in it (a name, a
// description, and tags only when the view lists them in `projection.semantic`).
type Projection struct {
	// Placement is set for a deployment view only: the instances and the gaps of its environment (design D17).
	*PlacementProjection
	// Observed is set for the observed projection only.
	*ObservedPart
	Coverage      ProjectionCoverage       `json:"coverage"`
	Contract      string                   `json:"contract"`
	Basis         string                   `json:"basis"`
	View          ProjectionView           `json:"view"`
	Elements      []ProjectionElement      `json:"elements"`
	Relationships []ProjectionRelationship `json:"relationships"`
	// Depicts is the depicted subject: the elements of the projection and the CALM IDs of the declared
	// relationships that the view shows. It is sorted, and it selects the slices of the digest (design D7).
	Depicts Subject `json:"depicts"`
	// Missing names the inputs that were not available. Sorted, never nil.
	Missing []string `json:"missing"`
}

// ObservedPart holds the entries that only the observed projection has.
type ObservedPart struct {
	// Ambiguous are the entities that more than one element claims at the abstraction of the view, with the
	// candidates. They are excluded: no relationship of such an entity is in the projection.
	Ambiguous []AmbiguousEntity `json:"ambiguous"`
}

// AmbiguousEntity is an observed entity that merges into more than one element.
type AmbiguousEntity struct {
	// Entity is the qualified name of the entity (the file path for a file without one).
	Entity string `json:"entity"`
	// Candidates are the elements, sorted.
	Candidates []string `json:"candidates"`
}

// Subject is a depicted subject: CALM element IDs and CALM relationship IDs, sorted.
type Subject struct {
	Elements      []string `json:"elements"`
	Relationships []string `json:"relationships"`
}

// ProjectionView identifies the view.
type ProjectionView struct {
	Environment *string  `json:"environment"`
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Abstraction string   `json:"abstraction"`
	Scope       []string `json:"scope"`
}

// ProjectionElement is one element at the abstraction of the view.
type ProjectionElement struct {
	// Parent is the CALM ID of the C4 parent, `null` for a top-level element.
	Parent      *string  `json:"parent"`
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	ID          string   `json:"id"`
	Level       string   `json:"level"`
	Type        string   `json:"type"`
	Tags        []string `json:"tags,omitempty"`
}

// ProjectionRelationship is one relationship between two elements. Protocol, interface, context and status
// are `null` when there is no value, which is not the same as an empty value.
type ProjectionRelationship struct {
	Protocol  *string `json:"protocol"`
	Interface *string `json:"interface"`
	Context   *string `json:"context"`
	Status    *string `json:"status"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Kind      string  `json:"kind"`
	Direction string  `json:"direction"`
	// Discriminator keeps parallel relationships apart when the view sets `parallelEdges: separate`: the CALM
	// relationship ID (declared) or the native kind, environment, reference role and occurrence (observed).
	Discriminator string `json:"discriminator,omitempty"`
}

// ProjectionCoverage is the coverage that the view needs and what is missing from it.
type ProjectionCoverage struct {
	// Unbound is the unbound-evidence class of an observed projection, `null` for the model projection.
	Unbound  *UnboundCoverage `json:"unbound"`
	Required RequiredCoverage `json:"required"`
	Lost     []LostCoverage   `json:"lost"`
}

// RequiredCoverage is the necessary coverage of the view (design D2).
type RequiredCoverage struct {
	Elements      []string `json:"elements"`
	Relationships []string `json:"relationships"`
}

// Kinds of lost coverage.
const (
	// LostBinding is a necessary element that no observed entity is bound to.
	LostBinding = "binding"
	// LostEdgeKind is an edge kind of the view that the provider does not support.
	LostEdgeKind = "edge-kind"
)

// LostCoverage is a necessary subject that the observed projection cannot show, so the projection is not
// smaller by design.
type LostCoverage struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
}

// Unbound-evidence classes.
const (
	UnboundNone = "none"
	UnboundSome = "some"
)

// UnboundCoverage says whether observed edges with an unbound end touch the elements of the view. It holds no
// edge count: a count would change with every internal edit. The unbound edges themselves are not
// relationships of the projection.
type UnboundCoverage struct {
	Class string `json:"class"`
	// Elements are the elements of the view with at least one unbound neighbor, sorted.
	Elements []string `json:"elements"`
}

// Marshal gives the canonical JSON of the projection.
func (p *Projection) Marshal() ([]byte, error) {
	return canonjson.Marshal(p)
}

// Digest gives the `sha256:<hex>` digest of the canonical JSON of the projection.
func (p *Projection) Digest() (string, error) {
	data, err := p.Marshal()
	if err != nil {
		return "", err
	}

	return digest.OfBytes(data), nil
}

// ModelInput is the input of BuildModelProjection.
type ModelInput struct {
	Model *intent.ArchitectureModel
	// Placement is the placement of a deployment view (ProjectPlacement of the environment of the view).
	// Nil for the other kinds; a deployment view without it names `placement` in `missing`.
	Placement *PlacementProjection
	Bindings  []binding.Binding
}

// BuildModelProjection builds the model projection of a view (design D5): the elements of the scope at the
// abstraction of the view, the declared relationships between them and the ones that cross the scope, the
// declared placement for a deployment view, and the necessary coverage. Persons are shown at every level; an
// element below the abstraction level rolls up into its ancestor at that level, and one above it stays as it is.
func BuildModelProjection(view *Definition, input *ModelInput) *Projection {
	lay := newLayout(view, input.Model, input.Bindings)

	rows, shown, depicted := lay.declaredRelationships(view)
	elements := map[string]string{}

	for id := range lay.inScope {
		elements[id] = ""
	}

	for _, row := range rows {
		elements[row.From], elements[row.To] = "", ""
	}

	missing := []string{MissingRelationshipContext, MissingRelationshipInterface}

	var placement *PlacementProjection

	if view.Kind == KindDeployment {
		if input.Placement == nil {
			missing = append(missing, MissingPlacement)
		} else {
			placement = input.Placement
			lay.addHosts(elements, placement)
		}
	}

	slices.Sort(missing)

	out := &Projection{
		PlacementProjection: placement,
		Contract:            ProjectionContract,
		Basis:               BasisDeclared,
		View:                viewOf(view),
		Elements:            lay.elementRows(view, elements),
		Relationships:       rows,
		Coverage: ProjectionCoverage{
			Required: lay.required(view, shown),
			Lost:     []LostCoverage{},
		},
		Missing: missing,
	}

	out.Depicts = Subject{Elements: sortedKeys(elements), Relationships: sortedKeys(depicted)}

	return out
}

func viewOf(view *Definition) ProjectionView {
	var environment *string
	if view.Environment != "" {
		environment = &view.Environment
	}

	return ProjectionView{
		ID: view.ID, Kind: string(view.Kind), Abstraction: string(view.Abstraction), Environment: environment,
		Scope: sortedCopy(view.Scope),
	}
}

// layout is the C4 structure of the model, at the abstraction of one view.
type layout struct {
	byID  map[string]*projection.C4Element
	nodes map[string]*intent.NodeDeclaration
	model *intent.ArchitectureModel
	// inScope are the elements at the abstraction of the view (and the persons) that the scope selects.
	inScope map[string]bool
	target  intent.C4Level
}

// targetLevel is the C4 level at which a view compares: a deployment view compares its containers.
func targetLevel(a Abstraction) intent.C4Level {
	switch a {
	case AbstractionSystem:
		return intent.C4SoftwareSystem
	case AbstractionComponent:
		return intent.C4Component
	case AbstractionContainer, AbstractionDeployment:
		return intent.C4Container
	}

	return intent.C4Container
}

// Depths of the C4 levels, from the outside in. A person has none.
const (
	depthSystem    = 1
	depthContainer = 2
	depthComponent = 3
)

// levelDepth orders the C4 levels from the outside in. A person has no depth.
func levelDepth(level intent.C4Level) int {
	switch level {
	case intent.C4SoftwareSystem:
		return depthSystem
	case intent.C4Container:
		return depthContainer
	case intent.C4Component:
		return depthComponent
	case intent.C4Person:
		return 0
	}

	return 0
}

func newLayout(view *Definition, model *intent.ArchitectureModel, bindings []binding.Binding) *layout {
	lay := &layout{
		byID: map[string]*projection.C4Element{}, nodes: map[string]*intent.NodeDeclaration{}, model: model,
		inScope: map[string]bool{}, target: targetLevel(view.Abstraction),
	}

	if model == nil {
		return lay
	}

	c4 := projection.MapC4(model, bindings, "")
	for i := range c4.Elements {
		lay.byID[c4.Elements[i].ID] = &c4.Elements[i]
	}

	for i := range model.Nodes {
		lay.nodes[model.Nodes[i].ID] = &model.Nodes[i]
	}

	lay.selectScope(view)

	return lay
}

// parentOf gives the real parent of an element: the synthetic system is not a CALM element.
func (l *layout) parentOf(e *projection.C4Element) *projection.C4Element {
	if e.Parent == "" {
		return nil
	}

	parent := l.byID[e.Parent]
	if parent == nil || parent.Synthetic {
		return nil
	}

	return parent
}

// rollUp gives the element that a CALM node shows as at the abstraction of the view. A person stays, an
// element above the level stays, an element below it takes its ancestor at the level (or the highest real
// ancestor when there is none). A node without a C4 level has no element.
func (l *layout) rollUp(id string) (string, bool) {
	elem := l.byID[id]
	if elem == nil || elem.Synthetic {
		return "", false
	}

	if elem.Level == intent.C4Person {
		return id, true
	}

	for levelDepth(elem.Level) > levelDepth(l.target) {
		parent := l.parentOf(elem)
		if parent == nil {
			break
		}

		elem = parent
	}

	return elem.ID, true
}

func (l *layout) selectScope(view *Definition) {
	scope := map[string]bool{}
	for _, id := range view.Scope {
		scope[id] = true
	}

	for id, elem := range l.byID {
		if l.selects(elem, scope) {
			l.inScope[id] = true
		}
	}

	for id := range scope {
		l.selectAncestorOfScopeElement(id)
	}
}

// selectAncestorOfScopeElement selects, for a scope element below the level, the ancestor at the level that
// shows it.
func (l *layout) selectAncestorOfScopeElement(id string) {
	elem := l.byID[id]
	if elem == nil || levelDepth(elem.Level) <= levelDepth(l.target) {
		return
	}

	if up, ok := l.rollUp(id); ok {
		l.inScope[up] = true
	}
}

// selects says whether the scope selects an element at the level of the view: a person that the scope names,
// or an element at the level that the scope names or that a named element contains.
func (l *layout) selects(elem *projection.C4Element, scope map[string]bool) bool {
	if elem.Synthetic {
		return false
	}

	if elem.Level == intent.C4Person {
		return scope[elem.ID]
	}

	if elem.Level != l.target {
		return false
	}

	for x := elem; x != nil; x = l.parentOf(x) {
		if scope[x.ID] {
			return true
		}
	}

	return false
}

// declaredSet collects the rows of the declared relationships of one view.
type declaredSet struct {
	seen map[string]bool
	// between are the CALM IDs of the relationships between two elements of the scope.
	between map[string]bool
	// depicted are the CALM IDs of every relationship that a row shows.
	depicted map[string]bool
	rows     []ProjectionRelationship
}

// add keeps the row of a relationship once for each key; with a discriminator, the keys of parallel relationships differ.
func (d *declaredSet) add(rel *intent.RelationshipDeclaration, row *ProjectionRelationship, bothInScope bool) {
	d.depicted[rel.ID] = true

	if bothInScope {
		d.between[rel.ID] = true
	}

	if key := relationshipKey(row); !d.seen[key] {
		d.seen[key] = true

		d.rows = append(d.rows, *row)
	}
}

// declaredRelationships gives the relationship rows of the declared model, the CALM IDs of the relationships
// between two elements of the scope (necessary coverage), and the CALM IDs of every relationship that a row
// shows.
func (l *layout) declaredRelationships(view *Definition) (rows []ProjectionRelationship, between, depicted map[string]bool) {
	set := &declaredSet{seen: map[string]bool{}, between: map[string]bool{}, depicted: map[string]bool{}}

	if l.model == nil {
		return []ProjectionRelationship{}, set.between, set.depicted
	}

	separate := view.EffectiveParallelEdges() == "separate"
	keepSelf := view.EffectiveSelfEdges() != "omit"

	for i := range l.model.Relationships {
		rel := &l.model.Relationships[i]
		if isDeclaredKind(rel) {
			l.addDeclaredRows(set, rel, separate, keepSelf)
		}
	}

	sortRelationships(set.rows)

	return nonNil(set.rows), set.between, set.depicted
}

// isDeclaredKind says whether a relationship of the model is one that the views show as a row: a connection or
// an interaction with a source.
func isDeclaredKind(rel *intent.RelationshipDeclaration) bool {
	return (rel.Kind == intent.RelConnects || rel.Kind == intent.RelInteracts) && rel.Source != ""
}

// addDeclaredRows adds the row of each source-target pair of a declared relationship that the view shows.
func (l *layout) addDeclaredRows(set *declaredSet, rel *intent.RelationshipDeclaration, separate, keepSelf bool) {
	for _, target := range rel.Targets {
		row, ok := l.declaredRow(rel, target, keepSelf)
		if !ok {
			continue
		}

		if separate {
			row.Discriminator = rel.ID
		}

		set.add(rel, &row, l.inScope[row.From] && l.inScope[row.To])
	}
}

// declaredRow gives the row of one source-target pair of a declared relationship, when the view shows it.
func (l *layout) declaredRow(rel *intent.RelationshipDeclaration, target string, keepSelf bool) (ProjectionRelationship, bool) {
	from, okFrom := l.rollUp(rel.Source)
	dest, okDest := l.rollUp(target)

	if !okFrom || !okDest || !l.rowShown(from, dest, keepSelf) {
		return ProjectionRelationship{}, false
	}

	return ProjectionRelationship{
		From: from, To: dest, Kind: string(rel.Kind), Direction: DirectionForward, Protocol: stringPtr(rel.Protocol),
	}, true
}

// rowShown says whether the view shows a relationship between two rolled-up elements: one of them is in the
// scope, and a relationship of an element with itself is shown only when the view keeps self edges.
func (l *layout) rowShown(from, dest string, keepSelf bool) bool {
	if !l.inScope[from] && !l.inScope[dest] {
		return false
	}

	return from != dest || keepSelf
}

// roleHost marks an element that a deployment view shows only as the target of a placement.
const roleHost = "host"

// addHosts adds the deployment targets of a deployment view as elements: the containers of the `deployed-in`
// relationships of the elements of the view, and the targets and the elements of the placement entries.
func (l *layout) addHosts(elements map[string]string, placement *PlacementProjection) {
	add := func(id, role string) {
		if _, shown := elements[id]; !shown && id != "" {
			elements[id] = role
		}
	}

	for i := range l.model.Relationships {
		rel := &l.model.Relationships[i]
		if rel.Kind != intent.RelDeployedIn || rel.Container == "" {
			continue
		}

		if slices.ContainsFunc(rel.Targets, func(target string) bool { _, shown := elements[target]; return shown }) {
			add(rel.Container, roleHost)
		}
	}

	for i := range placement.Entries {
		add(placement.Entries[i].Element, "")
		add(placement.Entries[i].Container, roleHost)
	}
}

// elementRows gives the sorted rows of the elements that are shown (the keys of shown, with their role).
func (l *layout) elementRows(view *Definition, shown map[string]string) []ProjectionElement {
	rows := make([]ProjectionElement, 0, len(shown))

	for _, id := range sortedKeys(shown) {
		node := l.nodes[id]
		if node == nil {
			continue
		}

		rows = append(rows, l.elementRow(view, id, shown[id], node))
	}

	return rows
}

// elementRow gives the row of one shown element: its level and parent come from the C4 layout, unless it is a
// deployment host or has no C4 element; its text comes from the semantic tokens that the view declares.
func (l *layout) elementRow(view *Definition, id, role string, node *intent.NodeDeclaration) ProjectionElement {
	row := ProjectionElement{ID: id, Type: node.NodeType, Level: LevelDeploymentNode}

	if e := l.byID[id]; e != nil && !e.Synthetic && role != roleHost {
		row.Level = string(e.Level)
		if parent := l.parentOf(e); parent != nil {
			row.Parent = &parent.ID
		}
	}

	addSemanticFields(&row, view, node)

	return row
}

// addSemanticFields sets the name, the description and the tags of a row when the view declares them semantic.
func addSemanticFields(row *ProjectionElement, view *Definition, node *intent.NodeDeclaration) {
	if declaresSemantic(view, SemanticName) {
		row.Name = &node.Name
	}

	if declaresSemantic(view, SemanticDescription) {
		row.Description = &node.Description
	}

	if declaresSemantic(view, SemanticTags) {
		row.Tags = sortedCopy(node.Tags)
	}
}

// declaresSemantic says whether the view declares a token significant.
func declaresSemantic(view *Definition, token string) bool {
	return view.Projection != nil && slices.Contains(view.Projection.Semantic, token)
}

// required gives the necessary coverage: the declared one, else the elements of the scope and the
// relationships between them.
func (l *layout) required(view *Definition, between map[string]bool) RequiredCoverage {
	out := RequiredCoverage{Elements: sortedKeys(l.inScope), Relationships: sortedKeys(between)}

	if c := view.Coverage; c != nil {
		if len(c.Elements) > 0 {
			out.Elements = sortedCopy(c.Elements)
		}

		if len(c.Relationships) > 0 {
			out.Relationships = sortedCopy(c.Relationships)
		}
	}

	return out
}

func relationshipKey(r *ProjectionRelationship) string {
	protocol := "\x00"
	if r.Protocol != nil {
		protocol = "\x01" + *r.Protocol
	}

	return strings.Join([]string{r.From, r.To, r.Kind, protocol, r.Direction, r.Discriminator}, "\x1f")
}

func sortRelationships(rows []ProjectionRelationship) {
	slices.SortFunc(rows, func(a, b ProjectionRelationship) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.From, b.From),
			canonjson.CompareUTF16(a.To, b.To),
			canonjson.CompareUTF16(a.Kind, b.Kind),
			compareProtocol(a.Protocol, b.Protocol),
			canonjson.CompareUTF16(a.Direction, b.Direction),
			canonjson.CompareUTF16(a.Discriminator, b.Discriminator),
		)
	})
}

// compareProtocol orders `null` before every value.
func compareProtocol(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}

	return canonjson.CompareUTF16(*a, *b)
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}

	return list
}

func sortedCopy(list []string) []string {
	out := slices.Clone(list)
	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(nonNil(out))
}

func sortedKeys[V any](set map[string]V) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}
