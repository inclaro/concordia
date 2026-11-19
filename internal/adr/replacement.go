package adr

import (
	"slices"

	"github.com/inclaro/concordia/internal/conformance"
)

// ReplacementResult is the result of GOV-ADR-002 for one registered decision (ADR-04, A12): whether the decision
// is the active governance that the register expects, and, for a historical decision, whether every link is
// transferred.
type ReplacementResult struct {
	Decision *Decision
	Verdict  conformance.RuleVerdict
	// Reason is the finding reason of a violation (`superseded`, `inactive`, `historical-link`) or the evidence
	// reason of an `unknown` (the applicability reason, `not-committed`, a no-revision or no-capability reason).
	Reason string
	// Subjects and Rules are what the finding names: for `superseded` the subjects and rules of the decision, for
	// `historical-link` the subjects and rules that no active decision declares and no `released` entry lists.
	Subjects []string
	Rules    []string
	// Candidates are the successors that the history names (effective links), never applied (`applied: false`).
	Candidates []conformance.CandidateSuccessor
}

// EvaluateReplacement reviews every registered decision against the expectation of its entry (design D6):
//
//   - `expect: active` and applicability `active` is `pass`; `superseded` is a `violation` with reason `superseded`
//     naming the subjects, the rules, and the candidate successors; `inactive` is a `violation` with reason `inactive`;
//   - `expect: historical` passes only when every governed subject and every enforcement rule is released (by
//     subject ref or by rule ref) or declared by another decision that justifies (applicability `active` and `expect:
//     active`); otherwise it is a `violation` with reason `historical-link` naming what was not transferred;
//   - a decision whose applicability is `ambiguous` or `unknown` (presence not committed, history anomaly, no
//     evidence) is `unknown`.
//
// A successor never receives the governance of the replaced decision: only the register does that.
func EvaluateReplacement(decisions []*Decision) []ReplacementResult {
	results := make([]ReplacementResult, len(decisions))

	for i, dec := range decisions {
		res := ReplacementResult{
			Decision: dec, Subjects: []string{}, Rules: []string{}, Candidates: candidatesOf(dec),
		}

		switch {
		case dec.Applicability == ApplicabilityAmbiguous || dec.Applicability == ApplicabilityUnknown:
			res.Verdict, res.Reason = conformance.VerdictUnknown, dec.Reason
		case dec.Historical():
			res.Subjects, res.Rules = untransferred(dec, decisions)

			if len(res.Subjects)+len(res.Rules) == 0 {
				res.Verdict = conformance.VerdictPass
			} else {
				res.Verdict, res.Reason = conformance.VerdictViolation, string(ReasonHistoricalLink)
			}
		case dec.Applicability == ApplicabilityActive:
			res.Verdict = conformance.VerdictPass
		case dec.Applicability == ApplicabilitySuperseded:
			res.Verdict, res.Reason = conformance.VerdictViolation, string(ReasonSuperseded)
			res.Subjects, res.Rules = slices.Clone(dec.Subjects), dec.RuleRefs()
		default:
			res.Verdict, res.Reason = conformance.VerdictViolation, string(ReasonInactive)
		}

		results[i] = res
	}

	return results
}

// candidatesOf gives the successors named by effective history links, sorted by path; never nil.
func candidatesOf(dec *Decision) []conformance.CandidateSuccessor {
	effective := dec.EffectiveSuccessors()
	out := make([]conformance.CandidateSuccessor, 0, len(effective))

	for _, n := range effective {
		out = append(out, conformance.CandidateSuccessor{ID: n.ID, Path: n.Path, Applied: false})
	}

	return out
}

// untransferred gives the subjects and the rule refs of a historical decision that are neither released by its
// entry nor declared by another decision that justifies.
func untransferred(dec *Decision, all []*Decision) (subjects, rules []string) {
	released := map[string]bool{}
	for i := range dec.Entry.Released {
		released[dec.Entry.Released[i].Key()] = true
	}

	declaredSubjects, declaredRules := map[string]bool{}, map[string]bool{}

	for _, other := range all {
		if other == dec || !other.Justifies() {
			continue
		}

		for _, subject := range other.Subjects {
			declaredSubjects[subject] = true
		}

		for _, ref := range other.RuleRefs() {
			declaredRules[ref] = true
		}
	}

	subjects, rules = []string{}, []string{}

	for _, subject := range dec.Subjects {
		if !released[subject] && !declaredSubjects[subject] {
			subjects = append(subjects, subject)
		}
	}

	for _, ref := range dec.RuleRefs() {
		if !released[ref] && !declaredRules[ref] {
			rules = append(rules, ref)
		}
	}

	return subjects, rules
}
