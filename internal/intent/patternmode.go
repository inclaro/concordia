package intent

import (
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/workspace"
)

// ValidatePatternDocument validates a CALM pattern like `calm validate --pattern`
// without an architecture: the pattern must compile as a JSON Schema, and the
// pattern rules of the CALM CLI apply. The path is relative to ctx.Root or absolute.
func ValidatePatternDocument(file string, ctx *LoadContext) DocumentResult {
	abs, uri := documentFile(file, ctx)
	result := DocumentResult{SourceURI: uri, Resources: []workspace.ResourceRef{}}

	data, doc, problem := readDocument(abs, uri)
	if problem != nil {
		result.Diagnostics = append(result.Diagnostics, *problem)

		return result
	}

	result.Digest = digest.OfBytes(data)

	pattern := patternFile{URI: uri, AbsPath: abs}
	if _, problems := patternSchema(&pattern, doc); len(problems) > 0 {
		result.Diagnostics = append(result.Diagnostics, problems...)
	}

	result.Diagnostics = append(result.Diagnostics, patternRules(doc).diagnostics(uri, "")...)

	return result
}
