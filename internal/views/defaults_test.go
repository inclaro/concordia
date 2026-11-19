package views_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

func messages(list []diag.Diagnostic) []string {
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.Message
	}

	return out
}

// declaredWorkspace declares every suggested default view of paymentsModel and a production environment.
func declaredWorkspace() *workspace.Workspace {
	context := authoredView("payments-context")

	container := generatedView("payments-container")

	deployment := generatedView("payments-prod")
	deployment.Kind = views.KindDeployment
	deployment.Abstraction = views.AbstractionDeployment
	deployment.Environment = "prod"

	return workspaceWith(context, container, deployment)
}

func TestSuggestDefaultsFullyDeclaredWorkspacePrintsNothing(t *testing.T) {
	t.Parallel()

	got := views.SuggestDefaults(paymentsModel(), declaredWorkspace())
	if got == nil || len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestSuggestDefaultsMissingProductionDeploymentView(t *testing.T) {
	t.Parallel()

	wsp := declaredWorkspace()
	wsp.Views = wsp.Views[:2]

	got := views.SuggestDefaults(paymentsModel(), wsp)
	if len(got) != 1 {
		t.Fatalf("got %v", messages(got))
	}

	d := got[0]
	if d.Code != diag.DGM005 || d.Severity != diag.SeverityInfo || !strings.Contains(d.Message, "deployment view for the production environment 'prod'") {
		t.Errorf("got %s %s: %s", d.Code, d.Severity, d.Message)
	}

	if _, stops := diag.ExitCodeOf(d.Code); stops {
		t.Error("a suggestion never changes the exit code")
	}
}

func TestSuggestDefaultsListsEachMissingViewInOrder(t *testing.T) {
	t.Parallel()

	got := messages(views.SuggestDefaults(paymentsModel(), workspaceWith()))
	want := []string{
		"a system-context view for the system 'payments-system'",
		"a container view for the system 'payments-system', which has services",
		"a deployment view for the production environment 'prod'",
	}

	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}

	for i := range want {
		if !strings.HasSuffix(got[i], want[i]) {
			t.Errorf("suggestion %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSuggestDefaultsNeedsServicesForAContainerView(t *testing.T) {
	t.Parallel()

	model := paymentsModel()
	model.Nodes[2].NodeType = "database"

	wsp := declaredWorkspace()
	wsp.Views = []views.Definition{wsp.Views[0], wsp.Views[2]}

	if got := views.SuggestDefaults(model, wsp); len(got) != 0 {
		t.Fatalf("got %v", messages(got))
	}
}

func TestSuggestDefaultsUsesTheC4LevelOverride(t *testing.T) {
	t.Parallel()

	model := &intent.ArchitectureModel{Nodes: []intent.NodeDeclaration{
		{ID: "shop", NodeType: "service", C4Level: intent.C4SoftwareSystem},
		{ID: "demoted", NodeType: "system", C4Level: intent.C4Container},
	}}

	wsp := workspaceWith()
	wsp.Environments = nil

	got := messages(views.SuggestDefaults(model, wsp))
	if len(got) != 1 || !strings.Contains(got[0], "'shop'") {
		t.Fatalf("got %q", got)
	}
}

func TestSuggestDefaultsWithoutModelOrEnvironmentsIsQuiet(t *testing.T) {
	t.Parallel()

	wsp := workspaceWith()
	wsp.Environments = []workspace.Environment{{ID: "staging", Sources: []string{"s"}}}

	if got := views.SuggestDefaults(nil, wsp); len(got) != 0 {
		t.Fatalf("got %v", messages(got))
	}
}

func TestIsProductionEnvironment(t *testing.T) {
	t.Parallel()

	for id, want := range map[string]bool{"prod": true, "production": true, "staging": false, "prod-eu": false} {
		if got := views.IsProductionEnvironment(id); got != want {
			t.Errorf("%s: %v", id, got)
		}
	}
}
