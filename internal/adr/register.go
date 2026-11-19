package adr

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/yamlparse"
)

//go:embed schemas/decision-register.schema.json
var schemaFS embed.FS

// RegisterSchemaID is the ID of the DecisionRegister schema.
const RegisterSchemaID = "https://concordia.io/schemas/v1alpha1/decision-register.schema.json"

var registerSchema = sync.OnceValue(func() *jsonschema.Schema {
	doc, err := schemaFS.ReadFile("schemas/decision-register.schema.json")
	if err != nil {
		panic("adr: embedded schema missing: " + err.Error())
	}

	schema, err := jsonschema.Compile(RegisterSchemaID, doc)
	if err != nil {
		panic("adr: embedded schema does not compile: " + err.Error())
	}

	return schema
})

// EntryKey returns the unique key of a register entry: canonical ID plus project.
func EntryKey(e *Entry) string { return e.ID + "\x00" + e.Project }

// globMeta are the characters that make a `files` item a glob.
const globMeta = "*?[{"

// LoadRegister reads the decision register files. Each item of files is a workspace-relative "/" path or glob
// (a path without glob characters must exist). The files are read once, validated against the embedded schema,
// and merged; entries are sorted by canonical ID, then project.
//
// Errors are *diag.Error: CO-ADR-001 (not valid: unreadable, syntax, schema, `released` on an active entry) and
// CO-ADR-002 (the same canonical ID and project twice, in one file or across files; both locations are named).
func LoadRegister(root string, files []string) (*Register, error) {
	paths, err := expandRegisterFiles(root, files)
	if err != nil {
		return nil, err
	}

	reg := &Register{Files: []RegisterFile{}, Entries: []Entry{}}
	seen := map[string]*Entry{}

	for _, path := range paths {
		file, entries, loadErr := loadRegisterFile(root, path)
		if loadErr != nil {
			return nil, loadErr
		}

		reg.Files = append(reg.Files, file)

		if dupErr := reg.addEntries(entries, seen); dupErr != nil {
			return nil, dupErr
		}
	}

	slices.SortStableFunc(reg.Entries, func(a, b Entry) int {
		if c := canonjson.CompareUTF16(a.ID, b.ID); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Project, b.Project)
	})

	return reg, nil
}

// addEntries appends the entries of one file. It fails with CO-ADR-002 for an entry whose key is already in seen,
// and names both locations.
func (r *Register) addEntries(entries []Entry, seen map[string]*Entry) error {
	for i := range entries {
		key := EntryKey(&entries[i])
		if prev, dup := seen[key]; dup {
			return diag.Fail(diag.ADR002, diag.Params{"id": fmt.Sprintf("%s (%s:%d and %s:%d)",
				entries[i].ID, prev.File, prev.Line, entries[i].File, entries[i].Line)},
				diag.WithSource(diag.Source{URI: entries[i].File, Line: entries[i].Line}))
		}

		r.Entries = append(r.Entries, entries[i])
		seen[key] = &r.Entries[len(r.Entries)-1]
	}

	return nil
}

// expandRegisterFiles gives the sorted unique register files that the items name.
func expandRegisterFiles(root string, items []string) ([]string, error) {
	set := map[string]struct{}{}

	for _, item := range items {
		item = strings.TrimPrefix(item, "./")

		if !strings.ContainsAny(item, globMeta) {
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(item))); err != nil || !info.Mode().IsRegular() {
				return nil, diag.Fail(diag.ADR001, diag.Detail("register file not found: "+item),
					diag.WithSource(diag.Source{URI: item}))
			}

			set[item] = struct{}{}

			continue
		}

		matched, err := binding.ExpandFileGlobs(root, []string{item})
		if err != nil {
			return nil, diag.Fail(diag.ADR001, diag.Detail(err.Error()), diag.WithSource(diag.Source{URI: item}))
		}

		for _, m := range matched {
			set[m] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

// registerSource is a register file with its parsed YAML tree, which locates the problems of its entries.
type registerSource struct {
	root *yaml.Node
	file string
}

// problem makes a CO-ADR-001 error at the node of the JSON Pointer (line 0 without a tree).
func (s registerSource) problem(detail, pointer string) *diag.Error {
	return diag.Fail(diag.ADR001, diag.Detail(detail), diag.WithSource(diag.Source{URI: s.file, Pointer: pointer, Line: s.line(pointer)}))
}

// line gives the line of the node at the JSON Pointer, 0 when there is none.
func (s registerSource) line(pointer string) int {
	if node := nodeAt(s.root, pointer); node != nil {
		return node.Line
	}

	return 0
}

// loadRegisterFile reads, validates, and converts one register file.
func loadRegisterFile(root, path string) (RegisterFile, []Entry, error) {
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) //nolint:gosec // the path matched the workspace adr.files.
	if err != nil {
		return RegisterFile{}, nil, registerSource{file: path}.problem("cannot read: "+err.Error(), "")
	}

	doc, problems := yamlparse.Parse(text)
	if len(problems) > 0 {
		return RegisterFile{}, nil, diag.Fail(diag.ADR001, diag.Detail("cannot parse: "+problems[0].Message),
			diag.WithSource(diag.Source{URI: path, Line: problems[0].Line}))
	}

	node, _ := yamlparse.ParseNode(text)
	src := registerSource{file: path, root: node}

	if errs := registerSchema().Validate(doc); len(errs) > 0 {
		first := errs[0]

		return RegisterFile{}, nil, src.problem(first.Message+" at "+orRoot(first.InstancePath), first.InstancePath)
	}

	entries, err := entriesOf(doc, src)
	if err != nil {
		return RegisterFile{}, nil, err
	}

	return RegisterFile{Path: path, Digest: digest.OfBytes(text)}, entries, nil
}

// entriesOf converts the `decisions` of a schema-valid register document.
func entriesOf(doc any, src registerSource) ([]Entry, error) {
	top, _ := doc.(map[string]any)
	list, _ := top["decisions"].([]any)
	entries := make([]Entry, 0, len(list))

	for i, raw := range list {
		fields, _ := raw.(map[string]any)

		entry, err := entryOf(fields, src, "/decisions/"+strconv.Itoa(i))
		if err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

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

// mappingChild gives the value of the key in a mapping node, or nil.
func mappingChild(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

// sequenceChild gives the item at the decimal index in a sequence node, or nil.
func sequenceChild(node *yaml.Node, index string) *yaml.Node {
	idx, err := strconv.Atoi(index)
	if err != nil {
		return nil
	}

	if idx < 0 || idx >= len(node.Content) {
		return nil
	}

	return node.Content[idx]
}

func stringsOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))

	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func stringOf(m map[string]any, key string) string {
	s, _ := m[key].(string)

	return s
}

// registerIDPattern is the form of a register decision ID: `ADR-<number>`, the prefix in any case and the number
// with or without leading zeros. It is the form of the provider label (`ADR-<n>`, CBM 0.12.1, O12), so an ID of
// another form could never match a decision.
var registerIDPattern = regexp.MustCompile(`(?i)^ADR-\d+$`)

// entryOf converts one schema-valid decision. It fails with CO-ADR-001 for an ID that is not `ADR-<number>` and for
// a `released` list on an active entry. The ID is stored in canonical form (see CanonicalID): it is the one name of
// the decision in every result.
func entryOf(fields map[string]any, src registerSource, pointer string) (Entry, error) {
	id := strings.TrimSpace(stringOf(fields, "id"))
	if !registerIDPattern.MatchString(id) {
		return Entry{}, src.problem(fmt.Sprintf(
			"decision id %q is not of the form ADR-<number> (for example ADR-7 or ADR-0007)", id), pointer+"/id")
	}

	entry := Entry{
		ID:          CanonicalID(id),
		Path:        stringOf(fields, "path"),
		Project:     stringOf(fields, "project"),
		Expect:      ExpectActive,
		Categories:  stringsOf(fields["categories"]),
		Enforcement: EnforcementNone,
		File:        src.file,
		Line:        src.line(pointer),
	}

	if v := stringOf(fields, "expect"); v != "" {
		entry.Expect = Expect(v)
	}

	if v := stringOf(fields, "enforcement"); v != "" {
		entry.Enforcement = EnforcementMode(v)
	}

	entry.Governs = governsOf(fields, src, pointer)
	entry.EnforcedBy = enforcedByOf(fields, src, pointer)

	released, err := releasedOf(fields, src, pointer)
	if err != nil {
		return Entry{}, err
	}

	entry.Released = released

	if len(entry.Released) > 0 && entry.Expect != ExpectHistorical {
		return Entry{}, src.problem(fmt.Sprintf("decision %s lists released items but expects %s; released needs expect: historical",
			entry.ID, entry.Expect), pointer+"/released")
	}

	return entry, nil
}

// governsOf converts the `governs` list of a decision.
func governsOf(fields map[string]any, src registerSource, pointer string) []GovernsRef {
	out := make([]GovernsRef, 0, len(anyList(fields["governs"])))

	for j, raw := range anyList(fields["governs"]) {
		item, _ := raw.(map[string]any)
		ref := GovernsRef{Ref: stringOf(item, "ref"), Line: src.line(pointer + "/governs/" + strconv.Itoa(j))}

		if sel, ok := item["select"].(map[string]any); ok {
			ref.Select = &Selector{CalmNodes: stringsOf(sel["calmNodes"]), NodeTypes: stringsOf(sel["nodeTypes"]), Tags: stringsOf(sel["tags"])}
		}

		out = append(out, ref)
	}

	return out
}

// enforcedByOf converts the `enforcedBy` list of a decision.
func enforcedByOf(fields map[string]any, src registerSource, pointer string) []RuleRef {
	out := make([]RuleRef, 0, len(anyList(fields["enforcedBy"])))

	for j, raw := range anyList(fields["enforcedBy"]) {
		item, _ := raw.(map[string]any)
		pack, _ := item["pack"].(map[string]any)

		out = append(out, RuleRef{
			Rule: stringOf(item, "rule"), PackID: stringOf(pack, "id"), PackVersion: stringOf(pack, "version"),
			Line: src.line(pointer + "/enforcedBy/" + strconv.Itoa(j)),
		})
	}

	return out
}

// releasedOf converts the `released` list of a decision. It fails with CO-ADR-001 for a released rule that is not
// `<rule-id>@<pack-id>/<pack-version>`.
func releasedOf(fields map[string]any, src registerSource, pointer string) ([]Release, error) {
	out := []Release{}

	for j, raw := range anyList(fields["released"]) {
		item, _ := raw.(map[string]any)
		at := pointer + "/released/" + strconv.Itoa(j)
		rel := Release{Ref: stringOf(item, "ref"), Reason: stringOf(item, "reason"), Line: src.line(at)}

		if text := stringOf(item, "rule"); text != "" {
			parsed, ok := ParseRuleRef(text)
			if !ok {
				return nil, src.problem("released rule "+strconv.Quote(text)+" is not <rule-id>@<pack-id>/<pack-version>", at)
			}

			parsed.Line = rel.Line
			rel.Rule = &parsed
		}

		out = append(out, rel)
	}

	return out, nil
}

func anyList(v any) []any {
	list, _ := v.([]any)

	return list
}
