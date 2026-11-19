package adr

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

// justificationUnits are the units of GOV-ADR-003: one per selected rule that a decision links.
func (e *engine) justificationUnits(plan *policy.RulePlan) []unit {
	results := EvaluateJustification(e.decisions, e.class)
	units := make([]unit, 0, len(results))

	for i := range results {
		res := &results[i]
		ref := res.Rule.String()

		if isSettledVerdict(res.Verdict) {
			units = append(units, unit{subject: ref, verdict: res.Verdict})

			continue
		}

		finding := e.justificationFinding(plan, res)
		units = append(units, unit{subject: ref, verdict: res.Verdict, code: string(ReasonRuleUnjustified), message: finding.Observed, finding: &finding})
	}

	return units
}

// openDecisionTrace is what the finding of an unjustified rule says about its open decisions.
type openDecisionTrace struct {
	parts      []string
	related    []string
	candidates []conformance.CandidateSuccessor
}

// traceOpenDecisions describes the decisions that link a rule without justifying it, with their candidate successors.
func traceOpenDecisions(open []*Decision) openDecisionTrace {
	trace := openDecisionTrace{parts: make([]string, 0, len(open))}

	for _, dec := range open {
		trace.parts = append(trace.parts, dec.Entry.ID+" ("+stateWord(dec)+")")
		trace.related = append(trace.related, dec.ID())

		for _, c := range candidatesOf(dec) {
			trace.candidates = append(trace.candidates, c)
			trace.related = append(trace.related, c.ID)
		}
	}

	return trace
}

// ruleLinkLocation is the place of the first decision that links the rule: its register entry, at the line of the
// link when the entry has one.
func ruleLinkLocation(first *Decision, rule string) *conformance.Location {
	loc := &conformance.Location{Path: first.Entry.File, Line: first.Entry.Line}

	for _, link := range first.Entry.EnforcedBy {
		if link.String() == rule && link.Line > 0 {
			loc.Line = link.Line
		}
	}

	return loc
}

func (e *engine) justificationFinding(plan *policy.RulePlan, res *JustificationResult) conformance.Finding {
	trace := traceOpenDecisions(res.Open)
	first := res.Open[0]
	observed := "linked by " + strings.Join(trace.parts, ", ") + "; none is shown active"

	return buildFinding(plan, &findingSpec{
		Result: res.Verdict, Reason: string(ReasonRuleUnjustified), Source: res.Rule.Rule, Subject: res.Rule.Rule,
		Message:  "mandatory rule " + res.Rule.String() + " is justified by no active decision: " + observed,
		Expected: "at least one decision that links the mandatory rule " + res.Rule.String() + " is active",
		Observed: observed, Location: ruleLinkLocation(first, res.Rule.String()),
		Decision: e.red.blockOf(first, dedupeCandidates(trace.candidates)), Related: trace.related,
	})
}

// stateWord says why a linking decision does not justify its rule.
func stateWord(dec *Decision) string {
	if dec.Historical() {
		return "expected historical"
	}

	if dec.Applicability == ApplicabilityAmbiguous || dec.Applicability == ApplicabilityUnknown {
		return string(dec.Applicability) + ": " + dec.Reason
	}

	return string(dec.Applicability)
}

func dedupeCandidates(list []conformance.CandidateSuccessor) []conformance.CandidateSuccessor {
	out := []conformance.CandidateSuccessor{}

	for _, c := range list {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}

	slices.SortFunc(out, func(a, b conformance.CandidateSuccessor) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	return out
}
