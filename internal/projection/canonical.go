package projection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Canonicalization rule names, as they appear in the view manifest.
const (
	// CanonicalizationJSON is the rule for the `json` export: the renderer
	// metadata of ExportMetadataRule is removed and the JSON is written with
	// canonjson (sorted keys, 2-space indent, final newline).
	CanonicalizationJSON = "structurizr-json/1"
	// CanonicalizationText is the rule for the `mermaid`, `plantuml`, and `dot`
	// exports: line endings are LF. The pinned CLI writes no other renderer data.
	CanonicalizationText = "structurizr-text/1"
)

// ErrNoCanonicalization is given (wrapped) for a format that has no canonicalization rule.
var ErrNoCanonicalization = errors.New("no canonicalization rule for the export format")

// MetadataRule lists the renderer metadata of the `json` export. The lists come
// from an export of the pinned CLI (testdata/exports/workspace.json); the test
// in canonical_test.go fails when a recorded export has a metadata key that the
// rule does not list, so a new pin cannot change the committed files unseen.
type MetadataRule struct {
	// WorkspaceKeys are top-level keys that the rule removes.
	WorkspaceKeys []string
	// PropertyKeys are keys of the top-level `properties` object that the rule removes.
	PropertyKeys []string
	// PropertyPrefixes remove every key of the top-level `properties` object that starts with one of them.
	PropertyPrefixes []string
	// ContentKeys are top-level keys that carry model content and are kept.
	ContentKeys []string
}

// ExportMetadataRule is the rule for the `json` export of the pinned CLI.
//
// Written by the pinned CLI in the recording: `id` (the workspace identifier,
// always 0), the property `structurizr.dsl` (the DSL source in base64), and the
// properties `structurizr.inspection.*` (counts of the CLI's own inspections).
// Listed because the Structurizr JSON format has them and a workspace that was
// saved by a tool writes them: `lastModifiedDate`, `lastModifiedAgent`,
// `lastModifiedUser`, and `revision`, which hold the generation time and the
// generating program.
var ExportMetadataRule = MetadataRule{
	WorkspaceKeys:    strings.Fields("id lastModifiedAgent lastModifiedDate lastModifiedUser revision"),
	PropertyKeys:     []string{"structurizr.dsl"},
	PropertyPrefixes: []string{"structurizr.inspection."},
	ContentKeys: []string{
		"configuration", "description", "documentation", "model", "name", "properties", "views",
	},
}

// CanonicalizationFor gives the name of the rule for an export format, and
// false when the format has none (`static`).
func CanonicalizationFor(format ExportFormat) (string, bool) {
	switch format {
	case FormatJSON:
		return CanonicalizationJSON, true
	case FormatMermaid, FormatPlantUML, FormatDot:
		return CanonicalizationText, true
	case FormatStatic:
		return "", false
	}

	return "", false
}

// CanonicalizeExport gives the canonical bytes of an export file: the bytes that
// are committed and that digests are computed over. The `json` export loses its
// renderer metadata (ExportMetadataRule); the text formats get LF line endings.
// A format without a rule gives ErrNoCanonicalization.
func CanonicalizeExport(format ExportFormat, data []byte) ([]byte, error) {
	switch format {
	case FormatJSON:
		return canonicalizeJSON(data)
	case FormatMermaid, FormatPlantUML, FormatDot:
		return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), nil
	case FormatStatic:
	}

	return nil, fmt.Errorf("%w: %s", ErrNoCanonicalization, format)
}

func canonicalizeJSON(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var workspace map[string]any
	if err := dec.Decode(&workspace); err != nil {
		return nil, fmt.Errorf("decode the json export: %w", err)
	}

	rule := &ExportMetadataRule

	for _, key := range rule.WorkspaceKeys {
		delete(workspace, key)
	}

	if props, ok := workspace["properties"].(map[string]any); ok {
		for key := range props {
			if rule.removesProperty(key) {
				delete(props, key)
			}
		}

		if len(props) == 0 {
			delete(workspace, "properties")
		}
	}

	out, err := canonjson.Marshal(workspace)
	if err != nil {
		return nil, fmt.Errorf("write the canonical json export: %w", err)
	}

	return out, nil
}

func (r *MetadataRule) removesProperty(key string) bool {
	return slices.Contains(r.PropertyKeys, key) ||
		slices.ContainsFunc(r.PropertyPrefixes, func(prefix string) bool { return strings.HasPrefix(key, prefix) })
}
