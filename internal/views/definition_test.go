package views_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/views"
)

const (
	testDigest       = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	testWorkspaceURI = "concordia.yaml"
)

// generatedView is a valid generated container view.
func generatedView(id string) views.Definition {
	return views.Definition{
		ID: id, Kind: views.KindContainer, Origin: views.OriginGenerated, Owner: "team", Scope: []string{"payments-system"},
		Abstraction: views.AbstractionContainer,
		Output:      &views.Output{Path: "docs/architecture/views/" + id + ".dsl"},
	}
}

// authoredView is a valid authored system-context view with a Mermaid source.
func authoredView(id string) views.Definition {
	return views.Definition{
		ID: id, Kind: views.KindSystemContext, Origin: views.OriginAuthored, Owner: "team", Scope: []string{"payments-system"},
		Abstraction: views.AbstractionSystem,
		Source:      &views.Source{Path: "docs/arch/payments.md", Format: "mermaid", Selector: &views.Selector{Title: "Payments"}},
		Mapping:     &views.Mapping{Elements: map[string]string{"orders-api": "orders-service"}},
	}
}

func rasterView(id string) views.Definition {
	v := authoredView(id)
	v.Source = &views.Source{Path: "docs/arch/payments.png", Format: "png"}
	v.Mapping = nil

	return v
}

// definitionScenario is one case of definition validation.
type definitionScenario struct {
	name string
	// want is a fragment of the CO-DGM-001 message; "" means the definitions are valid.
	want string
	defs []views.Definition
}

// mutatedView gives one definition: the view `v1` that build makes, after the change.
func mutatedView(build func(id string) views.Definition, change func(v *views.Definition)) []views.Definition {
	v := build("v1")
	change(&v)

	return []views.Definition{v}
}

func identityScenarios() []definitionScenario {
	return []definitionScenario{
		{"complete generated view", "", []views.Definition{generatedView("payments-container")}},
		{"complete authored view", "", []views.Definition{authoredView("payments-context")}},
		{"no views", "", nil},
		{"duplicate ID names both definitions", "views[0] and views[1]", []views.Definition{generatedView("v1"), authoredView("v1")}},
		{"id pattern", "[a-z][a-z0-9-]*", []views.Definition{generatedView("Payments_View")}},
		{"kind and abstraction disagree", "requires abstraction 'container'", mutatedView(generatedView, func(v *views.Definition) {
			v.Abstraction = views.AbstractionSystem
		})},
	}
}

func kindScenarios() []definitionScenario {
	return []definitionScenario{
		{"generated trust view", "no generator exists for this kind", mutatedView(generatedView, func(v *views.Definition) {
			v.Kind = views.KindTrust
		})},
		{"generated data-flow view", "no generator exists for this kind", mutatedView(generatedView, func(v *views.Definition) {
			v.Kind = views.KindDataFlow
		})},
		{"authored trust view declares its abstraction", "", mutatedView(authoredView, func(v *views.Definition) {
			v.Kind = views.KindTrust
			v.Abstraction = views.AbstractionContainer
		})},
		{"deployment view without environment", "must declare environment", mutatedView(generatedView, func(v *views.Definition) {
			v.Kind = views.KindDeployment
			v.Abstraction = views.AbstractionDeployment
		})},
		{"deployment view with environment", "", mutatedView(generatedView, func(v *views.Definition) {
			v.Kind = views.KindDeployment
			v.Abstraction = views.AbstractionDeployment
			v.Environment = "prod"
		})},
	}
}

func sourceScenarios() []definitionScenario {
	return []definitionScenario{
		{"generated view without output", "must declare output.path", mutatedView(generatedView, func(v *views.Definition) {
			v.Output = nil
		})},
		{"authored view without source", "must declare source", mutatedView(authoredView, func(v *views.Definition) {
			v.Source = nil
		})},
		{"ordinal selector", "title or anchor, not by index or ordinal", mutatedView(authoredView, func(v *views.Definition) {
			v.Source.Selector = &views.Selector{}
		})},
		{"selector with title and anchor", "exactly one of title or anchor", mutatedView(authoredView, func(v *views.Definition) {
			v.Source.Selector = &views.Selector{Title: "t", Anchor: "a"}
		})},
		{"anchor selector", "", mutatedView(authoredView, func(v *views.Definition) {
			v.Source.Selector = &views.Selector{Anchor: "payments"}
		})},
		{"static export", "'static' has no canonicalization rule", mutatedView(generatedView, func(v *views.Definition) {
			v.Output.Exports = []views.Export{{Format: "mermaid", Path: "a.mmd"}, {Format: "static", Path: "site"}}
		})},
	}
}

func rasterScenarios() []definitionScenario {
	return []definitionScenario{
		{"raster without depiction is valid", "", []views.Definition{rasterView("v1")}},
		{"raster with depiction is valid", "", mutatedView(rasterView, func(v *views.Definition) {
			v.Mapping = &views.Mapping{Depicts: &views.Depicts{Digest: testDigest, Elements: []string{"orders-service"}}}
		})},
		{"depiction on a structured source", "mapping.depicts applies to a raster source", mutatedView(authoredView, func(v *views.Definition) {
			v.Mapping.Depicts = &views.Depicts{Digest: testDigest}
		})},
		{"structured authored view without mapping", "must declare mapping.elements", mutatedView(authoredView, func(v *views.Definition) {
			v.Mapping = nil
		})},
	}
}

// requireOneDefinitionDiagnostic checks that the diagnostics are the one CO-DGM-001 error that names the view
// and has the wanted message.
func requireOneDefinitionDiagnostic(t *testing.T, got []diag.Diagnostic, want string) {
	t.Helper()

	if len(got) != 1 {
		t.Fatalf("want one diagnostic, got %v", got)
	}

	d := got[0]
	if d.Code != diag.DGM001 || !strings.Contains(d.Message, want) {
		t.Errorf("got %s: %s, want a message with %q", d.Code, d.Message, want)
	}

	if exit, _ := diag.ExitCodeOf(d.Code); exit != diag.InvalidInput {
		t.Errorf("exit code %d", exit)
	}

	if !strings.Contains(d.Message, "v1") && !strings.Contains(d.Message, "Payments_View") {
		t.Errorf("the message does not name the view: %s", d.Message)
	}
}

func TestValidateDefinitionsOneCasePerScenario(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(identityScenarios(), kindScenarios(), sourceScenarios(), rasterScenarios())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := views.ValidateDefinitions(tc.defs, testWorkspaceURI)

			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("want valid, got %v", got)
				}

				return
			}

			requireOneDefinitionDiagnostic(t, got, tc.want)
		})
	}
}

func TestEffectiveDefaults(t *testing.T) {
	t.Parallel()

	v := generatedView("v1")

	if got := v.EffectiveAgainst(); !slices.Equal(got, []string{"declared", "observed"}) {
		t.Errorf("against %v", got)
	}

	if got := effectiveSettings(&v); got != [3]string{"error", "omit", "merge"} {
		t.Errorf("defaults %s %s %s", got[0], got[1], got[2])
	}

	v.Comparison = &views.Comparison{Against: []string{"declared"}, Severity: "warning"}
	v.Projection = &views.ProjectionRules{SelfEdges: "keep", ParallelEdges: "separate"}

	if got := v.EffectiveAgainst(); !slices.Equal(got, []string{"declared"}) {
		t.Errorf("declared against: %v", got)
	}

	if got := effectiveSettings(&v); got != [3]string{"warning", "keep", "separate"} {
		t.Errorf("declared values: %s %s %s", got[0], got[1], got[2])
	}

	if got := v.OutputPaths(); !slices.Equal(got, []string{"docs/architecture/views/v1.dsl"}) {
		t.Errorf("output paths %v", got)
	}
}

// effectiveSettings gives the effective severity, self-edge rule and parallel-edge rule of a view.
func effectiveSettings(v *views.Definition) [3]string {
	return [3]string{v.EffectiveSeverity(), v.EffectiveSelfEdges(), v.EffectiveParallelEdges()}
}
