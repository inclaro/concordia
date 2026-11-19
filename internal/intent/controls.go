package intent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/jsonschema"
)

// Rule IDs of the controls rule of the CALM CLI (validate-controls.ts).
const (
	ruleControlRequirement = "control-requirement-validation"
	ruleControlLegacy      = "control-legacy-naming"
)

// calmCoreParses approximates CalmCore.fromSchema of the CALM CLI: the
// controls and node-details rules run only when the architecture has this shape.
func calmCoreParses(doc obj) bool {
	for _, key := range []string{keyNodes, keyRelationships, "flows"} {
		value, present := doc[key]
		if !present || value == nil {
			continue
		}

		list, ok := value.([]any)
		if !ok {
			return false
		}

		for _, item := range list {
			if _, isObj := item.(obj); !isObj {
				return false
			}
		}
	}

	return true
}

// calmReleaseSchema gives the parsed calm.json of a CALM release (the pattern of
// a document whose `$schema` names the release).
func calmReleaseSchema(version string) any {
	data, err := schemaFS.ReadFile("schemas/calm-" + version + "/calm.json")
	if err != nil {
		return nil
	}

	var tree any
	if json.Unmarshal(data, &tree) != nil {
		return nil
	}

	return tree
}

// controlLocation is one control of the architecture (control-iteration.ts).
type controlLocation struct {
	control obj
	pointer string // "/controls", "/nodes/<i>/controls", …
	id      string
}

func (l *loader) controlLocations() []controlLocation {
	out := appendControlLocations(nil, l.doc["controls"], "/controls")

	for _, key := range []string{keyNodes, keyRelationships, "flows"} {
		for i, item := range asArr(l.doc[key]) {
			if m, ok := item.(obj); ok {
				out = appendControlLocations(out, m["controls"], "/"+key+"/"+strconv.Itoa(i)+"/controls")
			}
		}
	}

	return out
}

// appendControlLocations adds the controls of one controls object, in key order.
func appendControlLocations(out []controlLocation, value any, pointer string) []controlLocation {
	controls, ok := value.(obj)
	if !ok {
		return out
	}

	for _, id := range sortedKeys(controls) {
		if control, isObj := controls[id].(obj); isObj {
			out = append(out, controlLocation{pointer: pointer, id: id, control: control})
		}
	}

	return out
}

// controlRules ports validate-controls.ts: each control requirement config is
// validated against its requirement schema. patternDoc resolves a requirement-url
// that starts with "#" (nil: no pattern document).
func (l *loader) controlRules(patternDoc any) {
	if text, err := json.Marshal(l.doc); err == nil && bytes.Contains(text, []byte(`"control-requirement-url"`)) {
		l.add(diag.INT015, diag.Detail("Architecture contains legacy control naming (control-requirement-url). "+
			"Migrate to requirement-url. Controls with legacy naming were not validated."), "", diag.WithRule(ruleControlLegacy))
	}

	if !calmCoreParses(l.doc) {
		return
	}

	for _, loc := range l.controlLocations() {
		l.controlRequirements(loc, patternDoc)
	}
}

// controlRequirements validates the requirement objects of one control.
func (l *loader) controlRequirements(loc controlLocation, patternDoc any) {
	for i, raw := range asArr(loc.control["requirements"]) {
		if detail, ok := raw.(obj); ok {
			l.controlRequirement(detail, loc.pointer+"/"+esc(loc.id)+"/requirements/"+strconv.Itoa(i), patternDoc)
		}
	}
}

func (l *loader) controlError(pointer, message string) {
	l.add(diag.INT014, diag.Detail(message), pointer, diag.WithRule(ruleControlRequirement))
}

// controlRequirement ports validateControlDetail.
func (l *loader) controlRequirement(detail obj, pointer string, patternDoc any) {
	url, _ := detail["requirement-url"].(string)
	if url == "" {
		return
	}

	schema, ok := l.requirementSchema(requirementRef{url: url, pointer: pointer}, patternDoc)
	if !ok {
		return
	}

	config, ok := l.requirementConfig(detail, pointer)
	if !ok || !truthy(config) {
		return
	}

	for _, e := range schema.Validate(config) {
		l.controlError(pointer+e.InstancePath, e.Message)
	}
}

// requirementConfig gives the config of a control requirement: the document
// behind config-url, or the inline config. It is false where config-url does not
// load.
func (l *loader) requirementConfig(detail obj, pointer string) (any, bool) {
	ref, hasURL := detail["config-url"]
	if !hasURL {
		return detail["config"], true
	}

	refText := jsString(ref)

	value, err := l.loadReference(refText, pointer+"/config-url")
	if err != nil {
		if !errors.Is(err, errUnresolved) {
			l.controlError(pointer, fmt.Sprintf("Could not load config from '%s': %s", refText, err))
		}

		return nil, false
	}

	return value, true
}

// requirementRef locates the requirement-url of a control requirement: the URL
// and the JSON Pointer of the requirement in the architecture.
type requirementRef struct {
	url     string
	pointer string
}

// requirementSchema finds and compiles the requirement schema of a control. As in
// the CALM CLI, the schema is a JSON Pointer into the pattern document ("#…"), a
// built-in CALM release schema, or an http(s) URL; another reference does not load.
func (l *loader) requirementSchema(ref requirementRef, patternDoc any) (*jsonschema.Schema, bool) {
	var (
		tree    any
		success bool
	)

	switch {
	case strings.HasPrefix(ref.url, "#"):
		tree, success = l.requirementPointerTree(ref, patternDoc)
	case strings.HasPrefix(ref.url, calmReleasePrefix):
		return l.requirementReleaseSchema(ref)
	case !httpURL.MatchString(ref.url):
		l.controlError(ref.pointer, fmt.Sprintf("Could not load requirement schema from '%s': Not a valid absolute URL: %s", ref.url, ref.url))
	default:
		tree, success = l.requirementRemoteTree(ref)
	}

	if !success {
		return nil, false
	}

	return l.compileRequirementTree(ref, tree)
}

// requirementPointerTree resolves a requirement-url JSON Pointer in the pattern document.
func (l *loader) requirementPointerTree(ref requirementRef, patternDoc any) (any, bool) {
	if patternDoc == nil {
		l.controlError(ref.pointer, fmt.Sprintf("requirement-url '%s' is a JSON pointer but no pattern document is available", ref.url))

		return nil, false
	}

	value, ok := jsonPointerGet(patternDoc, ref.url[1:])
	if !ok {
		l.controlError(ref.pointer, fmt.Sprintf("Could not resolve requirement-url '%s' as a JSON pointer in the pattern document", ref.url))

		return nil, false
	}

	return value, true
}

// requirementReleaseSchema compiles the built-in schema of a CALM release.
func (l *loader) requirementReleaseSchema(ref requirementRef) (*jsonschema.Schema, bool) {
	compiled, err := compileByID(ref.url)
	if err != nil {
		l.controlError(ref.pointer, fmt.Sprintf("Could not load requirement schema from '%s': %s", ref.url, err))

		return nil, false
	}

	return compiled, true
}

// requirementRemoteTree reads the requirement schema of an http(s) URL.
func (l *loader) requirementRemoteTree(ref requirementRef) (any, bool) {
	value, err := l.loadReference(ref.url, ref.pointer+"/requirement-url")
	if err != nil {
		if !errors.Is(err, errUnresolved) {
			l.controlError(ref.pointer, fmt.Sprintf("Could not load requirement schema from '%s': %s", ref.url, err))
		}

		return nil, false
	}

	if !truthy(value) {
		l.controlError(ref.pointer, fmt.Sprintf("Requirement schema not found: '%s'", ref.url))

		return nil, false
	}

	return value, true
}

// compileRequirementTree compiles a requirement schema tree.
func (l *loader) compileRequirementTree(ref requirementRef, tree any) (*jsonschema.Schema, bool) {
	schemaTree, isObj := tree.(obj)
	if !isObj {
		l.controlError(ref.pointer, fmt.Sprintf("Could not compile requirement schema from '%s': schema must be an object", ref.url))

		return nil, false
	}

	compiled, err := compileTree(schemaTree, "urn:concordia:requirement:"+neturl.PathEscape(l.uri+l.prefix+ref.pointer))
	if err != nil {
		l.controlError(ref.pointer, fmt.Sprintf("Could not compile requirement schema from '%s': %s", ref.url, err))

		return nil, false
	}

	return compiled, true
}

// compileByID compiles a built-in schema by its ID.
func compileByID(id string) (*jsonschema.Schema, error) {
	reg, err := newRegistry()
	if err != nil {
		return nil, err
	}

	return reg.Compile(id) //nolint:wrapcheck // the caller adds the URL.
}

// errUnresolved is the error of a remote reference that did not resolve; the
// diagnostic (CO-INT-012 or CO-INT-013) is already added.
var errUnresolved = errors.New("remote reference not resolved")

// loadReference reads the JSON document of a reference in the document.
func (l *loader) loadReference(ref, pointer string) (any, error) {
	file, ok := l.localFile(ref, pointer, nil, nil)
	if !ok {
		return nil, errUnresolved
	}

	return readJSONFile(file)
}

// jsonPointerGet resolves a JSON Pointer ("" is the whole document) as the
// json-pointer package does.
func jsonPointerGet(doc any, pointer string) (any, bool) {
	if pointer == "" {
		return doc, true
	}

	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}

	value := doc

	for _, raw := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")

		next, ok := jsonPointerStep(value, token)
		if !ok {
			return nil, false
		}

		value = next
	}

	return value, true
}

// jsonPointerStep follows one unescaped reference token into an object or array.
func jsonPointerStep(value any, token string) (any, bool) {
	switch typed := value.(type) {
	case obj:
		next, ok := typed[token]

		return next, ok
	case []any:
		return jsonPointerIndex(typed, token)
	default:
		return nil, false
	}
}

func jsonPointerIndex(list []any, token string) (any, bool) {
	i, err := strconv.Atoi(token)
	if err != nil {
		return nil, false
	}

	if i < 0 || i >= len(list) {
		return nil, false
	}

	return list[i], true
}
