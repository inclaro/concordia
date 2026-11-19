package statement_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/redact"
)

func slsaStatement(t *testing.T, extra map[string]any) statement.Loaded {
	t.Helper()

	predicate := provenanceWithSecrets()

	for key, value := range extra {
		predicate[key] = value
	}

	details, _ := predicate["runDetails"].(map[string]any)
	metadata, _ := details["metadata"].(map[string]any)
	metadata["buildLevel"] = 3

	return mustParse(t, "build/attestations/shop.json", statementJSON(t, statement.PredicateSLSAProvenance, predicate,
		statement.Subject{Name: "registry.example/shop", Digest: map[string]string{"sha256": "aaa"}}))
}

func TestParseSLSAKeepsTheFourFactsAndTheVerification(t *testing.T) {
	t.Parallel()

	verification := statement.Verification{Signature: statement.SignatureVerified, ProducerID: "ci", Subject: statement.SubjectUnchecked}

	loaded := slsaStatement(t, nil)

	got, err := statement.ParseSLSA(&loaded.Statement, verification)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Subjects) != 1 || got.Subjects[0].Digest["sha256"] != "aaa" {
		t.Errorf("subjects = %+v", got.Subjects)
	}

	if got.BuildType != "https://actions.github.io/buildtypes/workflow/v1" {
		t.Errorf("build type = %q", got.BuildType)
	}

	if len(got.ResolvedDependencies) != 1 || got.ResolvedDependencies[0].Digest["gitCommit"] == "" ||
		!strings.HasPrefix(got.ResolvedDependencies[0].URI, "git+https://") {
		t.Errorf("resolved dependencies = %+v", got.ResolvedDependencies)
	}

	if got.BuilderID != "https://github.com/acme/shop/.github/workflows/build.yml@refs/heads/main" {
		t.Errorf("builder = %q", got.BuilderID)
	}

	if !got.Time().Equal(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)) || got.InvocationID != "run-1" {
		t.Errorf("time = %v, invocation = %q", got.Time(), got.InvocationID)
	}

	if got.Verification != verification || len(got.Missing) != 0 {
		t.Errorf("verification = %+v, missing = %v", got.Verification, got.Missing)
	}
}

func TestParseSLSAListsMissingRequiredFields(t *testing.T) {
	t.Parallel()

	loaded := mustParse(t, "x.json", statementJSON(t, statement.PredicateSLSAProvenance, map[string]any{"buildDefinition": map[string]any{}}))

	got, err := statement.ParseSLSA(&loaded.Statement, statement.Verification{})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"buildDefinition.buildType", "runDetails.builder.id"}; !reflect.DeepEqual(got.Missing, want) {
		t.Errorf("missing = %v, want %v", got.Missing, want)
	}

	plan := mustParse(t, "y.json", statementJSON(t, statement.PredicatePlan, map[string]any{}))
	if _, err = statement.ParseSLSA(&plan.Statement, statement.Verification{}); err == nil {
		t.Error("a predicate of another type must be an error")
	}
}

// TestSLSALevelIsIgnored: a producer may state a level; Concordia neither reads nor shows it.
func TestSLSALevelIsIgnored(t *testing.T) {
	t.Parallel()

	loaded := slsaStatement(t, map[string]any{"slsaLevel": 3, "verificationSummary": map[string]any{"verifiedLevels": []any{"SLSA_BUILD_LEVEL_3"}}})

	got, err := statement.ParseSLSA(&loaded.Statement, statement.Verification{})
	if err != nil {
		t.Fatal(err)
	}

	for typ, name := range map[reflect.Type]string{reflect.TypeOf(got): "SLSA", reflect.TypeOf(got.ResolvedDependencies).Elem(): "ResourceDescriptor"} {
		for i := range typ.NumField() {
			if strings.Contains(strings.ToLower(typ.Field(i).Name), "level") {
				t.Errorf("%s has the field %s: Concordia claims no SLSA level", name, typ.Field(i).Name)
			}
		}
	}

	persisted, err := statement.Persist(&loaded, redact.Default())
	if err != nil {
		t.Fatal(err)
	}

	out, err := json.Marshal(persisted.Predicate)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(strings.ToLower(string(out)), "level") {
		t.Errorf("the persisted provenance shows a level: %s", out)
	}

	// The producer's field is removed and the removal is recorded, not shown as a claim.
	found := false

	for _, rec := range persisted.Redactions {
		found = found || rec.Property == "slsaLevel" || rec.Property == "verificationSummary" || rec.Property == "runDetails.metadata.buildLevel"
	}

	if !found {
		t.Errorf("no record of the removed level fields: %+v", persisted.Redactions)
	}
}

// levelKey finds the keys that state a SLSA level: `slsaLevel`, `slsa_level`, `slsa-build-level`, `buildLevel`.
var levelKey = regexp.MustCompile(`(?im)(?:"[^"\s]*(?:slsa[^"\s]*level|level[^"\s]*slsa|build[-_]?level)[^"\s]*"\s*:|^\s*[\w.-]*(?:slsa[\w.-]*level|level[\w.-]*slsa|build[-_]?level)[\w.-]*\s*:)`)

func TestLevelKeyScannerFindsLevels(t *testing.T) {
	t.Parallel()

	for _, text := range []string{`{"slsaLevel": 3}`, `{"x":{"buildLevel":"3"}}`, "slsa_level: 3", `{"slsa-build-level" : 2}`} {
		if !levelKey.MatchString(text) {
			t.Errorf("the scanner misses %q", text)
		}
	}

	for _, text := range []string{`{"level": "declared"}`, `{"evidenceLevel": "x"}`, "the rule says no SLSA level is claimed"} {
		if levelKey.MatchString(text) {
			t.Errorf("the scanner flags %q", text)
		}
	}
}

// TestNoOutputSchemaOrGoldenStatesASLSALevel scans every output schema and every golden file of the repository: no
// output carries a SLSA level (spec artifact-provenance, "no output field states a SLSA level").
func TestNoOutputSchemaOrGoldenStatesASLSALevel(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	scanned := 0

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		if entry.IsDir() {
			switch rel {
			case ".git", ".bin", "node_modules", "docs":
				return filepath.SkipDir
			}

			return nil
		}

		isSchema := strings.HasPrefix(rel, "internal/") && strings.Contains(rel, "/schemas/") && strings.HasSuffix(rel, ".json")
		if !isSchema && !strings.HasPrefix(rel, "test/golden/") {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		scanned++

		if loc := levelKey.FindIndex(data); loc != nil {
			t.Errorf("%s states a SLSA level: %q", rel, data[loc[0]:loc[1]])
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if scanned < 10 {
		t.Fatalf("scanned %d files; the walk does not reach the schemas and goldens", scanned)
	}
}
