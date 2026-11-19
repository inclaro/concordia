package impact

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// NodeType is the type of a graph node.
type NodeType string

// Node types. Other capabilities register consumer nodes with NodeConsumer or one of the typed values.
const (
	NodeItem            NodeType = "item"
	NodeAssumption      NodeType = "assumption"
	NodeAssessment      NodeType = "assessment"
	NodeView            NodeType = "view"
	NodeControl         NodeType = "control"
	NodeQualityScenario NodeType = "quality-scenario"
)

// EdgeType is the type of a dependency edge.
type EdgeType string

// Edge types. The edge points from the dependent to what it depends on.
const (
	// EdgeDependsOn: an assumption or assessment depends on an item or assumption.
	EdgeDependsOn EdgeType = "DEPENDS_ON"
	// EdgeUsesEvidence: an assessment or assumption uses an evidence node.
	EdgeUsesEvidence EdgeType = "USES_EVIDENCE"
	// EdgeAssesses: an assessment assesses a subject node.
	EdgeAssesses EdgeType = "ASSESSES"
	// EdgeConsumes: a consumer (view, control, quality scenario) consumes a node.
	EdgeConsumes EdgeType = "CONSUMES"
)

// NodeID is the identity and version of a graph node. The visited set of a traversal is keyed by it.
// Items use an empty version; assessments and assumptions use their version number as text.
type NodeID struct {
	Identity string `json:"identity"`
	Version  string `json:"version"`
}

// String gives `identity` or `identity@version`.
func (n NodeID) String() string {
	if n.Version == "" {
		return n.Identity
	}

	return n.Identity + "@" + n.Version
}

// ItemNodeID gives the node of a dependency item. Items have no version.
func ItemNodeID(kind Kind, identity string) NodeID {
	return NodeID{Identity: string(kind) + ":" + identity}
}

// CompareNodeID orders by identity, then version, in UTF-16 order.
func CompareNodeID(a, b NodeID) int {
	return cmp.Or(canonjson.CompareUTF16(a.Identity, b.Identity), canonjson.CompareUTF16(a.Version, b.Version))
}

// Graph is the dependency graph. Build it with AddNode and AddDependency; it is not safe for
// concurrent writes.
type Graph struct {
	types map[NodeID]NodeType
	// dependents[x] are the nodes that depend on x, with the edge type.
	dependents map[NodeID]map[NodeID]EdgeType
	// dependencies[x] are the nodes that x depends on.
	dependencies map[NodeID]map[NodeID]EdgeType
}

// NewGraph gives an empty graph.
func NewGraph() *Graph {
	return &Graph{
		types:        map[NodeID]NodeType{},
		dependents:   map[NodeID]map[NodeID]EdgeType{},
		dependencies: map[NodeID]map[NodeID]EdgeType{},
	}
}

// AddNode registers a node with its type. A second call with the same ID replaces the type.
func (g *Graph) AddNode(id NodeID, t NodeType) { g.types[id] = t }

// Type gives the type of a node; unregistered nodes are items.
func (g *Graph) Type(id NodeID) NodeType {
	if t, ok := g.types[id]; ok {
		return t
	}

	return NodeItem
}

// AddDependency records that dependent depends on dependency. A pair that has several edge types
// keeps the smallest type text, so the graph does not depend on the order of calls.
func (g *Graph) AddDependency(dependent, dependency NodeID, edge EdgeType) {
	for _, id := range []NodeID{dependent, dependency} {
		if _, ok := g.types[id]; !ok {
			g.types[id] = NodeItem
		}
	}

	set := func(m map[NodeID]map[NodeID]EdgeType, from, to NodeID) {
		if m[from] == nil {
			m[from] = map[NodeID]EdgeType{}
		}

		if old, ok := m[from][to]; !ok || edge < old {
			m[from][to] = edge
		}
	}

	set(g.dependents, dependency, dependent)
	set(g.dependencies, dependent, dependency)
}

// Edge gives the type of the edge from dependent to dependency.
func (g *Graph) Edge(dependent, dependency NodeID) (EdgeType, bool) {
	t, ok := g.dependencies[dependent][dependency]

	return t, ok
}

// Nodes gives every node, sorted.
func (g *Graph) Nodes() []NodeID {
	out := make([]NodeID, 0, len(g.types))
	for id := range g.types {
		out = append(out, id)
	}

	slices.SortFunc(out, CompareNodeID)

	return out
}

func sortedKeys(m map[NodeID]EdgeType) []NodeID {
	out := make([]NodeID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}

	slices.SortFunc(out, CompareNodeID)

	return out
}

// Closure gives every node that id depends on, directly or through other nodes, sorted. It is the
// dependency closure of a full evaluation: a visited set keeps each node once, so a cycle ends.
func (g *Graph) Closure(id NodeID) []NodeID {
	seen := map[NodeID]bool{id: true}
	stack := []NodeID{id}

	var out []NodeID

	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		for _, dep := range sortedKeys(g.dependencies[node]) {
			if !seen[dep] {
				seen[dep] = true
				out = append(out, dep)
				stack = append(stack, dep)
			}
		}
	}

	slices.SortFunc(out, CompareNodeID)

	if out == nil {
		out = []NodeID{}
	}

	return out
}
