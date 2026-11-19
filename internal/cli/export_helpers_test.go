package cli_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/testutil"
)

// outsideSecret is text that only a file outside the workspace holds.
const outsideSecret = "OUTSIDE-THE-ROOT-CONTENT-4f2a"

// exportTestSkipWithoutShell skips the test where the fake cosign shell script cannot run.
func exportTestSkipWithoutShell(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
}

// exportTestWriteScript writes an executable script.
func exportTestWriteScript(t *testing.T, path, text string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
}

// exportTestBundlePath is the path where `export bundle` writes the archive.
func exportTestBundlePath(s *chkSetup) string {
	return filepath.Join(s.dir, "build", "concordia", "conformance.tar.gz")
}

// exportTestSymlinkOut replaces the file at link with a symbolic link to a file outside the workspace that holds
// content. It skips the test where symbolic links are not available.
func exportTestSymlinkOut(t *testing.T, link, outsideName, content string) {
	t.Helper()

	outside := filepath.Join(t.TempDir(), outsideName)
	if err := os.WriteFile(outside, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
}

// exportTestWantNoSnippets reports every snippet of the bundle files.
func exportTestWantNoSnippets(t *testing.T, files map[string]string) {
	t.Helper()

	for p := range files {
		if strings.HasPrefix(p, "snippets/") {
			t.Errorf("snippet %s of a refused path", p)
		}
	}
}

// markIncomplete sets the status of every run manifest of the workspace to incomplete.
func markIncomplete(t *testing.T, dir string) {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(dir, ".concordia", "runs", "*", "manifest.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("run manifests %v, err %v", paths, err)
	}

	for _, p := range paths {
		exportTestMarkManifestIncomplete(t, p)
	}
}

func exportTestMarkManifestIncomplete(t *testing.T, p string) {
	t.Helper()

	text, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(text, &m); err != nil {
		t.Fatal(err)
	}

	m["status"] = "incomplete"

	if text, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, text, 0o600); err != nil {
		t.Fatal(err)
	}
}

// exportedBundle exports a bundle and gives its path.
func exportedBundle(t *testing.T, s *chkSetup) string {
	t.Helper()

	if res := s.run(t, "export", "bundle"); res.Code != 0 {
		t.Fatalf("export: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return exportTestBundlePath(s)
}

// exportTestTarPutter writes files of a tar archive.
type exportTestTarPutter struct {
	t  *testing.T
	tw *tar.Writer
}

func (p exportTestTarPutter) put(name string, data []byte) {
	p.t.Helper()

	if err := p.tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0)}); err != nil {
		p.t.Fatal(err)
	}

	if _, err := p.tw.Write(data); err != nil {
		p.t.Fatal(err)
	}
}

// exportTestCopyEntries copies the entries of the archive to put; edit sees each file except the manifest.
func exportTestCopyEntries(tr *tar.Reader, put exportTestTarPutter, edit func(name string, data []byte) ([]byte, bool)) {
	for {
		hdr, err := tr.Next()
		if err != nil {
			return
		}

		data, _ := io.ReadAll(tr)
		if hdr.Name == "manifest.json" {
			put.put(hdr.Name, data)

			continue
		}

		if changed, keep := edit(hdr.Name, data); keep {
			put.put(hdr.Name, changed)
		}
	}
}

// tamper rewrites the archive: edit sees each file and may change or drop it; extra adds files.
func tamper(t *testing.T, path string, edit func(name string, data []byte) ([]byte, bool), extra map[string]string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	put := exportTestTarPutter{t: t, tw: tw}

	exportTestCopyEntries(tar.NewReader(gr), put, edit)

	for name, data := range extra {
		put.put(name, []byte(data))
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// lineGraph is the check graph with line numbers on the billing and refund files, so that a
// finding has a source location and a snippet can be asked for.
func lineGraph() *testutil.FakeGraph {
	graph := chkGraph()

	for _, e := range graph.Edges {
		if e.From.Path == chkBillingFile {
			e.From.Line = 2
		}
	}

	return graph
}

// extracted opens the bundle that `export bundle` wrote and gives the text of every file by path.
func extracted(t *testing.T, s *chkSetup) map[string]string {
	t.Helper()

	dir := t.TempDir()

	if _, err := bundle.Open(exportTestBundlePath(s), dir); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = string(data)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

func wantNoText(t *testing.T, files map[string]string, text string) {
	t.Helper()

	for p, content := range files {
		if strings.Contains(content, text) {
			t.Errorf("%s holds %q", p, text)
		}
	}
}

// seededGraph is the check graph with one importing file for each file of the seeded fixture
// repository, below the billing project, with the line of the first seed of the file.
func seededGraph(t *testing.T, seeds []infraSeed) (graph *testutil.FakeGraph, files map[string]string, paths []string) {
	t.Helper()

	store := testutil.FileNode("2", chkStoreFile)
	legacy := testutil.FileNode("3", chkLegacyFile)
	nodes := []testutil.FakeNode{store, legacy}
	files = map[string]string{}

	var edges []testutil.FakeEdge

	for _, seed := range seeds {
		rel := "services/billing/" + seed.Path
		if _, done := files[rel]; done {
			continue
		}

		content := infraFixture(t, seed.Path)
		files[rel] = content
		paths = append(paths, rel)

		node := testutil.FileNode(strconv.Itoa(len(nodes)+10), rel)
		node.Line = lineOf(t, content, seed.Value)
		nodes = append(nodes, node)
		edges = append(edges, testutil.FakeEdge{
			Type: "CALLS", From: &nodes[len(nodes)-1], To: &nodes[0],
			Props: map[string]string{"strategy": "lsp_direct", "api_token": "ghp_CONCORDIASEEDedgeproperty000000000000"},
		})
	}

	return testutil.NewFakeGraph("demo", nodes, edges), files, paths
}
