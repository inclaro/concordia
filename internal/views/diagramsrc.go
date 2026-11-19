package views

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/observation"
)

// Reasons that a diagram structure is not available. The text of
// ReasonStructureNotAvailable is the reason that correspondence reports
// (`unknown` with the reason `structure not available`).
const (
	ReasonStructureNotAvailable = "structure not available"
	// ReasonSelectorAmbiguous means that more than one diagram block of the file matches the selector.
	ReasonSelectorAmbiguous = "diagram selector is ambiguous"
)

// Diagram kind and format that have a recorded provider profile (CBM 0.12.1,
// O12). Any other value gives no structure.
const (
	supportedDiagramKind   = "flowchart"
	supportedDiagramFormat = "mermaid"
)

const (
	labelDiagram        = "Diagram"
	labelDiagramElement = "DiagramElement"
)

// DiagramSelector selects one diagram block of a source file by the heading of
// its section. Title and Anchor are the `selector` keys of a view `source`;
// both empty selects the only diagram of the file.
type DiagramSelector struct {
	Title  string
	Anchor string
}

// DiagramEvidence is the kept provider evidence that the reader uses: the
// observed nodes by observed ID and the normalized edges of a fetch
// (observation.EvidenceQueryResult.Nodes and .Edges). Nodes must include the
// `Diagram` nodes, for `diagram_kind` and `format`.
type DiagramEvidence struct {
	Nodes map[string]observation.ObservedNode
	Edges []*observation.EvidenceEdge
}

// DiagramElement is a node of a diagram, identified by the author's node ID.
type DiagramElement struct {
	// ID identifies the element across block reorders: file, section and element_id.
	ID observation.DiagramElementID
	// Kind is the provider `element_kind`.
	Kind string
	// Label is the label text of the node (`Orders API` for `api[Orders API]`),
	// the provider `name` when it differs from the node ID. Empty when the
	// author wrote no label text. It is the mapping key only as a fallback.
	Label string
	// Line is the 1-based line of the element. 0 means none.
	Line int
}

// DiagramArrow is an arrow of a diagram. From is the tail and To is the head,
// as the edge gives them.
type DiagramArrow struct {
	From  observation.DiagramElementID
	To    observation.DiagramElementID
	Label string
	Style string
	// Line is the 1-based line of the arrow. 0 means none.
	Line int
}

// DiagramStructure is the structure of one diagram block.
type DiagramStructure struct {
	// Path is the diagram file, repository relative.
	Path string
	// Section is the section of the diagram block.
	Section string
	// QualifiedName is the provider qualified name of the block, with the
	// ordinal (`#diagram-<ordinal>`). It changes when blocks are reordered and
	// is for display only, never identity or a mapping key.
	QualifiedName string
	// DiagramKind and Format are the provider `diagram_kind` and `format`.
	DiagramKind string
	Format      string
	// Elements are sorted by element ID; a repeated element_id stays repeated.
	Elements []DiagramElement
	// Arrows are sorted by line, from, to, label.
	Arrows []DiagramArrow
}

// DiagramRead is the result of ReadDiagram. When Reason is empty, Structure
// holds the diagram; otherwise the structure is not available and Reason says why.
type DiagramRead struct {
	Reason    string
	Structure DiagramStructure
}

// Available says whether the provider supplied the structure.
func (r *DiagramRead) Available() bool { return r.Reason == "" }

// ReadDiagram reads the diagram of file that the selector names from kept
// provider evidence. It only reads nodes and arrows: it never opens, follows
// or runs a link, hook or command of the diagram, and a link text is kept as
// the label of its arrow, nothing more.
//
// The structure is not available (ReasonStructureNotAvailable) when no block
// matches, the provider gave no `Diagram` node for the block, the
// `diagram_kind` or `format` has no recorded profile, or the block has no
// elements or no arrows. A section can hold more than one diagram block, and
// the provider numbers them in the order of the file (`#diagram-<ordinal>`), so
// the selector chooses among the blocks that have a recorded profile: more than
// one of them gives ReasonSelectorAmbiguous.
func ReadDiagram(file string, selector DiagramSelector, evidence *DiagramEvidence) DiagramRead {
	blocks := diagramBlocks(file, evidence)

	var matching []string

	for _, qualifiedName := range slices.Sorted(blockKeys(blocks)) {
		if block := blocks[qualifiedName]; selectorMatches(selector, block.section) && block.supported() {
			matching = append(matching, qualifiedName)
		}
	}

	switch len(matching) {
	case 0:
		return DiagramRead{Reason: ReasonStructureNotAvailable}
	case 1:
	default:
		return DiagramRead{Reason: ReasonSelectorAmbiguous}
	}

	block := blocks[matching[0]]

	structure := DiagramStructure{
		Path:          file,
		Section:       block.section,
		QualifiedName: matching[0],
		DiagramKind:   block.kind,
		Format:        block.format,
		Elements:      block.elements,
		Arrows:        arrowsOf(evidence, block),
	}

	if len(structure.Elements) == 0 || len(structure.Arrows) == 0 {
		return DiagramRead{Reason: ReasonStructureNotAvailable}
	}

	slices.SortStableFunc(structure.Elements, func(a, b DiagramElement) int { return compareElementIDs(a.ID, b.ID) })
	slices.SortStableFunc(structure.Arrows, func(a, b DiagramArrow) int {
		return cmp.Or(
			cmp.Compare(a.Line, b.Line),
			compareElementIDs(a.From, b.From),
			compareElementIDs(a.To, b.To),
			cmp.Compare(a.Label, b.Label),
		)
	})

	return DiagramRead{Structure: structure}
}

// diagramBlock is one diagram block of a file, found by its qualified name.
type diagramBlock struct {
	elementNodes   map[string]observation.DiagramElementID // observed node ID to element identity
	section        string
	kind           string
	format         string
	elements       []DiagramElement
	hasDiagramNode bool
}

// supported says whether the provider gave a `Diagram` node with a recorded profile.
func (b *diagramBlock) supported() bool {
	return b.hasDiagramNode && b.kind == supportedDiagramKind && b.format == supportedDiagramFormat
}

func blockKeys(blocks map[string]*diagramBlock) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for key := range blocks {
			if !yield(key) {
				return
			}
		}
	}
}

// diagramBlocks groups the `Diagram` and `DiagramElement` nodes of file by the
// qualified name of the diagram block.
func diagramBlocks(file string, evidence *DiagramEvidence) map[string]*diagramBlock {
	blocks := map[string]*diagramBlock{}

	blockOf := func(qualifiedName, section string) *diagramBlock {
		block, ok := blocks[qualifiedName]
		if !ok {
			block = &diagramBlock{section: section, elementNodes: map[string]observation.DiagramElementID{}}
			blocks[qualifiedName] = block
		}

		return block
	}

	for observedID := range evidence.Nodes {
		node := evidence.Nodes[observedID]
		if node.FilePath != file {
			continue
		}

		switch node.Label {
		case labelDiagram:
			id, err := observation.DiagramElementOf(file, node.QualifiedName, "x")
			if err != nil {
				continue
			}

			block := blockOf(node.QualifiedName, id.Section)
			block.hasDiagramNode = true
			block.kind = node.Properties["diagram_kind"]
			block.format = node.Properties["format"]
		case labelDiagramElement:
			id, err := observation.DiagramElementOfNode(&node)
			if err != nil {
				continue
			}

			diagramQN := strings.TrimSuffix(node.QualifiedName, "."+id.ElementID)
			block := blockOf(diagramQN, id.Section)
			block.elementNodes[observedID] = id
			block.elements = append(block.elements, DiagramElement{
				ID: id, Kind: node.Properties["element_kind"], Label: labelOf(&id, &node), Line: node.StartLine,
			})
		}
	}

	return blocks
}

// arrowsOf gives the `diagram-arrow` facts whose endpoints are both elements of the block.
func arrowsOf(evidence *DiagramEvidence, block *diagramBlock) []DiagramArrow {
	var arrows []DiagramArrow

	for _, edge := range evidence.Edges {
		if edge == nil || edge.Kind != observation.KindDiagramArrow {
			continue
		}

		from, fromOK := block.elementNodes[edge.From]
		head, headOK := block.elementNodes[edge.To]

		if !fromOK || !headOK {
			continue
		}

		line := 0
		if edge.SourceSpan != nil {
			line = edge.SourceSpan.Line
		}

		arrows = append(arrows, DiagramArrow{From: from, To: head, Label: edge.Properties["label"], Style: edge.Properties["style"], Line: line})
	}

	return arrows
}

// labelOf gives the label text of a diagram element: CBM 0.12.1 puts the label
// text in the node `name`, and the node ID there when the author wrote none.
func labelOf(id *observation.DiagramElementID, node *observation.ObservedNode) string {
	if name := node.Properties["name"]; name != id.ElementID {
		return name
	}

	return ""
}

func compareElementIDs(a, b observation.DiagramElementID) int {
	return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Section, b.Section), cmp.Compare(a.ElementID, b.ElementID))
}

// selectorMatches compares the section of a block, a heading slug such as
// `Context-diagram`, with the title (spaces become `-`) or the anchor of the
// selector, ignoring case. An empty selector matches every section.
func selectorMatches(selector DiagramSelector, section string) bool {
	switch {
	case selector.Title != "":
		return strings.EqualFold(section, strings.ReplaceAll(strings.TrimSpace(selector.Title), " ", "-"))
	case selector.Anchor != "":
		return strings.EqualFold(section, strings.TrimPrefix(selector.Anchor, "#"))
	default:
		return true
	}
}
