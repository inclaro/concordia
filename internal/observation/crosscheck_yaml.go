package observation

import (
	"slices"

	"go.yaml.in/yaml/v3"
)

// ----- pod specs -----

// podSpec gives the pod spec of a workload document.
func podSpec(d *manifestDoc) *yaml.Node {
	spec := fieldVal(d.root, "spec")

	switch d.kind {
	case "Pod":
		return spec
	case "CronJob":
		return fieldVal(fieldVal(fieldVal(fieldVal(spec, "jobTemplate"), "spec"), "template"), "spec")
	}

	if !slices.Contains(workloadKinds, d.kind) {
		return nil
	}

	return fieldVal(fieldVal(spec, "template"), "spec")
}

func containers(pod *yaml.Node) []*yaml.Node {
	return append(items(fieldVal(pod, "containers")), items(fieldVal(pod, "initContainers"))...)
}

// ----- YAML access -----

// field gives the key node and the value node of a key of a mapping. Both are nil when the key is missing.
func field(n *yaml.Node, key string) (keyNode, value *yaml.Node) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], resolve(n.Content[i+1])
		}
	}

	return nil, nil
}

// fieldVal gives the value node of a key.
func fieldVal(n *yaml.Node, key string) *yaml.Node {
	_, v := field(n, key)

	return v
}

// hasField says whether a key is present with a non-empty scalar value, without reading the value.
func hasField(n *yaml.Node, key string) bool {
	v := fieldVal(n, key)

	return v != nil && v.Kind == yaml.ScalarNode && v.Value != ""
}

// items gives the elements of a sequence.
func items(n *yaml.Node) []*yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}

	out := make([]*yaml.Node, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, resolve(c))
	}

	return out
}

// name gives the scalar text of a node that the allow-list names (a kind, a name, a namespace). It gives "" for any other node.
func name(n *yaml.Node) string {
	n = resolve(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}

	return n.Value
}

func isString(n *yaml.Node) bool {
	n = resolve(n)

	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value != ""
}

func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}

	return n
}
