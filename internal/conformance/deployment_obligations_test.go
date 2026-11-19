package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// ---- deployedIn: the target taken from the model ----

// deployedWorld has `billing` deployed in the container `ns-prod`, `ledger` in `ns-batch`, and complete coverage of `placed-in`.
func deployedWorld() *world {
	w := newWorld(observation.KindPlacedIn)
	w.model = typedModel(
		intent.RelationshipDeclaration{ID: "r-1", Kind: intent.RelDeployedIn, Container: "ns-prod", Targets: []string{"billing"}, Pointer: "/relationships/r-1"},
		intent.RelationshipDeclaration{ID: "r-2", Kind: intent.RelDeployedIn, Container: "ns-batch", Targets: []string{"ledger"}, Pointer: "/relationships/r-2"},
	)

	return w
}

// placedIn makes the rule of a placement obligation whose target comes from the model.
func placedIn(typ policy.ObligationType, mode policy.TargetMode, subjects ...string) policy.RulePlan {
	return typedRule("T-1", subjects, typ, observation.KindPlacedIn, policy.DirectionFromWorkload, func(s *policy.ObligationSpec) {
		s.Relation.TargetMode = mode
		s.PassCompleteness = policy.CompletenessAny
	})
}

// deployPlacement is a workload of the subject, placed in a hosting target of the environment that is bound to the
// CALM element (none when the element is empty).
type deployPlacement struct {
	workload, subject, namespace, element, env string
}

// place makes the workload of the placement in its environment, the hosting target bound to the CALM element, and
// the `placed-in` fact between them.
func (w *world) place(p *deployPlacement) {
	w.add(resource(p.workload, "Deployment", p.env, p.workload), p.subject)

	if _, known := w.nodes["cbm:p:"+p.namespace]; !known {
		if p.element == "" {
			w.add(resource(p.namespace, "Namespace", p.env, p.namespace))
		} else {
			w.add(resource(p.namespace, "Namespace", p.env, p.namespace), p.element)
		}
	}

	w.edges = append(w.edges, fact(observation.KindPlacedIn, p.workload, p.namespace, func(e *observation.EvidenceEdge) { e.Environment = p.env }))
}

// depNotApplicable requires a `not-applicable` obligation without a reason whose first occurrence says why.
func depNotApplicable(t *testing.T, o *conformance.ObligationResult, detail string) {
	t.Helper()

	evalRequireEqual(t, "verdict", o.Verdict, conformance.VerdictNotApplicable)
	evalRequireEqual(t, "reason", o.Reason, "")

	if !strings.Contains(o.Occurrences[0].Detail, detail) {
		t.Fatalf("occurrences %+v, want the detail %q", o.Occurrences, detail)
	}
}

// depExpectViolationAt expects an occurrence that is a violation of an edge at the path, and whose detail has the text.
func depExpectViolationAt(t *testing.T, occ *conformance.OccurrenceResult, path, text string) {
	t.Helper()

	evalExpectEqual(t, "occurrence result", occ.Result, conformance.VerdictViolation)

	if occ.Edge == nil {
		t.Errorf("occurrence has no edge: %+v", occ)
	}

	if occ.Location == nil {
		t.Errorf("occurrence has no location: %+v", occ)
	} else {
		evalExpectEqual(t, "occurrence path", occ.Location.Path, path)
	}

	if !strings.Contains(occ.Detail, text) {
		t.Errorf("occurrence detail %q, want %q", occ.Detail, text)
	}
}

// depObligationsByKey builds the obligations of the rule and keys them by subject and environment.
func depObligationsByKey(w *world, rule *policy.RulePlan) map[string]conformance.Obligation {
	byKey := map[string]conformance.Obligation{}

	built := conformance.BuildObligations(rule, w.model, w.typed.Workspace, w.loaded, nil)
	for i := range built {
		byKey[built[i].Subject+"/"+built[i].Environment] = built[i]
	}

	return byKey
}

func TestBuildObligationsTakesTheTargetFromTheModel(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	declared := placedIn(policy.ObligationRequired, policy.TargetDeclaredContainers, "billing", "ledger", "ns-prod")
	byKey := depObligationsByKey(w, &declared)

	billing := byKey["billing/production"]
	if !slices.Equal(billing.Relation.Target, []string{"ns-prod"}) {
		t.Errorf("billing relation = %+v", billing.Relation)
	}

	evalExpectEqual(t, "billing target mode", billing.Relation.TargetMode, policy.TargetDeclaredContainers)

	if got := byKey["ledger/production"].Relation.Target; !slices.Equal(got, []string{"ns-batch"}) {
		t.Errorf("ledger target = %v, want its own container", got)
	}

	if billing.Placement == nil {
		t.Fatalf("billing placement = %+v", billing.Placement)
	}

	evalExpectEqual(t, "billing container", billing.Placement.Container, "ns-prod")
	evalExpectEqual(t, "billing relationship", billing.Placement.RelationshipID, "r-1")

	// A subject that no relationship lists has no container, and the obligation keeps the mode.
	unlisted := byKey["ns-prod/production"]
	evalExpectEqual(t, "ns-prod targets", len(unlisted.Relation.Target), 0)
	evalExpectEqual(t, "ns-prod target mode", unlisted.Relation.TargetMode, policy.TargetDeclaredContainers)

	if unlisted.Placement != nil {
		t.Errorf("ns-prod placement = %+v", unlisted.Placement)
	}
}

func TestBuildObligationsKeepsTheIDWhateverTheModelSays(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	declared := placedIn(policy.ObligationRequired, policy.TargetDeclaredContainers, "billing", "ledger", "ns-prod")
	other := placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")

	// The same rule and subject give one ID whatever the model says, and the two targets are two relations.
	again := conformance.BuildObligations(&declared, typedModel(), w.typed.Workspace, w.loaded, nil)
	if again[0].ID != depObligationsByKey(w, &declared)["billing/production"].ID {
		t.Error("the obligation ID depends on the model")
	}

	forbidden := conformance.BuildObligations(&other, w.model, w.typed.Workspace, w.loaded, nil)
	evalExpectEqual(t, "forbidden target mode", forbidden[0].Relation.TargetMode, policy.TargetOtherHostingTargets)

	if !slices.Equal(forbidden[0].Relation.Target, []string{"ns-prod"}) {
		t.Errorf("forbidden relation = %+v", forbidden[0].Relation)
	}
}

func TestTargetIsTheContainerOfTheSubject(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	// billing is placed in a namespace that is bound to the container of ledger; ledger is placed in the container of billing.
	w.place(&deployPlacement{"wl-billing", "billing", "ns-b", "ns-batch", "production"})
	w.place(&deployPlacement{"wl-ledger", "ledger", "ns-p", "ns-prod", "production"})
	w.plans = []policy.RulePlan{placedIn(policy.ObligationRequired, policy.TargetDeclaredContainers, "billing", "ledger")}

	out := w.evaluate()
	rule := typedRuleResult(t, &out, "T-1")

	for _, subject := range []string{"billing", "ledger"} {
		o := obligationOf(t, rule, subject, "production")
		// a placement in the container of the other node must not satisfy it
		evalExpectEqual(t, subject+" verdict", o.Verdict, conformance.VerdictViolation)
		evalExpectEqual(t, subject+" passes", countResults(o, conformance.VerdictPass), 0)
	}

	// The placement in its own container does.
	w = deployedWorld()
	w.place(&deployPlacement{"wl-billing", "billing", "ns-p", "ns-prod", "production"})
	w.plans = []policy.RulePlan{placedIn(policy.ObligationRequired, policy.TargetDeclaredContainers, "billing")}

	out = w.evaluate()
	if o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production"); o.Verdict != conformance.VerdictPass {
		t.Fatalf("verdict %s (%s)", o.Verdict, o.Detail)
	}
}

func TestSubjectWithNoDeclaredPlacementIsNotApplicable(t *testing.T) {
	t.Parallel()

	for name, mode := range map[string]policy.TargetMode{
		"deployedIn true":  policy.TargetDeclaredContainers,
		"deployedIn false": policy.TargetOtherHostingTargets,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := deployedWorld()
			// ns-prod is a CALM element that no deployed-in relationship lists.
			w.place(&deployPlacement{"wl", "ns-prod", "ns-p", "ns-prod", "production"})
			w.plans = []policy.RulePlan{placedIn(policy.ObligationRequired, mode, "billing", "ns-prod")}

			out := w.evaluate()
			rule := typedRuleResult(t, &out, "T-1")

			depNotApplicable(t, obligationOf(t, rule, "ns-prod", "production"), "declares no placement")

			if billing := obligationOf(t, rule, "billing", "production"); billing.Verdict == conformance.VerdictNotApplicable {
				t.Errorf("billing is deployed and must have a verdict: %s", billing.Verdict)
			}
		})
	}
}

// ---- deployedIn: false ----

func TestPlacementInAnotherBoundTargetIsAViolation(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	w.place(&deployPlacement{"wl", "billing", "ns-d", "ns-dev", "production"})
	w.plans = []policy.RulePlan{placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")}

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
	occ := occurrenceOfClass(t, o, conformance.OccurrenceMatching)

	evalExpectEqual(t, "verdict", o.Verdict, conformance.VerdictViolation)
	depExpectViolationAt(t, occ, "k8s/prod/wl.yaml", "placed-in")
}

func TestPlacementInTheDeclaredContainerIsNotEvaluated(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	w.place(&deployPlacement{"wl", "billing", "ns-p", "ns-prod", "production"})
	w.plans = []policy.RulePlan{placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")}

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")

	if o.Verdict != conformance.VerdictPass {
		t.Fatalf("verdict %s (%s)", o.Verdict, o.Detail)
	}

	for i := range o.Occurrences {
		if o.Occurrences[i].Edge != nil {
			t.Errorf("the fact in the declared container was evaluated: %+v", o.Occurrences[i])
		}
	}

	// With partial coverage the pass is a claim of absence, and it needs complete coverage.
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindPlacedIn)

	out = w.evaluate()
	o = obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
	evalExpectEqual(t, "partial coverage verdict", o.Verdict, conformance.VerdictUnknown)
	evalExpectEqual(t, "partial coverage reason", o.Reason, conformance.ReasonCoverageIncomplete)
}

func TestHostingTargetWithNoBindingIsABindingGap(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	w.place(&deployPlacement{"wl", "billing", "ns-u", "", "production"}) // the namespace has no binding
	w.plans = []policy.RulePlan{placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")}

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
	gap := occurrenceOfClass(t, o, conformance.OccurrenceBindingGap)

	evalExpectEqual(t, "verdict", o.Verdict, conformance.VerdictUnknown)
	evalExpectEqual(t, "reason", o.Reason, conformance.ReasonBindingGap)
	evalExpectEqual(t, "next action", gap.NextAction, conformance.NextActionResolveBinding)

	if gap.Edge == nil {
		t.Fatalf("gap has no edge: %+v", gap)
	}
}

func TestHostingTargetWithAmbiguousBindingIsAmbiguous(t *testing.T) {
	t.Parallel()

	w := deployedWorld()
	w.place(&deployPlacement{"wl", "billing", "ns-a", "ns-dev", "production"})
	w.set.Ambiguous = map[string][]string{"cbm:p:ns-a": {"b-1", "b-2"}}
	w.plans = []policy.RulePlan{placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")}

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")

	evalExpectEqual(t, "verdict", o.Verdict, conformance.VerdictUnknown)
	evalExpectEqual(t, "reason", o.Reason, conformance.ReasonAmbiguousBinding)
}

// reverseWorld is the reverse constellation: the workload is bound to the container of billing and the hosting target is
// bound to billing, in the environment.
func reverseWorld(env string) *world {
	w := deployedWorld()
	w.place(&deployPlacement{"wl", "ns-prod", "ns-b", "billing", env})

	return w
}

// scopedCoverage is the coverage of `placed-in`: the project record and one record for each environment, with the status of each.
func scopedCoverage(project, production, staging observation.CoverageStatus) []observation.PredicateCoverage {
	out := make([]observation.PredicateCoverage, 0, 3)

	for scope, status := range map[observation.CoverageScope]observation.CoverageStatus{
		{Project: "p"}: project, {Environment: "production"}: production, {Environment: "staging"}: staging,
	} {
		rec := observation.PredicateCoverage{Predicate: string(observation.KindPlacedIn), Scope: scope, Status: status, LossModes: []observation.LossMode{}, Reasons: []string{}}
		if status != observation.CoverageComplete {
			rec.Reasons = []string{"test: " + string(status)}
		}

		out = append(out, rec)
	}

	return out
}

// An obligation of an environment is judged by the coverage record of its predicate for that environment, not by
// the project record: a gap of production does not make a complete staging obligation unknown, and a gap of staging does.
func TestObligationOfAnEnvironmentUsesTheCoverageOfThatEnvironment(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		project, production, staging observation.CoverageStatus
		wantProduction, wantStaging  conformance.RuleVerdict
	}{
		"production gap": {
			project: observation.CoveragePartial, production: observation.CoveragePartial, staging: observation.CoverageComplete,
			wantProduction: conformance.VerdictUnknown, wantStaging: conformance.VerdictPass,
		},
		"staging gap": {
			project: observation.CoveragePartial, production: observation.CoverageComplete, staging: observation.CoveragePartial,
			wantProduction: conformance.VerdictPass, wantStaging: conformance.VerdictUnknown,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := deployedWorld()
			rule := placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")
			rule.Obligation.Scope.Environments = []string{"production", "staging"}
			rule.Obligation.PassCompleteness = policy.CompletenessComplete
			w.plans = []policy.RulePlan{rule}
			w.scope.Predicates = scopedCoverage(tc.project, tc.production, tc.staging)

			out := w.evaluate()
			rr := typedRuleResult(t, &out, "T-1")

			if got := obligationOf(t, rr, "billing", "production").Verdict; got != tc.wantProduction {
				t.Errorf("production = %s, want %s", got, tc.wantProduction)
			}

			if got := obligationOf(t, rr, "billing", "staging").Verdict; got != tc.wantStaging {
				t.Errorf("staging = %s, want %s", got, tc.wantStaging)
			}
		})
	}
}

func TestReversePlacementOfADerivedPlacementFact(t *testing.T) {
	t.Parallel()

	w := reverseWorld("production")
	w.plans = []policy.RulePlan{
		placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing"),
		func() policy.RulePlan {
			p := placedIn(policy.ObligationRequired, policy.TargetDeclaredContainers, "billing")
			p.RuleID = "T-2"

			return p
		}(),
	}

	out := w.evaluate()

	forbidden := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
	reverse := occurrenceOfClass(t, forbidden, conformance.OccurrenceWrongDirection)

	evalExpectEqual(t, "verdict", forbidden.Verdict, conformance.VerdictViolation)
	evalExpectEqual(t, "decisive", reverse.Decisive, true)
	depExpectViolationAt(t, reverse, "k8s/prod/wl.yaml", "unexpected placement")

	// No spurious pass of the absence next to the violation.
	if countResults(forbidden, conformance.VerdictPass) != 0 {
		t.Errorf("a pass next to the reverse violation: %+v", forbidden.Occurrences)
	}

	required := obligationOf(t, typedRuleResult(t, &out, "T-2"), "billing", "production")
	if required.Verdict == conformance.VerdictPass {
		t.Fatalf("the reverse fact satisfied the required obligation: %s %+v", required.Verdict, required.Occurrences)
	}

	evalRequireEqual(t, "passes of the required obligation", countResults(required, conformance.VerdictPass), 0)
}

func TestReversePlacementOfAnIneligibleFactIsNotAViolation(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		edit   func(*policy.ObligationSpec)
		reason conformance.UnknownReason
		class  conformance.OccurrenceClass
	}{
		"role": {
			edit: func(s *policy.ObligationSpec) {
				s.Roles = []observation.EvidenceRole{observation.RoleRuntimeObservation}
			},
			reason: conformance.ReasonCoverageIncomplete, class: conformance.OccurrenceRoleMismatch,
		},
		"resolution": {
			edit:   func(s *policy.ObligationSpec) { s.MinimumResolution = observation.ResolutionExact },
			reason: conformance.ReasonCoverageIncomplete, class: conformance.OccurrenceMatching,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := reverseWorld("production")
			rule := placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")
			tc.edit(rule.Obligation)
			w.plans = []policy.RulePlan{rule}

			out := w.evaluate()
			o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")

			if o.Verdict == conformance.VerdictViolation {
				t.Fatalf("an ineligible reverse fact is a violation: %s %+v", o.Verdict, o.Occurrences)
			}

			evalRequireEqual(t, "violations", countResults(o, conformance.VerdictViolation), 0)

			if got := occurrenceOfClass(t, o, tc.class); got.Result != conformance.VerdictUnknown {
				t.Fatalf("occurrence = %+v", got)
			}
		})
	}
}

func TestReversePlacementIsScopedToTheEnvironmentOfTheObligation(t *testing.T) {
	t.Parallel()

	w := reverseWorld("staging")
	rule := placedIn(policy.ObligationForbidden, policy.TargetOtherHostingTargets, "billing")
	rule.Obligation.Scope.Environments = []string{"production", "staging"}
	w.plans = []policy.RulePlan{rule}

	out := w.evaluate()
	rr := typedRuleResult(t, &out, "T-1")

	production := obligationOf(t, rr, "billing", "production")
	evalExpectEqual(t, "production verdict", production.Verdict, conformance.VerdictPass)

	if slices.ContainsFunc(production.Occurrences, func(o conformance.OccurrenceResult) bool {
		return o.Class == conformance.OccurrenceWrongDirection
	}) {
		t.Fatalf("production uses the staging fact: %s %+v", production.Verdict, production.Occurrences)
	}

	staging := obligationOf(t, rr, "billing", "staging")
	reverse := occurrenceOfClass(t, staging, conformance.OccurrenceWrongDirection)
	evalExpectEqual(t, "staging verdict", staging.Verdict, conformance.VerdictViolation)
	evalExpectEqual(t, "reverse environment", reverse.Environment, "staging")
}

func TestReversePlacementIsOnlyForTheNegatedForbiddenTarget(t *testing.T) {
	t.Parallel()

	// The other forms of the same rule do not read the hosting side.
	for name, rule := range map[string]policy.RulePlan{
		"forbidden with deployedIn true": placedIn(policy.ObligationForbidden, policy.TargetDeclaredContainers, "billing"),
		"allowed with deployedIn false":  placedIn(policy.ObligationAllowed, policy.TargetOtherHostingTargets, "billing"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := reverseWorld("production")
			w.plans = []policy.RulePlan{rule}

			out := w.evaluate()

			o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
			if slices.ContainsFunc(o.Occurrences, func(occ conformance.OccurrenceResult) bool {
				return occ.Class == conformance.OccurrenceWrongDirection && occ.Result == conformance.VerdictViolation
			}) {
				t.Fatalf("unexpected reverse violation: %+v", o.Occurrences)
			}
		})
	}
}
