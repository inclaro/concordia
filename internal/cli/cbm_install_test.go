package cli_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inclaro/concordia/internal/cbmpin"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/digest"
)

var pinnedContent = []byte("#!/bin/sh\necho pinned build\n")

// cbmBinaryName is the name of the CBM executable on this platform.
func cbmBinaryName() string {
	if runtime.GOOS == "windows" {
		return "codebase-memory-mcp.exe"
	}

	return "codebase-memory-mcp"
}

// cbmArchive gives an archive that holds the binary and a license file. The name decides the format.
func cbmArchive(t *testing.T, name string, files map[string][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)

		for fileName, body := range files {
			w, err := zw.Create(fileName)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := w.Write(body); err != nil {
				t.Fatal(err)
			}
		}

		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for fileName, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: fileName, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write(body); err != nil {
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

// cbmRelease is a fake CBM release: a pin for this platform and a TLS server that serves its archive.
type cbmRelease struct {
	pin  *cbmpin.Pin
	srv  *httptest.Server
	hits *atomic.Int32
}

// pinFor gives a pin for this platform with the archive name and the digests (`sha256:<hex>` or hex).
func pinFor(t *testing.T, archive, archiveSum, binarySum string) *cbmpin.Pin {
	t.Helper()

	text := strings.Join([]string{
		"version\tv9.9.9",
		"repository\tacme/cbm",
		strings.Join([]string{runtime.GOOS + "-" + runtime.GOARCH, archive, strings.TrimPrefix(archiveSum, digest.Prefix), cbmBinaryName(), strings.TrimPrefix(binarySum, digest.Prefix)}, "\t"),
	}, "\n") + "\n"

	pin, err := cbmpin.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	return pin
}

// newRelease makes a release whose archive has the name (.tar.gz or .zip) and the files. The pin has the digests of
// the archive and of pinnedContent. serve is the archive that the server gives; nil gives the real archive.
func newRelease(t *testing.T, name string, files, serve map[string][]byte) *cbmRelease {
	t.Helper()

	archive := cbmArchive(t, name, files)
	body := archive

	if serve != nil {
		body = cbmArchive(t, name, serve)
	}

	hits := &atomic.Int32{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)

		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return &cbmRelease{
		pin:  pinFor(t, name, digest.OfBytes(archive), digest.OfBytes(pinnedContent)),
		srv:  srv,
		hits: hits,
	}
}

// standardRelease is a release with the pinned binary and a license file in a .tar.gz archive.
func standardRelease(t *testing.T) *cbmRelease {
	t.Helper()

	return newRelease(t, "cbm.tar.gz", map[string][]byte{cbmBinaryName(): pinnedContent, "LICENSE": []byte("license")}, nil)
}

// cfg is the configuration of a run of `cbm install`: the PATH and the home directory are the ones of the test.
func (r *cbmRelease) cfg(t *testing.T, pathDirs ...string) *runConfig {
	t.Helper()

	return &runConfig{
		Dir: t.TempDir(),
		Env: map[string]string{
			"HOME":                t.TempDir(),
			"LOCALAPPDATA":        t.TempDir(),
			"PATH":                strings.Join(pathDirs, string(os.PathListSeparator)),
			cbmpin.DownloadURLEnv: r.srv.URL,
		},
		Deps: cli.Deps{HTTPClient: r.srv.Client(), Cbm: &cli.CbmDeps{Pin: r.pin}},
	}
}

// writeCbm writes a CBM executable file with the content in the directory and gives its resolved path.
func writeCbm(t *testing.T, dir string, content []byte) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(dir, cbmBinaryName())
	if err := os.WriteFile(file, content, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}

	return resolved
}

func TestCbmInstallDownloadsTheArchiveAndWritesTheBinary(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"cbm.tar.gz", "cbm.zip"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rel := newRelease(t, name, map[string][]byte{"LICENSE": []byte("license"), cbmBinaryName(): pinnedContent}, nil)
			dir := filepath.Join(t.TempDir(), "bin")
			cfg := rel.cfg(t, dir)

			res := runWith(t, cfg, "cbm", "install", "--dir", dir)
			requireExit0(t, &res)

			target := filepath.Join(dir, cbmBinaryName())

			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, pinnedContent) {
				t.Fatalf("file = %q, %v", got, err)
			}

			expectStdoutHas(t, res.Stdout, "Installed codebase-memory-mcp v9.9.9 to "+target, digest.OfBytes(pinnedContent),
				"does not run `codebase-memory-mcp install`", "does not configure your AI agents")

			if strings.Contains(res.Stderr, "not on the PATH") {
				t.Errorf("the directory is on the PATH, stderr = %q", res.Stderr)
			}

			if rel.hits.Load() != 1 {
				t.Errorf("requests = %d, want 1", rel.hits.Load())
			}
		})
	}
}

func TestCbmInstallUsesTheDefaultDirectoryAndWarnsWhenItIsNotOnThePath(t *testing.T) {
	t.Parallel()

	rel := standardRelease(t)
	cfg := rel.cfg(t)

	want := filepath.Join(cfg.Env["HOME"], ".local", "bin")
	if runtime.GOOS == "windows" {
		want = filepath.Join(cfg.Env["LOCALAPPDATA"], "Programs", "codebase-memory-mcp")
	}

	res := runWith(t, cfg, "cbm", "install")
	requireExit0(t, &res)

	if _, err := os.Stat(filepath.Join(want, cbmBinaryName())); err != nil {
		t.Errorf("the binary is not in the default directory %s: %v", want, err)
	}

	if !strings.Contains(res.Stderr, "Warning: "+want+" is not on the PATH") {
		t.Errorf("stderr = %q", res.Stderr)
	}
}

func TestCbmInstallSaysThatThePinnedBuildIsAlreadyThere(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		onPath bool
	}{{"in the install directory", false}, {"on the PATH", true}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rel := standardRelease(t)
			installDir, otherDir := filepath.Join(t.TempDir(), "bin"), filepath.Join(t.TempDir(), "other")
			cfg := rel.cfg(t)

			existing := writeCbm(t, installDir, pinnedContent)
			if tc.onPath {
				existing = writeCbm(t, otherDir, pinnedContent)
				cfg.Env["PATH"] = otherDir
			}

			res := runWith(t, cfg, "cbm", "install", "--dir", installDir)
			requireExit0(t, &res)

			expectStdoutHas(t, res.Stdout, "The pinned codebase-memory-mcp build v9.9.9 is already installed: "+existing, "Nothing to do.")

			if rel.hits.Load() != 0 {
				t.Errorf("requests = %d, want none", rel.hits.Load())
			}

			if warned := strings.Contains(res.Stderr, "not on the PATH"); warned == tc.onPath {
				t.Errorf("PATH warning = %v, stderr = %q", warned, res.Stderr)
			}
		})
	}
}

func TestCbmInstallRefusesAnotherBuildAndNeverOverwritesIt(t *testing.T) {
	t.Parallel()

	other := []byte("#!/bin/sh\necho another build\n")

	for _, where := range []string{"in the install directory", "on the PATH"} {
		t.Run(where, func(t *testing.T) {
			t.Parallel()

			rel := standardRelease(t)
			installDir, otherDir := filepath.Join(t.TempDir(), "bin"), filepath.Join(t.TempDir(), "other")
			cfg := rel.cfg(t)

			var existing string
			if where == "on the PATH" {
				existing = writeCbm(t, otherDir, other)
				cfg.Env["PATH"] = otherDir
			} else {
				existing = writeCbm(t, installDir, other)
			}

			res := runWith(t, cfg, "cbm", "install", "--dir", installDir)

			if res.Code != 5 {
				t.Fatalf("exit code = %d, want 5; stderr = %q", res.Code, res.Stderr)
			}

			expectStdoutHas(t, res.Stderr, "CO-OBS-005", existing, digest.OfBytes(other), "v9.9.9", digest.OfBytes(pinnedContent), "docs/explanation/cbm-and-agents.md")

			if got, _ := os.ReadFile(existing); !bytes.Equal(got, other) {
				t.Errorf("the other build was changed: %q", got)
			}

			if rel.hits.Load() != 0 {
				t.Errorf("requests = %d, want none", rel.hits.Load())
			}

			if where == "on the PATH" {
				if _, err := os.Stat(installDir); err == nil {
					t.Error("the install directory was created")
				}
			}
		})
	}
}

func TestCbmInstallFailsWithCOOBS006WhenAnArchiveCheckFails(t *testing.T) {
	t.Parallel()

	good := map[string][]byte{cbmBinaryName(): pinnedContent}
	cases := []struct {
		name  string
		files map[string][]byte
		serve map[string][]byte
		want  string
	}{
		{"archive digest differs", good, map[string][]byte{cbmBinaryName(): []byte("tampered")}, "the archive cbm.tar.gz has the digest"},
		{"binary digest differs", map[string][]byte{cbmBinaryName(): []byte("not pinned")}, nil, "the binary " + cbmBinaryName() + " has the digest"},
		{"unsafe entry", map[string][]byte{cbmBinaryName(): pinnedContent, "../evil": []byte("x")}, nil, "unsafe path"},
		{"no binary", map[string][]byte{"LICENSE": []byte("license")}, nil, "no entry " + cbmBinaryName()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rel := newRelease(t, "cbm.tar.gz", tc.files, tc.serve)
			dir := filepath.Join(t.TempDir(), "bin")
			res := runWith(t, rel.cfg(t, dir), "cbm", "install", "--dir", dir)

			if res.Code != 6 {
				t.Fatalf("exit code = %d, want 6; stderr = %q", res.Code, res.Stderr)
			}

			expectStdoutHas(t, res.Stderr, "CO-OBS-006", tc.want)

			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("the install directory has %d entries, want none", len(entries))
			}
		})
	}
}

func TestCbmInstallDoesNotDownloadWithOffline(t *testing.T) {
	t.Parallel()

	rel := standardRelease(t)
	dir := filepath.Join(t.TempDir(), "bin")

	res := runWith(t, rel.cfg(t, dir), "--offline", "cbm", "install", "--dir", dir)

	if res.Code != 6 || !strings.Contains(res.Stderr, "CO-OBS-006") || !strings.Contains(res.Stderr, "--offline") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if rel.hits.Load() != 0 {
		t.Errorf("requests = %d, want none", rel.hits.Load())
	}
}

func TestCbmInstallDoesNotUseHTTP(t *testing.T) {
	t.Parallel()

	rel := standardRelease(t)
	dir := filepath.Join(t.TempDir(), "bin")
	cfg := rel.cfg(t, dir)
	cfg.Env[cbmpin.DownloadURLEnv] = strings.Replace(rel.srv.URL, "https://", "http://", 1)

	res := runWith(t, cfg, "cbm", "install", "--dir", dir)

	if res.Code != 6 || !strings.Contains(res.Stderr, "https") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// doctorWithPin runs `doctor --json` for a CBM whose executable has the content, with the pin that has the digest.
func doctorWithPin(t *testing.T, content []byte, pinned string, args ...string) (result, capState) {
	t.Helper()

	file := writeCbm(t, t.TempDir(), content)
	deps := fileDeps(file)
	deps.Cbm = &cli.CbmDeps{Pin: pinFor(t, "cbm.tar.gz", otherPin, pinned)}

	res := runWith(t, &runConfig{Dir: initialized(t), Deps: deps}, append([]string{"doctor", "--json"}, args...)...)

	return res, capabilities(t, res.Stdout)["cbm"]
}

func TestDoctorSaysThatTheExecutableIsThePinnedBuild(t *testing.T) {
	t.Parallel()

	sum := digest.OfBytes(pinnedContent)

	res, cbm := doctorWithPin(t, pinnedContent, sum)
	requireExit0(t, &res)

	if !cbm.hasStatus("available", "pinned build v9.9.9", "tested") || strings.Contains(cbm.Detail, "not the CBM build") {
		t.Errorf("cbm = %+v", cbm)
	}
}

func TestDoctorDegradesACbmThatIsNotThePinnedBuild(t *testing.T) {
	t.Parallel()

	pinned := digest.OfBytes(pinnedContent)

	res, cbm := doctorWithPin(t, []byte("another build"), pinned)
	requireExit0(t, &res)

	want := "not the CBM build Concordia is tested with (pinned v9.9.9 sha256:" + strings.TrimPrefix(pinned, digest.Prefix)[:12] + ")"
	if !cbm.hasStatus("degraded", want) {
		t.Errorf("cbm = %+v, want detail %q", cbm, want)
	}

	if strict, _ := doctorWithPin(t, []byte("another build"), pinned, "--strict"); strict.Code != 5 {
		t.Errorf("--strict: exit code = %d, want 5", strict.Code)
	}
}
