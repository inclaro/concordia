package exceptions

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/fsutil"
)

// ValidateWaiver checks a waiver that the caller built, with the rules that ParseWaivers applies to a register: the
// shape, the scope, the approver, and the options (the maximum duration). The error is a CO-EXC-002 *diag.Error.
func ValidateWaiver(w *Waiver, opts ...ParseOption) error {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	appendToRegister(root, waiverNode(w))

	text, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("render waiver %s: %w", w.ID, err)
	}

	_, err = ParseWaivers(string(text), "", opts...)

	return err
}

// AppendWaiver adds a waiver to the waiver file (an OS path). A missing or
// empty file is created from the register skeleton. The file is read to a YAML
// node tree and written again, so comments stay.
func AppendWaiver(path string, waiver *Waiver) error {
	doc, err := openRegister(path)
	if err != nil {
		return err
	}

	root := documentRoot(doc)
	if root.Kind != yaml.MappingNode {
		return waiverProblem(path, "not an object", "", root.Line)
	}

	appendToRegister(root, waiverNode(waiver))

	data, err := encodeRegister(doc, path)
	if err != nil {
		return err
	}

	if err := fsutil.WriteFileAtomic(filepath.Clean(path), data); err != nil {
		return fmt.Errorf("write waiver file: %w", err)
	}

	return nil
}

// openRegister reads the waiver file to a YAML document. A missing or empty file gives the register skeleton, and a
// file with only comments keeps them above the skeleton.
func openRegister(path string) (*yaml.Node, error) {
	text, err := readRegister(path)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(text) == "" {
		text = registerSkeleton
	}

	doc, err := unmarshalWaiverText(text, path)
	if err != nil {
		return nil, err
	}

	if documentRoot(doc) != nil {
		return doc, nil
	}

	// Only comments: keep them and add the skeleton below.
	return unmarshalWaiverText(text+"\n"+registerSkeleton, path)
}

// encodeRegister writes a YAML document with the indent of the register file. The path is for the error.
func encodeRegister(doc *yaml.Node, path string) ([]byte, error) {
	var out bytes.Buffer

	enc := yaml.NewEncoder(&out)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("write waiver file %s: %w", path, err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("write waiver file %s: %w", path, err)
	}

	return out.Bytes(), nil
}

// readRegister reads the waiver file. A missing file gives the empty text.
func readRegister(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller chooses the waiver file: reading it is the function.
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", waiverProblem(path, "cannot read: "+err.Error(), "", 0)
	}

	return string(data), nil
}

// appendToRegister adds the item to the waiver list of the root mapping. When the
// list is missing or is not a sequence, a new block sequence replaces it.
func appendToRegister(root, item *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if k := root.Content[i]; k.Kind != yaml.ScalarNode || k.Value != waiversKey {
			continue
		}

		list := resolveAlias(root.Content[i+1])
		if list.Kind == yaml.SequenceNode {
			list.Content = append(list.Content, item)
			list.Style &^= yaml.FlowStyle

			return
		}

		root.Content[i+1] = newYAMLSequence(item)

		return
	}

	root.Content = append(root.Content, newYAMLText(waiversKey, false), newYAMLSequence(item))
}

// yamlPair is one key and text value of a YAML mapping that waiverNode writes.
type yamlPair struct {
	key   string
	value string
}

// newYAMLText makes a YAML text node. A plain node has an empty tag, so that the writer prints a date without quotes.
func newYAMLText(value string, plain bool) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: value}
	if plain {
		node.Tag = ""
	}

	return node
}

// newYAMLSequence makes a block sequence node of items.
func newYAMLSequence(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq, Content: items}
}

// newYAMLObject makes a mapping node of the pairs that have a value.
func newYAMLObject(pairs ...yamlPair) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}

	for _, pair := range pairs {
		if pair.value != "" {
			addYAMLPair(out, pair.key, newYAMLText(pair.value, false))
		}
	}

	return out
}

// addYAMLPair adds a key and its value node to a mapping node.
func addYAMLPair(into *yaml.Node, key string, value *yaml.Node) {
	into.Content = append(into.Content, newYAMLText(key, false), value)
}

// addYAMLOptional adds a key and its text to a mapping node, unless the text is empty.
func addYAMLOptional(into *yaml.Node, key, value string) {
	if value != "" {
		addYAMLPair(into, key, newYAMLText(value, false))
	}
}

// waiverNode makes the YAML mapping of a waiver. The key order of a finding waiver is: id, finding, rule, owner,
// reason, expires, issue. The key order of a scoped waiver is: id, rule, scope, owner, reason, expires, issue,
// approver, compensatingEvidence.
func waiverNode(waiver *Waiver) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}

	addYAMLPair(node, idKey, newYAMLText(waiver.ID, false))
	addYAMLOptional(node, findingKey, waiver.Finding)
	addYAMLPair(node, ruleKey, newYAMLText(waiver.Rule, false))
	addScopeNode(node, waiver.Scope)
	addYAMLPair(node, ownerKey, newYAMLText(waiver.Owner, false))
	addYAMLPair(node, reasonKey, newYAMLText(waiver.Reason, false))
	addYAMLPair(node, expiresKey, newYAMLText(waiver.Expires, datePattern.MatchString(waiver.Expires)))
	addYAMLOptional(node, issueKey, waiver.Issue)
	addApproverNode(node, waiver.Approver)
	addEvidenceNode(node, waiver.CompensatingEvidence)

	return node
}

// addScopeNode adds the scope of a scoped waiver.
func addScopeNode(node *yaml.Node, scope *Scope) {
	if scope == nil {
		return
	}

	addYAMLPair(node, scopeKey, newYAMLObject(
		yamlPair{subjectKey, scope.Subject},
		yamlPair{environmentKey, scope.Environment},
		yamlPair{obligationKey, scope.Obligation},
	))
}

// addApproverNode adds the approver of a scoped waiver.
func addApproverNode(node *yaml.Node, approver *Approver) {
	if approver == nil {
		return
	}

	addYAMLPair(node, approverKey, newYAMLObject(
		yamlPair{"identity", approver.Identity},
		yamlPair{"authority", approver.Authority},
	))
}

// addEvidenceNode adds the compensating evidence of a scoped waiver, when there is any.
func addEvidenceNode(node *yaml.Node, evidence []Evidence) {
	if len(evidence) == 0 {
		return
	}

	list := newYAMLSequence()

	for _, e := range evidence {
		list.Content = append(list.Content, newYAMLObject(yamlPair{"path", e.Path}, yamlPair{"sha256", e.SHA256}))
	}

	addYAMLPair(node, evidenceKey, list)
}
