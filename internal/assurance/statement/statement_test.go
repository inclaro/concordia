package statement_test

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/statement"
)

func TestParseReadsEveryForm(t *testing.T) {
	t.Parallel()

	payload := statementJSON(t, statement.PredicatePlan, map[string]string{"environment": "production"})

	tests := []struct {
		name string
		form statement.Form
		data []byte
	}{
		{name: "bare statement", data: payload, form: statement.FormStatement},
		{name: "DSSE envelope", data: envelopeJSON(t, payload), form: statement.FormEnvelope},
		{name: "Sigstore bundle", data: bundleJSON(t, payload), form: statement.FormBundle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mustParse(t, "build/plans/prod.intoto.json", tt.data)

			if got.Form != tt.form {
				t.Errorf("form = %s, want %s", got.Form, tt.form)
			}

			if got.Statement.PredicateType != statement.PredicatePlan || len(got.Statement.Subject) != 1 {
				t.Errorf("statement = %+v", got.Statement)
			}

			if !got.Statement.Names(statement.Digest{Algorithm: "sha256", Value: "AAA"}) {
				t.Error("the subject digest should match without case")
			}

			if !strings.HasPrefix(got.Digest, "sha256:") || got.Path != "build/plans/prod.intoto.json" {
				t.Errorf("digest = %q, path = %q", got.Digest, got.Path)
			}
		})
	}
}

func TestParseReadsTheRecordedCosignBundle(t *testing.T) {
	t.Parallel()

	bundle := mustParse(t, "check-result.sigstore.json", readFixture(t, "check-result.sigstore.json"))
	bare := mustParse(t, "statement.json", readFixture(t, "statement.json"))

	if bundle.Form != statement.FormBundle || bare.Form != statement.FormStatement {
		t.Fatalf("forms = %s, %s", bundle.Form, bare.Form)
	}

	if bundle.Statement.PredicateType != statement.PredicateCheckResult || statement.ClaimKind(bundle.Statement.PredicateType) != "check-result" {
		t.Errorf("predicate type = %s", bundle.Statement.PredicateType)
	}
}

// expectMalformed gives the error as a malformed-file error whose reason holds the wanted text.
func expectMalformed(t *testing.T, err error, want string) *statement.MalformedError {
	t.Helper()

	var bad *statement.MalformedError
	if !errors.As(err, &bad) || !strings.Contains(bad.Reason, want) {
		t.Fatalf("err = %v, want a *MalformedError containing %q", err, want)
	}

	return bad
}

// readFrom reads from the given files. A file with no data is a file that does not exist.
func readFrom(files map[string][]byte) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		data, ok := files[path]
		if !ok || data == nil {
			return nil, fmt.Errorf("open %s: %w", path, os.ErrNotExist)
		}

		return data, nil
	}
}

// expectLoadedPaths stops the test when the loaded files are not the wanted ones, in order.
func expectLoadedPaths(t *testing.T, loaded []statement.Loaded, want ...string) {
	t.Helper()

	paths := make([]string, 0, len(loaded))
	for i := range loaded {
		paths = append(paths, loaded[i].Path)
	}

	if !slices.Equal(paths, want) {
		t.Fatalf("loaded = %+v, want the files %v sorted by path", loaded, want)
	}
}

func TestParseRejectsMalformedFiles(t *testing.T) {
	t.Parallel()

	good := statementJSON(t, statement.PredicatePlan, map[string]string{})
	old := strings.Replace(string(good), "Statement/v1", "Statement/v0.1", 1)

	tests := []struct {
		name string
		data string
		want string
	}{
		{"not JSON", `{"_type":`, "not valid JSON"},
		{"unknown shape", `{"hello":"world"}`, "not an in-toto statement"},
		{"old statement type", old, "statement type"},
		{"no subject", `{"_type":"` + statement.StatementType + `","predicateType":"x","subject":[]}`, "no subject"},
		{"empty digest", `{"_type":"` + statement.StatementType + `","predicateType":"x","subject":[{"digest":{"sha256":""}}]}`, "empty sha256 digest"},
		{"subject without digest", `{"_type":"` + statement.StatementType + `","predicateType":"x","subject":[{"name":"a"}]}`, "no digest"},
		{"no predicate type", `{"_type":"` + statement.StatementType + `","subject":[{"digest":{"sha256":"a"}}]}`, "no predicateType"},
		{"envelope of another type", `{"payloadType":"text/plain","payload":"e30="}`, "payload type"},
		{"envelope payload not base64", `{"payloadType":"` + statement.PayloadType + `","payload":"!!"}`, "not base64"},
		{"bundle without envelope", `{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","messageSignature":{}}`, "no DSSE envelope"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := statement.Parse("x.json", []byte(tt.data))
			bad := expectMalformed(t, err, tt.want)

			d := bad.Diagnostic()
			expectPRV009Warning(t, &d)

			if d.Source == nil {
				t.Fatalf("diagnostic = %+v, want a CO-PRV-009 warning that names the file", d)
			}

			expectEqual(t, "source", d.Source.URI, "x.json")
		})
	}
}

func TestLoadAllWarnsForEachBadFileAndKeepsTheOthers(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"build/attestations/b.json":    statementJSON(t, statement.PredicateSLSAProvenance, map[string]any{}),
		"build/attestations/a.json":    statementJSON(t, statement.PredicateSLSAProvenance, map[string]any{}),
		"build/attestations/bad.json":  []byte(`{not json`),
		"build/attestations/gone.json": nil,
	}

	paths := []string{
		"build/attestations/b.json", "build/attestations/bad.json", "build/attestations/a.json",
		"build/attestations/gone.json", "build/attestations/a.json",
	}

	loaded, diags := statement.LoadAll(paths, readFrom(files))

	expectLoadedPaths(t, loaded, "build/attestations/a.json", "build/attestations/b.json")

	if len(diags) != 2 {
		t.Fatalf("diagnostics = %+v, want one per bad file", diags)
	}

	for i, name := range []string{"bad.json", "gone.json"} {
		expectPRV009Warning(t, &diags[i])
		expectContains(t, fmt.Sprintf("message of diagnostic %d", i), diags[i].Message, name)
	}
}
