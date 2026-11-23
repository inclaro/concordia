// Package plan reads the JSON that `terraform show -json` or `tofu show -json` writes for a saved plan. It records the
// planned resources by address with their planned attribute values, as evidence of the rendered-or-evaluated level.
// A plan is a prediction: nothing here says that anything was applied, and the package has no notion of deployment.
//
// Two kinds of attribute never become values. An attribute that is unknown until apply is a gap, never a guess. An
// attribute that the plan marks sensitive is replaced by a redaction marker and recorded as a redaction; the plan's
// other copies of the value (`variables`, `planned_values`, `prior_state`) are never read, because they carry no
// marker per attribute. Of the `configuration` section only the `references` lists are read, which hold addresses and
// not values; `constant_value` is never read.
package plan

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/redact"
)

// SupportedFormatVersions are the `format_version` values of the plan JSON that the reader accepts. The JSON format
// is versioned by the Terraform core: `1.0` to `1.2` (Terraform 1.1 to 1.17) share the fields that this reader uses,
// `resource_changes` with `change.after`, `after_unknown` and `after_sensitive`. The fixture of the reader is
// Terraform 1.17.0 (`1.2`). OpenTofu writes the same format; that was not run for this reader. Earlier versions (`0.x`)
// have no per-attribute sensitivity in `resource_changes`, so the reader cannot keep their secrets out of the output,
// and rejects them. A later version is rejected until it is added here and tested.
var SupportedFormatVersions = []string{"1.0", "1.1", "1.2"}

// Gap kinds of a plan.
const (
	// GapUnknownAfterApply is an attribute whose value the plan cannot know until apply.
	GapUnknownAfterApply = "unknown-after-apply"
	// GapIncompletePlan is a plan that Terraform marks incomplete: it has deferred changes, so the resource set is partial.
	GapIncompletePlan = "incomplete-plan"
)

// Plan is the evidence that one plan file gives.
type Plan struct {
	// Digest is the digest of the plan file ("sha256:<hex>"), the subject that a statement binds.
	Digest string `json:"digest"`
	// FormatVersion is the `format_version` of the file.
	FormatVersion string `json:"formatVersion"`
	// ToolVersion is the `terraform_version` of the file (OpenTofu writes its own version here).
	ToolVersion string `json:"toolVersion,omitempty"`
	// Coverage is complete when the plan lists every change, partial when Terraform marked it incomplete.
	Coverage assurance.Coverage `json:"coverage"`
	// Resources are sorted by address, then by deposed key.
	Resources []Resource `json:"resources"`
	// Gaps are sorted by detail. There is one for each unknown attribute and one for an incomplete plan.
	Gaps []assurance.Gap `json:"gaps"`
	// Redactions are the values of sensitive attributes that were removed, sorted by property. They never hold a value.
	Redactions []redact.Record `json:"redactions"`
}

// Resource is one resource (or data source) of the plan.
type Resource struct {
	// Address is the full resource address, for example `module.net.aws_security_group.web["public"]`.
	Address string `json:"address"`
	// Module is the address of the module that holds the resource, empty for the root module.
	Module string `json:"module,omitempty"`
	Mode   string `json:"mode"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	// Provider is the provider source address, for example `registry.terraform.io/hashicorp/aws`.
	Provider string `json:"provider,omitempty"`
	// Deposed is the key of a deposed object that the plan destroys, empty otherwise.
	Deposed string `json:"deposed,omitempty"`
	// Actions are the planned actions: `create`, `update`, `delete`, `read`, `no-op`; a replacement is two of them.
	Actions []string `json:"actions"`
	// Values are the planned attribute values after apply. Unknown attributes are absent, sensitive ones hold the
	// redaction marker. Values is nil when the resource does not exist after apply (the action is `delete`).
	Values map[string]any `json:"values,omitempty"`
	// Unknown are the paths of the attributes that are unknown until apply, sorted, for example `input.owner`.
	Unknown []string `json:"unknown,omitempty"`
	// References are the resources that the configuration names in each attribute, by the attribute path in the form
	// of Unknown (for example `ingress[0].security_groups`). Each address is absolute in the module tree and has no
	// instance key; the lists are sorted and hold no duplicates. References is nil when the configuration names none.
	References map[string][]string `json:"references,omitempty"`
}

// document is the part of the plan JSON that the reader uses. Fields that it does not use are not decoded.
//
//nolint:tagliatelle // the field names are those of the Terraform JSON format.
type document struct {
	Complete         *bool             `json:"complete"`
	FormatVersion    string            `json:"format_version"`
	TerraformVersion string            `json:"terraform_version"`
	PlannedValues    json.RawMessage   `json:"planned_values"`
	Values           json.RawMessage   `json:"values"`
	ResourceChanges  []json.RawMessage `json:"resource_changes"`
	Configuration    json.RawMessage   `json:"configuration"`
	Errored          bool              `json:"errored"`
}

//nolint:tagliatelle // the field names are those of the Terraform JSON format.
type resourceChange struct {
	Change        *change `json:"change"`
	Address       string  `json:"address"`
	ModuleAddress string  `json:"module_address"`
	Mode          string  `json:"mode"`
	Type          string  `json:"type"`
	Name          string  `json:"name"`
	ProviderName  string  `json:"provider_name"`
	Deposed       string  `json:"deposed"`
}

//nolint:tagliatelle // the field names are those of the Terraform JSON format.
type change struct {
	After        any `json:"after"`
	AfterUnknown any `json:"after_unknown"`
	// AfterSensitive mirrors After: true marks a value (or a whole subtree) as sensitive.
	AfterSensitive any      `json:"after_sensitive"`
	Actions        []string `json:"actions"`
}

// reject is the result for a file that the reader cannot use.
func reject(file, detail string) (*Plan, []diag.Diagnostic) {
	return nil, []diag.Diagnostic{diag.New(diag.PRV008, diag.Detail(detail), diag.WithSource(diag.Source{URI: file}))}
}

// Read reads the plan JSON of one file. file is the workspace-relative path, used in diagnostics and records.
//
// A file that the reader cannot use gives a nil Plan and a CO-PRV-008 warning: an unsupported or missing format
// version, JSON that is not a plan (a state file, for example), an errored plan, or a resource change without an
// address. A partly unusable plan is never returned: dependent claims stay `unknown`.
func Read(data []byte, file string) (*Plan, []diag.Diagnostic) {
	doc, detail := decodeDocument(data)
	if detail != "" {
		return reject(file, detail)
	}

	out := newPlan(data, &doc, file)

	if detail = out.addChanges(doc.ResourceChanges, file); detail != "" {
		return reject(file, detail)
	}

	out.addReferences(doc.Configuration)

	out.sortEntries()

	return out, nil
}

// decodeDocument decodes the plan JSON. detail says why it is not a plan that the reader can use, or "" when it is.
func decodeDocument(data []byte) (doc document, detail string) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	if err := dec.Decode(&doc); err != nil {
		return doc, "not valid JSON: " + err.Error()
	}

	detail = checkDocument(&doc)

	return doc, detail
}

// newPlan is the plan of a document before its resource changes are read.
func newPlan(data []byte, doc *document, file string) *Plan {
	out := &Plan{
		Digest: digest.OfBytes(data), FormatVersion: doc.FormatVersion, ToolVersion: doc.TerraformVersion,
		Coverage: assurance.CoverageComplete, Resources: []Resource{}, Gaps: []assurance.Gap{}, Redactions: []redact.Record{},
	}

	if doc.Complete != nil && !*doc.Complete {
		out.Coverage = assurance.CoveragePartial
		out.Gaps = append(out.Gaps, assurance.Gap{
			Kind: GapIncompletePlan, Detail: "the plan is marked incomplete and has deferred changes", Path: file,
		})
	}

	return out
}

// addChanges reads the resource changes into the plan. detail says why one is unusable, or "" when all are usable.
func (p *Plan) addChanges(changes []json.RawMessage, file string) (detail string) {
	for i, raw := range changes {
		rc, ok := decodeResourceChange(raw)
		if !ok {
			return "resource_changes[" + strconv.Itoa(i) + "] has no address or no change"
		}

		res, gaps, redactions := readResource(&rc, file)
		p.Resources = append(p.Resources, res)
		p.Gaps = append(p.Gaps, gaps...)
		p.Redactions = append(p.Redactions, redactions...)
	}

	return ""
}

// decodeResourceChange decodes one resource change. It reports false for one without an address or without a change.
func decodeResourceChange(raw json.RawMessage) (rc resourceChange, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := dec.Decode(&rc); err != nil {
		return rc, false
	}

	return rc, rc.Address != "" && rc.Change != nil
}

// sortEntries sorts the resources, gaps and redactions into the order that the fields of Plan promise.
func (p *Plan) sortEntries() {
	slices.SortStableFunc(p.Resources, func(a, b Resource) int {
		if c := canonjson.CompareUTF16(a.Address, b.Address); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Deposed, b.Deposed)
	})
	slices.SortStableFunc(p.Gaps, func(a, b assurance.Gap) int { return canonjson.CompareUTF16(a.Detail, b.Detail) })
	slices.SortStableFunc(p.Redactions, func(a, b redact.Record) int { return canonjson.CompareUTF16(a.Property, b.Property) })
}

// checkDocument says why a decoded document is not a plan that the reader can use, or "" when it is.
func checkDocument(doc *document) string {
	switch {
	case doc.FormatVersion == "":
		return "no format_version: not the output of `show -json`"
	case !slices.Contains(SupportedFormatVersions, doc.FormatVersion):
		return "format version " + doc.FormatVersion + " is not supported (supported: " + strings.Join(SupportedFormatVersions, ", ") + ")"
	case doc.PlannedValues == nil && doc.ResourceChanges == nil:
		if doc.Values != nil {
			return "the file is a state (`show -json` without a plan file), not a plan"
		}

		return "no planned_values and no resource_changes: not a plan"
	case doc.Errored:
		return "the plan is marked errored; its values are incomplete"
	}

	return ""
}

// readResource turns one resource change into a Resource, its gaps, and its redactions.
func readResource(item *resourceChange, file string) (Resource, []assurance.Gap, []redact.Record) {
	res := Resource{
		Address: item.Address, Module: item.ModuleAddress, Mode: item.Mode, Type: item.Type, Name: item.Name,
		Provider: item.ProviderName, Deposed: item.Deposed, Actions: slices.Clone(item.Change.Actions),
	}

	if res.Actions == nil {
		res.Actions = []string{}
	}

	// An object that is destroyed has no `after`: it has no planned values and so no gaps.
	after, exists := item.Change.After.(map[string]any)
	if !exists {
		return res, nil, nil
	}

	walk := walker{address: item.Address, file: file}
	res.Values = walk.object(after, &node{unknown: item.Change.AfterUnknown, sensitive: item.Change.AfterSensitive})

	slices.SortFunc(walk.unknown, canonjson.CompareUTF16)
	res.Unknown = walk.unknown

	gaps := make([]assurance.Gap, len(walk.unknown))
	for i, path := range walk.unknown {
		gaps[i] = assurance.Gap{Kind: GapUnknownAfterApply, Detail: item.Address + ": " + path + " is unknown until apply", Path: file}
	}

	return res, gaps, walk.redactions
}

// walker walks the `after` value of one resource beside its `after_unknown` and `after_sensitive` mirrors.
type walker struct {
	address    string
	file       string
	unknown    []string
	redactions []redact.Record
}

// mark gives the mirror entry for a key of an object: false when the mirror has none.
func mark(mirror any, key string) any {
	if m, ok := mirror.(map[string]any); ok {
		return m[key]
	}

	return nil
}

// markAt gives the mirror entry for an index of an array.
func markAt(mirror any, index int) any {
	if m, ok := mirror.([]any); ok && index < len(m) {
		return m[index]
	}

	return nil
}

func isTrue(v any) bool { b, ok := v.(bool); return ok && b }

// node is one value of `after` with its entries in the `after_unknown` and `after_sensitive` mirrors. name is the
// attribute name that the value belongs to; an array element has the name of its array.
type node struct {
	item      any
	unknown   any
	sensitive any
	path      string
	name      string
}

// child is the node for a key of an object.
func (n *node) child(item any, key string) *node {
	return &node{item: item, unknown: mark(n.unknown, key), sensitive: mark(n.sensitive, key), path: join(n.path, key), name: key}
}

// element is the node for an index of an array. An index beyond value has no item: only the mirror names it.
func (n *node) element(value []any, index int) *node {
	var item any
	if index < len(value) {
		item = value[index]
	}

	return &node{
		item: item, unknown: markAt(n.unknown, index), sensitive: markAt(n.sensitive, index),
		path: n.path + "[" + strconv.Itoa(index) + "]", name: n.name,
	}
}

// object cleans the attributes of an object. Its node holds the mirrors of the object.
func (w *walker) object(value map[string]any, n *node) map[string]any {
	out := make(map[string]any, len(value))

	for key, item := range value {
		if cleaned, keep := w.value(n.child(item, key)); keep {
			out[key] = cleaned
		}
	}

	w.unknownKeys(value, n)

	return out
}

// unknownKeys records the unknown attributes that are not in `after`; the mirror is the only place that names them.
func (w *walker) unknownKeys(value map[string]any, parent *node) {
	mirror, ok := parent.unknown.(map[string]any)
	if !ok {
		return
	}

	for key, u := range mirror {
		if _, present := value[key]; present {
			continue
		}

		w.value(&node{unknown: u, path: join(parent.path, key), name: key})
	}
}

// array cleans the elements of an array. Its node holds the mirrors of the array.
func (w *walker) array(value []any, n *node) []any {
	count := len(value)
	if m, ok := n.unknown.([]any); ok {
		count = max(count, len(m))
	}

	out := make([]any, 0, len(value))

	for i := range count {
		cleaned, keep := w.value(n.element(value, i))
		if keep && i < len(value) {
			out = append(out, cleaned)
		}
	}

	return out
}

// value cleans one value. keep is false when the value is unknown and so has no value to keep.
func (w *walker) value(current *node) (cleaned any, keep bool) {
	if isTrue(current.unknown) {
		w.unknown = append(w.unknown, current.path)

		return nil, false
	}

	if isTrue(current.sensitive) && current.item != nil {
		w.redact(current.path, redact.ClassSecretData)

		return redact.Marker(redact.ClassSecretData), true
	}

	switch typed := current.item.(type) {
	case map[string]any:
		return w.object(typed, current), true
	case []any:
		return w.array(typed, current), true
	case string:
		return w.text(typed, current), true
	case nil:
		w.partlyUnknown(current)

		return nil, true
	default:
		return current.item, true
	}
}

// text removes a credential from a string. The plan marks what Terraform knows to be sensitive; a credential in a
// plain attribute is still removed.
func (w *walker) text(value string, n *node) string {
	text, records := redact.Default().RedactValue(n.name, value)
	for _, r := range records {
		w.redact(n.path, r.Class)
	}

	return text
}

// partlyUnknown handles a null value: `after` omits an object or a list that is unknown in part, and the mirror still
// names the unknown parts.
func (w *walker) partlyUnknown(n *node) {
	rest := &node{unknown: n.unknown, path: n.path, name: n.name}

	switch n.unknown.(type) {
	case map[string]any:
		w.object(nil, rest)
	case []any:
		w.array(nil, rest)
	}
}

func (w *walker) redact(path, class string) {
	w.redactions = append(w.redactions, redact.Record{File: w.file, Property: w.address + "." + path, Class: class})
}

func join(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}
