package quality

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/diag"
)

// sourceDoc is a scenario file together with its YAML tree, so that an item of the document can be given the line
// it is written on.
type sourceDoc struct {
	node *yaml.Node
	file string
}

// nodeAt finds the node at a JSON Pointer; when the pointer leaves the tree, the deepest node found.
func (s sourceDoc) nodeAt(pointer string) *yaml.Node {
	cur := s.node
	if cur == nil || pointer == "" {
		return cur
	}

	for seg := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		next := childNode(cur, seg)
		if next == nil {
			return cur
		}

		cur = next
	}

	return cur
}

// lineAtPointer is the line of the node at a JSON Pointer, or 0 when the tree is unknown.
func (s sourceDoc) lineAtPointer(pointer string) int {
	return lineOf(s.nodeAt(pointer))
}

// location is the file and line of the node at a JSON Pointer.
func (s sourceDoc) location(pointer string) Location {
	return Location{File: s.file, Line: s.lineAtPointer(pointer)}
}

// invalidAt makes a CO-QUA-001 error at the node at a JSON Pointer.
func (s sourceDoc) invalidAt(detail, pointer string) *diag.Error {
	return invalid(s.file, detail, pointer, s.nodeAt(pointer))
}

// invalid makes a CO-QUA-001 error at a node of the file.
func invalid(file, detail, pointer string, node *yaml.Node) *diag.Error {
	return diag.Fail(diag.QUA001, diag.Detail(detail),
		diag.WithSource(diag.Source{URI: file, Pointer: pointer, Line: lineOf(node)}))
}

func lineOf(node *yaml.Node) int {
	if node == nil {
		return 0
	}

	return node.Line
}

func stringOf(v any) string {
	s, ok := v.(string)
	if ok {
		return s
	}

	return fmt.Sprint(v)
}

func orRoot(pointer string) string {
	if pointer == "" {
		return "the document root"
	}

	return pointer
}

// describe names the scenario or finding that a schema error is inside, so that the message says which one and,
// through the library message, which field.
func describe(doc map[string]any, pointer string) string {
	kind, idx, ok := splitItemPointer(pointer)
	if !ok {
		return ""
	}

	list, _ := doc[kind].([]any)
	if idx < 0 || idx >= len(list) {
		return ""
	}

	item, _ := list[idx].(map[string]any)

	id, _ := item["id"].(string)
	if id == "" {
		id = "#" + strconv.Itoa(idx+1)
	}

	return strings.TrimSuffix(kind, "s") + " " + id + ": "
}

// splitItemPointer splits a pointer into the list it points into (`scenarios` or `findings`) and the index of
// the item; ok is false for any other pointer.
func splitItemPointer(pointer string) (kind string, idx int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	if len(parts) < 2 || !slices.Contains([]string{"scenarios", "findings"}, parts[0]) {
		return "", 0, false
	}

	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, false
	}

	return parts[0], idx, true
}

func childNode(node *yaml.Node, seg string) *yaml.Node {
	node = resolveAlias(node)
	if node == nil {
		return nil
	}

	switch node.Kind {
	case yaml.MappingNode:
		return mappingValue(node, seg)
	case yaml.SequenceNode:
		return sequenceItem(node, seg)
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
		return nil
	}

	return nil
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	return node
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

func sequenceItem(node *yaml.Node, seg string) *yaml.Node {
	idx, err := strconv.Atoi(seg)
	if err != nil || idx < 0 || idx >= len(node.Content) {
		return nil
	}

	return node.Content[idx]
}
