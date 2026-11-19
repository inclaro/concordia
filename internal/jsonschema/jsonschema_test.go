package jsonschema_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/inclaro/concordia/internal/jsonschema"
)

const sarifSchemaPath = "../../test/schemas/sarif-schema-2.1.0.json"

const (
	validName = "ab"
	nameKey   = "name"
)

const baseSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.test/base.json",
  "$defs": {
    "name": { "type": "string", "minLength": 2 }
  }
}`

const mainSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.test/main.json",
  "type": "object",
  "required": ["name"],
  "additionalProperties": false,
  "properties": {
    "name": { "$ref": "base.json#/$defs/name" },
    "when": { "type": "string", "format": "date-time" },
    "kind": { "enum": ["a", "b"] },
    "n": { "type": "integer", "maximum": 3 }
  }
}`

func compileMain(t *testing.T) *jsonschema.Schema {
	t.Helper()

	r := jsonschema.NewRegistry()

	for _, doc := range []string{baseSchema, mainSchema} {
		if _, err := r.Add([]byte(doc)); err != nil {
			t.Fatal(err)
		}
	}

	s, err := r.Compile("https://example.test/main.json")
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestSchemasReferToEachOther(t *testing.T) {
	t.Parallel()

	s := compileMain(t)

	if errs := s.Validate(map[string]any{nameKey: validName}); errs != nil {
		t.Fatalf("valid instance gave errors: %v", errs)
	}

	errs := s.Validate(map[string]any{nameKey: "a"})
	if len(errs) != 1 || errs[0].InstancePath != "/name" {
		t.Fatalf("errors %+v", errs)
	}
}

func TestErrorsHaveInstancePathsAndNames(t *testing.T) {
	t.Parallel()

	s := compileMain(t)

	errs := s.Validate(map[string]any{"kind": "c", "n": 5, "bogus": 1})
	paths := map[string]string{}

	for _, e := range errs {
		paths[e.InstancePath] = e.Message
	}

	for _, p := range []string{"", "/kind", "/n"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("no error for %q in %+v", p, errs)
		}
	}

	found := map[string]bool{}

	for _, e := range errs {
		for _, word := range []string{"bogus", nameKey} {
			if strings.Contains(e.Message, word) {
				found[word] = true
			}
		}
	}

	if !found["bogus"] || !found[nameKey] {
		t.Errorf("messages must name the property: %+v", errs)
	}
}

func TestFormatsAreAsserted(t *testing.T) {
	t.Parallel()

	s := compileMain(t)

	if errs := s.Validate(map[string]any{nameKey: validName, "when": "2026-01-02T03:04:05Z"}); errs != nil {
		t.Fatalf("errors %v", errs)
	}

	errs := s.Validate(map[string]any{nameKey: validName, "when": "yesterday"})
	if len(errs) != 1 || errs[0].InstancePath != "/when" {
		t.Fatalf("errors %+v", errs)
	}
}

func TestErrorsAreUnique(t *testing.T) {
	t.Parallel()

	schema := `{
	  "$id": "https://example.test/dup.json",
	  "allOf": [
	    {"properties": {"a": {"type": "string"}}},
	    {"properties": {"a": {"type": "string"}}},
	    {"$ref": "#/$defs/s"}
	  ],
	  "$defs": {"s": {"properties": {"a": {"type": "string"}}}}
	}`

	s, err := jsonschema.Compile("https://example.test/dup.json", []byte(schema))
	if err != nil {
		t.Fatal(err)
	}

	errs := s.Validate(map[string]any{"a": 1})
	if len(errs) != 1 {
		t.Fatalf("want one error without duplicates, got %+v", errs)
	}
}

func TestValidateJSONAndGoValues(t *testing.T) {
	t.Parallel()

	s := compileMain(t)

	if errs := s.ValidateJSON([]byte(`{"name":"ab","n":3}`)); errs != nil {
		t.Errorf("errors %v", errs)
	}

	if errs := s.ValidateJSON([]byte(`{`)); len(errs) != 1 {
		t.Errorf("errors %v", errs)
	}

	type typed struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}

	if errs := s.Validate(typed{Name: validName, N: 2}); errs != nil {
		t.Errorf("errors %v", errs)
	}

	if errs := s.Validate(make(chan int)); len(errs) != 1 {
		t.Errorf("errors %v", errs)
	}
}

func TestAddFS(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"schemas/base.json":  {Data: []byte(baseSchema)},
		"schemas/main.json":  {Data: []byte(mainSchema)},
		"schemas/readme.txt": {Data: []byte("ignored")},
	}
	r := jsonschema.NewRegistry()

	if err := r.AddFS(fsys, "schemas"); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Compile("https://example.test/main.json"); err != nil {
		t.Fatal(err)
	}

	if err := r.AddFS(fsys, "missing"); err == nil {
		t.Error("expected an error for a missing directory")
	}
}

func TestAddErrors(t *testing.T) {
	t.Parallel()

	r := jsonschema.NewRegistry()

	if _, err := r.Add([]byte(`{"type":"string"}`)); !errors.Is(err, jsonschema.ErrNoID) {
		t.Errorf("err %v", err)
	}

	if _, err := r.Add([]byte(`{`)); err == nil {
		t.Error("expected a parse error")
	}

	if _, err := r.Compile("https://example.test/unknown.json"); err == nil {
		t.Error("expected an error for an unknown schema")
	}
}

func TestSARIFSchemaDraft4CompilesAndValidates(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(sarifSchemaPath)
	if err != nil {
		t.Fatal(err)
	}

	s, err := jsonschema.Compile("https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json", doc)
	if err != nil {
		t.Fatal(err)
	}

	valid := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"Concordia","version":"dev"}},"results":[{"ruleId":"CO-BND-001","level":"error","message":{"text":"m"}}]}]}`
	if errs := s.ValidateJSON([]byte(valid)); errs != nil {
		t.Fatalf("valid SARIF gave errors: %+v", errs)
	}

	invalid := `{"version":"2.1.0","runs":[{"tool":{"driver":{}},"results":[{"level":"bogus","message":{"text":"m"}}]}]}`

	errs := s.ValidateJSON([]byte(invalid))
	if len(errs) < 2 {
		t.Fatalf("invalid SARIF must give errors: %+v", errs)
	}
}
