package conformance_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// ---- passCompleteness ----

func passWorld(edit func(*observation.EvidenceEdge)) *world {
	w := newWorld(observation.KindUsesImage)
	w.add(resource("billing-a", "Deployment", "production", "billing-a"), "billing")
	w.add(observation.ObservedNode{ObservedID: "cbm:p:img", Label: observation.LabelImage, QualifiedName: "registry/billing"})
	w.edges = []*observation.EvidenceEdge{fact(observation.KindUsesImage, "billing-a", "img", func(e *observation.EvidenceEdge) {
		e.Environment = "production"

		if edit != nil {
			edit(e)
		}
	})}

	return w
}

// passRule makes a rule with the default `completeness: complete` and the given `passCompleteness`.
func passRule(pass policy.Completeness) policy.RulePlan {
	return typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload,
		func(s *policy.ObligationSpec) { s.PassCompleteness = pass })
}

func resultOf(t *testing.T, w *world) *conformance.ObligationResult {
	t.Helper()

	out := w.evaluate()

	return obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
}

// depRequireUnknownBecause requires an `unknown` obligation with the reason.
func depRequireUnknownBecause(t *testing.T, o *conformance.ObligationResult, reason conformance.UnknownReason) {
	t.Helper()

	evalRequireEqual(t, "verdict", o.Verdict, conformance.VerdictUnknown)
	evalRequireEqual(t, "reason", o.Reason, reason)
}

func TestPresencePassesUnderPartialCoverageAndAbsenceStaysUnknown(t *testing.T) {
	t.Parallel()

	w := passWorld(nil)
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
	w.plans = []policy.RulePlan{passRule(policy.CompletenessAny)}

	if o := resultOf(t, w); o.Verdict != conformance.VerdictPass {
		t.Fatalf("presence: verdict %s reason %q (%s)", o.Verdict, o.Reason, o.Detail)
	}

	absent := passWorld(nil)
	absent.edges = nil
	absent.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
	absent.plans = []policy.RulePlan{passRule(policy.CompletenessAny)}

	depRequireUnknownBecause(t, resultOf(t, absent), conformance.ReasonCoverageIncomplete)

	// With complete coverage the absence is a violation, as before.
	absent.scope.Predicates = records(observation.CoverageComplete, observation.KindUsesImage)

	if o := resultOf(t, absent); o.Verdict != conformance.VerdictViolation {
		t.Fatalf("absence with complete coverage: verdict %s", o.Verdict)
	}
}

func TestPassCompletenessDefaultsToCompleteness(t *testing.T) {
	t.Parallel()

	w := passWorld(nil)
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
	w.plans = []policy.RulePlan{passRule("")}

	depRequireUnknownBecause(t, resultOf(t, w), conformance.ReasonCoverageIncomplete)

	// An explicit `partial` pass accepts partial coverage and nothing less.
	w.plans = []policy.RulePlan{passRule(policy.CompletenessPartial)}

	if o := resultOf(t, w); o.Verdict != conformance.VerdictPass {
		t.Fatalf("partial pass: verdict %s (%s)", o.Verdict, o.Detail)
	}

	w.scope.Predicates = records(observation.CoverageUnknown, observation.KindUsesImage)

	if o := resultOf(t, w); o.Verdict != conformance.VerdictUnknown {
		t.Fatalf("a partial pass on unknown coverage: verdict %s", o.Verdict)
	}
}

func TestPossiblyLossyFactsBlockOnlyAPassThatNeedsCompleteCoverage(t *testing.T) {
	t.Parallel()

	lossy := func(e *observation.EvidenceEdge) {
		e.PossiblyLossy, e.LossModes = true, []string{observation.LossMergedOccurrences}
	}

	for name, tc := range map[string]struct {
		pass policy.Completeness
		want conformance.RuleVerdict
	}{
		"unset":    {"", conformance.VerdictUnknown},
		"complete": {policy.CompletenessComplete, conformance.VerdictUnknown},
		"partial":  {policy.CompletenessPartial, conformance.VerdictPass},
		"any":      {policy.CompletenessAny, conformance.VerdictPass},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := passWorld(lossy)
			w.plans = []policy.RulePlan{passRule(tc.pass)}

			if o := resultOf(t, w); o.Verdict != tc.want {
				t.Fatalf("verdict %s (%s), want %s", o.Verdict, o.Detail, tc.want)
			}
		})
	}
}

func TestAFailedScopeStillBlocksWhateverThePassNeeds(t *testing.T) {
	t.Parallel()

	blocks := map[string]func(w *world){
		"failed scope": func(w *world) {
			w.scope.Projects = []string{"p", "q"}
			w.scope.NodeProjects = map[string][]string{"billing": {"q"}}
			w.scope.Failures = []observation.ScopeFailure{{Project: "q", Reason: "CO-OBS-001 query failed"}}
		},
		"limit exceeded": func(w *world) {
			w.scope.Breaches = []conformance.LimitBreach{{Limit: conformance.LimitGraphMaxEdges, Configured: 1, Observed: 2}}
		},
		"capability regression": func(w *world) {
			w.typed.Regressions = []observation.CapabilityRegression{{Family: "uses-image", Was: observation.SupportSupported, Now: observation.SupportUnsupported}}
		},
		"unsupported family": func(w *world) {
			w.scope.Predicates = records(observation.CoverageUnsupported, observation.KindUsesImage)
		},
	}

	for name, block := range blocks {
		for _, pass := range []policy.Completeness{policy.CompletenessAny, policy.CompletenessPartial} {
			t.Run(name+"/"+string(pass), func(t *testing.T) {
				t.Parallel()

				w := passWorld(nil)
				block(w)
				w.plans = []policy.RulePlan{passRule(pass)}

				if o := resultOf(t, w); o.Verdict != conformance.VerdictUnknown {
					t.Fatalf("verdict %s (%s)", o.Verdict, o.Detail)
				}
			})
		}
	}

	w := passWorld(nil)
	blocks["failed scope"](w)
	w.plans = []policy.RulePlan{passRule(policy.CompletenessAny)}

	o := resultOf(t, w)
	evalExpectEqual(t, "reason", o.Reason, conformance.ReasonCoverageIncomplete)

	if !strings.Contains(o.Detail, "q") {
		t.Fatalf("reason %q (%s)", o.Reason, o.Detail)
	}
}

func TestPassCompletenessDoesNotChangeTheForbiddenAbsence(t *testing.T) {
	t.Parallel()

	// A forbidden rule passes on an absence, which needs `completeness`, never `passCompleteness`.
	w := passWorld(nil)
	w.edges = nil
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationForbidden, observation.KindUsesImage, policy.DirectionFromWorkload,
		func(s *policy.ObligationSpec) { s.PassCompleteness = policy.CompletenessAny })}

	depRequireUnknownBecause(t, resultOf(t, w), conformance.ReasonCoverageIncomplete)
}
