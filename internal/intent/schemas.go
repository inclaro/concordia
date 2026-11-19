package intent

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/workspace"
)

//go:embed schemas
var schemaFS embed.FS

// Schema IDs.
const (
	// ExtSchemaID is the ID of the Concordia extension schema.
	ExtSchemaID = "https://concordia.io/schemas/extensions.schema.json"
)

const (
	extFile           = "schemas/concordia-extensions.schema.json"
	extPrefix         = ExtSchemaID + "#/$defs/"
	calmReleasePrefix = "https://calm.finos.org/release/"
	calmDraftPrefix   = "https://calm.finos.org/draft/"
	standardsDir      = "schemas/standards/deployment-1.0"
)

// CalmSchemaID is the ID of the CALM schema of a release version.
func CalmSchemaID(version string) string {
	return calmReleasePrefix + version + "/meta/calm.json"
}

// CalmDecoratorID is the ID of the decorator definition of a CALM release.
func CalmDecoratorID(version string) string {
	return calmReleasePrefix + version + "/meta/decorators.json#/defs/decorator"
}

// standardIDs are the IDs of the built-in decorator standards.
var standardIDs = []string{
	"https://calm.finos.org/standards/deployment/1.0/deployment.decorator.standard.json",
	"https://calm.finos.org/standards/deployment/1.0/kubernetes.decorator.standard.json",
}

// SchemaVersionOf gives the CALM release that the `$schema` of a document names.
// A document without `$schema` is workspace.DefaultCalmSchemaVersion. ok is false
// when `$schema` names no supported release.
func SchemaVersionOf(doc map[string]any) (version string, ok bool) {
	declared, present := doc["$schema"]
	if !present {
		return workspace.DefaultCalmSchemaVersion, true
	}

	for _, v := range workspace.CalmSchemaVersions {
		if declared == CalmSchemaID(v) {
			return v, true
		}
	}

	return "", false
}

// ReadSchemaVersion reads the CALM document at path and gives its release.
// A read or JSON error, or a document that is not an object, is a CO-INT-001
// *diag.Error. An unsupported CALM release `$schema` is a CO-INT-003 *diag.Error.
// A `$schema` that names a pattern gives the PatternRelease of a local pattern,
// and workspace.DefaultCalmSchemaVersion for a remote pattern or a pattern that
// cannot be read (the loader reports those problems). uri is the diagnostic source.
func ReadSchemaVersion(path, uri string) (string, error) {
	doc, err := readSchemaDocument(path, uri)
	if err != nil {
		return "", err
	}

	if declared, ok := doc["$schema"].(string); ok && !isCalmSchemaURL(declared) {
		return declaredPatternRelease(path, declared), nil
	}

	version, ok := SchemaVersionOf(doc)
	if !ok {
		return "", diag.Fail(diag.INT003, diag.Params{paramSchema: declaredSchemaText(doc["$schema"])},
			diag.WithSource(diag.Source{URI: uri, Pointer: "/$schema"}))
	}

	return version, nil
}

// readSchemaDocument reads the JSON object at path. Any failure is a CO-INT-001
// *diag.Error with uri as the diagnostic source.
func readSchemaDocument(path, uri string) (map[string]any, error) {
	failure := func(detail string) error {
		return diag.Fail(diag.INT001, diag.Detail(detail), diag.WithSource(diag.Source{URI: uri}))
	}

	data, err := os.ReadFile(path) //nolint:gosec // the path comes from the workspace or the command line.
	if err != nil {
		return nil, failure(err.Error())
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, failure(err.Error())
	}

	doc, isObj := parsed.(map[string]any)
	if !isObj {
		return nil, failure("document is not a JSON object")
	}

	return doc, nil
}

// declaredPatternRelease gives the release of the pattern that a document at
// path names in `$schema`. A remote pattern or a pattern that cannot be read
// gives workspace.DefaultCalmSchemaVersion.
func declaredPatternRelease(path, declared string) string {
	if httpURL.MatchString(declared) {
		return workspace.DefaultCalmSchemaVersion
	}

	file := declared
	if !filepath.IsAbs(file) {
		file = filepath.Join(filepath.Dir(path), filepath.FromSlash(pathpkg.Clean(declared)))
	}

	_, pattern, err := readObjectFile(file)
	if err != nil {
		return workspace.DefaultCalmSchemaVersion
	}

	return PatternRelease(pattern)
}

// PatternRelease gives the CALM release that a pattern builds on: the release of
// the first `$ref` or `$schema` string (keys in sorted order, depth first) of the
// form https://calm.finos.org/release/<r>/meta/… with a supported <r>. A pattern
// without one gives workspace.DefaultCalmSchemaVersion.
func PatternRelease(pattern map[string]any) string {
	if r, ok := firstSchemaRef(pattern, releaseOfURL); ok {
		return r
	}

	return workspace.DefaultCalmSchemaVersion
}

// firstSchemaRef gives the first result of pick on a `$ref` or `$schema` string
// of a document (keys in sorted order, depth first).
func firstSchemaRef(value any, pick func(string) (string, bool)) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return firstSchemaRefInMap(typed, pick)
	case []any:
		for _, item := range typed {
			if r, ok := firstSchemaRef(item, pick); ok {
				return r, true
			}
		}
	}

	return "", false
}

func firstSchemaRefInMap(tree map[string]any, pick func(string) (string, bool)) (string, bool) {
	for _, key := range sortedKeys(tree) {
		if text, isRef := schemaRefText(key, tree[key]); isRef {
			if r, ok := pick(text); ok {
				return r, true
			}
		}

		if r, ok := firstSchemaRef(tree[key], pick); ok {
			return r, true
		}
	}

	return "", false
}

// schemaRefText gives the string of a `$ref` or `$schema` member.
func schemaRefText(key string, value any) (string, bool) {
	text, isStr := value.(string)

	return text, isStr && (key == keyRef || key == keySchema)
}

// releaseOfURL gives the supported CALM release of a URL https://calm.finos.org/release/<r>/meta/….
func releaseOfURL(url string) (string, bool) {
	rest, ok := strings.CutPrefix(url, calmReleasePrefix)
	if !ok {
		return "", false
	}

	release, tail, ok := strings.Cut(rest, "/")
	if !ok || !strings.HasPrefix(tail, "meta/") {
		return "", false
	}

	if !slices.Contains(workspace.CalmSchemaVersions, release) {
		return "", false
	}

	return release, true
}

// isCalmSchemaURL says whether a URL names a schema of a CALM release or draft
// (and not a pattern).
func isCalmSchemaURL(url string) bool {
	return strings.HasPrefix(url, calmReleasePrefix) || strings.HasPrefix(url, calmDraftPrefix)
}

// declaredSchemaText gives a `$schema` value as text for CO-INT-003.
func declaredSchemaText(v any) string {
	if text, ok := v.(string); ok {
		return text
	}

	return canonjson.MustString(v)
}

// unsupportedRelease finds the first `$ref` or `$schema` string of a document
// (keys in sorted order, depth first) that names a CALM release schema that
// Concordia does not support.
func unsupportedRelease(value any) (string, bool) {
	return firstSchemaRef(value, unsupportedReleaseURL)
}

// unsupportedReleaseURL gives the URL when it names a CALM release schema that
// Concordia does not support.
func unsupportedReleaseURL(url string) (string, bool) {
	if !isCalmSchemaURL(url) {
		return "", false
	}

	if _, ok := releaseOfURL(url); ok {
		return "", false
	}

	return url, true
}

// newRegistry makes a registry with the CALM schemas and the extension schema.
func newRegistry() (*jsonschema.Registry, error) {
	reg := jsonschema.NewRegistry()
	for _, v := range workspace.CalmSchemaVersions {
		if err := reg.AddFS(schemaFS, "schemas/calm-"+v); err != nil {
			return nil, fmt.Errorf("load CALM %s schemas: %w", v, err)
		}

		// A pattern may use calm.json as its `$schema`; the metaschema requires the
		// CALM core vocabulary, which the CALM CLI (AJV) does not assert either.
		reg.AllowVocabulary(calmReleasePrefix + v + "/meta/core.json")
	}

	if err := reg.AddFS(schemaFS, standardsDir); err != nil {
		return nil, fmt.Errorf("load decorator standards: %w", err)
	}

	doc, err := schemaFS.ReadFile(extFile)
	if err != nil {
		return nil, fmt.Errorf("read extension schema: %w", err)
	}

	if _, err := reg.Add(doc); err != nil {
		return nil, fmt.Errorf("load extension schema: %w", err)
	}

	return reg, nil
}

type schemaSet struct {
	calm                        map[string]*jsonschema.Schema
	decorator                   map[string]*jsonschema.Schema // key: CALM release
	timeline                    map[string]*jsonschema.Schema // key: CALM release
	standards                   map[string]*jsonschema.Schema // key: $id
	node, relationship, control *jsonschema.Schema
}

var schemas = sync.OnceValues(func() (*schemaSet, error) {
	reg, err := newRegistry()
	if err != nil {
		return nil, err
	}

	set := &schemaSet{
		calm:      map[string]*jsonschema.Schema{},
		decorator: map[string]*jsonschema.Schema{},
		timeline:  map[string]*jsonschema.Schema{},
		standards: map[string]*jsonschema.Schema{},
	}

	for _, item := range []struct {
		dst **jsonschema.Schema
		id  string
	}{
		{&set.node, extPrefix + "node"},
		{&set.relationship, extPrefix + "relationship"},
		{&set.control, extPrefix + "controlConfig"},
	} {
		compiled, err := reg.Compile(item.id)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", item.id, err)
		}

		*item.dst = compiled
	}

	for _, release := range workspace.CalmSchemaVersions {
		compiled, err := reg.Compile(CalmSchemaID(release))
		if err != nil {
			return nil, fmt.Errorf("compile CALM %s: %w", release, err)
		}

		set.calm[release] = compiled

		decorator, err := reg.Compile(CalmDecoratorID(release))
		if err != nil {
			return nil, fmt.Errorf("compile CALM %s decorator: %w", release, err)
		}

		set.decorator[release] = decorator

		timeline, err := reg.Compile(CalmTimelineID(release))
		if err != nil {
			return nil, fmt.Errorf("compile CALM %s timeline: %w", release, err)
		}

		set.timeline[release] = timeline
	}

	for _, id := range standardIDs {
		compiled, err := reg.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("compile decorator standard %s: %w", id, err)
		}

		set.standards[id] = compiled
	}

	return set, nil
})
