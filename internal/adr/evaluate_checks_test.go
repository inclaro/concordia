package adr_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
)

// evalWantEqual reports a mismatch of one value and goes on.
func evalWantEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// evalRequireEqual reports a mismatch of one value and stops the test.
func evalRequireEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// evalWantSlice reports a mismatch of one list and goes on.
func evalWantSlice[T comparable](t *testing.T, label string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// evalAnyDiagnostic matches every diagnostic of a code.
func evalAnyDiagnostic(diag.Diagnostic) bool { return true }

// evalHasDiagnostic reports whether a diagnostic of the code satisfies the match.
func evalHasDiagnostic(diagnostics []diag.Diagnostic, code string, match func(diag.Diagnostic) bool) bool {
	return slices.ContainsFunc(diagnostics, func(d diag.Diagnostic) bool { return d.Code == code && match(d) })
}

// evalSnapshotFiles reads every file under the root.
func evalSnapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()

	files := map[string]string{}

	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		data, readErr := os.ReadFile(p)
		files[p] = string(data)

		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}
