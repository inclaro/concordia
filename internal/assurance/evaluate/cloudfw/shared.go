package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/canonjson"
)

// tri is a three-valued truth value. Every rule check gives one, and `maybe` is what an unsupported or unresolvable
// construct gives: it is neither allow nor deny. `and` is the minimum and `or` the maximum, so allowing rules can only
// raise a result (the monotonicity that the property test checks).
type tri int8

const (
	triNo    tri = 0
	triMaybe tri = 1
	triYes   tri = 2
)

func and(a, b tri) tri { return min(a, b) }
func or(a, b tri) tri  { return max(a, b) }

// side is the result of one check point on the path of the traffic (the outbound check at the source, the inbound
// check at the destination, or one Azure NSG): its verdict, the rules behind it, and the gaps it found.
type side struct {
	rules   []RuleRef
	gaps    []assurance.Gap
	verdict tri
}

// gap records a gap of the side.
func (s *side) gap(kind, detail string) {
	s.gaps = append(s.gaps, assurance.Gap{Kind: kind, Detail: detail})
}

// rule records a rule that took part in the decision of the side.
func (s *side) rule(address, rule string) {
	s.rules = append(s.rules, RuleRef{Address: address, Rule: rule})
}

// decide combines the sides of a path: the traffic is allowed when every side allows it, denied when one side denies
// it, and unknown otherwise. The rules of the sides that have the combined verdict decide it; the gaps of the
// `maybe` sides explain an `unknown` result.
func decide(sides ...*side) Result {
	verdict := triYes
	for _, s := range sides {
		verdict = and(verdict, s.verdict)
	}

	var res Result

	for _, s := range sides {
		if s.verdict != verdict {
			continue
		}

		res.Rules = append(res.Rules, s.rules...)

		if verdict == triMaybe {
			res.Gaps = append(res.Gaps, s.gaps...)
		}
	}

	res.Verdict = verdictOf(verdict)
	if verdict == triMaybe {
		res.Reason = reasonFor(res.Gaps)
	}

	return finish(&res)
}

func verdictOf(t tri) Verdict {
	switch t {
	case triYes:
		return VerdictAllowed
	case triNo:
		return VerdictDenied
	case triMaybe:
		return VerdictUnknown
	}

	return VerdictUnknown
}

// unknownFrom is an `unknown` result for gaps found before any rule was evaluated, with the reason that the gaps
// give.
func unknownFrom(gaps []assurance.Gap) Result {
	res := unknownResult(reasonFor(gaps), gaps...)

	return finish(&res)
}

// constructGaps are the gap kinds of a construct that the evaluator does not support. Every other kind is missing
// evidence.
var constructGaps = []string{
	evaluate.GapUnsupportedKind,
	evaluate.GapUnsupportedResource,
	evaluate.GapPeeringNotEvaluated,
	evaluate.GapAddressDependent,
	evaluate.GapInvalidRange,
	evaluate.GapMixedProviders,
}

// reasonFor is the reason of an `unknown` result: an unsupported construct when every gap is a construct gap, else
// incomplete coverage.
func reasonFor(gaps []assurance.Gap) string {
	construct := func(g assurance.Gap) bool { return slices.Contains(constructGaps, g.Kind) }
	if len(gaps) > 0 && !slices.ContainsFunc(gaps, func(g assurance.Gap) bool { return !construct(g) }) {
		return evaluate.ReasonUnsupportedConstruct
	}

	return evaluate.ReasonCoverageIncomplete
}

func sortRules(refs []RuleRef) []RuleRef {
	if refs == nil {
		return []RuleRef{}
	}

	slices.SortFunc(refs, func(a, b RuleRef) int {
		return cmpChain(canonjson.CompareUTF16(a.Address, b.Address), canonjson.CompareUTF16(a.Rule, b.Rule))
	})

	return slices.Compact(refs)
}

func sortGaps(gaps []assurance.Gap) []assurance.Gap {
	if gaps == nil {
		return []assurance.Gap{}
	}

	slices.SortFunc(gaps, func(a, b assurance.Gap) int {
		return cmpChain(canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Detail, b.Detail))
	})

	return slices.Compact(gaps)
}

func cmpChain(cmps ...int) int {
	for _, c := range cmps {
		if c != 0 {
			return c
		}
	}

	return 0
}
