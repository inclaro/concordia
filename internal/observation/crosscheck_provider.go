package observation

import (
	"cmp"
	"slices"
)

// providerResource is a `Resource` node of the provider evidence.
type providerResource struct {
	kind      string
	name      string
	namespace string
	env       string
	ids       []string
}

// providerIndex is the provider evidence in the form that the cross-check queries.
type providerIndex struct {
	byFile map[string][]providerResource
	// usesImage gives, by node id, the container names that have a `uses-image` edge.
	usesImage map[string]map[string]bool
	// selecting is the set of node ids that have a selection edge.
	selecting map[string]bool
	all       []providerResource
}

func newProviderIndex(evidence *EvidenceQueryResult) *providerIndex {
	index := &providerIndex{
		byFile: map[string][]providerResource{}, usesImage: map[string]map[string]bool{}, selecting: map[string]bool{},
	}
	if evidence == nil {
		return index
	}

	for nodeID := range evidence.Nodes {
		node := evidence.Nodes[nodeID]
		index.addNode(nodeID, &node)
	}

	for _, e := range evidence.Edges {
		index.addEdge(e)
	}

	return index
}

// addNode indexes a provider node that has a parseable object key.
func (p *providerIndex) addNode(nodeID string, node *ObservedNode) {
	key, ok := ObjKeyOf(node)
	if !ok {
		return
	}

	parsed, err := ParseObjKey(key)
	if err != nil {
		return
	}

	r := providerResource{
		ids: []string{nodeID, node.ObservedID}, kind: parsed.Kind, name: parsed.Name, namespace: parsed.Namespace,
		env: concreteEnv(node.Environment),
	}
	p.all = append(p.all, r)
	p.byFile[node.FilePath] = append(p.byFile[node.FilePath], r)
}

func (p *providerIndex) addEdge(edge *EvidenceEdge) {
	if edge == nil {
		return
	}

	if edge.Kind == KindUsesImage {
		container := cmp.Or(edge.Properties["container"], edge.Occurrence)
		if p.usesImage[edge.From] == nil {
			p.usesImage[edge.From] = map[string]bool{}
		}

		p.usesImage[edge.From][container] = true
	}

	if isSelectionEdge(edge) {
		p.selecting[edge.From] = true
	}
}

// isSelectionEdge says whether the provider gives the edge for a selector.
func isSelectionEdge(edge *EvidenceEdge) bool {
	ref := edge.Properties["ref"]

	return ref == "podSelector" || ref == "selector" || edge.Properties["kind"] == "selector"
}

func concreteEnv(env string) string {
	if env == "unresolved" {
		return ""
	}

	return env
}

// resources gives the provider nodes of one file for a kind and name in an environment.
func (p *providerIndex) resources(file, kind, nm, env string) []providerResource {
	var out []providerResource

	for _, r := range p.byFile[file] {
		if r.isNamed(kind, nm) && !environmentsDiffer(r.env, env) {
			out = append(out, r)
		}
	}

	return out
}

func (r *providerResource) isNamed(kind, nm string) bool { return r.kind == kind && r.name == nm }

// environmentsDiffer says whether two environments are both known and not the same.
func environmentsDiffer(a, b string) bool {
	return a != "" && b != "" && a != b
}

// anyID says whether any node id of the nodes satisfies the predicate.
func anyID(nodes []providerResource, pred func(id string) bool) bool {
	for i := range nodes {
		if slices.ContainsFunc(nodes[i].ids, pred) {
			return true
		}
	}

	return false
}

// isSelected says whether the provider gives a selection edge for any of the nodes.
func (p *providerIndex) isSelected(nodes []providerResource) bool {
	return anyID(nodes, func(id string) bool { return p.selecting[id] })
}

// hasOccurrence says whether any of the nodes has a `uses-image` edge for the container.
func (p *providerIndex) hasOccurrence(nodes []providerResource, container string) bool {
	return anyID(nodes, func(id string) bool { return p.usesImage[id][container] })
}
