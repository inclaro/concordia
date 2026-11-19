package adr_test

import (
	"context"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// decisionOpt changes a Decision of the pure obligation tests.
type decisionOpt func(*adr.Decision)

// newDecision makes an active, committed decision with the ID and no links.
func newDecision(id string, opts ...decisionOpt) *adr.Decision {
	dec := &adr.Decision{
		Entry:         &adr.Entry{ID: adr.CanonicalID(id), Expect: adr.ExpectActive, File: "gov/r.yaml", Line: 4},
		Subjects:      []string{},
		Applicability: adr.ApplicabilityActive,
		Presence: adr.PresenceResult{
			State: adr.PresenceCommitted, Path: "docs/adr/" + id + ".md",
			Decision: &adr.EvidenceDecision{Path: "docs/adr/" + id + ".md", Label: adr.CanonicalID(id), Status: "accepted", StatusText: "Accepted", Line: 1},
		},
	}

	for _, opt := range opts {
		opt(dec)
	}

	return dec
}

func withApplicability(state adr.ApplicabilityState, reason string) decisionOpt {
	return func(d *adr.Decision) { d.Applicability, d.Reason = state, reason }
}

func historical() decisionOpt { return func(d *adr.Decision) { d.Entry.Expect = adr.ExpectHistorical } }

func governing(subjects ...string) decisionOpt {
	return func(d *adr.Decision) { d.Subjects = subjects }
}

func enforcedBy(refs ...string) decisionOpt {
	return func(d *adr.Decision) {
		for _, text := range refs {
			rule, ok := adr.ParseRuleRef(text)
			if !ok {
				panic("bad rule ref " + text)
			}

			rule.Line = 9
			d.Entry.EnforcedBy = append(d.Entry.EnforcedBy, rule)
		}
	}
}

func requiring() decisionOpt {
	return func(d *adr.Decision) { d.Entry.Enforcement = adr.EnforcementRequired }
}

func releasing(items ...adr.Release) decisionOpt {
	return func(d *adr.Decision) { d.Entry.Released = items }
}

func successor(id string, effective bool) decisionOpt {
	return func(d *adr.Decision) {
		d.Successors = append(d.Successors, adr.Neighbor{Path: "docs/adr/" + id + ".md", ID: adr.CanonicalID(id), Effective: effective})
	}
}

const coreRule = "CORE-DB-001@core/1.0.0"

// plan makes a selected rule plan of a pack version.
func plan(rule, pack string, severity policy.RuleSeverity, onUnknown policy.UnknownDisposition) policy.RulePlan {
	return policy.RulePlan{
		RuleID: rule, Name: rule, Severity: severity, Pack: policy.PackRef{ID: pack, Version: "1.0.0"},
		EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown},
	}
}

func corePlans(severity policy.RuleSeverity, onUnknown policy.UnknownDisposition) adr.Classifier {
	return adr.Classifier{Plans: []policy.RulePlan{plan("CORE-DB-001", "core", severity, onUnknown)}, FailOn: diag.SeverityError}
}

func mandatoryCore() adr.Classifier { return corePlans(policy.SeverityError, policy.UnknownFail) }

// govPlans are the selected plans of the six governance rules, with the default severities of the pack.
func govPlans() []policy.RulePlan {
	table := []struct {
		id, obligation string
		severity       policy.RuleSeverity
	}{
		{adr.RulePresence, adr.ObligationPresence, policy.SeverityError},
		{adr.RuleApplicability, adr.ObligationApplicability, policy.SeverityError},
		{adr.RuleJustification, adr.ObligationJustification, policy.SeverityError},
		{adr.RuleEnforcement, adr.ObligationEnforcement, policy.SeverityError},
		{adr.RuleHistory, adr.ObligationHistory, policy.SeverityError},
		{adr.RuleScope, adr.ObligationScope, policy.SeverityWarning},
	}

	out := make([]policy.RulePlan, 0, len(table))

	for _, row := range table {
		p := plan(row.id, "adr-governance", row.severity, policy.UnknownFail)
		p.Predicate = policy.Predicate{Family: policy.FamilyDecisionGovernance, Decisions: row.obligation}
		out = append(out, p)
	}

	return out
}

// world is the input of an end-to-end evaluation on pure data: a register text, decisions as the provider reports
// them, and Git facts of a fake Git port.
type world struct {
	t       *testing.T
	git     *recordingGit
	nodes   map[string]observation.ObservedNode
	changed *conformance.RunChanged
	// register is the text after the register header.
	register string
	edges    []*observation.EvidenceEdge
	plans    []policy.RulePlan
	// triggers are the `adr.triggers` of the workspace.
	triggers            []workspace.ADRTrigger
	projects            []conformance.DecisionProject
	unsupportedProvider bool
}

// recordingGit is a FakeGit that records which methods the evaluation calls.
type recordingGit struct {
	*workspace.FakeGit
	calls []string
}

func (g *recordingGit) Revision(ctx context.Context) (string, error) {
	g.calls = append(g.calls, "Revision")

	return g.FakeGit.Revision(ctx)
}

func (g *recordingGit) DirtyFiles(ctx context.Context) ([]string, error) {
	g.calls = append(g.calls, "DirtyFiles")

	return g.FakeGit.DirtyFiles(ctx)
}

func (g *recordingGit) ListFilesAt(ctx context.Context, rev string) ([]string, error) {
	g.calls = append(g.calls, "ListFilesAt")

	return g.FakeGit.ListFilesAt(ctx, rev)
}

func (g *recordingGit) ShowFile(ctx context.Context, rev, path string) (data []byte, found bool, err error) {
	g.calls = append(g.calls, "ShowFile")

	return g.FakeGit.ShowFile(ctx, rev, path)
}

// newWorld makes a world with a committed Git revision that tracks the files.
func newWorld(t *testing.T, tracked ...string) *world {
	t.Helper()

	return &world{
		t: t, nodes: map[string]observation.ObservedNode{}, plans: slices.Concat(govPlans(), []policy.RulePlan{
			plan("CORE-DB-001", "core", policy.SeverityError, policy.UnknownFail),
		}),
		git: &recordingGit{FakeGit: fakeGit(testRev, tracked)},
	}
}

// addADR adds a decision node as CBM 0.12.1 reports it (the ID is `cbm:p:<id>`).
func (w *world) addADR(id, path, adrID, title, status, statusText string) {
	w.addADRIn("p", id, path, adrID, title, status, statusText)
}

// addADRIn adds a decision node of a CBM project. As the pipeline does, the path of a project other than the
// primary one (`p`) carries the prefix `@<project>/`.
func (w *world) addADRIn(project, id, path, adrID, title, status, statusText string) {
	if project != "p" {
		path = "@" + project + "/" + path
	}

	observed := "cbm:" + project + ":" + id
	w.nodes[observed] = observation.ObservedNode{
		ObservedID: observed, Label: observation.LabelDecision, FilePath: path, StartLine: 1,
		Properties: map[string]string{"adr_id": adrID, "title": title, "status": status, "status_text": statusText},
	}
}

// supersede adds the edge `newID` supersedes `oldID`.
func (w *world) supersede(newID, oldID string) {
	w.edges = append(w.edges, &observation.EvidenceEdge{
		Kind: observation.KindSupersedes, From: "cbm:p:" + newID, To: "cbm:p:" + oldID, Properties: map[string]string{"line": "7"},
	})
}

// input writes the register and builds the input of Evaluate.
func (w *world) input() *conformance.DecisionEvalInput {
	w.t.Helper()

	dir := w.t.TempDir()
	testutil.WriteTree(w.t, dir, map[string]string{"gov/decisions.yaml": registerHeader + w.register})

	caps := observation.Capabilities{Families: map[string]observation.FamilyState{
		"supersedes": {Level: observation.SupportSupported},
	}}
	if w.unsupportedProvider {
		caps = observation.Capabilities{}
	}

	projects := w.projects
	if projects == nil {
		projects = []conformance.DecisionProject{{Git: w.git, Revision: testRev, Root: dir}}
	}

	compiled := compiledPacks()
	compiled.Plans = w.plans

	return &conformance.DecisionEvalInput{
		Workspace: &workspace.Workspace{Root: dir, ADR: &workspace.ADR{
			Files:     []string{"gov/decisions.yaml"},
			Lifecycle: workspace.ADRLifecycle{Active: workspace.DefaultADRActive(), StatusMap: map[string]string{}},
			Triggers:  w.triggers,
		}},
		Model: subjectModel(), Evidence: &observation.EvidenceQueryResult{Nodes: w.nodes, Edges: w.edges},
		Policies: compiled, Changed: w.changed, FailOn: diag.SeverityError, OnUnknown: policy.UnknownFail,
		Capabilities: caps, Projects: projects,
	}
}

// evaluate runs adr.Evaluate on the world.
func (w *world) evaluate() *conformance.DecisionEvaluation {
	w.t.Helper()

	out, err := adr.Evaluate(w.t.Context(), w.input())
	if err != nil {
		w.t.Fatalf("Evaluate: %v", err)
	}

	if out == nil {
		w.t.Fatal("Evaluate gave no result")
	}

	return out
}

func ruleOf(t *testing.T, out *conformance.DecisionEvaluation, id string) *conformance.RuleResult {
	t.Helper()

	for i := range out.Rules {
		if out.Rules[i].RuleID == id {
			return &out.Rules[i]
		}
	}

	t.Fatalf("no rule result %s in %+v", id, out.Rules)

	return nil
}

func findingsOf(out *conformance.DecisionEvaluation, rule string) []conformance.Finding {
	var found []conformance.Finding

	for i := range out.Findings {
		if out.Findings[i].RuleID == rule {
			found = append(found, out.Findings[i])
		}
	}

	return found
}

func wantVerdicts(t *testing.T, out *conformance.DecisionEvaluation, want map[string]conformance.RuleVerdict) {
	t.Helper()

	for id, verdict := range want {
		if got := ruleOf(t, out, id).Verdict; got != verdict {
			t.Errorf("%s = %s, want %s (reasons %+v)", id, got, verdict, ruleOf(t, out, id).Reasons)
		}
	}
}

const (
	adr3Path = "docs/adr/0003-orders-own-db.md"
	adr7Path = "docs/adr/0007-managed-db.md"
)

// a12World is the A12 situation: ADR-0003 (accepted) is superseded by ADR-0007 (accepted), and ADR-0003 is the only
// decision that links the mandatory rule CORE-DB-001. register is the text after the header.
func a12World(t *testing.T, register string) *world {
	t.Helper()

	w := newWorld(t, adr3Path, adr7Path)
	w.addADR("1", adr3Path, "ADR-3", "ADR-0003: Orders own their database", "accepted", "Accepted")
	w.addADR("2", adr7Path, "ADR-7", "ADR-0007: Managed database", "accepted", "Accepted")
	w.supersede("2", "1")
	w.register = register

	return w
}

const a12Before = `  - id: ADR-0003
    governs: [{ref: 'calm:node:orders-db'}]
    enforcement: required
    enforcedBy: [{rule: CORE-DB-001, pack: {id: core, version: 1.0.0}}]
`

// a12After is the register edit that closes the review: ADR-0007 declares the same scope and ADR-0003 is historical.
const a12After = `  - id: ADR-0003
    expect: historical
    governs: [{ref: 'calm:node:orders-db'}]
    enforcedBy: [{rule: CORE-DB-001, pack: {id: core, version: 1.0.0}}]
  - id: ADR-0007
    governs: [{ref: 'calm:node:orders-db'}]
    enforcement: required
    enforcedBy: [{rule: CORE-DB-001, pack: {id: core, version: 1.0.0}}]
`
