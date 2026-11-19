package exceptions_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/testutil"
)

func TestAppendWaiver(t *testing.T) {
	t.Parallel()

	t.Run("creates the file with ids in order", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "exceptions", registerFile)

		first := exceptions.Waiver{ID: idOne, Finding: fp(1), Rule: ruleOne, Owner: "team-billing", Reason: "Refund migration in progress", Expires: "2026-12-31"}
		mustAppendWaiver(t, path, &first)

		want := "apiVersion: concordia.io/v1alpha1\nkind: ExceptionRegister\nwaivers:\n  - id: WV-1\n    finding: " + fp(1) +
			"\n    rule: R-1\n    owner: team-billing\n    reason: Refund migration in progress\n    expires: 2026-12-31\n"
		if got := testutil.ReadFile(t, path); got != want {
			t.Errorf("file:\n%s\nwant:\n%s", got, want)
		}

		second := exceptions.Waiver{ID: exceptions.NextWaiverID(mustReadWaivers(t, path)), Finding: fp(2), Rule: ruleTwo, Owner: "o", Reason: "a: b # c", Expires: "2027-01-31", Issue: "ABC-1"}
		mustAppendWaiver(t, path, &second)

		if all := mustReadWaivers(t, path); !reflect.DeepEqual(all, []exceptions.Waiver{first, second}) || second.ID != "WV-2" {
			t.Errorf("waivers = %+v", all)
		}
	})

	t.Run("keeps comments and other keys", func(t *testing.T) {
		t.Parallel()

		text := "# Register of waivers.\napiVersion: concordia.io/v1alpha1\nkind: ExceptionRegister\n# The list:\nwaivers:\n  # first\n  - id: WV-1\n    finding: " + fp(1) +
			"\n    rule: R\n    owner: o\n    reason: r\n    expires: 2026-12-31 # soon\n"
		path := registerWith(t, text)

		mustAppendWaiver(t, path, ptr(waiver(2, "2027-01-01", "WV-2")))

		got := testutil.ReadFile(t, path)
		wantContainsAll(t, got, "# Register of waivers.", "# The list:", "# first", "# soon", "kind: ExceptionRegister", "id: WV-2", "expires: 2027-01-01")

		if _, err := exceptions.ParseWaivers(got, path); err != nil {
			t.Errorf("result not valid: %v", err)
		}
	})

	t.Run("a comment-only file keeps the comment", func(t *testing.T) {
		t.Parallel()

		path := registerWith(t, "# nothing yet\n")
		mustAppendWaiver(t, path, ptr(waiver(1, "2026-12-31", idOne)))

		got := testutil.ReadFile(t, path)
		if !strings.Contains(got, "# nothing yet") || !strings.Contains(got, "id: WV-1") {
			t.Errorf("file:\n%s", got)
		}
	})
}

func TestAppendWaiverToOtherRegisterShapes(t *testing.T) {
	t.Parallel()

	t.Run("a null list is replaced", func(t *testing.T) {
		t.Parallel()

		path := registerWith(t, "kind: ExceptionRegister\nwaivers:\n")
		mustAppendWaiver(t, path, ptr(waiver(1, "2026-12-31", idOne)))

		mustReadWaivers(t, path)
	})

	t.Run("a missing list is added", func(t *testing.T) {
		t.Parallel()

		path := registerWith(t, "kind: ExceptionRegister\n")
		mustAppendWaiver(t, path, ptr(waiver(1, "2026-12-31", idOne)))

		if got := mustReadWaivers(t, path); len(got) != 1 {
			t.Errorf("got %v", got)
		}
	})

	t.Run("a file that is not valid is refused", func(t *testing.T) {
		t.Parallel()

		path := registerWith(t, notAMapping)

		if de := asDiag(t, exceptions.AppendWaiver(path, ptr(waiver(1, "2026-12-31", idOne)))); de.Diagnostic.Code != diag.EXC002 {
			t.Errorf("diagnostic = %+v", de.Diagnostic)
		}

		if got := testutil.ReadFile(t, path); got != notAMapping {
			t.Errorf("file changed: %q", got)
		}
	})
}

// registerWith writes text as the register file of a new directory and gives its path.
func registerWith(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), registerFile)
	testutil.WriteTree(t, filepath.Dir(path), map[string]string{registerFile: text})

	return path
}

func mustAppendWaiver(t *testing.T, path string, w *exceptions.Waiver) {
	t.Helper()

	if err := exceptions.AppendWaiver(path, w); err != nil {
		t.Fatal(err)
	}
}

func mustReadWaivers(t *testing.T, path string) []exceptions.Waiver {
	t.Helper()

	got, err := exceptions.ReadWaivers(path)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// wantContainsAll reports an error for each of the wanted texts that text does not contain.
func wantContainsAll(t *testing.T, text string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}
