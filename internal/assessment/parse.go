package assessment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/yamlparse"
)

// source is one parsed document before its kind is checked.
type source struct {
	doc  map[string]any
	node *yaml.Node
	file string
}

// invalid makes a CO-ASM-001 error at a node of the file. A nil node gives the line 0.
func invalid(file, detail, pointer string, node *yaml.Node) *diag.Error {
	line := 0
	if node != nil {
		line = node.Line
	}

	return diag.Fail(diag.ASM001, diag.Detail(detail),
		diag.WithSource(diag.Source{URI: file, Pointer: pointer, Line: line}))
}

// read parses the text as one YAML mapping with its node tree.
func read(file string, text []byte) (*source, error) {
	tree, problems := yamlparse.Parse(text)
	if len(problems) > 0 {
		return nil, diag.Fail(diag.ASM001, diag.Detail("cannot parse: "+problems[0].Message),
			diag.WithSource(diag.Source{URI: file, Line: problems[0].Line}))
	}

	doc, ok := normalizeTimes(tree).(map[string]any)
	if !ok {
		return nil, invalid(file, "the document must be a mapping", "", nil)
	}

	node, _ := yamlparse.ParseNode(text)

	return &source{file: file, doc: doc, node: node}, nil
}

// normalizeTimes changes the timestamps that the YAML decoder resolves (an unquoted `2026-12-31` or
// `2026-12-31T10:00:00Z`) into RFC 3339 strings, so that a document means the same whether or not the author
// quoted the value, and the digests are stable.
func normalizeTimes(node any) any {
	switch val := node.(type) {
	case map[string]any:
		for key, item := range val {
			val[key] = normalizeTimes(item)
		}

		return val
	case []any:
		for i, item := range val {
			val[i] = normalizeTimes(item)
		}

		return val
	case time.Time:
		return val.Format(time.RFC3339Nano)
	default:
		return node
	}
}

// kind gives the kind of the document, or "".
func (s *source) kind() string {
	k, _ := s.doc["kind"].(string)

	return k
}

// validate checks the kind and then the document against the closed schema of its kind. An empty want accepts
// any known kind.
func (s *source) validate(want string) error {
	kind := s.kind()

	switch {
	case kind == "":
		return invalid(s.file, "missing kind: expected one of "+strings.Join(Kinds, ", "), "/kind", s.node)
	case want != "" && kind != want:
		return invalid(s.file, fmt.Sprintf("expected kind %s, found %s", want, kind), "/kind", nodeAt(s.node, "/kind"))
	}

	schema := schemaOf(kind)
	if schema == nil {
		return invalid(s.file, fmt.Sprintf("unknown kind %q: expected one of %s", kind, strings.Join(Kinds, ", ")),
			"/kind", nodeAt(s.node, "/kind"))
	}

	if errs := schema.Validate(s.doc); len(errs) > 0 {
		first := errs[0]

		return invalid(s.file, first.Message+" at "+orRoot(first.InstancePath), first.InstancePath, nodeAt(s.node, first.InstancePath))
	}

	return nil
}

// decode converts the schema-valid document into its Go type.
func (s *source) decode(into any) error {
	data, err := json.Marshal(s.doc)
	if err != nil {
		return invalid(s.file, "cannot convert: "+err.Error(), "", s.node)
	}

	if err := json.Unmarshal(data, into); err != nil {
		return invalid(s.file, "cannot convert: "+err.Error(), "", s.node)
	}

	return nil
}

// location gives the location of the document root.
func (s *source) location() Location {
	line := 0
	if s.node != nil {
		line = s.node.Line
	}

	return Location{File: s.file, Line: line}
}

// at gives a diagnostic location for a pointer inside the document.
func (s *source) at(pointer string) *yaml.Node { return nodeAt(s.node, pointer) }

func orRoot(pointer string) string {
	if pointer == "" {
		return "the document root"
	}

	return pointer
}

// nodeAt finds the node at a JSON Pointer; when the pointer leaves the tree, the deepest node found.
func nodeAt(root *yaml.Node, pointer string) *yaml.Node {
	cur := root
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

// childNode gives the child of a mapping (by key) or sequence (by index) node, or nil.
func childNode(node *yaml.Node, seg string) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	if node == nil {
		return nil
	}

	if node.Kind == yaml.MappingNode {
		return mappingChild(node, seg)
	}

	if node.Kind == yaml.SequenceNode {
		return sequenceChild(node, seg)
	}

	return nil
}

// mappingChild gives the value of the key seg in a mapping node, or nil.
func mappingChild(node *yaml.Node, seg string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == seg {
			return node.Content[i+1]
		}
	}

	return nil
}

// sequenceChild gives the item at the index seg in a sequence node, or nil.
func sequenceChild(node *yaml.Node, seg string) *yaml.Node {
	idx, err := strconv.Atoi(seg)
	if err != nil {
		return nil
	}

	if idx < 0 || idx >= len(node.Content) {
		return nil
	}

	return node.Content[idx]
}

// ParseAssessment parses and validates one assessment document (schema, authored state, duplicate evidence
// ids). The version digest is set. References to assumptions are checked by Load, which sees all documents.
func ParseAssessment(file string, text []byte) (*Assessment, error) {
	src, err := read(file, text)
	if err != nil {
		return nil, err
	}

	return buildAssessment(src)
}

func buildAssessment(src *source) (*Assessment, error) {
	if err := src.validate(KindAssessment); err != nil {
		return nil, err
	}

	var result Assessment
	if err := src.decode(&result); err != nil {
		return nil, err
	}

	if result.State == "" {
		result.State = StateDraft
	}

	if !result.State.Authorable() {
		return nil, authoredState(src, &result)
	}

	if err := uniqueEvidence(src, result.Evidence, "/evidence"); err != nil {
		return nil, err
	}

	if err := checkMovedDispositions(src, result.Dispositions); err != nil {
		return nil, err
	}

	result.Location = src.location()

	var err error

	result.Digest, err = VersionDigest(src.doc)
	if err != nil {
		return nil, invalid(src.file, err.Error(), "", src.node)
	}

	return &result, nil
}

// checkMovedDispositions fails with CO-ASM-001 when a `moved` disposition names its own identity.
func checkMovedDispositions(src *source, dispositions []Disposition) error {
	for i, disp := range dispositions {
		if disp.Disposition == DispositionMoved && disp.MovedTo == disp.Identity {
			pointer := "/dispositions/" + strconv.Itoa(i) + "/movedTo"

			return invalid(src.file, "a disposition `moved` must name another identity", pointer, src.at(pointer))
		}
	}

	return nil
}

// authoredState makes the CO-ASM-003 error for a document whose authored state is not draft or ready-for-review.
func authoredState(src *source, a *Assessment) *diag.Error {
	return diag.Fail(diag.ASM003, diag.Params{"detail": fmt.Sprintf(
		"assessment %s authors the state %q; a document authors only draft or ready-for-review, and approved, "+
			"rejected, and revoked come from decision records, review-required and expired from currency evaluation",
		a.Key(), a.State)},
		diag.WithSource(diag.Source{URI: src.file, Pointer: "/state", Line: lineOf(src.at("/state"))}))
}

func lineOf(node *yaml.Node) int {
	if node == nil {
		return 0
	}

	return node.Line
}

// uniqueEvidence fails with CO-ASM-001 when two evidence items have the same id.
func uniqueEvidence(src *source, list []Evidence, pointer string) error {
	seen := map[string]bool{}

	for i, e := range list {
		if seen[e.ID] {
			at := pointer + "/" + strconv.Itoa(i) + "/id"

			return invalid(src.file, fmt.Sprintf("evidence id %q is declared twice", e.ID), at, src.at(at))
		}

		seen[e.ID] = true
	}

	return nil
}

// ParseAssumption parses and validates one assumption document.
func ParseAssumption(file string, text []byte) (*Assumption, error) {
	src, err := read(file, text)
	if err != nil {
		return nil, err
	}

	return buildAssumption(src)
}

func buildAssumption(src *source) (*Assumption, error) {
	// The schema also requires one of the two, but its message for an anyOf is not helpful.
	if src.kind() == KindAssumption {
		_, hasSelectors := src.doc["selectors"]
		_, hasEvidence := src.doc["evidence"]

		if !hasSelectors && !hasEvidence {
			return nil, invalid(src.file, fmt.Sprintf("assumption %v declares neither selectors nor evidence", src.doc["id"]),
				"", src.node)
		}
	}

	if err := src.validate(KindAssumption); err != nil {
		return nil, err
	}

	var result Assumption
	if err := src.decode(&result); err != nil {
		return nil, err
	}

	if err := uniqueEvidence(src, result.Evidence, "/evidence"); err != nil {
		return nil, err
	}

	result.Location = src.location()

	var err error

	result.Digest, err = RecordDigest(src.doc)
	if err != nil {
		return nil, invalid(src.file, err.Error(), "", src.node)
	}

	return &result, nil
}

// VersionDigest gives the version digest of an assessment document: "sha256:<hex>" of the canonical JSON of the
// parsed document without its authored state. Comments, formatting, and key order do not change it. The
// document is the parsed YAML mapping (see ParseAssessment).
func VersionDigest(doc map[string]any) (string, error) {
	stripped := make(map[string]any, len(doc))

	for key, value := range doc {
		if key != "state" {
			stripped[key] = value
		}
	}

	return RecordDigest(stripped)
}

// RecordDigest gives "sha256:<hex>" of the canonical JSON of a parsed document. It is the digest of an
// assumption, an approval, and a revocation, and it does not depend on key order or formatting.
func RecordDigest(doc map[string]any) (string, error) {
	data, err := canonjson.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("assessment: canonical form: %w", err)
	}

	return digest.OfBytes(data), nil
}

// canonicalOf gives the canonical JSON of a parsed document.
func canonicalOf(doc map[string]any) ([]byte, error) {
	data, err := canonjson.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("assessment: canonical form: %w", err)
	}

	return bytes.Clone(data), nil
}
