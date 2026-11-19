package intent

import (
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// schemaOf gives the CALM release of the document. A `$schema` that is not a
// CALM release names a pattern (a path relative to the document, or an http(s)
// URL), as in the CALM CLI: the pattern takes the place of the release schema and
// its release is PatternRelease. ok is false when the load cannot go on (the
// diagnostic is added).
func (l *loader) schemaOf(seen map[string]struct{}, resources *[]workspace.ResourceRef) (string, *patternFile, bool) {
	value, isStr := l.doc["$schema"].(string)
	if !isStr || isCalmSchemaURL(value) {
		version, ok := SchemaVersionOf(l.doc)
		if !ok {
			l.add(diag.INT003, diag.Params{paramSchema: declaredSchemaText(l.doc["$schema"])}, "/$schema")
		}

		return version, nil, ok
	}

	file, ok := l.localFile(value, "/$schema", seen, resources)
	if !ok {
		return "", nil, false
	}

	tree, err := readJSONFile(file)
	if err != nil {
		l.add(diag.INT001, diag.Detail("pattern "+value+": "+err.Error()), "/$schema")

		return "", nil, false
	}

	pattern, _ := tree.(obj)

	return PatternRelease(pattern), &patternFile{
		URI: l.patternURI(value, file), AbsPath: file, Source: &diag.Source{URI: l.uri, Pointer: l.prefix + "/$schema"},
	}, true
}

// patternURI gives the URI of a pattern: the URL itself, or the path relative to the workspace root.
func (l *loader) patternURI(value, file string) string {
	if httpURL.MatchString(value) {
		return value
	}

	if rel, err := fsutil.ToPosixRelative(l.ctx.Root, file); err == nil {
		return rel
	}

	return value
}

// calmRules runs the rules of the CALM CLI validation engine on the document of
// the loader: the JSON Schema (the CALM release schema, or the patterns), the
// pattern rules, the architecture rules, the controls, and the node details. A
// pattern that does not compile stops the controls and the node details, as in
// the CALM CLI. selfPattern says that patterns[0] comes from `$schema`.
func (l *loader) calmRules(data []byte, version string, patterns []patternFile, selfPattern bool) {
	if !selfPattern {
		for _, e := range l.set.calm[version].ValidateJSON(data) {
			l.add(diag.INT002, diag.Detail(e.Message), e.InstancePath, diag.WithRule(ruleJSONSchema))
		}
	}

	patternDoc, compiled := l.validatePatterns(data, patterns)

	l.diagnostics = append(l.diagnostics, architectureRules(l.doc).diagnostics(l.uri, l.prefix)...)

	if !compiled {
		return
	}

	if patternDoc == nil {
		if _, declared := l.doc["$schema"]; declared {
			patternDoc = calmReleaseSchema(version)
		}
	}

	l.controlRules(patternDoc)
	l.nodeDetails()
}

// validatePatterns validates the document against each pattern. It gives the tree of the first pattern and
// whether every pattern compiled.
func (l *loader) validatePatterns(data []byte, patterns []patternFile) (any, bool) {
	compiled := true

	var patternDoc any

	for i, p := range patterns {
		result := validatePattern(data, l.doc, p, l.uri, l.prefix)
		l.diagnostics = append(l.diagnostics, result.diagnostics...)
		compiled = compiled && result.compiled

		if i == 0 {
			patternDoc = result.tree
		}
	}

	return patternDoc, compiled
}
