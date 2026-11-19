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

var (
	ledgerAPI = obs("services.ledger.api", "services/ledger/api.py")
	helper    = obs("services.shared.helper", "services/shared/helper.ts")
	allObs    = []*observation.ObservedNode{inv, store, contract, notify, ledgerAPI, helper}
)

func evaluateOne(t *testing.T, s *scenario) (*conformance.RuleResult, []conformance.Finding) {
	t.Helper()

	if s.observed == nil {
		s.observed = allObs
	}

	out := conformance.Evaluate(build(s))
	if len(out.RuleResults) != 1 {
		t.Fatalf("rule results = %d", len(out.RuleResults))
	}

	return &out.RuleResults[0], out.Findings
}

func wantViolation(t *testing.T, r *conformance.RuleResult, findings []conformance.Finding, kind conformance.FindingKind) *conformance.Finding {
	t.Helper()

	if r.Verdict != conformance.VerdictViolation || len(findings) != 1 {
		t.Fatalf("verdict %s findings %+v reasons %v", r.Verdict, findings, r.Reasons)
	}

	if findings[0].Kind != kind {
		t.Fatalf("verdict %s findings %+v reasons %v", r.Verdict, findings, r.Reasons)
	}

	return &findings[0]
}

func wantPass(t *testing.T, r *conformance.RuleResult, findings []conformance.Finding) {
	t.Helper()

	if r.Verdict != conformance.VerdictPass || len(findings) != 0 {
		t.Fatalf("verdict %s findings %+v reasons %v", r.Verdict, findings, r.Reasons)
	}
}

func edgePlan(family policy.PredicateFamily, edit func(*policy.RulePlan)) policy.RulePlan {
	return plan(policy.Predicate{Family: family}, edit)
}

func TestAllowOnlyFlagsADependencyOutsideTheTargets(t *testing.T) {
	t.Parallel()

	p := edgePlan(policy.FamilyAllowOnly, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing"}
		p.Targets = []string{"fulfillment"}
	})

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{p}})
	f := wantViolation(t, r, findings, conformance.KindDisallowedDep)

	if f.Message != "dependency billing -> ledger is outside the allowed targets" {
		t.Errorf("message = %q", f.Message)
	}
}

func TestAllowOnlyPassesADependencyOnAnAllowedTarget(t *testing.T) {
	t.Parallel()

	p := edgePlan(policy.FamilyAllowOnly, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing"}
		p.Targets = []string{"fulfillment"}
	})

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{p}})
	wantPass(t, r, findings)
}

func TestIndependentFlagsADependencyBetweenPeers(t *testing.T) {
	t.Parallel()

	p := edgePlan(policy.FamilyIndependent, func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{p}})
	f := wantViolation(t, r, findings, conformance.KindPeerDependency)

	if f.Message != "peer dependency billing -> fulfillment" {
		t.Errorf("message = %q", f.Message)
	}
}

func TestIndependentPassesWhenTheDependencyLeavesThePeers(t *testing.T) {
	t.Parallel()

	p := edgePlan(policy.FamilyIndependent, func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{p}})
	wantPass(t, r, findings)
}

func TestIndependentSkipsAContainerAndItsPart(t *testing.T) {
	t.Parallel()

	p := edgePlan(policy.FamilyIndependent, func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })
	composed := intent.RelationshipDeclaration{
		ID: "c1", Kind: intent.RelComposedOf, Container: "billing", Targets: []string{"fulfillment"}, Pointer: "/relationships/c1",
	}

	r, findings := evaluateOne(t, &scenario{
		edges: []*observation.EvidenceEdge{edge(inv, store)}, relationships: []intent.RelationshipDeclaration{composed},
		plans: []policy.RulePlan{p},
	})
	wantPass(t, r, findings)
}

func TestIndependentIsNotApplicableWithOneSubject(t *testing.T) {
	t.Parallel()

	p := edgePlan(policy.FamilyIndependent, func(p *policy.RulePlan) { p.Subjects = []string{"billing"} })

	r, _ := evaluateOne(t, &scenario{plans: []policy.RulePlan{p}})
	if r.Verdict != conformance.VerdictNotApplicable || !slices.Equal(reasonCodes(r), []string{"CO-CON-022"}) {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.Reasons)
	}
}

func publicAPIPlan(explicitTargets []string) policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyPublicAPIOnly, APISourceRoots: []string{"packages/fulfillment-contract/**"}},
		func(p *policy.RulePlan) {
			p.Subjects = []string{"billing"}
			p.Targets = explicitTargets
		})
}

func TestPublicAPIOnlyFlagsAUseOfAnInternalFile(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{
		edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{publicAPIPlan([]string{"fulfillment"})},
	})
	f := wantViolation(t, r, findings, conformance.KindNonPublicAPI)

	want := "billing uses fulfillment outside its public API: services/fulfillment/src/internal/Store.ts"
	if f.Message != want {
		t.Errorf("message = %q", f.Message)
	}
}

func TestPublicAPIOnlyPassesAUseOfAPublicFile(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{
		edges: []*observation.EvidenceEdge{edge(inv, contract)}, plans: []policy.RulePlan{publicAPIPlan([]string{"fulfillment"})},
	})
	wantPass(t, r, findings)
}

func TestPublicAPIOnlyIsNotApplicableWhenTheTargetSelectsNothing(t *testing.T) {
	t.Parallel()

	r, _ := evaluateOne(t, &scenario{plans: []policy.RulePlan{publicAPIPlan([]string{})}})
	if r.Verdict != conformance.VerdictNotApplicable || !slices.Equal(reasonCodes(r), []string{"CO-CON-025"}) {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.Reasons)
	}
}

func ownershipPlan() policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyOwnership}, func(p *policy.RulePlan) {
		p.Subjects = []string{"fulfillment"}
		p.Targets = []string{"billing"}
		p.Traversal.Direction = "inbound"
	})
}

func TestOwnershipFlagsAnAccessByANonOwner(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(ledgerAPI, store)}, plans: []policy.RulePlan{ownershipPlan()}})
	f := wantViolation(t, r, findings, conformance.KindOwnershipViolation)

	if f.Message != "ledger accesses fulfillment, which only billing may access" {
		t.Errorf("message = %q", f.Message)
	}
}

func TestOwnershipPassesAnAccessByTheOwner(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{ownershipPlan()}})
	wantPass(t, r, findings)
}

func budgetPlan(measure string, maxFindings int, edit func(*policy.RulePlan)) policy.RulePlan {
	predicate := policy.Predicate{Family: policy.FamilyBudget, Budget: &policy.Budget{Measure: measure, Max: maxFindings}}
	if measure == policy.BudgetCycles {
		predicate.CollapseBy = policy.CollapseByArchitectureBinding
	}

	return plan(predicate, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing"}
		if measure == policy.BudgetForbiddenEdges {
			p.Targets = []string{"fulfillment"}
		}

		if edit != nil {
			edit(p)
		}
	})
}

func twoForbiddenEdges() []*observation.EvidenceEdge {
	return []*observation.EvidenceEdge{
		edgeOf(inv, store, observation.KindImports, observation.ConfidenceSyntactic, 3),
		edgeOf(inv, notify, observation.KindImports, observation.ConfidenceSyntactic, 4),
	}
}

func TestBudgetOfForbiddenEdgesIsExceeded(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: twoForbiddenEdges(), plans: []policy.RulePlan{budgetPlan(policy.BudgetForbiddenEdges, 1, nil)}})
	f := wantViolation(t, r, findings, conformance.KindBudgetExceeded)

	if f.Message != "forbidden-edges count 2 exceeds the budget 1" || len(f.RelatedLocations) != 1 {
		t.Errorf("finding %+v", f)
	}

	if f.Edge != nil {
		t.Errorf("finding %+v", f)
	}
}

func TestBudgetOfForbiddenEdgesPassesWithinTheMaximum(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: twoForbiddenEdges(), plans: []policy.RulePlan{budgetPlan(policy.BudgetForbiddenEdges, 2, nil)}})
	wantPass(t, r, findings)
}

func TestBudgetOfCyclesIsExceeded(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{edge(inv, store), edge(store, inv)}
	edit := func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} }

	r, findings := evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{budgetPlan(policy.BudgetCycles, 0, edit)}})
	f := wantViolation(t, r, findings, conformance.KindBudgetExceeded)

	if f.Message != "cycles count 1 exceeds the budget 0" {
		t.Errorf("message = %q", f.Message)
	}

	r, findings = evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{budgetPlan(policy.BudgetCycles, 1, edit)}})
	wantPass(t, r, findings)
}

func requireEdgePlan() policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyRequireEdge}, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing"}
		p.Targets = []string{"fulfillment"}
		p.Traversal.EdgeKinds = []observation.CanonicalEdgeKind{observation.KindImports}
	})
}

func TestRequireEdgeReportsAMissingEdge(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, ledgerAPI)}, plans: []policy.RulePlan{requireEdgePlan()}})
	f := wantViolation(t, r, findings, conformance.KindMissingEdge)

	if f.Message != "no imports edge from billing to any of fulfillment" {
		t.Errorf("finding %+v", f)
	}

	if f.Source.ArchitectureID != "billing" || f.Target.ArchitectureID != "fulfillment" {
		t.Errorf("finding %+v", f)
	}
}

func TestRequireEdgePassesWhenTheEdgeIsObserved(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{requireEdgePlan()}})
	wantPass(t, r, findings)
}

func TestRequireEdgeIsUnknownWithoutCoverage(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{
		plans: []policy.RulePlan{requireEdgePlan()}, extraFiles: []string{"services/billing/src/Old.kt"},
		cbmFiles: []string{"services/billing/src/Invoice.ts"},
	})
	if r.Verdict != conformance.VerdictUnknown || len(findings) != 0 {
		t.Fatalf("verdict %s findings %d reasons %v", r.Verdict, len(findings), r.Reasons)
	}
}

func requireEntityPlan(labels []string, pattern string) policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyRequireEntity, EntityLabels: labels, NamePattern: pattern},
		func(p *policy.RulePlan) {
			p.Subjects = []string{"billing"}
			p.Traversal.EdgeKinds = []observation.CanonicalEdgeKind{}
		})
}

func TestRequireEntityPassesWhenAMatchingNodeIsBound(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{plans: []policy.RulePlan{requireEntityPlan([]string{"Module"}, `\.Invoice$`)}})
	wantPass(t, r, findings)
}

func TestRequireEntityReportsAMissingLabelAndAMissingName(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{plans: []policy.RulePlan{requireEntityPlan([]string{"Class", "Function"}, "")}})
	f := wantViolation(t, r, findings, conformance.KindMissingEntity)

	if f.Message != "no observed Class/Function entity bound to billing" {
		t.Errorf("message = %q", f.Message)
	}

	r, findings = evaluateOne(t, &scenario{plans: []policy.RulePlan{requireEntityPlan([]string{"Module"}, `^nothing$`)}})
	wantViolation(t, r, findings, conformance.KindMissingEntity)
}

func denyPathPlan(maxDepth int) policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyDenyPath}, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing"}
		p.Targets = []string{"fulfillment"}
		p.Traversal.MaximumDepth = maxDepth
	})
}

func TestDenyPathFindsATransitivePathThroughAnUnboundNode(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{edge(inv, helper), edgeOf(helper, store, observation.KindImports, observation.ConfidenceSyntactic, 8)}

	r, findings := evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{denyPathPlan(2)}})
	f := wantViolation(t, r, findings, conformance.KindForbiddenPath)

	if len(f.EvidencePath) != 5 || f.Message != "forbidden path billing -> fulfillment (2 hops)" {
		t.Errorf("finding %+v", f)
	}

	if f.Target.Observed == nil || f.Target.Observed.QualifiedName != store.QualifiedName {
		t.Errorf("target %+v", f.Target)
	}

	if f.Location == nil || f.Location.Path != "services/billing/src/Invoice.ts" {
		t.Errorf("location %+v", f.Location)
	}
}

func TestDenyPathPassesWhenThePathIsLongerThanTheMaximumDepth(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{edge(inv, helper), edgeOf(helper, store, observation.KindImports, observation.ConfidenceSyntactic, 8)}

	r, findings := evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{denyPathPlan(1)}})
	wantPass(t, r, findings)
}

func TestDenyPathCountsASubThresholdPath(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{
		edge(inv, helper), edgeOf(helper, store, observation.KindImports, observation.ConfidenceHeuristic, 8),
	}

	r, findings := evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{denyPathPlan(2)}})
	if r.Verdict != conformance.VerdictUnknown || len(findings) != 0 {
		t.Fatalf("verdict %s findings %d coverage %+v", r.Verdict, len(findings), r.Coverage)
	}

	if r.Coverage.SubThresholdEvidence != 1 || !slices.Contains(reasonCodes(r), "CO-CON-023") {
		t.Fatalf("verdict %s findings %d coverage %+v", r.Verdict, len(findings), r.Coverage)
	}
}

func TestDenyPathIsNotApplicableWithoutTargets(t *testing.T) {
	t.Parallel()

	p := denyPathPlan(2)
	p.Targets = []string{}

	r, _ := evaluateOne(t, &scenario{plans: []policy.RulePlan{p}})
	if r.Verdict != conformance.VerdictNotApplicable || !slices.Equal(reasonCodes(r), []string{"CO-CON-025"}) {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.Reasons)
	}
}

func TestAggregationFileGroupsEdgesFromOneFile(t *testing.T) {
	t.Parallel()

	byEdge, _ := evaluateOne(t, &scenario{edges: twoForbiddenEdges(), plans: []policy.RulePlan{boundary(nil)}})
	if byEdge.FindingCount != 2 {
		t.Fatalf("edge aggregation findings = %d", byEdge.FindingCount)
	}

	grouped := boundary(func(p *policy.RulePlan) { p.Aggregation = policy.AggregationFile })

	r, findings := evaluateOne(t, &scenario{edges: twoForbiddenEdges(), plans: []policy.RulePlan{grouped}})
	f := wantViolation(t, r, findings, conformance.KindForbiddenDependency)

	if len(f.RelatedLocations) != 1 {
		t.Errorf("related locations = %v", f.RelatedLocations)
	}
}

func TestAggregationArchitecturePairGroupsAllEdgesOfThePair(t *testing.T) {
	t.Parallel()

	edges := append(twoForbiddenEdges(), edgeOf(contract, store, observation.KindCalls, observation.ConfidenceSyntactic, 5))
	grouped := boundary(func(p *policy.RulePlan) {
		p.Aggregation = policy.AggregationArchitecturePair
		p.Subjects = []string{"billing", "fulfillment"}
		p.Targets = []string{"fulfillment", "billing"}
	})

	r, findings := evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{grouped}})
	wantViolation(t, r, findings, conformance.KindForbiddenDependency)
}

func TestAggregationSymbolKeepsTheSourceSymbolApart(t *testing.T) {
	t.Parallel()

	other := obs("services.billing.src.Other", "services/billing/src/Other.ts")
	edges := []*observation.EvidenceEdge{edge(inv, store), edge(inv, notify), edge(other, store)}
	grouped := boundary(func(p *policy.RulePlan) { p.Aggregation = policy.AggregationSymbol })

	r, findings := evaluateOne(t, &scenario{observed: append(slices.Clone(allObs), other), edges: edges, plans: []policy.RulePlan{grouped}})
	if r.Verdict != conformance.VerdictViolation || len(findings) != 2 {
		t.Fatalf("verdict %s findings %d", r.Verdict, len(findings))
	}
}

func heuristicCallEdges() []*observation.EvidenceEdge {
	return []*observation.EvidenceEdge{edgeOf(inv, store, observation.KindCalls, observation.ConfidenceHeuristic, 3)}
}

func TestEdgeKindsConfidenceKeepsAHeuristicEdgeUnknownByDefault(t *testing.T) {
	t.Parallel()

	r, findings := evaluateOne(t, &scenario{edges: heuristicCallEdges(), plans: []policy.RulePlan{boundary(nil)}})
	if r.Verdict != conformance.VerdictUnknown || len(findings) != 0 {
		t.Fatalf("strict: verdict %s findings %d", r.Verdict, len(findings))
	}
}

func TestEdgeKindsConfidenceLetsAHeuristicEdgeMakeAFinding(t *testing.T) {
	t.Parallel()

	perKind := boundary(func(p *policy.RulePlan) {
		p.EvidenceRequirements.EdgeKindConfidence = map[observation.CanonicalEdgeKind]observation.Confidence{
			observation.KindCalls: observation.ConfidenceHeuristic,
		}
	})

	r, findings := evaluateOne(t, &scenario{edges: heuristicCallEdges(), plans: []policy.RulePlan{perKind}})
	wantViolation(t, r, findings, conformance.KindForbiddenDependency)

	calls := r.Coverage.EdgeKinds[observation.KindCalls]
	if !calls.Supported || calls.MinimumConfidence != observation.ConfidenceHeuristic {
		t.Errorf("calls coverage = %+v", calls)
	}

	if calls.Observed != 1 || calls.BelowThreshold != 0 {
		t.Errorf("calls coverage = %+v", calls)
	}

	if imports := r.Coverage.EdgeKinds[observation.KindImports]; imports.MinimumConfidence != observation.ConfidenceSyntactic || imports.Observed != 0 {
		t.Errorf("imports coverage = %+v", imports)
	}
}

func TestCoverageCountsEdgesBelowTheThresholdAndUnresolvedReferences(t *testing.T) {
	t.Parallel()

	edges := []*observation.EvidenceEdge{
		edgeOf(inv, store, observation.KindCalls, observation.ConfidenceHeuristic, 3),
		edgeOf(inv, contract, observation.KindImports, observation.ConfidenceSyntactic, 4),
		edgeOf(inv, notify, observation.KindAmbiguousUsage, observation.ConfidenceHeuristic, 5),
		edgeOf(ledgerAPI, notify, observation.KindAmbiguousUsage, observation.ConfidenceHeuristic, 6),
	}

	r, _ := evaluateOne(t, &scenario{edges: edges, plans: []policy.RulePlan{boundary(nil)}})

	if got := r.Coverage.EdgeKinds[observation.KindCalls]; got.BelowThreshold != 1 || got.Observed != 0 {
		t.Errorf("calls = %+v", got)
	}

	if got := r.Coverage.EdgeKinds[observation.KindImports]; got.Observed != 1 {
		t.Errorf("imports = %+v", got)
	}

	if r.Coverage.UnresolvedReferences != 1 {
		t.Errorf("unresolved references = %d", r.Coverage.UnresolvedReferences)
	}
}

// byteCoverageSizes are the file sizes of the byte coverage scenarios: one large file that the run excludes.
var byteCoverageSizes = map[string]int64{
	"services/billing/src/Invoice.ts":            10,
	"services/fulfillment/src/internal/Store.ts": 10,
	"packages/fulfillment-contract/src/index.ts": 10,
	"services/fulfillment/src/notify.ts":         10,
	"services/billing/src/Old.kt":                1000,
}

func excludedLargeFile(p *policy.RulePlan, cbmFiles []string) *scenario {
	return &scenario{
		plans: []policy.RulePlan{*p}, extraFiles: []string{"services/billing/src/Old.kt"}, exclude: []string{"services/billing/src/Old.kt"},
		sizes: byteCoverageSizes, cbmFiles: cbmFiles,
	}
}

func TestByteCoverageGivesUnknownWhenALargeFileIsExcluded(t *testing.T) {
	t.Parallel()

	minBytes, noLanguageGap := 0.99, 0.0

	p := boundary(func(p *policy.RulePlan) {
		p.EvidenceRequirements.MinimumCoverage = 0.5
		p.EvidenceRequirements.MinimumByteCoverage = &minBytes
		p.EvidenceRequirements.MinimumLanguageCoverage = &noLanguageGap
	})

	r, findings := evaluateOne(t, excludedLargeFile(&p, nil))

	if r.Verdict != conformance.VerdictUnknown || len(findings) != 0 {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.Reasons)
	}

	if !slices.Equal(reasonCodes(r), []string{"CO-CON-028"}) {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.Reasons)
	}

	if !strings.Contains(r.Reasons[0].Message, "byte coverage not met: 0.038 < 0.99") || r.Coverage.Files.Bytes.Total != 1040 {
		t.Errorf("reason %q bytes %+v", r.Reasons[0].Message, r.Coverage.Files.Bytes)
	}
}

func TestByteCoverageIsNotRequiredByDefault(t *testing.T) {
	t.Parallel()

	passing := boundary(func(p *policy.RulePlan) { p.EvidenceRequirements.MinimumCoverage = 0.5 })

	r, findings := evaluateOne(t, excludedLargeFile(&passing, nil))
	if slices.Contains(reasonCodes(r), "CO-CON-028") || len(findings) != 0 {
		t.Fatalf("without a byte requirement: reasons %v", r.Reasons)
	}
}
