package intent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/jsonschema"
)

// errNotObject is the error of a document that is not a JSON object.
var errNotObject = errors.New("document is not a JSON object")

// patternFile is a pattern (a JSON Schema file).
type patternFile struct {
	// Source, when not nil, is the source of the diagnostics about the pattern
	// file itself (for a pattern named by the `$schema` of the architecture).
	Source *diag.Source
	// URI is the workspace-relative "/" path shown in diagnostics.
	URI string
	// AbsPath is the OS path.
	AbsPath string
}

func (p *patternFile) src() diag.Option {
	if p.Source != nil {
		return diag.WithSource(*p.Source)
	}

	return diag.WithSource(diag.Source{URI: p.URI})
}

// patternResult is the result of validatePattern.
type patternResult struct {
	// tree is the parsed pattern, or nil when it cannot be read.
	tree        any
	diagnostics []diag.Diagnostic
	// compiled is false when the pattern cannot be read or is not a valid JSON Schema.
	compiled bool
}

// compileTree compiles a parsed JSON Schema with the CALM release schemas. A
// schema without `$id` gets fallbackID.
func compileTree(tree obj, fallbackID string) (*jsonschema.Schema, error) {
	copied := make(obj, len(tree)+1)
	for k, v := range tree {
		copied[k] = v
	}

	if id, ok := copied["$id"].(string); !ok || id == "" {
		copied["$id"] = fallbackID
	}

	schemaID, _ := copied["$id"].(string)

	reg, err := newRegistry()
	if err != nil {
		return nil, err
	}

	doc, err := json.Marshal(copied)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the context.
	}

	if _, err := reg.Add(doc); err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the context.
	}

	return reg.Compile(schemaID) //nolint:wrapcheck // the caller adds the context.
}

// readPattern reads a pattern file. A read or JSON error is a CO-INT-001 without a rule.
func readPattern(pattern *patternFile) (obj, []diag.Diagnostic) {
	data, err := os.ReadFile(filepath.FromSlash(pattern.AbsPath))
	if err == nil {
		var tree obj
		if err = json.Unmarshal(data, &tree); err == nil && tree != nil {
			return tree, nil
		}

		if err == nil {
			err = errNotObject
		}
	}

	return nil, []diag.Diagnostic{diag.New(diag.INT001, diag.Detail("pattern "+pattern.URI+": "+err.Error()), pattern.src())}
}

// patternSchema compiles a parsed pattern. A pattern that refers to a CALM release
// that Concordia does not support is a CO-INT-003; a pattern that is not a valid
// JSON Schema is a CO-INT-001 with the rule json-schema.
func patternSchema(pattern *patternFile, tree obj) (*jsonschema.Schema, []diag.Diagnostic) {
	if url, found := unsupportedRelease(tree); found {
		return nil, []diag.Diagnostic{diag.New(diag.INT003, diag.Params{paramSchema: url}, pattern.src())}
	}

	// The registry needs an ID. A pattern without one gets one that is made from its URI.
	compiled, err := compileTree(tree, "urn:concordia:pattern:"+pattern.URI)
	if err != nil {
		return nil, []diag.Diagnostic{
			diag.New(diag.INT001, diag.Detail("pattern "+pattern.URI+" is not a valid JSON Schema: "+err.Error()),
				pattern.src(), diag.WithRule(ruleJSONSchema)),
		}
	}

	return compiled, nil
}

// validatePattern validates the architecture document against a pattern, as the
// CALM CLI does: the pattern rules lint the pattern, the answers of the
// architecture narrow the pattern, the architecture must match the narrowed
// pattern, and each oneOf decision needs an answer. A pattern never proves that
// the code agrees with the architecture. The architecture diagnostics have the
// source architectureURI; all pointers are below prefix.
func validatePattern(data []byte, architecture obj, pattern patternFile, architectureURI, prefix string) patternResult {
	tree, problems := readPattern(&pattern)
	if tree == nil {
		return patternResult{diagnostics: problems}
	}

	out := patternRules(tree).diagnostics(pattern.URI, prefix)
	typeError := func(err error) patternResult {
		out = append(out, diag.New(diag.INT001, diag.Detail("pattern "+pattern.URI+": the decisions cannot be applied: "+err.Error()),
			pattern.src()))

		return patternResult{diagnostics: out, tree: tree}
	}

	narrowed, err := applyArchitectureOptionsToPattern(architecture, tree)
	if err != nil {
		return typeError(err)
	}

	narrowedTree, isObj := narrowed.(obj)
	if !isObj {
		return typeError(errNotObject)
	}

	schema, problems := patternSchema(&pattern, narrowedTree)
	if schema == nil {
		return patternResult{diagnostics: append(out, problems...), tree: tree}
	}

	errs := schema.ValidateJSON(data)
	errorPaths := map[string]bool{}

	for _, e := range errs {
		errorPaths[e.InstancePath] = true
		out = append(out, diag.New(diag.INT006,
			diag.Detail(e.Message+" (pattern "+pattern.URI+")"),
			diag.WithSource(diag.Source{URI: architectureURI, Pointer: prefix + e.InstancePath}), diag.WithRule(ruleJSONSchema)))
	}

	unanswered, err := unansweredOneOfDecisions(architecture, tree)
	if err != nil {
		return typeError(err)
	}

	// The pattern may already reject an empty answer, with `minItems` on its options list.
	for _, f := range unanswered {
		pointer := pointerOf(f.path)
		if errorPaths[pointer] {
			continue
		}

		out = append(out, diag.New(f.code, f.params,
			diag.WithSource(diag.Source{URI: architectureURI, Pointer: prefix + pointer}), diag.WithRule(f.rule)))
	}

	return patternResult{diagnostics: out, tree: tree, compiled: true}
}
