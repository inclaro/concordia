package conformance_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// ---- 10.4: per-occurrence evaluation ----

// replicas gives a world with three workloads of billing in production and an image.
func replicas() *world {
	w := newWorld(observation.KindUsesImage)

	for _, name := range []string{"billing-a", "billing-b", "billing-c"} {
		w.add(resource(name, "Deployment", "production", name), "billing")
	}

	w.add(billingImageNode())

	return w
}

func TestOneOfThreeReplicasDoesNotSatisfyForAll(t *testing.T) {
	t.Parallel()

	w := replicas()
	w.addImageFact("billing-a", func(e *observation.EvidenceEdge) { e.Occurrence = "app" })
	w.plans = []policy.RulePlan{usesImageRule(nil)}

	out := w.evaluate()
	rule := typedRuleResult(t, &out, "T-1")
	o := obligationOf(t, rule, "billing", "production")

	if o.Verdict != conformance.VerdictViolation || rule.Verdict != conformance.VerdictViolation {
		t.Fatalf("obligation %s, rule %s; the service-level result must not be pass", o.Verdict, rule.Verdict)
	}

	if len(o.Occurrences) != 3 {
		t.Fatalf("occurrences = %+v, want one pass and two violations", o.Occurrences)
	}

	if countResults(o, conformance.VerdictPass) != 1 || countResults(o, conformance.VerdictViolation) != 2 {
		t.Fatalf("occurrences = %+v, want one pass and two violations", o.Occurrences)
	}
}

func TestExistsIsSatisfiedByOne(t *testing.T) {
	t.Parallel()

	w := replicas()
	w.addImageFact("billing-a", nil)
	w.plans = []policy.RulePlan{usesImageRule(func(s *policy.ObligationSpec) { s.Multiplicity = policy.MultiplicityExists })}

	o := w.billingObligation(t, "production")

	if o.Verdict != conformance.VerdictPass {
		t.Fatalf("exists with one satisfying workload = %s (%s)", o.Verdict, o.Detail)
	}

	if len(o.Occurrences) != 3 {
		t.Fatalf("the other workloads stay visible: %+v", o.Occurrences)
	}
}

func TestAgreement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		agreement string
		verdict   conformance.RuleVerdict
	}{
		{binding.AgreementAny, conformance.VerdictPass},
		{binding.AgreementAll, conformance.VerdictViolation},
	} {
		t.Run(tc.agreement, func(t *testing.T) {
			t.Parallel()

			w := replicas()
			w.set.Agreements = map[string]string{"billing": tc.agreement}
			w.addImageFact("billing-a", nil)
			w.plans = []policy.RulePlan{usesImageRule(nil)}

			if o := w.billingObligation(t, "production"); o.Verdict != tc.verdict {
				t.Fatalf("agreement %s = %s, want %s", tc.agreement, o.Verdict, tc.verdict)
			}
		})
	}
}

func TestRoleMismatchNamesTheMissingRole(t *testing.T) {
	t.Parallel()

	w := replicas()
	w.nodes = map[string]observation.ObservedNode{"cbm:p:billing-a": w.nodes["cbm:p:billing-a"], "cbm:p:img": w.nodes["cbm:p:img"]}
	w.bound = map[string][]string{"cbm:p:billing-a": {"billing"}}
	w.addImageFact("billing-a", nil)
	w.plans = []policy.RulePlan{usesImageRule(func(s *policy.ObligationSpec) {
		s.Roles = []observation.EvidenceRole{observation.RoleRuntimeObservation}
	})}

	o := w.billingObligation(t, "production")

	if o.Verdict != conformance.VerdictUnknown || !strings.Contains(o.Detail, "runtime-observation") {
		t.Fatalf("verdict %s detail %q, want unknown that names the runtime-observation role", o.Verdict, o.Detail)
	}

	if !strings.Contains(o.Detail, "drift") {
		t.Fatalf("declared versus runtime is drift, never a pass: %q", o.Detail)
	}

	occ := occurrenceOfClass(t, o, conformance.OccurrenceRoleMismatch)
	if occ.Role != observation.RoleParsedSourceFact || occ.Result != conformance.VerdictUnknown {
		t.Fatalf("occurrence = %+v", occ)
	}
}

// singleReplicaWorld has one workload of billing in production with a uses-image fact; edit changes the fact.
func singleReplicaWorld(edit func(*observation.EvidenceEdge)) *world {
	w := newWorld(observation.KindUsesImage)
	w.add(resource("billing-a", "Deployment", "production", "billing-a"), "billing")
	w.add(billingImageNode())
	w.addImageFact("billing-a", edit)

	return w
}

func withInferredResolution(e *observation.EvidenceEdge) {
	e.Resolution = observation.ResolutionInferred
}

func withMergedOccurrencesLoss(e *observation.EvidenceEdge) {
	e.PossiblyLossy, e.LossModes = true, []string{observation.LossMergedOccurrences}
}

// wantVerdict checks the verdict of an obligation.
func wantVerdict(want conformance.RuleVerdict) func(*testing.T, *conformance.ObligationResult) {
	return func(t *testing.T, o *conformance.ObligationResult) {
		t.Helper()

		if o.Verdict != want {
			t.Fatalf("verdict %s, want %s (%s)", o.Verdict, want, o.Detail)
		}
	}
}

// wantUnknownBecause checks that an obligation is unknown with a reason.
func wantUnknownBecause(reason conformance.UnknownReason) func(*testing.T, *conformance.ObligationResult) {
	return func(t *testing.T, o *conformance.ObligationResult) {
		t.Helper()

		requireUnknown(t, o, reason)
	}
}

// wantUnknownNaming checks that an obligation is unknown and that its detail names the text.
func wantUnknownNaming(text string) func(*testing.T, *conformance.ObligationResult) {
	return func(t *testing.T, o *conformance.ObligationResult) {
		t.Helper()

		if o.Verdict != conformance.VerdictUnknown {
			t.Fatalf("verdict %s detail %q", o.Verdict, o.Detail)
		}

		if !strings.Contains(o.Detail, text) {
			t.Fatalf("verdict %s detail %q", o.Verdict, o.Detail)
		}
	}
}

func TestMinimumResolutionAndCompleteness(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		edit    func(*observation.EvidenceEdge)
		spec    func(*policy.ObligationSpec)
		check   func(*testing.T, *conformance.ObligationResult)
		name    string
		partial bool
	}{
		{name: "present with sufficient resolution passes", check: wantVerdict(conformance.VerdictPass)},
		{name: "inferred resolution is below the default minimum", edit: withInferredResolution, check: wantVerdict(conformance.VerdictUnknown)},
		{
			name: "a lower minimum resolution accepts it", edit: withInferredResolution, check: wantVerdict(conformance.VerdictPass),
			spec: func(s *policy.ObligationSpec) { s.MinimumResolution = observation.ResolutionInferred },
		},
		{
			name: "partial coverage blocks a pass under completeness complete", partial: true,
			check: wantUnknownBecause(conformance.ReasonCoverageIncomplete),
		},
		{
			name: "partial coverage is enough for completeness partial", partial: true, check: wantVerdict(conformance.VerdictPass),
			spec: func(s *policy.ObligationSpec) { s.Completeness = policy.CompletenessPartial },
		},
		{
			name: "a possibly lossy fact never gives a complete pass", edit: withMergedOccurrencesLoss,
			check: wantUnknownNaming(observation.LossMergedOccurrences),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := singleReplicaWorld(tc.edit)
			if tc.partial {
				w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
			}

			w.plans = []policy.RulePlan{usesImageRule(tc.spec)}
			tc.check(t, w.billingObligation(t, "production"))
		})
	}
}

// unknownReasonVariant changes the base world or the obligation of rule T-1 so that one unknown reason follows.
type unknownReasonVariant struct {
	edit func(w *world, spec *policy.ObligationSpec)
	name string
	want conformance.UnknownReason
}

// unknownReasonBase has one workload of billing in production with a satisfying uses-image fact and complete coverage.
func unknownReasonBase() *world {
	return singleReplicaWorld(nil)
}

func evidenceConflictEdit(w *world, _ *policy.ObligationSpec) {
	w.edges[0].Properties["image"] = "registry/billing:1"
	w.addImageFact("billing-a", func(e *observation.EvidenceEdge) { e.Properties["image"] = "registry/billing:2" })
}

func unknownReasonVariants() []unknownReasonVariant {
	return []unknownReasonVariant{
		{name: "coverage incomplete", want: conformance.ReasonCoverageIncomplete, edit: func(w *world, _ *policy.ObligationSpec) {
			w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
		}},
		{name: "family unsupported", want: conformance.ReasonUnsupportedFamily, edit: func(w *world, _ *policy.ObligationSpec) {
			w.scope.Predicates = records(observation.CoverageUnsupported, observation.KindUsesImage)
		}},
		{name: "capability regression", want: conformance.ReasonCapabilityRegression, edit: func(w *world, _ *policy.ObligationSpec) {
			w.typed.Regressions = []observation.CapabilityRegression{{Family: "uses-image", Was: observation.SupportSupported, Now: observation.SupportUnsupported}}
		}},
		{name: "environment unresolved", want: conformance.ReasonEnvironmentUnresolved, edit: func(w *world, _ *policy.ObligationSpec) {
			w.edges[0].Environment = observation.EnvironmentUnresolved
		}},
		{name: "ambiguous binding", want: conformance.ReasonAmbiguousBinding, edit: func(w *world, _ *policy.ObligationSpec) {
			w.set.Ambiguous = map[string][]string{"cbm:p:billing-a": {"b-1", "b-2"}}
		}},
		{name: "binding gap", want: conformance.ReasonBindingGap, edit: func(_ *world, spec *policy.ObligationSpec) {
			spec.Relation.Target = []string{"ns-prod"}
		}},
		{name: "evidence conflict", want: conformance.ReasonEvidenceConflict, edit: evidenceConflictEdit},
		{name: "selector property unknown", want: conformance.ReasonSelectorPropertyUnknown, edit: func(_ *world, spec *policy.ObligationSpec) {
			spec.Applies = policy.Not(policy.Match(policy.DimensionProtocol, "https"))
		}},
		{name: "limit exceeded", want: conformance.ReasonLimitExceeded, edit: func(w *world, _ *policy.ObligationSpec) {
			w.scope.Breaches = []conformance.LimitBreach{{Limit: conformance.LimitGraphMaxEdges, Configured: 1, Observed: 2}}
		}},
	}
}

func TestEveryUnknownReason(t *testing.T) {
	t.Parallel()

	for _, tc := range unknownReasonVariants() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := unknownReasonBase()
			spec := &policy.ObligationSpec{}
			tc.edit(w, spec)

			w.plans = []policy.RulePlan{usesImageRule(func(s *policy.ObligationSpec) {
				s.Relation.Target, s.Applies = spec.Relation.Target, spec.Applies
			})}

			out := w.evaluate()
			rule := typedRuleResult(t, &out, "T-1")
			o := obligationOf(t, rule, "billing", "production")

			requireUnknown(t, o, tc.want)

			if !o.Reason.Valid() {
				t.Fatalf("reason %q is not valid", o.Reason)
			}

			if rule.Verdict != conformance.VerdictUnknown || len(rule.Reasons) != 1 {
				t.Fatalf("rule %s reasons %+v, want exactly one reason %q", rule.Verdict, rule.Reasons, tc.want)
			}

			if rule.Reasons[0].Code != string(tc.want) {
				t.Fatalf("rule %s reasons %+v, want exactly one reason %q", rule.Verdict, rule.Reasons, tc.want)
			}
		})
	}
}

func TestEvidenceConflictNamesBothSourcesAndNoWinner(t *testing.T) { // §8.3 same-role conflict, A08
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	w.add(resource("billing-a", "Deployment", "production", "billing-a"), "billing")
	w.add(billingImageNode())

	for i, tag := range []string{"1", "2"} {
		w.addImageFact("billing-a", func(e *observation.EvidenceEdge) {
			e.Properties["image"] = "registry/billing:" + tag
			e.SourceSpan = &observation.SourceSpan{Path: "k8s/production/file" + strconv.Itoa(i) + ".yaml", Line: 10 + i}
		})
	}

	w.plans = []policy.RulePlan{usesImageRule(nil)}

	o := w.billingObligation(t, "production")
	occ := occurrenceOfClass(t, o, conformance.OccurrenceConflict)

	if o.Verdict != conformance.VerdictUnknown || len(occ.Sources) != 2 {
		t.Fatalf("verdict %s sources %+v", o.Verdict, occ.Sources)
	}

	if !strings.Contains(occ.Detail, "file0.yaml:10") || !strings.Contains(occ.Detail, "file1.yaml:11") {
		t.Fatalf("the detail names both sources and no value: %q", occ.Detail)
	}

	if strings.Contains(occ.Detail, "registry/billing:1") {
		t.Fatalf("the detail names both sources and no value: %q", occ.Detail)
	}

	if countResults(o, conformance.VerdictPass) != 0 {
		t.Fatal("a fact won by order")
	}
}

// selectorObligation evaluates the rule T-1 that forbids calls of billing, limited by the selector, against one
// call of billing to ledger that has the protocol (none when empty).
func selectorObligation(t *testing.T, applies *policy.SelectorExpr, protocol string) *conformance.ObligationResult {
	t.Helper()

	w := serviceModuleWorld("calls-service")
	w.edges = []*observation.EvidenceEdge{fact(observation.KindCallsService, "a", "b", func(e *observation.EvidenceEdge) {
		if protocol != "" {
			e.Facets["protocol"] = protocol
		}
	})}
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationForbidden, observation.KindCallsService, policy.DirectionFromSubject,
		func(s *policy.ObligationSpec) { s.Applies, s.Scope.Environments = applies, []string{} })}

	return w.billingObligation(t, "")
}

func TestSelectorUnknownPropagates(t *testing.T) {
	t.Parallel()

	notHTTPS := policy.Not(policy.Match(policy.DimensionProtocol, "https"))
	httpsAndInternet := policy.AllOf(policy.Match(policy.DimensionProtocol, "https"), policy.Match(policy.DimensionTrustContext, "internet"))

	for _, tc := range []struct {
		check    func(*testing.T, *conformance.ObligationResult)
		applies  *policy.SelectorExpr
		name     string
		protocol string
	}{
		{
			name: "A07 not of an unknown property stays unknown", applies: notHTTPS,
			check: func(t *testing.T, o *conformance.ObligationResult) {
				t.Helper()
				requireUnknown(t, o, conformance.ReasonSelectorPropertyUnknown)
				wantUnknownNaming("protocol")(t, o)
			},
		},
		{name: "a known property decides", applies: notHTTPS, protocol: "http", check: wantVerdict(conformance.VerdictViolation)},
		{
			name: "a known false operand decides over an unknown one", applies: httpsAndInternet, protocol: "grpc",
			check: wantVerdict(conformance.VerdictPass),
		},
		{
			name: "a dimension without a source is unknown", applies: policy.Match(policy.DimensionTrustContext, "internet"), protocol: "https",
			check: wantUnknownBecause(conformance.ReasonSelectorPropertyUnknown),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, selectorObligation(t, tc.applies, tc.protocol))
		})
	}
}

// failedScopeWorld has billing in the failed project q and ledger in the project p.
func failedScopeWorld() *world {
	w := newWorld(observation.KindUsesImage)
	w.scope.Projects = []string{"p", "q"}
	w.scope.NodeProjects = map[string][]string{"billing": {"q"}, "ledger": {"p"}}
	w.scope.Failures = []observation.ScopeFailure{{Project: "q", Reason: "CO-OBS-001 query failed"}}

	if len(w.scope.Predicates) > 0 {
		w.scope.Predicates = append(w.scope.Predicates, observation.PredicateCoverage{
			Predicate: w.scope.Predicates[0].Predicate, Scope: observation.CoverageScope{Project: "q"}, Status: observation.CoverageFailed,
			LossModes: []observation.LossMode{}, Reasons: []string{"scope failed"},
		})
	}

	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing", "ledger"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)}

	return w
}

func TestFailedScopeBlocksOnlyDependentObligations(t *testing.T) { // A09
	t.Parallel()

	w := failedScopeWorld()
	out := w.evaluate()
	rule := typedRuleResult(t, &out, "T-1")

	failed := obligationOf(t, rule, "billing", "production")
	if failed.Verdict != conformance.VerdictUnknown || failed.Reason != conformance.ReasonCoverageIncomplete {
		t.Fatalf("billing: %s %q (%s)", failed.Verdict, failed.Reason, failed.Detail)
	}

	if !strings.Contains(failed.Detail, "q") {
		t.Fatalf("billing: %s %q (%s)", failed.Verdict, failed.Reason, failed.Detail)
	}

	if kept := obligationOf(t, rule, "ledger", "production"); kept.Verdict != conformance.VerdictViolation {
		t.Fatalf("ledger keeps its result: %s (%s)", kept.Verdict, kept.Detail)
	}
}
