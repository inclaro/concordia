package binding

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/yamlparse"
)

var skipDirs = []string{".git", "node_modules", ".concordia", "dist"}

// ExpandFileGlobs gives the workspace files (relative "/" paths, sorted) that match the globs.
func ExpandFileGlobs(root string, globs []string) ([]string, error) {
	if len(globs) == 0 {
		return []string{}, nil
	}

	out := []string{}

	err := filepath.WalkDir(root, globWalker(root, fsutil.GlobMatcher(globs), &out))
	if err != nil {
		return nil, fmt.Errorf("walk workspace %s: %w", root, err)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

// globWalker gives the walk function that adds the regular files under root
// that match to out, and skips the directories in skipDirs.
func globWalker(root string, match func(string) bool, out *[]string) fs.WalkDirFunc {
	return func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return skipDirOf(root, current, entry)
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		rel, relErr := fsutil.ToPosixRelative(root, current)
		if relErr != nil {
			return relErr
		}

		if match(rel) {
			*out = append(*out, rel)
		}

		return nil
	}
}

// skipDirOf gives filepath.SkipDir for a directory under root that the walk does not enter.
func skipDirOf(root, current string, entry fs.DirEntry) error {
	if current != root && slices.Contains(skipDirs, entry.Name()) {
		return filepath.SkipDir
	}

	return nil
}

// bindingFileProblem makes the CO-BND-002 diagnostic of a BindingSet file.
func bindingFileProblem(file, detail, pointer string, line int) diag.Diagnostic {
	return diag.New(diag.BND002, diag.Detail(detail),
		diag.WithSource(diag.Source{URI: file, Pointer: pointer, Line: line}))
}

// cannotParseProblems makes the one diagnostic of a BindingSet file that cannot be parsed.
func cannotParseProblems(file string, err error) []diag.Diagnostic {
	return []diag.Diagnostic{bindingFileProblem(file, "cannot parse: "+err.Error(), "", 0)}
}

// readSet reads and validates one BindingSet file.
func readSet(root, file string) ([]rawBinding, []diag.Diagnostic) {
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))) //nolint:gosec // the file matched the workspace binding globs.
	if err != nil {
		return nil, cannotParseProblems(file, err)
	}

	doc, problems := yamlparse.Parse(text)
	if len(problems) > 0 {
		return nil, yamlProblems(file, problems)
	}

	if errs := bindingSchema().Validate(doc); len(errs) > 0 {
		return nil, schemaProblems(file, errs)
	}

	return decodeRawSet(file, doc)
}

func yamlProblems(file string, problems []yamlparse.Problem) []diag.Diagnostic {
	out := make([]diag.Diagnostic, len(problems))
	for i, p := range problems {
		out[i] = bindingFileProblem(file, "cannot parse: "+p.Message, "", p.Line)
	}

	return out
}

func schemaProblems(file string, errs []jsonschema.Error) []diag.Diagnostic {
	out := make([]diag.Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = bindingFileProblem(file, e.Message, e.InstancePath, 0)
	}

	return out
}

// decodeRawSet decodes the parsed document of a BindingSet file.
func decodeRawSet(file string, doc any) ([]rawBinding, []diag.Diagnostic) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, cannotParseProblems(file, err)
	}

	var set rawBindingSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, cannotParseProblems(file, err)
	}

	return set.Bindings, nil
}
