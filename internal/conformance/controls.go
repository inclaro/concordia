package conformance

import (
	"cmp"
	"fmt"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

// controlResults gives the result of each control. The verdict of an applicable control is the worst verdict of the
// rules that ran and of its checks (`check-ids`). A check takes its verdict from the admitted check results of the run
// (ExternalEvaluation.Checks); a check with no result is `unknown` with `no-evidence-supplied` and the reason names it,
// so that a link to a check can never fulfil a control by itself. A control that lists no check is judged by its rules
// alone, as before; a control that lists no rule is judged by its checks alone.
//
// The settings of the control apply to its checks as they apply to its rules:
//   - `minimum-policy-coverage` is the share of the checks that have a definite result (a rule needs a share of the
//     files it covers). Below it the control is at best `unknown` with the reason `coverage-incomplete`.
//   - `unknown-disposition` decides whether an unknown check fails the run. A check has no enforcement mode, so it
//     follows the advisory default of the assurance families: only `fail`, set by the control or by `--unknown`
//     (`cliUnknown`), makes it fail, and the second result says so. `warn` and `ignore` leave the verdict `unknown`
//     and the run unchanged.
func controlResults(
	controls []policy.ControlPlan, rules []RuleResult, external *ExternalEvaluation, cliUnknown policy.UnknownDisposition,
) (results []ControlResult, failOnUnknownCheck bool) {
	verdictOf := make(map[string]RuleVerdict, len(rules))
	for i := range rules {
		verdictOf[rules[i].RuleID] = rules[i].Verdict
	}

	results = make([]ControlResult, 0, len(controls))

	for i := range controls {
		result, failsOnUnknown := judgeControl(&controls[i], verdictOf, external, cliUnknown)
		failOnUnknownCheck = failOnUnknownCheck || failsOnUnknown

		results = append(results, result)
	}

	return results, failOnUnknownCheck
}

// judgeControl gives the result of one control and whether an unknown check of it fails the run.
func judgeControl(
	ctl *policy.ControlPlan, verdictOf map[string]RuleVerdict, external *ExternalEvaluation, cliUnknown policy.UnknownDisposition,
) (result ControlResult, failsOnUnknownCheck bool) {
	result = ControlResult{
		ControlID: ctl.ControlID, Scope: ctl.Scope, RuleIDs: nonNilStrings(ctl.RuleIDs), CheckIDs: ctl.CheckIDs, Verdict: VerdictNotApplicable,
	}

	if !ctl.Applicable {
		return result, false
	}

	verdicts := ruleVerdictsOf(ctl, verdictOf)
	checks := checkVerdicts(ctl, external, &result)
	result.Verdict = WorstVerdict(append(verdicts, checks.verdicts...))
	applyCoverageFloor(ctl, checks, &result)

	return result, checks.unknown > 0 && cmp.Or(cliUnknown, ctl.UnknownDisposition) == policy.UnknownFail
}

// ruleVerdictsOf lists the verdicts of the rules of the control that ran.
func ruleVerdictsOf(ctl *policy.ControlPlan, verdictOf map[string]RuleVerdict) []RuleVerdict {
	var verdicts []RuleVerdict

	for _, id := range ctl.RuleIDs {
		if v, ok := verdictOf[id]; ok {
			verdicts = append(verdicts, v)
		}
	}

	return verdicts
}

// applyCoverageFloor makes the control at best unknown when too few of its checks have a definite result.
func applyCoverageFloor(ctl *policy.ControlPlan, checks controlChecks, result *ControlResult) {
	floor, ok := coverageFloorOf(ctl)
	if !ok || checks.coverage >= floor {
		return
	}

	result.Verdict = WorstVerdict([]RuleVerdict{result.Verdict, VerdictUnknown})
	result.Reasons = append(result.Reasons, Reason{
		Code: string(ReasonCoverageIncomplete),
		Message: fmt.Sprintf("%d of %d checks have a definite result (%.2f), below the control minimum %.2f",
			checks.decided, len(ctl.CheckIDs), checks.coverage, floor),
	})
}

// coverageFloorOf gives the minimum policy coverage of the control when it applies: a control with no check has no coverage to judge.
func coverageFloorOf(ctl *policy.ControlPlan) (float64, bool) {
	if ctl.MinimumPolicyCoverage == nil || len(ctl.CheckIDs) == 0 {
		return 0, false
	}

	return *ctl.MinimumPolicyCoverage, true
}

// controlChecks is what the checks of one control gave.
type controlChecks struct {
	verdicts []RuleVerdict
	// decided counts the checks with a definite result (pass, violation, or not applicable) and unknown counts the checks that are unknown.
	decided, unknown int
	// coverage is decided over the number of checks, 1 when the control lists none.
	coverage float64
}

// checkVerdicts reads the verdict of each check of the control, adds the reason of each check that is `unknown` or an
// `error` to the result, and counts them.
func checkVerdicts(ctl *policy.ControlPlan, external *ExternalEvaluation, result *ControlResult) controlChecks {
	checks := controlChecks{coverage: 1}

	for _, checkID := range ctl.CheckIDs {
		check := external.checkVerdict(checkID)
		checks.verdicts = append(checks.verdicts, check.Verdict)

		switch check.Verdict {
		case VerdictPass, VerdictViolation, VerdictNotApplicable:
			checks.decided++
		case VerdictUnknown:
			checks.unknown++

			result.Reasons = append(result.Reasons, Reason{Code: string(check.Reason), Message: "check " + checkID + ": " + check.Detail})
		case VerdictError:
			result.Reasons = append(result.Reasons, Reason{Code: diag.CON099, Message: "check " + checkID + ": " + check.Detail})
		}
	}

	if len(ctl.CheckIDs) > 0 {
		checks.coverage = float64(checks.decided) / float64(len(ctl.CheckIDs))
	}

	return checks
}

// controlsAndSummary gives the control results and the summary of the run. A control that sets
// `unknown-disposition: fail` fails the run for a check that has no definite result, as a rule does. Violations and
// errors keep their exit codes: only a clean run becomes an unknown run.
func (c *checker) controlsAndSummary(
	controls []policy.ControlPlan, rules []RuleResult, findings []Finding, failOn diag.Severity, unknownPolicy policy.UnknownDisposition,
) ([]ControlResult, RunSummary) {
	results, failsOnUnknownCheck := controlResults(controls, rules, c.external, c.opts.Unknown)
	summary := c.summarize(rules, findings, failOn, unknownPolicy)

	if failsOnUnknownCheck && summary.ExitCode == diag.OK {
		summary.ExitCode = diag.Unknown
	}

	return results, summary
}
