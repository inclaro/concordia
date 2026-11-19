package clock_test

import (
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/clock"
)

func TestNowHonorsEnv(t *testing.T) {
	t.Setenv(clock.EnvNow, "2027-01-01T00:00:00Z")

	if got := clock.ISO(clock.Now()); got != "2027-01-01T00:00:00.000Z" {
		t.Fatalf("got %s", got)
	}
}

func TestNowWithoutEnvIsCurrent(t *testing.T) {
	t.Setenv(clock.EnvNow, "")

	before := time.Now().UTC().Add(-time.Second)
	if got := clock.Now(); got.Before(before) || got.Location() != time.UTC {
		t.Fatalf("got %v", got)
	}
}

func TestNowIgnoresInvalidEnv(t *testing.T) {
	t.Setenv(clock.EnvNow, "not a time")

	if got := clock.Now(); time.Since(got) > time.Minute {
		t.Fatalf("got %v", got)
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"2026-03-04":                  "2026-03-04T00:00:00.000Z",
		"2026-03-04T05:06:07":         "2026-03-04T05:06:07.000Z",
		"2026-03-04T05:06:07.5Z":      "2026-03-04T05:06:07.500Z",
		"2026-03-04T07:06:07+02:00":   "2026-03-04T05:06:07.000Z",
		"2026-03-04T05:06:07.123456Z": "2026-03-04T05:06:07.123Z",
	}

	for in, want := range cases {
		got, err := clock.Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)

			continue
		}

		if clock.ISO(got) != want {
			t.Errorf("Parse(%q) = %s, want %s", in, clock.ISO(got), want)
		}
	}

	if _, err := clock.Parse("yesterday"); err == nil {
		t.Error("expected an error")
	}
}
