package projection_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/projection"
)

const exportsDir = "testdata/exports"

// recorded maps each export format to the files that the pinned CLI wrote for
// testdata/exports/workspace.dsl.
var recorded = map[projection.ExportFormat][]string{
	projection.FormatJSON:     {"workspace.json"},
	projection.FormatMermaid:  {"structurizr-shop-containers.mmd"},
	projection.FormatPlantUML: {"structurizr-shop-containers.puml", "structurizr-shop-containers-key.puml"},
	projection.FormatDot:      {"structurizr-shop-containers.dot"},
}

func readRecorded(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(exportsDir, name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// metadataLike matches key names that hold generation data.
var metadataLike = regexp.MustCompile(`(?i)(date|time|agent|user|revision|generated|modified|created|version|hash|checksum|uuid)`)

// unlistedMetadata gives the keys of a json export that the rule neither lists as
// metadata nor knows as content: top-level keys, keys of the `structurizr.`
// namespace in the top-level properties, and any key that looks like generation
// data anywhere in the tree.
func unlistedMetadata(t *testing.T, export []byte, rule *projection.MetadataRule) []string {
	t.Helper()

	var workspace map[string]any
	if err := json.Unmarshal(export, &workspace); err != nil {
		t.Fatal(err)
	}

	bad := canonicalTestUnlistedTopLevel(workspace, rule)
	bad = append(bad, canonicalTestUnlistedProperties(workspace, rule)...)
	bad = append(bad, canonicalTestMetadataLikeKeys("", workspace, rule)...)

	slices.Sort(bad)

	return slices.Compact(bad)
}

// canonicalTestListedWorkspaceKey reports whether the rule lists key as
// workspace metadata.
func canonicalTestListedWorkspaceKey(rule *projection.MetadataRule, key string) bool {
	return slices.Contains(rule.WorkspaceKeys, key)
}

// canonicalTestUnlistedTopLevel names the top-level keys that the rule knows
// neither as metadata nor as content.
func canonicalTestUnlistedTopLevel(workspace map[string]any, rule *projection.MetadataRule) []string {
	var bad []string

	for key := range workspace {
		if !canonicalTestListedWorkspaceKey(rule, key) && !slices.Contains(rule.ContentKeys, key) {
			bad = append(bad, "top-level key "+key)
		}
	}

	return bad
}

// canonicalTestUnlistedProperty reports whether key is a `structurizr.`
// property that the rule does not list.
func canonicalTestUnlistedProperty(rule *projection.MetadataRule, key string) bool {
	if !strings.HasPrefix(key, "structurizr.") || slices.Contains(rule.PropertyKeys, key) {
		return false
	}

	return !slices.ContainsFunc(rule.PropertyPrefixes, func(p string) bool { return strings.HasPrefix(key, p) })
}

// canonicalTestUnlistedProperties names the unlisted `structurizr.` keys of the
// top-level properties.
func canonicalTestUnlistedProperties(workspace map[string]any, rule *projection.MetadataRule) []string {
	props, _ := workspace["properties"].(map[string]any)

	var bad []string

	for key := range props {
		if canonicalTestUnlistedProperty(rule, key) {
			bad = append(bad, "property "+key)
		}
	}

	return bad
}

// canonicalTestFlagsKey reports whether a key that looks like generation data
// is unexplained: it sits below the top level, or the rule does not list it.
func canonicalTestFlagsKey(rule *projection.MetadataRule, path, key string) bool {
	if !metadataLike.MatchString(key) {
		return false
	}

	return path != "" || !canonicalTestListedWorkspaceKey(rule, key)
}

// canonicalTestMetadataLikeKeys walks the tree below v and names every key that
// canonicalTestFlagsKey flags.
func canonicalTestMetadataLikeKeys(path string, v any, rule *projection.MetadataRule) []string {
	var bad []string

	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if canonicalTestFlagsKey(rule, path, key) {
				bad = append(bad, "key "+path+"/"+key)
			}

			bad = append(bad, canonicalTestMetadataLikeKeys(path+"/"+key, child, rule)...)
		}
	case []any:
		for _, child := range node {
			bad = append(bad, canonicalTestMetadataLikeKeys(path, child, rule)...)
		}
	}

	return bad
}

func TestRecordedExportsComeFromThePinnedCLI(t *testing.T) {
	t.Parallel()

	got := projection.ParseCLIVersion(string(readRecorded(t, "cli-version.txt")))
	if got != projection.PinnedStructurizrCLI {
		t.Errorf("the exports in %s were recorded with %s, but the pin is %s: record them again and review the rule",
			exportsDir, got, projection.PinnedStructurizrCLI)
	}

	for format, files := range recorded {
		for _, name := range files {
			if len(readRecorded(t, name)) == 0 {
				t.Errorf("%s export %s is empty", format, name)
			}
		}
	}
}

func TestExportMetadataIsRemoved(t *testing.T) {
	t.Parallel()

	got := canonicalTestCanonicalJSON(t, readRecorded(t, "workspace.json"))
	text := string(got)

	for _, gone := range canonicalTestPresent(text, []string{`"structurizr.dsl"`, `"structurizr.inspection.`, "d29ya3NwYWNl"}) {
		t.Errorf("the canonical export still has %s", gone)
	}

	if strings.Contains(text, `"id": 0`) {
		t.Error("the canonical export still has the workspace id")
	}

	// Content stays: model identifiers, relationships, and the view.
	for _, kept := range canonicalTestAbsent(text, []string{`"structurizr.dsl.identifier": "api"`, `"destinationId": "4"`, `"key": "shop-containers"`, `"technology": "SQL"`, `"id": "3"`}) {
		t.Errorf("the canonical export lost %s", kept)
	}

	if !strings.HasSuffix(text, "}\n") {
		t.Errorf("the canonical export does not end with a newline")
	}

	again, err := projection.CanonicalizeExport(projection.FormatJSON, got)
	if err != nil || string(again) != text {
		t.Errorf("canonicalization is not idempotent (%v)", err)
	}
}

// canonicalTestCanonicalJSON gives the canonical bytes of a json export.
func canonicalTestCanonicalJSON(t *testing.T, export []byte) []byte {
	t.Helper()

	got, err := projection.CanonicalizeExport(projection.FormatJSON, export)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// canonicalTestPresent gives the needles that text contains.
func canonicalTestPresent(text string, needles []string) []string {
	return slices.DeleteFunc(slices.Clone(needles), func(needle string) bool { return !strings.Contains(text, needle) })
}

// canonicalTestAbsent gives the needles that text does not contain.
func canonicalTestAbsent(text string, needles []string) []string {
	return slices.DeleteFunc(slices.Clone(needles), func(needle string) bool { return strings.Contains(text, needle) })
}

// canonicalTestStamped gives the recorded json export with generation data set.
func canonicalTestStamped(t *testing.T, stamp, agent string) []byte {
	t.Helper()

	var workspace map[string]any
	if err := json.Unmarshal(readRecorded(t, "workspace.json"), &workspace); err != nil {
		t.Fatal(err)
	}

	workspace["lastModifiedDate"] = stamp
	workspace["lastModifiedAgent"] = agent
	workspace["lastModifiedUser"] = "ci"
	workspace["revision"] = 17

	out, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestExportTimestampDoesNotChangeTheCanonicalBytesOrDigest(t *testing.T) {
	t.Parallel()

	first := canonicalTestCanonicalJSON(t, canonicalTestStamped(t, "2025-01-01T10:00:00.000Z", "structurizr-java/5.0.2"))
	second := canonicalTestCanonicalJSON(t, canonicalTestStamped(t, "2026-06-30T23:59:59.999Z", "structurizr-java/6.0.0"))
	plain := canonicalTestCanonicalJSON(t, readRecorded(t, "workspace.json"))

	if sha256.Sum256(first) != sha256.Sum256(second) || !bytes.Equal(first, plain) {
		t.Error("the generation time or agent changed the canonical export")
	}

	for _, gone := range canonicalTestPresent(string(first), []string{"lastModified", "2025-01-01", "revision", "structurizr-java"}) {
		t.Errorf("the canonical export contains %s", gone)
	}
}

func TestEveryMetadataKeyOfTheRecordedExportIsListed(t *testing.T) {
	t.Parallel()

	if bad := unlistedMetadata(t, readRecorded(t, "workspace.json"), &projection.ExportMetadataRule); len(bad) != 0 {
		t.Errorf("the recorded export has metadata that the rule does not list: %v", bad)
	}
}

// section gives a JSON object below the workspace.
func section(workspace map[string]any, key string) map[string]any {
	obj, ok := workspace[key].(map[string]any)
	if !ok {
		panic(key + " is not an object")
	}

	return obj
}

func TestUnlistedMetadataKeyIsDetected(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(map[string]any){
		"top-level timestamp": func(w map[string]any) { w["generatedAt"] = "2026-01-01" },
		"new property":        func(w map[string]any) { section(w, "properties")["structurizr.build"] = "1" },
		"nested agent":        func(w map[string]any) { section(w, "views")["lastModifiedAgent"] = "x" },
		"unknown top-level":   func(w map[string]any) { w["thumbnail"] = "x" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var workspace map[string]any
			if err := json.Unmarshal(readRecorded(t, "workspace.json"), &workspace); err != nil {
				t.Fatal(err)
			}

			mutate(workspace)

			export, err := json.Marshal(workspace)
			if err != nil {
				t.Fatal(err)
			}

			if bad := unlistedMetadata(t, export, &projection.ExportMetadataRule); len(bad) == 0 {
				t.Error("the drift check accepted an unlisted metadata key")
			}
		})
	}
}

func TestTextExportsKeepTheirBytesAndGetLFLineEndings(t *testing.T) {
	t.Parallel()

	for _, format := range []projection.ExportFormat{projection.FormatMermaid, projection.FormatPlantUML, projection.FormatDot} {
		for _, name := range recorded[format] {
			data := readRecorded(t, name)

			got, err := projection.CanonicalizeExport(format, data)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("%s: canonicalization changed an LF export (%v)", name, err)
			}

			crlf := strings.ReplaceAll(string(data), "\n", "\r\n")

			if got, err = projection.CanonicalizeExport(format, []byte(crlf)); err != nil || !bytes.Equal(got, data) {
				t.Errorf("%s: CRLF is not normalized (%v)", name, err)
			}
		}
	}
}

func TestStaticExportHasNoCanonicalization(t *testing.T) {
	t.Parallel()

	if _, ok := projection.CanonicalizationFor(projection.FormatStatic); ok {
		t.Error("static has a rule")
	}

	if _, err := projection.CanonicalizeExport(projection.FormatStatic, []byte("<html>")); !errors.Is(err, projection.ErrNoCanonicalization) {
		t.Errorf("err = %v", err)
	}

	for format, want := range map[projection.ExportFormat]string{
		projection.FormatJSON: projection.CanonicalizationJSON, projection.FormatMermaid: projection.CanonicalizationText,
		projection.FormatPlantUML: projection.CanonicalizationText, projection.FormatDot: projection.CanonicalizationText,
	} {
		if got, ok := projection.CanonicalizationFor(format); !ok || got != want {
			t.Errorf("CanonicalizationFor(%s) = %q, %v", format, got, ok)
		}
	}
}

func TestCanonicalizeRejectsBrokenJSON(t *testing.T) {
	t.Parallel()

	if _, err := projection.CanonicalizeExport(projection.FormatJSON, []byte("{not json")); err == nil {
		t.Error("no error for broken JSON")
	}
}

// TestRecordedExportsMatchThePinnedCLI runs the pinned CLI on the recorded
// source and compares the canonical bytes with the recording. It needs the CLI
// and Docker or Java, so it runs with CONCORDIA_E2E=1 only.
func TestRecordedExportsMatchThePinnedCLI(t *testing.T) {
	if os.Getenv("CONCORDIA_E2E") != "1" {
		t.Skip("set CONCORDIA_E2E=1 to run the pinned Structurizr CLI")
	}

	cli, ok := projection.FindCLI(os.Getenv)
	if !ok {
		t.Skip("no Structurizr CLI")
	}

	dsl := readRecorded(t, "workspace.dsl")

	for format, files := range recorded {
		byPath := canonicalTestRunCLI(t, cli, format, dsl)

		if len(byPath) != len(files) {
			t.Errorf("%s: the CLI wrote %d files, the recording has %d", format, len(byPath), len(files))
		}

		for _, name := range files {
			canonicalTestCompareRecording(t, format, name, byPath[name])
		}
	}
}

// canonicalTestRunCLI runs the confined CLI for one format and gives the files
// it wrote by path.
func canonicalTestRunCLI(t *testing.T, cli string, format projection.ExportFormat, dsl []byte) map[string][]byte {
	t.Helper()

	res, err := projection.RunConfined(context.Background(), &projection.ConfinedOptions{
		CLI: cli, Format: format, WorkspaceRoot: t.TempDir(), DSL: dsl, Getenv: os.Getenv,
	})
	if err != nil {
		t.Fatalf("%s: %v", format, err)
	}

	byPath := map[string][]byte{}
	for _, f := range res.Files {
		byPath[f.Path] = f.Data
	}

	return byPath
}

// canonicalTestCompareRecording compares the canonical bytes of the CLI output
// with the canonical bytes of the recording called name.
func canonicalTestCompareRecording(t *testing.T, format projection.ExportFormat, name string, produced []byte) {
	t.Helper()

	want, err := projection.CanonicalizeExport(format, readRecorded(t, name))
	if err != nil {
		t.Fatal(err)
	}

	got, err := projection.CanonicalizeExport(format, produced)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the recording.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestHowToDockerWrapperHasNoNetworkAndNamesThePinnedTag keeps the wrapper in
// the how-to in line with the pin and with the documented confinement limit.
func TestHowToDockerWrapperHasNoNetworkAndNamesThePinnedTag(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "how-to", "render-diagrams.md"))
	if err != nil {
		t.Fatal(err)
	}

	wrappers := 0

	for line := range strings.SplitSeq(string(text), "\n") {
		if !strings.Contains(line, "structurizr/cli:") || !strings.Contains(line, " run ") {
			continue
		}

		wrappers++

		if !strings.Contains(line, " --network none ") || !strings.Contains(line, "structurizr/cli:"+projection.PinnedStructurizrCLI+" ") {
			t.Errorf("wrapper does not carry --network none and the tag %s: %s", projection.PinnedStructurizrCLI, line)
		}
	}

	if wrappers < 2 {
		t.Errorf("found %d Docker wrappers, want at least 2", wrappers)
	}
}
