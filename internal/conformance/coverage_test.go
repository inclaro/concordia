package conformance_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

func importsRecord(status observation.CoverageStatus, modes ...string) observation.PredicateCoverage {
	rec := observation.PredicateCoverage{
		Predicate: "imports", Scope: observation.CoverageScope{Project: "p"}, Status: status,
		LossModes: []observation.LossMode{}, Reasons: []string{},
	}

	for _, m := range modes {
		rec.LossModes = append(rec.LossModes, observation.LossMode{ID: m, Gaps: []observation.CoverageGap{{Reason: m, Path: "k8s/x.yaml", Line: 4}}})
		rec.Unresolved++
	}

	return rec
}

func evaluateWithScope(t *testing.T, s *scenario, scope *conformance.ScopeContext) (*conformance.RuleResult, conformance.EvaluateOutput) {
	t.Helper()

	if s.observed == nil {
		s.observed = allObs
	}

	in := build(s)
	in.Scope = *scope
	out := conformance.Evaluate(in)

	if len(out.RuleResults) != 1 {
		t.Fatalf("rule results = %d", len(out.RuleResults))
	}

	return &out.RuleResults[0], out
}

// isUnknownWithoutFindings says whether the rule is unknown for the reason code and the run made no finding.
func isUnknownWithoutFindings(r *conformance.RuleResult, out *conformance.EvaluateOutput, code string) bool {
	return r.Verdict == conformance.VerdictUnknown && len(out.Findings) == 0 && slices.Contains(reasonCodes(r), code)
}

// hasOneViolation says whether the rule is a violation and the run made exactly one finding.
func hasOneViolation(r *conformance.RuleResult, out *conformance.EvaluateOutput) bool {
	return r.Verdict == conformance.VerdictViolation && len(out.Findings) == 1
}

// traversalCutShort says whether the traversal of the rule was incomplete and the limit that cut it is the reason.
func traversalCutShort(r *conformance.RuleResult) bool {
	return r.Coverage.TraversalIncomplete && slices.Contains(reasonCodes(r), diag.CON030)
}

func predicateNames(records []observation.PredicateCoverage) []string {
	names := make([]string, len(records))
	for i := range records {
		names[i] = records[i].Predicate
	}

	return names
}

func failedProjects(failures []observation.ScopeFailure) []string {
	projects := make([]string, len(failures))
	for i := range failures {
		projects[i] = failures[i].Project
	}

	return projects
}

// evidenceIsIntact says whether the evaluation left the evidence of the query as it was read: 3 nodes, 4 edges, 1
// unsupported fact, and 2 entities.
func evidenceIsIntact(res *observation.EvidenceQueryResult) bool {
	return len(res.Nodes) == 3 && len(res.Edges) == 4 && len(res.Unsupported) == 1 && len(res.Entities) == 2
}

func TestSkipCountsReportAllFourReasons(t *testing.T) {
	t.Parallel()

	ghost := obs("services.billing.ghost", "services/billing/ghost.ts")
	excluded := obs("services.fulfillment.legacy.old", "services/fulfillment/legacy/old.ts")
	billingAPI := obs("services.billing.api", "services/billing/api.ts")

	edges := []*observation.EvidenceEdge{
		edge(inv, store),
		edge(inv, helper),   // helper has no binding: unbound.
		edge(inv, ghost),    // ghost is not in the graph result: endpoint missing.
		edge(inv, excluded), // excluded file: out of scope.
		edgeOf(inv, billingAPI, observation.KindAmbiguous, observation.ConfidenceHeuristic, 5), // unsupported kind.
	}

	r, out := evaluateWithScope(t, &scenario{
		observed: []*observation.ObservedNode{inv, store, helper, excluded, billingAPI},
		edges:    edges, exclude: []string{"services/fulfillment/legacy/**"},
		plans: []policy.RulePlan{boundary(nil)},
	}, &conformance.ScopeContext{UnsupportedFacts: 2})

	want := conformance.SkipCounts{Unbound: 1, EndpointMissing: 1, OutOfScope: 1, Unsupported: 3}
	if r.Coverage.Skipped != want {
		t.Fatalf("skipped = %+v, want %+v", r.Coverage.Skipped, want)
	}

	if r.Coverage.UnboundEndpoints != 1 {
		t.Errorf("unboundEndpoints = %d", r.Coverage.UnboundEndpoints)
	}

	for _, name := range []string{conformance.SkipUnbound, conformance.SkipEndpointMissing, conformance.SkipOutOfScope, conformance.SkipUnsupported} {
		if r.Coverage.SkipReasons[name] == "" {
			t.Errorf("no reason for %s: %v", name, r.Coverage.SkipReasons)
		}
	}

	if out.Skipped != want {
		t.Errorf("run skipped = %+v", out.Skipped)
	}

	// The edges stay in the evidence: the evaluation never removes a fact.
	if len(edges) != 5 {
		t.Errorf("edges = %d", len(edges))
	}
}

func TestSkipCountsAreZeroWithoutSkippedEdges(t *testing.T) {
	t.Parallel()

	r, _ := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, contract)}, plans: []policy.RulePlan{boundary(nil)}}, &conformance.ScopeContext{})
	if r.Coverage.Skipped != (conformance.SkipCounts{}) {
		t.Errorf("skipped = %+v reasons = %v", r.Coverage.Skipped, r.Coverage.SkipReasons)
	}

	if len(r.Coverage.SkipReasons) != 0 || r.Coverage.SkipReasons == nil {
		t.Errorf("skipped = %+v reasons = %v", r.Coverage.Skipped, r.Coverage.SkipReasons)
	}

	if r.Coverage.Predicates == nil {
		t.Error("predicates is nil")
	}
}

func TestRuleCoverageCarriesThePredicateRecordsOfItsKinds(t *testing.T) {
	t.Parallel()

	records := []observation.PredicateCoverage{
		importsRecord(observation.CoverageComplete),
		{Predicate: "calls", Scope: observation.CoverageScope{Project: "p"}, Status: observation.CoverageComplete, LossModes: []observation.LossMode{}, Reasons: []string{}},
		{Predicate: "uses-image", Scope: observation.CoverageScope{Project: "p"}, Status: observation.CoveragePartial, LossModes: []observation.LossMode{}, Reasons: []string{}},
	}

	r, _ := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, contract)}, plans: []policy.RulePlan{boundary(nil)}},
		&conformance.ScopeContext{Predicates: records, Projects: []string{"p"}})

	if !slices.Equal(predicateNames(r.Coverage.Predicates), []string{"imports", "calls"}) {
		t.Errorf("predicates = %+v", r.Coverage.Predicates)
	}

	if r.Verdict != conformance.VerdictPass {
		t.Errorf("verdict = %s, reasons %v", r.Verdict, r.Reasons)
	}
}

// A03: a necessary edge is absent and the extraction of the scope is partial: unknown with the coverage reason, never a definite absence.
func TestAbsenceIsUnknownWhenCoverageIsPartial(t *testing.T) {
	t.Parallel()

	scope := &conformance.ScopeContext{Projects: []string{"p"}, Predicates: []observation.PredicateCoverage{importsRecord(observation.CoveragePartial, observation.LossUnrenderedTemplate)}}
	r, out := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{requireEdgePlan()}}, scope)

	if !isUnknownWithoutFindings(r, &out, diag.CON032) {
		t.Fatalf("verdict %s findings %d reasons %v", r.Verdict, len(out.Findings), r.Reasons)
	}
}

func TestAbsenceIsAViolationWithCompleteCoverage(t *testing.T) {
	t.Parallel()

	scope := &conformance.ScopeContext{Projects: []string{"p"}, Predicates: []observation.PredicateCoverage{importsRecord(observation.CoverageComplete)}}
	r, out := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{requireEdgePlan()}}, scope)

	if !hasOneViolation(r, &out) || out.Findings[0].Kind != conformance.KindMissingEdge {
		t.Fatalf("verdict %s findings %+v", r.Verdict, out.Findings)
	}
}

func TestUnsupportedFamilyGivesCON031(t *testing.T) {
	t.Parallel()

	rec := importsRecord(observation.CoverageUnsupported)
	rec.Reasons = []string{observation.ReasonFamilyUnsupported}

	r, _ := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{requireEdgePlan()}},
		&conformance.ScopeContext{Projects: []string{"p"}, Predicates: []observation.PredicateCoverage{rec}})

	if r.Verdict != conformance.VerdictUnknown || !slices.Contains(reasonCodes(r), diag.CON031) {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.Reasons)
	}
}

func TestEvaluateAbsence(t *testing.T) {
	t.Parallel()

	complete := importsRecord(observation.CoverageComplete)
	partial := importsRecord(observation.CoveragePartial, observation.LossMergedOccurrences)
	failed := importsRecord(observation.CoverageFailed)
	unsupported := importsRecord(observation.CoverageUnsupported)
	ok := func(cov ...observation.PredicateCoverage) *conformance.AbsenceInput {
		return &conformance.AbsenceInput{Coverage: cov, Supported: true, InScope: true, AnalysisOK: true}
	}

	cases := []struct {
		name    string
		in      *conformance.AbsenceInput
		verdict conformance.RuleVerdict
		reason  conformance.UnknownReason
	}{
		{"complete qualified coverage proves the absence", ok(complete), conformance.VerdictViolation, ""},
		{"partial coverage is unknown", ok(partial), conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
		{"one incomplete predicate of two is unknown", ok(complete, partial), conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
		{"a failed scope is unknown", ok(failed), conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
		{"an unsupported family is unknown", ok(unsupported), conformance.VerdictUnknown, conformance.ReasonUnsupportedFamily},
		{"no record is unknown", ok(), conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
		{"an unsupported observation is unknown", &conformance.AbsenceInput{Coverage: []observation.PredicateCoverage{complete}, InScope: true, AnalysisOK: true}, conformance.VerdictUnknown, conformance.ReasonUnsupportedFamily},
		{"an observation out of scope is unknown", &conformance.AbsenceInput{Coverage: []observation.PredicateCoverage{complete}, Supported: true, AnalysisOK: true}, conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
		{"an analysis that failed is unknown", &conformance.AbsenceInput{Coverage: []observation.PredicateCoverage{complete}, Supported: true, InScope: true}, conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
		{"an exceeded limit is unknown", &conformance.AbsenceInput{Coverage: []observation.PredicateCoverage{complete}, Supported: true, InScope: true, AnalysisOK: true, Breaches: []conformance.LimitBreach{{Limit: conformance.LimitGraphMaxNodes, Configured: 1, Observed: 2}}}, conformance.VerdictUnknown, conformance.ReasonLimitExceeded},
	}

	for _, tc := range cases {
		got := conformance.EvaluateAbsence(tc.in)
		if got.Verdict != tc.verdict || got.Reason != tc.reason {
			t.Errorf("%s: got %s/%q (%s), want %s/%q", tc.name, got.Verdict, got.Reason, got.Detail, tc.verdict, tc.reason)
		}

		if got.Verdict == conformance.VerdictUnknown && got.Detail == "" {
			t.Errorf("%s: an unknown result has no detail", tc.name)
		}
	}
}

func TestCoverageOfPicksTheRecordsOfOneScope(t *testing.T) {
	t.Parallel()

	prod := importsRecord(observation.CoveragePartial)
	prod.Scope = observation.CoverageScope{Environment: "prod"}
	records := []observation.PredicateCoverage{importsRecord(observation.CoverageComplete), prod}

	got := conformance.CoverageOf(records, []string{"imports", "calls"}, observation.CoverageScope{Environment: "prod"})
	if len(got) != 1 || got[0].Status != observation.CoveragePartial {
		t.Errorf("records = %+v", got)
	}
}

func requireUnknownReasonsListedOnce(t *testing.T, reasons []conformance.UnknownReason) {
	t.Helper()

	seen := map[conformance.UnknownReason]bool{}

	for _, r := range reasons {
		if seen[r] {
			t.Errorf("%s is listed twice", r)
		}

		seen[r] = true
	}
}

func requireUnknownReasonsValid(t *testing.T, reasons []conformance.UnknownReason) {
	t.Helper()

	for _, r := range reasons {
		if !r.Valid() {
			t.Errorf("%s is not valid", r)
		}
	}
}

func TestUnknownReasonsAreAClosedSet(t *testing.T) {
	t.Parallel()

	// Each group is one change; a later change appends one group (and one line in the deployment test).
	foundation := []conformance.UnknownReason{
		"coverage-incomplete", "unsupported-family", "capability-regression", "environment-unresolved",
		"ambiguous-binding", "binding-gap", "evidence-conflict", "selector-property-unknown", "limit-exceeded",
	}
	reviewCurrency := []conformance.UnknownReason{
		"target-not-declared", "trust-anchor-unreadable", "verification-unavailable", "reference-unresolved",
	}
	assuranceAdapters := []conformance.UnknownReason{
		"claim-not-enabled", "no-evidence-supplied", "no-evaluator-available", "rendered-evidence-missing",
		"enforcement-not-declared", "reviewed-mode-not-available",
	}
	want := slices.Concat(foundation, reviewCurrency, assuranceAdapters)

	requireUnknownReasonsListedOnce(t, want)

	if got := conformance.UnknownReasons(); !slices.Equal(got, want) {
		t.Fatalf("reasons = %v", got)
	}

	requireUnknownReasonsValid(t, want)

	// The claim registry cannot import this package, so it holds the reason as a string: the two stay equal.
	if string(conformance.ReasonClaimNotEnabled) != claims.ReasonClaimNotEnabled {
		t.Errorf("claims.ReasonClaimNotEnabled = %q", claims.ReasonClaimNotEnabled)
	}

	if conformance.UnknownReason("scope-failed").Valid() || conformance.UnknownReason("").Valid() {
		t.Error("a reason outside the set is valid")
	}
}

// A09: one project failed. The rule that depends on it is unknown; the rule of another project keeps its result.
func TestScopeFailureBlocksOnlyDependentRules(t *testing.T) {
	t.Parallel()

	billingPlan := boundary(nil)
	fulfillmentPlan := boundary(func(p *policy.RulePlan) {
		p.RuleID = "R-2"
		p.Subjects = []string{"fulfillment"}
		p.Targets = []string{"ledger"}
	})

	in := build(&scenario{observed: allObs, edges: []*observation.EvidenceEdge{edge(inv, contract)}, plans: []policy.RulePlan{billingPlan, fulfillmentPlan}})
	in.Scope = conformance.ScopeContext{
		Projects:     []string{"demo", "pay"},
		Failures:     []observation.ScopeFailure{{Project: "pay", Reason: "CO-OBS-001: query failed"}},
		NodeProjects: map[string][]string{"billing": {"pay"}, "fulfillment": {"demo"}, "ledger": {"demo"}},
	}

	out := conformance.Evaluate(in)
	byRule := map[string]*conformance.RuleResult{}

	for i := range out.RuleResults {
		byRule[out.RuleResults[i].RuleID] = &out.RuleResults[i]
	}

	if r := byRule["R-1"]; r.Verdict != conformance.VerdictUnknown || !slices.Contains(reasonCodes(r), diag.OBS018) {
		t.Errorf("dependent rule: %s %v", r.Verdict, r.Reasons)
	}

	if r := byRule["R-2"]; r.Verdict != conformance.VerdictPass || slices.Contains(reasonCodes(r), diag.OBS018) {
		t.Errorf("independent rule: %s %v", r.Verdict, r.Reasons)
	}

	// A violation that the other scopes prove stays a violation.
	in.Evidence.Edges = append(in.Evidence.Edges, edge(inv, store))
	out = conformance.Evaluate(in)

	if got := out.RuleResults[0]; got.RuleID != "R-1" || got.Verdict != conformance.VerdictViolation {
		t.Errorf("rule with a finding: %s %s", got.RuleID, got.Verdict)
	}
}

func TestScopeFailureBlocksAbsenceFindings(t *testing.T) {
	t.Parallel()

	scope := &conformance.ScopeContext{
		Projects: []string{"p"}, Predicates: []observation.PredicateCoverage{importsRecord(observation.CoverageFailed)},
		Failures: []observation.ScopeFailure{{Project: "p", Reason: "CO-OBS-001: query failed"}},
	}
	r, out := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{requireEdgePlan()}}, scope)

	if !isUnknownWithoutFindings(r, &out, diag.OBS018) {
		t.Errorf("verdict %s findings %d reasons %v", r.Verdict, len(out.Findings), r.Reasons)
	}
}

func TestFailedScopesOf(t *testing.T) {
	t.Parallel()

	scope := &conformance.ScopeContext{
		Projects:     []string{"a", "b", "c"},
		Failures:     []observation.ScopeFailure{{Project: "c", Reason: "x"}, {Project: "b", Reason: "y"}},
		NodeProjects: map[string][]string{"n1": {"a"}, "n2": {"b"}},
	}

	if got := scope.FailedScopesOf([]string{"b", "c", "d"}); !slices.Equal(failedProjects(got), []string{"b", "c"}) {
		t.Errorf("FailedScopesOf = %+v", got)
	}

	if got := scope.FailedScopesOfNodes([]string{"n1"}); len(got) != 0 {
		t.Errorf("n1 depends on a only: %+v", got)
	}

	if got := scope.FailedScopesOfNodes([]string{"n2"}); len(got) != 1 || got[0].Project != "b" {
		t.Errorf("n2 depends on b: %+v", got)
	}

	// A node with no project information depends on every project.
	if got := scope.FailedScopesOfNodes([]string{"unknown"}); len(got) != 2 {
		t.Errorf("unknown node: %+v", got)
	}
}

func TestGraphLimitsReportEachLimitAndCutNothing(t *testing.T) {
	t.Parallel()

	res := &observation.EvidenceQueryResult{
		Nodes:       map[string]observation.ObservedNode{"1": {}, "2": {}, "3": {}},
		Edges:       []*observation.EvidenceEdge{{}, {}, {}, {}},
		Unsupported: []*observation.EvidenceEdge{{}},
		Entities:    []observation.ObservedNode{{}, {}},
	}

	limits := workspace.DefaultLimits()
	if got := conformance.CheckGraphLimits(limits, res); len(got) != 0 {
		t.Fatalf("defaults give breaches: %+v", got)
	}

	limits.Graph.MaxNodes, limits.Graph.MaxEdges, limits.Evidence.MaxFacts = 2, 4, 5
	got := conformance.CheckGraphLimits(limits, res)

	want := []conformance.LimitBreach{
		{Limit: conformance.LimitEvidenceMaxFacts, Configured: 5, Observed: 6},
		{Limit: conformance.LimitGraphMaxEdges, Configured: 4, Observed: 5},
		{Limit: conformance.LimitGraphMaxNodes, Configured: 2, Observed: 3},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("breaches = %+v, want %+v", got, want)
	}

	// The evidence that was read is not cut.
	if !evidenceIsIntact(res) {
		t.Errorf("the evidence was cut: %d nodes, %d edges", len(res.Nodes), len(res.Edges))
	}

	if d := got[0].Diagnostic(); d.Code != diag.CON030 {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestExceededLimitMakesPassUnknownAndKeepsViolations(t *testing.T) {
	t.Parallel()

	breach := conformance.LimitBreach{Limit: conformance.LimitGraphMaxNodes, Configured: 1, Observed: 6}
	scope := &conformance.ScopeContext{Breaches: []conformance.LimitBreach{breach}}

	r, _ := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, contract)}, plans: []policy.RulePlan{boundary(nil)}}, scope)
	if r.Verdict != conformance.VerdictUnknown || !slices.Contains(reasonCodes(r), diag.CON030) {
		t.Errorf("pass with a breach: %s %v", r.Verdict, r.Reasons)
	}

	r, out := evaluateWithScope(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{boundary(nil)}}, scope)
	if r.Verdict != conformance.VerdictViolation || len(out.Findings) != 1 {
		t.Errorf("violation with a breach: %s findings %d", r.Verdict, len(out.Findings))
	}
}

func evaluateCycleSearch(t *testing.T, maxVisited int) (*conformance.RuleResult, conformance.EvaluateOutput) {
	t.Helper()

	p := acyclic(func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })
	edges := []*observation.EvidenceEdge{edge(inv, store), edge(store, inv)}

	limits := workspace.DefaultLimits()
	if maxVisited > 0 {
		limits.Traversal.MaxVisited = maxVisited
	}

	return evaluateWithScope(t, &scenario{observed: observed, edges: edges, plans: []policy.RulePlan{p}}, &conformance.ScopeContext{Limits: limits})
}

func TestTraversalLimitMakesACycleSearchIncompleteNotPass(t *testing.T) {
	t.Parallel()

	// Within the limit the cycle is found.
	r, out := evaluateCycleSearch(t, 0)
	if !hasOneViolation(r, &out) || r.Coverage.TraversalIncomplete {
		t.Fatalf("within the limit: %s findings %d incomplete %v", r.Verdict, len(out.Findings), r.Coverage.TraversalIncomplete)
	}

	// Over the limit the traversal stops: the result is incomplete and is not `pass`.
	r, out = evaluateCycleSearch(t, 1)
	if r.Verdict == conformance.VerdictPass || !traversalCutShort(r) {
		t.Fatalf("over the limit: %s incomplete %v reasons %v", r.Verdict, r.Coverage.TraversalIncomplete, r.Reasons)
	}

	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != diag.CON030 {
		t.Errorf("diagnostics = %+v", out.Diagnostics)
	}
}

func TestTraversalLimitMakesAPathSearchIncompleteNotPass(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{edge(inv, helper), edgeOf(helper, store, observation.KindImports, observation.ConfidenceSyntactic, 8)}

	r, _ := evaluateWithScope(t, &scenario{edges: edges, plans: []policy.RulePlan{denyPathPlan(2)}}, &conformance.ScopeContext{Limits: workspace.DefaultLimits()})
	if r.Verdict != conformance.VerdictViolation {
		t.Fatalf("within the limit: %s", r.Verdict)
	}

	limits := workspace.DefaultLimits()
	limits.Traversal.MaxVisited = 1

	r, _ = evaluateWithScope(t, &scenario{edges: edges, plans: []policy.RulePlan{denyPathPlan(2)}}, &conformance.ScopeContext{Limits: limits})
	if r.Verdict != conformance.VerdictUnknown || !traversalCutShort(r) {
		t.Fatalf("over the limit: %s incomplete %v reasons %v", r.Verdict, r.Coverage.TraversalIncomplete, r.Reasons)
	}
}

func TestTraversalLimitOfADAGIsNeverPass(t *testing.T) {
	t.Parallel()

	p := acyclic(func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })
	limits := workspace.DefaultLimits()
	limits.Traversal.MaxVisited = 1

	r, _ := evaluateWithScope(t, &scenario{observed: observed, edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{p}}, &conformance.ScopeContext{Limits: limits})
	if r.Verdict == conformance.VerdictPass {
		t.Fatalf("an incomplete traversal gave pass; incomplete = %v", r.Coverage.TraversalIncomplete)
	}
}
