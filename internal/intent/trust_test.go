package intent_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/trust"
)

const trustVocabClasses = "public,internal,restricted"

// trustExtension gives the `concordia.trust` property of an element; empty JSON means none.
func trustExtension(trustJSON string) string {
	if trustJSON == "" {
		return ""
	}

	return `,"concordia":{"trust":` + trustJSON + `}`
}

// trustDoc builds a two-node CALM document of the given release (`1.2` or `1.3`) with the
// raw JSON `nodeTrust` on node `a` and `relTrust` on the relationship (empty means none).
func trustDoc(release, nodeTrust, relTrust string) string {
	return `{"$schema":"https://calm.finos.org/release/` + release + `/meta/calm.json",` +
		`"nodes":[` +
		`{"unique-id":"a","node-type":"service","name":"a","description":"d"` + trustExtension(nodeTrust) + `},` +
		`{"unique-id":"b","node-type":"service","name":"b","description":"d"}],` +
		`"relationships":[{"unique-id":"r","relationship-type":{"connects":{"source":{"node":"a"},"destination":{"node":"b"}}}` + trustExtension(relTrust) + `}],` +
		`"flows":[{"unique-id":"f","name":"f","description":"d","transitions":[` +
		`{"relationship-unique-id":"r","sequence-number":2,"description":"back","direction":"destination-to-source"},` +
		`{"relationship-unique-id":"r","sequence-number":1,"description":"go"}]}]}`
}

// loadTrustDocWith loads doc from a temporary workspace with the given trust vocabulary.
func loadTrustDocWith(t *testing.T, doc string, vocab trust.Vocabulary) intent.LoadResult {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.calm.json")

	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	return intent.LoadArchitecture(path, intent.LoadContext{Root: dir, Trust: vocab})
}

func loadTrustDoc(t *testing.T, doc string) intent.LoadResult {
	t.Helper()

	return loadTrustDocWith(t, doc, trust.NewVocabulary([]string{"tenant"}, strings.Split(trustVocabClasses, ",")))
}

func requireTrustClean(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	if len(r.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
}

// requireTrustDimensions fails unless got is declared and every dimension of want has that value.
func requireTrustDimensions(t *testing.T, what string, got *trust.Declaration, want map[string]string) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s trust = %+v", what, got)
	}

	for dimension, value := range want {
		if got.Value(dimension) != value {
			t.Fatalf("%s trust = %+v, want %s=%q", what, got, dimension, value)
		}
	}
}

func requireTrustClassification(t *testing.T, what string, got *trust.Declaration, want string) {
	t.Helper()

	if got == nil || got.Classification != want {
		t.Fatalf("%s trust = %+v, want classification %q", what, got, want)
	}
}

func requireTrustCode(t *testing.T, r *intent.LoadResult, code string) *diag.Diagnostic {
	t.Helper()

	d := findCode(r, code)
	if d == nil {
		t.Fatalf("diagnostics = %+v, want %s", r.Diagnostics, code)
	}

	return d
}

func requireTrustInvalidInput(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	if intent.ExitCodeForDiagnostics(r.Diagnostics) != diag.InvalidInput {
		t.Fatalf("diagnostics = %+v, want exit code 2", r.Diagnostics)
	}
}

func TestTrustDimensionsOnNode(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 dimensions on a node. Valid under CALM 1.2 and 1.3.
	for _, release := range []string{"1.2", "1.3"} {
		t.Run("release "+release, func(t *testing.T) {
			t.Parallel()

			r := loadTrustDoc(t, trustDoc(release,
				`{"dimensions":{"organization":"acme","account":"a1","zone":"internet","tenant":"t1"},"classification":"restricted"}`, ""))
			requireTrustClean(t, &r)

			got := r.Model.Nodes[0].Trust
			requireTrustDimensions(t, "node", got, map[string]string{
				"organization": "acme", "account": "a1", "zone": "internet", "tenant": "t1",
			})
			requireTrustClassification(t, "node", got, "restricted")

			if r.Model.Nodes[1].Trust != nil {
				t.Fatalf("a node without trust must have none: %+v", r.Model.Nodes[1].Trust)
			}
		})
	}
}

func TestTrustOnRelationshipAndFlows(t *testing.T) {
	t.Parallel()

	r := loadTrustDoc(t, trustDoc("1.2", "", `{"dimensions":{"zone":"dmz"},"classification":"public"}`))
	requireTrustClean(t, &r)

	rel := r.Model.Relationships[0]
	requireTrustDimensions(t, "relationship", rel.Trust, map[string]string{"zone": "dmz"})
	requireTrustClassification(t, "relationship", rel.Trust, "public")

	if len(r.Model.Flows) != 1 || r.Model.Flows[0].ID != "f" {
		t.Fatalf("flows = %+v", r.Model.Flows)
	}

	transitions := r.Model.Flows[0].Transitions
	if len(transitions) != 2 {
		t.Fatalf("flows = %+v", r.Model.Flows)
	}

	for i, want := range []struct {
		direction string
		sequence  int
	}{{"source-to-destination", 1}, {"destination-to-source", 2}} {
		if transitions[i].Sequence != want.sequence || transitions[i].Direction != want.direction {
			t.Fatalf("transitions = %+v", transitions)
		}
	}
}

func TestTrustUndeclaredDimension(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 undeclared dimension name.
	r := loadTrustDoc(t, trustDoc("1.2", `{"dimensions":{"firewall":"x","zone":"internal"}}`, ""))

	d := requireTrustCode(t, &r, diag.SEC001)
	if d.Severity != diag.SeverityError {
		t.Fatalf("diagnostics = %+v, want an error CO-SEC-001", r.Diagnostics)
	}

	requireTrustInvalidInput(t, &r)

	if d.Source == nil || d.Source.Pointer != "/nodes/0/concordia/trust/dimensions/firewall" {
		t.Fatalf("source = %+v", d.Source)
	}

	// An undeclared dimension must not be kept.
	requireTrustDimensions(t, "node", r.Model.Nodes[0].Trust, map[string]string{"firewall": "", "zone": "internal"})
}

func TestTrustClassificationOutsideList(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 classification outside the declared list.
	r := loadTrustDoc(t, trustDoc("1.2", `{"classification":"secret"}`, ""))

	d := requireTrustCode(t, &r, diag.SEC001)
	requireTrustInvalidInput(t, &r)

	if !strings.Contains(d.Message, "secret") {
		t.Fatalf("diagnostics = %+v, want CO-SEC-001 naming 'secret'", r.Diagnostics)
	}

	if r.Model.Nodes[0].Trust != nil {
		t.Fatalf("an unlisted classification must not be kept: %+v", r.Model.Nodes[0].Trust)
	}

	// Without a workspace list no classification is accepted.
	bare := loadTrustDocWith(t, trustDoc("1.2", `{"classification":"public"}`, ""), trust.Vocabulary{})
	requireTrustCode(t, &bare, diag.SEC001)
}

func TestTrustUnknownKeyFailsSchema(t *testing.T) {
	t.Parallel()

	// Scenario: Unknown trust key.
	for _, trustJSON := range []string{`{"zones":{"zone":"x"}}`, `{"dimensions":{"zone":""}}`, `{"dimensions":{"zone":1}}`} {
		r := loadTrustDoc(t, trustDoc("1.3", trustJSON, ""))

		if !slices.Contains(codes(&r), diag.INT007) {
			t.Errorf("%s: codes = %v, want CO-INT-007", trustJSON, codes(&r))
		}

		if intent.ExitCodeForDiagnostics(r.Diagnostics) != diag.InvalidInput {
			t.Errorf("%s: exit code is not 2", trustJSON)
		}
	}
}
