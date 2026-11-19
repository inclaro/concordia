package conformance_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

var (
	inv      = obs("services.billing.src.Invoice", "services/billing/src/Invoice.ts")
	store    = obs("services.fulfillment.src.internal.Store", "services/fulfillment/src/internal/Store.ts")
	contract = obs("packages.fulfillment-contract.src.index", "packages/fulfillment-contract/src/index.ts")
	notify   = obs("services.fulfillment.src.notify", "services/fulfillment/src/notify.ts")
	observed = []*observation.ObservedNode{inv, store, contract, notify}
)

func boundary(edit func(*policy.RulePlan)) policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyBoundary, Allow: &policy.BoundaryAllow{
		TargetSourceRoots: []string{"packages/fulfillment-contract/**"},
		TargetCalmNodes:   []string{}, EdgeKinds: []observation.CanonicalEdgeKind{}, TargetTags: []string{},
	}}, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing"}
		p.Targets = []string{"fulfillment"}

		if edit != nil {
			edit(p)
		}
	})
}

func acyclic(edit func(*policy.RulePlan)) policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyAcyclic, CollapseBy: policy.CollapseByArchitectureBinding}, edit)
}

func reasonCodes(r *conformance.RuleResult) []string {
	codes := make([]string, 0, len(r.Reasons))
	for _, x := range r.Reasons {
		codes = append(codes, x.Code)
	}

	return codes
}

func evalRequireEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func evalExpectEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func evalExpectLocation(t *testing.T, got *conformance.Location, want conformance.Location) {
	t.Helper()

	if got == nil {
		t.Errorf("location = nil, want %+v", want)

		return
	}

	evalExpectEqual(t, "location", *got, want)
}

func evalRequireVerdict(t *testing.T, r *conformance.RuleResult, want conformance.RuleVerdict) {
	t.Helper()

	if r.Verdict != want {
		t.Fatalf("verdict %s, want %s; reasons %v", r.Verdict, want, r.Reasons)
	}
}

func evalRequireFirstReason(t *testing.T, r *conformance.RuleResult, code string) {
	t.Helper()

	if r.Reasons[0].Code != code {
		t.Fatalf("first reason %s, want %s; reasons %v", r.Reasons[0].Code, code, r.Reasons)
	}
}

func evalRequireReason(t *testing.T, r *conformance.RuleResult, code string, want bool) {
	t.Helper()

	if slices.Contains(reasonCodes(r), code) != want {
		t.Fatalf("reason %s present = %t, want %t; reasons %v", code, !want, want, r.Reasons)
	}
}

func evalObservedFiles() []string {
	files := make([]string, 0, len(observed))
	for _, o := range observed {
		files = append(files, o.FilePath)
	}

	return files
}

func TestBoundaryFlagsForbiddenEdgeAndSparesAllowedRoot(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed,
		edges:    []*observation.EvidenceEdge{edge(inv, store), edgeOf(inv, contract, observation.KindImports, observation.ConfidenceSyntactic, 4)},
		plans:    []policy.RulePlan{boundary(nil)},
	}))

	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictViolation)
	evalRequireEqual(t, "findings", len(out.Findings), 1)

	f := out.Findings[0]
	evalExpectEqual(t, "kind", f.Kind, conformance.KindForbiddenDependency)
	evalExpectEqual(t, "source", f.Source.ArchitectureID, "billing")
	evalExpectEqual(t, "target", f.Target.ArchitectureID, "fulfillment")
	evalExpectLocation(t, f.Location, conformance.Location{Path: "services/billing/src/Invoice.ts", Line: 3})
	evalExpectEqual(t, "disposition", f.Disposition, "active")
	evalExpectEqual(t, "evidence path length", len(f.EvidencePath), 3)

	if f.Owner == nil {
		t.Fatal("owner = nil, want team-billing")
	}

	evalExpectEqual(t, "owner", *f.Owner, "team-billing")
}

func TestBoundaryPassesWithCoverage(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{observed: observed, edges: []*observation.EvidenceEdge{edge(inv, contract)}, plans: []policy.RulePlan{boundary(nil)}}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictPass)

	if r.Coverage == nil {
		t.Fatal("coverage = nil")
	}

	evalRequireEqual(t, "file coverage ratio", r.Coverage.Files.Ratio, 1)
}

func TestBoundaryNotApplicableWhenSubjectEmpty(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{observed: observed, plans: []policy.RulePlan{boundary(func(p *policy.RulePlan) { p.Subjects = []string{} })}}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictNotApplicable)
	evalRequireFirstReason(t, r, "CO-CON-022")
}

func TestBoundaryGroupsEdgesOfOneFingerprint(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed,
		edges: []*observation.EvidenceEdge{
			edgeOf(inv, store, observation.KindImports, observation.ConfidenceSyntactic, 3),
			edgeOf(inv, store, observation.KindImports, observation.ConfidenceSyntactic, 9),
		},
		plans: []policy.RulePlan{boundary(nil)},
	}))

	if len(out.Findings) != 1 {
		t.Fatalf("findings %d", len(out.Findings))
	}

	want := []conformance.Location{{Path: "services/billing/src/Invoice.ts", Line: 9}}
	if !slices.Equal(out.Findings[0].RelatedLocations, want) {
		t.Errorf("related %+v", out.Findings[0].RelatedLocations)
	}
}

func TestBoundaryUnknownForHeuristicEdgeBelowThreshold(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed,
		edges:    []*observation.EvidenceEdge{edgeOf(inv, store, observation.KindCalls, observation.ConfidenceHeuristic, 3)},
		plans:    []policy.RulePlan{boundary(nil)},
	}))
	r := &out.RuleResults[0]

	evalRequireEqual(t, "findings", len(out.Findings), 0)
	evalRequireVerdict(t, r, conformance.VerdictUnknown)
	evalRequireReason(t, r, "CO-CON-023", true)
}

func TestBoundaryKeepsExcludedFilesInTotal(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed:   observed,
		plans:      []policy.RulePlan{boundary(nil)},
		extraFiles: []string{"services/billing/src/Old.kt"},
		cbmFiles:   evalObservedFiles(),
		exclude:    []string{"services/billing/src/Old.kt"},
	}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictUnknown)
	evalRequireFirstReason(t, r, "CO-CON-021")
}

func TestBoundaryUnknownWhenExcludedLanguageHidesInRatio(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed:   observed,
		plans:      []policy.RulePlan{boundary(func(p *policy.RulePlan) { p.EvidenceRequirements.MinimumCoverage = 0.5 })},
		extraFiles: []string{"services/billing/src/Old.kt"},
		cbmFiles:   evalObservedFiles(),
		exclude:    []string{"services/billing/src/Old.kt"},
	}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictUnknown)
	evalRequireReason(t, r, "CO-CON-026", true)
	evalRequireReason(t, r, "CO-CON-021", false)
}

func TestBoundaryIgnoresLanguageThatCbmNeverIndexes(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed:   observed,
		plans:      []policy.RulePlan{boundary(func(p *policy.RulePlan) { p.EvidenceRequirements.MinimumCoverage = 0.5 })},
		extraFiles: []string{"services/billing/Makefile"},
		cbmFiles:   evalObservedFiles(),
	}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictPass)
	evalRequireReason(t, r, "CO-CON-026", false)
}

func TestBoundaryNotApplicableWhenTargetSelectsNothing(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed,
		edges:    []*observation.EvidenceEdge{edge(inv, store)},
		plans:    []policy.RulePlan{boundary(func(p *policy.RulePlan) { p.Targets = nil })},
	}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictNotApplicable)

	if !slices.Equal(reasonCodes(r), []string{"CO-CON-025"}) {
		t.Fatalf("reasons %v", r.Reasons)
	}

	evalRequireEqual(t, "findings", len(out.Findings), 0)
}

func TestBoundaryUnknownForUnsupportedEdgeKind(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed,
		plans: []policy.RulePlan{boundary(func(p *policy.RulePlan) {
			p.UnsupportedEdgeKinds = []observation.CanonicalEdgeKind{observation.KindUsesType}
		})},
	}))
	r := &out.RuleResults[0]

	evalRequireVerdict(t, r, conformance.VerdictUnknown)
	evalRequireFirstReason(t, r, "CO-POL-009")
}

func TestEvaluationErrorBecomesErrorVerdict(t *testing.T) {
	t.Parallel()

	bad := boundary(nil)
	bad.Predicate = policy.Predicate{Family: policy.FamilyBoundary}

	other := acyclic(func(p *policy.RulePlan) {
		p.RuleID = "R-2"
		p.Subjects = []string{"billing"}
	})

	out := conformance.Evaluate(build(&scenario{observed: observed, edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{bad, other}}))

	evalRequireEqual(t, "rule results", len(out.RuleResults), 2)
	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictError)
	evalRequireFirstReason(t, &out.RuleResults[0], diag.CON099)
}

func TestLayerFlagsInversionOnly(t *testing.T) {
	t.Parallel()

	layered := plan(policy.Predicate{Family: policy.FamilyLayer}, func(p *policy.RulePlan) {
		p.Subjects = []string{"billing", "fulfillment"}
		p.Targets = []string{"billing", "fulfillment"}
		p.Layers = [][]string{{"billing"}, {"fulfillment"}}
	})

	out := conformance.Evaluate(build(&scenario{
		observed: observed, edges: []*observation.EvidenceEdge{edge(inv, store), edge(notify, inv)}, plans: []policy.RulePlan{layered},
	}))

	evalRequireEqual(t, "findings", len(out.Findings), 1)
	evalExpectEqual(t, "kind", out.Findings[0].Kind, conformance.KindLayerViolation)
	evalExpectEqual(t, "source", out.Findings[0].Source.ArchitectureID, "fulfillment")
}

func declaredOnly() policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyDeclaredOnly}, func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment", "ledger"} })
}

func TestDeclaredOnlyReportsUndeclaredAndContradiction(t *testing.T) {
	t.Parallel()

	ledger := obs("services.ledger.api", "services/ledger/api.py")
	rels := []intent.RelationshipDeclaration{connects("c1", "billing", "fulfillment", &intent.RelationshipObservation{
		AllowedEdgeKinds: []observation.CanonicalEdgeKind{observation.KindImports},
	})}

	out := conformance.Evaluate(build(&scenario{
		observed: append(slices.Clone(observed), ledger),
		edges: []*observation.EvidenceEdge{
			edgeOf(inv, store, observation.KindCalls, observation.ConfidenceResolved, 3), edge(notify, inv), edge(inv, store),
		},
		relationships: rels,
		plans:         []policy.RulePlan{declaredOnly()},
	}))

	kinds := make([]string, 0, len(out.Findings))
	for _, f := range out.Findings {
		kinds = append(kinds, string(f.Kind))
	}

	slices.Sort(kinds)

	if !slices.Equal(kinds, []string{"contradiction", "undeclared"}) {
		t.Fatalf("kinds %v", kinds)
	}

	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictViolation)
}

func TestDeclaredOnlyPassesConformingEdge(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed, edges: []*observation.EvidenceEdge{edge(inv, store)},
		relationships: []intent.RelationshipDeclaration{connects("c1", "billing", "fulfillment", nil)},
		plans:         []policy.RulePlan{declaredOnly()},
	}))

	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictPass)
}

var (
	requireRule = plan(policy.Predicate{Family: policy.FamilyObservedRequired}, func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })
	requiredRel = []intent.RelationshipDeclaration{connects("c1", "billing", "fulfillment", &intent.RelationshipObservation{
		AllowedEdgeKinds: []observation.CanonicalEdgeKind{observation.KindImports}, Required: true,
	})}
)

func TestObservedRequiredFindsUnobservedWithFullCoverage(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{observed: observed, relationships: requiredRel, plans: []policy.RulePlan{requireRule}}))

	evalRequireEqual(t, "findings", len(out.Findings), 1)
	evalExpectEqual(t, "kind", out.Findings[0].Kind, conformance.KindUnobserved)
	evalExpectEqual(t, "evidence path length", len(out.Findings[0].EvidencePath), 0)
}

func TestObservedRequiredUnknownWithPartialCoverage(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed, relationships: requiredRel, plans: []policy.RulePlan{requireRule},
		extraFiles: []string{"services/billing/x.ts", "services/billing/y.ts"}, cbmFiles: evalObservedFiles(),
	}))

	evalRequireEqual(t, "findings", len(out.Findings), 0)
	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictUnknown)
}

func TestObservedRequiredPassesWhenEdgeQualifies(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed, edges: []*observation.EvidenceEdge{edge(inv, store)}, relationships: requiredRel, plans: []policy.RulePlan{requireRule},
	}))

	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictPass)
}

func evalCountEdgeSteps(path []conformance.EvidenceStep) int {
	edges := 0

	for _, s := range path {
		if s.Edge != nil {
			edges++
		}
	}

	return edges
}

func TestAcyclicFindsCycleOfThreeNodes(t *testing.T) {
	t.Parallel()

	a := obs("services.billing.a", "services/billing/a.ts")
	b := obs("services.fulfillment.b", "services/fulfillment/b.ts")
	c := obs("services.ledger.c", "services/ledger/c.py")
	p := acyclic(func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment", "ledger"} })

	out := conformance.Evaluate(build(&scenario{
		observed: []*observation.ObservedNode{a, b, c}, edges: []*observation.EvidenceEdge{edge(a, b), edge(b, c), edge(c, a)}, plans: []policy.RulePlan{p},
	}))

	if len(out.Findings) != 1 {
		t.Fatalf("findings %d", len(out.Findings))
	}

	f := out.Findings[0]
	evalExpectEqual(t, "kind", f.Kind, conformance.KindCycle)
	evalExpectEqual(t, "fingerprint", f.Fingerprint, conformance.CycleFingerprint("R-1", []string{"billing", "fulfillment", "ledger"}))
	evalExpectEqual(t, "edge steps", evalCountEdgeSteps(f.EvidencePath), 3)
}

func TestAcyclicPassesDAG(t *testing.T) {
	t.Parallel()

	p := acyclic(func(p *policy.RulePlan) { p.Subjects = []string{"billing", "fulfillment"} })
	out := conformance.Evaluate(build(&scenario{observed: observed, edges: []*observation.EvidenceEdge{edge(inv, store)}, plans: []policy.RulePlan{p}}))

	evalRequireVerdict(t, &out.RuleResults[0], conformance.VerdictPass)
}

func evalClassificationOf(entries []conformance.ReconciliationEntry, from, to string) conformance.Classification {
	for _, e := range entries {
		if e.From == from && e.To == to {
			return e.Classification
		}
	}

	return ""
}

func TestReconcileClassifiesPairs(t *testing.T) {
	t.Parallel()

	ledger := obs("services.ledger.api", "services/ledger/api.py")
	input := build(&scenario{
		observed: append(slices.Clone(observed), ledger),
		edges:    []*observation.EvidenceEdge{edge(inv, store), edge(notify, inv), edgeOf(inv, ledger, observation.KindCalls, observation.ConfidenceResolved, 3)},
		relationships: []intent.RelationshipDeclaration{
			connects("c1", "billing", "fulfillment", &intent.RelationshipObservation{AllowedEdgeKinds: []observation.CanonicalEdgeKind{observation.KindImports}, Required: true}),
			connects("c2", "ledger", "billing", nil),
		},
	})

	entries := conformance.Reconcile(&input.ReconcileInput)

	for _, c := range []struct {
		from, to string
		want     conformance.Classification
	}{
		{"billing", "fulfillment", conformance.ClassConforming},
		{"fulfillment", "billing", conformance.ClassUndeclared},
		{"billing", "ledger", conformance.ClassUndeclared},
		{"ledger", "billing", conformance.ClassUnobserved},
	} {
		if got := evalClassificationOf(entries, c.from, c.to); got != c.want {
			t.Errorf("%s -> %s: got %q want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestFingerprintIgnoresLineAndNormalizesWindowsPaths(t *testing.T) {
	t.Parallel()

	run := func(line int) string {
		out := conformance.Evaluate(build(&scenario{
			observed: observed, edges: []*observation.EvidenceEdge{edgeOf(inv, store, observation.KindImports, observation.ConfidenceSyntactic, line)},
			plans: []policy.RulePlan{boundary(nil)},
		}))

		return out.Findings[0].Fingerprint
	}

	if run(3) != run(77) {
		t.Error("the line changes the fingerprint")
	}

	base := conformance.Parts{
		RuleID: "R-1", SourceArchitectureID: "billing", TargetArchitectureID: "fulfillment", CanonicalEdgeKind: "imports",
		SourceQualifiedName: "a.b", TargetQualifiedName: "c.d", NormalizedRepositoryPath: "services/billing/x.ts",
	}
	win := base
	win.NormalizedRepositoryPath = `services\billing\x.ts`

	if conformance.Fingerprint(win) != conformance.Fingerprint(base) {
		t.Error("a Windows path changes the fingerprint")
	}
}

func TestFingerprintChangesWithTargetQualifiedName(t *testing.T) {
	t.Parallel()

	base := conformance.Parts{RuleID: "R-1", SourceArchitectureID: "billing", TargetArchitectureID: "fulfillment", CanonicalEdgeKind: "imports"}
	other := base
	other.TargetQualifiedName = "c.e"

	if conformance.Fingerprint(other) == conformance.Fingerprint(base) {
		t.Error("equal fingerprints")
	}

	got := conformance.Fingerprint(base)
	evalExpectEqual(t, "fingerprint length", len(got), len("sha256:")+64)
	evalExpectEqual(t, "fingerprint prefix", got[:7], "sha256:")
}

// evalTypedRule is a typed rule (an obligation block) that requires its subject to use an image.
func evalTypedRule() policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyTyped}, func(p *policy.RulePlan) {
		p.RuleID = "ZZZ-TYPED-001"
		p.Subjects = []string{"billing"}
		p.Traversal.EdgeKinds = []observation.CanonicalEdgeKind{observation.KindUsesImage}
		p.Obligation = &policy.ObligationSpec{
			Type: policy.ObligationRequired, Multiplicity: policy.DefaultMultiplicity, MinimumResolution: policy.DefaultMinimumResolution,
			Completeness: policy.DefaultCompleteness, Roles: policy.DefaultEvidenceRoles(),
			Relation: policy.RelationSpec{Kind: observation.KindUsesImage, Direction: policy.DirectionFromWorkload, Target: []string{}},
		}
	})
}

// A typed rule (an obligation block) is evaluated by its obligations: it gets a rule result of the family
// `typed` (here `unknown`, because the run has no coverage statement, never a false pass), and the other rules
// keep their results.
func TestEvaluateEvaluatesTypedRulesAndKeepsTheOthers(t *testing.T) {
	t.Parallel()

	out := conformance.Evaluate(build(&scenario{
		observed: observed,
		edges:    []*observation.EvidenceEdge{edge(inv, store)},
		plans:    []policy.RulePlan{evalTypedRule(), boundary(nil)},
	}))

	if len(out.RuleResults) != 2 {
		t.Fatalf("results = %+v", out.RuleResults)
	}

	other := &out.RuleResults[0]
	evalExpectEqual(t, "the other rule keeps its rule ID", other.RuleID, "R-1")
	evalExpectEqual(t, "the other rule keeps its family", other.Family, policy.FamilyBoundary)
	evalExpectEqual(t, "the other rule keeps its verdict", other.Verdict, conformance.VerdictViolation)

	typed := &out.RuleResults[1]
	evalExpectEqual(t, "typed rule ID", typed.RuleID, "ZZZ-TYPED-001")
	evalExpectEqual(t, "typed family", typed.Family, policy.FamilyTyped)
	evalExpectEqual(t, "typed verdict", typed.Verdict, conformance.VerdictUnknown)
	evalRequireEqual(t, "typed obligations", len(typed.Obligations), 1)
	evalExpectEqual(t, "typed obligation reason", typed.Obligations[0].Reason, conformance.ReasonCoverageIncomplete)

	if policy.FamilyTyped.EdgeFindings() {
		t.Error("a typed rule has no edge findings to reduce to the changed files")
	}
}

func workloadNode(qn, filePath string, line int) *observation.ObservedNode {
	n := obs(qn, filePath)
	n.Label = "Resource"
	n.StartLine = line

	return n
}

func selectsPlan() policy.RulePlan {
	return boundary(func(p *policy.RulePlan) {
		p.Traversal.EdgeKinds = []observation.CanonicalEdgeKind{observation.KindSelectsWorkload}
	})
}

// evd05Workload is an unbound workload that a code module selects through one fact.
func evd05Workload() (*observation.ObservedNode, *observation.EvidenceEdge) {
	workload := workloadNode("k8s.prod.billing::apps/Deployment/payments/billing", "k8s/prod/billing.yaml", 7)

	return workload, edgeOf(inv, workload, observation.KindSelectsWorkload, observation.ConfidenceSyntactic, 5)
}

func evd05CheckFinding(t *testing.T, f *conformance.Finding, workload *observation.ObservedNode) {
	t.Helper()

	evalExpectEqual(t, "kind", f.Kind, conformance.KindBindingGap)
	evalExpectEqual(t, "next action", f.NextAction, "resolve binding")
	evalExpectEqual(t, "severity", f.Severity, policy.SeverityInfo)
	evalExpectEqual(t, "source", f.Source.ArchitectureID, "billing")

	if f.Target.Observed == nil {
		t.Fatal("target is not observed")
	}

	evalExpectEqual(t, "target", f.Target.Observed.QualifiedName, workload.QualifiedName)
	evalExpectLocation(t, f.Location, conformance.Location{Path: "k8s/prod/billing.yaml", Line: 7})
}

func evd05CheckFactKept(t *testing.T, f *conformance.Finding) {
	t.Helper()

	if f.Edge == nil {
		t.Fatalf("fact is not kept in the finding: %+v", f)
	}

	evalExpectEqual(t, "edge kind", f.Edge.Kind, observation.KindSelectsWorkload)
	evalExpectEqual(t, "evidence path length", len(f.EvidencePath), 3)

	if f.Fingerprint == "" {
		t.Errorf("fact is not kept in the finding: %+v", f)
	}
}

func evd05CheckDoesNotFail(t *testing.T, out *conformance.EvaluateOutput, f *conformance.Finding) {
	t.Helper()

	if conformance.IsFailing(f, diag.SeverityWarning) || conformance.IsFailing(f, diag.SeverityInfo) {
		t.Error("a binding gap must not fail a run")
	}

	evalExpectEqual(t, "result", f.Result, conformance.VerdictUnknown)
	evalExpectEqual(t, "reason", f.Reason, conformance.ReasonBindingGap)

	if got := conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityInfo); got != diag.OK && got != diag.Unknown {
		t.Errorf("exit %d: a binding gap with failOn info is not a violation", got)
	}
}

func TestEVD05UnboundWorkloadGivesABindingGapAndKeepsTheFact(t *testing.T) {
	t.Parallel()

	workload, fact := evd05Workload()
	in := build(&scenario{observed: []*observation.ObservedNode{inv, workload}, edges: []*observation.EvidenceEdge{fact}, plans: []policy.RulePlan{selectsPlan()}})

	out := conformance.Evaluate(in)

	result := &out.RuleResults[0]
	evalRequireVerdict(t, result, conformance.VerdictUnknown)
	evalRequireReason(t, result, diag.BND005, true)
	evalRequireEqual(t, "findings", len(out.Findings), 1)

	evd05CheckFinding(t, &out.Findings[0], workload)
	evd05CheckFactKept(t, &out.Findings[0])
	evd05CheckDoesNotFail(t, &out, &out.Findings[0])

	// The fact itself stays in the evidence set.
	evalRequireEqual(t, "evidence edges", len(in.Evidence.Edges), 1)

	if in.Evidence.Edges[0] != fact {
		t.Errorf("evidence edges = %+v", in.Evidence.Edges)
	}
}

func TestBoundWorkloadAndCodeEdgesGiveNoBindingGap(t *testing.T) {
	t.Parallel()

	workload, fact := evd05Workload()
	in := build(&scenario{observed: []*observation.ObservedNode{inv, workload}, edges: []*observation.EvidenceEdge{fact}, plans: []policy.RulePlan{selectsPlan()}})
	in.BindingSet.ByObserved[workload.ObservedID] = []string{"billing-runtime"}

	if out := conformance.Evaluate(in); len(out.Findings) != 0 {
		t.Errorf("bound workload: findings = %+v", out.Findings)
	}

	// An unbound code module is only counted; the rule has the default code kinds.
	in = build(&scenario{observed: observed, edges: []*observation.EvidenceEdge{edge(inv, obs("x.Unbound", "other/x.ts"))}, plans: []policy.RulePlan{boundary(nil)}})

	if out := conformance.Evaluate(in); len(out.Findings) != 0 {
		t.Errorf("unbound code endpoint: findings = %+v", out.Findings)
	}
}
