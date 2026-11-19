package integration_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

// Enforcement of the `infrastructure` pack (INFRA-PLC and INFRA-TRC rules): advisory by default, `check.enforcement`
// per family and environment, `check.rollback`, the rollout report, and the refusal of `mandatory` while the families
// `placed-in`, `uses-image`, and `built-from` have maturity `advisory`.

const (
	enfStagingWorkloadBinding = "  - id: bind-worker-workload\n    architectureRef: calm:node:worker-service\n    match:\n      any:\n        - {kind: workload, environment: staging, resourceKind: Deployment, name: worker, namespace: payments}\n"

	enfPlacementShadowStaging = "  enforcement:\n    - {family: deployment-placement, environment: staging, mode: shadow}\n"
	enfTraceShadowStaging     = "  enforcement:\n    - {family: deployment-trace, environment: staging, mode: shadow}\n"
	enfRollback               = "  rollback: {reason: false positives on the new rules, authorizedBy: jane.doe, expires: \"2026-12-31\"}\n"
)

// enfWithoutStagingWorker is the default binding file without the binding of the staging worker. Then worker-service has
// no workload in either environment and both coverages of `placed-in` are complete, so the rules of both families give
// a violation in `production` and in `staging`.
func enfWithoutStagingWorker(t *testing.T) string {
	t.Helper()

	text := strings.Replace(deploymentBindings, enfStagingWorkloadBinding, "", 1)
	if text == deploymentBindings {
		t.Fatal("the default bindings have no binding of the staging worker")
	}

	return text
}

// enfFinding is a finding of results.json.
type enfFinding struct {
	RuleID      string `json:"ruleId"`
	Subject     string `json:"subject"`
	Environment string `json:"environment"`
	Result      string `json:"result"`
	Reason      string `json:"reason"`
	Enforcement string `json:"enforcement"`
	NextAction  string `json:"nextAction"`
	Fingerprint string `json:"fingerprint"`
}

// enfResults is the part of results.json that the tests read.
type enfResults struct {
	Summary struct {
		Rollback *struct {
			Reason       string `json:"reason"`
			AuthorizedBy string `json:"authorizedBy"`
			Expires      string `json:"expires"`
			Message      string `json:"message"`
		} `json:"rollback"`
		ExitCode int `json:"exitCode"`
	} `json:"summary"`
	Findings []enfFinding `json:"findings"`
}

// enfCheckJSON runs `check --format json` and decodes results.json.
func enfCheckJSON(t *testing.T, d *deployment) (cliResult, *enfResults) {
	t.Helper()

	res := d.cli(t, "check", "--format", "json")

	var doc enfResults
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("results.json: %v (exit code %d, stderr %q)", err, res.code, res.stderr)
	}

	return res, &doc
}

// enfKey names the finding of a rule for a subject in an environment.
type enfKey struct {
	rule, subject, env string
}

func (k enfKey) matches(f *enfFinding) bool {
	return f.RuleID == k.rule && f.Subject == k.subject && f.Environment == k.env
}

// enfFind gives the finding of the rule for the subject in the environment.
func enfFind(t *testing.T, doc *enfResults, rule, subject, env string) *enfFinding {
	t.Helper()

	return enfFindKey(t, doc, enfKey{rule: rule, subject: subject, env: env})
}

func enfFindKey(t *testing.T, doc *enfResults, key enfKey) *enfFinding {
	t.Helper()

	for i := range doc.Findings {
		if key.matches(&doc.Findings[i]) {
			return &doc.Findings[i]
		}
	}

	t.Fatalf("results.json has no finding of %s for %s in %q: %+v", key.rule, key.subject, key.env, doc.Findings)

	return nil
}

// enfInfraFindings are the findings of the rules of the `infrastructure` pack.
func enfInfraFindings(doc *enfResults) []*enfFinding {
	var out []*enfFinding

	for i := range doc.Findings {
		if strings.HasPrefix(doc.Findings[i].RuleID, "INFRA-") {
			out = append(out, &doc.Findings[i])
		}
	}

	return out
}

// enfIn says whether the finding is in the environment and its rule ID has the prefix.
func enfIn(f *enfFinding, env, rulePrefix string) bool {
	return f.Environment == env && strings.HasPrefix(f.RuleID, rulePrefix)
}

func enfRequireMode(t *testing.T, f *enfFinding, want string) {
	t.Helper()

	if f.Enforcement != want {
		t.Errorf("%s %s in %q has enforcement %q, want %s", f.RuleID, f.Subject, f.Environment, f.Enforcement, want)
	}
}

// enfRequireViolation checks that a finding is a violation and has the enforcement mode.
func enfRequireViolation(t *testing.T, f *enfFinding, label, enforcement string) {
	t.Helper()

	if f.Result != "violation" || f.Enforcement != enforcement {
		t.Errorf("%s = %+v, want %s with enforcement %s", label, f, "violation", enforcement)
	}
}

// enfRequireObligation checks the verdict and the enforcement mode of an obligation.
func enfRequireObligation(t *testing.T, ob *conformance.ObligationResult, label string, verdict conformance.RuleVerdict, enforcement policy.Enforcement) {
	t.Helper()

	if ob.Verdict != verdict || ob.Enforcement != enforcement {
		t.Errorf("%s = %s, %s; want %s, %s", label, ob.Verdict, ob.Enforcement, verdict, enforcement)
	}
}

func enfRequireContains(t *testing.T, what, text string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, text)
		}
	}
}

func enfRequireAbsent(t *testing.T, what, text string, unwanted ...string) {
	t.Helper()

	for _, item := range unwanted {
		if strings.Contains(text, item) {
			t.Errorf("%s shows %q", what, item)
		}
	}
}

// enfOutputs runs the pretty report (verbose), SARIF, and JUnit outputs of `check`.
func enfOutputs(t *testing.T, d *deployment) map[string]cliResult {
	t.Helper()

	return map[string]cliResult{
		"pretty": d.cli(t, "check", "--verbose"),
		"sarif":  d.cli(t, "check", "--format", "sarif"),
		"junit":  d.cli(t, "check", "--format", "junit"),
	}
}

// enfRequireHidden checks that the pretty report, SARIF, and JUnit show no trace of the finding, and that the same outputs
// show the visible finding.
func enfRequireHidden(t *testing.T, d *deployment, hidden, visible *enfFinding) {
	t.Helper()

	for name, res := range enfOutputs(t, d) {
		if res.code != 0 {
			t.Errorf("%s: exit code = %d, want 0: %s", name, res.code, res.stderr)
		}

		enfRequireAbsent(t, name+" (the shadow finding)", res.stdout, hidden.Fingerprint)
		enfRequireContains(t, name+" (the advisory finding)", res.stdout, visible.Fingerprint)
	}
}

// enfRequireAdvisoryPretty checks that the pretty report exits with 0 and shows what it must.
func enfRequireAdvisoryPretty(t *testing.T, d *deployment, wants ...string) {
	t.Helper()

	pretty := d.cli(t, "check")
	if pretty.code != 0 {
		t.Errorf("pretty exit code = %d, want 0: %s", pretty.code, pretty.stderr)
	}

	enfRequireContains(t, "the pretty report", pretty.stdout, wants...)
}

// enfCountInfraResults checks that every INFRA finding is advisory and counts the violations and the unknowns.
func enfCountInfraResults(t *testing.T, doc *enfResults) (violations, unknowns int) {
	t.Helper()

	for _, f := range enfInfraFindings(doc) {
		enfRequireMode(t, f, "advisory")

		switch f.Result {
		case "violation":
			violations++
		case "unknown":
			unknowns++
		}
	}

	return violations, unknowns
}

// Spec deployment-trace, "advisory unknown", and deployment-placement, "advisory default": no entry, so every INFRA
// finding is advisory, a violation and an `unknown` show with enforcement advisory, and the exit code is 0 (not 1, not 4)
// although the workspace says `unknown: fail`.
func TestDeploymentEnforcementAdvisoryDefault(t *testing.T) {
	d := deploymentRun(t, &deploymentSpec{})

	enfRequireObligation(t, d.obligationOf(t, "INFRA-PLC-001", "worker-service", "production"),
		"INFRA-PLC-001 worker-service production", conformance.VerdictViolation, policy.EnforcementAdvisory)
	enfRequireObligation(t, d.obligationOf(t, "INFRA-TRC-004", "api-service", "production"),
		"INFRA-TRC-004 api-service production", conformance.VerdictUnknown, policy.EnforcementAdvisory)

	if d.run.Summary.ExitCode != 0 {
		t.Errorf("run exit code = %d, want 0", d.run.Summary.ExitCode)
	}

	res, doc := enfCheckJSON(t, d)
	if res.code != 0 || doc.Summary.ExitCode != 0 {
		t.Errorf("exit code = %d (summary %d), want 0: a violation and an unknown of advisory rules never block (not 1, not 4)", res.code, doc.Summary.ExitCode)
	}

	if violations, unknowns := enfCountInfraResults(t, doc); violations == 0 || unknowns == 0 {
		t.Errorf("violations = %d, unknowns = %d, want both present", violations, unknowns)
	}

	if f := enfFind(t, doc, "INFRA-PLC-001", "worker-service", "production"); f.Result != "violation" || f.NextAction != "fix implementation" {
		t.Errorf("INFRA-PLC-001 worker-service production = %+v", f)
	}

	if f := enfFind(t, doc, "INFRA-TRC-004", "api-service", "production"); f.Result != "unknown" || f.Reason != "coverage-incomplete" {
		t.Errorf("INFRA-TRC-004 api-service production = %+v", f)
	}

	enfRequireAdvisoryPretty(t, d, "FAIL INFRA-PLC-001", "UNKNOWN INFRA-TRC-002", "(advisory: does not change the exit code)")
}

// enfPlacementShadowProblem says what is wrong with the mode of a finding when only the placement family of staging is
// shadow: every placement finding of staging is shadow, and no other family of staging changed its mode.
func enfPlacementShadowProblem(f *enfFinding) string {
	switch {
	case enfIn(f, "staging", "INFRA-PLC-") && f.Enforcement != "shadow":
		return fmt.Sprintf("%s %s in staging has enforcement %q, want shadow", f.RuleID, f.Subject, f.Enforcement)
	case enfIn(f, "production", "INFRA-") && f.Enforcement != "advisory":
		return fmt.Sprintf("%s %s in production has enforcement %q, want advisory", f.RuleID, f.Subject, f.Enforcement)
	case strings.HasPrefix(f.RuleID, "INFRA-TRC-") && f.Enforcement != "advisory":
		return fmt.Sprintf("%s %s in %q has enforcement %q, want advisory (family deployment-trace has no entry)", f.RuleID, f.Subject, f.Environment, f.Enforcement)
	}

	return ""
}

// Spec deployment-placement, "staging shadow, production advisory": the staging violation is in results.json only, with
// enforcement shadow; the production violation shows with enforcement advisory in pretty, SARIF, and JUnit.
func TestDeploymentEnforcementPlacementStagingShadow(t *testing.T) {
	d := deploymentRun(t, &deploymentSpec{
		bindings: enfWithoutStagingWorker(t),
		check:    deploymentCheck + enfPlacementShadowStaging,
	})

	enfRequireObligation(t, d.obligationOf(t, "INFRA-PLC-001", "worker-service", "staging"),
		"staging", conformance.VerdictViolation, policy.EnforcementShadow)
	enfRequireObligation(t, d.obligationOf(t, "INFRA-PLC-001", "worker-service", "production"),
		"production", conformance.VerdictViolation, policy.EnforcementAdvisory)

	res, doc := enfCheckJSON(t, d)
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0: %s", res.code, res.stderr)
	}

	hidden := enfFindKey(t, doc, enfKey{"INFRA-PLC-001", "worker-service", "staging"})
	visible := enfFindKey(t, doc, enfKey{"INFRA-PLC-001", "worker-service", "production"})

	enfRequireViolation(t, hidden, "staging finding", "shadow")
	enfRequireViolation(t, visible, "production finding", "advisory")

	for _, f := range doc.Findings {
		if problem := enfPlacementShadowProblem(&f); problem != "" {
			t.Error(problem)
		}
	}

	enfRequireHidden(t, d, hidden, visible)
	enfRequireAdvisoryPretty(t, d, "(advisory: does not change the exit code)")
}

// enfTraceShadowMode is the mode of an INFRA finding when only the trace family of staging is shadow.
func enfTraceShadowMode(f *enfFinding) string {
	if enfIn(f, "staging", "INFRA-TRC-") {
		return "shadow"
	}

	return "advisory"
}

// Spec deployment-trace, "shadow in one environment": the findings of the trace rules for staging are in results.json
// only; production stays advisory, and the placement rules are not changed.
func TestDeploymentEnforcementTraceStagingShadow(t *testing.T) {
	d := deploymentRun(t, &deploymentSpec{
		bindings: enfWithoutStagingWorker(t),
		check:    deploymentCheck + enfTraceShadowStaging,
	})

	enfRequireObligation(t, d.obligationOf(t, "INFRA-TRC-001", "worker-service", "staging"),
		"TRC-001 staging", conformance.VerdictViolation, policy.EnforcementShadow)
	enfRequireObligation(t, d.obligationOf(t, "INFRA-TRC-001", "worker-service", "production"),
		"TRC-001 production", conformance.VerdictViolation, policy.EnforcementAdvisory)

	res, doc := enfCheckJSON(t, d)
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0: %s", res.code, res.stderr)
	}

	for _, f := range enfInfraFindings(doc) {
		enfRequireMode(t, f, enfTraceShadowMode(f))
	}

	hidden := enfFindKey(t, doc, enfKey{"INFRA-TRC-001", "worker-service", "staging"})
	visible := enfFindKey(t, doc, enfKey{"INFRA-TRC-001", "worker-service", "production"})

	enfRequireViolation(t, hidden, "staging finding", "shadow")
	enfRequireHidden(t, d, hidden, visible)

	// The staging TRC-004 violation of the worker is shadow as well and is not shown: the coverage of `uses-image`
	// is complete for staging, so the absence of an image fact is a violation, whatever the coverage of production.
	unknown := enfFindKey(t, doc, enfKey{"INFRA-TRC-004", "worker-service", "staging"})
	enfRequireViolation(t, unknown, "INFRA-TRC-004 worker-service staging", "shadow")

	for name, out := range enfOutputs(t, d) {
		enfRequireAbsent(t, name+" (the shadow unknown)", out.stdout, unknown.Fingerprint)
	}

	// Placement findings of staging still show: the entry names the trace family only.
	plc := d.obligationOf(t, "INFRA-PLC-001", "worker-service", "staging")
	if plc.Enforcement != policy.EnforcementAdvisory {
		t.Errorf("INFRA-PLC-001 staging = %s, want advisory", plc.Enforcement)
	}
}

// enfRequireRollbackShadowObligations checks that every typed rule of the pack runs as shadow.
func enfRequireRollbackShadowObligations(t *testing.T, d *deployment) {
	t.Helper()

	for _, id := range []string{"INFRA-PLC-001", "INFRA-PLC-002", "INFRA-TRC-001", "INFRA-TRC-002", "INFRA-TRC-003", "INFRA-TRC-004"} {
		rule := ruleOf(t, d.run, id)
		if len(rule.Obligations) == 0 {
			t.Fatalf("%s has no obligations", id)
		}

		for i := range rule.Obligations {
			if o := &rule.Obligations[i]; o.Enforcement != policy.EnforcementShadow {
				t.Errorf("%s %s in %q has enforcement %q, want shadow under the rollback", id, o.Subject, o.Environment, o.Enforcement)
			}
		}
	}
}

func enfRequireRollbackSummary(t *testing.T, doc *enfResults) {
	t.Helper()

	rb := doc.Summary.Rollback
	if rb == nil {
		t.Fatal("summary.rollback is missing")
	}

	got := [3]string{rb.Reason, rb.AuthorizedBy, rb.Expires}
	want := [3]string{"false positives on the new rules", "jane.doe", "2026-12-31"}

	if got != want || !strings.Contains(rb.Message, "new enforcement was disabled") {
		t.Errorf("rollback = %+v", *rb)
	}
}

// Foundation `check.rollback` with the pack: an active rollback (expiry after the clock 2026-06-01) runs every typed rule
// as shadow, records the reason, and the pretty report says that new enforcement was disabled.
func TestDeploymentEnforcementRollback(t *testing.T) {
	d := deploymentRun(t, &deploymentSpec{check: deploymentCheck + enfRollback})

	enfRequireRollbackShadowObligations(t, d)

	res, doc := enfCheckJSON(t, d)
	if res.code != 0 {
		t.Errorf("exit code = %d, want 0: %s", res.code, res.stderr)
	}

	enfRequireRollbackSummary(t, doc)

	for _, f := range enfInfraFindings(doc) {
		enfRequireMode(t, f, "shadow")
	}

	pretty := d.cli(t, "check")
	if pretty.code != 0 {
		t.Errorf("pretty exit code = %d, want 0: %s", pretty.code, pretty.stderr)
	}

	enfRequireContains(t, "the pretty report", pretty.stdout,
		"Rollback active: new enforcement was disabled", "false positives on the new rules", "jane.doe", "2026-12-31",
		"This is not a successful typed, governance, or review check")
	enfRequireAbsent(t, "the pretty report (every typed rule is shadow)", pretty.stdout, "FAIL INFRA-PLC-001", "UNKNOWN INFRA-TRC-002")
}

// enfMandatoryRefusal is a `mandatory` entry that the check refuses, with the texts that stderr must show.
type enfMandatoryRefusal struct {
	name     string
	entry    string
	contains []string
}

var enfMandatoryRefusals = []enfMandatoryRefusal{
	{
		name:  "placement family",
		entry: "    - {family: deployment-placement, mode: mandatory}\n",
		contains: []string{
			"CO-POL-018", "INFRA-PLC-001", "INFRA-PLC-002", "production", "staging", "family placed-in has maturity advisory",
		},
	},
	{
		name:  "placement family in staging",
		entry: "    - {family: deployment-placement, environment: staging, mode: mandatory}\n",
		contains: []string{
			"CO-POL-018", "INFRA-PLC-001 is mandatory in staging", "family placed-in has maturity advisory",
		},
	},
	{
		name:  "trace family",
		entry: "    - {family: deployment-trace, mode: mandatory}\n",
		contains: []string{
			"CO-POL-018", "INFRA-TRC-001", "INFRA-TRC-002", "INFRA-TRC-003", "INFRA-TRC-004", "production", "staging",
			"family placed-in has maturity advisory", "family uses-image has maturity advisory", "family built-from has maturity advisory",
		},
	},
	{
		name:  "trace family in production",
		entry: "    - {family: deployment-trace, environment: production, mode: mandatory}\n",
		contains: []string{
			"CO-POL-018", "INFRA-TRC-002 is mandatory in production", "family uses-image has maturity advisory", "family built-from has maturity advisory",
		},
	},
}

// enfContainsAll says whether the text holds every part.
func enfContainsAll(text string, parts ...string) bool {
	return !slices.ContainsFunc(parts, func(part string) bool { return !strings.Contains(text, part) })
}

// A `mandatory` entry on the family of the rules: `check` exits with 2 and CO-POL-018 names the rule, the environment,
// and the family with its maturity `advisory`. The run gives no gate result.
func TestDeploymentEnforcementMandatoryRefused(t *testing.T) {
	for _, tt := range enfMandatoryRefusals {
		t.Run(tt.name, func(t *testing.T) {
			spec := &deploymentSpec{check: deploymentCheck + "  enforcement:\n" + tt.entry}

			err := deploymentCheckError(t, spec)
			if err == nil {
				t.Fatal("check ran although a mandatory rule depends on a family of maturity advisory")
			}

			if msg := err.Error(); !enfContainsAll(msg, "CO-POL-018", "is mandatory in ", "family ") {
				t.Errorf("error = %q", msg)
			}

			w := &deployment{w: newDeploymentWorkspace(t, spec)}
			w.rec = w.w.rec

			res := w.cli(t, "check")
			if res.code != 2 {
				t.Fatalf("exit code = %d, want 2: %s", res.code, res.stderr)
			}

			enfRequireContains(t, "stderr", res.stderr, tt.contains...)

			if strings.Contains(res.stdout, "Conformance:") {
				t.Errorf("the run printed a gate result:\n%s", res.stdout)
			}
		})
	}
}

// A `shadow` or `advisory` entry for the families is accepted: the refusal is for `mandatory` only, and the workspace
// chooses the mode (Concordia never lowers one).
func TestDeploymentEnforcementAdvisoryEntriesAreAccepted(t *testing.T) {
	d := deploymentRun(t, &deploymentSpec{check: deploymentCheck + "  enforcement:\n    - {family: deployment-placement, mode: advisory}\n    - {family: deployment-trace, mode: advisory}\n"})

	if res := d.cli(t, "check"); res.code != 0 {
		t.Errorf("exit code = %d, want 0: %s", res.code, res.stderr)
	}
}
