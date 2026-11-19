package binding_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

func trustEntry(trustYAML string) string {
	return header + "  - id: b1\n    architectureRef: calm:node:billing\n    match:\n      any:\n        - sourceRoot: services/billing/**\n    trust:\n" + trustYAML
}

func loadTrust(t *testing.T, file string, w *workspace.Workspace) binding.Loaded {
	t.Helper()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"bindings/a.yaml": file})

	w.Root = root
	w.Bindings = workspace.Files{Files: []string{"bindings/*.yaml"}}

	got, err := binding.Load(w, modelWith(calmNode()))
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func trustWorkspace() *workspace.Workspace {
	return &workspace.Workspace{Trust: &workspace.Trust{Dimensions: []string{"tenant"}, Classifications: []string{"public", "internal", "restricted"}}}
}

// trustValues gives the value of each named dimension of decl.
func trustValues(decl *trust.Declaration, dimensions ...string) []string {
	out := make([]string, 0, len(dimensions))
	for _, dimension := range dimensions {
		out = append(out, decl.Value(dimension))
	}

	return out
}

// hasSource says whether src names the URI and the JSON pointer.
func hasSource(src *diag.Source, uri, pointer string) bool {
	return src != nil && src.URI == uri && src.Pointer == pointer
}

func TestBindingEntryTrustIsLoaded(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 dimensions on a node (binding entry form).
	got := loadTrust(t, trustEntry("      dimensions: {zone: internal, account: a2, tenant: t1}\n      classification: restricted\n"), trustWorkspace())
	if len(got.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", got.Diagnostics)
	}

	decl := got.Bindings[0].Trust
	if decl == nil {
		t.Fatalf("trust = %+v", decl)
	}

	if !slices.Equal(trustValues(decl, "zone", "account", "tenant"), []string{"internal", "a2", "t1"}) {
		t.Fatalf("trust = %+v", decl)
	}

	if decl.Classification != "restricted" {
		t.Fatalf("trust = %+v", decl)
	}
}

func TestBindingEntryWithoutTrustHasNone(t *testing.T) {
	t.Parallel()

	got := loadTrust(t, header+"  - id: b1\n    architectureRef: calm:node:billing\n    match:\n      any:\n        - sourceRoot: a/**\n", trustWorkspace())
	if got.Bindings[0].Trust != nil {
		t.Fatalf("trust = %+v", got.Bindings[0].Trust)
	}
}

func TestBindingEntryUnknownTrustKeyIsRejected(t *testing.T) {
	t.Parallel()

	// Scenario: Unknown trust key.
	got := loadTrust(t, trustEntry("      zones: {zone: internal}\n"), trustWorkspace())
	if !slices.Contains(codesOf(&got), diag.BND002) {
		t.Fatalf("codes = %v", codesOf(&got))
	}

	if code, _ := diag.ExitCodeOf(diag.BND002); code != diag.InvalidInput {
		t.Fatalf("CO-BND-002 exit code = %d", code)
	}
}

func TestBindingEntryUndeclaredDimensionIsRejected(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 undeclared dimension name.
	got := loadTrust(t, trustEntry("      dimensions: {firewall: x, zone: dmz}\n"), trustWorkspace())
	if !slices.Equal(codesOf(&got), []string{diag.SEC001}) {
		t.Fatalf("codes = %v", codesOf(&got))
	}

	if code, _ := diag.ExitCodeOf(diag.SEC001); code != diag.InvalidInput {
		t.Fatalf("CO-SEC-001 exit code = %d", code)
	}

	if d := got.Diagnostics[0]; !hasSource(d.Source, "bindings/a.yaml", "/bindings/0/trust/dimensions/firewall") {
		t.Fatalf("source = %+v", d.Source)
	}

	decl := got.Bindings[0].Trust
	if decl == nil {
		t.Fatalf("trust = %+v", decl)
	}

	if !slices.Equal(trustValues(decl, "firewall", "zone"), []string{"", "dmz"}) {
		t.Fatalf("trust = %+v", decl)
	}
}

func TestBindingEntryClassificationOutsideTheListIsRejected(t *testing.T) {
	t.Parallel()

	// Scenario: SEC-01 classification outside the declared list.
	got := loadTrust(t, trustEntry("      classification: secret\n"), trustWorkspace())
	if !slices.Equal(codesOf(&got), []string{diag.SEC001}) || got.Bindings[0].Trust != nil {
		t.Fatalf("loaded = %+v", got)
	}

	// A workspace without a trust block accepts no classification.
	got = loadTrust(t, trustEntry("      classification: public\n"), &workspace.Workspace{})
	if !slices.Equal(codesOf(&got), []string{diag.SEC001}) {
		t.Fatalf("codes = %v", codesOf(&got))
	}
}

func TestWithTrustOverridesTheWorkspaceVocabulary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"bindings/a.yaml": trustEntry("      classification: public\n")})

	w := &workspace.Workspace{Root: root, Bindings: workspace.Files{Files: []string{"bindings/*.yaml"}}}

	got, err := binding.Load(w, modelWith(calmNode()), binding.WithTrust(trust.NewVocabulary(nil, []string{"public"})))
	if err != nil || len(got.Diagnostics) != 0 {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
}
