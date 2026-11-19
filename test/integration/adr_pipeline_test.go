package integration_test

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// adrScenario is one `check` run on a copy of the decision fixture (test/fixtures/adr) against its recording. The
// register is replaced by Register; the fake Git decides which ADR files are tracked and which are dirty.
type adrScenario struct {
	// Register is the content of governance/decisions.yaml.
	Register string
	// Waivers is the content of a waiver file, or "" for none.
	Waivers string
	// Unknown is `--unknown`; "" keeps the workspace value (fail).
	Unknown policy.UnknownDisposition
	// Untracked lists fixture files that exist in the working tree and the index but not in the revision.
	Untracked []string
	// WithoutGovernancePack removes `builtin:adr-governance` from the workspace packs; the register stays.
	WithoutGovernancePack bool
	// Rollback adds an active `check.rollback` to the workspace.
	Rollback bool
}

// run runs `check` and gives the run.
func (s *adrScenario) run(t *testing.T) *conformance.Run {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(adrFixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "governance", "decisions.yaml"), []byte(s.Register), 0o600); err != nil {
		t.Fatal(err)
	}

	s.editWorkspace(t, dir)

	waivers := s.writeWaivers(t, dir)
	tree := adrTrackedTree(t, dir, s.Untracked)

	client, err := observation.LoadReplayClient(adrRecordingFile)
	if err != nil {
		t.Fatalf("load the recording: %v", err)
	}

	run, err := conformance.RunCheck(t.Context(), s.checkOptions(dir, waivers, client, tree.git(client.HeadSHA())))
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	return run
}

// editWorkspace removes the governance pack and adds the rollback of the workspace file when the scenario says so.
func (s *adrScenario) editWorkspace(t *testing.T, dir string) {
	t.Helper()

	if !s.WithoutGovernancePack && !s.Rollback {
		return
	}

	wsPath := filepath.Join(dir, workspaceFile)

	raw, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(wsPath, []byte(s.workspaceText(string(raw))), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *adrScenario) workspaceText(text string) string {
	if s.WithoutGovernancePack {
		text = strings.Replace(text, "    - use: builtin:adr-governance\n", "", 1)
	}

	if s.Rollback {
		text = strings.Replace(text, "check:\n", "check:\n  rollback:\n    reason: register migration\n"+
			"    authorizedBy: jane.doe\n    expires: \"2099-01-01\"\n", 1)
	}

	return text
}

// writeWaivers writes the waiver file of the scenario and gives its path, or "" when the scenario has none.
func (s *adrScenario) writeWaivers(t *testing.T, dir string) string {
	t.Helper()

	if s.Waivers == "" {
		return ""
	}

	waivers := filepath.Join(dir, "waivers.yaml")
	if err := os.WriteFile(waivers, []byte(s.Waivers), 0o600); err != nil {
		t.Fatal(err)
	}

	return waivers
}

func (s *adrScenario) checkOptions(dir, waivers string, client observation.Client, git workspace.GitPort) *conformance.CheckOptions {
	return &conformance.CheckOptions{
		Env: conformance.Env{
			Logger: logger.New(logger.Options{Out: io.Discard, RunID: "run", Quiet: true}), RunID: runID,
			WorkspacePath: filepath.Join(dir, workspaceFile), Cwd: dir, ToolVersion: "test",
		},
		Deps: conformance.Deps{
			CreateClient:      func(*workspace.Workspace, *logger.Logger) observation.Client { return client },
			CreateGit:         func(string) workspace.GitPort { return git },
			EvaluateDecisions: adr.Evaluate,
		},
		Waivers:     waivers,
		Unknown:     s.Unknown,
		SkipReports: true,
	}
}

// adrTree is the tracked files of the fixture copy and their text.
type adrTree struct {
	content map[string]string
	files   []string
}

func (tree *adrTree) git(rev string) *workspace.FakeGit {
	return &workspace.FakeGit{Rev: rev, Files: tree.files, FilesAt: map[string]map[string]string{rev: tree.content}}
}

// adrIsTrackedFile says whether the file of the copy is in the revision: not a directory, not the waiver file, and not
// one of the untracked files.
func adrIsTrackedFile(entry fs.DirEntry, p string, untracked []string) bool {
	if entry.IsDir() || p == "waivers.yaml" {
		return false
	}

	return !slices.Contains(untracked, p)
}

// adrTrackedTree lists the files of the copy that the fake Git tracks.
func adrTrackedTree(t *testing.T, dir string, untracked []string) *adrTree {
	t.Helper()

	tree := &adrTree{content: map[string]string{}}

	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || !adrIsTrackedFile(entry, p, untracked) {
			return walkErr
		}

		data, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		tree.files = append(tree.files, p)
		tree.content[p] = string(data)

		return readErr
	})
	if err != nil {
		t.Fatalf("list the fixture: %v", err)
	}

	return tree
}

// waiversFor writes a waiver file that waives every live finding of the rule.
func waiversFor(run *conformance.Run, ruleID string) string {
	var b strings.Builder

	b.WriteString("waivers:\n")

	for i := range run.Findings {
		f := &run.Findings[i]
		if f.RuleID != ruleID {
			continue
		}

		b.WriteString("  - id: W" + string(rune('A'+i)) + "\n    finding: " + f.Fingerprint + "\n    rule: " + ruleID +
			"\n    owner: team-orders\n    reason: tracked in the decision review\n    expires: 2099-12-31\n")
	}

	return b.String()
}

func decisionOf(t *testing.T, run *conformance.Run, id string) conformance.DecisionEntry {
	t.Helper()

	if run.DecisionSummary == nil {
		t.Fatal("no decision summary")
	}

	for i := range run.DecisionSummary.Decisions {
		if run.DecisionSummary.Decisions[i].ID == id {
			return run.DecisionSummary.Decisions[i]
		}
	}

	t.Fatalf("decision %s not in %+v", id, run.DecisionSummary.Decisions)

	return conformance.DecisionEntry{}
}

const (
	registerHeader = "apiVersion: concordia.io/v1alpha1\nkind: DecisionRegister\ndecisions:\n"

	// a12Before is the register of the fixture: ADR-0003 alone.
	a12Before = registerHeader + `  - id: ADR-0003
    path: docs/adr/0003-orders-own-database.md
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
      - ref: calm:interface:orders-db/sql
      - select: {tags: [orders-data]}
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
`

	// a12After is the register edit of a person who reviews the replacement: ADR-0007 declares the scope of ADR-0003
	// and ADR-0003 is historical.
	a12After = registerHeader + `  - id: ADR-0003
    path: docs/adr/0003-orders-own-database.md
    expect: historical
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
      - ref: calm:interface:orders-db/sql
      - select: {tags: [orders-data]}
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
  - id: ADR-0007
    path: docs/adr/0007-orders-managed-database.md
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
      - ref: calm:interface:orders-db/sql
      - select: {tags: [orders-data]}
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
`
)

// Scenario: Pack enabled.
// Scenario: Finding for a superseded decision.
// TestADRPipelineA12SupersededDecisionIsTheOnlyJustification covers the spec scenarios "A12 superseded decision is
// the only justification", "Successor is not in the register" and "Successor declares the same scope" through
// `check`: the replacement gives exit 1 and no scope moves to ADR-0007; a register edit that transfers the scope
// gives exit 0.
func TestADRPipelineA12SupersededDecisionIsTheOnlyJustification(t *testing.T) {
	t.Parallel()

	run := (&adrScenario{Register: a12Before}).run(t)

	if run.Summary.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", run.Summary.ExitCode)
	}

	verdicts := verdictsOf(run)
	for rule, want := range map[string]conformance.RuleVerdict{"GOV-ADR-001": "pass", "GOV-ADR-002": "violation", "GOV-ADR-003": "violation"} {
		if verdicts[rule] != want {
			t.Errorf("%s = %q, want %q (all: %v)", rule, verdicts[rule], want, verdicts)
		}
	}

	adrRequireCandidateOnly(t, run)
	adrRequireCandidateSuccessors(t, run)

	after := (&adrScenario{Register: a12After}).run(t)
	if after.Summary.ExitCode != 0 {
		t.Fatalf("after the register edit: exit code = %d, want 0 (verdicts %v, findings %d)", after.Summary.ExitCode, verdictsOf(after), len(after.Findings))
	}

	if v := verdictsOf(after)["GOV-ADR-003"]; v != "pass" {
		t.Errorf("after the register edit GOV-ADR-003 = %q, want pass", v)
	}

	if hasCandidateAt(after, "0007-orders-managed-database.md") {
		t.Errorf("candidates = %+v, want no candidate for ADR-0007 after the register edit", after.DecisionSummary.Candidates)
	}
}

// adrRequireCandidateOnly checks that ADR-0007 is a candidate only: it governs and enforces nothing.
func adrRequireCandidateOnly(t *testing.T, run *conformance.Run) {
	t.Helper()

	summary := run.DecisionSummary

	if len(summary.Decisions) != 1 || summary.Decisions[0].ID != "ADR-3" {
		t.Fatalf("decisions = %+v, want ADR-0003 only", summary.Decisions)
	}

	if !hasCandidateAt(run, "0007-orders-managed-database.md") {
		t.Errorf("candidates = %+v, want ADR-0007 as a candidate", summary.Candidates)
	}

	for _, j := range summary.RuleJustification {
		if slices.Contains(j.LinkedBy, "ADR-0007") || slices.Contains(j.LinkedBy, "ADR-7") {
			t.Errorf("ADR-0007 justifies %s", j.Rule)
		}
	}
}

// adrIsUnappliedSuccessor says whether the decision of a finding names ADR-0007 as the one candidate successor, not
// applied.
func adrIsUnappliedSuccessor(f *conformance.Finding) bool {
	if f.Decision == nil || len(f.Decision.CandidateSuccessors) != 1 {
		return false
	}

	successor := f.Decision.CandidateSuccessors[0]

	return !successor.Applied && strings.Contains(successor.Path, "0007")
}

func adrRequireCandidateSuccessors(t *testing.T, run *conformance.Run) {
	t.Helper()

	if len(findingsOf(run, "GOV-ADR-002")) == 0 || len(findingsOf(run, "GOV-ADR-003")) == 0 {
		t.Fatalf("findings = %+v", run.Findings)
	}

	for _, rule := range []string{"GOV-ADR-002", "GOV-ADR-003"} {
		found := findingsOf(run, rule)
		for i := range found {
			if !adrIsUnappliedSuccessor(&found[i]) {
				t.Errorf("%s decision = %+v, want the candidate successor ADR-0007 with applied false", rule, found[i].Decision)
			}
		}
	}
}

// Scenario: Rollback covers decision-governance rules.
// The A12 violations of the real fixture run as shadow under a rollback: the exit code is 0, the findings and
// their verdicts are kept, and the run records the rollback.
func TestADRPipelineRollbackRunsTheGovernanceRulesAsShadow(t *testing.T) {
	t.Parallel()

	run := (&adrScenario{Register: a12Before, Rollback: true}).run(t)

	if run.Summary.ExitCode != 0 || run.Summary.Rollback == nil {
		t.Fatalf("exit code = %d, rollback = %+v, want 0 and a recorded rollback", run.Summary.ExitCode, run.Summary.Rollback)
	}

	verdicts := verdictsOf(run)
	if verdicts["GOV-ADR-002"] != conformance.VerdictViolation || verdicts["GOV-ADR-003"] != conformance.VerdictViolation {
		t.Errorf("verdicts = %v, want the A12 violations kept", verdicts)
	}

	governance := 0

	for i := range run.Findings {
		if run.Findings[i].Area == conformance.AreaADRGovernance {
			governance++

			adrRequireShadow(t, &run.Findings[i])
		}
	}

	if governance == 0 {
		t.Fatal("the governance findings are kept under a rollback")
	}
}

func adrRequireShadow(t *testing.T, f *conformance.Finding) {
	t.Helper()

	if f.Enforcement != policy.EnforcementShadow {
		t.Errorf("finding %s has enforcement %q, want shadow", f.RuleID, f.Enforcement)
	}
}

// hasCandidateAt says whether the run lists an unregistered-decision candidate whose path ends with the suffix.
func hasCandidateAt(run *conformance.Run, suffix string) bool {
	return slices.ContainsFunc(run.DecisionSummary.Candidates, func(c conformance.DecisionCandidate) bool {
		return c.Kind == adr.CandidateUnregistered && strings.HasSuffix(c.Path, suffix)
	})
}

// variantRegister registers one variant decision by ID and path pin, governing the database of the fixture, with
// no enforcement rule.
func variantRegister(entries ...[2]string) string {
	var b strings.Builder

	b.WriteString(registerHeader)

	for _, e := range entries {
		b.WriteString("  - id: " + e[0] + "\n    path: " + e[1] + "\n    governs:\n      - ref: calm:node:orders-db\n    enforcement: none\n")
	}

	return b.String()
}

// Scenario: Ambiguity blocks by default.
// TestADRPipelineA13HistoryFindings covers "A13 cycle", "A13 missing target" and "A13 contradictory metadata": each
// gives a GOV-ADR-005 history finding and the applicability `unknown` (ambiguous). The run exits 1 while the
// finding is live; once a waiver covers it, the unknown applicability result under `--unknown fail` gives exit 4.
func TestADRPipelineA13HistoryFindings(t *testing.T) {
	t.Parallel()

	for _, tc := range adrHistoryCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := (&adrScenario{Register: tc.register}).run(t)

			history := findingsOf(live, "GOV-ADR-005")
			if len(history) == 0 {
				t.Fatalf("no history finding: verdicts %v findings %+v", verdictsOf(live), live.Findings)
			}

			if live.Summary.ExitCode != 1 {
				t.Errorf("live finding: exit code = %d, want 1", live.Summary.ExitCode)
			}

			for i := range history {
				adrRequireHistoryFinding(t, &tc, &history[i])
			}

			if v := verdictsOf(live)["GOV-ADR-002"]; v != conformance.VerdictUnknown {
				t.Errorf("GOV-ADR-002 = %q, want unknown", v)
			}

			for _, id := range tc.ambiguous {
				entry := decisionOf(t, live, id)
				if entry.Applicability.State != string(adr.ApplicabilityAmbiguous) {
					t.Errorf("%s applicability = %+v, want ambiguous", id, entry.Applicability)
				}
			}

			adrRequireWaivedHistoryExit(t, &tc, live)
		})
	}
}

// adrHistoryCase is a register with a history problem of a decision (A13).
type adrHistoryCase struct {
	name, reason string
	register     string
	ambiguous    []string
	// names are what the message of the history finding names: the decisions by their register IDs and the missing
	// value.
	names []string
}

var adrHistoryCases = []adrHistoryCase{
	{
		name: "A13 cycle", reason: "cycle", names: []string{"ADR-501", "ADR-502"}, ambiguous: []string{"ADR-501", "ADR-502"},
		register: variantRegister(
			[2]string{"ADR-0501", "docs/adr/variants/v05/0501-log-a.md"},
			[2]string{"ADR-0502", "docs/adr/variants/v05/0502-log-b.md"}),
	},
	{
		name: "A13 missing target", reason: "missing-target", names: []string{"ADR-701", "0799-tracing-gone.md"}, ambiguous: []string{"ADR-701"},
		register: variantRegister([2]string{"ADR-0701", "docs/adr/variants/v07/0701-tracing-old.md"}),
	},
	{
		name: "A13 contradictory metadata", reason: "contradictory-status", names: []string{"ADR-801"}, ambiguous: []string{"ADR-801"},
		register: variantRegister([2]string{"ADR-0801", "docs/adr/variants/v08/0801-batch-old.md"}),
	},
}

// adrRequireHistoryFinding checks the kind of a history finding and that its message names the decisions and the value.
func adrRequireHistoryFinding(t *testing.T, tc *adrHistoryCase, f *conformance.Finding) {
	t.Helper()

	if f.Decision == nil || f.Kind != conformance.FindingKind("adr-"+tc.reason) {
		t.Errorf("history finding kind = %q, want adr-%s", f.Kind, tc.reason)
	}

	for _, name := range tc.names {
		if !strings.Contains(f.Message, name) {
			t.Errorf("history finding message %q does not name %q", f.Message, name)
		}
	}
}

// adrRequireWaivedHistoryExit waives the history finding of the live run: the unknown applicability result is left, and
// it gives exit 4 with `--unknown fail`.
func adrRequireWaivedHistoryExit(t *testing.T, tc *adrHistoryCase, live *conformance.Run) {
	t.Helper()

	waived := (&adrScenario{Register: tc.register, Waivers: waiversFor(live, "GOV-ADR-005"), Unknown: policy.UnknownFail}).run(t)

	history := findingsOf(waived, "GOV-ADR-005")
	for i := range history {
		if history[i].Disposition != exceptions.DispositionWaived {
			t.Errorf("history finding disposition = %q, want waived", history[i].Disposition)
		}
	}

	if waived.Summary.ExitCode != 4 {
		t.Errorf("waived finding: exit code = %d, want 4 (verdicts %v)", waived.Summary.ExitCode, verdictsOf(waived))
	}
}

// Scenario: Not committed but active text.
// TestADRPipelineA14IndexOnlyADR covers the scenario "Index-only ADR": the provider reports ADR-0003, but its file
// is not in the evaluated revision. The presence result is a violation with reason index-only, the facts of the
// uncommitted text are not used, so the applicability is unknown.
func TestADRPipelineA14IndexOnlyADR(t *testing.T) {
	t.Parallel()

	run := (&adrScenario{Register: a12Before, Untracked: []string{"docs/adr/0003-orders-own-database.md"}}).run(t)

	entry := decisionOf(t, run, "ADR-3")
	if entry.Presence.State != string(adr.PresenceIndexOnly) || entry.Presence.Reason != string(adr.ReasonIndexOnly) {
		t.Errorf("presence = %+v, want index-only with reason index-only", entry.Presence)
	}

	if entry.Applicability.State != string(adr.ApplicabilityUnknown) {
		t.Errorf("applicability = %+v, want unknown", entry.Applicability)
	}

	if v := verdictsOf(run)["GOV-ADR-001"]; v != conformance.VerdictViolation {
		t.Errorf("GOV-ADR-001 = %q, want violation", v)
	}

	findings := findingsOf(run, "GOV-ADR-001")
	if len(findings) != 1 || !adrIsIndexOnlyFinding(&findings[0]) {
		t.Errorf("GOV-ADR-001 findings = %+v, want one adr-index-only finding", findings)
	}

	if run.Summary.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", run.Summary.ExitCode)
	}
}

func adrIsIndexOnlyFinding(f *conformance.Finding) bool {
	return f.Kind == conformance.FindingKind("adr-index-only") && f.NextAction == adr.NextActionSupplyEvidence
}

// coreResultsOf is the canonical text of every result and finding of the `core` pack, without the governance rules.
func coreResultsOf(t *testing.T, run *conformance.Run) string {
	t.Helper()

	core := map[string]bool{}

	var results []any

	for i := range run.Rules {
		if run.Rules[i].Policy.ID == "core" {
			core[run.Rules[i].RuleID] = true

			results = append(results, run.Rules[i])
		}
	}

	if len(results) == 0 {
		t.Fatal("the run has no core rule result")
	}

	for i := range run.Findings {
		if core[run.Findings[i].RuleID] {
			results = append(results, run.Findings[i])
		}
	}

	text, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

// Scenario: Core pack independent of the governance pack.
// Adding the governance pack to the workspace leaves every result of the core rules as it was, and without the pack
// no governance rule runs.
func TestADRCorePackIsIndependentOfTheGovernancePack(t *testing.T) {
	t.Parallel()

	with := (&adrScenario{Register: a12Before}).run(t)
	without := (&adrScenario{Register: a12Before, WithoutGovernancePack: true}).run(t)

	if len(verdictsOf(with)) <= len(verdictsOf(without)) {
		t.Fatalf("rules with the pack %v, without %v: the pack adds the governance rules", verdictsOf(with), verdictsOf(without))
	}

	for id := range verdictsOf(without) {
		if strings.HasPrefix(id, "GOV-ADR-") {
			t.Errorf("governance rule %s ran without the pack", id)
		}
	}

	if got, want := coreResultsOf(t, with), coreResultsOf(t, without); got != want {
		t.Errorf("core results changed by the governance pack:\nwith    %s\nwithout %s", got, want)
	}
}
