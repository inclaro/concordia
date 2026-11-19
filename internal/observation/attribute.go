package observation

import (
	"maps"
	"slices"
)

// AttributionSummary counts what AttributeEnvironments did.
type AttributionSummary struct {
	// Attributed is the number of facts that got a declared environment.
	Attributed int
	// Unresolved is the number of facts that no declared environment owns.
	Unresolved int
	// FannedOut is the number of extra facts and nodes made for a fact that
	// matches several environments.
	FannedOut int
}

// envSuffixSeparator joins an observed ID and an environment id for the
// instance of a node or fact in an environment other than the first.
const envSuffixSeparator = "@"

// scopedNode is a node that has a file scope.
type scopedNode struct {
	envs []string
}

// AttributeEnvironments attributes infrastructure facts to declared
// environments. attribute gives the ids of the environments whose sources
// match a workspace-relative path in declaration order (workspace.Attribute).
//
// Facts of infrastructure kinds, and unsupported or ambiguous facts that touch
// an infrastructure node, are attributed. Nodes of those facts are attributed
// by their file path, except the unscoped nodes (IsUnscopedNode): `Image`,
// `Route`, project-wide `EnvVar` and external `Chart` nodes get no
// environment, and a fact gets its environment through its scoped endpoints.
//
//   - An endpoint attributed to no environment does not give the fact one; a
//     fact whose scoped endpoints are all unattributed is `unresolved`.
//   - A fact with several scoped endpoints belongs to the environments that
//     all of them share. No shared environment gives `unresolved`.
//   - A fact with no scoped endpoint is attributed by the path of its source span.
//   - A node or fact that matches several environments (a shared base file)
//     becomes one node or fact for each environment: the first keeps its
//     observed ID or edge ID and the others get `@<environment>` appended.
//     Each fact refers to the node instances of its own environment, so the
//     identity includes the environment.
//
// A fact that no environment owns has the environment EnvironmentUnresolved,
// never a default. Nodes of other facts are not changed. The result is in
// res (nodes added, facts added and set, edges re-sorted when any was added).
func AttributeEnvironments(res *EvidenceQueryResult, attribute func(path string) []string) AttributionSummary {
	var sum AttributionSummary

	nodes := scopedNodes(res, attribute)

	res.Edges, sum = attributeEdges(res.Edges, res, nodes, attribute, sum)
	res.Unsupported, sum = attributeEdges(res.Unsupported, res, nodes, attribute, sum)

	for nodeID, scoped := range nodes {
		node := res.Nodes[nodeID]

		if len(scoped.envs) == 0 {
			node.Environment = EnvironmentUnresolved
			res.Nodes[nodeID] = node

			continue
		}

		node.Environment = scoped.envs[0]
		res.Nodes[nodeID] = node

		for _, env := range scoped.envs[1:] {
			clone := node
			clone.ObservedID = nodeID + envSuffixSeparator + env
			clone.Environment = env
			clone.Properties = maps.Clone(node.Properties)
			res.Nodes[clone.ObservedID] = clone
			sum.FannedOut++
		}
	}

	if sum.FannedOut > 0 {
		SortEdges(res.Edges, res.Nodes)
	}

	return sum
}

// infrastructureLabel says whether a node label belongs to infrastructure evidence.
func infrastructureLabel(label string) bool {
	switch label {
	case labelResource, labelChart, LabelImage, labelRoute, labelEnvVar:
		return true
	default:
		return false
	}
}

// attributable says whether an edge is infrastructure evidence.
func attributable(e *EvidenceEdge, nodes map[string]ObservedNode) bool {
	if IsInfrastructureKind(e.Kind) {
		return true
	}

	if e.Kind != KindUnsupported && e.Kind != KindAmbiguous {
		return false
	}

	for _, id := range []string{e.From, e.To} {
		if n, ok := nodes[id]; ok && infrastructureLabel(n.Label) {
			return true
		}
	}

	return false
}

// scopedNodes finds the scoped nodes and attributes them by path: the
// endpoints of attributable edges, and every declared `Resource` and local
// `Chart` node, so that a resource that no fact touches still has an identity
// with its environment (a competing declaration is visible only then).
func scopedNodes(res *EvidenceQueryResult, attribute func(path string) []string) map[string]*scopedNode {
	out := map[string]*scopedNode{}
	add := func(nodeID string) { addScopedNode(out, res, nodeID, attribute) }

	for _, edge := range res.Edges {
		addAttributableEndpoints(edge, res, add)
	}

	for _, edge := range res.Unsupported {
		addAttributableEndpoints(edge, res, add)
	}

	for nodeID := range res.Nodes {
		if label := res.Nodes[nodeID].Label; label == labelResource || label == labelChart {
			add(nodeID)
		}
	}

	return out
}

// addScopedNode records a node, with its attributed environments, once.
func addScopedNode(out map[string]*scopedNode, res *EvidenceQueryResult, nodeID string, attribute func(path string) []string) {
	node, ok := res.Nodes[nodeID]
	if !ok || IsUnscopedNode(node.Label, node.QualifiedName) {
		return
	}

	if _, done := out[nodeID]; done {
		return
	}

	scoped := &scopedNode{}
	if node.FilePath != "" {
		scoped.envs = attribute(node.FilePath)
	}

	out[nodeID] = scoped
}

// addAttributableEndpoints adds both endpoints of an infrastructure edge.
func addAttributableEndpoints(edge *EvidenceEdge, res *EvidenceQueryResult, add func(nodeID string)) {
	if !attributable(edge, res.Nodes) {
		return
	}

	add(edge.From)
	add(edge.To)
}

// instanceID gives the observed ID of the instance of a node in an environment.
func instanceID(id string, sn *scopedNode, env string) string {
	if !hasEnvironmentInstance(sn, env) {
		return id
	}

	return id + envSuffixSeparator + env
}

// hasEnvironmentInstance says whether a node has its own instance in env: it
// is declared in several environments and env is not the primary one.
func hasEnvironmentInstance(sn *scopedNode, env string) bool {
	if sn == nil || len(sn.envs) < 2 {
		return false
	}

	return env != sn.envs[0] && slices.Contains(sn.envs, env)
}

// edgeEnvironments gives the environments of an edge. An empty result means unresolved.
func edgeEnvironments(edge *EvidenceEdge, nodes map[string]*scopedNode, attribute func(path string) []string) []string {
	var shared [][]string

	scoped := false

	for _, id := range []string{edge.From, edge.To} {
		entry, ok := nodes[id]
		if !ok {
			continue
		}

		scoped = true

		if len(entry.envs) > 0 {
			shared = append(shared, entry.envs)
		}
	}

	switch {
	case len(shared) > 0:
		return commonEnvironments(shared)
	case !scoped && edge.SourceSpan != nil && edge.SourceSpan.Path != "":
		return attribute(edge.SourceSpan.Path)
	default:
		return nil
	}
}

// commonEnvironments keeps the environments of the first list that every other list has.
func commonEnvironments(shared [][]string) []string {
	return slices.DeleteFunc(slices.Clone(shared[0]), func(env string) bool {
		for _, other := range shared[1:] {
			if !slices.Contains(other, env) {
				return true
			}
		}

		return false
	})
}

func attributeEdges(edges []*EvidenceEdge, res *EvidenceQueryResult, nodes map[string]*scopedNode, attribute func(path string) []string, sum AttributionSummary) ([]*EvidenceEdge, AttributionSummary) {
	out := make([]*EvidenceEdge, 0, len(edges))

	for _, edge := range edges {
		out = append(out, edge)

		if !attributable(edge, res.Nodes) {
			continue
		}

		envs := edgeEnvironments(edge, nodes, attribute)
		if len(envs) == 0 {
			edge.Environment = EnvironmentUnresolved
			sum.Unresolved++

			continue
		}

		sum.Attributed++

		clones := assignEnvironments(edge, nodes, envs)
		out = append(out, clones...)
		sum.FannedOut += len(clones)
		sum.Attributed += len(clones)
	}

	return out, sum
}

// assignEnvironments pins an edge to the first environment and returns one
// clone for each further environment.
func assignEnvironments(edge *EvidenceEdge, nodes map[string]*scopedNode, envs []string) []*EvidenceEdge {
	fromID, toID := edge.From, edge.To
	original := *edge

	edge.Environment = envs[0]
	edge.From = instanceID(fromID, nodes[fromID], envs[0])
	edge.To = instanceID(toID, nodes[toID], envs[0])

	clones := make([]*EvidenceEdge, 0, len(envs)-1)

	for _, env := range envs[1:] {
		clone := original
		clone.ID = original.ID + envSuffixSeparator + env
		clone.From = instanceID(fromID, nodes[fromID], env)
		clone.To = instanceID(toID, nodes[toID], env)
		clone.Environment = env
		clone.Properties = maps.Clone(original.Properties)
		clone.Facets = maps.Clone(original.Facets)

		if original.SourceSpan != nil {
			span := *original.SourceSpan
			clone.SourceSpan = &span
		}

		clones = append(clones, &clone)
	}

	return clones
}
