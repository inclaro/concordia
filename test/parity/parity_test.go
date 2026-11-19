package parity_test

// These tests compare `concordia model validate` with the recorded results of
// `calm validate` (the CALM CLI). They need no Node.js: the results are in the
// expected.json file of each case. `mise run calm:record` writes them.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/parity"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	casesDir       = "cases"
	exclusionsFile = "exclusions.json"
	rulesFile      = "upstream-rules.json"
	releasesFile   = "upstream-releases.json"
)

// releaseRe finds the CALM release ("1.2") or draft ("draft/2025-03") of a URL.
var releaseRe = regexp.MustCompile(`^https://calm\.finos\.org/(?:release/([^/]+)/meta/|(draft/[^/]+)/)`)

// caseDirs gives the "/" names (relative to cases/) of the directories with a case.json.
func caseDirs(t *testing.T) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(casesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && d.Name() == parity.CaseFile {
			rel, relErr := filepath.Rel(casesDir, filepath.Dir(p))
			if relErr != nil {
				return relErr
			}

			out = append(out, filepath.ToSlash(rel))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(out)

	return out
}

func readJSON(t *testing.T, file string, v any) bool {
	t.Helper()

	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	return true
}

// releaseDoc is a document that declaredReleases reads.
type releaseDoc struct {
	file    string
	pattern bool
}

// declaredReleases gives the CALM releases that a run uses, as Concordia sees
// them: the top-level `$schema` of the architecture and the timeline, and every
// `$schema` and `$ref` of a pattern. A top-level `$schema` that names a local
// document (a relative path, or a URL of the run's URL mapping) names a pattern,
// which is read too.
func declaredReleases(dir string, run *parity.Run) []string {
	mapping := loadURLMapping(dir, run.URLMapping)

	var out []string

	queue := []releaseDoc{{run.Architecture, false}, {run.Timeline, false}, {run.Pattern, true}}
	seen := map[string]bool{}

	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]

		if next.file == "" || seen[path.Clean(next.file)] {
			continue
		}

		seen[path.Clean(next.file)] = true

		tree, ok := readTree(dir, next.file)
		if !ok {
			continue
		}

		top, _ := tree.(map[string]any)
		schema, _ := top["$schema"].(string)

		refs := []string{schema}
		if next.pattern {
			refs = references(tree)
		}

		out = append(out, releasesOf(refs)...)

		if linked, local := linkedPattern(schema, next.file, mapping); local {
			queue = append(queue, releaseDoc{linked, true})
		}
	}

	return out
}

// loadURLMapping gives the URL mapping of a run with the files relative to the case directory.
// A missing or invalid mapping file gives an empty mapping.
func loadURLMapping(dir, urlMapping string) map[string]string {
	mapping := map[string]string{}

	if urlMapping == "" {
		return mapping
	}

	var raw map[string]string

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(urlMapping)))
	if err != nil || json.Unmarshal(data, &raw) != nil {
		return mapping
	}

	for url, p := range raw {
		mapping[url] = path.Join(path.Dir(urlMapping), p)
	}

	return mapping
}

// readTree reads a JSON document of the case. It gives false when the file cannot be read or parsed.
func readTree(dir, file string) (any, bool) {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, false
	}

	var tree any
	if json.Unmarshal(data, &tree) != nil {
		return nil, false
	}

	return tree, true
}

// releasesOf gives the CALM releases that the references name.
func releasesOf(refs []string) []string {
	var out []string

	for _, ref := range refs {
		ref, _, _ = strings.Cut(ref, "#")
		if m := releaseRe.FindStringSubmatch(ref); m != nil {
			out = append(out, m[1]+m[2])
		}
	}

	return out
}

// linkedPattern gives the pattern file that the top-level `$schema` of a document names, when it
// names a local document: a file of the URL mapping or a relative path.
func linkedPattern(schema, file string, mapping map[string]string) (string, bool) {
	switch {
	case releaseRe.MatchString(schema) || schema == "":
		return "", false
	case mapping[schema] != "":
		return mapping[schema], true
	case !strings.Contains(schema, "://"):
		return path.Join(path.Dir(file), schema), true
	default:
		return "", false
	}
}

// references gives the `$schema` and `$ref` string values of a document.
func references(value any) []string {
	var out []string

	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if s, ok := item.(string); ok && isReferenceKey(key) {
				out = append(out, s)
			}

			out = append(out, references(item)...)
		}
	case []any:
		for _, item := range v {
			out = append(out, references(item)...)
		}
	}

	return out
}

// isReferenceKey tells if the key of an object holds a reference to a schema or a document.
func isReferenceKey(key string) bool {
	return key == "$schema" || key == "$ref"
}

func loadExclusions(t *testing.T) parity.Exclusions {
	t.Helper()

	ex, err := parity.LoadExclusions(exclusionsFile)
	if err != nil {
		t.Fatal(err)
	}

	return ex
}

func TestConcordiaMatchesCalmCLI(t *testing.T) {
	t.Parallel()

	ex := loadExclusions(t)

	for _, name := range caseDirs(t) {
		runCaseTests(t, &ex, name)
	}
}

// recordedCase is a case with the recorded upstream outcomes of its runs.
type recordedCase struct {
	name     string
	dir      string
	expected parity.Expected
}

// runCaseTests starts a parallel subtest for each run of the case.
func runCaseTests(t *testing.T, ex *parity.Exclusions, name string) {
	t.Helper()

	rc := &recordedCase{name: name, dir: filepath.Join(casesDir, filepath.FromSlash(name))}

	c, err := parity.LoadCase(rc.dir)
	if err != nil {
		t.Fatal(err)
	}

	readJSON(t, filepath.Join(rc.dir, parity.ExpectedFile), &rc.expected)

	for _, run := range c.Runs {
		t.Run(name+"/"+run.Name, func(t *testing.T) {
			t.Parallel()

			rc.check(t, ex, &run)
		})
	}
}

// check checks one run: it is skipped when excluded, it reports an excluded release as not supported,
// and otherwise it gives the recorded upstream outcome.
func (rc *recordedCase) check(t *testing.T, ex *parity.Exclusions, run *parity.Run) {
	t.Helper()

	if skip, ok := ex.Run(rc.name, run.Name); ok {
		t.Skip(skip.Reason)
	}

	if slices.ContainsFunc(declaredReleases(rc.dir, run), ex.Release) {
		checkExcludedRelease(t, rc.dir, run)

		return
	}

	checkRecordedOutcome(t, rc.dir, run, &rc.expected)
}

// checkExcludedRelease checks that Concordia reports a run of an excluded release as not supported.
func checkExcludedRelease(t *testing.T, dir string, run *parity.Run) {
	t.Helper()

	diags, err := parity.ConcordiaDiagnostics(t.Context(), dir, run)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(diags, func(d diag.Diagnostic) bool { return d.Code == diag.INT003 }) {
		t.Errorf("excluded release: want CO-INT-003, got %v", diags)
	}
}

// checkRecordedOutcome checks that Concordia gives the recorded upstream outcome of a run.
func checkRecordedOutcome(t *testing.T, dir string, run *parity.Run, expected *parity.Expected) {
	t.Helper()

	want, ok := expected.Runs[run.Name]
	if !ok {
		t.Fatal("no recorded upstream outcome; run mise run calm:record")
	}

	got, err := parity.RunConcordia(t.Context(), dir, run)
	if err != nil {
		t.Fatal(err)
	}

	if diffs := parity.Compare(want, got); len(diffs) > 0 {
		t.Errorf("Concordia differs from calm %s:\n  %s", expected.Tag, strings.Join(diffs, "\n  "))
	}
}

func TestExclusionsAreUsed(t *testing.T) {
	t.Parallel()

	ex := loadExclusions(t)

	for _, r := range ex.Runs {
		c, err := parity.LoadCase(filepath.Join(casesDir, filepath.FromSlash(r.Case)))
		if err != nil {
			t.Errorf("exclusion %s/%s: %v", r.Case, r.Run, err)

			continue
		}

		if !slices.ContainsFunc(c.Runs, func(run parity.Run) bool { return run.Name == r.Run }) {
			t.Errorf("exclusion %s/%s: the case has no such run", r.Case, r.Run)
		}
	}
}

func TestEveryUpstreamRuleHasACase(t *testing.T) {
	t.Parallel()

	var rules []string
	if !readJSON(t, rulesFile, &rules) {
		t.Skip(rulesFile + " is missing; run mise run calm:record")
	}

	for _, rule := range rules {
		checkRuleCase(t, rule)
	}
}

// checkRuleCase checks that the case of an upstream rule has a run fail that triggers the rule
// and a run pass that does not.
func checkRuleCase(t *testing.T, rule string) {
	t.Helper()

	dir := filepath.Join(casesDir, "concordia", rule)

	var expected parity.Expected
	if !readJSON(t, filepath.Join(dir, parity.ExpectedFile), &expected) {
		t.Errorf("rule %s: no recorded case in %s", rule, dir)

		return
	}

	fail, ok := expected.Runs["fail"]
	if !ok || !triggersRule(fail, rule) {
		t.Errorf("rule %s: run fail does not trigger the rule", rule)
	}

	pass, ok := expected.Runs["pass"]
	if !ok || triggersRule(pass, rule) {
		t.Errorf("rule %s: run pass is missing or triggers the rule", rule)
	}
}

// triggersRule tells if the outcome has a finding of the rule.
func triggersRule(o parity.Outcome, rule string) bool {
	if rule == "json-schema" {
		return o.JSONSchema
	}

	return slices.ContainsFunc(o.Findings, func(f parity.Finding) bool { return f.Rule == rule })
}

func TestEveryUpstreamReleaseIsSupportedOrExcluded(t *testing.T) {
	t.Parallel()

	var releases []string
	if !readJSON(t, releasesFile, &releases) {
		t.Skip(releasesFile + " is missing; run mise run calm:record")
	}

	ex := loadExclusions(t)

	for _, r := range releases {
		if !slices.Contains(workspace.CalmSchemaVersions, r) && !ex.Release(r) {
			t.Errorf("CALM release %s is neither supported nor in excludedReleases", r)
		}
	}
}
