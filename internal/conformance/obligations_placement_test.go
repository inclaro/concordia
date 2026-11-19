package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// ---- 10.5: placement and the other reconciliation cases ----

// placement is the world of the §8.3 table: billing is deployed in the container ns-prod (CALM deployed-in
// relationship), the workload and the namespace are bound.
func placement() (*world, policy.RulePlan) {
	w := newWorld(observation.KindDeploysArtifact)
	w.model = typedModel(intent.RelationshipDeclaration{
		ID: "r-1", Kind: intent.RelDeployedIn, Container: "ns-prod", Targets: []string{"billing"}, Pointer: "/relationships/r-1",
	})

	rule := typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindDeploysArtifact, policy.DirectionFromWorkload,
		func(s *policy.ObligationSpec) { s.Relation.Target = []string{"ns-prod"} })

	return w, rule
}

// placedWorld is the placement world with the workload of billing and the namespace in the environment, and a
// deploys-artifact fact between them: from the workload to the namespace, or the reverse.
func placedWorld(env string, reversed bool) (*world, policy.RulePlan) {
	w, rule := placement()
	w.add(resource("wl", "Deployment", env, "billing"), "billing")
	w.add(resource("ns", "Namespace", env, "payments"), "ns-prod")

	from, to := "wl", "ns"
	if reversed {
		from, to = to, from
	}

	w.edges = []*observation.EvidenceEdge{fact(observation.KindDeploysArtifact, from, to, func(e *observation.EvidenceEdge) { e.Environment = env })}
	w.plans = []policy.RulePlan{rule}

	return w, rule
}

func checkPlacementPresent(t *testing.T) {
	t.Helper()

	w, _ := placedWorld("production", false)
	o := w.billingObligation(t, "production")

	if o.Verdict != conformance.VerdictPass || o.Placement == nil {
		t.Fatalf("verdict %s placement %+v (%s)", o.Verdict, o.Placement, o.Detail)
	}

	if o.Placement.Container != "ns-prod" {
		t.Fatalf("verdict %s placement %+v (%s)", o.Verdict, o.Placement, o.Detail)
	}
}

func checkPlacementAbsentIsViolation(t *testing.T) {
	t.Helper()

	w, rule := placement()
	w.plans = []policy.RulePlan{rule}

	requireVerdict(t, w.billingObligation(t, "production"), conformance.VerdictViolation)
}

func checkPlacementAbsentIncompleteCoverage(t *testing.T) {
	t.Helper()

	w, rule := placement()
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindDeploysArtifact)
	w.plans = []policy.RulePlan{rule}

	requireUnknown(t, w.billingObligation(t, "production"), conformance.ReasonCoverageIncomplete)
}

func checkPlacementReverseDirection(t *testing.T) {
	t.Helper()

	w, _ := placedWorld("production", true)
	o := w.billingObligation(t, "production")
	reverse := occurrenceOfClass(t, o, conformance.OccurrenceWrongDirection)

	if o.Verdict == conformance.VerdictPass || reverse.Result != conformance.VerdictViolation {
		t.Fatalf("verdict %s reverse %+v", o.Verdict, reverse)
	}

	if !strings.Contains(reverse.Detail, "unexpected placement") {
		t.Fatalf("verdict %s reverse %+v", o.Verdict, reverse)
	}

	if countResults(o, conformance.VerdictPass) != 0 {
		t.Fatal("the reverse relation satisfied the obligation")
	}
}

func checkPlacementWrongEnvironment(t *testing.T) {
	t.Helper()

	w, _ := placedWorld("staging", false)
	o := w.billingObligation(t, "production")
	staging := occurrenceOfClass(t, o, conformance.OccurrenceWrongEnvironment)

	if o.Verdict == conformance.VerdictPass || staging.Result != conformance.VerdictNotApplicable {
		t.Fatalf("verdict %s staging %+v", o.Verdict, staging)
	}

	if staging.Decisive {
		t.Fatalf("verdict %s staging %+v", o.Verdict, staging)
	}

	if o.Verdict != conformance.VerdictViolation {
		t.Fatalf("the production deployment is absent with complete coverage: %s", o.Verdict)
	}
}

func checkPlacementStagingObligation(t *testing.T) {
	t.Helper()

	w, rule := placedWorld("staging", false)
	rule.Obligation.Scope.Environments = []string{"production", "staging"}
	w.plans = []policy.RulePlan{rule}

	out := w.evaluate()
	rr := typedRuleResult(t, &out, "T-1")

	if obligationOf(t, rr, "billing", "staging").Verdict != conformance.VerdictPass {
		t.Fatalf("results = %+v", rr.Obligations)
	}

	if obligationOf(t, rr, "billing", "production").Verdict != conformance.VerdictViolation {
		t.Fatalf("results = %+v", rr.Obligations)
	}
}

// bindingGapFinding gives the binding-gap finding of the output, nil when there is none.
func bindingGapFinding(out *conformance.EvaluateOutput) *conformance.Finding {
	var finding *conformance.Finding

	for i := range out.Findings {
		if out.Findings[i].Kind == conformance.KindBindingGap {
			finding = &out.Findings[i]
		}
	}

	return finding
}

// isResolveBindingFinding says whether the finding asks to resolve the binding of the namespace manifest.
func isResolveBindingFinding(f *conformance.Finding, obligationID string) bool {
	if f == nil || f.Location == nil {
		return false
	}

	if f.NextAction != conformance.NextActionResolveBinding || f.Location.Path != "k8s/production/payments.yaml" {
		return false
	}

	return f.Severity == policy.SeverityInfo && f.ObligationID == obligationID
}

func checkPlacementEndpointWithoutBinding(t *testing.T) {
	t.Helper()

	w, _ := placedWorld("production", false)
	delete(w.bound, "cbm:p:ns") // no binding

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
	gap := occurrenceOfClass(t, o, conformance.OccurrenceBindingGap)

	requireUnknown(t, o, conformance.ReasonBindingGap)

	if gap.Edge == nil || gap.NextAction != conformance.NextActionResolveBinding {
		t.Fatalf("verdict %s reason %q gap %+v", o.Verdict, o.Reason, gap)
	}

	if finding := bindingGapFinding(&out); !isResolveBindingFinding(finding, o.ID) {
		t.Fatalf("binding gap finding = %+v", finding)
	}
}

func TestPlacement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		run  func(*testing.T)
		name string
	}{
		{checkPlacementPresent, "§8.3 present in the right environment and target with a binding"},
		{checkPlacementAbsentIsViolation, "§8.3 absent with complete coverage is a violation"},
		{checkPlacementAbsentIncompleteCoverage, "§8.3 absent with incomplete coverage is unknown with the coverage reason"},
		{checkPlacementReverseDirection, "the reverse direction does not satisfy placement and is evaluated separately"},
		{checkPlacementWrongEnvironment, "A11 wrong environment never satisfies the production obligation"},
		{checkPlacementStagingObligation, "a staging obligation evaluates the staging placement itself"},
		{checkPlacementEndpointWithoutBinding, "EVD-05 an endpoint without a binding keeps its fact and shows a binding gap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t)
		})
	}
}

// occurrenceOfKind gives the occurrence of the observed kind, nil when there is none.
func occurrenceOfKind(o *conformance.ObligationResult, kind observation.CanonicalEdgeKind) *conformance.OccurrenceResult {
	var found *conformance.OccurrenceResult

	for i := range o.Occurrences {
		if o.Occurrences[i].Kind == kind {
			found = &o.Occurrences[i]
		}
	}

	return found
}

// notAllowedRelationWorld has an allowed call of billing to ledger and a configured connection that no rule allows.
func notAllowedRelationWorld() *world {
	w := serviceModuleWorld("calls-service", "configured-connection")
	w.edges = []*observation.EvidenceEdge{
		fact(observation.KindCallsService, "a", "b", nil),
		fact(observation.KindConfiguredConnection, "a", "b", nil),
	}
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationAllowed, observation.KindCallsService, policy.DirectionFromSubject,
		func(s *policy.ObligationSpec) {
			s.Relation.Target, s.Scope.Environments = []string{"ledger"}, []string{}
		})}

	return w
}

// requireNotPermittedRelation requires that the configured connection is a violation of class not-permitted.
func requireNotPermittedRelation(t *testing.T, o *conformance.ObligationResult) {
	t.Helper()

	configured := occurrenceOfKind(o, observation.KindConfiguredConnection)
	if configured == nil || configured.Result != conformance.VerdictViolation {
		t.Fatalf("the not allowed relation is a violation: %+v", configured)
	}

	if configured.Class != conformance.OccurrenceNotPermitted {
		t.Fatalf("the not allowed relation is a violation: %+v", configured)
	}
}

func TestNotAllowedRelationOnAnAllowedPairIsAViolation(t *testing.T) { // §8.3
	t.Parallel()

	w := notAllowedRelationWorld()
	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "")

	allowed := occurrenceOfKind(o, observation.KindCallsService)
	if allowed == nil || allowed.Result != conformance.VerdictPass {
		t.Fatalf("the allowed relation keeps pass: %+v", allowed)
	}

	requireNotPermittedRelation(t, o)

	if o.Verdict != conformance.VerdictViolation || len(out.Findings) != 1 {
		t.Fatalf("verdict %s findings %+v", o.Verdict, out.Findings)
	}

	if out.Findings[0].Edge.Kind != observation.KindConfiguredConnection {
		t.Fatalf("verdict %s findings %+v", o.Verdict, out.Findings)
	}
}

// allowAndForbidWorld has two calls of billing to ledger, one over https and one administrative, with the rule
// A-allow for the https calls and the rule B-forbid for the administrative ones.
func allowAndForbidWorld() *world {
	w := serviceModuleWorld("calls-service")
	w.model = typedModel(connects("c-1", "billing", "ledger", nil))
	w.edges = []*observation.EvidenceEdge{
		fact(observation.KindCallsService, "a", "b", func(e *observation.EvidenceEdge) { e.Facets["protocol"] = "https" }),
		fact(observation.KindCallsService, "a", "b", func(e *observation.EvidenceEdge) { e.Facets["protocol"] = "administrative"; e.Occurrence = "admin" }),
	}
	w.plans = []policy.RulePlan{
		protocolRule("A-allow", policy.ObligationAllowed, "https"),
		protocolRule("B-forbid", policy.ObligationForbidden, "administrative"),
	}

	return w
}

// protocolRule makes a rule of billing for the calls to ledger that use the protocol.
func protocolRule(id string, typ policy.ObligationType, protocol string) policy.RulePlan {
	return typedRule(id, []string{"billing"}, typ, observation.KindCallsService, policy.DirectionFromSubject, func(s *policy.ObligationSpec) {
		s.Relation.Target, s.Scope.Environments = []string{"ledger"}, []string{}
		s.Applies = policy.Match(policy.DimensionProtocol, protocol)
	})
}

// requireAllowedAndForbiddenOutcome requires the pass of the https call, the violation of the administrative call, and
// that the violation alone is a finding.
func requireAllowedAndForbiddenOutcome(t *testing.T, out *conformance.EvaluateOutput) {
	t.Helper()

	allowed := obligationOf(t, typedRuleResult(t, out, "A-allow"), "billing", "")
	if allowed.Verdict != conformance.VerdictPass || len(allowed.Occurrences) != 1 {
		t.Fatalf("allowed: %s %+v", allowed.Verdict, allowed.Occurrences)
	}

	forbidden := obligationOf(t, typedRuleResult(t, out, "B-forbid"), "billing", "")
	if forbidden.Verdict != conformance.VerdictViolation || len(forbidden.Occurrences) != 1 {
		t.Fatalf("forbidden: %s %+v", forbidden.Verdict, forbidden.Occurrences)
	}

	if forbidden.Occurrences[0].Occurrence != "admin" {
		t.Fatalf("forbidden: %s %+v", forbidden.Verdict, forbidden.Occurrences)
	}

	if len(out.Findings) != 1 || out.Findings[0].Result != conformance.VerdictViolation {
		t.Fatalf("findings = %+v", out.Findings)
	}
}

func TestA02AllowedAndForbiddenConnectionOnOnePair(t *testing.T) {
	t.Parallel()

	w := allowAndForbidWorld()

	if _, err := conformance.CheckPolicyConflicts(w.plans, w.model, &w.typed); err != nil {
		t.Fatalf("distinct occurrences must not conflict: %v", err)
	}

	out := w.evaluate()
	requireAllowedAndForbiddenOutcome(t, &out)

	// The pair report stays available and does not hide the violation.
	if len(out.Reconciliation) == 0 {
		t.Fatal("the pair report is still produced")
	}
}

func TestCapabilityRegressionMakesRelatedClaimsUnknown(t *testing.T) { // §8.3
	t.Parallel()

	w, _ := placedWorld("production", false)
	w.typed.Regressions = []observation.CapabilityRegression{{
		Family: "deploys-artifact", Was: observation.SupportSupported, Now: observation.SupportUnsupported, LockDigest: "sha256:a", CurrentDigest: "sha256:b",
	}}

	o := w.billingObligation(t, "production")

	requireUnknown(t, o, conformance.ReasonCapabilityRegression)

	if !strings.Contains(o.Detail, "deploys-artifact") {
		t.Fatalf("verdict %s reason %q (%s)", o.Verdict, o.Reason, o.Detail)
	}
}

func TestBindingEvidenceIsRecordedWithStatusAndAuthority(t *testing.T) {
	t.Parallel()

	w, _ := placedWorld("production", false)
	w.loaded.Bindings = []binding.Binding{{ID: "b-wl", ArchitectureRef: "billing", Status: binding.StatusActive, Authority: "platform-team"}}
	w.set.UsedBy = map[string][]string{"cbm:p:wl": {"b-wl"}}

	o := w.billingObligation(t, "production")

	if !slices.Equal(o.Bindings, []string{"b-wl"}) {
		t.Fatalf("obligation bindings = %v", o.Bindings)
	}

	refs := o.Occurrences[0].Bindings
	if len(refs) != 1 {
		t.Fatalf("occurrence bindings = %+v", refs)
	}

	if refs[0].ID != "b-wl" || refs[0].Status != binding.StatusActive {
		t.Fatalf("occurrence bindings = %+v", refs)
	}

	if refs[0].Authority != "platform-team" {
		t.Fatalf("occurrence bindings = %+v", refs)
	}
}

func TestInfrastructureFactsStayOutOfTheReconciliation(t *testing.T) { // POL-04
	t.Parallel()

	w := newWorld(observation.KindRoutesTo)
	w.typed.Workspace = nil
	w.scope.Predicates = []observation.PredicateCoverage{projectCoverage("routes-to")}
	w.add(resource("route", "Ingress", "", "web"), "billing")
	w.add(resource("svc", "Service", "", "ledger"), "ledger")
	w.edges = []*observation.EvidenceEdge{fact(observation.KindRoutesTo, "route", "svc", nil)}
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationForbidden, observation.KindRoutesTo, policy.DirectionFromSubject,
		func(s *policy.ObligationSpec) { s.Scope.Environments = []string{} })}

	out := w.evaluate()

	if len(out.Findings) != 1 || out.Findings[0].Kind != conformance.KindObligationViolation {
		t.Fatalf("the gate decision comes from the per-occurrence result: %+v", out.Findings)
	}

	if out.Findings[0].Edge.Kind != observation.KindRoutesTo {
		t.Fatalf("the gate decision comes from the per-occurrence result: %+v", out.Findings)
	}

	for _, entry := range out.Reconciliation {
		if slices.Contains(entry.ObservedKinds, observation.KindRoutesTo) {
			t.Fatalf("an infrastructure fact entered the reconciliation: %+v", entry)
		}
	}
}

// isAbsentWorkloadFinding says whether the finding is a mandatory violation of billing in production for an absent
// obligation.
func isAbsentWorkloadFinding(f *conformance.Finding) bool {
	if f.ObligationID == "" || f.Subject != "billing" {
		return false
	}

	if f.Environment != "production" || f.Result != conformance.VerdictViolation {
		return false
	}

	return f.Enforcement == policy.EnforcementMandatory && f.Kind == conformance.KindObligationAbsent
}

func TestTypedFindingFields(t *testing.T) {
	t.Parallel()

	w := replicas()
	w.plans = []policy.RulePlan{usesImageRule(nil)}

	out := w.evaluate()
	if len(out.Findings) != 3 {
		t.Fatalf("findings = %d, want one for each workload", len(out.Findings))
	}

	seen := map[string]bool{}

	for i := range out.Findings {
		f := &out.Findings[i]
		if !isAbsentWorkloadFinding(f) {
			t.Fatalf("finding = %+v", *f)
		}

		if f.Fingerprint == "" || seen[f.Fingerprint] {
			t.Fatalf("finding = %+v", *f)
		}

		seen[f.Fingerprint] = true
	}
}

func TestTypedRuleDefaultsToAdvisory(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	rule := usesImageRule(nil)
	rule.Enforcement = ""
	w.plans = []policy.RulePlan{rule}

	out := w.evaluate()
	rr := typedRuleResult(t, &out, "T-1")

	if rr.Enforcement != policy.EnforcementAdvisory || out.Findings[0].Enforcement != policy.EnforcementAdvisory {
		t.Fatalf("enforcement = %q / %q, want advisory", rr.Enforcement, out.Findings[0].Enforcement)
	}

	if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError); got != diag.OK {
		t.Fatalf("an advisory violation gates: exit %d", got)
	}
}
