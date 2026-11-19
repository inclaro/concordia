package observation_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
)

// executableFake is a fake client that starts the executable at path.
type executableFake struct {
	*observation.FakeClient
	path string
}

func (f executableFake) Executable() string { return f.path }

func newExecutableFake(path string) executableFake {
	handler := func(string, map[string]any) (any, error) { return json.RawMessage(`{}`), nil }

	return executableFake{FakeClient: observation.NewFakeClient(handler, ""), path: path}
}

// exeSuffix is the extension that lets LookPath accept a file as an executable on this platform.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}

	return ""
}

// writeExecutable writes a file as the executable and gives its path and digest.
func writeExecutable(t *testing.T, dir string) (path, sum string) {
	t.Helper()

	path = filepath.Join(dir, "cbm"+exeSuffix())
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	sum, err := digest.OfFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return path, sum
}

func TestConnectHashesTheResolvedExecutable(t *testing.T) {
	t.Parallel()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	target, sum := writeExecutable(t, dir)
	link := filepath.Join(dir, "link"+exeSuffix())

	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	conn, err := observation.Connect(t.Context(), newExecutableFake(link), nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	if conn.Executable != target || conn.SHA256 != sum {
		t.Errorf("executable %q sha256 %q, want the resolved file %q with %q", conn.Executable, conn.SHA256, target, sum)
	}
}

func TestConnectWithoutAnExecutableHasNoIdentity(t *testing.T) {
	t.Parallel()

	conn, err := observation.Connect(t.Context(), observation.NewFakeClient(nil, ""), nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	if conn.Executable != "" || conn.SHA256 != "" {
		t.Errorf("executable %q sha256 %q, want none", conn.Executable, conn.SHA256)
	}
}

func TestConnectGivesOBS003ForAnExecutableThatIsNotThere(t *testing.T) {
	t.Parallel()

	_, err := observation.Connect(t.Context(), newExecutableFake(filepath.Join(t.TempDir(), "missing")), nil)

	wantDiag(t, err, "CO-OBS-003", diag.Toolchain)
}
