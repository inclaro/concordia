package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

const typedPolicyHead = `
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: typed-pack, version: 1.0.0}
`

func typedRuleText(id, extra, obligation string) string {
	return "  - id: " + id + "\n    name: typed " + id + "\n    severity: error\n    subject: {calmNodes: [billing-service]}\n" + extra +
		"    obligation:\n" + obligation
}

// requireImage requires that each workload of billing uses an image.
const requireImage = "      type: required\n      relation: {kind: uses-image, direction: from-workload}\n"

// forbidImports forbids that billing imports anything: the demo graph has imports of billing. `imports` is a code
// family, which is stable without a capability profile.
const forbidImports = "      type: forbidden\n      relation: {kind: imports, direction: from-subject}\n"

// typedArch is the demo architecture without the control that names the as-built rule.
func typedArch() string {
	return mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":   []any{calmNode("billing-service", nil), calmNode("fulfillment-service", nil), calmNode("legacy-service", nil)},
		"relationships": []any{map[string]any{
			"unique-id": "billing-to-fulfillment",
			"relationship-type": map[string]any{"connects": map[string]any{
				"source": map[string]any{"node": "billing-service"}, "destination": map[string]any{"node": "fulfillment-service"},
			}},
		}},
	})
}

func typedFiles(precedence string, rules ...string) map[string]string {
	return map[string]string{
		"architecture/a.calm.json":  typedArch(),
		"policies/demo.policy.yaml": typedPolicyHead + precedence + "rules:\n" + strings.Join(rules, ""),
	}
}

// checkTypedRule001 runs the check for the rule CO-TYP-001 only.
func checkTypedRule001(t *testing.T, files map[string]string, opts *setupOpts) *conformance.Run {
	t.Helper()

	return newSetup(t, files, opts).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} })
}

func TestRunCheckEvaluatesTypedRules(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "", requireImage))
	run := checkTypedRule001(t, files, nil)

	if len(run.Rules) != 1 {
		t.Fatalf("rules = %+v", run.Rules)
	}

	rule := run.Rules[0]

	// No workload of billing exists in the graph and the coverage of the family is complete: the necessary workload is absent.
	if !typedRuleIsAbsentWorkload(&rule) {
		t.Fatalf("rule = %+v", rule)
	}

	if !typedRunHasAbsentFinding(run, &rule) {
		t.Fatalf("findings = %+v", run.Findings)
	}

	// Typed rules are advisory by default: the violation is kept and does not gate.
	if !typedRunIsAdvisoryViolation(run, &rule) {
		t.Fatalf("enforcement %q exit %d verdict %s, want advisory, 0, violation", rule.Enforcement, run.Summary.ExitCode, run.Summary.Verdict)
	}
}

// typedRuleIsAbsentWorkload says whether the rule is a typed violation with one obligation on billing whose
// occurrence is absent.
func typedRuleIsAbsentWorkload(rule *conformance.RuleResult) bool {
	if rule.Family != "typed" || rule.Verdict != conformance.VerdictViolation {
		return false
	}

	if len(rule.Obligations) != 1 || rule.Obligations[0].Subject != "billing-service" {
		return false
	}

	return rule.Obligations[0].Occurrences[0].Class == conformance.OccurrenceAbsent
}

// typedRunHasAbsentFinding says whether the run has exactly one advisory finding for the absent obligation of the rule.
func typedRunHasAbsentFinding(run *conformance.Run, rule *conformance.RuleResult) bool {
	if len(run.Findings) != 1 {
		return false
	}

	finding := &run.Findings[0]
	if finding.Kind != conformance.KindObligationAbsent || finding.ObligationID != rule.Obligations[0].ID {
		return false
	}

	return finding.Enforcement == "advisory"
}

// typedRunIsAdvisoryViolation says whether the rule is advisory, the run does not gate, and the summary is a violation.
func typedRunIsAdvisoryViolation(run *conformance.Run, rule *conformance.RuleResult) bool {
	if rule.Enforcement != "advisory" || run.Summary.ExitCode != 0 {
		return false
	}

	return run.Summary.Verdict == conformance.VerdictViolation
}

// typedWorkspace declares the environment production, and adds the lines (indented by two) to `check`.
func typedWorkspace(checkLines string) string {
	const check = "check: {minimumCoverage: 0.5, unknown: fail, failOn: error}\n"

	return strings.Replace(wsText(""), check, "check:\n  minimumCoverage: 0.5\n  unknown: fail\n  failOn: error\n"+checkLines, 1) +
		"environments:\n  - {id: production, sources: [\"k8s/production/**\"]}\n"
}

func TestRunCheckMandatoryTypedViolationGivesExit1(t *testing.T) {
	t.Parallel()

	// `imports` is a code family: stable without a profile, so a mandatory rule may depend on it.
	files := typedFiles("", typedRuleText("CO-TYP-001", "    enforcement: mandatory\n", forbidImports))
	run := checkTypedRule001(t, files, &setupOpts{ws: typedWorkspace("")})

	if run.Summary.ExitCode != diag.Violation {
		t.Fatalf("exit %d enforcement %q verdict %s, want 1 and mandatory", run.Summary.ExitCode, run.Rules[0].Enforcement, run.Rules[0].Verdict)
	}

	if run.Rules[0].Enforcement != "mandatory" {
		t.Fatalf("exit %d enforcement %q verdict %s, want 1 and mandatory", run.Summary.ExitCode, run.Rules[0].Enforcement, run.Rules[0].Verdict)
	}
}

// A mandatory rule on an advisory family gives no gate result: CO-POL-018 names the rule, the environment, and the family.
func TestRunCheckMandatoryRuleOnAnAdvisoryFamilyIsRefused(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    enforcement: mandatory\n", requireImage))

	failure := newSetup(t, files, &setupOpts{ws: typedWorkspace("")}).failureOf(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} })

	if failure.Diagnostic.Code != diag.POL018 || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("failure = %+v", failure)
	}

	text := failure.Error()
	for _, want := range []string{"CO-TYP-001", "production", "uses-image"} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure = %+v", failure)
		}
	}
}

func TestRunCheckContradictingRulesStopWithExit2(t *testing.T) { // POL-05
	t.Parallel()

	forbidImage := "      type: forbidden\n      relation: {kind: uses-image, direction: from-workload}\n"
	files := typedFiles("", typedRuleText("CO-TYP-001", "", requireImage), typedRuleText("CO-TYP-002", "", forbidImage))

	failure := newSetup(t, files, nil).failureOf(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001", "CO-TYP-002"} })

	text := failure.Error()
	if failure.Diagnostic.Code != diag.POL017 || failure.ExitCode != 2 {
		t.Fatalf("failure = %+v", failure)
	}

	for _, want := range []string{"CO-TYP-001", "CO-TYP-002", "billing-service"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q is not in %q", want, text)
		}
	}
}

func TestRunCheckPrecedenceResolvesTheConflict(t *testing.T) {
	t.Parallel()

	forbidImage := "      type: forbidden\n      relation: {kind: uses-image, direction: from-workload}\n"
	files := typedFiles("precedence: {version: 1, order: [CO-TYP-002, CO-TYP-001]}\n",
		typedRuleText("CO-TYP-001", "", requireImage), typedRuleText("CO-TYP-002", "", forbidImage))

	run := newSetup(t, files, nil).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001", "CO-TYP-002"} })

	var loser *conformance.RuleResult

	for i := range run.Rules {
		if run.Rules[i].RuleID == "CO-TYP-001" {
			loser = &run.Rules[i]
		}
	}

	if !typedRuleSupersededByTyp002(loser) {
		t.Fatalf("loser = %+v", loser)
	}
}

// typedRuleSupersededByTyp002 says whether the rule lost to CO-TYP-002 under version 1 of the precedence typed-pack.
func typedRuleSupersededByTyp002(rule *conformance.RuleResult) bool {
	if rule == nil || rule.SupersededBy == nil {
		return false
	}

	by := rule.SupersededBy
	if by.RuleID != "CO-TYP-002" || by.PrecedenceVersion != 1 {
		return false
	}

	return by.PrecedenceID == "typed-pack"
}

// With --changed, a typed rule is not reducible: it is reported in full and CON027 names it.
func TestRunCheckChangedReportsTypedRulesInFull(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "", requireImage))
	s := newSetup(t, files, &setupOpts{graph: buildGraph(graphOpts{refund: true})})
	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{
			Rev:     "abc123",
			Files:   []string{billingFile, storeFile, legacyFile, refundFile},
			Changed: workspace.ChangeSet{Ref: "HEAD", Base: "abc123abc123abc123", Files: []string{refundFile}},
		}
	}

	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		o.Rules = []string{"CO-TYP-001"}
		o.Changed = "HEAD"
	})

	if run.Rules[0].ChangedScope != "full" || len(run.Rules[0].Obligations) != 1 {
		t.Fatalf("rule = %+v", run.Rules[0])
	}

	i := slices.IndexFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.CON027 })
	if i < 0 || !strings.Contains(run.Diagnostics[i].Message, "CO-TYP-001") {
		t.Fatalf("diagnostics = %+v, want CO-CON-027 naming the typed rule", run.Diagnostics)
	}
}

// The same rule set to advisory in the workspace runs: only the workspace chooses the mode.
func TestRunCheckWorkspaceAdvisoryRunsTheRuleThatWouldBeRefused(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    enforcement: mandatory\n", requireImage))
	ws := typedWorkspace("  enforcement:\n    - {rules: [CO-TYP-001], mode: advisory}\n")
	run := checkTypedRule001(t, files, &setupOpts{ws: ws})

	if run.Rules[0].Enforcement != "advisory" {
		t.Fatalf("enforcement %q exit %d, want advisory and 0", run.Rules[0].Enforcement, run.Summary.ExitCode)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Fatalf("enforcement %q exit %d, want advisory and 0", run.Rules[0].Enforcement, run.Summary.ExitCode)
	}
}

// MIG-03: the workspace gates by environment. Staging is mandatory on a code family; production stays advisory.
func TestRunCheckEnforcementIsPerEnvironment(t *testing.T) {
	t.Parallel()

	files := typedFiles("", typedRuleText("CO-TYP-001", "    family: deploy\n", forbidImports))
	ws := typedWorkspace("  enforcement:\n    - {family: deploy, environment: production, mode: advisory}\n")
	run := checkTypedRule001(t, files, &setupOpts{ws: ws})

	if len(run.Findings) != 1 {
		t.Fatalf("findings = %+v exit %d", run.Findings, run.Summary.ExitCode)
	}

	finding := &run.Findings[0]
	if finding.Environment != "production" || finding.Enforcement != "advisory" {
		t.Fatalf("findings = %+v exit %d", run.Findings, run.Summary.ExitCode)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Fatalf("findings = %+v exit %d", run.Findings, run.Summary.ExitCode)
	}
}
