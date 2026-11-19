package observation_test

import (
	"errors"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
)

// diagOf checks that err is a *diag.Error with the given code and exit
// code, and gives it. An exit code of diag.NoExit is not checked.
func diagOf(tb testing.TB, err error, code string, exit int) *diag.Error {
	tb.Helper()

	var de *diag.Error
	if !errors.As(err, &de) {
		tb.Fatalf("want a *diag.Error %s, got %v", code, err)
	}

	if de.Diagnostic.Code != code {
		tb.Fatalf("code = %s (%s), want %s", de.Diagnostic.Code, de.Diagnostic.Message, code)
	}

	if exit != diag.NoExit && de.ExitCode != exit {
		tb.Fatalf("exit code = %d, want %d", de.ExitCode, exit)
	}

	return de
}

// wantDiag is diagOf for a test that does not look at the error.
func wantDiag(tb testing.TB, err error, code string, exit int) {
	tb.Helper()

	_ = diagOf(tb, err, code, exit)
}
