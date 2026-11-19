package e2e_test

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/jsonschema"
)

var (
	shaValue   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	generation = regexp.MustCompile(`^(cbm:[^:]+:)[0-9a-f]{16}$`)
	gitSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	shaInText  = regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	stepPrefix = regexp.MustCompile(`^\d\d-`)
)

func readGolden(t tester, p string) string {
	t.Helper()

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// goldenComparer compares one golden file with the file of the Go tool.
type goldenComparer func(t tester, name, golden, got string)

// goldenSuffixComparers picks the comparison by the end of the file name; the first match wins.
var goldenSuffixComparers = []struct {
	compare goldenComparer
	suffix  string
}{
	{compareSARIF, ".sarif"},
	{compareJUnit, ".xml"},
	{compareDSL, ".dsl"},
	{compareJSON, ".json"},
	{compareYAML, ".yaml"},
	{compareLock, ".lock"},
}

// goldenUnchecked tells whether a golden file is not compared on purpose.
func goldenUnchecked(base string) bool {
	switch {
	case strings.HasSuffix(base, ".github_workflows_concordia.yml"),
		strings.HasSuffix(base, ".gitlab_concordia.gitlab-ci.yml"),
		base == "ci_concordia.sh":
		// The CI templates changed on purpose (Go in place of Node.js): compare only that the file exists.
		return true
	case base == "bindings_concordia.bindings.yaml":
		// The TypeScript starter file lacked apiVersion and kind and failed its own schema: the Go file is valid on purpose.
		return true
	}

	return false
}

// goldenResultJSON tells whether a golden file is JSON with the volatile keys and the digests to normalize.
func goldenResultJSON(base string) bool {
	switch base {
	case "build_concordia_results.json", "build_concordia_coverage.json", "manifest.json":
		return true
	}

	return strings.HasSuffix(base, "exceptions_concordia.baseline.json")
}

// compareFile compares one golden file with the file of the Go tool. name is
// "NN-<path with / replaced by _>".
func compareFile(t tester, name, golden, got string) {
	t.Helper()

	base := stepPrefix.ReplaceAllString(name, "")

	if goldenUnchecked(base) {
		return
	}

	if goldenResultJSON(base) {
		compareResultJSON(t, name, golden, got)

		return
	}

	for _, entry := range goldenSuffixComparers {
		if strings.HasSuffix(base, entry.suffix) {
			entry.compare(t, name, golden, got)

			return
		}
	}

	if golden != got {
		t.Errorf("%s: text differs\n--- golden\n%s\n--- go\n%s", name, golden, got)
	}
}

func compareDSL(t tester, name, golden, got string) {
	t.Helper()

	if a, b := shaInText.ReplaceAllString(golden, "sha256:X"), shaInText.ReplaceAllString(got, "sha256:X"); a != b {
		t.Errorf("%s: DSL text differs\n--- golden\n%s\n--- go\n%s", name, a, b)
	}
}

// normalize replaces the values that depend on the exact bytes of the repository:
// every sha256 digest becomes "sha256:X", the hash of a graph generation becomes
// "X" and a git commit id becomes "git:X". The golden files were recorded before
// the rename, so their repository trees (and so their commit ids and CBM
// generations) and their GU- fingerprints differ from the Go run.
// An array of objects with a "fingerprint" key is sorted after the replacement,
// because the tool sorts findings by fingerprint and the hash values differ.
func normalize(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return normalizeMap(value)
	case []any:
		return normalizeList(value)
	case string:
		return normalizeString(value)
	default:
		return v
	}
}

func normalizeMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for k, item := range value {
		out[k] = normalize(item)
	}

	return out
}

func normalizeList(value []any) []any {
	out := make([]any, len(value))
	for i, item := range value {
		out[i] = normalize(item)
	}

	if slices.ContainsFunc(out, hasFingerprint) {
		slices.SortFunc(out, func(a, b any) int { return strings.Compare(compactJSON(a), compactJSON(b)) })
	}

	return out
}

func normalizeString(value string) string {
	switch {
	case shaValue.MatchString(value):
		return "sha256:X"
	case gitSHA.MatchString(value):
		return "git:X"
	default:
		return generation.ReplaceAllString(value, "${1}X")
	}
}

// firstDiff gives a description of the first difference of two parsed values, or an empty string.
func firstDiff(path string, a, b any) string {
	if reflect.DeepEqual(a, b) {
		return ""
	}

	if d := firstNestedDiff(path, a, b); d != "" {
		return d
	}

	return fmt.Sprintf("%s: golden %s, Go output %s", path, shorten(compactJSON(a)), shorten(compactJSON(b)))
}

// firstNestedDiff looks into two objects or two lists of the same kind for the first difference.
func firstNestedDiff(path string, a, b any) string {
	switch left := a.(type) {
	case map[string]any:
		if right, ok := b.(map[string]any); ok {
			return firstMapDiff(path, left, right)
		}
	case []any:
		if right, ok := b.([]any); ok {
			return firstListDiff(path, left, right)
		}
	}

	return ""
}

func firstMapDiff(path string, left, right map[string]any) string {
	for _, k := range slices.Sorted(maps.Keys(left)) {
		if d := firstKeyDiff(path, k, left, right); d != "" {
			return d
		}
	}

	for _, k := range slices.Sorted(maps.Keys(right)) {
		if _, present := left[k]; !present {
			return fmt.Sprintf("%s.%s: only in Go output", path, k)
		}
	}

	return ""
}

// firstKeyDiff compares the value of one key of the golden object with the one of the Go object.
func firstKeyDiff(path, key string, left, right map[string]any) string {
	other, present := right[key]
	if !present {
		return fmt.Sprintf("%s.%s: only in golden", path, key)
	}

	return firstDiff(path+"."+key, left[key], other)
}

func firstListDiff(path string, left, right []any) string {
	for i := range min(len(left), len(right)) {
		if d := firstDiff(fmt.Sprintf("%s[%d]", path, i), left[i], right[i]); d != "" {
			return d
		}
	}

	if len(left) != len(right) {
		return fmt.Sprintf("%s: golden has %d items, Go output has %d", path, len(left), len(right))
	}

	return ""
}

func hasFingerprint(v any) bool {
	obj, ok := v.(map[string]any)
	if !ok {
		return false
	}

	_, present := obj["fingerprint"]

	return present
}

// compactJSON is the one-line JSON text of a parsed value.
func compactJSON(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}

	return string(out)
}

// shorten cuts a long text for an error message.
func shorten(text string) string {
	const limit = 300
	if len(text) > limit {
		return text[:limit] + "..."
	}

	return text
}

func parseJSON(t tester, name, text string) any {
	t.Helper()

	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s: invalid JSON: %v", name, err)
	}

	return v
}

// compareJSON compares parsed JSON.
func compareJSON(t tester, name, golden, got string) {
	t.Helper()

	a, b := parseJSON(t, name, golden), parseJSON(t, name, got)

	if d := firstDiff("$", a, b); d != "" {
		t.Errorf("%s: %s", name, d)
	}
}

// compareResultJSON compares parsed JSON after it removes the keys run, toolchain and inputs and
// normalizes the sha256 values.
func compareResultJSON(t tester, name, golden, got string) {
	t.Helper()

	a, b := parseJSON(t, name, golden), parseJSON(t, name, got)

	if d := firstDiff("$", normalize(dropVolatile(a)), normalize(dropVolatile(b))); d != "" {
		t.Errorf("%s: %s", name, d)
	}
}

func dropVolatile(v any) any {
	if obj, ok := v.(map[string]any); ok {
		for _, key := range []string{"run", "toolchain", "inputs"} {
			delete(obj, key)
		}
	}

	return v
}

// compareYAML compares parsed YAML with the sha256 values normalized (waivers contain a fingerprint).
func compareYAML(t tester, name, golden, got string) {
	t.Helper()

	var a, b any

	if err := yaml.Unmarshal([]byte(golden), &a); err != nil {
		t.Fatalf("%s: golden is not YAML: %v", name, err)
	}

	if err := yaml.Unmarshal([]byte(got), &b); err != nil {
		t.Fatalf("%s: invalid YAML: %v", name, err)
	}

	if d := firstDiff("$", normalize(a), normalize(b)); d != "" {
		t.Errorf("%s: %s", name, d)
	}
}

// compareLock compares a lockfile like compareYAML. The Go tool also pins the digest of the CBM executable
// (`resolved.cbm.sha256`), which depends on the machine, so that key is not compared. It is removed from both
// sides, so a golden written in update mode with the key still compares.
func compareLock(t tester, name, golden, got string) {
	t.Helper()

	compareYAML(t, name, withoutCbmDigest(t, name, golden), withoutCbmDigest(t, name, got))
}

// withoutCbmDigest gives the lockfile text without `resolved.cbm.sha256`.
func withoutCbmDigest(t tester, name, text string) string {
	t.Helper()

	var lock map[string]any
	if err := yaml.Unmarshal([]byte(text), &lock); err != nil {
		t.Fatalf("%s: invalid YAML: %v", name, err)
	}

	if resolved, ok := lock["resolved"].(map[string]any); ok {
		if cbm, ok := resolved["cbm"].(map[string]any); ok {
			delete(cbm, "sha256")
		}
	}

	trimmed, err := yaml.Marshal(lock)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	return string(trimmed)
}

type sarifResult struct {
	RuleID        string `json:"ruleId"`
	Level         string `json:"level"`
	BaselineState string `json:"baselineState"`
	Locations     []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

type sarifLog struct {
	Runs []struct {
		Results []sarifResult `json:"results"`
	} `json:"runs"`
}

// key is the line of (ruleId, level, baselineState, uri) of a result.
func (r sarifResult) key() string {
	uris := make([]string, 0, len(r.Locations))
	for _, l := range r.Locations {
		uris = append(uris, l.PhysicalLocation.ArtifactLocation.URI)
	}

	return strings.Join([]string{r.RuleID, r.Level, r.BaselineState, strings.Join(uris, ",")}, " | ")
}

// sarifKeys is the sorted multiset of (ruleId, level, baselineState, uri).
func sarifKeys(t tester, name, text string) []string {
	t.Helper()

	var log sarifLog
	if err := json.Unmarshal([]byte(text), &log); err != nil {
		t.Fatalf("%s: invalid SARIF: %v", name, err)
	}

	var keys []string

	for _, run := range log.Runs {
		for _, r := range run.Results {
			keys = append(keys, r.key())
		}
	}

	slices.Sort(keys)

	return keys
}

func compareSARIF(t tester, name, golden, got string) {
	t.Helper()

	schemaDoc, err := os.ReadFile(sarifSchemaPath)
	if err != nil {
		t.Fatal(err)
	}

	schema, err := jsonschema.Compile(sarifSchemaID, schemaDoc)
	if err != nil {
		t.Fatal(err)
	}

	if errs := schema.ValidateJSON([]byte(got)); len(errs) > 0 {
		t.Errorf("%s: SARIF is not valid: %+v", name, errs)
	}

	if a, b := sarifKeys(t, name, golden), sarifKeys(t, name, got); !slices.Equal(a, b) {
		t.Errorf("%s: SARIF results differ\n--- golden\n%s\n--- go\n%s", name, strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

type junitCase struct {
	Name    string   `xml:"name,attr"`
	Failure []string `xml:"failure"`
	Error   []string `xml:"error"`
	Skipped []string `xml:"skipped"`
}

type junitReport struct {
	Suites []struct {
		Cases []junitCase `xml:"testcase"`
	} `xml:"testsuite"`
}

// junitKeys is the sorted list of "name | failures | errors | skipped".
func junitKeys(t tester, name, text string) []string {
	t.Helper()

	var report junitReport
	if err := xml.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("%s: invalid JUnit XML: %v", name, err)
	}

	var keys []string

	for _, suite := range report.Suites {
		for _, c := range suite.Cases {
			keys = append(keys, fmt.Sprintf("%s | %d | %d | %d", c.Name, len(c.Failure), len(c.Error), len(c.Skipped)))
		}
	}

	slices.Sort(keys)

	return keys
}

func compareJUnit(t tester, name, golden, got string) {
	t.Helper()

	if a, b := junitKeys(t, name, golden), junitKeys(t, name, got); !slices.Equal(a, b) {
		t.Errorf("%s: JUnit test cases differ\n--- golden\n%s\n--- go\n%s", name, strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}
