package impact

import (
	"slices"
)

// Digest gives the digest of the graph: every node with its type and every edge with its type, sorted. Two graphs
// with the same digest give the same traversal result for the same seeds and limits, so the incremental cache keys
// the traversal results by it (design D15).
func (g *Graph) Digest() string {
	sum := NewHasher()

	for _, id := range g.Nodes() {
		sum.Add("n", id.Identity, id.Version, string(g.types[id]))
	}

	dependents := make([]NodeID, 0, len(g.dependencies))
	for id := range g.dependencies {
		dependents = append(dependents, id)
	}

	slices.SortFunc(dependents, CompareNodeID)

	for _, dependent := range dependents {
		deps := g.dependencies[dependent]

		for _, dependency := range sortedKeys(deps) {
			sum.Add("e", dependent.Identity, dependent.Version, dependency.Identity, dependency.Version, string(deps[dependency]))
		}
	}

	return sum.Sum()
}
