package plan

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// ignoredRoots are the first segments of references that name no resource: variables, locals, iteration values,
// paths, the resource itself, and workspace facts.
var ignoredRoots = []string{"var", "local", "each", "count", "path", "self", "terraform"}

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

type configResource struct {
	Expressions map[string]json.RawMessage `json:"expressions"`
	Address     string                     `json:"address"`
}

// expression is an expression of the configuration, of which only the references are read.
type expression struct {
	References []string `json:"references"`
}

// ConfigAddress is the address without its instance keys, for example `module.net.aws_security_group.web` for
// `module.net["a"].aws_security_group.web[0]`. It is the form that the configuration of a plan uses.
func ConfigAddress(address string) string {
	if !strings.Contains(address, "[") {
		return address
	}

	var out strings.Builder

	for i := 0; i < len(address); {
		if address[i] == '[' {
			i = keyEnd(address, i)

			continue
		}

		out.WriteByte(address[i])
		i++
	}

	return out.String()
}

// keyEnd is the index after the instance key that opens at start, or the end of address when the key is not
// closed. A quoted key may hold `]` and `.`.
func keyEnd(address string, start int) int {
	quoted := false

	for i := start + 1; i < len(address); i++ {
		switch {
		case quoted && address[i] == '\\':
			i++
		case address[i] == '"':
			quoted = !quoted
		case !quoted && address[i] == ']':
			return i + 1
		}
	}

	return len(address)
}

// addReferences reads the `references` lists of the configuration and attaches them to the resources whose address,
// without instance keys, is the configuration address. A missing or malformed configuration attaches nothing.
func (p *Plan) addReferences(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}

	var config configuration
	if json.Unmarshal(raw, &config) != nil {
		return
	}

	byAddress := map[string]map[string][]string{}
	collectModule(&config.RootModule, "", byAddress)

	for i := range p.Resources {
		if refs, ok := byAddress[ConfigAddress(p.Resources[i].Address)]; ok {
			p.Resources[i].References = refs
		}
	}
}

// collectModule adds the references of the resources of a module and of its module calls, by absolute address.
func collectModule(module *configModule, prefix string, out map[string]map[string][]string) {
	collectResources(module.Resources, prefix, out)

	for name, call := range module.ModuleCalls {
		if call.Module != nil {
			collectModule(call.Module, prefix+"module."+name+".", out)
		}
	}
}

// collectResources adds the references of the resources of one module, by absolute address.
func collectResources(resources []configResource, prefix string, out map[string]map[string][]string) {
	for i := range resources {
		refs := resourceRefs{prefix: prefix, byPath: map[string][]string{}}
		refs.block(resources[i].Expressions, "")

		if len(refs.byPath) > 0 {
			out[prefix+resources[i].Address] = sortedRefs(refs.byPath)
		}
	}
}

// resourceRefs collects the references of one resource. prefix is the module prefix of the resource; byPath holds
// the absolute addresses by attribute path.
type resourceRefs struct {
	byPath map[string][]string
	prefix string
}

// block adds the references of each expression of a block, by attribute path.
func (r *resourceRefs) block(exprs map[string]json.RawMessage, path string) {
	for key, raw := range exprs {
		r.expression(raw, join(path, key))
	}
}

// expression adds the references of one expression. An array of objects is a nested block; its elements have the
// path form of Resource.Unknown, for example `ingress[0].security_groups`.
func (r *resourceRefs) expression(raw json.RawMessage, path string) {
	var expr expression
	if json.Unmarshal(raw, &expr) == nil {
		r.add(expr.References, path)

		return
	}

	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}

	for i, block := range blocks {
		r.block(block, path+"["+strconv.Itoa(i)+"]")
	}
}

// add adds the resource addresses that the reference strings name.
func (r *resourceRefs) add(references []string, path string) {
	for _, ref := range references {
		if address, ok := resourceOf(ref); ok {
			r.byPath[path] = append(r.byPath[path], r.prefix+address)
		}
	}
}

// resourceOf is the address of the resource, data source or module call that a reference names, without the
// attribute after it and without instance keys. It reports false for a reference that names no resource.
func resourceOf(ref string) (string, bool) {
	parts := strings.Split(ConfigAddress(ref), ".")

	switch {
	case slices.Contains(ignoredRoots, parts[0]):
		return "", false
	case parts[0] == "data" && len(parts) >= 3:
		return strings.Join(parts[:3], "."), true
	case parts[0] != "data" && len(parts) >= 2:
		return strings.Join(parts[:2], "."), true
	}

	return "", false
}

// sortedRefs sorts the addresses of each path and removes duplicates.
func sortedRefs(refs map[string][]string) map[string][]string {
	for path, addresses := range refs {
		slices.SortFunc(addresses, canonjson.CompareUTF16)
		refs[path] = slices.Compact(addresses)
	}

	return refs
}
