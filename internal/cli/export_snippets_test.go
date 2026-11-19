package cli_test

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/redact"
)

// exportTestNamesRefusedPath tells whether stderr has the CO-REP-006 diagnostic that names the path.
func exportTestNamesRefusedPath(stderr, path string) bool {
	return strings.Contains(stderr, "CO-REP-006") && strings.Contains(stderr, path)
}

func TestExportBundleRefusesASnippetThroughASymbolicLinkOutOfTheRoot(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, map[string]string{chkBillingFile: "one\ntwo\nthree\n"}, lineGraph())

	link := filepath.Join(s.dir, filepath.FromSlash(chkBillingFile))
	exportTestSymlinkOut(t, link, "outside.ts", "a\n"+outsideSecret+"\nc\n")

	res := s.run(t, "export", "bundle", "--include-source-snippets", "violations")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exportTestNamesRefusedPath(res.Stderr, chkBillingFile) {
		t.Errorf("stderr lacks the CO-REP-006 diagnostic that names the path: %q", res.Stderr)
	}

	files := extracted(t, s)
	wantNoText(t, files, outsideSecret)
	exportTestWantNoSnippets(t, files)
}

func TestExportBundleReadsASnippetBelowTheRoot(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, map[string]string{chkBillingFile: "one\ntwo\nthree\n"}, lineGraph())

	res := s.run(t, "export", "bundle", "--include-source-snippets", "violations")
	if res.Code != 0 || strings.Contains(res.Stderr, "CO-REP-006") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	found := false

	for p, text := range extracted(t, s) {
		if strings.HasPrefix(p, "snippets/") && strings.Contains(text, "two") {
			found = true
		}
	}

	if !found {
		t.Error("no snippet of the file below the root")
	}
}

func TestExportBundleRefusesAProviderPathWithDotDot(t *testing.T) {
	t.Parallel()

	// The provider reports the node at ../../etc/passwd. A glob that matches it binds it to the billing service.
	graph := chkGraph()
	graph.Nodes[0].Path = "../../etc/passwd"
	graph.Edges[0].From.Path = "../../etc/passwd"
	graph.Edges[0].From.Line = 1

	s := newChkSetup(t, map[string]string{
		"concordia.yaml":                   strings.Replace(chkWorkspaceText(""), `include: ["services/**"]`, `include: ["services/**", "*/*/etc/**"]`, 1),
		"bindings/concordia.bindings.yaml": strings.Replace(chkBindings, `"services/billing/**"`, `"*/*/etc/**"`, 1),
	}, graph)

	res := s.run(t, "export", "bundle", "--include-source-snippets", "violations")
	if res.Code > 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exportTestNamesRefusedPath(res.Stderr, "../../etc/passwd") {
		t.Fatalf("stderr lacks the CO-REP-006 diagnostic that names the path: %q", res.Stderr)
	}

	files := extracted(t, s)
	wantNoText(t, files, "root:x:0:0")
	exportTestWantNoSnippets(t, files)
}

func TestExportBundleRefusesAnInputThatIsASymbolicLinkOutOfTheRoot(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	link := filepath.Join(s.dir, "architecture", "a.calm.json")
	exportTestSymlinkOut(t, link, "a.calm.json", chkArch())

	res := s.run(t, "export", "bundle")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exportTestNamesRefusedPath(res.Stderr, "architecture/a.calm.json") {
		t.Errorf("stderr lacks the CO-REP-006 diagnostic that names the path: %q", res.Stderr)
	}

	files := extracted(t, s)

	if _, ok := files["inputs/architecture/a.calm.json"]; ok {
		t.Error("the bundle holds the input that left the root")
	}

	if _, ok := files["inputs/concordia.yaml"]; !ok {
		t.Error("the bundle lacks the workspace input")
	}
}

// exportTestFirstSeedOfEachFile keeps the first seed of each file; it gives the line of the snippet.
func exportTestFirstSeedOfEachFile(seeds []infraSeed) []infraSeed {
	var first []infraSeed

	done := map[string]bool{}

	for _, seed := range seeds {
		if !done[seed.Path] {
			done[seed.Path] = true
			first = append(first, seed)
		}
	}

	return first
}

// exportTestRedaction is one redaction record of the bundle manifest.
type exportTestRedaction struct {
	Path     string `json:"path"`
	File     string `json:"file"`
	Property string `json:"property"`
	Class    string `json:"class"`
	Line     int    `json:"line"`
	Count    int    `json:"count"`
}

// exportTestRedactionManifest is the part of the bundle manifest that records the redactions.
type exportTestRedactionManifest struct {
	RedactionRules string                `json:"redactionRules"`
	Redactions     []exportTestRedaction `json:"redactions"`
}

// exportTestWantRedactionShape checks one record and tells whether it is the record of a snippet.
func exportTestWantRedactionShape(t *testing.T, r *exportTestRedaction) bool {
	t.Helper()

	if r.Class == "" || r.Count < 1 {
		t.Errorf("redaction record %+v", r)
	}

	if !strings.HasPrefix(r.Path, "snippets/") {
		return false
	}

	if r.Line < 1 || r.File == "" {
		t.Errorf("snippet redaction record without file and line: %+v", r)
	}

	return true
}

// exportTestWantRedactionRecords checks the redaction records of the manifest: one for each file of paths.
func exportTestWantRedactionRecords(t *testing.T, manifestText string, paths []string) {
	t.Helper()

	var manifest exportTestRedactionManifest
	if err := json.Unmarshal([]byte(manifestText), &manifest); err != nil {
		t.Fatal(err)
	}

	if manifest.RedactionRules != redact.Version {
		t.Errorf("redactionRules = %q, want %q", manifest.RedactionRules, redact.Version)
	}

	redactedFiles := map[string]bool{}

	for _, r := range manifest.Redactions {
		if exportTestWantRedactionShape(t, &r) {
			redactedFiles[r.File] = true
		}
	}

	for _, p := range paths {
		if !redactedFiles[p] {
			t.Errorf("the manifest has no redaction record for %s: %+v", p, manifest.Redactions)
		}
	}
}

// exportTestWantNoSeeds checks that neither the bundle nor the output and the logs hold a seed.
func exportTestWantNoSeeds(t *testing.T, bundleFiles map[string]string, res result, seeds []infraSeed) {
	t.Helper()

	for _, seed := range seeds {
		wantNoText(t, bundleFiles, seed.Value)

		if strings.Contains(res.Stdout, seed.Value) || strings.Contains(res.Stderr, seed.Value) {
			t.Errorf("the output or the logs have the seed %q of %s", seed.Value, seed.Path)
		}
	}

	wantNoText(t, bundleFiles, "CONCORDIASEEDedgeproperty")

	if strings.Contains(res.Stderr, "CONCORDIASEEDedgeproperty") {
		t.Errorf("the logs have the edge property value: %s", res.Stderr)
	}
}

func TestExportBundleAndLogsHaveNoSeededSecret(t *testing.T) {
	t.Parallel()

	seeds := loadInfraSeeds(t)
	graph, files, paths := seededGraph(t, exportTestFirstSeedOfEachFile(seeds))

	s := newChkSetup(t, files, graph)

	res := s.run(t, "--verbose", "--trace", "export", "bundle", "--include-graph", "--include-source-snippets", "violations")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	bundleFiles := extracted(t, s)

	snippets := 0

	for p := range bundleFiles {
		if strings.HasPrefix(p, "snippets/") {
			snippets++
		}
	}

	if snippets != len(paths) {
		t.Fatalf("%d snippets for %d files with seeds: %v", snippets, len(paths), slices.Collect(maps.Keys(bundleFiles)))
	}

	exportTestWantNoSeeds(t, bundleFiles, res, seeds)
	exportTestWantRedactionRecords(t, bundleFiles["manifest.json"], paths)

	if n := strings.Count(res.Stderr, "CO-REP-005"); n < len(paths) {
		t.Errorf("%d CO-REP-005 notices for %d snippets:\n%s", n, len(paths), res.Stderr)
	}
}

func TestExportBundleSnippetOfATfvarsFile(t *testing.T) {
	t.Parallel()

	content := infraFixture(t, "terraform/prod.tfvars")
	rel := "services/billing/terraform/prod.tfvars"

	graph := chkGraph()
	graph.Nodes[0].Path = rel
	graph.Edges[0].From.Path = rel
	graph.Edges[0].From.Line = lineOf(t, content, "CONCORDIA-SEED-tfvars-6e4d")
	// An IMPORTS edge from a `.tfvars` file is not a code import (EVD-06); CALLS does not depend on the file type.
	graph.Edges[0].Type = "CALLS"
	graph.Edges[0].Props = map[string]string{"strategy": "lsp_direct"}

	s := newChkSetup(t, map[string]string{rel: content}, graph)

	res := s.run(t, "export", "bundle", "--include-source-snippets", "violations")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stderr, "CO-REP-005", "redaction applied: 1 value(s) removed from snippets/")

	found := false

	for p, text := range extracted(t, s) {
		if !strings.HasPrefix(p, "snippets/") {
			continue
		}

		found = true

		wantContains(t, text, "db_password = [REDACTED:secret-data]")

		if strings.Contains(text, "CONCORDIA-SEED-tfvars-6e4d") {
			t.Errorf("snippet %s has the seed:\n%s", p, text)
		}
	}

	if !found {
		t.Error("no snippet")
	}
}
