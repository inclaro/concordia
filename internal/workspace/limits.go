package workspace

// Default resource limits. They are far above the sizes of the fixtures and of
// common repositories; the keys and their meaning do not depend on the numbers.
const (
	DefaultMaxNodes   = 5_000_000
	DefaultMaxEdges   = 20_000_000
	DefaultMaxVisited = 10_000_000
	DefaultMaxFacts   = 20_000_000
)

// GraphLimits bound the size of the graph that a run reads.
type GraphLimits struct {
	// MaxNodes is the most nodes of the observed graph.
	MaxNodes int `json:"maxNodes"`
	// MaxEdges is the most edges (facts, with the unsupported ones) that the provider gave.
	MaxEdges int `json:"maxEdges"`
}

// TraversalLimits bound each cycle or path traversal.
type TraversalLimits struct {
	// MaxVisited is the most nodes that one traversal visits before it stops and says it is incomplete.
	MaxVisited int `json:"maxVisited"`
}

// EvidenceLimits bound the evidence that an evaluation processes.
type EvidenceLimits struct {
	// MaxFacts is the most facts (edges and synthesized entities) of the evidence of a run.
	MaxFacts int `json:"maxFacts"`
}

// Limits are the resource limits of a run (`limits` in the workspace file).
// A run that goes above a limit reports the limit, the configured value and the
// observed value, and the results that depend on it are `unknown`; Concordia
// never cuts evidence to stay below a limit.
type Limits struct {
	Graph     GraphLimits     `json:"graph"`
	Traversal TraversalLimits `json:"traversal"`
	Evidence  EvidenceLimits  `json:"evidence"`
}

// DefaultLimits gives the limits that a workspace without `limits` has.
func DefaultLimits() Limits {
	return Limits{
		Graph:     GraphLimits{MaxNodes: DefaultMaxNodes, MaxEdges: DefaultMaxEdges},
		Traversal: TraversalLimits{MaxVisited: DefaultMaxVisited},
		Evidence:  EvidenceLimits{MaxFacts: DefaultMaxFacts},
	}
}

// limitsOf reads the `limits` block; a key that is not set has its default.
func limitsOf(root object) Limits {
	block := obj(root, "limits")
	graph, traversal, evidence := obj(block, "graph"), obj(block, "traversal"), obj(block, "evidence")

	return Limits{
		Graph: GraphLimits{
			MaxNodes: int(num(graph, "maxNodes", DefaultMaxNodes)),
			MaxEdges: int(num(graph, "maxEdges", DefaultMaxEdges)),
		},
		Traversal: TraversalLimits{MaxVisited: int(num(traversal, "maxVisited", DefaultMaxVisited))},
		Evidence:  EvidenceLimits{MaxFacts: int(num(evidence, "maxFacts", DefaultMaxFacts))},
	}
}
