package adr_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
)

const orders = "calm:node:orders-db"

func review(decisions ...*adr.Decision) []adr.ReplacementResult {
	return adr.EvaluateReplacement(decisions)
}

// wantReplacementViolation checks that the result is a violation with the reason, the subjects, and the rules of
// want.
func wantReplacementViolation(t *testing.T, res, want *adr.ReplacementResult) {
	t.Helper()

	if res.Verdict != conformance.VerdictViolation {
		t.Errorf("verdict = %s, want violation: %+v", res.Verdict, res)
	}

	if res.Reason != want.Reason {
		t.Errorf("reason = %q, want %q: %+v", res.Reason, want.Reason, res)
	}

	if !slices.Equal(res.Subjects, want.Subjects) {
		t.Errorf("subjects = %v, want %v: %+v", res.Subjects, want.Subjects, res)
	}

	if !slices.Equal(res.Rules, want.Rules) {
		t.Errorf("rules = %v, want %v: %+v", res.Rules, want.Rules, res)
	}
}

// A12: the successor is active and unregistered, so it gets no scope.
func TestReplacementSupersededDecisionListsItsLinksAndCandidates(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-0003", withApplicability(adr.ApplicabilitySuperseded, "superseded"), governing(orders), enforcedBy(coreRule),
		successor("ADR-0007", true))

	res := review(old)[0]
	wantReplacementViolation(t, &res, &adr.ReplacementResult{Reason: "superseded", Subjects: []string{orders}, Rules: []string{coreRule}})

	want := []conformance.CandidateSuccessor{{ID: "ADR-7", Path: "docs/adr/ADR-0007.md", Applied: false}}
	if !slices.Equal(res.Candidates, want) {
		t.Errorf("candidates = %+v, want %+v", res.Candidates, want)
	}
}

func TestReplacementPendingSuccessorIsNoCandidate(t *testing.T) {
	t.Parallel()

	res := review(newDecision("ADR-3", successor("ADR-7", false)))[0]
	if res.Verdict != conformance.VerdictPass || len(res.Candidates) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestReplacementInactiveDecisionIsAViolation(t *testing.T) {
	t.Parallel()

	res := review(newDecision("ADR-3", withApplicability(adr.ApplicabilityInactive, "inactive")))[0]
	if res.Verdict != conformance.VerdictViolation || res.Reason != "inactive" {
		t.Errorf("result = %+v", res)
	}
}

func TestReplacementSuccessorRegisteredWithTheSameScopePasses(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-0003", historical(), withApplicability(adr.ApplicabilitySuperseded, "superseded"), governing(orders), enforcedBy(coreRule))
	next := newDecision("ADR-0007", governing(orders), enforcedBy(coreRule))

	for _, res := range review(old, next) {
		if res.Verdict != conformance.VerdictPass {
			t.Errorf("%s: %+v", res.Decision.Entry.ID, res)
		}
	}
}

// Scenario: Historical decision keeps an untransferred link.
func TestReplacementHistoricalWithAnUntransferredSubjectFails(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-0003", historical(), governing(orders, "calm:node:orders"), enforcedBy(coreRule))
	next := newDecision("ADR-0007", governing(orders), enforcedBy(coreRule))

	res := review(old, next)[0]
	wantReplacementViolation(t, &res, &adr.ReplacementResult{Reason: "historical-link", Subjects: []string{"calm:node:orders"}})
}

// Scenario: Historical decision with a released subject.
func TestReplacementReleasedSubjectPasses(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-0003", historical(), governing(orders, "calm:node:legacy"), enforcedBy(coreRule),
		releasing(adr.Release{Ref: "calm:node:legacy", Reason: "retired"}))
	next := newDecision("ADR-0007", governing(orders), enforcedBy(coreRule))

	if res := review(old, next)[0]; res.Verdict != conformance.VerdictPass {
		t.Errorf("result = %+v", res)
	}
}

// Scenario: Historical decision with a released rule.
func TestReplacementReleasedRulePasses(t *testing.T) {
	t.Parallel()

	rule, _ := adr.ParseRuleRef(coreRule)
	old := newDecision("ADR-0003", historical(), governing(orders), enforcedBy(coreRule),
		releasing(adr.Release{Rule: &rule, Reason: "moved to a platform team"}))
	next := newDecision("ADR-0007", governing(orders))

	if res := review(old, next)[0]; res.Verdict != conformance.VerdictPass {
		t.Errorf("result = %+v", res)
	}
}

func TestReplacementHistoricalWithAnUntransferredRuleFails(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-0003", historical(), governing(orders), enforcedBy(coreRule))
	next := newDecision("ADR-0007", governing(orders))

	res := review(old, next)[0]
	wantReplacementViolation(t, &res, &adr.ReplacementResult{Reason: "historical-link", Rules: []string{coreRule}})
}

// Only a decision that justifies transfers: a successor that is itself historical or inactive does not.
func TestReplacementTransferNeedsAnActiveDecision(t *testing.T) {
	t.Parallel()

	old := newDecision("ADR-0003", historical(), governing(orders))

	for name, other := range map[string]*adr.Decision{
		"historical": newDecision("ADR-0007", historical(), governing(orders)),
		"inactive":   newDecision("ADR-0007", withApplicability(adr.ApplicabilityInactive, "inactive"), governing(orders)),
	} {
		if res := review(old, other)[0]; res.Verdict != conformance.VerdictViolation {
			t.Errorf("%s: %+v", name, res)
		}
	}
}

func TestReplacementAmbiguousOrUncommittedIsUnknown(t *testing.T) {
	t.Parallel()

	for name, d := range map[string]*adr.Decision{
		"ambiguous":     newDecision("ADR-3", withApplicability(adr.ApplicabilityAmbiguous, "cycle")),
		"not committed": newDecision("ADR-3", withApplicability(adr.ApplicabilityUnknown, "not-committed")),
		"historical":    newDecision("ADR-3", historical(), withApplicability(adr.ApplicabilityAmbiguous, "cycle")),
	} {
		if res := review(d)[0]; res.Verdict != conformance.VerdictUnknown || res.Reason == "" {
			t.Errorf("%s: %+v", name, res)
		}
	}
}
