package testutil

import (
	"maps"
	"path"
	"strconv"
	"strings"
)

// FileNode makes a `File` node for a path.
func FileNode(id, filePath string) FakeNode {
	withoutExt := strings.TrimSuffix(filePath, path.Ext(filePath))

	return FakeNode{
		ID:    id,
		Label: fileLabel,
		QN:    strings.ReplaceAll(withoutExt, "/", ".") + ".__file__",
		Path:  filePath,
	}
}

// Discriminators are the edge properties that CBM 0.12.1 uses to tell the
// meaning of an infrastructure edge: `via`, `ref`, `kind`, `strategy`,
// `syntax` and `tier`. Empty values are not set.
type Discriminators struct {
	Via      string
	Ref      string
	Kind     string
	Strategy string
	Syntax   string
	Tier     string
}

// Props gives the discriminators as edge properties. Empty values are left out.
func (d Discriminators) Props() map[string]string { //nolint:gocritic // a literal of six strings: a pointer receiver would not work on a literal.
	props := map[string]string{}

	for name, value := range map[string]string{
		"via": d.Via, "ref": d.Ref, "kind": d.Kind, "strategy": d.Strategy, "syntax": d.Syntax, "tier": d.Tier,
	} {
		if value != "" {
			props[name] = value
		}
	}

	return props
}

// InfraNode makes a node of any label with a file path and properties, for
// example a `Resource`, `Image`, `Module`, `Chart` or `EnvVar`. The qualified
// name has no project prefix; a pseudo node such as `__image__<repo>` has
// no file path.
func InfraNode(id, label, qn, filePath string, props map[string]string) FakeNode {
	return FakeNode{ID: id, Label: label, QN: qn, Path: filePath, Props: maps.Clone(props)}
}

// ADRNode makes an `ADR` node of a decision file (CBM 0.12.1 observations, O12). The qualified name is
// `<file path with dots, without extension>.__adr__` and the start line is 1. The properties are those that the
// provider gives: `adr_id`, `title`, `status`, `status_text`, `date`, `deciders`, `superseded_by`.
func ADRNode(id, filePath string, props map[string]string) FakeNode {
	withoutExt := strings.TrimSuffix(filePath, path.Ext(filePath))

	return FakeNode{
		ID:    id,
		Label: "ADR",
		QN:    strings.ReplaceAll(withoutExt, "/", ".") + ".__adr__",
		Path:  filePath,
		Line:  1,
		Props: maps.Clone(props),
	}
}

// Namespace markers of a Resource `obj_key` (CBM 0.12.1 observations, O2).
const (
	// ClusterScoped is the namespace part of the obj_key of a cluster-scoped kind.
	ClusterScoped = "_"
	// NamespaceUnknown is the namespace part of the obj_key of a namespaced kind whose manifest has no namespace.
	NamespaceUnknown = "?"
)

// ObjKey gives `<group>/<Kind>/<namespace>/<name>`, the `obj_key` of a Resource.
// Use "core" for the core API group, ClusterScoped for a cluster-scoped kind
// and NamespaceUnknown for a manifest without namespace.
func ObjKey(group, kind, namespace, name string) string {
	return group + "/" + kind + "/" + namespace + "/" + name
}

// ResourceNode makes a `Resource` node of a manifest document, with the
// properties `kind`, `group`, `namespace`, `obj_key` and `source: k8s`. Its
// qualified name is `<file path with dots>::<obj_key>`, so the same obj_key in
// two files gives two nodes.
func ResourceNode(id, filePath, group, kind, namespace, name string) FakeNode {
	objKey := ObjKey(group, kind, namespace, name)

	return FakeNode{
		ID:    id,
		Label: "Resource",
		QN:    strings.ReplaceAll(filePath, "/", ".") + "::" + objKey,
		Path:  filePath,
		Props: map[string]string{
			"source": "k8s", "group": group, "kind": kind, "namespace": namespace, "obj_key": objKey, "name": name,
		},
	}
}

// ImageNode makes an `Image` node: one for each repository, with no file path,
// no tag and no digest (O3).
func ImageNode(id, repository string) FakeNode {
	registry := "unknown"
	if first, _, found := strings.Cut(repository, "/"); found && strings.ContainsAny(first, ".:") {
		registry = first
	}

	return FakeNode{
		ID:    id,
		Label: "Image",
		QN:    "__image__" + repository,
		Props: map[string]string{"repository": repository, "registry": registry},
	}
}

// DiagramNode makes a `Diagram` node of a diagram block. The qualified name is
// `<file path without extension, "/" as ".">.<section>#diagram-<ordinal>`, as
// CBM 0.12.1 gives it (O12); kind and format are `diagram_kind` and `format`.
func DiagramNode(id, filePath, section string, ordinal int, kind, format string) FakeNode {
	stem := strings.ReplaceAll(strings.TrimSuffix(filePath, path.Ext(filePath)), "/", ".")

	return FakeNode{
		ID:    id,
		Label: "Diagram",
		QN:    stem + "." + section + "#diagram-" + strconv.Itoa(ordinal),
		Path:  filePath,
		Props: map[string]string{"diagram_kind": kind, "format": format},
	}
}

// DiagramElementNode makes a `DiagramElement` node of a diagram. Its
// qualified name is `<diagram qualified name>.<element_id>`; elementID is the
// node ID that the author wrote in the diagram source. Its `name` is the node
// ID, as CBM 0.12.1 gives it for a node without a label text (O12).
func DiagramElementNode(id string, diagram *FakeNode, elementID, elementKind string) FakeNode {
	return DiagramElementLabeled(id, diagram, elementID, elementKind, elementID)
}

// DiagramElementLabeled is DiagramElementNode for a node with a label text:
// `api[Orders API]` has the element_id `api` and the name `Orders API`.
func DiagramElementLabeled(id string, diagram *FakeNode, elementID, elementKind, label string) FakeNode {
	return FakeNode{
		ID:    id,
		Label: "DiagramElement",
		QN:    diagram.QN + "." + elementID,
		Path:  diagram.Path,
		Props: map[string]string{"element_id": elementID, "element_kind": elementKind, fakeNameProp: label},
	}
}

// fakeNameProp is the key in FakeNode.Props of the node property `name`. CBM
// keeps it out of `properties(n)`; the fake gives it in the column `n.name`.
const fakeNameProp = "name"

// DiagramArrowEdge makes a `DIAGRAM_ARROW` edge from the element at the tail
// to the element at the head. Empty label, style and line are not set.
func DiagramArrowEdge(tail, head *FakeNode, label, style string, line int) FakeEdge {
	props := map[string]string{}

	if label != "" {
		props["label"] = label
	}

	if style != "" {
		props["style"] = style
	}

	if line > 0 {
		props["line"] = strconv.Itoa(line)
	}

	return FakeEdge{From: tail, To: head, Type: "DIAGRAM_ARROW", Props: props}
}

// NewEdge makes an edge with properties.
func NewEdge(from, to *FakeNode, edgeType string, props map[string]string) FakeEdge {
	return FakeEdge{From: from, To: to, Type: edgeType, Props: maps.Clone(props)}
}

// InfraEdge makes an edge with discriminators and further properties
// (`container`, `image`, `line`, ...). A property in extra wins over a
// discriminator of the same name.
func InfraEdge(from, to *FakeNode, edgeType string, discriminators Discriminators, extra map[string]string) FakeEdge { //nolint:gocritic // a literal of six strings: a pointer would only add noise at the call sites.
	props := discriminators.Props()
	maps.Copy(props, extra)

	return FakeEdge{From: from, To: to, Type: edgeType, Props: props}
}
