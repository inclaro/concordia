package projection_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/projection"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

type nodeOpt func(*intent.NodeDeclaration)

func node(id, nodeType string, opts ...nodeOpt) intent.NodeDeclaration {
	n := intent.NodeDeclaration{
		ID: id, NodeType: nodeType, Name: strings.ToUpper(id), Tags: []string{},
		Bindings: []intent.ExtBinding{}, Pointer: "/nodes/" + id,
	}
	for _, opt := range opts {
		opt(&n)
	}

	return n
}

func model(nodes []intent.NodeDeclaration, rels ...intent.RelationshipDeclaration) *intent.ArchitectureModel {
	if rels == nil {
		rels = []intent.RelationshipDeclaration{}
	}

	return &intent.ArchitectureModel{
		SourceURI: "arch.json", Digest: "sha256:x", SchemaVersion: "1.2", Nodes: nodes, Relationships: rels,
		Controls: []intent.ControlAttachment{}, Metadata: map[string]any{},
	}
}

func rel(id string, kind intent.RelationshipKind, mutate func(*intent.RelationshipDeclaration)) intent.RelationshipDeclaration {
	r := intent.RelationshipDeclaration{ID: id, Kind: kind, Targets: []string{}, Pointer: "/relationships/" + id}
	mutate(&r)

	return r
}

func codes(diagnostics []diag.Diagnostic) []string {
	out := make([]string, len(diagnostics))
	for i := range diagnostics {
		out[i] = diagnostics[i].Code
	}

	return out
}

type projectionTestDiagCheck func(err error, de *diag.Error) bool

func projectionTestExitIs(exit int) projectionTestDiagCheck {
	return func(_ error, de *diag.Error) bool { return de.ExitCode == exit }
}

func projectionTestMessageHas(text string) projectionTestDiagCheck {
	return func(_ error, de *diag.Error) bool { return strings.Contains(de.Diagnostic.Message, text) }
}

func projectionTestErrorTextHas(text string) projectionTestDiagCheck {
	return func(err error, _ *diag.Error) bool { return strings.Contains(err.Error(), text) }
}

func projectionTestDiagErrorMatches(err error, code string, checks ...projectionTestDiagCheck) bool {
	var de *diag.Error
	if !errors.As(err, &de) || de.Diagnostic.Code != code {
		return false
	}

	for _, check := range checks {
		if !check(err, de) {
			return false
		}
	}

	return true
}

func projectionTestExpectDiagError(t *testing.T, err error, code string, checks ...projectionTestDiagCheck) {
	t.Helper()

	if !projectionTestDiagErrorMatches(err, code, checks...) {
		t.Errorf("err = %v", err)
	}
}

func projectionTestNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func projectionTestDSL(t *testing.T, m *intent.ArchitectureModel, run *projection.RunView, opts *projection.DSLOptions) string {
	t.Helper()

	dsl, err := projection.ToStructurizrDSL(m, run, *opts)
	projectionTestNoError(t, err)

	return dsl
}

func projectionTestExpectEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func projectionTestExpectContains(t *testing.T, dsl string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(dsl, want) {
			t.Errorf("DSL lacks %s", want)
		}
	}
}

func projectionTestExpectContainsWithDSL(t *testing.T, dsl string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(dsl, want) {
			t.Errorf("DSL lacks %s:\n%s", want, dsl)
		}
	}
}

func projectionTestExpectAbsent(t *testing.T, dsl string, absents ...string) {
	t.Helper()

	for _, absent := range absents {
		if strings.Contains(dsl, absent) {
			t.Errorf("DSL must not contain %s", absent)
		}
	}
}

func projectionTestElementsByID(c4 projection.C4Map) (by map[string]projection.C4Element, ids []string) {
	by = map[string]projection.C4Element{}
	ids = make([]string, len(c4.Elements))

	for i, e := range c4.Elements {
		by[e.ID] = e
		ids[i] = e.ID
	}

	return by, ids
}

func projectionTestFirstDiagCodeIs(c4 projection.C4Map, code string) bool {
	return len(c4.Diagnostics) > 0 && c4.Diagnostics[0].Code == code
}

func projectionTestC4MapsDefaultLevels(t *testing.T) {
	t.Parallel()

	m := model(
		[]intent.NodeDeclaration{node("a", "actor"), node("sys", "system"), node("svc", "service"), node("orphan", "database"), node("net", "network")},
		rel("c1", intent.RelComposedOf, func(r *intent.RelationshipDeclaration) {
			r.Container = "sys"
			r.Targets = []string{"svc"}
		}),
	)
	c4 := projection.MapC4(m, nil, "Shop")
	by, ids := projectionTestElementsByID(c4)

	projectionTestExpectEqual(t, "a level", by["a"].Level, intent.C4Person)
	projectionTestExpectEqual(t, "svc parent", by["svc"].Parent, "sys")
	projectionTestExpectEqual(t, "orphan parent", by["orphan"].Parent, projection.SyntheticSystemID)
	projectionTestExpectEqual(t, "synthetic name", by[projection.SyntheticSystemID].Name, "Shop")

	if _, ok := by["net"]; ok {
		t.Error("net must not be projected")
	}

	if got := codes(c4.Diagnostics); !slices.Equal(got, []string{"CO-PRJ-006"}) {
		t.Errorf("diagnostic codes = %v", got)
	}

	if !slices.IsSorted(ids) {
		t.Errorf("elements not sorted: %v", ids)
	}
}

func projectionTestC4ConflictingOverrides(t *testing.T) {
	t.Parallel()

	b := binding.Binding{ID: "b", ArchitectureRef: "x", Exclusive: true, Required: true, Projection: &binding.Projection{C4Level: "component"}, Source: "f"}
	c4 := projection.MapC4(model([]intent.NodeDeclaration{node("x", "service", func(n *intent.NodeDeclaration) { n.C4Level = intent.C4Container })}),
		[]binding.Binding{b}, "W")

	if !projectionTestFirstDiagCodeIs(c4, "CO-PRJ-005") {
		t.Errorf("diagnostics = %v", c4.Diagnostics)
	}
}

func projectionTestC4UnknownOverride(t *testing.T) {
	t.Parallel()

	c4 := projection.MapC4(model([]intent.NodeDeclaration{node("x", "service", func(n *intent.NodeDeclaration) { n.C4Level = "bogus" })}), nil, "W")
	if !projectionTestFirstDiagCodeIs(c4, "CO-PRJ-005") || len(c4.Elements) != 0 {
		t.Errorf("diagnostics = %v, elements = %v", c4.Diagnostics, c4.Elements)
	}
}

func projectionTestC4ComponentWithoutContainer(t *testing.T) {
	t.Parallel()

	c4 := projection.MapC4(model([]intent.NodeDeclaration{node("x", "service", func(n *intent.NodeDeclaration) { n.C4Level = intent.C4Component })}), nil, "W")
	if !projectionTestFirstDiagCodeIs(c4, "CO-PRJ-005") {
		t.Errorf("diagnostics = %v", c4.Diagnostics)
	}
}

func TestC4Map(t *testing.T) {
	t.Parallel()

	t.Run("maps default levels, nests under composed-of, and uses a synthetic system", projectionTestC4MapsDefaultLevels)
	t.Run("conflicting overrides give CO-PRJ-005", projectionTestC4ConflictingOverrides)
	t.Run("an unknown override level gives CO-PRJ-005", projectionTestC4UnknownOverride)
	t.Run("a component without a container parent gives CO-PRJ-005", projectionTestC4ComponentWithoutContainer)
}

func projectionTestIdentifierCollisions(t *testing.T) {
	t.Parallel()

	ids := projection.MakeIdentifiers([]string{"a-b", "a.b", "a_b"})
	want := map[string]string{"a-b": "gu_a_b", "a.b": "gu_a_b_2", "a_b": "gu_a_b_3"}

	for id, w := range want {
		if ids[id] != w {
			t.Errorf("ident[%q] = %q, want %q", id, ids[id], w)
		}
	}
}

func projectionTestEscapesQuotes(t *testing.T) {
	t.Parallel()

	if got, want := projection.DSLString("a\"b\\c\nd"), `"a\"b\\c d"`; got != want {
		t.Errorf("DSLString = %s, want %s", got, want)
	}

	n := node("x", "service", func(n *intent.NodeDeclaration) { n.Description = `say "hi" } evil` })
	dsl := projectionTestDSL(t, model([]intent.NodeDeclaration{n}), nil, &projection.DSLOptions{})

	if !strings.Contains(dsl, `"say \"hi\" } evil"`) {
		t.Errorf("DSL does not escape the description:\n%s", dsl)
	}
}

func TestDSLIdentifiersAndEscaping(t *testing.T) {
	t.Parallel()

	t.Run("sanitizes identifiers and resolves collisions", projectionTestIdentifierCollisions)
	t.Run("escapes quotes and backslashes", projectionTestEscapesQuotes)
}

func dslModel() *intent.ArchitectureModel {
	return model(
		[]intent.NodeDeclaration{
			node("cust", "actor"), node("sys", "system"),
			node("a", "service", func(n *intent.NodeDeclaration) { n.Owner = "team-a" }),
			node("b", "service"), node("c", "database"),
		},
		rel("r0", intent.RelComposedOf, func(r *intent.RelationshipDeclaration) {
			r.Container = "sys"
			r.Targets = []string{"a", "b"}
		}),
		rel("r1", intent.RelConnects, func(r *intent.RelationshipDeclaration) {
			r.Source = "a"
			r.Targets = []string{"b"}
			r.Protocol = "HTTP"
			r.Description = "calls"
		}),
		rel("r2", intent.RelInteracts, func(r *intent.RelationshipDeclaration) {
			r.Source = "cust"
			r.Targets = []string{"a"}
		}),
	)
}

func projectionTestDSLCalmIDsAndSynthetic(t *testing.T) {
	t.Parallel()

	dsl := projectionTestDSL(t, dslModel(), nil, &projection.DSLOptions{WorkspaceName: "Shop"})

	for _, id := range []string{"cust", "sys", "a", "b", "c"} {
		if !strings.Contains(dsl, `"calm.unique-id" "`+id+`"`) {
			t.Errorf("missing calm.unique-id %q", id)
		}
	}

	projectionTestExpectContains(t, dsl,
		`"concordia.synthetic" "true"`, "gu_system_default = softwareSystem", `"concordia.owner" "team-a"`, `"concordia.status" "none"`,
		`workspace "Shop" "Generated by Concordia from CALM architecture arch.json" {`,
	)

	if !regexp.MustCompile(`gu_a -> gu_b "\[declared-only\] calls" "HTTP" "concordia:declared-only"`).MatchString(dsl) {
		t.Errorf("DSL lacks the declared-only relationship:\n%s", dsl)
	}
}

func projectionTestDSLRunView() *projection.RunView {
	return &projection.RunView{
		Findings: []conformance.Finding{{
			Fingerprint: "sha256:aa", Disposition: exceptions.DispositionActive,
			Source: conformance.Endpoint{ArchitectureID: "a"}, Target: conformance.Endpoint{ArchitectureID: "b"},
		}},
		Reconciliation: []conformance.ReconciliationEntry{{
			From: "b", To: "a", Classification: conformance.ClassUndeclared, ObservedKinds: []observation.CanonicalEdgeKind{observation.KindImports}, EdgeCount: 1,
		}},
		Rules: []conformance.RuleResult{
			{Subjects: []string{"a"}, Verdict: conformance.VerdictViolation},
			{Subjects: []string{"a", "b"}, Verdict: conformance.VerdictPass},
		},
	}
}

func projectionTestDSLRunStatus(t *testing.T) {
	t.Parallel()

	run := projectionTestDSLRunView()
	dsl := projectionTestDSL(t, dslModel(), run, &projection.DSLOptions{})

	projectionTestExpectContains(t, dsl,
		`gu_a -> gu_b "[violating] calls" "HTTP" "concordia:violating"`,
		`"concordia.findings" "sha256:aa"`,
		`gu_b -> gu_a "[observed-only] observed only" "" "concordia:observed-only"`,
		`"concordia.observed-kinds" "imports"`,
		`"concordia.status" "violation"`,
		`"concordia.status" "pass"`,
		`systemContext gu_sys "context-gu_sys"`,
		`"drift-gu_sys"`,
	)

	again, err := projection.ToStructurizrDSL(dslModel(), run, projection.DSLOptions{})
	if err != nil || again != dsl {
		t.Errorf("DSL is not deterministic (err %v)", err)
	}
}

func projectionTestDSLViewAndFocus(t *testing.T) {
	t.Parallel()

	dsl := projectionTestDSL(t, dslModel(), nil, &projection.DSLOptions{Views: map[projection.ViewKind]bool{projection.ViewDrift: true}, Focus: "a"})

	projectionTestExpectAbsent(t, dsl, "systemLandscape", "systemContext", `"drift-gu_system_default"`)

	if !strings.Contains(dsl, `"drift-gu_sys"`) {
		t.Error(`DSL lacks "drift-gu_sys"`)
	}

	_, err := projection.ToStructurizrDSL(dslModel(), nil, projection.DSLOptions{Focus: "zzz"})
	projectionTestExpectDiagError(t, err, "CO-WRK-010", projectionTestErrorTextHas("focus"))
}

func projectionTestDSLInvalidMapping(t *testing.T) {
	t.Parallel()

	bad := model([]intent.NodeDeclaration{node("x", "service", func(n *intent.NodeDeclaration) { n.C4Level = intent.C4Component })})

	_, err := projection.ToStructurizrDSL(bad, nil, projection.DSLOptions{})
	projectionTestExpectDiagError(t, err, "CO-PRJ-005", projectionTestExitIs(diag.InvalidInput))

	var collected []diag.Diagnostic

	net := model([]intent.NodeDeclaration{node("n", "network"), node("s", "system")})
	projectionTestDSL(t, net, nil, &projection.DSLOptions{Diagnostics: &collected})

	if got := codes(collected); !slices.Equal(got, []string{"CO-PRJ-006"}) {
		t.Errorf("collected = %v", got)
	}
}

func TestToStructurizrDSL(t *testing.T) {
	t.Parallel()

	t.Run("every element has calm.unique-id; containers without parent go to the synthetic system", projectionTestDSLCalmIDsAndSynthetic)
	t.Run("uses run status for relationships, elements, and observed-only pairs", projectionTestDSLRunStatus)
	t.Run("--view and --focus restrict views; unknown focus gives CO-WRK-010", projectionTestDSLViewAndFocus)
	t.Run("an invalid C4 mapping gives CO-PRJ-005 with exit code 2; other diagnostics are collected", projectionTestDSLInvalidMapping)
}

var sha256Value = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

func projectionTestLoadCommerce(t *testing.T) (*workspace.Workspace, *intent.ArchitectureModel, []binding.Binding) {
	t.Helper()

	ws, err := workspace.Load(filepath.Join("..", "..", "docs", "examples", "commerce", "concordia.yaml"), "")
	projectionTestNoError(t, err)

	mapping, err := intent.LoadURLMapping(ws)
	projectionTestNoError(t, err)

	patterns := make([]string, len(ws.Architecture.Patterns))
	for i, p := range ws.Architecture.Patterns {
		patterns[i] = ws.ResolveInRoot(p)
	}

	loaded := intent.LoadArchitecture(ws.ResolveInRoot(ws.Architecture.Source), intent.LoadContext{Root: ws.Root, URLMapping: mapping, Patterns: patterns})
	if loaded.Model == nil {
		t.Fatalf("model not loaded: %v", loaded.Diagnostics)
	}

	bindings, err := binding.Load(ws, loaded.Model)
	projectionTestNoError(t, err)

	return ws, loaded.Model, bindings.Bindings
}

// TestCommerceGolden makes the DSL of the commerce example from the recorded TypeScript run
// and compares it with the recorded TypeScript DSL (except the sha256 values: the rule IDs changed).
func TestCommerceGolden(t *testing.T) {
	t.Parallel()

	ws, model, bindings := projectionTestLoadCommerce(t)
	golden := filepath.Join("..", "..", "test", "golden", "commerce", "check", "files")

	var run projection.RunView
	projectionTestNoError(t, json.Unmarshal([]byte(testutil.ReadFile(t, filepath.Join(golden, "02-build_concordia_results.json"))), &run))

	got := projectionTestDSL(t, model, &run, &projection.DSLOptions{Bindings: bindings, WorkspaceName: ws.Metadata.Name})
	want := testutil.ReadFile(t, filepath.Join(golden, "07-build_concordia_structurizr_workspace.dsl"))
	got = sha256Value.ReplaceAllString(got, "sha256:X")
	want = sha256Value.ReplaceAllString(want, "sha256:X")

	if got != want {
		t.Errorf("DSL differs from the golden file.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()

	p := filepath.Join(dir, name)
	projectionTestNoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	return p
}

// projectionTestExpectCLI checks FindCLI; an empty want means no CLI may be found.
func projectionTestExpectCLI(t *testing.T, values map[string]string, want string) {
	t.Helper()

	p, ok := projection.FindCLI(env(values))
	if want == "" {
		if ok {
			t.Errorf("found %q", p)
		}

		return
	}

	if !ok || p != want {
		t.Errorf("found %q (%v), want %q", p, ok, want)
	}
}

func projectionTestFindsNothingOnEmptyPath(t *testing.T) {
	t.Parallel()

	projectionTestExpectCLI(t, map[string]string{"PATH": ""}, "")
}

func projectionTestFindsByNameOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeExecutable(t, dir, "structurizr")
	second := writeExecutable(t, dir, "structurizr.sh")

	projectionTestExpectCLI(t, map[string]string{"PATH": dir}, second)
}

func projectionTestFindsExplicitCLI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	onPath := writeExecutable(t, dir, "structurizr-cli")
	explicit := writeExecutable(t, t.TempDir(), "my-structurizr")

	projectionTestExpectCLI(t, map[string]string{"PATH": dir, "STRUCTURIZR_CLI": explicit}, explicit)
	projectionTestExpectCLI(t, map[string]string{"PATH": dir, "STRUCTURIZR_CLI": filepath.Join(dir, "missing")}, onPath)
	projectionTestExpectCLI(t, map[string]string{"PATH": dir, "STRUCTURIZR_CLI": "structurizr-cli"}, onPath)
}

func projectionTestIgnoresNonExecutable(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows has no execute permission bit")
	}

	dir := t.TempDir()
	projectionTestNoError(t, os.WriteFile(filepath.Join(dir, "structurizr"), []byte("x"), 0o644))

	projectionTestExpectCLI(t, map[string]string{"PATH": dir}, "")
}

func TestFindCLI(t *testing.T) {
	t.Parallel()

	t.Run("finds nothing on an empty PATH", projectionTestFindsNothingOnEmptyPath)
	t.Run("finds the CLI by name on the PATH, in name order", projectionTestFindsByNameOrder)
	t.Run("STRUCTURIZR_CLI wins, and a bad value falls back to the PATH", projectionTestFindsExplicitCLI)
	t.Run("a file without execute permission is not found", projectionTestIgnoresNonExecutable)
}

func TestRequireCLI(t *testing.T) {
	t.Parallel()

	_, err := projection.RequireCLI("mermaid", env(map[string]string{"PATH": ""}))

	projectionTestExpectDiagError(t, err, "CO-PRJ-001", projectionTestExitIs(diag.Toolchain), projectionTestMessageHas("mermaid"))
}

type projectionTestFakeCLIs struct {
	dir      string
	argsFile string
	ok       string
	fail     string
}

func projectionTestWriteFakeCLIs(t *testing.T) projectionTestFakeCLIs {
	t.Helper()

	dir := t.TempDir()
	fakes := projectionTestFakeCLIs{
		dir: dir, argsFile: filepath.Join(dir, "args.txt"), ok: filepath.Join(dir, "ok.sh"), fail: filepath.Join(dir, "fail.sh"),
	}

	testutil.WriteTree(t, dir, map[string]string{
		"ok.sh":   "#!/bin/sh\necho \"$@\" > " + fakes.argsFile + "\n",
		"fail.sh": "#!/bin/sh\necho 'bad workspace' >&2\nexit 3\n",
	})

	for _, p := range []string{fakes.ok, fakes.fail} {
		projectionTestNoError(t, os.Chmod(p, 0o755))
	}

	return fakes
}

func projectionTestExpectArgs(t *testing.T, argsFile, want string) {
	t.Helper()

	if got := strings.TrimSpace(testutil.ReadFile(t, argsFile)); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestValidateAndExport(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}

	fakes := projectionTestWriteFakeCLIs(t)
	ctx := context.Background()

	projectionTestNoError(t, projection.Export(ctx, fakes.ok, "w.dsl", projection.FormatMermaid, "out"))
	projectionTestExpectArgs(t, fakes.argsFile, "export -workspace w.dsl -format mermaid -output out")

	projectionTestNoError(t, projection.Validate(ctx, fakes.ok, "w.dsl"))
	projectionTestExpectArgs(t, fakes.argsFile, "validate -workspace w.dsl")

	err := projection.Validate(ctx, fakes.fail, "w.dsl")
	projectionTestExpectDiagError(t, err, "CO-PRJ-002", projectionTestExitIs(diag.AdapterFailure), projectionTestMessageHas("bad workspace"))

	err = projection.Validate(ctx, filepath.Join(fakes.dir, "missing"), "w.dsl")
	projectionTestExpectDiagError(t, err, "CO-PRJ-002")
}

func deploymentModel() *intent.ArchitectureModel {
	return model(
		[]intent.NodeDeclaration{
			node("sys", "system"), node("a", "service"), node("b", "service"),
			node("net", "network"), node("host", "service"),
		},
		rel("r0", intent.RelComposedOf, func(r *intent.RelationshipDeclaration) {
			r.Container = "sys"
			r.Targets = []string{"a"}
		}),
		rel("d1", intent.RelDeployedIn, func(r *intent.RelationshipDeclaration) {
			r.Container = "net"
			r.Targets = []string{"host"}
		}),
		rel("d2", intent.RelDeployedIn, func(r *intent.RelationshipDeclaration) {
			r.Container = "host"
			r.Targets = []string{"a", "sys"}
		}),
	)
}

func TestDeploymentView(t *testing.T) {
	t.Parallel()

	var diagnostics []diag.Diagnostic

	dsl := projectionTestDSL(t, deploymentModel(), nil, &projection.DSLOptions{Diagnostics: &diagnostics})

	projectionTestExpectContainsWithDSL(t, dsl,
		`deploymentEnvironment "Default" {`, `deploymentNode "NET"`, `deploymentNode "HOST"`, "containerInstance gu_a",
		"softwareSystemInstance gu_sys", `tags "concordia:deployment"`, `deployment * "Default" "deployment" {`,
	)

	for _, d := range diagnostics {
		if d.Code == "CO-PRJ-006" {
			t.Errorf("a deployed network node gives %s", d.Code)
		}
	}
}

func TestDeploymentViewRequestedWithoutDeploymentNodesGivesPRJ008(t *testing.T) {
	t.Parallel()

	var diagnostics []diag.Diagnostic

	dsl := projectionTestDSL(t, dslModel(), nil, &projection.DSLOptions{
		Diagnostics: &diagnostics, Views: map[projection.ViewKind]bool{projection.ViewDeployment: true},
	})

	if strings.Contains(dsl, "deployment *") || !slices.Contains(codes(diagnostics), "CO-PRJ-008") {
		t.Errorf("diagnostics = %v, DSL:\n%s", codes(diagnostics), dsl)
	}
}

func TestDynamicViewAddsEvidenceRelationshipsThatOtherViewsExclude(t *testing.T) {
	t.Parallel()

	path := &projection.DynamicPath{
		Key: "evidence-aabbccddeeff", Title: "R1: a uses c",
		Steps: []projection.DynamicStep{{FromID: "a", ToID: "b", Label: "IMPORTS"}, {FromID: "b", ToID: "c", Label: "CALLS"}},
	}

	dsl := projectionTestDSL(t, dslModel(), nil, &projection.DSLOptions{Dynamic: path})

	projectionTestExpectContainsWithDSL(t, dsl,
		`dynamic * "evidence-aabbccddeeff" "R1: a uses c" {`, `gu_a -> gu_b "IMPORTS"`,
		`gu_b -> gu_c "CALLS"`, `"concordia:evidence"`,
	)

	// a -> b is declared (r1), so only b -> c gets an evidence relationship.
	if strings.Contains(dsl, `gu_a -> gu_b "evidence"`) || !strings.Contains(dsl, `gu_b -> gu_c "evidence"`) {
		t.Errorf("evidence relationships are wrong:\n%s", dsl)
	}

	if !strings.Contains(dsl, `container gu_sys "container-gu_sys" {`+"\n      include *\n      exclude \"relationship.tag==concordia:observed-only\" \"relationship.tag==concordia:evidence\"") {
		t.Errorf("the container view does not exclude the evidence relationship:\n%s", dsl)
	}
}
