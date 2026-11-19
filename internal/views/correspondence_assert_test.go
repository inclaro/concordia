package views_test

import (
	"slices"
	"testing"
)

// assertEq reports a difference between a value and what is wanted, and goes on.
func assertEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// requireEq reports a difference between a value and what is wanted, and stops the test.
func requireEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// assertSliceEq reports a difference between a list and what is wanted, and goes on.
func assertSliceEq[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// requireSliceEq reports a difference between a list and what is wanted, and stops the test.
func requireSliceEq[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// assertHas reports an item that a list does not have, and goes on.
func assertHas[T comparable](t *testing.T, what string, list []T, item T) {
	t.Helper()

	if !slices.Contains(list, item) {
		t.Errorf("%s = %v, want it to have %v", what, list, item)
	}
}

// requireHas reports an item that a list does not have, and stops the test.
func requireHas[T comparable](t *testing.T, what string, list []T, item T) {
	t.Helper()

	if !slices.Contains(list, item) {
		t.Fatalf("%s = %v, want it to have %v", what, list, item)
	}
}

// assertLacks reports an item that a list has, and goes on.
func assertLacks[T comparable](t *testing.T, what string, list []T, item T) {
	t.Helper()

	if slices.Contains(list, item) {
		t.Errorf("%s = %v, want it not to have %v", what, list, item)
	}
}

// assertNilPtr reports a pointer that is not nil, and goes on.
func assertNilPtr[T any](t *testing.T, what string, p *T) {
	t.Helper()

	if p != nil {
		t.Errorf("%s = %v, want nil", what, *p)
	}
}
