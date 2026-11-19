package adr

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
)

// JustificationResult is the result of GOV-ADR-003 for one linked rule (design D6, ADR-02).
type JustificationResult struct {
	// LinkedBy are the decisions that link the rule, sorted by canonical ID, then project.
	LinkedBy []*Decision
	// Open are the linking decisions that do not justify the rule; the finding names them. Empty for a pass.
	Open []*Decision
	// Verdict is `pass` (an active decision links the rule), `violation` (none does), `unknown` (every decision
	// that does not justify is unresolved: ambiguous or unverifiable), or `not-applicable` (the rule is advisory).
	Verdict conformance.RuleVerdict
	// Rule is the linked rule: the rule of a pack version.
	Rule RuleRef
	// Mandatory says that the rule is at or above `check.failOn` with effective `onUnknown: fail`.
	Mandatory bool
}

// EvaluateJustification judges every rule that a registered decision links with `enforcedBy` (design D6):
//
//   - a rule that no decision links is not judged (it has no result);
//   - a rule that is not selected (no plan; a `--rule` filter removed it) is not judged either;
//   - an advisory rule (below `failOn`, or `onUnknown` other than `fail`) is `not-applicable`;
//   - a mandatory rule with at least one linking decision that is active (applicability `active` and `expect:
//     active`) is `pass`;
//   - otherwise it is `violation`, or `unknown` when every linking decision that does not justify is unresolved
//     (ambiguous or unverifiable). The links of a `historical` decision never justify.
//
// The results are sorted by rule ref.
func EvaluateJustification(decisions []*Decision, class Classifier) []JustificationResult {
	linked := map[string]*JustificationResult{}

	for _, d := range decisions {
		for _, link := range d.Entry.EnforcedBy {
			res := linked[link.String()]
			if res == nil {
				res = &JustificationResult{Rule: RuleRef{Rule: link.Rule, PackID: link.PackID, PackVersion: link.PackVersion}}
				linked[link.String()] = res
			}

			if !slices.Contains(res.LinkedBy, d) {
				res.LinkedBy = append(res.LinkedBy, d)
			}
		}
	}

	results := make([]JustificationResult, 0, len(linked))

	for _, res := range linked {
		plan := class.Plan(res.Rule)
		if plan == nil {
			continue
		}

		res.Mandatory = class.Mandatory(plan)

		switch {
		case !res.Mandatory:
			res.Verdict = conformance.VerdictNotApplicable
		case slices.ContainsFunc(res.LinkedBy, (*Decision).Justifies):
			res.Verdict = conformance.VerdictPass
		default:
			res.Open = slices.Clone(res.LinkedBy)
			res.Verdict = conformance.VerdictViolation

			if !slices.ContainsFunc(res.LinkedBy, func(d *Decision) bool { return !d.Unresolved() }) {
				res.Verdict = conformance.VerdictUnknown
			}
		}

		results = append(results, *res)
	}

	slices.SortFunc(results, func(x, y JustificationResult) int {
		return canonjson.CompareUTF16(x.Rule.String(), y.Rule.String())
	})

	return results
}
