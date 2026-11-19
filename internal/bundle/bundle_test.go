package bundle_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/exceptions"
)

// allTrue tells if every condition is true. It lets a check name its conditions without a long boolean expression.
func allTrue(conditions ...bool) bool {
	return !slices.Contains(conditions, false)
}

func sample() (bundle.Manifest, []bundle.Entry) {
	m := bundle.Manifest{
		SchemaVersion: bundle.SchemaVersion, Kind: bundle.Kind, CreatedAt: "2026-01-01T00:00:00Z", Concordia: "test",
		Toolchain: json.RawMessage(`{}`), Run: json.RawMessage(`{"id":"r"}`),
		Options: bundle.BundleOptions{IncludeSourceSnippets: "none"},
	}

	return m, []bundle.Entry{{Path: "b/two.txt", Data: []byte("two")}, {Path: "a/one.txt", Data: []byte("one")}}
}

func write(t *testing.T) []byte {
	t.Helper()

	m, e := sample()

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, e); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestBundleWriteIsDeterministic(t *testing.T) {
	if !bytes.Equal(write(t), write(t)) {
		t.Fatal("two writes differ")
	}
}

func TestBundleOpenGivesManifest(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(archive, write(t), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	m, err := bundle.Open(archive, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(m.Files) != 2 || !allTrue(m.Files[0].Path == "a/one.txt", m.Kind == bundle.Kind) {
		t.Fatalf("manifest: %+v", m)
	}

	if got, _ := os.ReadFile(filepath.Join(dir, "a", "one.txt")); string(got) != "one" {
		t.Fatalf("extracted: %q", got)
	}
}

// rebuild writes an archive from raw tar entries.
func rebuild(t *testing.T, mutate func(name string, data []byte) (string, []byte)) string {
	t.Helper()

	gr, err := gzip.NewReader(bytes.NewReader(write(t)))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}

		var data bytes.Buffer

		_, _ = data.ReadFrom(tr)
		name, content := mutate(hdr.Name, data.Bytes())

		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), ModTime: time.Unix(0, 0)})
		_, _ = tw.Write(content)
	}

	_ = tw.Close()
	_ = gz.Close()

	p := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := os.WriteFile(p, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func wantREP004(t *testing.T, err error) {
	t.Helper()

	de, ok := err.(*diag.Error) //nolint:errorlint // direct type.
	if !ok || de.Diagnostic.Code != diag.REP004 {
		t.Fatalf("want CO-REP-004, got %v", err)
	}
}

func TestBundleOpenRejectsChangedFile(t *testing.T) {
	p := rebuild(t, func(name string, data []byte) (string, []byte) {
		if name == "a/one.txt" {
			return name, []byte("ONE")
		}

		return name, data
	})

	_, err := bundle.Open(p, t.TempDir())
	wantREP004(t, err)
}

func TestBundleOpenRejectsEscapingEntry(t *testing.T) {
	p := rebuild(t, func(name string, data []byte) (string, []byte) {
		if name == "a/one.txt" {
			return "../x", data
		}

		return name, data
	})

	_, err := bundle.Open(p, t.TempDir())
	wantREP004(t, err)
}

func archiveOf(t *testing.T, data []byte) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

// wantSampleIntegrity checks the integrity of the bundle of sample: the digest level, a digest for every file and
// the manifest digest.
func wantSampleIntegrity(t *testing.T, got *bundle.Manifest) {
	t.Helper()

	in := got.Integrity
	if !allTrue(in.Algorithm == "sha256", in.Level == bundle.LevelDigest, in.ManifestDigest != "", len(in.Files) == 2) ||
		in.Files["a/one.txt"] != got.Files[0].SHA256 {
		t.Errorf("integrity: %+v", in)
	}
}

// wantUnavailableGraphSnapshot checks that the manifest says that a full reproduction is not available
// because of the graph snapshot.
func wantUnavailableGraphSnapshot(t *testing.T, got *bundle.Manifest) {
	t.Helper()

	if r := got.Reproduction; !allTrue(!r.Available, slices.Equal(r.Unavailable, []string{"graph-snapshot"}), r.Reason != "") {
		t.Errorf("reproduction: %+v", got.Reproduction)
	}
}

func TestBundleManifestRecordsIntegrityReferencesAndReproduction(t *testing.T) {
	m, e := sample()
	m.References = []bundle.Reference{
		{Kind: "architecture", Digest: "sha256:aa", Availability: bundle.Included},
		{Kind: "graph-snapshot", Digest: "cbm:x:1", Availability: bundle.Unavailable, Reason: bundle.ReasonNotIncluded},
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, e); err != nil {
		t.Fatal(err)
	}

	got, err := bundle.Open(archiveOf(t, buf.Bytes()), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	wantSampleIntegrity(t, got)
	wantUnavailableGraphSnapshot(t, got)

	if got.Redactions == nil || len(got.Redactions) != 0 {
		t.Errorf("redactions: %#v", got.Redactions)
	}

	if len(got.References) != 2 || got.References[1].Reason != bundle.ReasonNotIncluded {
		t.Errorf("references: %+v", got.References)
	}
}

func TestBundleReproductionIsAvailableWhenAllReferencesAreIncluded(t *testing.T) {
	m, e := sample()
	m.References = []bundle.Reference{{Kind: "architecture", Digest: "sha256:aa", Availability: bundle.Included}}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, e); err != nil {
		t.Fatal(err)
	}

	got, err := bundle.Open(archiveOf(t, buf.Bytes()), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if !got.Reproduction.Available || len(got.Reproduction.Unavailable) != 0 {
		t.Errorf("reproduction: %+v", got.Reproduction)
	}
}

func TestBundleVerifyCleanBundle(t *testing.T) {
	m, err := bundle.Verify(archiveOf(t, write(t)))
	if err != nil || m.Kind != bundle.Kind {
		t.Fatalf("%v %v", m, err)
	}
}

func TestBundleVerifyRejectsChangedFileAndNamesIt(t *testing.T) {
	p := rebuild(t, func(name string, data []byte) (string, []byte) {
		if name == "a/one.txt" {
			return name, []byte("ONE")
		}

		return name, data
	})

	_, err := bundle.Verify(p)
	wantREP004(t, err)

	if !strings.Contains(err.Error(), "a/one.txt") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestBundleVerifyRejectsMissingFile(t *testing.T) {
	// A renamed entry leaves the listed file missing and the other one extra.
	p := rebuild(t, func(name string, data []byte) (string, []byte) {
		if name == "a/one.txt" {
			return "c/three.txt", data
		}

		return name, data
	})

	_, err := bundle.Verify(p)
	wantREP004(t, err)
}

func TestBundleVerifyRejectsExtraFile(t *testing.T) {
	gr, err := gzip.NewReader(bytes.NewReader(write(t)))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)

	for tr := tar.NewReader(gr); ; {
		hdr, err := tr.Next()
		if err != nil {
			break
		}

		var data bytes.Buffer

		_, _ = data.ReadFrom(tr)
		_ = tw.WriteHeader(&tar.Header{Name: hdr.Name, Mode: 0o644, Size: int64(data.Len()), ModTime: time.Unix(0, 0)})
		_, _ = tw.Write(data.Bytes())
	}

	_ = tw.WriteHeader(&tar.Header{Name: "extra.txt", Mode: 0o644, Size: 1, ModTime: time.Unix(0, 0)})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()

	_, err = bundle.Verify(archiveOf(t, out.Bytes()))
	wantREP004(t, err)

	if !strings.Contains(err.Error(), "extra.txt") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestBundleVerifyRejectsEditedManifest(t *testing.T) {
	p := rebuild(t, func(name string, data []byte) (string, []byte) {
		if name == bundle.ManifestPath {
			return name, bytes.Replace(data, []byte(`"concordia": "test"`), []byte(`"concordia": "forged"`), 1)
		}

		return name, data
	})

	_, err := bundle.Verify(p)
	wantREP004(t, err)

	if !strings.Contains(err.Error(), "manifestDigest") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBundleVerifyWritesNothingToTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	if _, err := bundle.Verify(archiveOf(t, write(t))); err != nil {
		t.Fatal(err)
	}

	left, err := os.ReadDir(tmp)
	if err != nil || len(left) != 0 {
		t.Errorf("verify left files in the temporary directory: %v %v", left, err)
	}
}

func TestBundleVerifyRejectsTooManyEntries(t *testing.T) {
	m, _ := sample()

	entries := make([]bundle.Entry, 0, 10_001)
	for i := range 10_001 {
		entries = append(entries, bundle.Entry{Path: fmt.Sprintf("f/%05d.txt", i), Data: []byte("x")})
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, entries); err != nil {
		t.Fatal(err)
	}

	_, err := bundle.Verify(archiveOf(t, buf.Bytes()))
	wantREP004(t, err)

	if !strings.Contains(err.Error(), "more than") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBundleVerifyCountsDirectoryHeadersAgainstTheEntryLimit(t *testing.T) {
	var buf bytes.Buffer

	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for i := range 10_001 {
		hdr := &tar.Header{Name: fmt.Sprintf("d%05d/", i), Typeflag: tar.TypeDir, Mode: 0o755}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := bundle.Verify(archiveOf(t, buf.Bytes()))
	wantREP004(t, err)

	if !strings.Contains(err.Error(), "more than 10000 entries") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestBundleVerifyBoundsHeaderRecordsInTheStream: tar.Reader consumes a local PAX header inside
// Next, so neither the entry nor the content budget sees its bytes. The stream limit does.
func TestBundleVerifyBoundsHeaderRecordsInTheStream(t *testing.T) {
	bundle.SetMaxStreamSize(t, 64<<10)

	var buf bytes.Buffer

	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o644, Format: tar.FormatPAX,
		PAXRecords: map[string]string{"comment": strings.Repeat("x", 512<<10)},
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := bundle.Verify(archiveOf(t, buf.Bytes()))
	wantREP004(t, err)

	if !strings.Contains(err.Error(), "larger than the limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBundleVerifyAcceptsAStreamOfExactlyTheLimit(t *testing.T) {
	archive := archiveOf(t, write(t))

	size := decompressedSize(t, archive)
	bundle.SetMaxStreamSize(t, size)

	if _, err := bundle.Verify(archive); err != nil {
		t.Fatalf("a stream of exactly the limit: %v", err)
	}

	bundle.SetMaxStreamSize(t, size-1)

	_, err := bundle.Verify(archive)
	wantREP004(t, err)
}

func decompressedSize(t *testing.T, archive string) int64 {
	t.Helper()

	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}

	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	n, err := io.Copy(io.Discard, gzr)
	if err != nil {
		t.Fatal(err)
	}

	return n
}

// viewsBundle writes a bundle with the given view entries and files and gives its archive path.
func viewsBundle(t *testing.T, views []bundle.View, entries ...bundle.Entry) string {
	t.Helper()

	m, e := sample()
	m.Views = views

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, append(e, entries...)); err != nil {
		t.Fatal(err)
	}

	return archiveOf(t, buf.Bytes())
}

func sumOf(data string) string { return digest.OfBytes([]byte(data)) }

// Scenario "Bundle content": the manifest, the artifacts and the projection of the view are in the bundle, and the
// manifest lists them with their digests.
func TestBundleViewsBundleContent(t *testing.T) {
	files := map[string]string{
		"views/payments/manifest.json":                      `{"schema":"concordia-view-manifest/1"}`,
		"views/payments/projection.json":                    `{"model":{}}`,
		"views/payments/artifacts/docs/views/payments.dsl":  "workspace {}",
		"views/payments/artifacts/docs/views/payments.mmd":  "graph TD",
		"views/context/artifacts/docs/arch/payments-ctx.md": "# context",
		"views/context/projection.json":                     `{"model":{}}`,
	}

	entries := make([]bundle.Entry, 0, len(files))
	for p, data := range files {
		entries = append(entries, bundle.Entry{Path: p, Data: []byte(data)})
	}

	views := contentViews(files)
	archive := viewsBundle(t, views, entries...)

	if _, err := bundle.Verify(archive); err != nil {
		t.Fatalf("verify: %v", err)
	}

	dir := t.TempDir()

	got, err := bundle.Open(archive, dir)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got.Views, views) {
		t.Errorf("views = %+v\nwant %+v", got.Views, views)
	}

	wantFilesExtractedAndListed(t, dir, got, files)

	// The same input gives the same archive.
	again := viewsBundle(t, views, entries...)

	a, _ := os.ReadFile(archive)
	b, _ := os.ReadFile(again)

	if !bytes.Equal(a, b) {
		t.Error("two writes of the same view bundle differ")
	}
}

// contentViews gives the views of the "Bundle content" scenario. Their files are in files by path.
func contentViews(files map[string]string) []bundle.View {
	file := func(role, repo, in string) bundle.ViewFile {
		return bundle.ViewFile{Role: role, Path: repo, State: bundle.StatePresent, File: in, SHA256: sumOf(files[in])}
	}

	return []bundle.View{
		{ID: "context", Origin: "authored", State: bundle.StatePresent, Files: []bundle.ViewFile{
			file("source", "docs/arch/payments-ctx.md", "views/context/artifacts/docs/arch/payments-ctx.md"),
			file(bundle.RoleProjection, "", "views/context/projection.json"),
		}},
		{ID: "payments", Origin: "generated", State: bundle.StatePresent, Files: []bundle.ViewFile{
			file("manifest", "docs/views/payments.dsl.manifest.json", "views/payments/manifest.json"),
			file("output", "docs/views/payments.dsl", "views/payments/artifacts/docs/views/payments.dsl"),
			file("export", "docs/views/payments.mmd", "views/payments/artifacts/docs/views/payments.mmd"),
			file(bundle.RoleProjection, "", "views/payments/projection.json"),
		}},
	}
}

// wantFilesExtractedAndListed checks that Open extracted every file into dir and that the manifest lists it
// with its digest.
func wantFilesExtractedAndListed(t *testing.T, dir string, got *bundle.Manifest, files map[string]string) {
	t.Helper()

	for p, want := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", p, data, err, want)
		}

		if !slices.ContainsFunc(got.Files, func(f bundle.ManifestFile) bool { return f.Path == p && f.SHA256 == sumOf(want) }) {
			t.Errorf("manifest files do not list %s with its digest", p)
		}
	}
}

// A bundle without views has no `views` key: it is the bundle of before.
func TestBundleWithoutViewsHasNoViewsKey(t *testing.T) {
	dir := t.TempDir()

	if _, err := bundle.Open(archiveOf(t, write(t)), dir); err != nil {
		t.Fatal(err)
	}

	text, err := os.ReadFile(filepath.Join(dir, bundle.ManifestPath))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(text, []byte(`"views"`)) {
		t.Errorf("manifest has a views key: %s", text)
	}
}

// Scenario "Absent artifact": a declared artifact that is not in the revision is listed with the state `absent`
// and has no content; the view is `absent` too, and the bundle verifies.
func TestBundleViewsAbsentArtifact(t *testing.T) {
	dsl := "workspace {}"
	views := []bundle.View{{ID: "payments", Origin: "generated", State: bundle.StateAbsent, Files: []bundle.ViewFile{
		{Role: "manifest", Path: "docs/views/payments.dsl.manifest.json", State: bundle.StateAbsent},
		{Role: "output", Path: "docs/views/payments.dsl", State: bundle.StatePresent, File: "views/payments/artifacts/docs/views/payments.dsl", SHA256: sumOf(dsl)},
	}}}

	archive := viewsBundle(t, views, bundle.Entry{Path: "views/payments/artifacts/docs/views/payments.dsl", Data: []byte(dsl)})

	got, err := bundle.Verify(archive)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if len(got.Views) != 1 || !allTrue(
		got.Views[0].State == bundle.StateAbsent,
		got.Views[0].Files[0].State == bundle.StateAbsent,
	) {
		t.Fatalf("views = %+v", got.Views)
	}

	for _, f := range got.Files {
		if strings.Contains(f.Path, "manifest.json") && strings.HasPrefix(f.Path, bundle.ViewsDir) {
			t.Errorf("the absent manifest has content: %s", f.Path)
		}
	}
}

func TestBundleVerifyRejectsInconsistentViewEntries(t *testing.T) {
	const in = "views/v/artifacts/a.dsl"

	present := bundle.ViewFile{Role: "output", Path: "a.dsl", State: bundle.StatePresent, File: in, SHA256: sumOf("dsl")}

	for name, v := range map[string]bundle.View{
		"a digest that is not the file digest":       {ID: "v", State: bundle.StatePresent, Files: []bundle.ViewFile{{Role: "output", Path: "a.dsl", State: bundle.StatePresent, File: in, SHA256: sumOf("other")}}},
		"a file that is not in the bundle":           {ID: "v", State: bundle.StatePresent, Files: []bundle.ViewFile{{Role: "output", Path: "b.dsl", State: bundle.StatePresent, File: "views/v/artifacts/b.dsl", SHA256: sumOf("dsl")}}},
		"an absent file with content":                {ID: "v", State: bundle.StateAbsent, Files: []bundle.ViewFile{{Role: "output", Path: "a.dsl", State: bundle.StateAbsent, File: in, SHA256: sumOf("dsl")}}},
		"a state that is not present or absent":      {ID: "v", State: bundle.StatePresent, Files: []bundle.ViewFile{{Role: "output", Path: "a.dsl", State: "missing"}}},
		"a view that is present with an absent file": {ID: "v", State: bundle.StatePresent, Files: []bundle.ViewFile{present, {Role: "export", Path: "a.mmd", State: bundle.StateAbsent}}},
		"a view that is absent with all files":       {ID: "v", State: bundle.StateAbsent, Files: []bundle.ViewFile{present}},
		"a file outside views/":                      {ID: "v", State: bundle.StatePresent, Files: []bundle.ViewFile{{Role: "output", Path: "a.dsl", State: bundle.StatePresent, File: "a/one.txt", SHA256: sumOf("one")}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bundle.Verify(viewsBundle(t, []bundle.View{v}, bundle.Entry{Path: in, Data: []byte("dsl")}))
			wantREP004(t, err)
		})
	}

	t.Run("a view that is listed twice", func(t *testing.T) {
		v := bundle.View{ID: "v", State: bundle.StatePresent, Files: []bundle.ViewFile{present}}

		_, err := bundle.Verify(viewsBundle(t, []bundle.View{v, v}, bundle.Entry{Path: in, Data: []byte("dsl")}))
		wantREP004(t, err)
	})
}

// waiverRecord is a scoped waiver as the bundle file holds it.
type waiverRecord struct {
	ID           string         `json:"id"`
	Scope        map[string]any `json:"scope"`
	Approver     map[string]any `json:"approver"`
	Verification map[string]any `json:"verification"`
	Expires      string         `json:"expires"`
	Covered      []string       `json:"coveredFindings"`
	Evidence     []struct {
		State string `json:"state"`
	} `json:"compensatingEvidence"`
}

// waiversDoc is the scoped waivers file of a bundle.
type waiversDoc struct {
	SchemaVersion string         `json:"schemaVersion"`
	ScopedWaivers []waiverRecord `json:"scopedWaivers"`
}

// scopedWaiverRun gives a run with one scoped waiver.
func scopedWaiverRun() *conformance.Run {
	return &conformance.Run{Exceptions: conformance.RunExceptions{ScopedWaivers: []conformance.ScopedWaiver{{
		ID: "WV-12", Rule: "REV-APR-001", Scope: exceptions.Scope{Subject: "ASM-EDGE", Environment: "production"},
		Owner: "team-platform", Expires: "2027-01-31", State: "active", Digest: "sha256:" + strings.Repeat("c", 64),
		Approver: exceptions.Approver{Identity: "alice@example.com", Authority: "exception-board"},
		Verification: exceptions.Verification{
			Verified: true, Mechanism: "protected-review", Authority: "exception-board", Signer: "alice@example.com",
		},
		CompensatingEvidence: []exceptions.EvidenceResult{{Path: "docs/mitigation.md", SHA256: "sha256:" + strings.Repeat("d", 64), State: exceptions.EvidenceUnresolved}},
		CoveredFindings:      []string{"sha256:" + strings.Repeat("a", 64)},
	}}}}
}

// readWaiversDoc reads and decodes the scoped waivers file that Open extracted into dir.
func readWaiversDoc(t *testing.T, dir string) (doc waiversDoc, text []byte) {
	t.Helper()

	text, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(bundle.ScopedWaiversPath)))
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}

	return doc, text
}

// isExpectedWaiverRecord tells if the record is the waiver of scopedWaiverRun.
func isExpectedWaiverRecord(w *waiverRecord) bool {
	if len(w.Evidence) != 1 {
		return false
	}

	return allTrue(
		w.ID == "WV-12",
		w.Scope["subject"] == "ASM-EDGE",
		w.Approver["identity"] == "alice@example.com",
		w.Verification["verified"] == true,
		w.Expires == "2027-01-31",
		len(w.Covered) == 1,
		w.Evidence[0].State == "unresolved",
	)
}

// openScopedWaiverBundle writes a bundle with the scoped waivers file of scopedWaiverRun, opens it and gives
// the manifest and the directory with the extracted files.
func openScopedWaiverBundle(t *testing.T) (opened *bundle.Manifest, dir string) {
	t.Helper()

	content, err := conformance.ScopedWaiversJSON(scopedWaiverRun())
	if err != nil {
		t.Fatal(err)
	}

	m, _ := sample()

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, []bundle.Entry{{Path: bundle.ScopedWaiversPath, Data: content}}); err != nil {
		t.Fatal(err)
	}

	dir = t.TempDir()

	opened, err = bundle.Open(archiveOf(t, buf.Bytes()), dir)
	if err != nil {
		t.Fatal(err)
	}

	return opened, dir
}

// Scenario: Bundle records waivers.
func TestBundleHoldsTheScopedWaiversWithoutSecrets(t *testing.T) {
	const secret = "sk_live_9f8e7d6c5b4a39281706"

	opened, dir := openScopedWaiverBundle(t)

	if _, ok := opened.Integrity.Files["exceptions/scoped.json"]; !ok || bundle.ScopedWaiversPath != "exceptions/scoped.json" {
		t.Fatalf("integrity = %+v", opened.Integrity.Files)
	}

	doc, data := readWaiversDoc(t, dir)
	if doc.SchemaVersion != "1.0" || len(doc.ScopedWaivers) != 1 {
		t.Fatalf("document = %s", data)
	}

	if w := &doc.ScopedWaivers[0]; !isExpectedWaiverRecord(w) {
		t.Errorf("record = %+v", *w)
	}

	if strings.Contains(string(data), secret) || strings.Contains(string(data), `"reason"`) {
		t.Errorf("the file holds free text of a waiver: %s", data)
	}
}

// Scenario: Reference no longer resolvable.
// A reference that is marked not reproducible is unavailable: the manifest says that a full reproduction is not
// available, and the review provenance survives the archive.
func TestBundleMarksAnUnresolvedAssessmentReferenceNotReproducible(t *testing.T) {
	t.Parallel()

	m, entries := sample()
	m.Provenance.Review = &bundle.ReviewRef{
		ContractVersion: "review-digest/1", CapabilityProfile: "cbm-0.12.1", AuthorityDigest: "sha256:" + strings.Repeat("a", 64), TrustAnchor: "abc123",
	}
	m.References = []bundle.Reference{
		{Kind: "assessment", Digest: "sha256:" + strings.Repeat("b", 64), Availability: bundle.Included},
		{Kind: "assessment-evidence", Digest: "sha256:" + strings.Repeat("c", 64), Availability: bundle.Unavailable, Reason: bundle.ReasonNotReproducible},
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &m, entries); err != nil {
		t.Fatal(err)
	}

	opened, err := bundle.Open(archiveOf(t, buf.Bytes()), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if opened.Reproduction.Available || !slices.Equal(opened.Reproduction.Unavailable, []string{"assessment-evidence"}) {
		t.Errorf("reproduction = %+v", opened.Reproduction)
	}

	if opened.Provenance.Review == nil || !allTrue(
		opened.Provenance.Review.TrustAnchor == "abc123",
		opened.Provenance.Review.ContractVersion == "review-digest/1",
	) {
		t.Errorf("review = %+v", opened.Provenance.Review)
	}
}
