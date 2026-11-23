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

	var b strings.Builder

	for i := 0; i < len(address); {
		if address[i] == '[' {
			i = keyEnd(address, i)

			continue
		}

		b.WriteByte(address[i])
		i++
	}

	return b.String()
}

// keyEnd is the index after the instance key that opens at start, or the end of s when the key is not closed. A
// quoted key may hold `]` and `.`.
func keyEnd(s string, start int) int {
	quoted := false

	for i := start + 1; i < len(s); i++ {
		switch {
		case quoted && s[i] == '\\':
			i++
		case s[i] == '"':
			quoted = !quoted
		case !quoted && s[i] == ']':
			return i + 1
		}
	}

	return len(s)
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
	for i := range module.Resources {
		res := &module.Resources[i]

		refs := map[string][]string{}
		collectBlock(res.Expressions, "", prefix, refs)

		if len(refs) > 0 {
			out[prefix+res.Address] = sortedRefs(refs)
		}
	}

	for name, call := range module.ModuleCalls {
		if call.Module != nil {
			collectModule(call.Module, prefix+"module."+name+".", out)
		}
	}
}

// collectBlock adds the references of each expression of a block to refs, by attribute path.
func collectBlock(exprs map[string]json.RawMessage, path, prefix string, refs map[string][]string) {
	for key, raw := range exprs {
		collectExpression(raw, join(path, key), prefix, refs)
	}
}

// collectExpression adds the references of one expression. An array of objects is a nested block; its elements have
// the path form of Resource.Unknown, for example `ingress[0].security_groups`.
func collectExpression(raw json.RawMessage, path, prefix string, refs map[string][]string) {
	var expr expression
	if json.Unmarshal(raw, &expr) == nil {
		addRefs(expr.References, path, prefix, refs)

		return
	}

	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}

	for i, block := range blocks {
		collectBlock(block, path+"["+strconv.Itoa(i)+"]", prefix, refs)
	}
}

// addRefs adds the resource addresses that the reference strings name.
func addRefs(references []string, path, prefix string, refs map[string][]string) {
	for _, ref := range references {
		if address, ok := resourceOf(ref); ok {
			refs[path] = append(refs[path], prefix+address)
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
