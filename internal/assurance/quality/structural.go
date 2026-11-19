package quality

// RuleResult is the verdict of one policy rule in the run, as the integrator reads it from the rule results.
type RuleResult struct {
	Verdict Verdict
	// Reason is the closed unknown reason of an `unknown` verdict (for example `coverage-incomplete`). Empty
	// means the rule gave none, and the scenario then says `no-evidence-supplied`.
	Reason string
	// Detail says more, when the rule has something to say.
	Detail string
}

// EvaluateStructural gives the structural result of a scenario from the verdicts of the rules that it names (spec
// "Structural scenarios use rule verdicts and coverage"). The verdict is the worst of `error`, `violation`,
// `unknown`, `pass`, so a rule that is `unknown` because extraction was incomplete makes the scenario `unknown`
// with that coverage reason, unless another rule is already worse. A named rule with no result in the map was not
// evaluated (for example `check --rule` filtered it out), and a rule that is `not-applicable` found nothing to
// check: neither is evidence for a pass, so both count as `unknown` with `no-evidence-supplied`.
//
// The result is labelled `structural`: it is evidence about the architecture graph and never a behavioral result,
// and Result.Satisfies does not let it stand in for a measured or reviewed scenario.
func EvaluateStructural(scenario *Scenario, rules map[string]RuleResult) Result {
	res := Result{Scenario: scenario.ID, Mode: ModeStructural, Verdict: VerdictUnknown, Details: []string{}}

	if len(scenario.Criterion.Rules) == 0 {
		res.Reason = ReasonNoEvidenceSupplied
		res.Details = append(res.Details, "the scenario names no rule")

		return res
	}

	worst := -1
	worstReason := ""

	for _, id := range scenario.Criterion.Rules {
		rule, found := rules[id]

		verdict, reason := effective(rule, found)

		line := id + ": " + string(rule.Verdict)
		if !found {
			line = id + ": not evaluated"
		}

		if rule.Detail != "" {
			line += " (" + rule.Detail + ")"
		}

		res.Details = append(res.Details, line)

		if verdict.severity() > worst {
			worst, res.Verdict, worstReason = verdict.severity(), verdict, reason
		}
	}

	if res.Verdict == VerdictUnknown {
		res.Reason = worstReason
	}

	return res
}

// effective gives the verdict and unknown reason that a rule contributes to worst-of.
func effective(rule RuleResult, found bool) (verdict Verdict, reason string) {
	if !found {
		return VerdictUnknown, ReasonNoEvidenceSupplied
	}

	switch rule.Verdict {
	case VerdictPass, VerdictViolation, VerdictError:
		return rule.Verdict, ""
	case VerdictUnknown:
		if rule.Reason != "" {
			return VerdictUnknown, rule.Reason
		}
	case VerdictNotApplicable:
	}

	return VerdictUnknown, ReasonNoEvidenceSupplied
}
