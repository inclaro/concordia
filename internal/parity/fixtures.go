package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// fixtureDirs are the upstream directories with test fixtures.
var fixtureDirs = []string{"cli/test_fixtures", "shared/test_fixtures"}

// Document kinds of Classify.
const (
	KindNone         = ""
	KindArchitecture = ModeArchitecture
	KindPattern      = ModePattern
	KindTimeline     = ModeTimeline
)

// Classify gives the validation mode of a fixture file, or KindNone.
func Classify(data []byte) string {
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil || doc == nil {
		return KindNone
	}

	props, _ := doc["properties"].(map[string]any)

	switch {
	case hasAnyKey(doc, "moments"):
		return KindTimeline
	case hasAnyKey(props, "nodes", "relationships"):
		return KindPattern
	case hasAnyKey(doc, "nodes", "relationships"):
		return KindArchitecture
	default:
		return KindNone
	}
}

// hasAnyKey tells if the object has at least one of the keys.
func hasAnyKey(object map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := object[key]; ok {
			return true
		}
	}

	return false
}

// CaseName gives the case name of an upstream fixture directory ("/" path in the checkout).
func CaseName(upstreamPath string) string {
	return strings.ReplaceAll(upstreamPath, "/", "__")
}

// RunName gives the run name of a file ("/" path in the case).
func RunName(file string) string {
	return strings.ReplaceAll(strings.TrimSuffix(file, ".json"), "/", "__")
}

// importFixtures makes a case for each upstream fixture directory.
func importFixtures(opts *RecordOptions) error {
	root := filepath.Join(opts.Cases, CasesDir, upstreamCase)
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove %s: %w", root, err)
	}

	for _, fixture := range fixtureDirs {
		if err := importFixtureDir(opts, root, fixture); err != nil {
			return err
		}
	}

	return nil
}

// isTopLevelJSON tells if the relative path is a JSON file directly in the directory.
func isTopLevelJSON(rel string) bool {
	return !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".json")
}

// isJSON tells if the relative path is a JSON file.
func isJSON(rel string) bool {
	return strings.HasSuffix(rel, ".json")
}

// importFixtureDir makes a case for the JSON files of an upstream fixture
// directory and a case for each of its subdirectories.
func importFixtureDir(opts *RecordOptions, root, fixture string) error {
	src := filepath.Join(opts.Upstream, filepath.FromSlash(fixture))

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", fixture, err)
	}

	if err := importCase(filepath.Join(root, CaseName(fixture)), src, fixture, isTopLevelJSON); err != nil {
		return err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		up := fixture + "/" + e.Name()

		if err := importCase(filepath.Join(root, CaseName(up)), filepath.Join(src, e.Name()), up, isJSON); err != nil {
			return err
		}
	}

	return nil
}

func importCase(dst, src, upstreamPath string, keep func(rel string) bool) error {
	if err := CopyDir(src, dst, keep); err != nil {
		return err
	}

	files, err := copiedFiles(dst)
	if err != nil {
		return err
	}

	fixtureCase := Case{Origin: "upstream:" + upstreamPath, Runs: []Run{}}

	for _, file := range files {
		run, ok, err := fixtureRun(dst, file)
		if err != nil {
			return err
		}

		if ok {
			fixtureCase.Runs = append(fixtureCase.Runs, run)
		}
	}

	if len(fixtureCase.Runs) == 0 {
		return os.RemoveAll(dst) //nolint:wrapcheck // the path is in the error.
	}

	return writeJSON(filepath.Join(dst, CaseFile), fixtureCase)
}

// copiedFiles gives the sorted slash paths of the files below dst.
func copiedFiles(dst string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dst, p)
		if err != nil {
			return err
		}

		files = append(files, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dst, err)
	}

	slices.Sort(files)

	return files, nil
}

// fixtureRun makes the run of a copied fixture file. It gives false when the
// file is not a fixture of a validation run.
func fixtureRun(dst, file string) (Run, bool, error) {
	base := path.Base(file)
	if base == urlMappingFile || strings.HasSuffix(base, "_output.json") {
		return Run{}, false, nil
	}

	data, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(file))) //nolint:gosec // a copied fixture.
	if err != nil {
		return Run{}, false, fmt.Errorf("read fixture: %w", err)
	}

	run := Run{Name: RunName(file), Mode: Classify(data)}

	switch run.Mode {
	case KindNone:
		return Run{}, false, nil
	case KindTimeline:
		run.Timeline = file
	case KindPattern:
		run.Pattern = file
	case KindArchitecture:
		run.Architecture = file
	}

	run.URLMapping = nearestURLMapping(dst, file)

	return run, true, nil
}

// nearestURLMapping gives the url mapping file in the directory of file or the
// nearest parent (up to the case directory), or "".
func nearestURLMapping(caseDir, file string) string {
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		candidate := path.Join(dir, urlMappingFile)
		if _, err := os.Stat(filepath.Join(caseDir, filepath.FromSlash(candidate))); err == nil {
			return candidate
		}

		if dir == "." {
			return ""
		}
	}
}
