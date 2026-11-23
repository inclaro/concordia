package adr_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/workspace"
)

func triggerBase() *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		Nodes: []intent.NodeDeclaration{
			{ID: "orders", NodeType: "service", Tags: []string{}},
			{ID: "orders-db", NodeType: "database", Tags: []string{"data"}},
		},
	}
}

func triggerCurrent() *intent.ArchitectureModel {
	cur := triggerBase()
	cur.Relationships = []intent.RelationshipDeclaration{
		{ID: "orders-to-db", Kind: intent.RelConnects, Source: "orders", Targets: []string{"orders-db"}},
	}

	return cur
}

func triggerRegister() (reg *adr.Register, paths map[string]string) {
	reg = &adr.Register{Entries: []adr.Entry{
		{ID: "ADR-1", Categories: []string{"deployment-strategy"}},
		{ID: "ADR-2", Categories: []string{"system-boundaries", "team-topology"}},
	}}
	paths = map[string]string{
		adr.EntryKey(&reg.Entries[0]): "docs/adr/0001.md",
		adr.EntryKey(&reg.Entries[1]): "docs/adr/0002.md",
	}

	return reg, paths
}

var triggerDefs = []workspace.ADRTrigger{
	{ID: "TRG-DEPLOY", Category: "deployment-strategy", Paths: []string{"deploy/**"}},
	{ID: "TRG-BOUNDARY", Category: "system-boundaries", Architecture: []string{"relationship-added"}},
}

// triggerWant stops the test when the value differs from the wanted one.
func triggerWant[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// triggerWantList stops the test when the list differs from the wanted one.
func triggerWantList(t *testing.T, what string, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// Scenario: Trigger on a deployment path.
func TestTriggerDeployPathRaised(t *testing.T) {
	reg, paths := triggerRegister()
	got := adr.EvaluateTriggers(triggerDefs, &adr.ChangeSet{Base: "main", Files: []string{"deploy/orders.yaml", "src/a.go"}},
		triggerBase(), nil, triggerBase(), reg, paths)

	triggerWant(t, "result count", len(got), 2)
	triggerWant(t, "first result (sorted by ID)", got[0].ID, "TRG-BOUNDARY")
	triggerWant(t, "second result (sorted by ID)", got[1].ID, "TRG-DEPLOY")

	deploy := got[1]
	triggerWant(t, "deploy raised", deploy.Raised, true)
	triggerWant(t, "deploy result", deploy.Result, "violation")
	triggerWantList(t, "deploy raised by", deploy.RaisedBy, []string{"deploy/orders.yaml"})

	triggerWant(t, "boundary raised", got[0].Raised, false)
	triggerWant(t, "boundary result", got[0].Result, "pass")
}

// Scenario: Trigger on a relationship change.
func TestTriggerRelationshipAddedNamesRelationship(t *testing.T) {
	reg, paths := triggerRegister()
	got := adr.EvaluateTriggers(triggerDefs[1:], &adr.ChangeSet{Files: []string{"architecture/a.json"}},
		triggerBase(), nil, triggerCurrent(), reg, paths)

	triggerWant(t, "raised", got[0].Raised, true)
	triggerWantList(t, "raised by", got[0].RaisedBy, []string{"relationship-added:orders-to-db"})
	triggerWant(t, "result", got[0].Result, "violation")
}

// Scenario: Internal edit does not raise a trigger.
func TestTriggerInternalEditRaisesNothing(t *testing.T) {
	reg, paths := triggerRegister()
	got := adr.EvaluateTriggers(triggerDefs, &adr.ChangeSet{Files: []string{"internal/x.go"}},
		triggerBase(), nil, triggerBase(), reg, paths)

	for _, r := range got {
		triggerWant(t, r.ID+" raised", r.Raised, false)
		triggerWant(t, r.ID+" result", r.Result, "pass")
		triggerWantList(t, r.ID+" raised by", r.RaisedBy, nil)
		triggerWantList(t, r.ID+" satisfied by", r.SatisfiedBy, nil)
	}
}

// Scenario: Unknown category is allowed as custom.
func TestTriggerCustomCategory(t *testing.T) {
	reg, paths := triggerRegister()
	trig := []workspace.ADRTrigger{{ID: "TRG-TEAM", Category: "team-topology", Paths: []string{"owners/**"}}}

	unsat := adr.EvaluateTriggers(trig, &adr.ChangeSet{Files: []string{"owners/CODEOWNERS"}}, nil, nil, nil, reg, paths)
	triggerWant(t, "unsatisfied result", unsat[0].Result, "violation")
	triggerWant(t, "unsatisfied category", unsat[0].Category, "team-topology")

	sat := adr.EvaluateTriggers(trig, &adr.ChangeSet{Files: []string{"docs/adr/0002.md", "owners/CODEOWNERS"}}, nil, nil, nil, reg, paths)
	triggerWant(t, "satisfied result", sat[0].Result, "pass")
	triggerWantList(t, "satisfied by", sat[0].SatisfiedBy, []string{"ADR-2"})
}

// Scenario: Change set adds the decision.
// Scenario: No decision in the change set.
func TestTriggerSatisfiedAndUnsatisfied(t *testing.T) {
	reg, paths := triggerRegister()

	// A changed ADR of another category does not satisfy.
	other := adr.EvaluateTriggers(triggerDefs[:1], &adr.ChangeSet{Files: []string{"deploy/a.yaml", "docs/adr/0002.md"}}, nil, nil, nil, reg, paths)
	triggerWant(t, "other category result", other[0].Result, "violation")
	triggerWant(t, "other category reason", other[0].Reason, string(adr.ReasonTriggerUnsatisfied))

	// The category's decision exists but did not change.
	unchanged := adr.EvaluateTriggers(triggerDefs[:1], &adr.ChangeSet{Files: []string{"deploy/a.yaml"}}, nil, nil, nil, reg, paths)
	triggerWant(t, "unchanged result", unchanged[0].Result, "violation")

	sat := adr.EvaluateTriggers(triggerDefs[:1], &adr.ChangeSet{Files: []string{"deploy/a.yaml", "docs/adr/0001.md"}}, nil, nil, nil, reg, paths)
	triggerWant(t, "satisfied result", sat[0].Result, "pass")
	triggerWantList(t, "satisfied by", sat[0].SatisfiedBy, []string{"ADR-1"})
	triggerWant(t, "satisfied raised", sat[0].Raised, true)
}

// Scenario: Run without a change set.
func TestTriggerNoChangeSetNotApplicable(t *testing.T) {
	got := adr.EvaluateTriggers(triggerDefs, nil, nil, nil, nil, nil, nil)
	triggerWant(t, "result count", len(got), 2)

	for _, r := range got {
		triggerWant(t, r.ID+" result", r.Result, "not-applicable")
		triggerWant(t, r.ID+" raised", r.Raised, false)
		triggerWant(t, r.ID+" has a reason", r.Reason != "", true)
	}
}

// wantUnreadableBaseResults checks that, with no path match and an unreadable base, the architecture triggers are
// unknown with a reason and the path-only trigger still passes.
func wantUnreadableBaseResults(t *testing.T, got []conformance.TriggerResult) {
	t.Helper()

	want := map[string]string{"TRG-BOTH": "unknown", "TRG-BOUNDARY": "unknown", "TRG-DEPLOY": "pass"}

	for _, r := range got {
		triggerWant(t, r.ID+" result", r.Result, want[r.ID])

		if r.Result == "unknown" {
			triggerWant(t, r.ID+" has a reason", r.Reason != "", true)
		}
	}
}

// wantPathRaiseDecides checks that a path raise decides the trigger that lists paths and architecture changes.
func wantPathRaiseDecides(t *testing.T, got []conformance.TriggerResult) {
	t.Helper()

	for _, r := range got {
		if r.ID == "TRG-BOTH" {
			triggerWant(t, "path raise raised", r.Raised, true)
			triggerWant(t, "path raise result", r.Result, "violation")
		}
	}
}

func TestTriggerBaseUnreadableUnknownForArchitectureOnly(t *testing.T) {
	reg, paths := triggerRegister()
	both := workspace.ADRTrigger{ID: "TRG-BOTH", Category: "deployment-strategy", Paths: []string{"deploy/**"}, Architecture: []string{"node-added"}}
	trig := append([]workspace.ADRTrigger{both}, triggerDefs...)

	// No path matches: architecture triggers are unknown, the path-only trigger still passes.
	got := adr.EvaluateTriggers(trig, &adr.ChangeSet{Files: []string{"src/a.go"}}, nil, errors.New("boom"), triggerCurrent(), reg, paths)
	wantUnreadableBaseResults(t, got)

	// A path raise still decides.
	got = adr.EvaluateTriggers(trig, &adr.ChangeSet{Files: []string{"deploy/a.yaml"}}, nil, errors.New("boom"), triggerCurrent(), reg, paths)
	wantPathRaiseDecides(t, got)
}

func TestTriggerArchitectureNarrowing(t *testing.T) {
	reg, paths := triggerRegister()
	cur := triggerBase()
	cur.Nodes = append(cur.Nodes, intent.NodeDeclaration{ID: "cache", NodeType: "database", Tags: []string{}})
	cur.Relationships = []intent.RelationshipDeclaration{{ID: "orders-to-cache", Source: "orders", Targets: []string{"cache"}}}
	change := &adr.ChangeSet{Files: []string{"a"}}

	run := func(trig workspace.ADRTrigger) []string {
		return adr.EvaluateTriggers([]workspace.ADRTrigger{trig}, change, triggerBase(), nil, cur, reg, paths)[0].RaisedBy
	}

	if got := run(workspace.ADRTrigger{ID: "T", Category: "c", Architecture: []string{"node-added", "relationship-added"}, NodeTypes: []string{"database"}}); !slices.Equal(got, []string{"node-added:cache", "relationship-added:orders-to-cache"}) {
		t.Fatalf("nodeTypes: %v", got)
	}

	if got := run(workspace.ADRTrigger{ID: "T", Category: "c", Architecture: []string{"node-added", "relationship-added"}, Tags: []string{"data"}}); len(got) != 0 {
		t.Fatalf("tags should not match: %v", got)
	}

	if got := run(workspace.ADRTrigger{ID: "T", Category: "c", Architecture: []string{"node-removed"}}); len(got) != 0 {
		t.Fatalf("node-removed: %v", got)
	}

	rem := adr.EvaluateTriggers([]workspace.ADRTrigger{{ID: "T", Category: "c", Architecture: []string{"relationship-removed", "node-removed"}}},
		change, cur, nil, triggerBase(), reg, paths)[0].RaisedBy
	if !slices.Equal(rem, []string{"node-removed:cache", "relationship-removed:orders-to-cache"}) {
		t.Fatalf("removed: %v", rem)
	}
}

func TestLoadBaseModelFromGit(t *testing.T) {
	doc, err := os.ReadFile("../../test/fixtures/calm/valid.calm.json")
	if err != nil {
		t.Fatal(err)
	}

	git := &workspace.FakeGit{FilesAt: map[string]map[string]string{"main": {"architecture/a.json": string(doc), "architecture/bad.json": "{"}}}
	wsp := &workspace.Workspace{Architecture: workspace.Architecture{Source: "architecture/a.json"}}

	model, err := adr.LoadBaseModel(context.Background(), wsp, git, "main")
	if err != nil || len(model.Nodes) == 0 {
		t.Fatalf("model=%v err=%v", model, err)
	}

	if _, err = adr.LoadBaseModel(context.Background(), wsp, git, "other"); err == nil {
		t.Fatal("missing base file should fail")
	}

	wsp.Architecture.Source = "architecture/bad.json"
	if _, err = adr.LoadBaseModel(context.Background(), wsp, git, "main"); err == nil {
		t.Fatal("invalid document should fail")
	}

	wsp.Architecture.Source = "https://example.com/a.json"
	if _, err = adr.LoadBaseModel(context.Background(), wsp, git, "main"); err == nil {
		t.Fatal("remote source should fail")
	}
}

const (
	baseSource     = "architecture/a.calm.json"
	baseMapping    = "architecture/url-mapping.json"
	baseControlURL = "https://example.org/controls/data-ownership/1.0/requirement.json"
)

// baseCALM is a CALM document with the orders service and a restricted orders-db that has a control whose
// requirement URL is baseControlURL. The document has no relationship.
const baseCALM = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "orders", "node-type": "service", "name": "Orders", "description": "Orders"},
    {"unique-id": "orders-db", "node-type": "database", "name": "Orders DB", "description": "Orders data",
     "concordia": {"trust": {"classification": "restricted"}},
     "controls": {"ctl-ownership": {"description": "Orders owns its data.", "requirements": [
       {"requirement-url": "` + baseControlURL + `", "config": {"control-id": "ctl-ownership", "engine": "concordia", "rule-ids": ["DATA-001"]}}
     ]}}}
  ],
  "relationships": []
}`

// baseWorkspace declares the restricted classification and the URL mapping file of the base revision.
func baseWorkspace() *workspace.Workspace {
	return &workspace.Workspace{
		Architecture: workspace.Architecture{Source: baseSource, URLMapping: baseMapping},
		Trust:        &workspace.Trust{Classifications: []string{"public", "internal", "restricted"}},
	}
}

// A base CALM that uses a workspace-declared classification and a URL-mapped control requirement loads with the
// mapping and the mapped file of the base revision, and the architecture trigger is decided.
func TestLoadBaseModelUsesWorkspaceTrustAndRevisionURLMapping(t *testing.T) {
	git := &workspace.FakeGit{FilesAt: map[string]map[string]string{"main": {
		baseSource:  baseCALM,
		baseMapping: `{"` + baseControlURL + `": "resources/requirement.json"}`,
		"architecture/resources/requirement.json": `{"description": "data ownership requirement"}`,
	}}}

	model, err := adr.LoadBaseModel(context.Background(), baseWorkspace(), git, "main")
	if err != nil {
		t.Fatalf("load base model: %v", err)
	}

	trig := []workspace.ADRTrigger{{ID: "TRG-DATA", Category: "data-ownership", Architecture: []string{"relationship-added"}, NodeTypes: []string{"database"}}}
	reg, paths := triggerRegister()

	got := adr.EvaluateTriggers(trig, &adr.ChangeSet{Files: []string{baseSource}}, model, nil, triggerCurrent(), reg, paths)
	triggerWant(t, "result", got[0].Result, "violation")
	triggerWantList(t, "raised by", got[0].RaisedBy, []string{"relationship-added:orders-to-db"})
}

// A base revision without the mapping file has no mapping, so its mapped control requirement is not resolved.
func TestLoadBaseModelWithoutMappingAtBaseFails(t *testing.T) {
	git := &workspace.FakeGit{FilesAt: map[string]map[string]string{"main": {baseSource: baseCALM}}}

	_, err := adr.LoadBaseModel(context.Background(), baseWorkspace(), git, "main")
	if err == nil || !strings.Contains(err.Error(), baseControlURL+" has no entry in architecture.urlMapping") {
		t.Fatalf("err = %v, want the unmapped requirement URL", err)
	}
}
