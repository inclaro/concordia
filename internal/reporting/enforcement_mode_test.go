package reporting_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

// modeRun is a run with one typed rule and one finding for each (environment, mode) pair. The finding fingerprint is
// `sha256:` and the repeated letter that the pair names.
func modeRun(modes map[string]policy.Enforcement) *conformance.Run {
	run := testutil.SampleRun()
	rule := ruleWith(conformance.VerdictViolation, policy.UnknownFail)
	rule.RuleID, rule.Name, rule.Family = "CO-TYP-001", "typed rule", policy.FamilyTyped
	rule.Enforcement = policy.EnforcementShadow

	run.Findings = nil

	for _, env := range []string{"production", "staging"} {
		mode, ok := modes[env]
		if !ok {
			continue
		}

		f := infraFinding(mode)
		f.Environment = env
		f.Fingerprint = fingerprintOf(env)
		run.Findings = append(run.Findings, f)
		rule.Enforcement = conformance.Stronger(rule.Enforcement, mode)
	}

	run.Rules = []conformance.RuleResult{rule}
	run.Summary = conformance.Summarize(run.Rules, run.Findings, diag.SeverityError, policy.UnknownFail)

	return run
}

func fingerprintOf(env string) string { return "sha256:" + strings.Repeat(string(env[0]), 64) }

func shortOf(env string) string { return reporting.ShortFingerprint(fingerprintOf(env)) }

// Scenario: MIG-03 advisory rule does not block.
func TestMIG03AdvisoryDoesNotBlock(t *testing.T) {
	t.Parallel()

	run := modeRun(map[string]policy.Enforcement{"production": policy.EnforcementAdvisory})
	equal(t, run.Summary.ExitCode, 0)
	equal(t, run.Summary.Verdict, conformance.VerdictViolation)

	pretty := reporting.RenderPretty(run, plain)
	contains(t, pretty, "CO-TYP-001 typed rule (advisory: does not change the exit code)")
	contains(t, pretty, "k8s/prod/deployment.yaml:42")
	contains(t, reporting.RenderPretty(run, reporting.PrettyOptions{Verbose: true}), "fingerprint "+fingerprintOf("production"))

	junit := reporting.RenderJUnit(run)
	notContains(t, junit, "<failure")
	contains(t, junit, fingerprintOf("production"))

	_, doc := renderSarif(t, run)
	equal(t, len(doc.Runs[0].Results), 1)
	equal(t, doc.Runs[0].Results[0].Level, "note")
	equal(t, doc.Runs[0].Results[0].Properties["enforcement"], any("advisory"))
}

// Scenario: MIG-03 shadow rule is recorded only.
// The baseline half is TestBaselineCreateExcludesShadowFindings and TestBaselineUpdateRefusesToAcceptAShadowFinding
// (internal/cli).
func TestMIG03ShadowIsRecordedOnly(t *testing.T) {
	t.Parallel()

	run := modeRun(map[string]policy.Enforcement{"production": policy.EnforcementShadow})
	equal(t, run.Summary.ExitCode, 0)

	pretty := reporting.RenderPretty(run, plain)
	notContains(t, pretty, "CO-TYP-001")
	notContains(t, pretty, shortOf("production"))
	notContains(t, pretty, "1 active error")

	junit := reporting.RenderJUnit(run)
	notContains(t, junit, "CO-TYP-001")
	notContains(t, junit, fingerprintOf("production"))
	contains(t, junit, `tests="0"`)

	sarif, doc := renderSarif(t, run)
	equal(t, len(doc.Runs[0].Results), 0)
	notContains(t, string(sarif), "CO-TYP-001")
	notContains(t, string(sarif), fingerprintOf("production"))

	// results.json keeps it, with its enforcement.
	text, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	contains(t, string(text), fingerprintOf("production"))
	contains(t, string(text), `"enforcement": "shadow"`)
}

func TestMIG03PerEnvironmentGate(t *testing.T) {
	t.Parallel()

	// Mandatory in staging, advisory in production: the violation in staging blocks and the one in production does not.
	run := modeRun(map[string]policy.Enforcement{"production": policy.EnforcementAdvisory, "staging": policy.EnforcementMandatory})
	equal(t, run.Summary.ExitCode, 1)

	junit := reporting.RenderJUnit(run)
	contains(t, junit, `failures="1"`)
	contains(t, junit, fingerprintOf("staging"))

	_, doc := renderSarif(t, run)
	levels := levelsByFingerprint(doc.Runs[0].Results)

	equal(t, levels[fingerprintOf("staging")], "error")
	equal(t, levels[fingerprintOf("production")], "note")

	// The same violation with both environments advisory does not block, and one of them is shadow: only the other shows.
	run = modeRun(map[string]policy.Enforcement{"production": policy.EnforcementAdvisory, "staging": policy.EnforcementShadow})
	equal(t, run.Summary.ExitCode, 0)

	verbose := reporting.RenderPretty(run, reporting.PrettyOptions{Verbose: true})
	contains(t, verbose, "fingerprint "+fingerprintOf("production"))
	notContains(t, verbose, fingerprintOf("staging"))
}

func TestPrettySaysThatNewEnforcementWasDisabled(t *testing.T) {
	t.Parallel()

	run := modeRun(map[string]policy.Enforcement{"production": policy.EnforcementShadow})
	run.Summary.Rollback = &conformance.RunRollback{
		Reason: "false positives", AuthorizedBy: "jane", Expires: "2026-11-15",
		Message: "new enforcement was disabled: typed-obligation, decision-governance, and review rules ran as shadow (reason: false positives; authorized by jane; expires 2026-11-15)",
	}

	pretty := reporting.RenderPretty(run, plain)
	contains(t, pretty, "Rollback active: new enforcement was disabled")
	contains(t, pretty, "not a successful typed, governance, or review check")

	run.Summary.Rollback = nil
	notContains(t, reporting.RenderPretty(run, plain), "Rollback active")
}
