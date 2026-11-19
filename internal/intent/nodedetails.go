package intent

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// ruleNodeDetails is the rule ID of the node-details rule of the CALM CLI.
const ruleNodeDetails = "node-details-validation"

// nodeDetails ports validate-node-details.ts: each `details.detailed-architecture`
// of a node is validated again with the CALM CLI rules, below the pointer of the
// reference. A reference is validated once per load; a reference that could not
// be read is reported at each node that names it.
func (l *loader) nodeDetails() {
	if !calmCoreParses(l.doc) {
		return
	}

	for i, raw := range asArr(l.doc["nodes"]) {
		node, _ := raw.(obj)
		details, _ := node["details"].(obj)

		ref, _ := details["detailed-architecture"].(string)
		if ref == "" {
			continue
		}

		if read, seen := l.visited[ref]; seen && read {
			continue
		}

		pointer := "/nodes/" + strconv.Itoa(i) + "/details/detailed-architecture"

		// As in the CALM CLI, a relative reference is relative to the working
		// directory: the workspace root.
		file, ok := l.rootFile(ref, pointer)
		if !ok {
			l.visited[ref] = false

			continue
		}

		data, sub, err := readObjectFile(file)
		if err != nil {
			l.visited[ref] = false
			l.add(diag.INT014, diag.Detail(fmt.Sprintf("Could not load detailed-architecture '%s': %s", ref, err)), pointer,
				diag.WithRule(ruleNodeDetails))

			continue
		}

		l.visited[ref] = true

		child := &loader{
			ctx: l.ctx, abs: file, uri: l.uri, doc: sub, set: l.set, remote: l.remote,
			prefix: l.prefix + pointer, visited: l.visited,
		}

		version, pattern := child.detailPattern(details)

		var patterns []patternFile
		if pattern != nil {
			patterns = append(patterns, *pattern)
		}

		child.calmRules(data, version, patterns, pattern != nil)
		l.diagnostics = append(l.diagnostics, child.diagnostics...)
	}
}

// detailPattern finds the pattern of a detailed architecture: the
// `details.required-pattern` of the node, else the `$schema` of the document.
// A CALM release (or no `$schema`) gives the release and no pattern. As in the CALM
// CLI, only an http(s) pattern loads; a pattern that cannot be read is skipped
// without a diagnostic.
func (l *loader) detailPattern(details obj) (string, *patternFile) {
	refs := []string{}
	if required, ok := details["required-pattern"].(string); ok && required != "" {
		refs = append(refs, required)
	}

	if declared, ok := l.doc[keySchema].(string); ok && declared != "" {
		refs = append(refs, declared)
	}

	for _, ref := range refs {
		if isCalmSchemaURL(ref) {
			if version, ok := SchemaVersionOf(obj{keySchema: ref}); ok {
				return version, nil
			}

			continue
		}

		if !httpURL.MatchString(ref) {
			continue
		}

		file, ok := l.remoteFileQuiet(ref)
		if !ok {
			continue
		}

		_, tree, err := readObjectFile(file)
		if err != nil {
			continue
		}

		return PatternRelease(tree), &patternFile{URI: ref, AbsPath: file}
	}

	return workspace.DefaultCalmSchemaVersion, nil
}

// rootFile is localFile with a relative path from the workspace root.
func (l *loader) rootFile(ref, pointer string) (string, bool) {
	if httpURL.MatchString(ref) || filepath.IsAbs(ref) {
		return l.localFile(ref, pointer, nil, nil)
	}

	return filepath.Join(l.ctx.Root, filepath.FromSlash(path.Clean(ref))), true
}

// remoteFileQuiet resolves a remote URL like resolveRemote, without a diagnostic.
func (l *loader) remoteFileQuiet(ref string) (string, bool) {
	if p, ok := l.mapped(ref); ok {
		return p, true
	}

	if l.ctx.Remote == nil {
		return "", false
	}

	base, _, _ := strings.Cut(ref, "#")
	p, _, _ := l.ctx.Remote(base)

	return p, p != ""
}

// readObjectFile reads a JSON file whose value is an object.
func readObjectFile(file string) (data []byte, tree obj, err error) {
	value, err := readJSONFile(file)
	if err != nil {
		return nil, nil, err
	}

	tree, ok := value.(obj)
	if !ok {
		return nil, nil, errNotObject
	}

	data, err = json.Marshal(tree)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // a parsed document always encodes.
	}

	return data, tree, nil
}
