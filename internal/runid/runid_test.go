package runid_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/runid"
)

func TestNewHasTheDocumentedForm(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.UTC)
	if got := runid.New(at); !regexp.MustCompile(`^20260102T030405Z-[0-9a-f]{8}$`).MatchString(got) {
		t.Fatalf("got %s", got)
	}
}

func TestNewUsesUTCAndIsRandom(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 5, 4, 5, 0, time.FixedZone("x", 2*3600))
	a, b := runid.New(at), runid.New(at)

	if a[:16] != "20260102T030405Z" {
		t.Errorf("got %s", a)
	}

	if a == b {
		t.Errorf("two IDs are equal: %s", a)
	}
}
