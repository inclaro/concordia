package adr

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// engine holds the state of one evaluation: the declarations, the evidence, the Git facts, the history, and the
// evaluated decisions. The obligation checks are pure functions of these (see decision.go and the files of the
// checks); only newEngine reads Git, through the ports of the projects.
type engine struct {
	input     *conformance.DecisionEvalInput
	decls     *Declarations
	facts     *GitFacts
	hist      *History
	decisions []*Decision
	// names maps a decision path to its canonical ID: the register ID of the entry that matched it, else its label.
	names map[string]string
	nodes map[string]*HistoryNode
	class Classifier
	red   redactor
	ev    DecisionEvidence
}

// Evaluate is conformance.Deps.EvaluateDecisions: the ADR governance checks of a run (design D6). It reads Git
// through the ports of the projects (revision, tracked files, dirty files, and the architecture file of the base
// for triggers), reads the register and the model, and writes nothing. ADR text is data: nothing is executed or
// fetched, and the only readers are the Git ports.
//
//   - no register and no selected governance rule: nil result, no diagnostic;
//   - a register and no selected governance rule: only the informational CO-ADR-007 (no rules, no summary);
//   - no register and a selected governance rule: each rule is `not-applicable` with the reason "no register", no
//     summary (INT-05);
//   - otherwise the register is loaded and validated (a *diag.Error with exit code 2 stops the run) and one rule
//     result is produced per selected governance rule, with the findings and the decision summary.
func Evaluate(ctx context.Context, input *conformance.DecisionEvalInput) (*conformance.DecisionEvaluation, error) {
	plans := governancePlans(input.Policies)

	if res, done := evaluateWithoutRun(input, plans); done {
		return res, nil
	}

	input, primary := keyPrimary(input)

	decls, err := LoadDeclarations(input.Workspace, input.Model, input.Policies)
	if err != nil {
		return nil, err
	}

	scopePrimary(decls.Register, primary)

	eng, err := newEngine(ctx, input, decls)
	if err != nil {
		return nil, err
	}

	return eng.run(ctx, plans)
}

// evaluateWithoutRun gives the result of a run that needs no register evaluation: nil when there is neither a
// register nor a governance rule, only CO-ADR-007 when there is a register and no rule, and the "no register"
// results when there is a rule and no register. done is false when the register must be evaluated.
func evaluateWithoutRun(input *conformance.DecisionEvalInput, plans []policy.RulePlan) (res *conformance.DecisionEvaluation, done bool) {
	configured := input.Workspace != nil && input.Workspace.ADR != nil && len(input.Workspace.ADR.Files) > 0

	switch {
	case !configured && len(plans) == 0:
		return nil, true
	case len(plans) == 0:
		return &conformance.DecisionEvaluation{
			Rules: []conformance.RuleResult{}, Findings: []conformance.Finding{}, Inputs: []conformance.InputRef{},
			Diagnostics: []diag.Diagnostic{diag.New(diag.ADR007, nil)}, Redactions: nil,
		}, true
	case !configured:
		return withoutRegister(plans), true
	}

	return nil, false
}

// keyPrimary gives a copy of the input in which the primary project (the one with no path prefix) has the name "":
// the evidence, the Git facts, and the register scopes key the primary project by "". It also gives the workspace
// name of the primary project ("" when no project is primary).
func keyPrimary(input *conformance.DecisionEvalInput) (keyed *conformance.DecisionEvalInput, primary string) {
	copied := *input
	copied.Projects = slices.Clone(input.Projects)

	for i := range copied.Projects {
		if copied.Projects[i].Prefix == "" {
			primary = copied.Projects[i].Name
			copied.Projects[i].Name = ""
		}
	}

	return &copied, primary
}

// scopePrimary marks the register entries whose `project` names the primary project: their scope has the
// evidence key "" (EvidenceScope). Project itself is kept as written, because it is part of the identity of the
// entry (EntryKey, the subjects, the fingerprints), and the entry stays scoped: it does not match other projects.
func scopePrimary(reg *Register, primary string) {
	if primary == "" {
		return
	}

	for i := range reg.Entries {
		reg.Entries[i].PrimaryScope = reg.Entries[i].Project == primary
	}
}

// governancePlans gives the selected plans of the `adr-governance` rules, sorted by rule ID.
func governancePlans(compiled *policy.CompileResult) []policy.RulePlan {
	if compiled == nil {
		return nil
	}

	var out []policy.RulePlan

	for i := range compiled.Plans {
		if compiled.Plans[i].Predicate.Family == policy.FamilyDecisionGovernance {
			out = append(out, compiled.Plans[i])
		}
	}

	slices.SortStableFunc(out, func(a, b policy.RulePlan) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) })

	return out
}

// withoutRegister gives the result of a run with the governance pack and no register: every rule is
// `not-applicable` with the reason "no register", and there is no summary.
func withoutRegister(plans []policy.RulePlan) *conformance.DecisionEvaluation {
	out := &conformance.DecisionEvaluation{
		Rules: make([]conformance.RuleResult, 0, len(plans)), Findings: []conformance.Finding{}, Inputs: []conformance.InputRef{},
		Diagnostics: []diag.Diagnostic{},
	}

	for i := range plans {
		res, _ := assemble(&plans[i], nil, reasonNoRegister, "no register")
		out.Rules = append(out.Rules, res)
	}

	return out
}

// newEngine builds the evidence, reads the Git facts through the ports, and evaluates presence, the history, and
// the applicability of every registered decision.
func newEngine(ctx context.Context, input *conformance.DecisionEvalInput, decls *Declarations) (*engine, error) {
	eng := &engine{
		input: input, decls: decls, ev: BuildEvidence(input.Evidence, input.Capabilities, input.Projects),
		names: map[string]string{}, nodes: map[string]*HistoryNode{},
		class: Classifier{Plans: input.Policies.Plans, FailOn: input.FailOn},
	}

	for i := range input.Coverage {
		eng.ev.ApplyCoverage(&input.Coverage[i])
	}

	facts, err := gitFacts(ctx, input.Projects, eng.wantedPaths())
	if err != nil {
		return nil, err
	}

	eng.facts = facts
	eng.buildDecisions()

	return eng, nil
}

// wantedPaths are the paths that the Git facts must cover: the evidence decisions and the path pins. The facts of
// every other file are never read, so the memory of a run does not grow with the size of the repository.
func (e *engine) wantedPaths() map[string]bool {
	wanted := map[string]bool{}

	for i := range e.ev.Decisions {
		wanted[e.ev.Decisions[i].Path] = true
	}

	for i := range e.decls.Register.Entries {
		if pin := pinPath(&e.decls.Register.Entries[i]); pin != "" {
			wanted[pin] = true
		}
	}

	return wanted
}

// gitFacts reads the revision, the tracked files, and the dirty files of each project through its Git port, with
// the project prefix applied. Only the wanted paths are kept.
func gitFacts(ctx context.Context, projects []conformance.DecisionProject, wanted map[string]bool) (*GitFacts, error) {
	facts := &GitFacts{Revisions: map[string]string{}, Tracked: map[string]bool{}, Dirty: map[string]bool{}}

	for i := range projects {
		proj := &projects[i]
		facts.Revisions[proj.Name] = proj.Revision

		if proj.Revision == "" || proj.Git == nil {
			continue
		}

		if err := addProjectGitFacts(ctx, facts, proj, wanted); err != nil {
			return nil, err
		}
	}

	return facts, nil
}

// addProjectGitFacts adds the wanted tracked and dirty files of one project to the facts.
func addProjectGitFacts(ctx context.Context, facts *GitFacts, proj *conformance.DecisionProject, wanted map[string]bool) error {
	tracked, err := proj.Git.ListFilesAt(ctx, proj.Revision)
	if err != nil {
		return fmt.Errorf("adr: list files of %s at %s: %w", projectLabel(proj.Name), proj.Revision, err)
	}

	markWanted(facts.Tracked, proj.Prefix, tracked, wanted)

	dirty, err := proj.Git.DirtyFiles(ctx)
	if err != nil {
		return fmt.Errorf("adr: list dirty files of %s: %w", projectLabel(proj.Name), err)
	}

	markWanted(facts.Dirty, proj.Prefix, dirty, wanted)

	return nil
}

// markWanted sets the prefixed files that are wanted in the set.
func markWanted(set map[string]bool, prefix string, files []string, wanted map[string]bool) {
	for _, file := range files {
		if path := prefix + file; wanted[path] {
			set[path] = true
		}
	}
}

func projectLabel(name string) string {
	if name == "" {
		return "the primary project"
	}

	return "project " + name
}

// committed says whether the ADR file is a committed decision: tracked in the evaluated revision, not dirty, and
// not stale in the index (design D3).
func (e *engine) committed(path string) bool {
	return e.facts.Tracked[path] && !e.facts.Dirty[path] && !slices.Contains(e.ev.Stale, path)
}

// buildDecisions evaluates presence, builds the history of the components that hold a registered decision, and
// derives the applicability of every entry.
func (e *engine) buildDecisions() {
	reg := e.decls.Register
	presence := EvaluatePresence(reg.Entries, &e.ev, e.facts)

	hev := e.historyEvidence()
	e.hist = BuildHistory(&hev, e.decls.Lifecycle, e.committed, registeredPaths(reg.Entries, presence))

	for i := range e.hist.Nodes {
		e.nodes[e.hist.Nodes[i].Path] = &e.hist.Nodes[i]
		e.names[e.hist.Nodes[i].Path] = e.hist.Nodes[i].Label
	}

	for i := range reg.Entries {
		e.decisions = append(e.decisions, e.decide(&reg.Entries[i], presence))
	}

	for _, dec := range e.decisions {
		if dec.Presence.State == PresenceCommitted {
			dec.Successors = e.neighbors(dec.Presence.Path, true)
			dec.Predecessors = e.neighbors(dec.Presence.Path, false)
		}
	}
}

// registeredPaths gives the paths of the decisions that the register entries matched, with the other paths of a
// duplicate ID.
func registeredPaths(entries []Entry, presence map[string]PresenceResult) []string {
	registered := make([]string, 0, len(entries))

	for i := range entries {
		pres := presence[EntryKey(&entries[i])]
		registered = append(registered, matchedPaths(&pres)...)
	}

	return registered
}

// decide derives the applicability of one entry from its presence and the history, and names the decision path of
// a committed entry by the register ID.
func (e *engine) decide(entry *Entry, presence map[string]PresenceResult) *Decision {
	pres := presence[EntryKey(entry)]
	state, reason, lags := EvaluateApplicability(entry, pres, e.hist, e.decls.Lifecycle)

	dec := &Decision{
		Entry: entry, Subjects: sortedUnique(e.decls.Subjects[EntryKey(entry)]), Presence: pres,
		Applicability: state, Reason: reason, StatusLags: lags,
	}

	if pres.State == PresenceCommitted {
		e.names[pres.Path] = entry.ID
		if node := e.nodes[pres.Path]; node != nil {
			dec.Mapped = node.Mapped
		}
	}

	return dec
}

// historyEvidence gives the evidence that the history reads. The same ID in two projects is a duplicate in one
// history, unless the register scopes the decision to a project: a decision whose label is registered only with
// project pins, none of them its own project, is left out, so a scoped entry sees only the decisions of its project
// (design D14). A decision that a path pin selects always stays in the history.
func (e *engine) historyEvidence() DecisionEvidence {
	scopes := scanEvidenceScopes(e.decls.Register.Entries)
	left := map[string]bool{}
	hev := e.ev
	hev.Decisions = slices.DeleteFunc(slices.Clone(e.ev.Decisions), func(d EvidenceDecision) bool {
		drop := scopes.leavesOut(&d)
		left[d.Path] = drop

		return drop
	})

	if len(hev.Decisions) != len(e.ev.Decisions) {
		hev.Supersedes = slices.DeleteFunc(slices.Clone(e.ev.Supersedes), func(edge SupersedesEdge) bool {
			return left[edge.From] || left[edge.To]
		})
	}

	return hev
}

// evidenceScopes are the register facts that decide which evidence decisions the history leaves out.
type evidenceScopes struct {
	// pinned maps a decision label to the projects that the register entries of that label are scoped to.
	pinned map[string]map[string]bool
	// pinnedPaths are the paths that a path pin selects.
	pinnedPaths map[string]bool
	// unscoped are the labels with at least one unscoped register entry.
	unscoped map[string]bool
}

// scanEvidenceScopes reads the project pins and the path pins of the register entries.
func scanEvidenceScopes(entries []Entry) evidenceScopes {
	scopes := evidenceScopes{
		pinned: map[string]map[string]bool{}, pinnedPaths: map[string]bool{}, unscoped: map[string]bool{},
	}

	for i := range entries {
		entry := &entries[i]
		if pin := pinPath(entry); pin != "" {
			scopes.pinnedPaths[pin] = true
		}

		project, scoped := entry.EvidenceScope()
		if !scoped {
			scopes.unscoped[entry.ID] = true

			continue
		}

		if scopes.pinned[entry.ID] == nil {
			scopes.pinned[entry.ID] = map[string]bool{}
		}

		scopes.pinned[entry.ID][project] = true
	}

	return scopes
}

// leavesOut says whether the history leaves the decision out: it is not selected by a path pin, and its label is
// registered only with project pins, none of them its own project.
func (s evidenceScopes) leavesOut(d *EvidenceDecision) bool {
	if s.pinnedPaths[d.Path] {
		return false
	}

	projects := s.pinned[d.Label]
	if len(projects) == 0 || s.unscoped[d.Label] {
		return false
	}

	return !projects[d.Project]
}

// neighbors gives the successors (or predecessors) of a decision path with their canonical IDs, sorted by path.
func (e *engine) neighbors(path string, successors bool) []Neighbor {
	out := []Neighbor{}

	for _, link := range e.hist.Links {
		switch {
		case successors && link.ToPath == path:
			out = append(out, Neighbor{Path: link.FromPath, ID: e.nameOf(link.FromPath), Effective: link.Effect == EffectEffective})
		case !successors && link.FromPath == path:
			out = append(out, Neighbor{Path: link.ToPath, ID: e.nameOf(link.ToPath), Effective: link.Effect == EffectEffective})
		}
	}

	slices.SortFunc(out, func(a, b Neighbor) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	return out
}

// nameOf gives the canonical ID of a decision path; the path itself when no decision is known there.
func (e *engine) nameOf(path string) string {
	if name, ok := e.names[path]; ok {
		return name
	}

	return path
}

// leadProject is the project that holds the architecture file: the primary project, else the first.
func (e *engine) leadProject() (*conformance.DecisionProject, error) {
	for i := range e.input.Projects {
		if e.input.Projects[i].Prefix == "" {
			return &e.input.Projects[i], nil
		}
	}

	if len(e.input.Projects) > 0 {
		return &e.input.Projects[0], nil
	}

	return nil, errors.New("no project")
}

// evaluateTriggers runs the configured triggers. The base model is read through the Git port only when a trigger
// lists an architecture change and the run has a change set.
func (e *engine) evaluateTriggers(ctx context.Context) []conformance.TriggerResult {
	change, base, baseErr := e.changeAndBase(ctx)
	paths := map[string]string{}

	for _, dec := range e.decisions {
		if dec.Presence.State == PresenceCommitted {
			paths[EntryKey(dec.Entry)] = dec.Presence.Path
		}
	}

	return EvaluateTriggers(e.decls.Triggers, change, base, baseErr, e.input.Model, e.decls.Register, paths)
}

// changeAndBase gives the change set of the run, sorted, and the base model with its read error. The base model is
// read only when a trigger lists an architecture change; without a change set there is neither.
func (e *engine) changeAndBase(ctx context.Context) (*ChangeSet, *intent.ArchitectureModel, error) {
	if e.input.Changed == nil {
		return nil, nil, nil
	}

	files := slices.Clone(e.input.Changed.Files)
	slices.SortFunc(files, canonjson.CompareUTF16)
	change := &ChangeSet{Base: e.input.Changed.Base, Files: files}

	if !slices.ContainsFunc(e.decls.Triggers, func(t workspace.ADRTrigger) bool { return len(t.Architecture) > 0 }) {
		return change, nil, nil
	}

	base, err := e.baseModel(ctx)

	return change, base, err
}

// baseModel reads the architecture model at the change base through the Git port of the lead project.
func (e *engine) baseModel(ctx context.Context) (*intent.ArchitectureModel, error) {
	proj, err := e.leadProject()
	if err != nil {
		return nil, err
	}

	ref := cmp.Or(proj.ChangedBase, e.input.Changed.Base)

	return LoadBaseModel(ctx, e.input.Workspace, proj.Git, ref)
}

// run produces the rule results, the findings, the summary, and the diagnostics.
func (e *engine) run(ctx context.Context, plans []policy.RulePlan) (*conformance.DecisionEvaluation, error) {
	triggers := e.evaluateTriggers(ctx)
	candidates := e.candidates()

	out := &conformance.DecisionEvaluation{
		Rules: make([]conformance.RuleResult, 0, len(plans)), Findings: []conformance.Finding{},
		Inputs: slices.Clone(e.decls.Inputs),
	}

	for i := range plans {
		plan := &plans[i]

		res, found := e.ruleOf(plan, triggers)
		out.Rules = append(out.Rules, res)
		out.Findings = append(out.Findings, found...)
	}

	out.Summary = e.summary(triggers, candidates, out.Findings)
	out.Diagnostics = e.diagnostics(candidates)
	out.Redactions = e.red.sorted()

	return out, nil
}

// candidates collects the non-authoritative candidates from the committed decisions only: a decision that is not
// tracked, or edited, is not a candidate of anything.
func (e *engine) candidates() []conformance.DecisionCandidate {
	cev := e.ev
	cev.Decisions = slices.DeleteFunc(slices.Clone(e.ev.Decisions), func(d EvidenceDecision) bool { return !e.committed(d.Path) })

	matched := map[string]bool{}

	for _, dec := range e.decisions {
		for _, p := range matchedPaths(&dec.Presence) {
			matched[p] = true
		}
	}

	return CollectCandidates(&cev, e.decls.Register, e.input.Model, matched)
}

// matchedPaths gives the decision paths that the presence result matched: the path of the matched decision, and
// the other paths of a duplicate ID.
func matchedPaths(pres *PresenceResult) []string {
	var out []string

	if pres.Decision != nil {
		out = append(out, pres.Path)
	}

	if pres.Reason == string(ReasonDuplicateID) {
		out = append(out, pres.OtherPaths...)
	}

	return out
}

// diagnostics gives the non-blocking CO-ADR diagnostics: the provider capability or the revision unavailable
// (warning), the candidates (info, one summary), and the triggers with no change set (info).
func (e *engine) diagnostics(candidates []conformance.DecisionCandidate) []diag.Diagnostic {
	out := []diag.Diagnostic{}

	if !e.ev.Supported {
		out = append(out, diag.New(diag.ADR006, diag.Detail(
			"the provider capability profile has no ADR evidence; presence, applicability, and history are unknown")))
	}

	var missing []string

	for i := range e.input.Projects {
		if e.input.Projects[i].Revision == "" {
			missing = append(missing, projectLabel(e.input.Projects[i].Name))
		}
	}

	if len(missing) > 0 {
		out = append(out, diag.New(diag.ADR006, diag.Detail("no git revision for "+strings.Join(missing, ", ")+
			"; presence is unknown")))
	}

	if text := candidateText(candidates); text != "" {
		out = append(out, diag.New(diag.ADR008, diag.Detail(text)))
	}

	if len(e.decls.Triggers) > 0 && e.input.Changed == nil {
		out = append(out, diag.New(diag.ADR009, diag.Detail(
			fmt.Sprintf("%d trigger(s) are not evaluated; run `check --changed` to evaluate them", len(e.decls.Triggers)))))
	}

	return diag.Sort(out)
}

// maxCandidateIDs bounds the IDs that the candidate diagnostic names.
const maxCandidateIDs = 5

// candidateText summarizes the candidates: the counts by kind and the first unregistered decisions. "" when there
// is none.
func candidateText(candidates []conformance.DecisionCandidate) string {
	if len(candidates) == 0 {
		return ""
	}

	counts, unregistered := countCandidates(candidates)

	text := fmt.Sprintf("%d unregistered decision(s), %d mention(s), %d CALM adrs entr(ies); they change no result",
		counts[CandidateUnregistered], counts[CandidateMention], counts[CandidateCalmADRs])

	if len(unregistered) > 0 {
		shown := unregistered[:min(len(unregistered), maxCandidateIDs)]
		text += ": " + strings.Join(shown, ", ")

		if len(unregistered) > len(shown) {
			text += ", ..."
		}
	}

	return text
}

// countCandidates gives the number of candidates by kind and the names of the unregistered decisions.
func countCandidates(candidates []conformance.DecisionCandidate) (counts map[string]int, unregistered []string) {
	counts = map[string]int{}

	for i := range candidates {
		counts[candidates[i].Kind]++

		if candidates[i].Kind == CandidateUnregistered {
			unregistered = append(unregistered, cmp.Or(candidates[i].ID, candidates[i].Path))
		}
	}

	return counts, unregistered
}
