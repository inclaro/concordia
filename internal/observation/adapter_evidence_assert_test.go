package observation_test

import (
	"reflect"
	"strings"
	"testing"
)

// obsEqual fails the test when got is not want.
func obsEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// obsRequire fails the test with msg when ok is false.
func obsRequire(t *testing.T, ok bool, msg string) {
	t.Helper()

	if !ok {
		t.Fatal(msg)
	}
}

// obsContains fails the test when text does not contain part.
func obsContains(t *testing.T, what, text, part string) {
	t.Helper()

	if !strings.Contains(text, part) {
		t.Fatalf("%s = %q, want it to contain %q", what, text, part)
	}
}

// obsDeepEqual fails the test when got is not deeply equal to want.
func obsDeepEqual(t *testing.T, what string, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}
