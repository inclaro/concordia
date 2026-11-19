package trust_test

import (
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/trust"
)

const referencePage = "../../docs/reference/trust-context.md"

// specDependencyKinds is the closed list of the trust-context spec ("Dependency kinds eligible for assessments"),
// written out here so that a change of the list in code or in the spec fails this test.
var specDependencyKinds = []string{
	"runtime-identity", "role-binding", "secret-reference", "configuration-reference", "storage-reference",
	"public-entry-point", "protocol", "certificate-reference", "external-destination", "trust-context",
	"data-classification", "placement", "binding", "control", "implementation", "verification", "quality-scenario",
}

func TestDependencyKindsAreTheSpecList(t *testing.T) {
	t.Parallel()

	got := trust.DependencyKinds()
	if len(got) != len(specDependencyKinds) {
		t.Fatalf("DependencyKinds() = %v, want %v", got, specDependencyKinds)
	}

	for i := range got {
		if got[i] != specDependencyKinds[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], specDependencyKinds[i])
		}
	}
}

// Every dependency kind of the trust-context spec appears in the reference page, in its kinds table.
func TestEveryDependencyKindAppearsInTheReference(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(referencePage)
	if err != nil {
		t.Fatal(err)
	}

	page := string(raw)

	for _, kind := range specDependencyKinds {
		if !strings.Contains(page, "| `"+kind+"` |") {
			t.Errorf("%s does not list the dependency kind %q in its table", referencePage, kind)
		}
	}
}

func TestReferenceNamesTheBuiltInDimensions(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(referencePage)
	if err != nil {
		t.Fatal(err)
	}

	for _, dimension := range trust.BuiltinDimensions() {
		if !strings.Contains(string(raw), "| `"+dimension+"` |") {
			t.Errorf("%s does not describe the dimension %q", referencePage, dimension)
		}
	}
}
