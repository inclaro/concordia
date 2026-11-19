package views_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

func paymentsModel() *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		SourceURI: "architecture/payments.calm.json",
		Nodes: []intent.NodeDeclaration{
			{ID: "customer", NodeType: "actor"},
			{ID: "payments-system", NodeType: "system"},
			{ID: "orders-service", NodeType: "service"},
			{ID: "payments-db", NodeType: "database"},
		},
		Relationships: []intent.RelationshipDeclaration{
			{ID: "payments-composition", Kind: intent.RelComposedOf, Container: "payments-system", Targets: []string{"orders-service", "payments-db"}},
			{ID: "orders-to-db", Kind: intent.RelConnects, Source: "orders-service", Targets: []string{"payments-db"}},
		},
	}
}

func workspaceWith(defs ...views.Definition) *workspace.Workspace {
	return &workspace.Workspace{
		Root: "/ws", Path: "/ws/concordia.yaml",
		Environments: []workspace.Environment{{ID: "staging", Sources: []string{"deploy/staging/**"}}, {ID: "prod", Sources: []string{"deploy/prod/**"}}},
		Projection:   workspace.Projection{Output: "build/concordia/structurizr"},
		Views:        defs,
	}
}

// problems runs ValidateAgainst and gives the messages of the diagnostics by code.
func problems(t *testing.T, wsp *workspace.Workspace, model *intent.ArchitectureModel, git workspace.GitPort) []diag.Diagnostic {
	t.Helper()

	got, err := views.ValidateAgainst(t.Context(), wsp, model, git)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestValidateAgainstAcceptsAValidWorkspace(t *testing.T) {
	t.Parallel()

	container := generatedView("payments-container")
	container.Environment = "staging"
	container.Coverage = &views.Coverage{Elements: []string{"orders-service"}, Relationships: []string{"orders-to-db"}}
	container.Projection = &views.ProjectionRules{EdgeKinds: []string{"calls", "imports"}}
	container.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/architecture/views/payments-container.mmd"}}

	context := authoredView("payments-context")
	context.Mapping = &views.Mapping{Elements: map[string]string{"orders-api": "orders-service"}}

	if got := problems(t, workspaceWith(container, context), paymentsModel(), &workspace.FakeGit{}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// validateScenario is one case of ValidateAgainst: a change of a valid view and the one diagnostic it causes.
type validateScenario struct {
	name   string
	code   string
	want   string
	edit   func(v *views.Definition)
	ignore string
}

// referenceScenarios are the views that name an environment, element, node, edge kind or token that is not declared.
func referenceScenarios() []validateScenario {
	return []validateScenario{
		{
			name: "Unknown environment", code: diag.DGM001, want: "environment 'prod-eu' is not declared (declared environments: staging, prod)",
			edit: func(v *views.Definition) { v.Environment = "prod-eu" },
		},
		{
			name: "Coverage names an unknown element", code: diag.DGM001, want: "coverage.elements names 'billing-service'",
			edit: func(v *views.Definition) { v.Coverage = &views.Coverage{Elements: []string{"billing-service"}} },
		},
		{
			name: "Coverage names an unknown relationship", code: diag.DGM001, want: "coverage.relationships names 'nope'",
			edit: func(v *views.Definition) { v.Coverage = &views.Coverage{Relationships: []string{"nope"}} },
		},
		{
			name: "Scope names an unknown node", code: diag.DGM001, want: "scope names 'ghost'",
			edit: func(v *views.Definition) { v.Scope = []string{"ghost"} },
		},
		{
			name: "Mapping to an unknown CALM ID", code: diag.DGM001, want: "mapping.elements key 'orders-api' maps to 'ghost'",
			edit: func(v *views.Definition) {
				v.Mapping = &views.Mapping{Elements: map[string]string{"orders-api": "ghost"}}
			},
		},
		{
			name: "Unknown edge kind", code: diag.DGM001, want: "projection.edgeKinds names the kind 'teleports'",
			edit: func(v *views.Definition) {
				v.Projection = &views.ProjectionRules{EdgeKinds: []string{"calls", "teleports"}}
			},
		},
		{
			name: "Unknown semantic token", code: diag.DGM001, want: "projection.semantic names 'label'",
			edit: func(v *views.Definition) {
				v.Projection = &views.ProjectionRules{Semantic: []string{"name", "label"}}
			},
		},
	}
}

// ignoredPathScenarios are the views that write a path that Git ignores.
func ignoredPathScenarios() []validateScenario {
	return []validateScenario{
		{
			name: "Output path is ignored by Git", code: diag.DGM002, want: "ignored by Git", ignore: "build/concordia/views/container.dsl",
			edit: func(v *views.Definition) { v.Output.Path = "build/concordia/views/container.dsl" },
		},
		{
			name: "Export path is ignored by Git", code: diag.DGM002, want: "ignored by Git", ignore: "out/v.mmd",
			edit: func(v *views.Definition) { v.Output.Exports = []views.Export{{Format: "mermaid", Path: "out/v.mmd"}} },
		},
		{
			name: "Manifest path is ignored by Git", code: diag.DGM002, want: "ignored by Git", ignore: "docs/v.dsl.manifest.json",
			edit: func(v *views.Definition) { v.Output.Path = "docs/v.dsl" },
		},
	}
}

// outputPathScenarios are the views that write a path outside the workspace, under projection.output, or not canonical.
func outputPathScenarios() []validateScenario {
	return []validateScenario{
		{
			name: "Output path outside the workspace", code: diag.DGM002, want: "outside the workspace",
			edit: func(v *views.Definition) { v.Output.Path = "../shared/v.dsl" },
		},
		{
			name: "Absolute output path", code: diag.DGM002, want: "outside the workspace",
			edit: func(v *views.Definition) { v.Output.Path = "/etc/v.dsl" },
		},
		{
			name: "Output path under projection.output", code: diag.DGM002, want: "under projection.output",
			edit: func(v *views.Definition) { v.Output.Path = "build/concordia/structurizr/v.dsl" },
		},
		{
			name: "Output path under projection.output spelled with dot segments", code: diag.DGM002, want: "under projection.output",
			edit: func(v *views.Definition) { v.Output.Path = "docs/../build/concordia/structurizr/./v.dsl" },
		},
		{
			name: "Output path with a dot segment", code: diag.DGM002, want: "not in canonical form, use docs/v.dsl",
			edit: func(v *views.Definition) { v.Output.Path = "docs/./v.dsl" },
		},
		{
			name: "Export path with a doubled slash", code: diag.DGM002, want: "not in canonical form, use docs/v.mmd",
			edit: func(v *views.Definition) { v.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs//v.mmd"}} },
		},
	}
}

// requireOneProblem checks that the diagnostics are the one error of the code that names the view and has the message.
func requireOneProblem(t *testing.T, got []diag.Diagnostic, code, want string) {
	t.Helper()

	if len(got) != 1 {
		t.Fatalf("want one diagnostic, got %v", got)
	}

	if !isProblemOf(&got[0], code, want) {
		t.Errorf("got %s: %s, want %s with %q", got[0].Code, got[0].Message, code, want)
	}

	if exit, _ := diag.ExitCodeOf(got[0].Code); exit != diag.InvalidInput {
		t.Errorf("exit code %d", exit)
	}
}

func isProblemOf(d *diag.Diagnostic, code, want string) bool {
	return d.Code == code && strings.Contains(d.Message, want) && strings.Contains(d.Message, "v1")
}

func TestValidateAgainstScenarios(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(referenceScenarios(), ignoredPathScenarios(), outputPathScenarios())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := generatedView("v1")
			if strings.Contains(tc.name, "Mapping") {
				v = authoredView("v1")
			}

			tc.edit(&v)

			git := &workspace.FakeGit{Ignored: map[string]bool{tc.ignore: tc.ignore != ""}}
			got := problems(t, workspaceWith(v), paymentsModel(), git)

			requireOneProblem(t, got, tc.code, tc.want)
		})
	}
}

// namesBothViews says whether the diagnostic is a CO-DGM-002 that names the views `first` and `second`.
func namesBothViews(d *diag.Diagnostic) bool {
	return d.Code == diag.DGM002 && strings.Contains(d.Message, "first") && strings.Contains(d.Message, "second")
}

// Two generated views must not write the same path: each is a CO-DGM-002 that names both view IDs.
func TestValidateAgainstRejectsViewsThatWriteTheSamePath(t *testing.T) {
	t.Parallel()

	tests := map[string]func(a, b *views.Definition){
		"same output path": func(_, b *views.Definition) { b.Output.Path = "docs/a.dsl" },
		"export path equals another output path": func(_, b *views.Definition) {
			b.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/a.dsl"}}
		},
		"same export path": func(a, b *views.Definition) {
			a.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/x.mmd"}}
			b.Output.Exports = []views.Export{{Format: "mermaid", Path: "docs/x.mmd"}}
		},
		"output path equals another manifest path": func(_, b *views.Definition) { b.Output.Path = "docs/a.dsl" + views.ManifestSuffix },
	}

	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			a, b := generatedView("first"), generatedView("second")
			a.Output.Path = "docs/a.dsl"
			b.Output.Path = "docs/b.dsl"
			edit(&a, &b)

			got := problems(t, workspaceWith(a, b), paymentsModel(), &workspace.FakeGit{})

			if len(got) == 0 {
				t.Fatal("want a diagnostic")
			}

			for i := range got {
				if !namesBothViews(&got[i]) {
					t.Errorf("got %s: %s", got[i].Code, got[i].Message)
				}
			}
		})
	}
}

func TestValidateAgainstAcceptsATrackedPath(t *testing.T) {
	t.Parallel()

	v := generatedView("v1")
	v.Output.Path = "docs/architecture/views/container.dsl"

	// The ignore rule matches another path only: the set decides, nothing else.
	git := &workspace.FakeGit{Ignored: map[string]bool{"build/concordia/": true}}

	if got := problems(t, workspaceWith(v), paymentsModel(), git); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestValidateAgainstSkipsCALMChecksWithoutAModel(t *testing.T) {
	t.Parallel()

	v := generatedView("v1")
	v.Scope = []string{"ghost"}

	if got := problems(t, workspaceWith(v), nil, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestValidateAgainstListsNoEnvironmentWhenNoneIsDeclared(t *testing.T) {
	t.Parallel()

	v := generatedView("v1")
	v.Environment = "prod"

	wsp := workspaceWith(v)
	wsp.Environments = nil

	got := problems(t, wsp, paymentsModel(), nil)
	if len(got) != 1 || !strings.Contains(got[0].Message, "declares no environment") {
		t.Fatalf("got %v", got)
	}
}

func TestDiagnoseCombinesProblemsAndSuggestions(t *testing.T) {
	t.Parallel()

	v := generatedView("v1")
	v.Environment = "ghost"

	got, err := views.Diagnose(t.Context(), workspaceWith(v), paymentsModel(), &workspace.FakeGit{})
	if err != nil {
		t.Fatal(err)
	}

	codes := map[string]int{}
	for _, d := range got {
		codes[d.Code]++
	}

	if codes[diag.DGM001] != 1 || codes[diag.DGM005] == 0 {
		t.Fatalf("got %v", got)
	}
}

func TestDiagnoseSuggestsNothingForAWorkspaceWithoutViewsOrEnvironments(t *testing.T) {
	t.Parallel()

	wsp := workspaceWith()
	wsp.Views, wsp.Environments = nil, nil

	got, err := views.Diagnose(t.Context(), wsp, paymentsModel(), &workspace.FakeGit{})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
