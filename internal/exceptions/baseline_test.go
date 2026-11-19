package exceptions_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/testutil"
)

func TestBaselineRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "exceptions", "concordia.baseline.json")

	revision := "46d2f68f"
	b := baselineOf(3, 1, 2)
	b.CreatedFrom.SourceRevision = &revision
	b.Findings[0].ExpiresAt = "2027-01-01"

	if err := exceptions.WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}

	if got, want := testutil.ReadFile(t, path), writtenBaseline(); got != want {
		t.Fatalf("file:\n%s\nwant:\n%s", got, want)
	}

	read, err := exceptions.ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}

	if !isRoundTripped(&read, revision) {
		t.Errorf("read = %+v", read)
	}

	// Brownfield: the file bytes do not change when dispositions are applied.
	before := fileHash(t, path)
	res := exceptions.ApplyDispositions([]exceptions.Key{finding(1), finding(2), finding(3), finding(4)}, &read, nil, time.Now(), nil)

	if active := activeIndexes(res.Assignments); !slices.Equal(active, []int{3}) {
		t.Errorf("active findings = %v, want only the 4th", active)
	}

	if fileHash(t, path) != before {
		t.Error("baseline file changed")
	}
}

// writtenBaseline is the file that WriteBaseline writes for baselineOf(3, 1, 2) with the revision 46d2f68f and the
// expiry 2027-01-01 on the first entry.
func writtenBaseline() string {
	return `{
  "createdFrom": {
    "graphGeneration": "g",
    "policyDigest": "d",
    "sourceRevision": "46d2f68f"
  },
  "findings": [
    {
      "createdAt": "2026-01-01T00:00:00Z",
      "fingerprint": "` + fp(1) + `",
      "owner": "o",
      "reason": "r",
      "ruleId": "R-1"
    },
    {
      "createdAt": "2026-01-01T00:00:00Z",
      "fingerprint": "` + fp(2) + `",
      "owner": "o",
      "reason": "r",
      "ruleId": "R-1"
    },
    {
      "createdAt": "2026-01-01T00:00:00Z",
      "expiresAt": "2027-01-01",
      "fingerprint": "` + fp(3) + `",
      "owner": "o",
      "reason": "r",
      "ruleId": "R-1"
    }
  ],
  "schemaVersion": "1.0"
}
`
}

// isRoundTripped says whether read has the three entries, sorted, and the source revision.
func isRoundTripped(read *exceptions.Baseline, revision string) bool {
	if len(read.Findings) != 3 || read.Findings[0].Fingerprint != fp(1) {
		return false
	}

	return read.CreatedFrom.SourceRevision != nil && *read.CreatedFrom.SourceRevision == revision
}

// activeIndexes gives the positions of the assignments that are active.
func activeIndexes(assignments []exceptions.Assignment) []int {
	var active []int

	for i, a := range assignments {
		if a.Disposition == exceptions.DispositionActive {
			active = append(active, i)
		}
	}

	return active
}

func fileHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return sha256.Sum256(data)
}

func TestWriteBaselineNullRevisionAndEmptyFindings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "b.json")
	if err := exceptions.WriteBaseline(path, baselineOf()); err != nil {
		t.Fatal(err)
	}

	got := testutil.ReadFile(t, path)
	if !strings.Contains(got, `"sourceRevision": null`) || !strings.Contains(got, `"findings": []`) {
		t.Errorf("file:\n%s", got)
	}
}

// goodBaseline gives a baseline document that parses.
func goodBaseline() map[string]any {
	return map[string]any{
		"schemaVersion": "1.0",
		"createdFrom":   map[string]any{"sourceRevision": nil, "graphGeneration": "g", "policyDigest": "d"},
		"findings": []any{map[string]any{
			"fingerprint": fp(1), "ruleId": "R", "owner": "o", "reason": "r", "createdAt": "t",
		}},
	}
}

// firstBaselineEntry gives the first entry of the findings of a baseline document.
func firstBaselineEntry(m map[string]any) map[string]any {
	return m["findings"].([]any)[0].(map[string]any) //nolint:forcetypeassert,errcheck // the test builds this value.
}

// baselineMutations change a good baseline document so that it does not parse.
func baselineMutations() map[string]func(m map[string]any) {
	entry := firstBaselineEntry

	return map[string]func(m map[string]any){
		"wrong version":        func(m map[string]any) { m["schemaVersion"] = "2.0" },
		"no createdFrom":       func(m map[string]any) { delete(m, "createdFrom") },
		"no sourceRevision":    func(m map[string]any) { delete(m["createdFrom"].(map[string]any), "sourceRevision") }, //nolint:forcetypeassert,errcheck // the test builds this value.
		"number revision":      func(m map[string]any) { m["createdFrom"].(map[string]any)["sourceRevision"] = 1.0 },   //nolint:forcetypeassert,errcheck // the test builds this value.
		"findings not a list":  func(m map[string]any) { m["findings"] = "x" },
		"entry not an object":  func(m map[string]any) { m["findings"] = []any{"x"} },
		"empty owner":          func(m map[string]any) { entry(m)["owner"] = "" },
		"no reason":            func(m map[string]any) { delete(entry(m), "reason") },
		"fingerprint format":   func(m map[string]any) { entry(m)["fingerprint"] = "sha256:abc" },
		"expiresAt not string": func(m map[string]any) { entry(m)["expiresAt"] = 1.0 },
		"expiresAt null":       func(m map[string]any) { entry(m)["expiresAt"] = nil },
		"duplicate": func(m map[string]any) {
			m["findings"] = append(m["findings"].([]any), entry(m)) //nolint:forcetypeassert,errcheck // the test builds this value.
		},
	}
}

func TestParseBaselineErrors(t *testing.T) {
	t.Parallel()

	if _, err := exceptions.ParseBaseline(goodBaseline(), "b.json"); err != nil {
		t.Fatalf("good baseline: %v", err)
	}

	for name, mutate := range baselineMutations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := goodBaseline()
			mutate(m)

			_, err := exceptions.ParseBaseline(m, "b.json")
			if err == nil {
				t.Fatal("no error")
			}

			if de := asDiag(t, err); de.ExitCode != diag.InvalidInput || de.Diagnostic.Code != diag.EXC001 {
				t.Errorf("diagnostic = %+v", de.Diagnostic)
			}
		})
	}
}

func TestParseBaselineRejectsOtherInputs(t *testing.T) {
	t.Parallel()

	if _, err := exceptions.ParseBaseline([]any{}, "b.json"); err == nil {
		t.Error("array accepted")
	}

	if _, err := exceptions.ParseBaselineJSON([]byte("{"), "b.json"); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("invalid JSON: %v", err)
	}
}

func TestReadBaselineMissing(t *testing.T) {
	t.Parallel()

	_, err := exceptions.ReadBaseline(filepath.Join(t.TempDir(), "none.json"))
	if de := asDiag(t, err); de.Diagnostic.Code != diag.EXC001 || !strings.Contains(de.Diagnostic.Message, "cannot read") {
		t.Errorf("diagnostic = %+v", de.Diagnostic)
	}
}

func TestPolicyDigestOf(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("a\nb"))
	want := "sha256:" + hex.EncodeToString(sum[:])

	if got := exceptions.PolicyDigestOf([]string{"b", "a"}); got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}

	if got := exceptions.PolicyDigestOf([]string{"a", "b"}); got != want {
		t.Errorf("sorted input: digest = %q, want %q", got, want)
	}
}
