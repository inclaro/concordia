package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

// extWant reports a mismatch and goes on.
func extWant[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// extRequire reports a mismatch and stops the test.
func extRequire[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// extWantText reports a text that is empty.
func extWantText(t *testing.T, what, got string) {
	t.Helper()

	if got == "" {
		t.Errorf("%s is empty", what)
	}
}

// extWantContains reports a text that lacks the part.
func extWantContains(t *testing.T, what, got, part string) {
	t.Helper()

	if !strings.Contains(got, part) {
		t.Errorf("%s %q lacks %s", what, got, part)
	}
}

// extReasonCodes gives the codes of the reasons.
func extReasonCodes(reasons []conformance.Reason) []string {
	codes := make([]string, 0, len(reasons))
	for i := range reasons {
		codes = append(codes, reasons[i].Code)
	}

	return codes
}

// extWantReasons reports reasons whose codes are not the wanted ones, in order.
func extWantReasons(t *testing.T, reasons []conformance.Reason, want ...string) {
	t.Helper()

	if got := extReasonCodes(reasons); !slices.Equal(got, want) {
		t.Errorf("reason codes = %v, want %v", got, want)
	}
}

// extLocationPath gives the path of the location, or empty without one.
func extLocationPath(location *conformance.Location) string {
	if location == nil {
		return ""
	}

	return location.Path
}

// extOwner gives the owner of a finding, or empty without one.
func extOwner(owner *string) string {
	if owner == nil {
		return ""
	}

	return *owner
}
