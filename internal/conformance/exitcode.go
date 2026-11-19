package conformance

import (
	"slices"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
)

// verdictOrder lists the verdicts from the worst to the best.
var verdictOrder = []RuleVerdict{VerdictError, VerdictViolation, VerdictUnknown, VerdictPass, VerdictNotApplicable}

// WorstVerdict gives the worst verdict, in the order error, violation, unknown,
// pass, not-applicable. It gives `not-applicable` for an empty list.
func WorstVerdict(verdicts []RuleVerdict) RuleVerdict {
	best := len(verdictOrder) - 1

	for _, v := range verdicts {
		if i := slices.Index(verdictOrder, v); i >= 0 {
			best = min(best, i)
		}
	}

	return verdictOrder[best]
}

func severityRank(severity string) int {
	switch severity {
	case string(diag.SeverityError):
		return 2
	case string(diag.SeverityWarning):
		return 1
	default:
		return 0
	}
}

// IsLive says whether a finding counts against the run: active, or its waiver expired.
func IsLive(f *Finding) bool {
	return f.Disposition == exceptions.DispositionActive || f.Disposition == exceptions.DispositionExpiredWaiver
}

// gates says whether a result of the enforcement mode blocks: only `mandatory` does. A rule that declares no
// mode (the as-built families) gates as `mandatory`. Advisory results keep their verdicts and findings; shadow
// results are recorded only.
func gates(mode policy.Enforcement) bool {
	return mode == "" || mode == policy.EnforcementMandatory
}

// RuleGates says whether a rule blocks because of the verdict. A rule with obligations (a typed rule, a review rule)
// gates when an obligation that counts (no precedence entry supersedes it) has the verdict and a gating mode: each
// obligation takes the mode of its environment, so an advisory violation in one environment never hides a mandatory
// unknown in another, whatever the worst verdict of the rule is. Another rule gates through its own verdict and mode.
func RuleGates(r *RuleResult, verdict RuleVerdict) bool {
	if len(r.Obligations) == 0 {
		return r.Verdict == verdict && gates(r.Enforcement)
	}

	return slices.ContainsFunc(r.Obligations, func(o ObligationResult) bool {
		return !o.Superseded() && o.Verdict == verdict && gates(o.Enforcement)
	})
}

// IsFailing says whether a finding blocks: it is live, its enforcement mode gates, it is a violation (a binding gap
// finding of a typed rule is `unknown` and gates through the rule verdict), and it is at or above the failure threshold.
func IsFailing(f *Finding, failOn diag.Severity) bool {
	return IsLive(f) && gates(f.Enforcement) && (f.Result == "" || f.Result == VerdictViolation) &&
		severityRank(string(f.Severity)) >= severityRank(string(failOn))
}

// ComputeExitCode gives the exit code. Only rules and findings with enforcement `mandatory` count (a rule with no
// declared enforcement is mandatory). The first true condition wins:
//  1. a rule has the verdict `error`: 7;
//  2. an `active` or `expired-waiver` violation at or above failOn: 1 (a waived violation stays a violation, but is not live);
//  3. a rule is `unknown` and its effective `onUnknown` is `fail`: 4;
//  4. else 0.
func ComputeExitCode(rules []RuleResult, findings []Finding, failOn diag.Severity) int {
	if slices.ContainsFunc(rules, func(r RuleResult) bool { return RuleGates(&r, VerdictError) }) {
		return diag.Internal
	}

	for i := range findings {
		if IsFailing(&findings[i], failOn) {
			return diag.Violation
		}
	}

	if slices.ContainsFunc(rules, func(r RuleResult) bool {
		return RuleGates(&r, VerdictUnknown) && r.Plan.EvidenceRequirements.OnUnknown == policy.UnknownFail
	}) {
		return diag.Unknown
	}

	return diag.OK
}

// Summarize counts the rules and findings of a run.
func Summarize(rules []RuleResult, findings []Finding, failOn diag.Severity, unknownPolicy policy.UnknownDisposition) RunSummary {
	var counts RuleCounts

	verdicts := make([]RuleVerdict, 0, len(rules))

	for i := range rules {
		verdicts = append(verdicts, rules[i].Verdict)

		switch rules[i].Verdict {
		case VerdictNotApplicable:
			counts.NotApplicable++
		case VerdictPass:
			counts.Pass++
		case VerdictViolation:
			counts.Violation++
		case VerdictUnknown:
			counts.Unknown++
		case VerdictError:
			counts.Error++
		}
	}

	byDisposition := func(d exceptions.Disposition) int {
		return len(slices.DeleteFunc(slices.Clone(findings), func(f Finding) bool { return f.Disposition != d }))
	}

	return RunSummary{
		Verdict:       WorstVerdict(verdicts),
		ExitCode:      ComputeExitCode(rules, findings, failOn),
		FailOn:        failOn,
		UnknownPolicy: unknownPolicy,
		Active:        byDisposition(exceptions.DispositionActive),
		Baselined:     byDisposition(exceptions.DispositionBaselined),
		Waived:        byDisposition(exceptions.DispositionWaived),
		ExpiredWaiver: byDisposition(exceptions.DispositionExpiredWaiver),
		UnknownRules:  counts.Unknown,
		Rules:         counts,
	}
}
