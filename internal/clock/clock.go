// Package clock gives the current time. CONCORDIA_NOW overrides it for
// reproducible runs.
package clock

import (
	"os"
	"time"
)

// EnvNow is the name of the environment variable that fixes the current time.
const EnvNow = "CONCORDIA_NOW"

// isoLayout is the layout of JavaScript Date.toISOString: always milliseconds and "Z".
const isoLayout = "2006-01-02T15:04:05.000Z"

// layouts are the accepted time texts. A text without a zone is UTC.
var layouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// Parse reads an ISO 8601 time text (RFC 3339, with or without zone, or a date only).
func Parse(text string) (time.Time, error) {
	var (
		t   time.Time
		err error
	)

	for _, layout := range layouts {
		t, err = time.Parse(layout, text)
		if err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, err //nolint:wrapcheck // the error of time.Parse already names the text and the layout.
}

// Now gives the current time in UTC. When CONCORDIA_NOW holds a valid time
// text, that time is given. An invalid text is ignored.
func Now() time.Time {
	if fixed := os.Getenv(EnvNow); fixed != "" {
		if t, err := Parse(fixed); err == nil {
			return t
		}
	}

	return time.Now().UTC()
}

// ISO formats a time as JavaScript Date.toISOString does: UTC, milliseconds, "Z".
func ISO(t time.Time) string {
	return t.UTC().Format(isoLayout)
}
