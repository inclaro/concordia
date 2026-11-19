package render_test

import (
	"reflect"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
)

// expectDeep reports an error when got and want are not deeply equal. what names the value in the message.
func expectDeep[T any](t *testing.T, what string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// expectLen stops the test when got does not hold want items, so the caller can index it safely.
func expectLen[T any](t *testing.T, what string, got []T, want int) {
	t.Helper()

	if len(got) != want {
		t.Fatalf("%s = %+v, want %d items", what, got, want)
	}
}

// anyDiagnostic tells whether a diagnostic of res satisfies match.
func anyDiagnostic(res *render.Result, match func(d *diag.Diagnostic) bool) bool {
	for i := range res.Diagnostics {
		if match(&res.Diagnostics[i]) {
			return true
		}
	}

	return false
}
