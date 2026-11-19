package intent //nolint:testpackage // the tests use the unexported timelineParseDate.

import "testing"

func TestTimelineParseDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"2023-01-01", 1672531200000, true},
		{"2024-02-30", 1709251200000, true}, // V8 overflows the day
		{"2024-01-01T10:00:00.5+02:00", 1704096000500, true},
		{"2024-01-01T24:01Z", 0, false},
		{"2024-13-01", 0, false},
		{"not-a-date", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		got, ok := timelineParseDate(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("timelineParseDate(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
