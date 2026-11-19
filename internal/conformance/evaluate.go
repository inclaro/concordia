package conformance

import (
	"cmp"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// Snapshot names the frozen graph of a run.
type Snapshot struct {
	Project         string
	GraphGeneration string
}

// EvaluateInput is the input of Evaluate.
type EvaluateInput struct {
	// Profile is the capability profile of the build. Findings name it in their provider limits. nil: no profile applies.
	Profile *observation.ProfileRef
	// External gives the results of the rules of the families `effective-access`, `check-result`, and `assurance-claim`
	// (see external.go). nil: no assurance evidence exists, and every such rule is `unknown`.
	External *ExternalEvaluation
	Snapshot Snapshot
	// Typed is what the obligations of the typed rules need besides the evidence (environments, bindings, capability state, precedence).
	Typed TypedContext
	// Enforcement resolves the enforcement mode of each rule and environment (`check.enforcement`, `check.rollback`).
	// The zero value sets nothing: the rule's own mode, then the defaults.
	Enforcement EnforcementConfig
	Plans       []policy.RulePlan
	ReconcileInput
	// Scope is what the run knows about predicate coverage, failed scopes and limits. The zero value blocks nothing.
	Scope ScopeContext
}

// EvaluateOutput is the result of Evaluate.
type EvaluateOutput struct {
	// RuleResults are sorted by rule ID.
	RuleResults []RuleResult
	// Findings are sorted by (ruleId, fingerprint).
	Findings       []Finding
	Reconciliation []ReconciliationEntry
	// Diagnostics are the warnings of the evaluation: a traversal that stopped at its limit (CO-CON-030).
	Diagnostics []diag.Diagnostic
	// Skipped counts, over all fact kinds, the edges that no evaluation can read. Facts are never removed.
	Skipped SkipCounts
}

// candidate is a finding before aggregation. Edge is set for edge-based findings.
// EndNode, when set, is the observed target of a finding that spans more than one edge.
type candidate struct {
	edge           *BoundEdge
	endNode        *observation.ObservedNode
	location       *Location
	fingerprint    string
	kind           FindingKind
	sourceID       string
	targetID       string
	message        string
	evidencePath   []EvidenceStep
	extraLocations []Location
}

type planOutcome struct {
	verdict     RuleVerdict
	findings    []Finding
	reasons     []Reason
	diagnostics []diag.Diagnostic
	coverage    Coverage
}

var errNoAllow = errors.New("boundary predicate has no allow clause")

// paramRatio is the diagnostic parameter of a coverage ratio.
const (
	paramRatio    = "ratio"
	paramRequired = "required"
)

var errNoBudget = errors.New("budget predicate has no budget clause")

func observedRef(n *observation.ObservedNode) *ObservedRef {
	return &ObservedRef{QualifiedName: n.QualifiedName, FilePath: n.FilePath, Label: n.Label}
}

func edgeRef(bound *BoundEdge) *EdgeRef {
	e := bound.Edge

	return &EdgeRef{Kind: e.Kind, NativeKind: e.NativeKind, Confidence: e.Confidence, ResolutionMethod: e.ResolutionMethod}
}

func edgeLocation(bound *BoundEdge) Location {
	line := bound.From.StartLine
	p := bound.From.FilePath

	if bound.Edge.SourceSpan != nil {
		p = bound.Edge.SourceSpan.Path
		if bound.Edge.SourceSpan.Line != 0 {
			line = bound.Edge.SourceSpan.Line
		}
	}

	return Location{Path: fsutil.ToPosix(p), Line: line}
}

func stepEdge(bound *BoundEdge, ref *EdgeRef, loc *Location) *StepEdge {
	step := &StepEdge{
		Kind: ref.Kind, NativeKind: ref.NativeKind, Confidence: ref.Confidence, ResolutionMethod: ref.ResolutionMethod,
		From: bound.Edge.From, To: bound.Edge.To, Location: loc,
	}

	if id := observation.IdentityOf(bound.Edge); id.HasIdentityDetail() {
		step.Identity = &id
	}

	return step
}

func edgeSteps(bound *BoundEdge) []EvidenceStep {
	loc := edgeLocation(bound)
	from, to := bound.From, bound.To
	ref := edgeRef(bound)

	return []EvidenceStep{
		{Node: &from},
		{Edge: stepEdge(bound, ref, &loc)},
		{Node: &to},
	}
}

func compareLocations(a, b Location) int {
	return cmp.Or(compareStrings(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
}

// toFixed3 writes x with 3 decimals as JavaScript `toFixed(3)` does (exact value, ties round up).
func toFixed3(x float64) string {
	r := new(big.Rat)
	if r.SetFloat64(x) == nil {
		return strconv.FormatFloat(x, 'f', 3, 64)
	}

	const scale = 1000

	r.Mul(r, big.NewRat(scale, 1))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Div(r.Num(), r.Denom())
	s := n.String()

	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	for len(s) < 4 {
		s = "0" + s
	}

	out := s[:len(s)-3] + "." + s[len(s)-3:]
	if neg {
		out = "-" + out
	}

	return out
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// Evaluate evaluates all plans and reconciles the declared and observed architecture.
func Evaluate(input *EvaluateInput) EvaluateOutput {
	nodesByID := make(map[string]*intent.NodeDeclaration, len(input.Model.Nodes))
	for i := range input.Model.Nodes {
		nodesByID[input.Model.Nodes[i].ID] = &input.Model.Nodes[i]
	}

	plans := slices.Clone(input.Plans)
	slices.SortStableFunc(plans, func(a, b policy.RulePlan) int { return compareStrings(a.RuleID, b.RuleID) })

	results := make([]RuleResult, 0, len(plans))
	findings := []Finding{}

	var diagnostics []diag.Diagnostic

	typed := evaluateTyped(input, plans)
	diagnostics = append(diagnostics, typed.diagnostics...)

	for i := range plans {
		plan := &plans[i]

		if plan.Predicate.Family == policy.FamilyDecisionGovernance {
			// The register gives the results of a governance rule (see decisions.go); the predicate evaluators never see it.
			continue
		}

		if plan.Predicate.Family == policy.FamilyAssessmentCurrency {
			// The assessment evaluation gives the results of a review rule (see review.go); the predicate evaluators never see it.
			continue
		}

		if isExternal(plan) {
			// The assurance evaluators give the results of these rules (see external.go); the predicate evaluators
			// never see them.
			result, found := input.External.ruleResult(plan, input, nodesByID)
			results = append(results, result)
			findings = append(findings, found...)

			continue
		}

		if plan.Predicate.Family == policy.FamilyTyped {
			// The obligations of the rule give its result (see typed.go); the predicate evaluators never see it.
			result, found := typed.ruleResult(plan, input, nodesByID)
			results = append(results, result)
			findings = append(findings, found...)

			continue
		}

		base := RuleResult{
			RuleID:   plan.RuleID,
			Name:     plan.Name,
			Severity: plan.Severity,
			Policy:   plan.Pack,
			Family:   plan.Predicate.Family,
			Subjects: nonNilStrings(plan.Subjects),
			Targets:  nonNilStrings(plan.Targets),
			Plan:     *plan,
			// The rules of the as-built families gate as `mandatory` unless the workspace or the rule sets a mode.
			Enforcement: input.Enforcement.Resolve(plan, "").Mode,
		}

		if code, skip := notApplicable(plan); skip {
			base.Verdict = VerdictNotApplicable
			base.Reasons = []Reason{{Code: code, Message: diag.New(code, nil).Message}}
			results = append(results, base)

			continue
		}

		out, err := evaluatePlan(plan, input, nodesByID)
		if err != nil {
			base.Verdict = VerdictError
			base.Reasons = []Reason{{Code: diag.CON099, Message: diag.New(diag.CON099, diag.Detail(err.Error())).Message}}
		} else {
			for j := range out.findings {
				out.findings[j].Enforcement = input.Enforcement.Resolve(plan, out.findings[j].Environment).Mode
			}

			findings = append(findings, out.findings...)
			diagnostics = append(diagnostics, out.diagnostics...)
			base.Verdict = out.verdict
			base.Reasons = out.reasons
			base.Coverage = &out.coverage
			base.FindingCount = len(out.findings)
		}

		results = append(results, base)
	}

	slices.SortStableFunc(findings, func(a, b Finding) int {
		return cmp.Or(compareStrings(a.RuleID, b.RuleID), compareStrings(a.Fingerprint, b.Fingerprint))
	})

	reconciliation := Reconcile(&input.ReconcileInput)
	completeFindings(findings, results, input, reconciliation)

	return EvaluateOutput{
		RuleResults: results, Findings: findings, Reconciliation: reconciliation,
		Diagnostics: diagnostics, Skipped: RunSkipCounts(&input.ReconcileInput, input.Scope.UnsupportedFacts),
	}
}

// notApplicable gives the reason code when a plan selects nothing to check.
func notApplicable(plan *policy.RulePlan) (string, bool) {
	family := plan.Predicate.Family

	switch {
	case len(plan.Subjects) == 0:
		return diag.CON022, true
	case family == policy.FamilyLayer && !slices.ContainsFunc(plan.Layers, func(l []string) bool { return len(l) > 0 }):
		return diag.CON022, true
	case family == policy.FamilyIndependent && len(plan.Subjects) < minIndependentSubjects:
		return diag.CON022, true
	case len(plan.Targets) == 0 && selectsTargets(plan):
		return diag.CON025, true
	}

	return "", false
}

// minIndependentSubjects is the fewest subjects that an `independent` rule can check.
const minIndependentSubjects = 2

// selectsTargets says whether the family of the plan needs at least one target.
func selectsTargets(plan *policy.RulePlan) bool {
	switch plan.Predicate.Family {
	case policy.FamilyBoundary, policy.FamilyDenyPath, policy.FamilyRequireEdge, policy.FamilyPublicAPIOnly:
		return true
	case policy.FamilyBudget:
		return plan.Predicate.Budget != nil && plan.Predicate.Budget.Measure == policy.BudgetForbiddenEdges
	case policy.FamilyLayer, policy.FamilyDeclaredOnly, policy.FamilyObservedRequired, policy.FamilyAcyclic,
		policy.FamilyAllowOnly, policy.FamilyIndependent, policy.FamilyOwnership, policy.FamilyRequireEntity, policy.FamilyTyped, policy.FamilyView,
		policy.FamilyDecisionGovernance, policy.FamilyAssessmentCurrency,
		policy.FamilyEffectiveAccess, policy.FamilyCheckResult, policy.FamilyAssuranceClaim:
		return false
	}

	return false
}

func kindSet(kinds []observation.CanonicalEdgeKind) map[observation.CanonicalEdgeKind]struct{} {
	set := make(map[observation.CanonicalEdgeKind]struct{}, len(kinds))
	for _, k := range kinds {
		set[k] = struct{}{}
	}

	return set
}

func stringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, k := range items {
		set[k] = struct{}{}
	}

	return set
}

// planRun holds the state of one plan evaluation.
type planRun struct {
	plan      *policy.RulePlan
	input     *EvaluateInput
	nodesByID map[string]*intent.NodeDeclaration
	subjects  map[string]struct{}
	targets   map[string]struct{}
	coverage  *Coverage
	req       *policy.EvidenceRequirements
	// breach is set when a cycle or path traversal of the rule stopped at the traversal limit.
	breach     *LimitBreach
	bound      []*BoundEdge
	coverageOK bool
}

// rankFor gives the minimum confidence rank of edges of kind.
func (r *planRun) rankFor(kind observation.CanonicalEdgeKind) int {
	return r.req.MinimumConfidenceFor(kind).Rank()
}

// anyRankAboveZero says whether any traversed edge kind has a minimum confidence above `unknown`.
func (r *planRun) anyRankAboveZero() bool {
	if len(r.plan.Traversal.EdgeKinds) == 0 {
		return r.req.MinimumConfidence.Rank() > 0
	}

	return slices.ContainsFunc(r.plan.Traversal.EdgeKinds, func(k observation.CanonicalEdgeKind) bool { return r.rankFor(k) > 0 })
}

// edgeCoverage gives the evidence state of each edge kind that the plan traverses.
func edgeCoverage(plan *policy.RulePlan, scan *EdgeScan, rankFor func(observation.CanonicalEdgeKind) int) map[observation.CanonicalEdgeKind]EdgeCoverage {
	out := make(map[observation.CanonicalEdgeKind]EdgeCoverage, len(plan.Traversal.EdgeKinds))

	for _, kind := range plan.Traversal.EdgeKinds {
		entry := EdgeCoverage{
			Supported:         !slices.Contains(plan.UnsupportedEdgeKinds, kind),
			MinimumConfidence: plan.EvidenceRequirements.MinimumConfidenceFor(kind),
		}

		for _, bound := range scan.Bound {
			if bound.Edge.Kind != kind {
				continue
			}

			if bound.Edge.Confidence.Rank() >= rankFor(kind) {
				entry.Observed++
			} else {
				entry.BelowThreshold++
			}
		}

		out[kind] = entry
	}

	return out
}

// unresolvedReferences counts the code `ambiguous-usage` edges that start in a subject.
// Infrastructure ambiguity (`ambiguous`) is a code-free kind and never counts here.
func unresolvedReferences(input *EvaluateInput, subjects map[string]struct{}) int {
	count := 0

	for _, edge := range input.Evidence.Edges {
		if edge.Kind != observation.KindAmbiguousUsage {
			continue
		}

		if slices.ContainsFunc(input.BindingSet.ByObserved[edge.From], func(id string) bool { return hasKey(subjects, id) }) {
			count++
		}
	}

	return count
}

// coverageReasons gives the reasons that the file, byte and language coverage give.
func coverageReasons(req *policy.EvidenceRequirements, files *observation.FileCoverage, gaps []observation.LanguageGap, bytesOK bool) []Reason {
	reasons := []Reason{}

	if files.Ratio < req.MinimumCoverage {
		reasons = append(reasons, Reason{
			Code:    diag.CON021,
			Message: diag.New(diag.CON021, diag.Params{paramRatio: toFixed3(files.Ratio), paramRequired: req.MinimumCoverage}).Message,
		})
	}

	if !bytesOK {
		reasons = append(reasons, Reason{
			Code: diag.CON028,
			Message: diag.New(diag.CON028, diag.Params{
				paramRatio: toFixed3(files.Bytes.Ratio), paramRequired: *req.MinimumByteCoverage,
			}).Message,
		})
	}

	for _, gap := range gaps {
		reasons = append(reasons, Reason{
			Code: diag.CON026,
			Message: diag.New(diag.CON026, diag.Params{
				"language": gap.Language, paramRatio: toFixed3(gap.Ratio), paramRequired: req.LanguageThreshold(),
			}).Message,
		})
	}

	return reasons
}

func evaluatePlan(plan *policy.RulePlan, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) (*planOutcome, error) {
	family := plan.Predicate.Family

	var kinds map[observation.CanonicalEdgeKind]struct{}
	if family != policy.FamilyObservedRequired {
		kinds = kindSet(plan.Traversal.EdgeKinds)
	}

	scan := scanEdges(&input.ReconcileInput, kinds)
	scan.Skipped.Unsupported += input.Scope.UnsupportedFacts
	req := plan.EvidenceRequirements
	files := coverageForNodes(&input.ReconcileInput, append(slices.Clone(plan.Subjects), plan.Targets...))
	gaps := observation.LanguageGaps(&files, input.CoverageInputs.CbmFiles, req.LanguageThreshold())
	bytesOK := req.MinimumByteCoverage == nil || files.Bytes.Ratio >= *req.MinimumByteCoverage
	subjects := stringSet(plan.Subjects)
	scope := input.Scope.forPlan(plan)

	run := &planRun{
		plan: plan, input: input, nodesByID: nodesByID, bound: scan.Bound, req: &req,
		subjects: subjects, targets: stringSet(plan.Targets),
		// A missing necessary item is a violation only with complete qualified coverage (absence rule).
		coverageOK: files.Ratio >= req.MinimumCoverage && bytesOK && len(gaps) == 0 && scope.complete(),
	}

	coverage := Coverage{
		Files: files, Required: req.MinimumCoverage, UnboundEndpoints: scan.Skipped.Unbound,
		Skipped: scan.Skipped, SkipReasons: scan.Skipped.Reasons(), Predicates: scope.records,
		EdgeKinds:            edgeCoverage(plan, &scan, run.rankFor),
		UnresolvedReferences: unresolvedReferences(input, subjects),
	}
	run.coverage = &coverage

	candidates, err := run.candidates()
	if err != nil {
		return nil, err
	}

	// Edges below the confidence threshold never make a finding.
	if family != policy.FamilyAcyclic {
		candidates = run.dropSubThreshold(candidates)
	}

	found := aggregate(plan, candidates, nodesByID)
	reasons := coverageReasons(&req, &files, gaps, bytesOK)
	reasons = append(reasons, scope.reasonsFor(family)...)

	var diagnostics []diag.Diagnostic

	// A traversal that stopped at its limit is incomplete: it never gives `pass`, and no finding is dropped to stay under the limit.
	if run.breach != nil {
		coverage.TraversalIncomplete = true

		reasons = append(reasons, run.breach.Reason())
		diagnostics = append(diagnostics, run.breach.Diagnostic())
	}

	for _, kind := range plan.UnsupportedEdgeKinds {
		reasons = append(reasons, Reason{
			Code:    diag.POL009,
			Message: diag.New(diag.POL009, diag.Params{"kind": string(kind), "ruleId": plan.RuleID}).Message,
		})
	}

	if coverage.SubThresholdEvidence > 0 {
		reasons = append(reasons, Reason{
			Code:    diag.CON023,
			Message: diag.New(diag.CON023, diag.Params{"count": coverage.SubThresholdEvidence}).Message,
		})
	}

	if files.EmptyScope {
		reasons = append(reasons, Reason{Code: diag.CON024, Message: diag.New(diag.CON024, nil).Message})
	}

	// A binding gap keeps the fact and is an info finding, never a violation: the rule cannot decide, so it is not `pass`.
	unbound := bindingGaps(plan, &input.ReconcileInput, kinds)
	reasons = append(reasons, gapReasons(unbound)...)

	verdict := VerdictPass

	switch {
	case len(found) > 0:
		verdict = VerdictViolation
	case len(reasons) > 0:
		verdict = VerdictUnknown
	}

	return &planOutcome{findings: slices.Concat(found, unbound), coverage: coverage, reasons: reasons, verdict: verdict, diagnostics: diagnostics}, nil
}

// dropSubThreshold removes the candidates whose edge is below the minimum confidence of its kind
// and counts them as sub-threshold evidence.
func (r *planRun) dropSubThreshold(candidates []candidate) []candidate {
	kept := candidates[:0:0]

	for i := range candidates {
		c := &candidates[i]
		if c.edge != nil && c.edge.Edge.Confidence.Rank() < r.rankFor(c.edge.Edge.Kind) {
			r.coverage.SubThresholdEvidence++

			continue
		}

		kept = append(kept, *c)
	}

	return kept
}
