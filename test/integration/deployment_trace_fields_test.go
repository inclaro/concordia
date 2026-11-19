package integration_test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
)

// documentedTraceFields are the fields of the `deploymentTrace` section, as docs/reference/results.md lists them.
var documentedTraceFields = map[string][]string{
	"section":     {"environments", "levels", "status", "summary", "unresolvedEnvironmentActions", "unresolvedEnvironmentWorkloads"},
	"environment": {"actions", "cluster", "coverage", "name", "subjects", "unboundWorkloads"},
	"subject":     {"architectureId", "environment", "gaps", "level", "limits", "rules", "status", "steps"},
	"step":        {"entities", "gaps", "limits", "resolution", "role", "step"},
	"entity":      {"bindings", "consumers", "identity", "kind", "lineage", "locations", "properties", "resolution"},
	"gap":         {"detail", "lossModes", "reason", "step"},
	"summary":     {"byStatus", "environments", "subjects"},
}

// optionalTraceFields are documented fields that the infra recording does not have to show in every run.
var optionalTraceFields = []string{"bindings", "consumers", "lineage", "cluster"}

func objectOf(t *testing.T, value any) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%v is not an object", value)
	}

	return object
}

func listOf(t *testing.T, value any) []map[string]any {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%v is not a list", value)
	}

	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, objectOf(t, item))
	}

	return out
}

func keysOf(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// seenTraceFields holds, for every kind of object of the section, the fields that the output has.
type seenTraceFields map[string]map[string]bool

func (s seenTraceFields) note(kind string, object map[string]any) {
	if s[kind] == nil {
		s[kind] = map[string]bool{}
	}

	for _, key := range keysOf(object) {
		s[kind][key] = true
	}
}

func (s seenTraceFields) noteSection(t *testing.T, section map[string]any) {
	t.Helper()

	s.note("section", section)
	s.note("summary", objectOf(t, section["summary"]))

	for _, environment := range listOf(t, section["environments"]) {
		s.note("environment", environment)

		for _, subject := range listOf(t, environment["subjects"]) {
			s.noteSubject(t, subject)
		}
	}
}

func (s seenTraceFields) noteSubject(t *testing.T, subject map[string]any) {
	t.Helper()

	s.note("subject", subject)

	for _, gap := range listOf(t, subject["gaps"]) {
		s.note("gap", gap)
	}

	for _, step := range listOf(t, subject["steps"]) {
		s.note("step", step)

		for _, entity := range listOf(t, step["entities"]) {
			s.note("entity", entity)
		}
	}
}

// requireDocumentedKind checks one kind of object: every documented field shows in the output and in the docs, and
// every field of the output is documented.
func requireDocumentedKind(t *testing.T, kind string, documented []string, seen map[string]bool, docs string) {
	t.Helper()

	for _, field := range documented {
		requireDocumentedField(t, kind, field, seen[field], docs)
	}

	for field := range seen {
		if !slices.Contains(documented, field) {
			t.Errorf("the %s has the undocumented field %q", kind, field)
		}
	}
}

func requireDocumentedField(t *testing.T, kind, field string, inOutput bool, docs string) {
	t.Helper()

	if !inOutput && !slices.Contains(optionalTraceFields, field) {
		t.Errorf("documented %s field %q is not in the output", kind, field)
	}

	if !strings.Contains(docs, "`"+field+"`") {
		t.Errorf("docs/reference/results.md does not document %q", field)
	}
}

func TestDeploymentTraceDocumentedFieldsExist(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	text, err := canonjson.Marshal(d.run.DeploymentTrace)
	if err != nil {
		t.Fatal(err)
	}

	var section map[string]any
	if err = json.Unmarshal(text, &section); err != nil {
		t.Fatal(err)
	}

	docs := string(mustRead(t, "../../docs/reference/results.md"))

	// Every field that the output has is documented, and every documented field of an object shows in the output.
	seen := seenTraceFields{}
	seen.noteSection(t, section)

	for kind, fields := range documentedTraceFields {
		requireDocumentedKind(t, kind, fields, seen[kind], docs)
	}

	if _, err = os.Stat("../../docs/reference/results.md"); err != nil {
		t.Fatal(err)
	}
}
