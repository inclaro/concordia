package adr_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestValidateTriggers(t *testing.T) {
	t.Parallel()

	if err := adr.ValidateTriggers([]workspace.ADRTrigger{
		{ID: "A", Paths: []string{"deploy/**"}}, {ID: "B", Architecture: []string{"node-added"}},
	}); err != nil {
		t.Errorf("valid triggers: %v", err)
	}

	cases := map[string][]workspace.ADRTrigger{
		"duplicate ID": {{ID: "A", Paths: []string{"a/**"}}, {ID: "A", Paths: []string{"b/**"}}},
		"empty":        {{ID: "A", Tags: []string{"t"}}},
		"bad glob":     {{ID: "A", Paths: []string{"a/[**"}}},
	}

	for name, triggers := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_ = wantDiag(t, adr.ValidateTriggers(triggers), diag.ADR005)
		})
	}
}

func TestLoadDeclarations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"gov/r.yaml": registerHeader +
		"  - {id: ADR-3, governs: [{ref: 'calm:node:orders'}, {select: {tags: [orders-data]}}], enforcedBy: [{rule: CORE-DB-001, pack: {id: core, version: 1.0.0}}]}\n"})

	wsp := &workspace.Workspace{Root: dir, ADR: &workspace.ADR{
		Files:     []string{"gov/r.yaml"},
		Lifecycle: workspace.ADRLifecycle{Active: workspace.DefaultADRActive(), StatusMap: map[string]string{"Ratified": "accepted"}},
	}}

	decl, err := adr.LoadDeclarations(wsp, subjectModel(), compiledPacks())
	if err != nil {
		t.Fatal(err)
	}

	key := adr.EntryKey(&decl.Register.Entries[0])
	if got := strings.Join(decl.Subjects[key], ","); got != "calm:node:orders,calm:node:orders-db" {
		t.Errorf("subjects = %s", got)
	}

	if len(decl.Inputs) != 2 || decl.Inputs[0].Path != "concordia.yaml#adr.lifecycle" || decl.Inputs[1].Path != "gov/r.yaml" ||
		!strings.HasPrefix(decl.Inputs[0].Digest, "sha256:") || decl.Triggers == nil {
		t.Errorf("inputs = %+v", decl.Inputs)
	}

	// The lifecycle digest follows the profile.
	wsp.ADR.Lifecycle.StatusMap = map[string]string{"Ratified": "implemented"}

	other, err := adr.LoadDeclarations(wsp, subjectModel(), compiledPacks())
	if err != nil || other.Inputs[0].Digest == decl.Inputs[0].Digest {
		t.Errorf("lifecycle digest must change with the profile: %v", err)
	}

	// The lifecycle profile is recorded under the actual workspace file, so `export bundle` finds it as a part of
	// that file and never reads it as a file of its own.
	wsp.Path = dir + "/concordia.ci.yaml"

	named, err := adr.LoadDeclarations(wsp, subjectModel(), compiledPacks())
	if err != nil || named.Inputs[0].Path != "concordia.ci.yaml#adr.lifecycle" {
		t.Errorf("inputs with workspace concordia.ci.yaml = %+v (%v)", named.Inputs, err)
	}

	// An invalid link fails the load.
	compiled := compiledPacks()
	compiled.Packs[0].Version = "2.0.0"
	compiled.DeclaredRules[0].Pack.Version = "2.0.0"
	compiled.DeclaredRules[1].Pack.Version = "2.0.0"

	_, err = adr.LoadDeclarations(wsp, subjectModel(), compiled)
	_ = wantDiag(t, err, diag.ADR004)
}

func TestLoadDeclarationsWithoutRegister(t *testing.T) {
	t.Parallel()

	for _, wsp := range []*workspace.Workspace{{}, {ADR: &workspace.ADR{}}} {
		decl, err := adr.LoadDeclarations(wsp, subjectModel(), compiledPacks())
		if decl != nil || err != nil {
			t.Errorf("got %v, %v; want nil, nil", decl, err)
		}
	}
}
