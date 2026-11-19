// Package projection maps the CALM architecture and a conformance run to a C4
// view: it makes the Structurizr DSL workspace and runs the Structurizr CLI.
package projection

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

// SyntheticSystemID is the identifier of the synthetic system that holds
// containers without a parent.
const SyntheticSystemID = "gu_system_default"

// levelNone marks a node type that has no C4 level.
const levelNone intent.C4Level = ""

// C4Element is one element of the C4 model.
type C4Element struct {
	// Node is the CALM node. It is nil for the synthetic system.
	Node *intent.NodeDeclaration
	// ID is the CALM unique-id. For the synthetic system, SyntheticSystemID.
	ID    string
	Level intent.C4Level
	// Parent is the element ID of the parent. It is empty when there is none.
	Parent    string
	Name      string
	Synthetic bool
}

// C4Map is the result of MapC4. Elements are sorted by ID and include the
// synthetic system (if any).
type C4Map struct {
	Elements    []C4Element
	Diagnostics []diag.Diagnostic
}

// defaultLevel is the C4 level of a node type with no override. A node type that
// is not in the table is a container.
var defaultLevel = map[string]intent.C4Level{
	"actor":      intent.C4Person,
	"system":     intent.C4SoftwareSystem,
	"ecosystem":  intent.C4SoftwareSystem,
	"service":    intent.C4Container,
	"webclient":  intent.C4Container,
	"database":   intent.C4Container,
	"ldap":       intent.C4Container,
	"data-asset": intent.C4Container,
	"network":    levelNone,
}

var validLevels = []intent.C4Level{intent.C4Person, intent.C4SoftwareSystem, intent.C4Container, intent.C4Component}

// bindingLevelSets gives, for each CALM ID, the C4 levels that bindings give to it.
func bindingLevelSets(bindings []binding.Binding) map[string]map[string]bool {
	levels := map[string]map[string]bool{}

	for i := range bindings {
		level := ""
		if bindings[i].Projection != nil {
			level = bindings[i].Projection.C4Level
		}

		if level == "" {
			continue
		}

		set := levels[bindings[i].ArchitectureRef]
		if set == nil {
			set = map[string]bool{}
			levels[bindings[i].ArchitectureRef] = set
		}

		set[level] = true
	}

	return levels
}

// nodeLevel gives the C4 level of a node. A node with no level gives a diagnostic
// (CO-PRJ-005 for a bad override, CO-PRJ-006 for a node type with no C4 level).
func nodeLevel(model *intent.ArchitectureModel, node *intent.NodeDeclaration, fromBindings map[string]bool, deploymentNode bool) (intent.C4Level, *diag.Diagnostic) {
	source := diag.WithSource(diag.Source{URI: model.SourceURI, Pointer: node.Pointer})
	candidates := map[string]bool{}

	for level := range fromBindings {
		candidates[level] = true
	}

	if node.C4Level != "" {
		candidates[string(node.C4Level)] = true
	}

	switch len(candidates) {
	case 0:
	case 1:
		override := intent.C4Level(onlyKey(candidates))
		if !slices.Contains(validLevels, override) {
			d := diag.New(diag.PRJ005, diag.Detail("node '"+node.ID+"' has unknown level '"+string(override)+"'"), source)

			return levelNone, &d
		}

		return override, nil
	default:
		sorted := make([]string, 0, len(candidates))
		for level := range candidates {
			sorted = append(sorted, level)
		}

		slices.SortFunc(sorted, canonjson.CompareUTF16)

		d := diag.New(diag.PRJ005, diag.Detail("node '"+node.ID+"' has conflicting levels: "+strings.Join(sorted, ", ")), source)

		return levelNone, &d
	}

	level, known := defaultLevel[node.NodeType]
	if !known {
		return intent.C4Container, nil
	}

	if level == levelNone {
		if deploymentNode && node.NodeType == "network" {
			return levelNone, nil
		}

		d := diag.New(diag.PRJ006, diag.Params{"id": node.ID, "detail": "node-type " + node.NodeType + " is shown only in deployment views"})

		return levelNone, &d
	}

	return level, nil
}

// onlyKey gives the single key of a one-item set.
func onlyKey(set map[string]bool) string {
	for key := range set {
		return key
	}

	return ""
}

// parentFinder gives the parent of an element: the container of the first
// `composed-of` relationship (sorted by ID) that has the element as a target and
// whose container has the level.
type parentFinder struct {
	levels   map[string]intent.C4Level
	composed []intent.RelationshipDeclaration
}

func newParentFinder(model *intent.ArchitectureModel, levels map[string]intent.C4Level) parentFinder {
	composed := make([]intent.RelationshipDeclaration, 0, len(model.Relationships))

	for i := range model.Relationships {
		if rel := model.Relationships[i]; rel.Kind == intent.RelComposedOf && rel.Container != "" {
			composed = append(composed, rel)
		}
	}

	slices.SortFunc(composed, func(a, b intent.RelationshipDeclaration) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return parentFinder{composed: composed, levels: levels}
}

func (f parentFinder) of(id string, parentLevel intent.C4Level) string {
	for i := range f.composed {
		if slices.Contains(f.composed[i].Targets, id) && f.levels[f.composed[i].Container] == parentLevel {
			return f.composed[i].Container
		}
	}

	return ""
}

// MapC4 maps the CALM model to C4 elements (the PRD table). workspaceName names
// the synthetic system.
func MapC4(model *intent.ArchitectureModel, bindings []binding.Binding, workspaceName string) C4Map {
	var diagnostics []diag.Diagnostic

	fromBindings := bindingLevelSets(bindings)

	nodes := slices.Clone(model.Nodes)
	slices.SortFunc(nodes, func(a, b intent.NodeDeclaration) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	deploying := deploymentContainers(model)

	levels := make(map[string]intent.C4Level, len(nodes))

	for i := range nodes {
		level, problem := nodeLevel(model, &nodes[i], fromBindings[nodes[i].ID], deploying[nodes[i].ID])
		if problem != nil {
			diagnostics = append(diagnostics, *problem)

			continue
		}

		if level == levelNone {
			continue
		}

		levels[nodes[i].ID] = level
	}

	parents := newParentFinder(model, levels)
	elements := make([]C4Element, 0, len(nodes)+1)
	needSynthetic := false

	for i := range nodes {
		node := &nodes[i]

		level, mapped := levels[node.ID]
		if !mapped {
			continue
		}

		elem := C4Element{ID: node.ID, Level: level, Node: node, Name: node.Name}

		switch level {
		case intent.C4Container:
			if elem.Parent = parents.of(node.ID, intent.C4SoftwareSystem); elem.Parent == "" {
				elem.Parent = SyntheticSystemID
				needSynthetic = true
			}
		case intent.C4Component:
			if elem.Parent = parents.of(node.ID, intent.C4Container); elem.Parent == "" {
				diagnostics = append(diagnostics, diag.New(diag.PRJ005,
					diag.Detail("component '"+node.ID+"' has no container parent (composed-of)"),
					diag.WithSource(diag.Source{URI: model.SourceURI, Pointer: node.Pointer})))

				continue
			}
		case intent.C4Person, intent.C4SoftwareSystem:
		}

		elements = append(elements, elem)
	}

	if needSynthetic {
		elements = append(elements, C4Element{ID: SyntheticSystemID, Level: intent.C4SoftwareSystem, Synthetic: true, Name: workspaceName})
	}

	slices.SortFunc(elements, func(a, b C4Element) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return C4Map{Elements: elements, Diagnostics: diagnostics}
}
