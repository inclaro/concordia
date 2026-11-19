package testutil

import (
	"maps"
	"slices"
)

// schemaEdgeStats is what the schema says about one edge type.
type schemaEdgeStats struct {
	props map[string]struct{}
	count int
}

// schemaEntry is one entry of the schema sequence: a node label or an edge
// type. Exactly one of the two maps is set.
type schemaEntry struct {
	node map[string]any
	edge map[string]any
}

// nodeProperties gives the properties that the schema lists for a node label:
// the base properties, then the sorted property names of the nodes with that label.
func nodeProperties(nodes []FakeNode, label string) []string {
	names := map[string]struct{}{}

	for i := range nodes {
		if nodes[i].Label == label {
			for name := range nodes[i].Props {
				names[name] = struct{}{}
			}
		}
	}

	return append([]string{qualifiedNameProp, filePathProp, "start_line"}, slices.Sorted(maps.Keys(names))...)
}

// schema gives one page of the sequence "node labels, then edge types". The
// properties of each entry are only there with diagnostics "full".
func (g *FakeGraph) schema(limit, offset int, full bool) map[string]any {
	entries := slices.Concat(g.nodeSchemaEntries(full), g.edgeSchemaEntries(full))
	end := offset + limit
	page := entries[min(offset, len(entries)):min(end, len(entries))]
	nodeLabels := []map[string]any{}
	edgeTypes := []map[string]any{}

	for _, e := range page {
		if e.node != nil {
			nodeLabels = append(nodeLabels, e.node)
		} else {
			edgeTypes = append(edgeTypes, e.edge)
		}
	}

	hasMore := offset+len(page) < len(entries)
	res := map[string]any{
		"node_labels": nodeLabels,
		"edge_types":  edgeTypes,
		totalKey:      len(entries),
		"returned":    len(page),
		"has_more":    hasMore,
	}

	if hasMore {
		res["next_offset"] = offset + len(page)
	}

	return res
}

// nodeSchemaEntries gives one entry for each node label, sorted by label.
func (g *FakeGraph) nodeSchemaEntries(full bool) []schemaEntry {
	labels := map[string]int{}
	for i := range g.Nodes {
		labels[g.Nodes[i].Label]++
	}

	entries := make([]schemaEntry, 0, len(labels))

	for _, label := range slices.Sorted(maps.Keys(labels)) {
		node := map[string]any{"label": label, "count": labels[label]}
		if full {
			node["properties"] = nodeProperties(g.Nodes, label)
		}

		entries = append(entries, schemaEntry{node: node})
	}

	return entries
}

// edgeSchemaEntries gives one entry for each edge type, sorted by type.
func (g *FakeGraph) edgeSchemaEntries(full bool) []schemaEntry {
	types := g.edgeTypeStats()
	entries := make([]schemaEntry, 0, len(types))

	for _, name := range slices.Sorted(maps.Keys(types)) {
		stats := types[name]
		edge := map[string]any{"type": name, "count": stats.count}

		if full {
			edge["properties"] = append([]string{"source_id", "target_id"}, slices.Sorted(maps.Keys(stats.props))...)
		}

		entries = append(entries, schemaEntry{edge: edge})
	}

	return entries
}

// edgeTypeStats counts the edges of each type and collects their property names.
func (g *FakeGraph) edgeTypeStats() map[string]*schemaEdgeStats {
	types := map[string]*schemaEdgeStats{}

	for i := range g.Edges {
		edge := &g.Edges[i]

		stats, ok := types[edge.Type]
		if !ok {
			stats = &schemaEdgeStats{props: map[string]struct{}{}}
			types[edge.Type] = stats
		}

		stats.count++

		for name := range edge.Props {
			stats.props[name] = struct{}{}
		}
	}

	return types
}
