package intent

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

// LoadContext is the input of LoadArchitecture.
type LoadContext struct {
	// Root is the absolute workspace root. Diagnostic paths are relative to it.
	Root string
	// URLMapping maps a URL to an absolute local path (see LoadURLMapping).
	URLMapping map[string]string
	// Remote resolves an https URL that has no URLMapping entry to a local file. nil means "no remote resolution".
	// On failure it gives "" and a diagnostic code (CO-INT-012 or CO-INT-013) and a detail.
	Remote func(uri string) (path, code, detail string)
	// ArchitectureURL is the https URL of the architecture when it comes from a remote
	// architecture.source, else "". A decorator target can name this URL.
	ArchitectureURL string
	// Patterns are the absolute OS paths of pattern files (JSON Schemas).
	Patterns []string
	// Decorators are the absolute OS paths of decorator documents.
	Decorators []string
	// Trust is the workspace trust vocabulary (`trust.dimensions`, `trust.classifications`) that the
	// `concordia.trust` declarations of nodes and relationships are validated against. The zero value
	// accepts the built-in dimensions and no classification.
	Trust trust.Vocabulary
}

// LoadResult is the result of LoadArchitecture.
type LoadResult struct {
	// Model is nil when the document cannot be read or its schema version is not supported.
	Model       *ArchitectureModel
	Diagnostics []diag.Diagnostic
	// Resources are the url-mapped remote resources that the document uses (input to workspace.VerifyLock).
	Resources []workspace.ResourceRef
}

// ExitCodeForDiagnostics gives the exit code that the diagnostics imply: 6
// (network) for a lock or remote error, 2 for other errors, else 0.
func ExitCodeForDiagnostics(list []diag.Diagnostic) int {
	code := diag.OK

	for i := range list {
		if list[i].Severity != diag.SeverityError {
			continue
		}

		if list[i].Code == diag.INT012 || list[i].Code == diag.INT013 {
			return diag.Network
		}

		code = diag.InvalidInput
	}

	return code
}

// loader is the state of one LoadArchitecture call, or of one detailed
// architecture below it (see nodeDetails).
type loader struct {
	ctx    LoadContext
	abs    string
	uri    string
	doc    obj
	set    *schemaSet
	remote map[string]remoteResult
	// visited holds the detailed-architecture references of the load: true when the
	// document was read, false when it could not be read. Shared by all loaders of a load.
	visited map[string]bool
	// prefix is the pointer of a detailed architecture in the root document ("" for the root).
	// Each diagnostic pointer starts with it.
	prefix      string
	diagnostics []diag.Diagnostic
	nodes       []NodeDeclaration
	rels        []RelationshipDeclaration
	flows       []FlowDeclaration
	controls    []ControlAttachment
	pending     []pendingControl
}

type remoteResult struct {
	path string
	ok   bool
}

func (l *loader) src(pointer string) diag.Option {
	return diag.WithSource(diag.Source{URI: l.uri, Pointer: l.prefix + pointer})
}

func (l *loader) add(code string, params diag.Params, pointer string, opts ...diag.Option) {
	l.diagnostics = append(l.diagnostics, diag.New(code, params, append([]diag.Option{l.src(pointer)}, opts...)...))
}

// extErrors validates an extension value. It adds CO-INT-007 for each error.
func (l *loader) extErrors(schema *jsonschema.Schema, value any, base string) bool {
	errs := schema.Validate(value)
	for _, e := range errs {
		l.add(diag.INT007, diag.Detail(e.Message), base+e.InstancePath)
	}

	return len(errs) == 0
}

// LoadArchitecture loads a CALM 1.2 or 1.3 document. Problems are diagnostics; the
// model is built from whatever structure is usable. The path is relative to
// ctx.Root or absolute (an OS path).
func LoadArchitecture(file string, ctx LoadContext) LoadResult { //nolint:gocritic // LoadArchitecture is public API and takes the context by value.
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(ctx.Root, filepath.FromSlash(file))
	}

	abs = filepath.Clean(abs)

	load := &loader{ctx: ctx, abs: abs, uri: posixRelativeOrAbs(ctx.Root, abs), remote: map[string]remoteResult{}, visited: map[string]bool{}}

	return load.run()
}

// posixRelativeOrAbs gives abs relative to root as a slash path, or abs as a slash path when it is not below root.
func posixRelativeOrAbs(root, abs string) string {
	rel, err := fsutil.ToPosixRelative(root, abs)
	if err != nil {
		return fsutil.ToPosix(abs)
	}

	return rel
}

func (l *loader) fail(detail string) LoadResult {
	l.diagnostics = append(l.diagnostics, diag.New(diag.INT001, diag.Detail(detail), diag.WithSource(diag.Source{URI: l.uri})))

	return LoadResult{Diagnostics: l.diagnostics}
}

func (l *loader) run() LoadResult {
	data, detail, ok := l.loadDocument()
	if !ok {
		return l.fail(detail)
	}

	seen := map[string]struct{}{}
	resources := []workspace.ResourceRef{}

	// schema version: a CALM release, or a pattern that the architecture follows
	version, self, ok := l.schemaOf(seen, &resources)
	if !ok {
		return LoadResult{Diagnostics: l.diagnostics, Resources: resources}
	}

	l.collect()

	resources = append(resources, l.scanRemote(seen)...)
	l.checkControlConfigs()

	// the CALM CLI rules: JSON Schema, patterns, Spectral rules, controls, node details
	l.calmRules(data, version, l.patternFiles(self), self != nil)

	if len(l.ctx.Decorators) > 0 {
		resources = append(resources, l.checkDecorators(version, seen)...)
	}

	return LoadResult{Model: l.buildModel(data, version), Diagnostics: l.diagnostics, Resources: resources}
}

// loadDocument reads the architecture file into the loader. When it cannot, it
// gives the detail of the CO-INT-001 diagnostic and false.
func (l *loader) loadDocument() (data []byte, detail string, ok bool) {
	data, err := os.ReadFile(l.abs)
	if err != nil {
		return nil, err.Error(), false
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err.Error(), false
	}

	doc, isObject := asObj(parsed)
	if !isObject {
		return nil, "document is not a JSON object", false
	}

	l.doc = doc

	l.set, err = schemas()
	if err != nil {
		return nil, err.Error(), false
	}

	return data, "", true
}

// patternFiles lists the patterns that the document is checked against: the one
// that `$schema` names (self), then the patterns of the load context.
func (l *loader) patternFiles(self *patternFile) []patternFile {
	patterns := make([]patternFile, 0, len(l.ctx.Patterns)+1)
	if self != nil {
		patterns = append(patterns, *self)
	}

	for _, p := range l.ctx.Patterns {
		patterns = append(patterns, patternFile{URI: posixRelativeOrAbs(l.ctx.Root, p), AbsPath: p})
	}

	return patterns
}

func (l *loader) buildModel(data []byte, version string) *ArchitectureModel {
	return &ArchitectureModel{
		SourceURI:     l.uri,
		Digest:        digest.OfBytes(data),
		SchemaVersion: version,
		Nodes:         nonNilSlice(l.nodes),
		Relationships: nonNilSlice(l.rels),
		Controls:      nonNilSlice(l.controls),
		Flows:         l.flows,
		Metadata:      metadataOf(l.doc["metadata"]),
		ADRs:          adrsOf(l.doc["adrs"]),
	}
}
