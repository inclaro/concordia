package intent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

const fixtures = "../../test/fixtures/calm"

func load(t *testing.T, name string, mutate func(*intent.LoadContext)) intent.LoadResult {
	t.Helper()

	root, err := filepath.Abs(fixtures)
	if err != nil {
		t.Fatal(err)
	}

	ctx := intent.LoadContext{Root: root}
	if mutate != nil {
		mutate(&ctx)
	}

	return intent.LoadArchitecture(filepath.Join(root, name), ctx)
}

func codes(r *intent.LoadResult) []string {
	out := make([]string, len(r.Diagnostics))
	for i := range r.Diagnostics {
		out[i] = r.Diagnostics[i].Code
	}

	return out
}

func errorsOf(r *intent.LoadResult) []diag.Diagnostic {
	var out []diag.Diagnostic

	for i := range r.Diagnostics {
		if r.Diagnostics[i].Severity == diag.SeverityError {
			out = append(out, r.Diagnostics[i])
		}
	}

	return out
}

func findCode(r *intent.LoadResult, code string) *diag.Diagnostic {
	for i := range r.Diagnostics {
		if r.Diagnostics[i].Code == code {
			return &r.Diagnostics[i]
		}
	}

	return nil
}

func TestAcceptsAValidDocument(t *testing.T) {
	t.Parallel()

	r := load(t, "valid.calm.json", nil)
	if errs := errorsOf(&r); len(errs) != 0 {
		t.Fatalf("errors: %+v", errs)
	}

	checkValidNodes(t, &r)
	checkValidObservation(t, &r)
	checkValidControl(t, &r)

	if !strings.HasPrefix(r.Model.Digest, "sha256:") {
		t.Errorf("digest = %q", r.Model.Digest)
	}
}

func checkValidNodes(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	ids := make([]string, 0, 8)
	for i := range r.Model.Nodes {
		ids = append(ids, r.Model.Nodes[i].ID)
	}

	if want := []string{"customer", "billing-service", "fulfillment-service"}; !slices.Equal(ids, want) {
		t.Fatalf("nodes = %v", ids)
	}

	b := &r.Model.Nodes[1]
	if b.Owner != "team-billing" {
		t.Errorf("owner = %q", b.Owner)
	}

	if want := []intent.ExtBinding{{Kind: "source-root", Value: "services/billing/**"}}; !reflect.DeepEqual(b.Bindings, want) {
		t.Errorf("bindings = %v", b.Bindings)
	}
}

func checkValidObservation(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	obs := r.Model.Relationships[0].Observation
	want := &intent.RelationshipObservation{
		AllowedEdgeKinds:  []observation.CanonicalEdgeKind{observation.KindImports, observation.KindCalls},
		MinimumConfidence: observation.ConfidenceSyntactic,
		Required:          true,
	}

	if !reflect.DeepEqual(obs, want) {
		t.Errorf("observation = %+v", obs)
	}
}

func checkValidControl(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	c := &r.Model.Controls[0]
	if c.Config == nil || !slices.Equal(c.Config.RuleIDs, []string{"CO-BND-001"}) {
		t.Errorf("config = %+v", c.Config)
	}

	if c.Scope != (intent.ControlScope{Level: intent.ScopeNode, ID: "billing-service"}) {
		t.Errorf("scope = %+v", c.Scope)
	}
}

// diagnosticPointer gives the JSON Pointer of the source of a diagnostic, or "" when it has none.
func diagnosticPointer(d *diag.Diagnostic) string {
	if d == nil || d.Source == nil {
		return ""
	}

	return d.Source.Pointer
}

func TestReportsANodeWithoutNodeType(t *testing.T) {
	t.Parallel()

	r := load(t, "missing-node-type.calm.json", nil)

	for i := range r.Diagnostics {
		d := &r.Diagnostics[i]
		if d.Code == diag.INT002 && diagnosticPointer(d) == "/nodes/0" {
			if d.Source.URI != "missing-node-type.calm.json" {
				t.Errorf("uri = %q", d.Source.URI)
			}

			return
		}
	}

	t.Fatalf("no CO-INT-002 at /nodes/0: %+v", r.Diagnostics)
}

func TestReportsADuplicateID(t *testing.T) {
	t.Parallel()

	r := load(t, "duplicate-id.calm.json", nil)
	if !slices.Contains(codes(&r), diag.INT004) {
		t.Fatalf("codes = %v", codes(&r))
	}
}

func TestReportsAnUnknownConnectsDestination(t *testing.T) {
	t.Parallel()

	r := load(t, "bad-endpoint.calm.json", nil)
	if !slices.Contains(codes(&r), diag.INT005) {
		t.Fatalf("codes = %v", codes(&r))
	}
}

func TestReportsAnIncorrectC4Level(t *testing.T) {
	t.Parallel()

	r := load(t, "bad-c4-level.calm.json", nil)

	d := findCode(&r, diag.INT007)
	if diagnosticPointer(d) != "/nodes/1/concordia/c4-level" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestRejectsAnUnsupportedSchemaVersion(t *testing.T) {
	t.Parallel()

	r := load(t, "old-schema.calm.json", nil)
	if !slices.Equal(codes(&r), []string{diag.INT003}) || r.Model != nil {
		t.Fatalf("codes = %v, model = %v", codes(&r), r.Model)
	}
}

func TestRemoteConfigURLWithoutAMappingGivesExitCode6(t *testing.T) {
	t.Parallel()

	r := load(t, "remote-config-url.calm.json", nil)
	if !slices.Contains(codes(&r), diag.INT012) {
		t.Fatalf("codes = %v", codes(&r))
	}

	if got := intent.ExitCodeForDiagnostics(r.Diagnostics); got != diag.Network {
		t.Fatalf("exit code = %d", got)
	}
}

func TestResolvesAMappedRemoteConfigURL(t *testing.T) {
	t.Parallel()

	cfg := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(cfg, []byte(`{"engine":"concordia","rule-ids":["CO-X-001"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r := load(t, "remote-config-url.calm.json", func(c *intent.LoadContext) {
		c.URLMapping = map[string]string{"https://hub.example/cfg.json": cfg}
	})

	if len(r.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}

	if want := []workspace.ResourceRef{{URI: "https://hub.example/cfg.json", AbsPath: cfg}}; !reflect.DeepEqual(r.Resources, want) {
		t.Fatalf("resources = %+v", r.Resources)
	}

	if got := r.Model.Controls[0].Config.RuleIDs; !slices.Equal(got, []string{"CO-X-001"}) {
		t.Fatalf("rule ids = %v", got)
	}
}

func TestReportsAnUnreadableFileAsINT001(t *testing.T) {
	t.Parallel()

	r := load(t, "nope.json", nil)
	if !slices.Equal(codes(&r), []string{diag.INT001}) {
		t.Fatalf("codes = %v", codes(&r))
	}
}

func TestReportsPatternViolationsAsINT006(t *testing.T) {
	t.Parallel()

	pattern, err := filepath.Abs(filepath.Join(fixtures, "pattern.json"))
	if err != nil {
		t.Fatal(err)
	}

	r := load(t, "valid.calm.json", func(c *intent.LoadContext) { c.Patterns = []string{pattern} })

	var found []diag.Diagnostic

	for i := range r.Diagnostics {
		if r.Diagnostics[i].Code == diag.INT006 {
			found = append(found, r.Diagnostics[i])
		}
	}

	if len(found) == 0 || found[0].Source.Pointer != "/nodes" {
		t.Fatalf("diagnostics = %+v", r.Diagnostics)
	}
}

func TestReportsAnUnknownAllowedEdgeKindAsINT007(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join(fixtures, "valid.calm.json"))
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	rel, _ := doc["relationships"].([]any)[0].(map[string]any)
	ext, _ := rel["concordia"].(map[string]any)
	obs, _ := ext["observation"].(map[string]any)
	obs["allowed-edge-kinds"] = []any{"TELEPORTS"}

	dir := t.TempDir()

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(dir, "a.json")
	if err := os.WriteFile(file, out, 0o600); err != nil {
		t.Fatal(err)
	}

	r := intent.LoadArchitecture(file, intent.LoadContext{Root: dir})
	if !slices.Contains(codes(&r), diag.INT007) {
		t.Fatalf("codes = %v", codes(&r))
	}
}

func TestLoadURLMapping(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "arch"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "arch", "map.json"), []byte(`{"https://x/a.json":"a.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{Root: dir, Architecture: workspace.Architecture{URLMapping: "arch/map.json"}}

	got, err := intent.LoadURLMapping(ws)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(dir, "arch", "a.json"); got["https://x/a.json"] != want {
		t.Fatalf("mapping = %v", got)
	}

	empty, err := intent.LoadURLMapping(&workspace.Workspace{Root: dir})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty = %v, %v", empty, err)
	}
}

func TestRejectsCalm10And11Documents(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"1.0", "1.1"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "a.calm.json")
		doc := `{"$schema":"https://calm.finos.org/release/` + version + `/meta/calm.json","nodes":[],"relationships":[]}`

		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}

		r := intent.LoadArchitecture(path, intent.LoadContext{Root: dir})
		if r.Model != nil {
			t.Fatalf("version %s: model = %+v, want nil", version, r.Model)
		}

		found := false
		for _, d := range r.Diagnostics {
			found = found || d.Code == "CO-INT-003"
		}

		if !found {
			t.Fatalf("version %s: diagnostics = %+v", version, r.Diagnostics)
		}
	}
}

// writeCalmDocument writes a one-node architecture with the given $schema member and node, and gives its path.
func writeCalmDocument(t *testing.T, schema, node string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "a.calm.json")
	// The relationship keeps the node referenced (architecture-nodes-must-be-referenced).
	doc := `{` + schema + `"nodes":[` + node + `],"relationships":[` +
		`{"unique-id":"r","relationship-type":{"interacts":{"actor":"a","nodes":["a"]}}}]}`

	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// acceptedAs tells whether a load gave a model of the schema version and no diagnostics.
func acceptedAs(r *intent.LoadResult, version string) bool {
	if r.Model == nil {
		return false
	}

	return r.Model.SchemaVersion == version && len(r.Diagnostics) == 0
}

func TestLoadAcceptsCalm12And13(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, schema, want string }{
		{"1.2", `"$schema":"https://calm.finos.org/release/1.2/meta/calm.json",`, "1.2"},
		{"1.3", `"$schema":"https://calm.finos.org/release/1.3/meta/calm.json",`, "1.3"},
		{"no $schema", "", "1.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeCalmDocument(t, tc.schema, `{"unique-id":"a","node-type":"service","name":"a","description":"d"}`)

			r := intent.LoadArchitecture(path, intent.LoadContext{Root: filepath.Dir(path)})
			if !acceptedAs(&r, tc.want) {
				t.Fatalf("model = %+v, diagnostics = %+v", r.Model, r.Diagnostics)
			}

			// Without node-type the CALM core rules of the release reject the node.
			path = writeCalmDocument(t, tc.schema, `{"unique-id":"a","name":"a","description":"d"}`)

			r = intent.LoadArchitecture(path, intent.LoadContext{Root: filepath.Dir(path)})
			if findCode(&r, "CO-INT-002") == nil {
				t.Fatalf("diagnostics = %+v, want CO-INT-002", r.Diagnostics)
			}
		})
	}
}

// loadWithBindings loads valid.calm.json with the bindings of its billing node replaced.
func loadWithBindings(t *testing.T, bindings []map[string]any) intent.LoadResult {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtures, "valid.calm.json"))
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	nodes, _ := doc["nodes"].([]any)
	node, _ := nodes[1].(map[string]any)
	ext, _ := node["concordia"].(map[string]any)
	ext["bindings"] = bindings

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs(fixtures)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "bindings.calm.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	return intent.LoadArchitecture(path, intent.LoadContext{Root: root})
}

func TestExtensionAcceptsDeployedResourceKinds(t *testing.T) {
	t.Parallel()

	r := loadWithBindings(t, []map[string]any{
		{"kind": "workload", "environment": "production", "api-group": "apps", "resource-kind": "Deployment", "namespace": "payments", "name": "billing", "cardinality": "one", "authority": "me"},
		{"kind": "image", "repository": "ghcr.io/acme/billing", "digest": "sha256:abc"},
		{"kind": "route", "environment": "production", "name": "billing-ing"},
		{"kind": "infra-resource", "environment": "production", "address": "kubernetes_namespace.payments"},
		{"kind": "compose-service", "file": "compose.yaml", "service": "billing", "status": "proposed"},
		{"kind": "otel-service", "value": "billing"},
	})

	if findCode(&r, diag.INT007) != nil {
		t.Fatalf("diagnostics: %+v", r.Diagnostics)
	}

	got := r.Model.Nodes[1].Bindings
	if len(got) != 6 {
		t.Fatalf("bindings = %+v", got)
	}

	for _, field := range []struct{ name, got, want string }{
		{"APIGroup", got[0].APIGroup, "apps"},
		{"ResourceKind", got[0].ResourceKind, "Deployment"},
		{"Name", got[0].Name, "billing"},
		{"Cardinality", got[0].Cardinality, "one"},
		{"Authority", got[0].Authority, "me"},
		{"Digest", got[1].Digest, "sha256:abc"},
		{"Address", got[3].Address, "kubernetes_namespace.payments"},
		{"Service", got[4].Service, "billing"},
		{"Status", got[4].Status, "proposed"},
	} {
		if field.got != field.want {
			t.Errorf("bindings = %+v (%s = %q, want %q)", got, field.name, field.got, field.want)
		}
	}
}

func TestExtensionRejectsBadDeployedResourceBindings(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]any{
		"missing name":      {"kind": "workload", "environment": "production", "resource-kind": "Deployment"},
		"unknown key":       {"kind": "image", "repository": "r", "bogus": "x"},
		"camel case kind":   {"kind": "infraResource", "environment": "production", "address": "a"},
		"unknown kind":      {"kind": "nope", "value": "x"},
		"foreign field":     {"kind": "compose-service", "file": "f", "service": "s", "name": "n"},
		"bad cardinality":   {"kind": "image", "repository": "r", "cardinality": "two"},
		"value on workload": {"kind": "workload", "environment": "p", "resource-kind": "Deployment", "name": "n", "value": "v"},
	}

	for name, item := range cases {
		r := loadWithBindings(t, []map[string]any{item})
		if len(errorsOf(&r)) == 0 {
			t.Errorf("%s: want an error, got %+v", name, r.Diagnostics)
		}
	}
}

// controlDocument is a one-node architecture whose control carries the given Concordia configuration, under the
// given CALM release.
func controlDocument(t *testing.T, release string, config map[string]any) intent.LoadResult {
	t.Helper()

	doc := map[string]any{
		"$schema": "https://calm.finos.org/release/" + release + "/meta/calm.json",
		"nodes": []any{map[string]any{
			"unique-id": "billing-service", "node-type": "service", "name": "Billing", "description": "Billing",
			"controls": map[string]any{"architecture-001": map[string]any{
				"description": "Boundaries",
				"requirements": []any{map[string]any{
					"requirement-url": "https://calm.finos.org/release/" + release + "/meta/control.json",
					"config":          config,
				}},
			}},
		}},
		"relationships": []any{},
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "arch.calm.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	return intent.LoadArchitecture(filepath.Join(root, "arch.calm.json"), intent.LoadContext{Root: root})
}

// configMatches tells whether a control configuration is present with the given rule IDs and check IDs.
func configMatches(cfg *intent.ControlConfig, ruleIDs, checkIDs []string) bool {
	if cfg == nil {
		return false
	}

	return slices.Equal(cfg.RuleIDs, ruleIDs) && slices.Equal(cfg.CheckIDs, checkIDs)
}

func TestControlConfigAcceptsCheckIDs(t *testing.T) {
	t.Parallel()

	for _, release := range []string{"1.2", "1.3"} {
		for _, tc := range []struct {
			name      string
			config    map[string]any
			wantRules []string
			wantCheck []string
		}{
			{"checks only", map[string]any{"engine": "concordia", "check-ids": []any{"unit-tests"}}, nil, []string{"unit-tests"}},
			{"rules only", map[string]any{"engine": "concordia", "rule-ids": []any{"CO-BND-001"}}, []string{"CO-BND-001"}, nil},
			{
				"both", map[string]any{"engine": "concordia", "rule-ids": []any{"CO-BND-001"}, "check-ids": []any{"unit-tests", "sast"}},
				[]string{"CO-BND-001"}, []string{"unit-tests", "sast"},
			},
		} {
			t.Run(release+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				r := controlDocument(t, release, tc.config)
				if errs := errorsOf(&r); len(errs) != 0 {
					t.Fatalf("errors: %+v", errs)
				}

				cfg := r.Model.Controls[0].Config
				if !configMatches(cfg, tc.wantRules, tc.wantCheck) {
					t.Fatalf("config = %+v", cfg)
				}
			})
		}
	}
}

func TestControlConfigWithNeitherListExitsWithCode2(t *testing.T) {
	t.Parallel()

	for _, release := range []string{"1.2", "1.3"} {
		t.Run(release, func(t *testing.T) {
			t.Parallel()

			r := controlDocument(t, release, map[string]any{"engine": "concordia", "unknown-disposition": "warn"})
			if !slices.Contains(codes(&r), diag.INT007) {
				t.Fatalf("codes = %v", codes(&r))
			}

			if got := intent.ExitCodeForDiagnostics(r.Diagnostics); got != diag.InvalidInput {
				t.Fatalf("exit code = %d", got)
			}

			if r.Model.Controls[0].Config != nil {
				t.Errorf("config = %+v, want none for an invalid configuration", r.Model.Controls[0].Config)
			}
		})
	}
}
