package adr

import (
	"github.com/inclaro/concordia/internal/conformance"
)

// EnforcementResult is the enforcement coverage of one registered decision (GOV-ADR-004, ADR-05). It never
// depends on presence: a decision that is committed and active but has no rule keeps its other two states.
type EnforcementResult struct {
	Decision *Decision
	// State is `enforced` (a linked rule is mandatory), `advisory-only` (rules are linked, none is mandatory),
	// `unlinked`, or `not-applicable` (the decision is not active: superseded, inactive, or expected historical).
	State EnforcementState
	// Verdict is set only for a unit.
	Verdict conformance.RuleVerdict
	// Reason is the finding reason of a violation: `enforcement-gap` (no rule) or `advisory-only` (no mandatory rule).
	Reason Reason
	// Rules are the linked rules in the ref form, sorted.
	Rules []string
	// Required says that the entry sets `enforcement: required`.
	Required bool
	// Unit says that the decision is a unit of GOV-ADR-004: it requires enforcement.
	Unit bool
}

// EvaluateEnforcement gives the enforcement coverage of every decision, in the order of decisions (design D6).
//
// The state follows the links: a linked rule that is selected is mandatory when the classifier says so; a linked
// rule that the run did not select (a `--rule` filter) cannot be classified and counts as mandatory, because the
// link is valid (design D11) and the filter only chooses which rules run. A decision that is not active gets
// `not-applicable`. Only a decision with `enforcement: required` is a unit: an active one passes with a mandatory
// rule, else it is a `violation` (`enforcement-gap`, or `advisory-only` when only advisory rules are linked); an
// inactive one is `not-applicable`; one whose activity is unresolved (ambiguous or unverifiable) is `unknown`.
func EvaluateEnforcement(decisions []*Decision, class Classifier) []EnforcementResult {
	results := make([]EnforcementResult, len(decisions))

	for i, dec := range decisions {
		res := EnforcementResult{
			Decision: dec, Required: dec.Entry.Enforcement == EnforcementRequired, Rules: dec.RuleRefs(),
			State: linkState(dec, class),
		}

		if !dec.Justifies() && !dec.Unresolved() {
			res.State = EnforcementNotApplicable
		}

		if res.Required {
			res.Unit = true

			switch {
			case dec.Justifies() && res.State == EnforcementEnforced:
				res.Verdict = conformance.VerdictPass
			case dec.Justifies() && res.State == EnforcementAdvisoryOnly:
				res.Verdict, res.Reason = conformance.VerdictViolation, ReasonAdvisoryOnly
			case dec.Justifies():
				res.Verdict, res.Reason = conformance.VerdictViolation, ReasonEnforcementGap
			case dec.Unresolved():
				res.Verdict = conformance.VerdictUnknown
			default:
				res.Verdict = conformance.VerdictNotApplicable
			}
		}

		results[i] = res
	}

	return results
}

// linkState gives the state that the links of the decision alone give.
func linkState(dec *Decision, class Classifier) EnforcementState {
	if len(dec.Entry.EnforcedBy) == 0 {
		return EnforcementUnlinked
	}

	for _, link := range dec.Entry.EnforcedBy {
		plan := class.Plan(link)
		if plan == nil || class.Mandatory(plan) {
			return EnforcementEnforced
		}
	}

	return EnforcementAdvisoryOnly
}
