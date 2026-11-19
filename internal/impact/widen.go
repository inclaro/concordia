package impact

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Unresolved is a selector of an assessment whose closure cannot be computed, for example because
// it depends on a binding that is unresolved.
type Unresolved struct {
	// Selector names the selector, for the explanation.
	Selector string `json:"selector"`
	// Reason is the closed reason of the gap, for example `binding-gap`.
	Reason string `json:"reason"`
}

// Closure is the set of dependency nodes that an assessment is evaluated against.
type Closure struct {
	// Reason is the reason of the unknown (the reason of the first unresolved selector).
	Reason string `json:"reason,omitempty"`
	// Nodes are the nodes of the closure, sorted.
	Nodes []NodeID `json:"nodes"`
	// Unresolved are the selectors that could not be resolved, sorted.
	Unresolved []Unresolved `json:"unresolved"`
	// Widened says that the closure is the full declared scope because a selector could not be resolved.
	Widened bool `json:"widened"`
	// Unknown says that neither the closure nor the declared scope can be computed.
	Unknown bool `json:"unknown"`
}

// WidenClosure gives the closure of one assessment (SEC-07). When every selector resolved, the
// closure is the resolved nodes. When one did not, the closure is widened to the full declared scope
// (the resolved nodes plus declaredScope()). When declaredScope reports that it cannot be computed,
// the closure is unknown, with the reason of the first unresolved selector, so the currency is
// `unknown` and never `current`.
func WidenClosure(resolved []NodeID, unresolved []Unresolved, declaredScope func() ([]NodeID, bool)) Closure {
	out := Closure{Nodes: slices.Clone(resolved), Unresolved: slices.Clone(unresolved)}
	if out.Nodes == nil {
		out.Nodes = []NodeID{}
	}

	if out.Unresolved == nil {
		out.Unresolved = []Unresolved{}
	}

	slices.SortFunc(out.Unresolved, func(a, b Unresolved) int {
		return cmp.Or(canonjson.CompareUTF16(a.Selector, b.Selector), canonjson.CompareUTF16(a.Reason, b.Reason))
	})

	if len(unresolved) > 0 {
		scope, ok := declaredScope()
		if !ok {
			out.Unknown = true
			out.Reason = out.Unresolved[0].Reason

			if out.Reason == "" {
				out.Reason = ReasonCoverageIncomplete
			}

			return out
		}

		out.Widened = true
		out.Nodes = append(out.Nodes, scope...)
	}

	slices.SortFunc(out.Nodes, CompareNodeID)
	out.Nodes = slices.Compact(out.Nodes)

	return out
}

// Widen makes the assessment depend on every node of the closure, so that a change to any of them
// reaches it in a traversal.
func (g *Graph) Widen(assessment NodeID, closure Closure) {
	for _, n := range closure.Nodes {
		g.AddDependency(assessment, n, EdgeDependsOn)
	}
}
