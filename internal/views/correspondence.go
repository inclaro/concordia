package views

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// Methods of a correspondence result (spec view-currency, "Correspondence reports its method").
const (
	// MethodGeneratedSource compares the diagram that Concordia generated, which is the declared projection.
	MethodGeneratedSource = "generated-source"
	// MethodStructuredSource compares the structure of an authored diagram that the provider kept.
	MethodStructuredSource = "structured-source"
	// MethodSupplementalMapping compares the `mapping.depicts` claim that is bound to the digest of a raster image.
	MethodSupplementalMapping = "supplemental-mapping"
	// MethodPresenceOnly means that nothing but the presence of the artifact is known: no automated pass.
	MethodPresenceOnly = "presence-only"
	// MethodReviewed is reserved for add-review-currency, which supplies an authorized result. Concordia never
	// emits it in this release.
	MethodReviewed = "reviewed"
)

// The basis of a correspondence result: which diagram content was compared.
const (
	// CorrespondenceBasisRegenerated says that a generated view was compared as a fresh render shows it,
	// because freshness is not `pass`: the committed artifact may differ.
	CorrespondenceBasisRegenerated = "regenerated-projection"
	// CorrespondenceBasisCommitted says that the committed artifact equals a fresh render.
	CorrespondenceBasisCommitted = "committed-artifact"
)

// Kinds of difference between a diagram and a projection (spec view-currency, "Correspondence compares typed topology").
const (
	// DifferenceMissing is in the projection and not in the diagram.
	DifferenceMissing = "missing"
	// DifferenceExtra is in the diagram and in no selected projection.
	DifferenceExtra = "extra"
	// DifferenceIncorrect has the same endpoints in both, with another direction or protocol.
	DifferenceIncorrect = "incorrect"
	// DifferenceUnmapped is a diagram element that no mapping key reaches. Arrows of it are not compared.
	DifferenceUnmapped = "unmapped"
)

// What a difference is about.
const (
	SubjectElement      = "element"
	SubjectRelationship = "relationship"
)

// Reasons of an `unknown` correspondence. The text is the reason that the view result reports.
const (
	ReasonAmbiguousMapping    = "ambiguous mapping"
	ReasonAmbiguousMappingKey = "ambiguous mapping key"
	ReasonMappingGap          = "mapping gap"
	ReasonNoBinding           = "no binding for necessary element"
	ReasonCoverageLost        = "coverage lost"
	ReasonAmbiguousBinding    = "ambiguous binding"
	ReasonUnboundEvidence     = "unbound evidence for necessary element"
	ReasonSupplementalStale   = "supplemental mapping stale"
	ReasonPresenceOnly        = "raster image without supplemental mapping"
	ReasonObservedUnavailable = "observed projection not available"
	ReasonDeclaredUnavailable = "declared projection not available"
	ReasonImageUnavailable    = "image digest not available"
	ReasonDepictedUnknown     = "depicted relationship not in model"
)

// The next-action categories of a view check (design D10). The category of a binding problem is
// conformance.NextActionResolveBinding.
const (
	NextActionRegenerateView = "regenerate view"
	NextActionCorrectIntent  = "correct intent"
	NextActionSupplyEvidence = "supply evidence"
	NextActionFixAnalysis    = "fix analysis configuration"
)

// Difference is one difference between the diagram and a projection.
type Difference struct {
	// Kind is DifferenceMissing, DifferenceExtra, DifferenceIncorrect or DifferenceUnmapped.
	Kind string `json:"kind"`
	// Subject is SubjectElement or SubjectRelationship.
	Subject string `json:"subject"`
	// Basis is the projection that holds the subject (BasisDeclared or BasisObserved) for `missing` and
	// `incorrect`. It is empty for `extra` (no selected projection holds it) and `unmapped`.
	Basis string `json:"basis,omitempty"`
	// From is the CALM element ID of the element, or of the tail of the relationship. For `unmapped` it is the
	// diagram element identifier.
	From string `json:"from"`
	// To is the CALM element ID of the head of a relationship.
	To string `json:"to,omitempty"`
	// Detail says what is incorrect: `direction` or `protocol <diagram> <projection>`.
	Detail string `json:"detail,omitempty"`
	// Locations are the places in the diagram source that show the subject; none for `missing`.
	Locations []Location `json:"locations,omitempty"`
}

// Key gives the subject key of the difference: the element ID, or `from>to`. The finding fingerprint of a
// difference uses it (design D3).
func (d *Difference) Key() string {
	if d.To == "" {
		return d.From
	}

	return d.From + ">" + d.To
}

// UnknownReason is one reason that correspondence cannot say pass or violation for a subject.
type UnknownReason struct {
	// Reason is one of the Reason constants, or the reason of the diagram reader (ReasonStructureNotAvailable).
	Reason string `json:"reason"`
	// Subject is the element, mapping key, edge kind or entity that the reason is about.
	Subject string `json:"subject,omitempty"`
	// Candidates are the elements that an ambiguous entity could be.
	Candidates []string `json:"candidates,omitempty"`
	// Locations are the places in the diagram source that cause the reason.
	Locations []Location `json:"locations,omitempty"`
}

// CorrespondenceResult is the result of the correspondence check of one view.
type CorrespondenceResult struct {
	// Source is the diagram file (authored) or the generated output (generated).
	Source Location `json:"source"`
	// Status is `violation` when a difference exists, else `unknown` when a reason exists, else `pass`.
	Status Status `json:"status"`
	// Method is one of the Method constants.
	Method string `json:"method"`
	// Basis is set for a generated view: CorrespondenceBasisRegenerated or CorrespondenceBasisCommitted.
	Basis string `json:"basis,omitempty"`
	// NextAction is the next-action category of a non-pass result.
	NextAction string `json:"nextAction,omitempty"`
	// Section and QualifiedName name the diagram block of a structured source. The qualified name carries the
	// provider ordinal: it is for display, never an identity.
	Section       string `json:"section,omitempty"`
	QualifiedName string `json:"qualifiedName,omitempty"`
	// Against are the projections that the view compares with (`comparison.against`).
	Against []string `json:"against"`
	// Compared are the projections that the check compared. A projection that is not available is not in it.
	Compared []string `json:"compared"`
	// ByConstruction are the projections that the diagram equals by construction: the declared projection of
	// a generated view. Whether the committed file equals a fresh render is freshness, not correspondence.
	ByConstruction []string `json:"byConstruction"`
	// Differences are sorted. Empty for `pass`.
	Differences []Difference `json:"differences"`
	// Unknown are the reasons of an `unknown` result, and the reasons that hold back a subject while another
	// subject has a difference.
	Unknown []UnknownReason `json:"unknown"`
	// Lost is the coverage that the observed projection lost (design D5).
	Lost []LostCoverage `json:"lost"`
	// Reasons are the distinct reason texts, sorted: the Unknown reasons and `<kind> <subject>` of each difference.
	Reasons []string `json:"reasons"`
}

// CorrespondenceInput is the input of Correspond.
type CorrespondenceInput struct {
	// Declared is the model projection and Observed the observed projection of the view. Observed is nil when
	// no observation is available; Declared is nil only when the model is not available.
	Declared *Projection
	Observed *Projection
	// Evidence is the kept provider evidence for an authored structured view: it must hold the `Diagram` and
	// `DiagramElement` nodes and the `diagram-arrow` edges (see DiagramEvidenceOf).
	Evidence *DiagramEvidence
	// Model and Bindings resolve the `mapping.depicts.relationships` of a raster view.
	Model *intent.ArchitectureModel
	View  *Definition
	// ImageDigest is the `sha256:` digest of the committed raster image, read from Git. Empty when unavailable.
	ImageDigest string
	// Freshness is the freshness verdict of a generated view; it decides the basis of the result.
	Freshness Status
	Bindings  []binding.Binding
}

// DiagramEvidenceOf gives the diagram evidence of a fetch of the provider (observation.EvidenceQueryResult).
func DiagramEvidenceOf(res *observation.EvidenceQueryResult) *DiagramEvidence {
	return &DiagramEvidence{Nodes: res.Nodes, Edges: res.Edges}
}

// selection is one projection that the view compares with.
type selection struct {
	proj  *Projection
	basis string
}

// Correspond runs the correspondence check of one view (design D10).
//
// A generated view is the declared projection (method `generated-source`); it is compared with the observed
// projection, so a fresh render of stale intent still shows implementation drift. An authored structured view
// is read from the kept diagram evidence and mapped by `mapping.elements`. A raster view is `presence-only`, or
// `supplemental-mapping` when `mapping.depicts` is bound to the digest of the committed image.
//
// An arrow is the same relationship as a projection row when both connect the same two CALM elements in the
// same direction; protocols are compared when both sides carry one. A diagram element or an arrow that is
// unmapped, ambiguous, or held back by lost coverage is not compared: it has an `unknown` reason and no
// difference.
func Correspond(input *CorrespondenceInput) CorrespondenceResult {
	res := CorrespondenceResult{
		Against:        slices.Clone(input.View.EffectiveAgainst()),
		Compared:       []string{},
		ByConstruction: []string{},
		Differences:    []Difference{},
		Unknown:        []UnknownReason{},
		Lost:           []LostCoverage{},
	}

	switch {
	case input.View.Origin == OriginGenerated:
		generatedCorrespondence(input, &res)
	case input.View.IsRaster():
		rasterCorrespondence(input, &res)
	default:
		structuredCorrespondence(input, &res)
	}

	res.finish()

	return res
}

// generatedCorrespondence compares the declared projection, as a fresh render shows it, with the selected
// projections. The declared comparison holds by construction.
func generatedCorrespondence(input *CorrespondenceInput, res *CorrespondenceResult) {
	res.Method = MethodGeneratedSource
	res.Basis = CorrespondenceBasisRegenerated

	if input.Freshness == StatusPass {
		res.Basis = CorrespondenceBasisCommitted
	}

	if input.View.Output != nil {
		res.Source = Location{Path: input.View.Output.Path}
	}

	if input.Declared == nil {
		res.Unknown = append(res.Unknown, UnknownReason{Reason: ReasonDeclaredUnavailable, Subject: input.View.ID})

		return
	}

	if slices.Contains(res.Against, BasisDeclared) {
		res.ByConstruction = append(res.ByConstruction, BasisDeclared)
	}

	drawn := newDrawn()

	for i := range input.Declared.Elements {
		drawn.elements[input.Declared.Elements[i].ID] = []Location{res.Source}
	}

	for i := range input.Declared.Relationships {
		row := &input.Declared.Relationships[i]
		drawn.arrows = append(drawn.arrows, arrowFact{
			from: row.From, to: row.To, protocol: deref(row.Protocol), loc: res.Source,
		})
	}

	drawn.run(input, res)
}

// structuredCorrespondence reads the diagram of an authored view from the provider evidence and compares it.
func structuredCorrespondence(input *CorrespondenceInput, res *CorrespondenceResult) {
	res.Method = MethodStructuredSource

	if input.View.Source == nil {
		return
	}

	res.Source = Location{Path: input.View.Source.Path}

	var selector DiagramSelector

	if input.View.Source.Selector != nil {
		selector = DiagramSelector{Title: input.View.Source.Selector.Title, Anchor: input.View.Source.Selector.Anchor}
	}

	evidence := input.Evidence
	if evidence == nil {
		evidence = &DiagramEvidence{}
	}

	read := ReadDiagram(input.View.Source.Path, selector, evidence)
	if !read.Available() {
		res.Unknown = append(res.Unknown, UnknownReason{Reason: read.Reason, Subject: input.View.Source.Path})

		return
	}

	res.Section, res.QualifiedName = read.Structure.Section, read.Structure.QualifiedName

	var mapping map[string]string
	if input.View.Mapping != nil {
		mapping = input.View.Mapping.Elements
	}

	drawn := resolveMapping(&read.Structure, mapping)
	drawn.gaps(requiredElements(input), mapping)
	drawn.run(input, res)
}

// rasterCorrespondence has no structure to read. Without a depiction it is presence-only; with one, the
// depiction is compared when it is bound to the digest of the committed image.
func rasterCorrespondence(input *CorrespondenceInput, res *CorrespondenceResult) {
	res.Method = MethodPresenceOnly

	if input.View.Source != nil {
		res.Source = Location{Path: input.View.Source.Path}
	}

	if input.View.Mapping == nil || input.View.Mapping.Depicts == nil {
		res.Unknown = append(res.Unknown, UnknownReason{Reason: ReasonPresenceOnly, Subject: res.Source.Path})

		return
	}

	res.Method = MethodSupplementalMapping
	depicts := input.View.Mapping.Depicts

	switch {
	case input.ImageDigest == "":
		res.Unknown = append(res.Unknown, UnknownReason{Reason: ReasonImageUnavailable, Subject: res.Source.Path})

		return
	case input.ImageDigest != depicts.Digest:
		res.Unknown = append(res.Unknown, UnknownReason{Reason: ReasonSupplementalStale, Subject: res.Source.Path})

		return
	}

	drawn := newDrawn()

	for _, id := range depicts.Elements {
		drawn.elements[id] = []Location{res.Source}
	}

	drawn.claimArrows(input, depicts.Relationships, res)
	drawn.run(input, res)
}

// requiredElements gives the necessary elements of the view: the coverage that its projections require.
func requiredElements(input *CorrespondenceInput) []string {
	set := map[string]bool{}

	for _, p := range []*Projection{input.Declared, input.Observed} {
		if p == nil {
			continue
		}

		for _, id := range p.Coverage.Required.Elements {
			set[id] = true
		}
	}

	return sortedKeys(set)
}

// arrowFact is a diagram arrow between two CALM elements.
type arrowFact struct {
	from, to string
	protocol string
	loc      Location
}

// drawn is a diagram as CALM identities: the elements that it shows (with the places of their boxes), its
// arrows between mapped elements, the CALM elements that it cannot compare, and what the mapping found.
type drawn struct {
	elements    map[string][]Location
	skip        map[string]bool
	arrows      []arrowFact
	differences []Difference
	unknown     []UnknownReason
}

func newDrawn() *drawn {
	return &drawn{elements: map[string][]Location{}, skip: map[string]bool{}}
}

// resolveMapping maps the elements and arrows of a diagram structure to CALM IDs (design D10). The key of an
// element is its element_id; only a mapping key that matches no element_id falls back to the label text of
// the node. An identifier that two elements share, and a label that two elements share, are ambiguous: the
// mapped element is not compared, and nothing is guessed.
func resolveMapping(structure *DiagramStructure, mapping map[string]string) *drawn {
	dia := newDrawn()
	byID := elementsByID(structure.Elements)
	assigned, byLabel := dia.assignByID(byID, mapping)

	dia.labelFallback(byLabel, byID, mapping, assigned)
	dia.reportUnmapped(byID, byLabel, assigned)
	dia.placeElements(byID, assigned)
	dia.mapArrows(structure.Arrows, assigned)

	return dia
}

// elementsByID groups diagram elements by their element_id.
func elementsByID(elements []DiagramElement) map[string][]DiagramElement {
	byID := map[string][]DiagramElement{}

	for i := range elements {
		byID[elements[i].ID.ElementID] = append(byID[elements[i].ID.ElementID], elements[i])
	}

	return byID
}

// assignByID maps each element whose element_id is a mapping key. An identifier that two elements share is
// ambiguous. An element without a mapping key is a candidate for the label fallback.
func (d *drawn) assignByID(byID map[string][]DiagramElement, mapping map[string]string) (assigned map[string]string, byLabel map[string][]DiagramElement) {
	assigned = map[string]string{}
	byLabel = map[string][]DiagramElement{}

	for _, key := range sortedKeys(byID) {
		elements := byID[key]
		target, mapped := mapping[key]

		switch {
		case len(elements) > 1:
			d.unknown = append(d.unknown, UnknownReason{Reason: ReasonAmbiguousMapping, Subject: key, Locations: locationsOf(elements)})
			d.holdBack(target)
		case mapped:
			assigned[key] = target
		case elements[0].Label != "":
			byLabel[elements[0].Label] = append(byLabel[elements[0].Label], elements[0])
		}
	}

	return assigned, byLabel
}

// reportUnmapped reports each element that no mapping key reached, neither by identifier nor by label.
func (d *drawn) reportUnmapped(byID, byLabel map[string][]DiagramElement, assigned map[string]string) {
	for _, key := range sortedKeys(byID) {
		if isDecided(key, byID, byLabel, assigned) {
			continue
		}

		d.differences = append(d.differences, Difference{
			Kind: DifferenceUnmapped, Subject: SubjectElement, From: key, Locations: locationsOf(byID[key]),
		})
	}
}

// isDecided says whether the mapping decided an element: it is mapped, ambiguous by identifier, or decided by
// the label fallback.
func isDecided(key string, byID, byLabel map[string][]DiagramElement, assigned map[string]string) bool {
	if _, ok := assigned[key]; ok {
		return true
	}

	return len(byID[key]) > 1 || fellBack(byLabel, &byID[key][0])
}

// placeElements records the places of the boxes of each mapped element under its CALM ID.
func (d *drawn) placeElements(byID map[string][]DiagramElement, assigned map[string]string) {
	for key, target := range assigned {
		for _, e := range byID[key] {
			d.elements[target] = append(d.elements[target], elementLocation(&e))
		}
	}
}

// mapArrows keeps the arrows whose both ends are mapped elements.
func (d *drawn) mapArrows(arrows []DiagramArrow, assigned map[string]string) {
	for i := range arrows {
		arrow := &arrows[i]
		from, fromOK := assigned[arrow.From.ElementID]
		dest, destOK := assigned[arrow.To.ElementID]

		if fromOK && destOK {
			d.arrows = append(d.arrows, arrowFact{
				from: from, to: dest, protocol: arrowProtocol(arrow.Label), loc: Location{Path: arrow.From.File, Line: arrow.Line},
			})
		}
	}
}

// labelFallback maps the elements of a mapping key that is a label. A label that is also an element_id of the
// diagram is not a fallback key: the element with that identifier owns it.
func (d *drawn) labelFallback(
	byLabel map[string][]DiagramElement, byID map[string][]DiagramElement, mapping, assigned map[string]string,
) {
	for _, label := range sortedKeys(byLabel) {
		target, mapped := mapping[label]
		if !mapped || byID[label] != nil {
			delete(byLabel, label)

			continue
		}

		elements := byLabel[label]
		if len(elements) > 1 {
			d.unknown = append(d.unknown, UnknownReason{Reason: ReasonAmbiguousMappingKey, Subject: label, Locations: locationsOf(elements)})
			d.holdBack(target)

			continue
		}

		assigned[elements[0].ID.ElementID] = target
	}
}

// fellBack says whether the label fallback decided an element: it is not unmapped, it is either mapped by its
// label or ambiguous.
func fellBack(byLabel map[string][]DiagramElement, e *DiagramElement) bool {
	return e.Label != "" && byLabel[e.Label] != nil
}

// holdBack keeps a CALM element out of the comparison.
func (d *drawn) holdBack(id string) {
	if id != "" {
		d.skip[id] = true
	}
}

// gaps reports each necessary element that no mapping value points to (a mapping gap, design D10).
func (d *drawn) gaps(required []string, mapping map[string]string) {
	mapped := map[string]bool{}
	for _, target := range mapping {
		mapped[target] = true
	}

	for _, id := range required {
		if !mapped[id] {
			d.unknown = append(d.unknown, UnknownReason{Reason: ReasonMappingGap, Subject: id})
			d.holdBack(id)
		}
	}
}

// claimArrows turns the relationships that a raster image claims to show into arrows, by the model.
func (d *drawn) claimArrows(input *CorrespondenceInput, relationships []string, res *CorrespondenceResult) {
	if len(relationships) == 0 {
		return
	}

	lay := newLayout(input.View, input.Model, input.Bindings)
	declared := relationshipsByID(input.Model)

	for _, id := range relationships {
		rel := declared[id]
		if rel == nil {
			d.unknown = append(d.unknown, UnknownReason{Reason: ReasonDepictedUnknown, Subject: id})

			continue
		}

		d.arrows = append(d.arrows, claimedArrows(lay, rel, res.Source)...)
	}
}

// relationshipsByID indexes the declared relationships of the model; a nil model declares none.
func relationshipsByID(model *intent.ArchitectureModel) map[string]*intent.RelationshipDeclaration {
	declared := map[string]*intent.RelationshipDeclaration{}
	if model == nil {
		return declared
	}

	for i := range model.Relationships {
		declared[model.Relationships[i].ID] = &model.Relationships[i]
	}

	return declared
}

// claimedArrows gives the arrows of one claimed relationship, from its source to each target as the layout
// rolls them up.
func claimedArrows(lay *layout, rel *intent.RelationshipDeclaration, loc Location) []arrowFact {
	from, fromOK := lay.rollUp(rel.Source)
	if !fromOK {
		return nil
	}

	var arrows []arrowFact

	for _, target := range rel.Targets {
		if dest, ok := lay.rollUp(target); ok {
			arrows = append(arrows, arrowFact{from: from, to: dest, protocol: rel.Protocol, loc: loc})
		}
	}

	return arrows
}

// run compares the drawn diagram with the selected projections and adds the result to res.
func (d *drawn) run(input *CorrespondenceInput, res *CorrespondenceResult) {
	selections := d.selections(input, res)

	for _, s := range selections {
		res.Compared = append(res.Compared, s.basis)
	}

	required := requiredElements(input)

	res.Differences = append(res.Differences, d.differences...)
	res.Differences = append(res.Differences, d.compare(selections, required)...)
	res.Unknown = append(res.Unknown, d.unknown...)
}

// selections gives the projections that `comparison.against` selects and that are available. A selected
// projection that is not available is an `unknown` reason. The coverage that the observed projection lost
// holds subjects back from the comparison.
func (d *drawn) selections(input *CorrespondenceInput, res *CorrespondenceResult) []selection {
	var selections []selection

	for _, basis := range res.Against {
		var proj *Projection

		reason := ReasonDeclaredUnavailable

		switch basis {
		case BasisDeclared:
			proj = input.Declared
		case BasisObserved:
			proj, reason = input.Observed, ReasonObservedUnavailable
		}

		if proj == nil {
			d.unknown = append(d.unknown, UnknownReason{Reason: reason, Subject: input.View.ID})

			continue
		}

		selections = append(selections, selection{basis: basis, proj: proj})

		if basis == BasisObserved {
			d.observedCoverage(proj, requiredElements(input), res)
		}
	}

	return selections
}

// observedCoverage reports the coverage that the observed projection lost, the entities that it left out as
// ambiguous, and the necessary elements that touch unbound evidence. Each holds the affected elements back.
func (d *drawn) observedCoverage(obs *Projection, required []string, res *CorrespondenceResult) {
	res.Lost = append(res.Lost, obs.Coverage.Lost...)

	d.lostCoverage(obs.Coverage.Lost)

	if obs.ObservedPart != nil {
		d.ambiguousEntities(obs.Ambiguous)
	}

	if obs.Coverage.Unbound != nil {
		d.unboundEvidence(obs.Coverage.Unbound.Elements, required)
	}
}

// lostCoverage reports the lost coverage; only a lost binding holds its subject back.
func (d *drawn) lostCoverage(lost []LostCoverage) {
	for _, l := range lost {
		if l.Kind != LostBinding {
			d.unknown = append(d.unknown, UnknownReason{Reason: ReasonCoverageLost, Subject: l.Subject})

			continue
		}

		d.unknown = append(d.unknown, UnknownReason{Reason: ReasonNoBinding, Subject: l.Subject})
		d.holdBack(l.Subject)
	}
}

// ambiguousEntities reports the entities that the observed projection left out as ambiguous and holds back
// each candidate.
func (d *drawn) ambiguousEntities(entities []AmbiguousEntity) {
	for _, entity := range entities {
		d.unknown = append(d.unknown, UnknownReason{Reason: ReasonAmbiguousBinding, Subject: entity.Entity, Candidates: entity.Candidates})

		for _, candidate := range entity.Candidates {
			d.holdBack(candidate)
		}
	}
}

// unboundEvidence reports each necessary element that touches unbound evidence.
func (d *drawn) unboundEvidence(unbound, required []string) {
	for _, id := range unbound {
		if slices.Contains(required, id) {
			d.unknown = append(d.unknown, UnknownReason{Reason: ReasonUnboundEvidence, Subject: id})
			d.holdBack(id)
		}
	}
}

// pair is the identity of a relationship for the comparison: tail and head.
type pair struct{ from, to string }

// compare gives the differences between the diagram and the selected projections. `missing` and `incorrect`
// are judged against each projection; `extra` against all of them: an element or an arrow is extra only when no
// selected projection has it.
func (d *drawn) compare(selections []selection, required []string) []Difference {
	out := make([]Difference, 0, len(selections)+1)

	arrows := d.arrowsByPair()
	consumed := map[pair]bool{}

	for _, s := range selections {
		out = append(out, d.compareElements(&s, required)...)
		out = append(out, d.compareRelationships(&s, arrows, consumed)...)
	}

	out = append(out, d.extraElements(selections)...)
	out = append(out, d.extraArrows(selections, arrows, consumed)...)

	return out
}

func (d *drawn) arrowsByPair() map[pair][]arrowFact {
	byPair := map[pair][]arrowFact{}

	for _, a := range d.arrows {
		if !d.skip[a.from] && !d.skip[a.to] {
			byPair[pair{a.from, a.to}] = append(byPair[pair{a.from, a.to}], a)
		}
	}

	return byPair
}

// compareElements reports each necessary element that the projection shows and the diagram does not.
func (d *drawn) compareElements(sel *selection, required []string) []Difference {
	shown := projectedElementIDs(sel.proj)

	var out []Difference

	for _, id := range required {
		if shown[id] && d.isMissing(id) {
			out = append(out, Difference{Kind: DifferenceMissing, Subject: SubjectElement, Basis: sel.basis, From: id})
		}
	}

	return out
}

// isMissing says whether a comparable CALM element has no box in the diagram.
func (d *drawn) isMissing(id string) bool {
	return !d.skip[id] && d.elements[id] == nil
}

// projectedElementIDs gives the IDs of the elements that a projection shows.
func projectedElementIDs(p *Projection) map[string]bool {
	shown := make(map[string]bool, len(p.Elements))
	for i := range p.Elements {
		shown[p.Elements[i].ID] = true
	}

	return shown
}

// extraElements reports each diagram element that no selected projection shows.
func (d *drawn) extraElements(selections []selection) []Difference {
	known := map[string]bool{}

	for _, s := range selections {
		maps.Copy(known, projectedElementIDs(s.proj))
	}

	var out []Difference

	for _, id := range sortedKeys(d.elements) {
		if !known[id] && !d.skip[id] {
			out = append(out, Difference{Kind: DifferenceExtra, Subject: SubjectElement, From: id, Locations: sortedLocations(d.elements[id])})
		}
	}

	return out
}

// rowsByPair groups the relationships of a projection by tail and head.
func rowsByPair(p *Projection) map[pair][]*ProjectionRelationship {
	rows := map[pair][]*ProjectionRelationship{}

	for i := range p.Relationships {
		row := &p.Relationships[i]
		rows[pair{row.From, row.To}] = append(rows[pair{row.From, row.To}], row)
	}

	return rows
}

// compareRelationships reports the relationships of one projection that the diagram lacks (`missing`), draws
// the other way round, or draws with another protocol (`incorrect`). The reversed arrow of an `incorrect`
// direction is consumed: it is not also `extra`.
func (d *drawn) compareRelationships(sel *selection, arrows map[pair][]arrowFact, consumed map[pair]bool) []Difference {
	rows := rowsByPair(sel.proj)

	var out []Difference

	for _, key := range sortedPairs(rows) {
		if d.skip[key.from] || d.skip[key.to] {
			continue
		}

		reversed := pair{key.to, key.from}
		drawnArrows, drawnHere := arrows[key]
		base := Difference{Subject: SubjectRelationship, Basis: sel.basis, From: key.from, To: key.to}

		switch {
		case drawnHere:
			if detail, wrong := protocolMismatch(drawnArrows, rows[key]); wrong {
				base.Kind, base.Detail, base.Locations = DifferenceIncorrect, detail, arrowLocations(drawnArrows)
				out = append(out, base)
			}
		case len(arrows[reversed]) > 0 && rows[reversed] == nil:
			consumed[reversed] = true
			base.Kind, base.Detail, base.Locations = DifferenceIncorrect, "direction", arrowLocations(arrows[reversed])
			out = append(out, base)
		default:
			base.Kind = DifferenceMissing
			out = append(out, base)
		}
	}

	return out
}

// extraArrows reports each drawn pair that no selected projection has, and that is not the reversed arrow of an
// incorrect direction.
func (d *drawn) extraArrows(selections []selection, arrows map[pair][]arrowFact, consumed map[pair]bool) []Difference {
	known := map[pair]bool{}

	for _, s := range selections {
		for p := range rowsByPair(s.proj) {
			known[p] = true
		}
	}

	var out []Difference

	for _, p := range sortedPairs(arrows) {
		if !known[p] && !consumed[p] {
			out = append(out, Difference{
				Kind: DifferenceExtra, Subject: SubjectRelationship, From: p.from, To: p.to, Locations: arrowLocations(arrows[p]),
			})
		}
	}

	return out
}

// protocolMismatch says whether an arrow carries a protocol that the rows of the same pair do not have. It is
// compared only when both sides carry a protocol.
func protocolMismatch(arrows []arrowFact, rows []*ProjectionRelationship) (string, bool) {
	held := heldProtocols(rows)
	if len(held) == 0 {
		return "", false
	}

	for _, a := range arrows {
		if a.protocol == "" || slices.ContainsFunc(held, func(p string) bool { return strings.EqualFold(p, a.protocol) }) {
			continue
		}

		slices.Sort(held)

		return "protocol " + a.protocol + " " + strings.Join(held, ","), true
	}

	return "", false
}

// heldProtocols gives the non-empty protocols that the rows of a pair carry.
func heldProtocols(rows []*ProjectionRelationship) []string {
	var held []string

	for _, row := range rows {
		if p := strings.TrimSpace(deref(row.Protocol)); p != "" {
			held = append(held, p)
		}
	}

	return held
}

// protocolWords are the labels that name a transport or wire protocol. An arrow label is a protocol only when it
// is one of these, or when it ends in a bracketed technology as in the C4 notation `Description [HTTPS]`.
// Any other label is a description that the comparison does not read.
var protocolWords = map[string]bool{
	"http": true, "https": true, "grpc": true, "tcp": true, "udp": true, "tls": true, "amqp": true, "mqtt": true,
	"kafka": true, "jdbc": true, "sql": true, "graphql": true, "rest": true, "soap": true, "ws": true, "wss": true,
	"websocket": true, "ssh": true, "sftp": true, "ftp": true, "smtp": true, "redis": true, "nats": true,
}

var bracketTechnology = regexp.MustCompile(`\[([^\[\]]+)\]\s*$`)

// arrowProtocol gives the protocol that an arrow label carries, or "" when the label is a description.
func arrowProtocol(label string) string {
	label = strings.TrimSpace(label)

	if m := bracketTechnology.FindStringSubmatch(label); m != nil {
		return strings.TrimSpace(m[1])
	}

	if protocolWords[strings.ToLower(label)] {
		return label
	}

	return ""
}

func elementLocation(e *DiagramElement) Location {
	return Location{Path: e.ID.File, Line: e.Line}
}

func locationsOf(elements []DiagramElement) []Location {
	out := make([]Location, 0, len(elements))

	for i := range elements {
		out = append(out, elementLocation(&elements[i]))
	}

	return sortedLocations(out)
}

func arrowLocations(arrows []arrowFact) []Location {
	out := make([]Location, 0, len(arrows))
	for _, a := range arrows {
		out = append(out, a.loc)
	}

	return sortedLocations(out)
}

func sortedLocations(locations []Location) []Location {
	out := slices.Clone(locations)
	slices.SortFunc(out, func(a, b Location) int { return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line)) })

	return out
}

func sortedPairs[V any](set map[pair]V) []pair {
	out := slices.Collect(maps.Keys(set))
	slices.SortFunc(out, func(a, b pair) int { return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to)) })

	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// differenceKinds are the kinds of difference in the order of the result.
var differenceKinds = []string{DifferenceMissing, DifferenceExtra, DifferenceIncorrect, DifferenceUnmapped}

// finish sorts the result and sets its status, reasons and next action.
func (r *CorrespondenceResult) finish() {
	slices.SortStableFunc(r.Differences, func(a, b Difference) int {
		return cmp.Or(
			cmp.Compare(slices.Index(differenceKinds, a.Kind), slices.Index(differenceKinds, b.Kind)), cmp.Compare(a.Subject, b.Subject),
			cmp.Compare(a.Basis, b.Basis), cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To),
		)
	})

	slices.SortStableFunc(r.Unknown, func(a, b UnknownReason) int {
		return cmp.Or(cmp.Compare(a.Reason, b.Reason), cmp.Compare(a.Subject, b.Subject))
	})

	r.Unknown = slices.CompactFunc(r.Unknown, func(a, b UnknownReason) bool {
		return a.Reason == b.Reason && a.Subject == b.Subject && slices.Equal(a.Locations, b.Locations)
	})

	slices.Sort(r.Compared)
	r.Compared = slices.Compact(r.Compared)

	reasons := map[string]bool{}
	for i := range r.Unknown {
		reasons[r.Unknown[i].Reason] = true
	}

	for i := range r.Differences {
		reasons[r.Differences[i].Kind+" "+r.Differences[i].Subject] = true
	}

	r.Reasons = sortedKeys(reasons)

	switch {
	case len(r.Differences) > 0:
		r.Status = StatusViolation
	case len(r.Unknown) > 0:
		r.Status = StatusUnknown
	default:
		r.Status = StatusPass
	}

	r.NextAction = r.nextAction()
}

// nextAction gives the next-action category (design D10): a difference needs corrected intent, a binding
// problem a resolved binding, a mapping or evidence problem supplied evidence, and a lost capability a fixed
// analysis configuration.
func (r *CorrespondenceResult) nextAction() string {
	if len(r.Differences) > 0 {
		return NextActionCorrectIntent
	}

	actions := map[string]bool{}

	for i := range r.Unknown {
		actions[unknownReasonAction(r.Unknown[i].Reason)] = true
	}

	for _, action := range []string{conformance.NextActionResolveBinding, NextActionSupplyEvidence, NextActionFixAnalysis} {
		if actions[action] {
			return action
		}
	}

	return ""
}

// unknownReasonAction gives the next-action category of one reason that correspondence is unknown.
func unknownReasonAction(reason string) string {
	switch reason {
	case ReasonNoBinding, ReasonAmbiguousBinding:
		return conformance.NextActionResolveBinding
	case ReasonCoverageLost, ReasonObservedUnavailable, ReasonDeclaredUnavailable:
		return NextActionFixAnalysis
	}

	return NextActionSupplyEvidence
}
