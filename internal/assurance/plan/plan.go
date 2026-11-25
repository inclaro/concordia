// Package plan reads the JSON that `terraform show -json` or `tofu show -json` writes for a saved plan. It records the
// planned resources by address with their planned attribute values, as evidence of the rendered-or-evaluated level.
// A plan is a prediction: nothing here says that anything was applied, and the package has no notion of deployment.
//
// Two kinds of attribute never become values. An attribute that is unknown until apply is a gap, never a guess. An
// attribute that the plan marks sensitive is replaced by a redaction marker and recorded as a redaction; the plan's
// other copies of the value (`variables`, `planned_values`, `prior_state`) are never read, because they carry no
// marker per attribute. Of the values before a change (`change.before`), only the owner attributes of a firewall rule
// resource that the plan deletes or replaces are read (see Resource.Prior). Of the `configuration` section only the
// `references` lists are read, which hold addresses and not values; `constant_value` is never read.
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
	// ProviderConfig is the `provider_config_key` of the configuration of the resource, for example `azurerm` or
	// `azurerm.other`: the provider configuration, and so the account or subscription, that manages it. It is ""
	// when the plan's configuration does not give it.
	ProviderConfig string `json:"providerConfig,omitempty"`
	// Actions are the planned actions: `create`, `update`, `delete`, `read`, `no-op`; a replacement is two of them.
	Actions []string `json:"actions"`
	// Values are the planned attribute values after apply. Unknown attributes are absent, an unknown element of a list
	// is null at its position, and sensitive ones hold the redaction marker. Values is nil when the resource does not
	// exist after apply (the action is `delete`).
	Values map[string]any `json:"values,omitempty"`
	// References are the resources that the configuration names in each attribute, by the attribute path in the form
	// of Unknown (for example `ingress[0].security_groups`). Each address is absolute in the module tree and has no
	// instance key; the lists are sorted and hold no duplicates. References is nil when the configuration names none.
	References map[string][]string `json:"references,omitempty"`
	// Prior holds, for a firewall rule resource that the plan deletes or replaces, the attributes that named its
	// security group or network security group before the change: `security_group_id`, `network_security_group_name`,
	// and `resource_group_name`, when they were known strings that need no redaction. Prior is nil for every other
	// resource, and it never holds another attribute.
	Prior map[string]string `json:"prior,omitempty"`
	// Unknown are the paths of the attributes that are unknown until apply, sorted, for example `input.owner`.
	Unknown []string `json:"unknown,omitempty"`
	// Configured are the paths of the attribute and block keys that the configuration of the resource sets, sorted,
	// in the form of References (for example `vpc_id` or `network_interface[0].device_index`). Only key names are
	// read, never a value. A `dynamic` block does not appear in the configuration of a plan, so a block key that is
	// absent may still be set by one. Configured is nil when the plan's configuration does not cover the resource.
	Configured []string `json:"configured,omitempty"`
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
	AfterSensitive any `json:"after_sensitive"`
	// Before and BeforeSensitive are the values before the change and their sensitivity mirror. They are decoded
	// only for the owner attributes of a deleted or replaced firewall rule (see priorOwner).
	Before          json.RawMessage `json:"before"`
	BeforeSensitive json.RawMessage `json:"before_sensitive"`
	Actions         []string        `json:"actions"`
}

// reader reads one plan file. file is the workspace-relative path, used in diagnostics and records.
type reader struct {
	file string
	data []byte
}

// rejection says why a file is not a plan that the reader can use. It is empty when the file is usable.
type rejection string

// Read reads the plan JSON of one file. file is the workspace-relative path, used in diagnostics and records.
//
// A file that the reader cannot use gives a nil Plan and a CO-PRV-008 warning: an unsupported or missing format
// version, JSON that is not a plan (a state file, for example), an errored plan, or a resource change without an
// address. A partly unusable plan is never returned: dependent claims stay `unknown`.
func Read(data []byte, file string) (*Plan, []diag.Diagnostic) {
	r := reader{data: data, file: file}

	return r.read()
}

func (r *reader) read() (*Plan, []diag.Diagnostic) {
	doc, why := r.decode()
	if why != "" {
		return r.reject(why)
	}

	out := r.newPlan(&doc)

	if why = r.addChanges(out, doc.ResourceChanges); why != "" {
		return r.reject(why)
	}

	out.addConfiguration(doc.Configuration)

	out.sortEntries()

	return out, nil
}

// reject is the result for a file that the reader cannot use.
func (r *reader) reject(why rejection) (*Plan, []diag.Diagnostic) {
	return nil, []diag.Diagnostic{diag.New(diag.PRV008, diag.Detail(string(why)), diag.WithSource(diag.Source{URI: r.file}))}
}

// decode decodes the plan JSON. why says why it is not a plan that the reader can use.
func (r *reader) decode() (doc document, why rejection) {
	dec := json.NewDecoder(bytes.NewReader(r.data))
	dec.UseNumber()

	if err := dec.Decode(&doc); err != nil {
		return doc, rejection("not valid JSON: " + err.Error())
	}

	why = checkDocument(&doc)

	return doc, why
}

// newPlan is the plan of a document before its resource changes are read.
func (r *reader) newPlan(doc *document) *Plan {
	out := &Plan{
		Digest: digest.OfBytes(r.data), FormatVersion: doc.FormatVersion, ToolVersion: doc.TerraformVersion,
		Coverage: assurance.CoverageComplete, Resources: []Resource{}, Gaps: []assurance.Gap{}, Redactions: []redact.Record{},
	}

	if doc.Complete != nil && !*doc.Complete {
		out.Coverage = assurance.CoveragePartial
		out.Gaps = append(out.Gaps, assurance.Gap{
			Kind: GapIncompletePlan, Detail: "the plan is marked incomplete and has deferred changes", Path: r.file,
		})
	}

	return out
}

// addChanges reads the resource changes into the plan. why says why one is unusable.
func (r *reader) addChanges(p *Plan, changes []json.RawMessage) rejection {
	for i, raw := range changes {
		rc, ok := decodeResourceChange(raw)
		if !ok {
			return rejection("resource_changes[" + strconv.Itoa(i) + "] has no address or no change")
		}

		res, gaps, redactions := r.readResource(&rc)
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

// checkDocument says why a decoded document is not a plan that the reader can use.
func checkDocument(doc *document) rejection {
	switch {
	case doc.FormatVersion == "":
		return "no format_version: not the output of `show -json`"
	case !slices.Contains(SupportedFormatVersions, doc.FormatVersion):
		return rejection("format version " + doc.FormatVersion + " is not supported (supported: " + strings.Join(SupportedFormatVersions, ", ") + ")")
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
func (r *reader) readResource(item *resourceChange) (Resource, []assurance.Gap, []redact.Record) {
	res := Resource{
		Address: item.Address, Module: item.ModuleAddress, Mode: item.Mode, Type: item.Type, Name: item.Name,
		Provider: item.ProviderName, Deposed: item.Deposed, Actions: slices.Clone(item.Change.Actions),
	}

	if res.Actions == nil {
		res.Actions = []string{}
	}

	prior, redactions := r.priorOwner(item)
	res.Prior = prior

	// An object that is destroyed has no `after`: it has no planned values and so no gaps.
	after, exists := item.Change.After.(map[string]any)
	if !exists {
		return res, nil, redactions
	}

	walk := walker{address: item.Address, file: r.file, redactions: redactions}
	res.Values = walk.object(after, &node{unknown: item.Change.AfterUnknown, sensitive: item.Change.AfterSensitive})

	slices.SortFunc(walk.unknown, canonjson.CompareUTF16)
	res.Unknown = walk.unknown

	gaps := make([]assurance.Gap, len(walk.unknown))
	for i, path := range walk.unknown {
		gaps[i] = assurance.Gap{Kind: GapUnknownAfterApply, Detail: item.Address + ": " + path + " is unknown until apply", Path: r.file}
	}

	return res, gaps, walk.redactions
}
