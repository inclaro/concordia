package redact_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/redact"
)

func TestRedactRules(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(dockerRuleCases, scriptRuleCases, yamlRuleCases, textRuleCases)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRuleCase(t, tc)
		})
	}
}

// checkRuleCase redacts the input of the case and checks the output, the number of records,
// that a second run changes nothing, and that every record is well formed.
func checkRuleCase(t *testing.T, tc ruleCase) {
	t.Helper()

	got, records := redact.Default().Redact(tc.file, tc.in)
	if got != tc.want {
		t.Errorf("output:\n%s\nwant:\n%s", got, tc.want)
	}

	if len(records) != tc.count {
		t.Errorf("%d records %+v, want %d", len(records), records, tc.count)
	}

	again, _ := redact.Default().Redact(tc.file, got)
	if again != got {
		t.Errorf("not idempotent:\n%s", again)
	}

	for _, r := range records {
		if !isWellFormed(r) {
			t.Errorf("record %+v", r)
		}
	}
}

// isWellFormed says whether the record has a class and a line, and a property that holds no marker.
func isWellFormed(r redact.Record) bool {
	if r.Class == "" || r.Line <= 0 {
		return false
	}

	return !strings.Contains(r.Property, "[REDACTED")
}
