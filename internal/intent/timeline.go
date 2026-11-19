package intent

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// CalmTimelineID is the ID of the CALM timeline schema of a release version.
func CalmTimelineID(version string) string {
	return calmReleasePrefix + version + "/meta/calm-timeline.json"
}

// DocumentResult is the result of a pattern or timeline validation.
type DocumentResult struct {
	SourceURI   string
	Digest      string
	Diagnostics []diag.Diagnostic
	Resources   []workspace.ResourceRef
}

// documentFile gives the absolute path and the workspace-relative URI of a file.
func documentFile(file string, ctx *LoadContext) (abs, uri string) {
	abs = file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(ctx.Root, filepath.FromSlash(file))
	}

	abs = filepath.Clean(abs)

	uri, err := fsutil.ToPosixRelative(ctx.Root, abs)
	if err != nil {
		uri = fsutil.ToPosix(abs)
	}

	return abs, uri
}

// readDocument reads a JSON object document. A read or JSON error, or a value
// that is not an object, is a CO-INT-001 without a rule.
func readDocument(abs, uri string) (data []byte, doc obj, problem *diag.Diagnostic) {
	src := diag.WithSource(diag.Source{URI: uri})

	data, err := os.ReadFile(abs) //nolint:gosec // the path comes from the workspace or the command line.
	if err != nil {
		d := diag.New(diag.INT001, diag.Detail(err.Error()), src)

		return nil, nil, &d
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		d := diag.New(diag.INT001, diag.Detail(err.Error()), src)

		return nil, nil, &d
	}

	doc, ok := parsed.(obj)
	if !ok {
		d := diag.New(diag.INT001, diag.Detail(errNotObject.Error()), src)

		return nil, nil, &d
	}

	return data, doc, nil
}

// ValidateTimeline validates a CALM timeline document like `calm validate
// --timeline`: the CALM timeline schema of its `$schema` release and the
// timeline rules of the CALM CLI. The path is relative to ctx.Root or absolute.
func ValidateTimeline(file string, ctx *LoadContext) DocumentResult {
	abs, uri := documentFile(file, ctx)
	result := DocumentResult{SourceURI: uri, Resources: []workspace.ResourceRef{}}

	data, doc, problem := readDocument(abs, uri)
	if problem != nil {
		result.Diagnostics = []diag.Diagnostic{*problem}

		return result
	}

	result.Digest = digest.OfBytes(data)
	schemaSrc := diag.WithSource(diag.Source{URI: uri, Pointer: "/$schema"})

	declared, present := doc["$schema"]
	if !present {
		result.Diagnostics = []diag.Diagnostic{diag.New(diag.INT003, diag.Params{paramSchema: "(missing $schema)"}, schemaSrc)}

		return result
	}

	version := ""

	for _, v := range workspace.CalmSchemaVersions {
		if declared == CalmTimelineID(v) {
			version = v
		}
	}

	if version == "" {
		result.Diagnostics = []diag.Diagnostic{diag.New(diag.INT003, diag.Params{paramSchema: declaredSchemaText(declared)}, schemaSrc)}

		return result
	}

	set, err := schemas()
	if err != nil {
		result.Diagnostics = []diag.Diagnostic{diag.New(diag.INT001, diag.Detail(err.Error()), diag.WithSource(diag.Source{URI: uri}))}

		return result
	}

	for _, e := range set.timeline[version].ValidateJSON(data) {
		result.Diagnostics = append(result.Diagnostics, diag.New(diag.INT002, diag.Detail(e.Message),
			diag.WithSource(diag.Source{URI: uri, Pointer: e.InstancePath}), diag.WithRule(ruleJSONSchema)))
	}

	result.Diagnostics = append(result.Diagnostics, timelineRules(doc).diagnostics(uri, "")...)

	return result
}
