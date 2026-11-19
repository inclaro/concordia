// Package testutil has helpers for tests of the other packages.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	dirMode  = 0o755
	fileMode = 0o644
)

// WriteTree writes files below root. The keys are "/" paths relative to root;
// missing directories are created.
func WriteTree(tb testing.TB, root string, files map[string]string) {
	tb.Helper()

	for rel, content := range files {
		target := filepath.Join(root, filepath.FromSlash(rel))

		if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
			tb.Fatalf("create directory for %s: %v", rel, err)
		}

		if err := os.WriteFile(target, []byte(content), fileMode); err != nil {
			tb.Fatalf("write %s: %v", rel, err)
		}
	}
}

// ReadFile reads a file and fails the test on error.
func ReadFile(tb testing.TB, path string) string {
	tb.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // a test helper reads the path that the test gives.
	if err != nil {
		tb.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

// Git runs git in dir with a fixed author and committer, and fails the test on error.
func Git(tb testing.TB, dir string, args ...string) {
	tb.Helper()

	cmd := exec.CommandContext(tb.Context(), "git", args...) //nolint:gosec // a test helper runs git with the arguments that the test gives.
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.com",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
