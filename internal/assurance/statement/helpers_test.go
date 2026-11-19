package statement_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
)

// statementJSON is an in-toto Statement v1 with the given predicate type and predicate.
func statementJSON(t *testing.T, predicateType string, predicate any, subjects ...statement.Subject) []byte {
	t.Helper()

	if len(subjects) == 0 {
		subjects = []statement.Subject{{Name: "registry.example/shop", Digest: map[string]string{"sha256": "aaa"}}}
	}

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "subject": subjects, "predicateType": predicateType, "predicate": predicate,
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// envelopeJSON wraps a statement in a DSSE envelope with one signature.
func envelopeJSON(t *testing.T, payload []byte) []byte {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"payloadType": statement.PayloadType,
		"payload":     base64.StdEncoding.EncodeToString(payload),
		"signatures":  []map[string]string{{"sig": "c2ln"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// bundleJSON wraps a statement in a Sigstore bundle shaped like the output of cosign attest-blob.
func bundleJSON(t *testing.T, payload []byte) []byte {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"mediaType":            "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "aGludA=="}},
		"dsseEnvelope": map[string]any{
			"payloadType": statement.PayloadType,
			"payload":     base64.StdEncoding.EncodeToString(payload),
			"signatures":  []map[string]string{{"sig": "c2ln"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// mustParse parses a statement file or fails the test.
func mustParse(t *testing.T, path string, data []byte) statement.Loaded {
	t.Helper()

	loaded, err := statement.Parse(path, data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}

	return loaded
}

// writeFile writes data under a new temporary directory and gives the path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// fakeCosign writes an executable shell script that stands in for cosign.
func fakeCosign(t *testing.T, body string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	return writeScript(t, "#!/bin/sh\n"+body)
}

func writeScript(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// realFixtures is the directory of the recorded real cosign output.
func realFixtures() string {
	return filepath.Join("..", "..", "..", "test", "fixtures", "assurance", "statements", "real-cosign")
}

// readFixture reads a file of the recorded real cosign output.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(realFixtures(), name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// expectEqual reports a value that differs from the wanted one.
func expectEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// expectDeepEqual reports a value that is not deeply equal to the wanted one.
func expectDeepEqual[T any](t *testing.T, what string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// expectContains reports a text that does not hold the wanted part.
func expectContains(t *testing.T, what, text, want string) {
	t.Helper()

	if !strings.Contains(text, want) {
		t.Errorf("%s = %q, want it to contain %q", what, text, want)
	}
}

// expectNotContains reports a text that holds a part it must not.
func expectNotContains(t *testing.T, text string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if strings.Contains(text, part) {
			t.Errorf("the text holds %q:\n%s", part, text)
		}
	}
}

// expectPRV009Warning reports a diagnostic that is not a CO-PRV-009 warning.
func expectPRV009Warning(t *testing.T, d *diag.Diagnostic) {
	t.Helper()

	expectEqual(t, "code", d.Code, diag.PRV009)
	expectEqual(t, "severity", d.Severity, diag.SeverityWarning)
}
