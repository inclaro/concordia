package cli

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
)

// workspaceDoc is the workspace file as a YAML node tree. Edits keep the comments and the other keys.
type workspaceDoc struct {
	// packs is the `policies.packs` sequence. It is created in the tree when the file has none.
	packs *yaml.Node
	path  string
	doc   yaml.Node
}

func isNullNode(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// entryOf gives the value node of a key of a mapping node, or nil.
func entryOf(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if k := mapping.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return mapping.Content[i+1]
		}
	}

	return nil
}

// ensureEntry gives the value node of a key, and creates it (as a node of the
// kind) when it is missing or null. It gives nil when the value has another kind.
func ensureEntry(mapping *yaml.Node, key string, kind yaml.Kind) *yaml.Node {
	tag := "!!map"
	if kind == yaml.SequenceNode {
		tag = "!!seq"
	}

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind != yaml.ScalarNode || k.Value != key {
			continue
		}

		value := mapping.Content[i+1]
		if isNullNode(value) {
			mapping.Content[i+1] = &yaml.Node{Kind: kind, Tag: tag}

			return mapping.Content[i+1]
		}

		if value.Kind != kind {
			return nil
		}

		return value
	}

	created := &yaml.Node{Kind: kind, Tag: tag}
	mapping.Content = append(mapping.Content, scalarNode(key), created)

	return created
}

// isMappingDocument says whether a YAML document holds one mapping.
func isMappingDocument(doc *yaml.Node) bool {
	return doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode
}

func openWorkspaceDoc(file string) (*workspaceDoc, error) {
	data, err := os.ReadFile(file) //nolint:gosec // the path is the workspace file that the user named.
	if err != nil {
		return nil, diag.Fail(diag.WRK001, diag.Params{keyPath: file})
	}

	tree := &workspaceDoc{path: file}
	if err := yaml.Unmarshal(data, &tree.doc); err != nil {
		return nil, diag.Fail(diag.WRK002, diag.Detail("YAML syntax error: "+err.Error()), diag.WithSource(diag.Source{URI: file}))
	}

	if !isMappingDocument(&tree.doc) {
		return nil, diag.Fail(diag.WRK002, diag.Detail("the document must be a mapping"), diag.WithSource(diag.Source{URI: file}))
	}

	policies := ensureEntry(tree.doc.Content[0], "policies", yaml.MappingNode)
	if policies != nil {
		tree.packs = ensureEntry(policies, "packs", yaml.SequenceNode)
	}

	if tree.packs == nil {
		return nil, diag.Fail(diag.WRK002, diag.Detail("policies and policies.packs must be a mapping and a list"), diag.WithSource(diag.Source{URI: file}))
	}

	return tree, nil
}

// find gives the `policies.packs` entry with this `use`, or nil.
func (w *workspaceDoc) find(use string) *yaml.Node {
	for _, item := range w.packs.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}

		if value := entryOf(item, "use"); value != nil && value.Value == use {
			return item
		}
	}

	return nil
}

// set puts the entry of a pack in the tree: it adds a new entry, or sets the
// digest of an existing one (its overrides stay). It gives the previous digest.
func (w *workspaceDoc) set(use, sha string) (previous string) {
	item := w.find(use)
	if item == nil {
		item = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		item.Content = append(item.Content, scalarNode("use"), scalarNode(use))
		w.packs.Content = append(w.packs.Content, item)

		if len(w.packs.Content) == 1 {
			w.packs.Style = 0
		}
	}

	if sha == "" {
		return ""
	}

	if value := entryOf(item, "sha256"); value != nil {
		previous = value.Value
		value.Kind, value.Tag, value.Value, value.Style = yaml.ScalarNode, "!!str", sha, 0

		return previous
	}

	item.Content = append(item.Content, scalarNode("sha256"), scalarNode(sha))

	return ""
}

func (w *workspaceDoc) save() error {
	text, err := encodeYAML(&w.doc)
	if err != nil {
		return err
	}

	if err := fsutil.WriteFileAtomic(w.path, []byte(text)); err != nil {
		return fmt.Errorf("write %s: %w", w.path, err)
	}

	return nil
}
