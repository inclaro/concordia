package exceptions_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
)

var validRegister = "waivers:\n  - id: WV-1\n    finding: " + fp(1) + "\n    rule: R\n    owner: o\n    reason: r\n    expires: 2026-12-31\n    issue: ABC-1\n"

// emptyRegisters are the register texts that parse to no waiver.
var emptyRegisters = map[string]string{
	"empty":           "",
	"comment only":    "# nothing\n",
	"null document":   "null\n",
	"no waivers key":  "apiVersion: concordia.io/v1alpha1\n",
	"null waivers":    "waivers:\n",
	"empty waivers":   "waivers: []\n",
	"flow empty list": "kind: ExceptionRegister\nwaivers: []\n",
}

// invalidRegisters are the register texts that fail with CO-EXC-002.
var invalidRegisters = map[string]string{
	"syntax error":          "waivers: [\n",
	"duplicate key":         "waivers: []\nwaivers: []\n",
	"not a mapping":         notAMapping,
	"waivers not a list":    "waivers: x\n",
	"item not a mapping":    "waivers:\n  - x\n",
	"finding not sha256":    strings.Replace(validRegister, fp(1), "abc", 1),
	"expires not a date":    strings.Replace(validRegister, "2026-12-31", "tomorrow", 1),
	"expires month 13":      strings.Replace(validRegister, "2026-12-31", "2026-13-01", 1),
	"expires with time":     strings.Replace(validRegister, "2026-12-31", "2026-12-31T10:00:00Z", 1),
	"number is not text":    strings.Replace(validRegister, "owner: o", "owner: 5", 1),
	"empty required string": strings.Replace(validRegister, "owner: o", `owner: ""`, 1),
	"issue not a string":    strings.Replace(validRegister, "issue: ABC-1", "issue: 5", 1),
}

func TestWaiverSchemaErrors(t *testing.T) {
	t.Parallel()

	_, err := exceptions.ParseWaivers("waivers:\n  - id: WV-1\n", "w.yaml")
	if !isErrorMentioning(err, "waiver not valid") {
		t.Errorf("missing keys: %v", err)
	}

	de := asDiag(t, err)
	if !isInvalidInputAt(de, "/waivers/0/rule") {
		t.Errorf("diagnostic = %+v exit %d", de.Diagnostic, de.ExitCode)
	}

	line := `  - {id: WV-1, finding: "` + fp(1) + `", rule: R, owner: o, reason: r, expires: "2026-12-31"}`
	_, err = exceptions.ParseWaivers("waivers:\n"+line+"\n"+line+"\n", "w.yaml")

	if !isErrorMentioning(err, "duplicate") {
		t.Errorf("duplicate: %v", err)
	}

	if de := asDiag(t, err); de.Diagnostic.Code != diag.EXC004 || de.Diagnostic.Source.Pointer != "/waivers/1/id" {
		t.Errorf("duplicate diagnostic = %+v", de.Diagnostic)
	}
}

// isErrorMentioning says whether err is not nil and its text contains text.
func isErrorMentioning(err error, text string) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), text)
}

// isInvalidInputAt says whether de is an invalid-input error whose source has a line and the pointer.
func isInvalidInputAt(de *diag.Error, pointer string) bool {
	if de.ExitCode != diag.InvalidInput || de.Diagnostic.Source.Line == 0 {
		return false
	}

	return de.Diagnostic.Source.Pointer == pointer
}

func TestParseWaivers(t *testing.T) {
	t.Parallel()

	t.Run("bare date is text", func(t *testing.T) {
		t.Parallel()

		got, err := exceptions.ParseWaivers(validRegister, "w.yaml")
		if err != nil {
			t.Fatal(err)
		}

		want := []exceptions.Waiver{{ID: idOne, Finding: fp(1), Rule: "R", Owner: "o", Reason: "r", Expires: "2026-12-31", Issue: "ABC-1"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v", got)
		}
	})
}

func TestParseWaiversOfEmptyRegisters(t *testing.T) {
	t.Parallel()

	for name, text := range emptyRegisters {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := exceptions.ParseWaivers(text, "w.yaml")
			if err != nil || len(got) != 0 {
				t.Errorf("got %v, %v", got, err)
			}
		})
	}
}

func TestParseWaiversRejectsInvalidRegisters(t *testing.T) {
	t.Parallel()

	for name, text := range invalidRegisters {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := exceptions.ParseWaivers(text, "w.yaml")
			if err == nil {
				t.Fatal("no error")
			}

			if de := asDiag(t, err); de.ExitCode != diag.InvalidInput || de.Diagnostic.Code != diag.EXC002 {
				t.Errorf("diagnostic = %+v", de.Diagnostic)
			}
		})
	}
}

func TestParseWaiversAcceptsADayThatTheMonthHasNot(t *testing.T) {
	t.Parallel()

	t.Run("a day that the month has not rolls over", func(t *testing.T) {
		t.Parallel()

		_, err := exceptions.ParseWaivers(strings.Replace(validRegister, "2026-12-31", "2026-02-31", 1), "w.yaml")
		if err != nil {
			t.Errorf("JavaScript Date.parse accepts 2026-02-31: %v", err)
		}
	})
}

func TestReadWaivers(t *testing.T) {
	t.Parallel()

	_, err := exceptions.ReadWaivers(filepath.Join(t.TempDir(), "missing.yaml"))
	if de := asDiag(t, err); de.Diagnostic.Code != diag.EXC002 || !strings.Contains(de.Diagnostic.Message, "cannot read") {
		t.Errorf("diagnostic = %+v", de.Diagnostic)
	}
}

func TestNextWaiverID(t *testing.T) {
	t.Parallel()

	if got := exceptions.NextWaiverID(nil); got != idOne {
		t.Errorf("empty: %q", got)
	}

	list := []exceptions.Waiver{{ID: idOne}, {ID: "custom"}, {ID: "WV-12"}, {ID: "X-3"}}
	if got := exceptions.NextWaiverID(list); got != "WV-13" {
		t.Errorf("list: %q", got)
	}
}

func TestWaiverDiagnostics(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")
	list := []exceptions.Waiver{waiver(1, "2026-05-31", idOne), waiver(2, "2026-06-10", "WV-2"), waiver(3, "2026-12-31", "WV-3")}

	got := exceptions.Diagnostics(list, now, "w.yaml", nil)
	if !isExpiredAt(got, "w.yaml") {
		t.Errorf("without window: %+v", got)
	}

	days := 10.0
	if got := exceptions.Diagnostics(list, now, "w.yaml", &days); !slices.Equal(codes(got), []string{codeExpired, codeSoon}) {
		t.Errorf("10 days: %v", codes(got))
	}

	days = 400
	if got := exceptions.Diagnostics(list, now, "w.yaml", &days); !slices.Equal(codes(got), []string{codeExpired, codeSoon, codeSoon}) {
		t.Errorf("400 days: %v", codes(got))
	}
}

// isExpiredAt says whether got is the one CO-EXC-006 diagnostic of the file uri.
func isExpiredAt(got []diag.Diagnostic, uri string) bool {
	if !slices.Equal(codes(got), []string{codeExpired}) || got[0].Source == nil {
		return false
	}

	return got[0].Source.URI == uri
}
