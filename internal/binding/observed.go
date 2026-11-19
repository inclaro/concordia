package binding

import (
	"context"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// WithFileNodes adds one synthetic `File` node for each indexed file that has
// no `File` node in nodes. It gives all nodes sorted by observed ID.
func WithFileNodes(project string, nodes map[string]observation.ObservedNode, files []string) []observation.ObservedNode {
	seen := map[string]struct{}{}

	out := make(map[string]observation.ObservedNode, len(nodes))
	for id := range nodes {
		n := nodes[id]
		out[id] = n

		if n.Label == "File" {
			seen[n.FilePath] = struct{}{}
		}
	}

	for _, f := range files {
		if _, ok := seen[f]; ok {
			continue
		}

		id := "cbm:" + project + ":file:" + f
		out[id] = observation.ObservedNode{ObservedID: id, Label: "File", QualifiedName: "", FilePath: f}
	}

	list := make([]observation.ObservedNode, 0, len(out))
	for id := range out {
		list = append(list, out[id])
	}

	slices.SortFunc(list, func(a, b observation.ObservedNode) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })

	return list
}

// CollectObservedNodes gives all observed nodes that can carry a binding: the
// endpoints of `DEFINES` and `DEFINES_METHOD` edges, plus one synthetic `File`
// node for each indexed file that has no `File` node in those edges. Sorted by observed ID.
func CollectObservedNodes(ctx context.Context, provider observation.Provider, ref *observation.GraphSnapshotRef) ([]observation.ObservedNode, error) {
	files, err := provider.ListFiles(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}

	res, err := provider.FetchEdges(ctx, ref, []string{"DEFINES", "DEFINES_METHOD"})
	if err != nil {
		return nil, fmt.Errorf("fetch definition edges: %w", err)
	}

	return WithFileNodes(ref.Project, res.Nodes, files), nil
}
