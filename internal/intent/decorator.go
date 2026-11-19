package intent

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/workspace"
)

// idParam is the diagnostic parameter of a unique-id.
const idParam = "id"

// decoratorCheck is the state of one checkDecorators call.
type decoratorCheck struct {
	l       *loader
	seen    map[string]struct{}
	ids     map[string]struct{} // decorator unique-ids
	known   map[string]struct{} // architecture unique-ids, computed once
	version string
	out     []workspace.ResourceRef
}

// decoratorFile is one decorator document and its diagnostic source.
type decoratorFile struct {
	doc  obj
	path string
	uri  string
	data []byte
}

func (f *decoratorFile) src(pointer string) diag.Option {
	return diag.WithSource(diag.Source{URI: f.uri, Pointer: pointer})
}

// checkDecorators validates the decorator documents of ctx.Decorators against the
// decorator definition of the CALM release, against their declared standard, and
// against the architecture. It gives the remote standards that it resolved.
func (l *loader) checkDecorators(version string, seen map[string]struct{}) []workspace.ResourceRef {
	check := &decoratorCheck{l: l, seen: seen, ids: map[string]struct{}{}, version: version, out: []workspace.ResourceRef{}}

	for _, file := range l.ctx.Decorators {
		check.file(file)
	}

	return check.out
}

func (c *decoratorCheck) add(f *decoratorFile, code string, params diag.Params, pointer string) {
	c.l.diagnostics = append(c.l.diagnostics, diag.New(code, params, f.src(pointer)))
}

func (c *decoratorCheck) file(file string) {
	uri, err := fsutil.ToPosixRelative(c.l.ctx.Root, file)
	if err != nil {
		uri = fsutil.ToPosix(file)
	}

	dec := &decoratorFile{path: file, uri: uri}

	var problem string
	if dec.data, dec.doc, problem = readDecorator(file); problem != "" {
		c.add(dec, diag.INT001, diag.Detail("decorator "+uri+": "+problem), "")

		return
	}

	c.schemas(dec)

	// unique-id among decorators
	if decoratorID, ok := dec.doc["unique-id"].(string); ok {
		if _, dup := c.ids[decoratorID]; dup {
			c.add(dec, diag.INT004, diag.Params{idParam: decoratorID}, "/unique-id")
		}

		c.ids[decoratorID] = struct{}{}
	}

	c.architecture(dec)
}

// schemas validates the decorator against the CALM decorator definition, then against
// its declared standard.
func (c *decoratorCheck) schemas(dec *decoratorFile) {
	errs := c.l.set.decorator[c.version].ValidateJSON(dec.data)
	for _, e := range errs {
		c.add(dec, diag.INT008, diag.Detail(e.Message), e.InstancePath)
	}

	std, _ := dec.doc["$schema"].(string)
	if len(errs) > 0 || std == "" {
		return
	}

	schema := c.l.decoratorStandard(std, dec.path, dec.src("/$schema"), c.seen, &c.out)
	if schema == nil {
		return
	}

	for _, e := range schema.ValidateJSON(dec.data) {
		c.add(dec, diag.INT008, diag.Detail(e.Message+" (standard "+std+")"), e.InstancePath)
	}
}

// architecture checks target and applies-to against the architecture of this load.
func (c *decoratorCheck) architecture(dec *decoratorFile) {
	targets := strList(dec.doc["target"])
	if len(targets) == 0 {
		return
	}

	if !c.l.targetsArchitecture(dec.path, targets) {
		c.add(dec, diag.INT010, diag.Params{"path": c.l.uri}, "/target")

		return
	}

	if c.known == nil {
		c.known = map[string]struct{}{}
		collectUniqueIDs(c.l.doc, c.known)
	}

	for i, item := range asArr(dec.doc["applies-to"]) {
		if elementID, ok := item.(string); ok {
			if _, found := c.known[elementID]; !found {
				c.add(dec, diag.INT009, diag.Params{idParam: elementID}, "/applies-to/"+strconv.Itoa(i))
			}
		}
	}
}

// readDecorator reads and decodes a decorator document. problem is "" on success.
func readDecorator(p string) (data []byte, doc obj, problem string) {
	data, err := os.ReadFile(p) //nolint:gosec // the path comes from the workspace or the command line.
	if err != nil {
		return nil, nil, err.Error()
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, nil, err.Error()
	}

	doc, ok := asObj(parsed)
	if !ok {
		return nil, nil, "document is not a JSON object"
	}

	return data, doc, ""
}

// decoratorStandard gives the compiled decorator standard std of the decorator file, or
// nil when there is no standard check or the standard cannot be used (a diagnostic
// is then added).
func (l *loader) decoratorStandard(std, file string, src diag.Option, seen map[string]struct{}, out *[]workspace.ResourceRef) *jsonschema.Schema {
	if schema, ok := l.set.standards[std]; ok {
		return schema
	}

	if strings.HasPrefix(std, calmReleasePrefix) {
		return nil
	}

	var local string

	if httpURL.MatchString(std) {
		resolved, ok := l.noteRemote(std, src, seen, out)
		if !ok {
			return nil
		}

		local = resolved
	} else {
		local = filepath.Join(filepath.Dir(file), filepath.FromSlash(path.Clean(std)))
	}

	schema, err := compileStandard(local, std)
	if err != nil {
		l.diagnostics = append(l.diagnostics, diag.New(diag.INT001, diag.Detail("decorator standard "+std+": "+err.Error()), src))

		return nil
	}

	return schema
}

// compileStandard compiles the JSON Schema file local. A schema without `$id` gets
// the standard reference std (without a fragment) as its ID.
func compileStandard(local, std string) (*jsonschema.Schema, error) {
	data, err := os.ReadFile(local) //nolint:gosec // the path comes from the decorator or the URL mapping.
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the context.
	}

	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the context.
	}

	id, _, _ := strings.Cut(std, "#")

	return compileTree(tree, id)
}

// targetsArchitecture reports whether a target item of the decorator file names the
// architecture of this load.
func (l *loader) targetsArchitecture(file string, targets []string) bool {
	self := filepath.Clean(l.abs)

	for _, item := range targets {
		if httpURL.MatchString(item) {
			if l.ctx.ArchitectureURL != "" && item == l.ctx.ArchitectureURL {
				return true
			}

			continue
		}

		target := filepath.FromSlash(item)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(file), target)
		}

		if filepath.Clean(target) == self {
			return true
		}
	}

	return false
}

// collectUniqueIDs adds each string value of the key unique-id at any depth.
func collectUniqueIDs(value any, into map[string]struct{}) {
	switch val := value.(type) {
	case []any:
		for _, item := range val {
			collectUniqueIDs(item, into)
		}
	case obj:
		for key, child := range val {
			if s, ok := child.(string); ok && key == "unique-id" {
				into[s] = struct{}{}

				continue
			}

			collectUniqueIDs(child, into)
		}
	}
}
