package plan

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// droppedRoots are the first segments of references that cannot carry a resource ID: the index of `count`, files
// of `path`, the resource's own attributes through `self`, and workspace facts of `terraform`. `each.key` is a key
// and is dropped too. `var.`, `local.` and `each.value` can carry an ID and are kept as opaque entries.
var droppedRoots = []string{"count", "path", "self", "terraform"}

// opaqueRoots are the first segments of the opaque entries that keep their name: `var.<name>` and `local.<name>`.
var opaqueRoots = []string{"var", "local"}

// eachValue is the opaque entry of a reference through `each.value`.
const eachValue = "each.value"

const moduleRoot = "module."

// modulePath is the address prefix of a module in the module tree, for example `module.net.`; empty for the root.
type modulePath string

// attrPath is the path of an attribute of a resource, in the form of Resource.Unknown.
type attrPath string

// reference is one entry of a `references` list of the configuration, for example `aws_security_group.db.id`.
type reference string

// configuration is the part of the `configuration` section that the reader uses: the resources of each module and
// the `references` lists of their expressions. Values (`constant_value`) are never decoded.
//
//nolint:tagliatelle // the field names are those of the Terraform JSON format.
type configuration struct {
	RootModule configModule `json:"root_module"`
}

//nolint:tagliatelle // the field names are those of the Terraform JSON format.
type configModule struct {
	ModuleCalls map[string]moduleCall `json:"module_calls"`
	Resources   []configResource      `json:"resources"`
}

type moduleCall struct {
	Module *configModule `json:"module"`
}

// configResource is one resource of the configuration: its address, its provider configuration key
// (`provider_config_key`), and its expressions.
type configResource struct {
	Expressions       map[string]json.RawMessage
	Address           string
	ProviderConfigKey string
}

// UnmarshalJSON reads the three keys that the reader uses; the key names are those of the Terraform JSON format.
func (c *configResource) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// A key that is absent or of another type leaves its field empty, as for any other partial configuration.
	_ = json.Unmarshal(raw["expressions"], &c.Expressions)
	_ = json.Unmarshal(raw["address"], &c.Address)
	_ = json.Unmarshal(raw["provider_config_key"], &c.ProviderConfigKey)

	return nil
}

// expression is an expression of the configuration, of which only the references are read.
type expression struct {
	References []reference `json:"references"`
}

// ConfigAddress is the address without its instance keys, for example `module.net.aws_security_group.web` for
// `module.net["a"].aws_security_group.web[0]`. It is the form that the configuration of a plan uses.
func ConfigAddress(address string) string {
	if !strings.Contains(address, "[") {
		return address
	}

	var out strings.Builder

	keyed := keyedAddress(address)

	for i := 0; i < len(keyed); {
		if keyed[i] == '[' {
			i = keyed.keyEnd(i)

			continue
		}

		out.WriteByte(keyed[i])
		i++
	}

	return out.String()
}

// keyedAddress is an address that may hold instance keys, for example `aws_instance.app["a"]`.
type keyedAddress string

// keyEnd is the index after the instance key that opens at start, or the end of the address when the key is not
// closed. A quoted key may hold `]` and `.`.
func (a keyedAddress) keyEnd(start int) int {
	quoted := false

	for i := start + 1; i < len(a); i++ {
		switch {
		case quoted && a[i] == '\\':
			i++
		case a[i] == '"':
			quoted = !quoted
		case !quoted && a[i] == ']':
			return i + 1
		}
	}

	return len(a)
}

// addConfiguration reads the configuration of each resource: the `references` lists, and the attribute and block
// keys that the configuration sets (Resource.Configured). It attaches them to the resources whose address, without
// instance keys, is the configuration address. A missing or malformed configuration attaches nothing.
func (p *Plan) addConfiguration(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}

	var config configuration
	if json.Unmarshal(raw, &config) != nil {
		return
	}

	byAddress := map[string]*resourceRefs{}
	collectModule(&config.RootModule, "", byAddress)

	for i := range p.Resources {
		if refs, ok := byAddress[ConfigAddress(p.Resources[i].Address)]; ok {
			p.Resources[i].References = refs.references()
			p.Resources[i].Configured = slices.Clone(refs.configured)
			p.Resources[i].ProviderConfig = refs.providerConfig
		}
	}
}

// collectModule adds the configuration of the resources of a module and of its module calls, by absolute address.
func collectModule(module *configModule, prefix modulePath, out map[string]*resourceRefs) {
	collectResources(module.Resources, prefix, out)

	for name, call := range module.ModuleCalls {
		if call.Module != nil {
			collectModule(call.Module, prefix+moduleRoot+modulePath(name)+".", out)
		}
	}
}

// collectResources adds the configuration of the resources of one module, by absolute address.
func collectResources(resources []configResource, prefix modulePath, out map[string]*resourceRefs) {
	for i := range resources {
		refs := &resourceRefs{
			prefix: prefix, byPath: map[string][]string{}, configured: []string{},
			providerConfig: resources[i].ProviderConfigKey,
		}
		refs.block(resources[i].Expressions, "")
		slices.SortFunc(refs.configured, canonjson.CompareUTF16)

		out[string(prefix)+resources[i].Address] = refs
	}
}

// resourceRefs collects the configuration of one resource. prefix is the module prefix of the resource; byPath holds
// the absolute addresses by attribute path; configured holds the paths of the keys that the configuration sets.
type resourceRefs struct {
	byPath         map[string][]string
	prefix         modulePath
	providerConfig string
	configured     []string
}

// references are the sorted reference lists, or nil when the configuration names no resource.
func (r *resourceRefs) references() map[string][]string {
	if len(r.byPath) == 0 {
		return nil
	}

	return sortedRefs(r.byPath)
}

// block adds the keys and the references of each expression of a block, by attribute path.
func (r *resourceRefs) block(exprs map[string]json.RawMessage, path attrPath) {
	for key, raw := range exprs {
		keyPath := attrPath(join(string(path), key))
		r.configured = append(r.configured, string(keyPath))
		r.expression(raw, keyPath)
	}
}

// expression adds the references of one expression. An object with `references` or `constant_value` is an
// expression; any other object is a nested block written once (a single or a map block), whose attributes have the
// path `<block>.<attribute>`; an array of objects is a nested block list, whose elements have the path form of
// Resource.Unknown, for example `ingress[0].security_groups`. The value of `constant_value` is never decoded.
func (r *resourceRefs) expression(raw json.RawMessage, path attrPath) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		r.object(object, raw, path)

		return
	}

	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}

	for i, block := range blocks {
		r.block(block, path+attrPath("["+strconv.Itoa(i)+"]"))
	}
}

// object adds the references of an expression object, or the keys and references of a nested block object.
func (r *resourceRefs) object(object map[string]json.RawMessage, raw json.RawMessage, path attrPath) {
	_, hasRefs := object["references"]
	_, hasConstant := object["constant_value"]

	if !hasRefs && !hasConstant {
		r.block(object, path)

		return
	}

	var expr expression
	if json.Unmarshal(raw, &expr) == nil {
		r.add(expr.References, path)
	}
}

// add adds the resource addresses that the reference strings name.
func (r *resourceRefs) add(references []reference, path attrPath) {
	for _, ref := range references {
		if address, ok := resourceOf(ref); ok {
			r.byPath[string(path)] = append(r.byPath[string(path)], string(r.prefix)+address)
		}
	}
}

// resourceOf is the address of the resource, data source or module call that a reference names, or the opaque entry
// of a variable, a local or `each.value`, without the attribute after it and without instance keys. It reports
// false for a reference that cannot carry a resource ID.
func resourceOf(ref reference) (string, bool) {
	parts := strings.Split(ConfigAddress(string(ref)), ".")

	switch {
	case slices.Contains(droppedRoots, parts[0]):
		return "", false
	case parts[0] == "each":
		return eachOf(parts)
	case parts[0] == "data" && len(parts) >= 3:
		return strings.Join(parts[:3], "."), true
	case parts[0] != "data" && len(parts) >= 2:
		return strings.Join(parts[:2], "."), true
	}

	return "", false
}

// eachOf keeps a reference through `each.value` as its opaque entry and drops `each.key`.
func eachOf(parts []string) (string, bool) {
	if len(parts) >= 2 && parts[1] == "value" {
		return eachValue, true
	}

	return "", false
}

// IsOpaqueReference reports whether an entry of Resource.References is a variable (`var.<name>`), a local
// (`local.<name>`) or `each.value`, after any `module.<name>.` prefixes. Such an entry may carry a resource ID that
// the plan does not name, so a list that holds one is not fully resolved.
func IsOpaqueReference(address string) bool {
	rest := string(refEntry(address).withoutModules())
	root, _, _ := strings.Cut(rest, ".")

	return rest == eachValue || slices.Contains(opaqueRoots, root)
}

// refEntry is one entry of Resource.References, for example `module.net.var.extra`.
type refEntry string

// withoutModules is the entry without its leading `module.<name>.` prefixes. A module call itself (`module.<name>`)
// keeps its prefix.
func (e refEntry) withoutModules() refEntry {
	for strings.HasPrefix(string(e), moduleRoot) {
		_, rest, ok := strings.Cut(string(e[len(moduleRoot):]), ".")
		if !ok {
			return e
		}

		e = refEntry(rest)
	}

	return e
}

// sortedRefs sorts the addresses of each path and removes duplicates.
func sortedRefs(refs map[string][]string) map[string][]string {
	for path, addresses := range refs {
		slices.SortFunc(addresses, canonjson.CompareUTF16)
		refs[path] = slices.Compact(addresses)
	}

	return refs
}
