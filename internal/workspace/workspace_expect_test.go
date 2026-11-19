package workspace_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// mustLoadWorkspace loads workspace text under profile and fails the test when the load fails.
func mustLoadWorkspace(t *testing.T, yaml, profile string) *workspace.Workspace {
	t.Helper()

	w, err := workspace.Load(writeWorkspace(t, yaml), profile)
	if err != nil {
		t.Fatal(err)
	}

	return w
}

// loadFailure loads workspace text with no profile and returns the diagnostic error that the load must give.
func loadFailure(t *testing.T, yaml string) *diag.Error {
	t.Helper()

	_, err := workspace.Load(writeWorkspace(t, yaml), "")

	return failure(t, err)
}

// expectEqual reports a difference between what the code under test gave and what was wanted.
func expectEqual[T any](t *testing.T, what string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// requireEqual is expectEqual, and stops the test on a difference.
func requireEqual[T any](t *testing.T, what string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
}

// expectContains reports text that lacks the wanted part.
func expectContains(t *testing.T, what, text, part string) {
	t.Helper()

	if !strings.Contains(text, part) {
		t.Errorf("%s %q does not contain %q", what, text, part)
	}
}

// expectInvalidInputCode reports a diagnostic error whose code differs from the wanted one, or whose exit code is not the invalid-input one.
func expectInvalidInputCode(t *testing.T, de *diag.Error, code string) {
	t.Helper()

	expectEqual(t, "code", de.Diagnostic.Code, code)
	expectEqual(t, "exit code", de.ExitCode, diag.InvalidInput)
}

// expectSourceLine reports a diagnostic that carries no source line.
func expectSourceLine(t *testing.T, d *diag.Diagnostic) {
	t.Helper()

	if d.Source == nil || d.Source.Line == 0 {
		t.Errorf("no source line in %+v", *d)
	}
}
