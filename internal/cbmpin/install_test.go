package cbmpin_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cbmpin"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
)

const testBinary = "cbm-test"

var testContent = []byte("#!/bin/sh\necho cbm\n")

// entry is one file of a test archive.
type entry struct {
	name string
	body []byte
}

// tarGz gives a .tar.gz archive.
func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// zipArchive gives a .zip archive.
func zipArchive(t *testing.T, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// assetOf gives the asset of an archive: its name and the digests of the archive and of the binary content.
func assetOf(t *testing.T, name string, archive, binary []byte) *cbmpin.Asset {
	t.Helper()

	text := pinText(
		row("version", "v1.2.3"),
		row("repository", "acme/cbm"),
		row("linux-amd64", name, digest.SHA256Hex(archive), testBinary, digest.SHA256Hex(binary)),
	)

	pin, err := cbmpin.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	asset, _ := pin.Asset("linux", "amd64")

	return &asset
}

// serve starts a TLS server that gives the body for every path and counts the requests.
func serve(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// request makes the install request for an asset on a server.
func request(srv *httptest.Server, asset *cbmpin.Asset, dir string) *cbmpin.Request {
	return &cbmpin.Request{Client: srv.Client(), URL: srv.URL + "/" + asset.Archive, Dir: dir, Asset: *asset}
}

// wantFailure checks that the error is CO-OBS-006 with exit code 6 and gives its message.
func wantFailure(t *testing.T, err error) string {
	t.Helper()

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want a *diag.Error", err)
	}

	if failure.Diagnostic.Code != "CO-OBS-006" || failure.ExitCode != diag.Network {
		t.Fatalf("diagnostic = %s, exit code = %d", failure.Diagnostic.Code, failure.ExitCode)
	}

	return failure.Diagnostic.Message
}

// wantNoFile checks that a directory has no file, not even a temporary one.
func wantNoFile(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Errorf("%s has %d entries, want none", dir, len(entries))
	}
}

func TestInstallExtractsTheBinaryOfAnArchive(t *testing.T) {
	t.Parallel()

	extras := make([]entry, 0, 3)
	extras = append(extras, entry{"LICENSE", []byte("license")}, entry{"./sub/install.sh", []byte("#!/bin/sh")})

	cases := []struct {
		name    string
		archive []byte
	}{
		{"cbm.tar.gz", tarGz(t, append([]entry{{testBinary, testContent}}, extras...)...)},
		{"cbm-dot.tar.gz", tarGz(t, append([]entry{{"./" + testBinary, testContent}}, extras...)...)},
		{"cbm.zip", zipArchive(t, append([]entry{{testBinary, testContent}}, extras...)...)},
		{"cbm-last.zip", zipArchive(t, append(extras, entry{testBinary, testContent})...)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asset := assetOf(t, tc.name, tc.archive, testContent)
			srv := serve(t, http.StatusOK, tc.archive)
			dir := filepath.Join(t.TempDir(), "new", "bin")

			path, err := cbmpin.Install(t.Context(), request(srv, asset, dir))
			if err != nil {
				t.Fatal(err)
			}

			if path != filepath.Join(dir, testBinary) {
				t.Errorf("path = %q", path)
			}

			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, testContent) {
				t.Errorf("content = %q, %v", got, err)
			}

			checkMode(t, path)

			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("the directory has %d entries, want only the binary", len(entries))
			}
		})
	}
}

// checkMode checks the mode 0755 of the installed file. Windows has no such mode.
func checkMode(t *testing.T, path string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestInstallRefusesAnArchiveWithAnotherDigest(t *testing.T) {
	t.Parallel()

	good := tarGz(t, entry{testBinary, testContent})
	other := tarGz(t, entry{testBinary, []byte("another build")})
	asset := assetOf(t, "cbm.tar.gz", good, testContent)
	srv := serve(t, http.StatusOK, other)
	dir := t.TempDir()

	_, err := cbmpin.Install(t.Context(), request(srv, asset, dir))

	if msg := wantFailure(t, err); !strings.Contains(msg, "digest") || !strings.Contains(msg, asset.ArchiveSHA256) {
		t.Errorf("message = %q", msg)
	}

	wantNoFile(t, dir)
}

func TestInstallRefusesABinaryWithAnotherDigest(t *testing.T) {
	t.Parallel()

	archive := tarGz(t, entry{testBinary, []byte("another build")})
	asset := assetOf(t, "cbm.tar.gz", archive, testContent)
	srv := serve(t, http.StatusOK, archive)
	dir := t.TempDir()

	_, err := cbmpin.Install(t.Context(), request(srv, asset, dir))

	if msg := wantFailure(t, err); !strings.Contains(msg, "the binary "+testBinary) || !strings.Contains(msg, asset.BinarySHA256) {
		t.Errorf("message = %q", msg)
	}

	wantNoFile(t, dir)
}

func TestInstallRefusesArchiveEntriesWithAnUnsafePath(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"../evil", "a/../../evil", "/etc/evil", `..\evil`, `C:\evil`} {
		for _, kind := range []string{"tar.gz", "zip"} {
			t.Run(kind+" "+name, func(t *testing.T) {
				t.Parallel()

				entries := []entry{{testBinary, testContent}, {name, []byte("x")}}

				archive := tarGz(t, entries...)
				if kind == "zip" {
					archive = zipArchive(t, entries...)
				}

				asset := assetOf(t, "cbm."+kind, archive, testContent)
				srv := serve(t, http.StatusOK, archive)
				parent := t.TempDir()
				dir := filepath.Join(parent, "bin")

				_, err := cbmpin.Install(t.Context(), request(srv, asset, dir))

				if msg := wantFailure(t, err); !strings.Contains(msg, "unsafe path") {
					t.Errorf("message = %q", msg)
				}

				wantNoFile(t, dir)
				wantNoFile(t, filepath.Join(parent, "evil"))
			})
		}
	}
}

func TestInstallNeedsTheBinaryExactlyOnce(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, want string }{
		{"missing", "no entry " + testBinary},
		{"twice", "not one regular file"},
		{"nested", "no entry " + testBinary},
	}

	for _, tc := range cases {
		for _, kind := range []string{"tar.gz", "zip"} {
			t.Run(tc.name+" "+kind, func(t *testing.T) {
				t.Parallel()

				entries := map[string][]entry{
					"missing": {{"LICENSE", []byte("x")}},
					"twice":   {{testBinary, testContent}, {testBinary, testContent}},
					"nested":  {{"bin/" + testBinary, testContent}},
				}[tc.name]

				archive := tarGz(t, entries...)
				if kind == "zip" {
					archive = zipArchive(t, entries...)
				}

				asset := assetOf(t, "cbm."+kind, archive, testContent)
				srv := serve(t, http.StatusOK, archive)
				dir := t.TempDir()

				_, err := cbmpin.Install(t.Context(), request(srv, asset, dir))

				if msg := wantFailure(t, err); !strings.Contains(msg, tc.want) {
					t.Errorf("message = %q, want %q", msg, tc.want)
				}

				wantNoFile(t, dir)
			})
		}
	}
}

func TestInstallRefusesAnArchiveThatIsNotAnArchive(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"cbm.tar.gz", "cbm.zip"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			junk := []byte("this is not an archive")
			asset := assetOf(t, name, junk, testContent)
			srv := serve(t, http.StatusOK, junk)
			dir := t.TempDir()

			_, err := cbmpin.Install(t.Context(), request(srv, asset, dir))

			if msg := wantFailure(t, err); !strings.Contains(msg, "not valid") {
				t.Errorf("message = %q", msg)
			}

			wantNoFile(t, dir)
		})
	}
}

func TestInstallFailsOnHTTPErrorsAndUnreachableServers(t *testing.T) {
	t.Parallel()

	archive := tarGz(t, entry{testBinary, testContent})
	asset := assetOf(t, "cbm.tar.gz", archive, testContent)

	t.Run("status 404", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		_, err := cbmpin.Install(t.Context(), request(serve(t, http.StatusNotFound, nil), asset, dir))

		if msg := wantFailure(t, err); !strings.Contains(msg, "HTTP status 404") {
			t.Errorf("message = %q", msg)
		}

		wantNoFile(t, dir)
	})

	t.Run("server is down", func(t *testing.T) {
		t.Parallel()

		srv := serve(t, http.StatusOK, archive)
		req := request(srv, asset, t.TempDir())
		srv.Close()

		_, err := cbmpin.Install(t.Context(), req)

		if msg := wantFailure(t, err); !strings.Contains(msg, "download failed") || strings.Contains(msg, srv.URL) {
			t.Errorf("message = %q", msg)
		}
	})
}

func TestInstallUsesHTTPSOnly(t *testing.T) {
	t.Parallel()

	archive := tarGz(t, entry{testBinary, testContent})
	asset := assetOf(t, "cbm.tar.gz", archive, testContent)
	dir := t.TempDir()

	_, err := cbmpin.Install(t.Context(), &cbmpin.Request{URL: "http://example.invalid/cbm.tar.gz", Dir: dir, Asset: *asset})

	if msg := wantFailure(t, err); !strings.Contains(msg, "https") {
		t.Errorf("message = %q", msg)
	}

	wantNoFile(t, dir)
}
