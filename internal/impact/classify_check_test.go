package impact_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// checkEq reports an error unless got equals want.
func checkEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// checkSlice reports an error unless the two slices hold equal elements in order.
func checkSlice[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// checkTrue reports an error with the message unless ok holds.
func checkTrue(t *testing.T, ok bool, what string) {
	t.Helper()

	if !ok {
		t.Error(what)
	}
}

// requireEq stops the test unless got equals want.
func requireEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// mustOK stops the test when err is set.
func mustOK(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// checkErrIs reports an error unless err wraps target.
func checkErrIs(t *testing.T, err, target error) {
	t.Helper()

	if !errors.Is(err, target) {
		t.Errorf("err = %v, want %v", err, target)
	}
}

// checkContains reports an error unless text holds part.
func checkContains(t *testing.T, text, part string) {
	t.Helper()

	if !strings.Contains(text, part) {
		t.Errorf("%q lacks %q", text, part)
	}
}
