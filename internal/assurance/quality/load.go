package quality

import (
	"embed"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/yamlparse"
)

//go:embed schemas/quality.schema.json
var schemaFS embed.FS

// SchemaID is the `$id` of the embedded scenario schema.
const SchemaID = "https://concordia.io/schemas/v1alpha1/quality.schema.json"

var schema = sync.OnceValue(func() *jsonschema.Schema {
	doc, err := schemaFS.ReadFile("schemas/quality.schema.json")
	if err != nil {
		panic("quality: embedded schema missing: " + err.Error())
	}

	compiled, err := jsonschema.Compile(SchemaID, doc)
	if err != nil {
		panic("quality: embedded schema does not compile: " + err.Error())
	}

	return compiled
})

// Known are the identifiers that scenario files may refer to. Both sets are authoritative: an identifier that is
// not in the set is unresolved, so a nil set accepts nothing.
type Known struct {
	// Elements are the CALM node and relationship ids of the model.
	Elements map[string]struct{}
	// Rules are the ids of the rules that the loaded policies define.
	Rules map[string]struct{}
}

// Load reads the scenario files that the glob patterns match (workspace-relative, `assurance.quality.scenarios`),
// validates each against the embedded schema, and checks across files: duplicate scenario and finding ids, scope
// ids that the model does not contain, structural rules that no policy defines, and findings that name no known
// scenario. A pattern that matches no file is an error, because a typo would otherwise load nothing and say so
// nowhere. Every problem is a CO-QUA-001 or CO-QUA-002 error with exit code 2 and the file location; they come in
// a fixed order (file problems in path order, then the cross-file problems).
//
// The ADR identifiers are checked for form only. Whether a decision is registered is known only after the decision
// hook of the run, so ResolveLinks does that check.
func Load(root string, patterns []string, known Known) (*Set, []*diag.Error) {
	set := &Set{Scenarios: []*Scenario{}, Findings: []*Finding{}, Files: []File{}}

	ordered, errs := expandScenarioPatterns(root, patterns)

	for _, file := range ordered {
		one, fileErrs := loadScenarioFile(root, file)
		if len(fileErrs) > 0 {
			errs = append(errs, fileErrs...)

			continue
		}

		set.Scenarios = append(set.Scenarios, one.Scenarios...)
		set.Findings = append(set.Findings, one.Findings...)
		set.Files = append(set.Files, one.Files...)
	}

	errs = append(errs, checkDuplicates(set)...)
	errs = append(errs, checkReferences(set, known)...)

	slices.SortStableFunc(set.Scenarios, func(a, b *Scenario) int { return canonjson.CompareUTF16(a.ID, b.ID) })
	slices.SortStableFunc(set.Findings, func(a, b *Finding) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return set, errs
}

// expandScenarioPatterns resolves the glob patterns to the matched files in path order, with one error for every
// pattern that fails or matches nothing.
func expandScenarioPatterns(root string, patterns []string) ([]string, []*diag.Error) {
	var errs []*diag.Error

	files := map[string]struct{}{}

	for _, pattern := range patterns {
		matched, err := matchScenarioPattern(root, strings.TrimPrefix(pattern, "./"))
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, m := range matched {
			files[m] = struct{}{}
		}
	}

	ordered := make([]string, 0, len(files))
	for f := range files {
		ordered = append(ordered, f)
	}

	slices.SortFunc(ordered, canonjson.CompareUTF16)

	return ordered, errs
}

// matchScenarioPattern expands one glob pattern; a bad pattern and a pattern without a match are errors.
func matchScenarioPattern(root, pattern string) ([]string, *diag.Error) {
	source := diag.WithSource(diag.Source{URI: pattern})

	matched, err := binding.ExpandFileGlobs(root, []string{pattern})
	if err != nil {
		return nil, diag.Fail(diag.QUA001, diag.Detail(err.Error()), source)
	}

	if len(matched) == 0 {
		return nil, diag.Fail(diag.QUA001, diag.Detail("no scenario file matches "+pattern), source)
	}

	return matched, nil
}

// loadScenarioFile reads and parses one workspace-relative scenario file.
func loadScenarioFile(root, file string) (*Set, []*diag.Error) {
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))) //nolint:gosec // the path matched assurance.quality.scenarios.
	if err != nil {
		return nil, []*diag.Error{invalid(file, "cannot read: "+err.Error(), "", nil)}
	}

	return Parse(file, text)
}

// Parse reads one scenario file: syntax, the closed schema, and the form of the criteria and ADR identifiers. It
// checks nothing that needs another file or the model; Load does. A file with problems gives its problems and no
// set.
func Parse(file string, text []byte) (*Set, []*diag.Error) {
	tree, problems := yamlparse.Parse(text)
	if len(problems) > 0 {
		return nil, []*diag.Error{diag.Fail(diag.QUA001, diag.Detail("cannot parse: "+problems[0].Message),
			diag.WithSource(diag.Source{URI: file, Line: problems[0].Line}))}
	}

	doc, ok := tree.(map[string]any)
	if !ok {
		return nil, []*diag.Error{invalid(file, "the document must be a mapping", "", nil)}
	}

	node, _ := yamlparse.ParseNode(text)
	src := sourceDoc{file: file, node: node}

	if errs := preCheck(src, doc); len(errs) > 0 {
		return nil, errs
	}

	if errs := schemaErrors(src, doc); len(errs) > 0 {
		return nil, errs
	}

	set, err := build(src, doc)
	if err != nil {
		return nil, []*diag.Error{invalid(file, err.Error(), "", node)}
	}

	set.Files = []File{{Path: file, Digest: digest.OfBytes(text)}}

	return set, nil
}

// schemaErrors validates the document against the embedded schema and words every violation with its location.
func schemaErrors(src sourceDoc, doc map[string]any) []*diag.Error {
	verrs := schema().Validate(doc)
	if len(verrs) == 0 {
		return nil
	}

	errs := make([]*diag.Error, 0, len(verrs))

	for _, verr := range verrs {
		detail := describe(doc, verr.InstancePath) + verr.Message + " at " + orRoot(verr.InstancePath)
		errs = append(errs, invalid(src.file, detail, verr.InstancePath, src.nodeAt(verr.InstancePath)))
	}

	return errs
}
