package evidence_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/diag"
)

func codes(list []diag.Diagnostic) []string {
	out := make([]string, 0, len(list))
	for i := range list {
		out = append(out, list[i].Code)
	}

	return out
}

func hasCode(list []diag.Diagnostic, code string) bool { return slices.Contains(codes(list), code) }

// mustLoad loads the evidence and stops the test on an error.
func mustLoad(t *testing.T, ctx context.Context, opts *evidence.Options) *evidence.Evidence {
	t.Helper()

	got, err := evidence.Load(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// expectEqual reports a value that differs from the wanted one.
func expectEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// expectDeepEqual reports a value that is not deeply equal to the wanted one.
func expectDeepEqual(t *testing.T, what string, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// expectCode reports a diagnostic list without the code.
func expectCode(t *testing.T, list []diag.Diagnostic, code string) {
	t.Helper()

	if !hasCode(list, code) {
		t.Errorf("diagnostics = %v, want %s", codes(list), code)
	}
}

// expectNoEvidence reports evidence that was returned with an error.
func expectNoEvidence(t *testing.T, got *evidence.Evidence, reason string) {
	t.Helper()

	if got != nil {
		t.Errorf("evidence returned with %s", reason)
	}
}

// expectDiagError reports an error that is not a diag.Error with the code and the exit code.
func expectDiagError(t *testing.T, err error, code string, exit int) {
	t.Helper()

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want %s with exit %d", err, code, exit)
	}

	expectEqual(t, "error code", failure.Diagnostic.Code, code)
	expectEqual(t, "error exit code", failure.ExitCode, exit)
}

// expectThat reports a value that does not satisfy the condition the caller has judged.
func expectThat(t *testing.T, ok bool, what string, got any) {
	t.Helper()

	if !ok {
		t.Errorf("%s = %+v", what, got)
	}
}
