package statement_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/redact"
)

const (
	// token has the shape of a GitHub token, which the redaction pass knows.
	token = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"
	// secretValue is a value under a sensitive name, which the redaction pass redacts as a whole.
	secretValue = "hunter2-hunter2"
)

func provenanceWithSecrets() map[string]any {
	return map[string]any{
		"buildDefinition": map[string]any{
			"buildType": "https://actions.github.io/buildtypes/workflow/v1",
			"externalParameters": map[string]any{
				"workflow":  map[string]any{"ref": "refs/heads/main", "repository": "https://github.com/acme/shop"},
				"authToken": token,
				"inputs":    map[string]any{"password": secretValue},
			},
			"internalParameters": map[string]any{"github": map[string]any{"event_name": "push"}},
			"resolvedDependencies": []any{
				map[string]any{
					"uri":         "git+https://ci-bot:" + secretValue + "@github.com/acme/shop@refs/heads/main",
					"digest":      map[string]any{"gitCommit": "0123456789abcdef0123456789abcdef01234567"},
					"annotations": map[string]any{"note": token},
				},
			},
		},
		"runDetails": map[string]any{
			"builder":  map[string]any{"id": "https://github.com/acme/shop/.github/workflows/build.yml@refs/heads/main"},
			"metadata": map[string]any{"invocationId": "run-1", "finishedOn": "2026-10-30T12:00:00Z"},
			"byproducts": []any{
				map[string]any{"name": "log", "content": token},
			},
		},
	}
}

// mustPersist persists the statement with the default rules and stops the test on an error.
func mustPersist(t *testing.T, loaded *statement.Loaded) statement.Persisted {
	t.Helper()

	persisted, err := statement.Persist(loaded, redact.Default())
	if err != nil {
		t.Fatal(err)
	}

	return persisted
}

// persistedJSON gives the JSON of the persisted statement and stops the test on an error.
func persistedJSON(t *testing.T, persisted *statement.Persisted) []byte {
	t.Helper()

	out, err := persisted.JSON()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// expectDeclaredFieldsKept reports a persisted statement that lost a field its contract declares, or kept the
// external parameters.
func expectDeclaredFieldsKept(t *testing.T, persisted *statement.Persisted, out []byte) {
	t.Helper()

	build, _ := persisted.Predicate["buildDefinition"].(map[string]any)
	if _, ok := build["externalParameters"]; ok {
		t.Errorf("the external parameters were kept: %s", out)
	}

	var decoded struct {
		Predicate struct {
			RunDetails struct {
				Builder struct {
					ID string `json:"id"`
				} `json:"builder"`
			} `json:"runDetails"`
			BuildDefinition struct {
				BuildType            string           `json:"buildType"`
				ResolvedDependencies []map[string]any `json:"resolvedDependencies"`
			} `json:"buildDefinition"`
		} `json:"predicate"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}

	expectEqual(t, "the builder id is kept", decoded.Predicate.RunDetails.Builder.ID != "", true)
	expectEqual(t, "the build type is kept", decoded.Predicate.BuildDefinition.BuildType != "", true)
	expectEqual(t, "the resolved dependencies kept", len(decoded.Predicate.BuildDefinition.ResolvedDependencies), 1)
}

// recordsByProperty indexes the records by property and reports a record that does not name the output.
func recordsByProperty(t *testing.T, persisted *statement.Persisted, output string) map[string]redact.Record {
	t.Helper()

	byProperty := make(map[string]redact.Record, len(persisted.Redactions))

	for _, rec := range persisted.Redactions {
		byProperty[rec.Property] = rec

		if rec.Output != output {
			t.Errorf("record %+v does not name the bundle output", rec)
		}
	}

	return byProperty
}

// expectRecordClass reports a property that has no record of the wanted class.
func expectRecordClass(t *testing.T, byProperty map[string]redact.Record, property, class string) {
	t.Helper()

	rec, ok := byProperty[property]
	if !ok || rec.Class != class {
		t.Errorf("record of %s = %+v (found %v), want class %s", property, rec, ok, class)
	}
}

func TestPersistKeepsOnlyDeclaredFieldsAndRecordsRemovals(t *testing.T) {
	t.Parallel()

	data := bundleJSON(t, statementJSON(t, statement.PredicateSLSAProvenance, provenanceWithSecrets()))
	loaded := mustParse(t, "build/attestations/shop.json", data)

	persisted := mustPersist(t, &loaded)
	out := persistedJSON(t, &persisted)

	expectNotContains(t, string(out), token, secretValue, "ghp_")
	expectDeclaredFieldsKept(t, &persisted, out)
	expectEqual(t, "digest", persisted.Digest, loaded.Digest)

	byProperty := recordsByProperty(t, &persisted, "assurance/statements/"+strings.TrimPrefix(loaded.Digest, "sha256:")+".json")

	// A field outside the contract is dropped because the contract does not list it, not because it was found to be a
	// secret: its record has the class outside-contract.
	for _, removed := range []string{
		"buildDefinition.externalParameters", "buildDefinition.internalParameters",
		"buildDefinition.resolvedDependencies[0].annotations", "runDetails.byproducts",
	} {
		expectRecordClass(t, byProperty, removed, redact.ClassOutsideContract)
	}

	// The credential in a kept string is redacted, and the record never holds the value.
	expectRecordClass(t, byProperty, "buildDefinition.resolvedDependencies[0].uri", redact.ClassCredential)

	recorded, err := json.Marshal(persisted.Redactions)
	if err != nil {
		t.Fatal(err)
	}

	expectNotContains(t, string(recorded), token, secretValue)
}

func TestPersistIsDeterministic(t *testing.T) {
	t.Parallel()

	loaded := mustParse(t, "a.json", statementJSON(t, statement.PredicateSLSAProvenance, provenanceWithSecrets()))

	first := mustPersist(t, &loaded)
	second, _ := statement.Persist(&loaded, redact.Default())

	one, _ := first.JSON()
	two, _ := second.JSON()

	if !bytes.Equal(one, two) {
		t.Errorf("two persists differ:\n%s\n%s", one, two)
	}

	if !slices.IsSortedFunc(first.Redactions, func(a, b redact.Record) int { return canonjson.CompareUTF16(a.Property, b.Property) }) {
		t.Errorf("records are not sorted: %+v", first.Redactions)
	}
}

func TestPersistDropsTheUndeclaredFieldsOfEveryPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		predicateType string
		predicate     map[string]any
		removed       string
	}{
		{"check result", statement.PredicateCheckResult, map[string]any{"check": "unit-tests", "env": map[string]any{"API_TOKEN": token}}, "env"},
		{"deployment record", statement.PredicateDeploymentRecord, map[string]any{"environment": "production", "values": map[string]any{"password": secretValue}}, "values"},
		{"workload observation", statement.PredicateWorkloadObservation, map[string]any{"environment": "production", "envVars": []any{token}}, "envVars"},
		{"measurement", statement.PredicateMeasurement, map[string]any{"metric": "latency", "headers": map[string]any{"authorization": "Bearer " + secretValue}}, "headers"},
		{"plan", statement.PredicatePlan, map[string]any{"environment": "production", "variables": map[string]any{"db_password": secretValue}}, "variables"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loaded := mustParse(t, "x.json", statementJSON(t, tt.predicateType, tt.predicate))
			persisted := mustPersist(t, &loaded)

			out, _ := persisted.JSON()
			expectNotContains(t, string(out), token, secretValue)

			if !slices.ContainsFunc(persisted.Redactions, func(r redact.Record) bool { return r.Property == tt.removed }) {
				t.Errorf("no record for %s: %+v", tt.removed, persisted.Redactions)
			}
		})
	}
}

func TestPersistKeepsNothingOfAnUnknownPredicateType(t *testing.T) {
	t.Parallel()

	loaded := mustParse(t, "x.json", statementJSON(t, "https://example.test/custom/v1", map[string]any{"token": token}))
	persisted := mustPersist(t, &loaded)

	out, _ := persisted.JSON()
	expectNotContains(t, string(out), token)
	expectEqual(t, "predicate is empty", persisted.Predicate == nil, true)

	if len(persisted.Redactions) != 1 {
		t.Fatalf("persisted = %s, records = %+v", out, persisted.Redactions)
	}

	expectEqual(t, "class", persisted.Redactions[0].Class, redact.ClassOutsideContract)
}
