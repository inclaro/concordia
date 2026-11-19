package conformance

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// familyEvaluator evaluates one predicate family of a rule plan.
type familyEvaluator func(r *planRun) ([]candidate, error)

// plainFamily wraps an evaluator that cannot fail.
func plainFamily(eval func(r *planRun) []candidate) familyEvaluator {
	return func(r *planRun) ([]candidate, error) { return eval(r), nil }
}

// externalFamily is the evaluator of a family whose results do not come from the predicate evaluators. Evaluate
// skips these rules before candidates runs, so it never gets one:
//   - a view-currency rule takes its results from the declared views (see viewhook.go);
//   - a governance rule takes them from the decision register (see decisions.go);
//   - a review rule takes them from the assessment evaluation (see review.go);
//   - the three assurance families are evaluated outside the predicate evaluators (see external.go);
//   - a typed rule is evaluated by its obligations (BuildObligations and EvaluateObligation).
func externalFamily(*planRun) ([]candidate, error) { return nil, nil }

// familyEvaluators maps each predicate family to its evaluator.
var familyEvaluators = map[policy.PredicateFamily]familyEvaluator{
	policy.FamilyBoundary:           (*planRun).boundary,
	policy.FamilyLayer:              plainFamily((*planRun).layer),
	policy.FamilyDeclaredOnly:       plainFamily((*planRun).declaredOnly),
	policy.FamilyObservedRequired:   plainFamily((*planRun).observedRequired),
	policy.FamilyAcyclic:            plainFamily((*planRun).cycleCandidates),
	policy.FamilyDenyPath:           plainFamily((*planRun).denyPath),
	policy.FamilyAllowOnly:          plainFamily((*planRun).allowOnly),
	policy.FamilyIndependent:        plainFamily((*planRun).independent),
	policy.FamilyPublicAPIOnly:      plainFamily((*planRun).publicAPIOnly),
	policy.FamilyOwnership:          plainFamily((*planRun).ownership),
	policy.FamilyBudget:             (*planRun).budget,
	policy.FamilyRequireEdge:        plainFamily((*planRun).requireEdge),
	policy.FamilyRequireEntity:      (*planRun).requireEntity,
	policy.FamilyView:               externalFamily,
	policy.FamilyDecisionGovernance: externalFamily,
	policy.FamilyAssessmentCurrency: externalFamily,
	policy.FamilyEffectiveAccess:    externalFamily,
	policy.FamilyCheckResult:        externalFamily,
	policy.FamilyAssuranceClaim:     externalFamily,
	policy.FamilyTyped:              externalFamily,
}

func (r *planRun) candidates() ([]candidate, error) {
	family := r.plan.Predicate.Family

	eval, ok := familyEvaluators[family]
	if !ok {
		return nil, fmt.Errorf("unknown predicate family %q", family) //nolint:err113 // reported as the rule error verdict.
	}

	return eval(r)
}

// boundaryAllow is the compiled `allow` clause of a boundary rule.
type boundaryAllow struct {
	root  func(string) bool
	nodes map[string]struct{}
	kinds map[observation.CanonicalEdgeKind]struct{}
	tags  map[string]struct{}
}

func newBoundaryAllow(allow *policy.BoundaryAllow) *boundaryAllow {
	root := func(string) bool { return false }
	if len(allow.TargetSourceRoots) > 0 {
		root = fsutil.GlobMatcher(allow.TargetSourceRoots)
	}

	return &boundaryAllow{
		root: root, nodes: stringSet(allow.TargetCalmNodes), kinds: kindSet(allow.EdgeKinds), tags: stringSet(allow.TargetTags),
	}
}

// allowsEdge says whether the clause allows the edge itself (kind or target path).
func (a *boundaryAllow) allowsEdge(bound *BoundEdge) bool {
	_, ok := a.kinds[bound.Edge.Kind]

	return ok || a.root(fsutil.ToPosix(bound.To.FilePath))
}

// allowsTarget says whether the clause allows the target CALM node (ID or tag).
func (a *boundaryAllow) allowsTarget(id string, node *intent.NodeDeclaration) bool {
	if _, ok := a.nodes[id]; ok {
		return true
	}

	return node != nil && slices.ContainsFunc(node.Tags, func(tag string) bool {
		_, ok := a.tags[tag]

		return ok
	})
}

func (r *planRun) boundary() ([]candidate, error) {
	if r.plan.Predicate.Allow == nil {
		return nil, errNoAllow
	}

	return r.forbiddenCandidates(newBoundaryAllow(r.plan.Predicate.Allow)), nil
}

// forbiddenCandidates gives the edges from a subject to a target that the allow clause does not allow.
func (r *planRun) forbiddenCandidates(allow *boundaryAllow) []candidate {
	var out []candidate

	for _, bound := range r.bound {
		if allow.allowsEdge(bound) {
			continue
		}

		out = append(out, r.forbiddenEdgeCandidates(allow, bound)...)
	}

	return out
}

// forbiddenEdgeCandidates gives one candidate for each pair of the edge from a subject to a target
// that the allow clause does not allow.
func (r *planRun) forbiddenEdgeCandidates(allow *boundaryAllow, bound *BoundEdge) []candidate {
	var out []candidate

	for _, pair := range edgePairs(bound) {
		src, dst := pair[0], pair[1]
		if !r.isForbiddenPair(allow, src, dst) {
			continue
		}

		out = append(out, r.edgeCandidate(KindForbiddenDependency, src, dst, bound, fmt.Sprintf("forbidden dependency %s -> %s", src, dst)))
	}

	return out
}

// isForbiddenPair says whether src is a subject, dst is a target, and the allow clause allows dst nowhere.
func (r *planRun) isForbiddenPair(allow *boundaryAllow, src, dst string) bool {
	if !hasKey(r.subjects, src) || !hasKey(r.targets, dst) {
		return false
	}

	return !allow.allowsTarget(dst, r.nodesByID[dst])
}

func hasKey(set map[string]struct{}, key string) bool {
	_, ok := set[key]

	return ok
}

// layerIndex gives the first layer of each node ID.
func layerIndex(layers [][]string) map[string]int {
	layerOf := map[string]int{}

	for i, ids := range layers {
		for _, id := range ids {
			if _, ok := layerOf[id]; !ok {
				layerOf[id] = i
			}
		}
	}

	return layerOf
}

// edgePairCandidates makes the candidate that pick gives for each bound edge pair of CALM IDs.
func (r *planRun) edgePairCandidates(pick func(src, dst string, bound *BoundEdge) (candidate, bool)) []candidate {
	var out []candidate

	for _, bound := range r.bound {
		for _, pair := range edgePairs(bound) {
			if c, ok := pick(pair[0], pair[1], bound); ok {
				out = append(out, c)
			}
		}
	}

	return out
}

func (r *planRun) layer() []candidate {
	layerOf := layerIndex(r.plan.Layers)

	return r.edgePairCandidates(func(src, dst string, bound *BoundEdge) (candidate, bool) {
		i, okSrc := layerOf[src]
		j, okDst := layerOf[dst]

		if !okSrc || !okDst || j >= i {
			return candidate{}, false
		}

		return r.edgeCandidate(KindLayerViolation, src, dst, bound,
			fmt.Sprintf("layer violation: %s (layer %d) depends on %s (layer %d)", src, i, dst, j)), true
	})
}

func (r *planRun) declaredOnly() []candidate {
	permitting := permittingRelationships(r.input.Model)

	return r.edgePairCandidates(func(src, dst string, bound *BoundEdge) (candidate, bool) {
		if !hasKey(r.subjects, src) || !hasKey(r.subjects, dst) {
			return candidate{}, false
		}

		return r.declaredOnlyCandidate(permitting[pairKey(src, dst)], src, dst, bound)
	})
}

// declaredOnlyCandidate judges one edge pair against the relationships that permit it. An edge of an
// allowed kind with too little confidence is sub-threshold evidence, not a finding.
func (r *planRun) declaredOnlyCandidate(rels []*intent.RelationshipDeclaration, src, dst string, bound *BoundEdge) (candidate, bool) {
	if len(rels) == 0 {
		return r.edgeCandidate(KindUndeclared, src, dst, bound, fmt.Sprintf("undeclared dependency %s -> %s", src, dst)), true
	}

	if slices.ContainsFunc(rels, func(x *intent.RelationshipDeclaration) bool {
		return kindAllowedBy(x, bound.Edge.Kind) && confidenceOkFor(x, bound.Edge)
	}) {
		return candidate{}, false
	}

	if slices.ContainsFunc(rels, func(x *intent.RelationshipDeclaration) bool { return kindAllowedBy(x, bound.Edge.Kind) }) {
		r.coverage.SubThresholdEvidence++

		return candidate{}, false
	}

	return r.edgeCandidate(KindContradiction, src, dst, bound,
		fmt.Sprintf("edge kind %s not allowed by declared relationship %s", bound.Edge.Kind, rels[0].ID)), true
}

// observedQualifies says whether an observed edge satisfies a required relationship toward target.
func observedQualifies(everything []*BoundEdge, rel *intent.RelationshipDeclaration, target string, allowed map[observation.CanonicalEdgeKind]struct{}, minimum int) bool {
	return slices.ContainsFunc(everything, func(bound *BoundEdge) bool {
		if !hasKind(allowed, bound.Edge.Kind) || bound.Edge.Confidence.Rank() < minimum {
			return false
		}

		return slices.ContainsFunc(edgePairs(bound), func(p [2]string) bool { return p[0] == rel.Source && p[1] == target })
	})
}

func hasKind(set map[observation.CanonicalEdgeKind]struct{}, kind observation.CanonicalEdgeKind) bool {
	_, ok := set[kind]

	return ok
}

func (r *planRun) observedRequired() []candidate {
	everything := scanEdges(&r.input.ReconcileInput, nil).Bound
	defaultKinds := r.defaultObservedKinds()

	var out []candidate

	for i := range r.input.Model.Relationships {
		rel := &r.input.Model.Relationships[i]
		if !r.requiresObservation(rel) {
			continue
		}

		out = append(out, r.unobservedTargets(everything, rel, defaultKinds)...)
	}

	return out
}

// defaultObservedKinds gives the supported non-structural code edge kinds, which a required
// relationship accepts when it lists none.
func (r *planRun) defaultObservedKinds() []observation.CanonicalEdgeKind {
	var kinds []observation.CanonicalEdgeKind

	for _, k := range r.input.SupportedCanonicalKinds {
		if !IsStructuralKind(k) && observation.IsCodeKind(k) {
			kinds = append(kinds, k)
		}
	}

	return kinds
}

// requiresObservation says whether rel is a connection from a subject that must be observed.
func (r *planRun) requiresObservation(rel *intent.RelationshipDeclaration) bool {
	if rel.Kind != intent.RelConnects && rel.Kind != intent.RelInteracts {
		return false
	}

	if rel.Observation == nil || !rel.Observation.Required {
		return false
	}

	return rel.Source != "" && hasKey(r.subjects, rel.Source)
}

// unobservedTargets gives a candidate for each subject target of rel that no observed edge reaches.
func (r *planRun) unobservedTargets(everything []*BoundEdge, rel *intent.RelationshipDeclaration, defaultKinds []observation.CanonicalEdgeKind) []candidate {
	if !r.coverageOK {
		return nil
	}

	allowed, minConfidence := r.requiredEvidence(rel, defaultKinds)

	var out []candidate

	for _, target := range rel.Targets {
		if !hasKey(r.subjects, target) || observedQualifies(everything, rel, target, allowed, minConfidence.Rank()) {
			continue
		}

		out = append(out, r.unobservedCandidate(rel, target))
	}

	return out
}

// requiredEvidence gives the edge kinds and the minimum confidence that a required relationship accepts.
func (r *planRun) requiredEvidence(rel *intent.RelationshipDeclaration, defaultKinds []observation.CanonicalEdgeKind) (map[observation.CanonicalEdgeKind]struct{}, observation.Confidence) {
	list := rel.Observation.AllowedEdgeKinds
	if list == nil {
		list = defaultKinds
	}

	minConfidence := rel.Observation.MinimumConfidence
	if minConfidence == "" {
		minConfidence = r.plan.EvidenceRequirements.MinimumConfidence
	}

	return kindSet(list), minConfidence
}

func (r *planRun) unobservedCandidate(rel *intent.RelationshipDeclaration, target string) candidate {
	model := r.input.Model

	return candidate{
		fingerprint: UnobservedFingerprint(r.plan.RuleID, rel.Source, []string{target}, rel.ID),
		kind:        KindUnobserved,
		sourceID:    rel.Source,
		targetID:    target,
		message: fmt.Sprintf("declared relationship %s (%s -> %s) is not observed (%s#%s)",
			rel.ID, rel.Source, target, model.SourceURI, rel.Pointer),
		evidencePath: []EvidenceStep{},
		location:     &Location{Path: model.SourceURI},
	}
}

// fingerprintFor gives the fingerprint of the parts after the aggregation of the rule has
// cleared the parts that it groups over.
func (r *planRun) fingerprintFor(parts Parts) string { //nolint:gocritic // Parts is a small value type.
	switch r.plan.Aggregation {
	case policy.AggregationFile:
		parts.CanonicalEdgeKind, parts.SourceQualifiedName, parts.TargetQualifiedName = "", "", ""
	case policy.AggregationSymbol:
		parts.CanonicalEdgeKind, parts.TargetQualifiedName, parts.NormalizedRepositoryPath = "", "", ""
	case policy.AggregationArchitecturePair:
		parts.CanonicalEdgeKind, parts.SourceQualifiedName, parts.TargetQualifiedName, parts.NormalizedRepositoryPath = "", "", "", ""
	}

	return Fingerprint(parts)
}

func (r *planRun) edgeCandidate(kind FindingKind, src, dst string, bound *BoundEdge, message string) candidate {
	loc := edgeLocation(bound)

	return candidate{
		fingerprint: r.fingerprintFor(Parts{
			RuleID:                   r.plan.RuleID,
			SourceArchitectureID:     src,
			TargetArchitectureID:     dst,
			CanonicalEdgeKind:        string(bound.Edge.Kind),
			SourceQualifiedName:      bound.From.QualifiedName,
			TargetQualifiedName:      bound.To.QualifiedName,
			NormalizedRepositoryPath: bound.From.FilePath,
		}),
		kind:         kind,
		sourceID:     src,
		targetID:     dst,
		message:      message,
		edge:         bound,
		evidencePath: edgeSteps(bound),
		location:     &loc,
	}
}

// aggregate groups the candidates by fingerprint. The first candidate (edges arrive
// sorted) is the main evidence.
func aggregate(plan *policy.RulePlan, candidates []candidate, nodesByID map[string]*intent.NodeDeclaration) []Finding {
	groups := map[string][]*candidate{}

	var order []string

	for i := range candidates {
		c := &candidates[i]
		if _, ok := groups[c.fingerprint]; !ok {
			order = append(order, c.fingerprint)
		}

		groups[c.fingerprint] = append(groups[c.fingerprint], c)
	}

	out := make([]Finding, 0, len(order))
	for _, key := range order {
		out = append(out, buildFinding(plan, key, groups[key], nodesByID))
	}

	slices.SortStableFunc(out, func(a, b Finding) int { return compareStrings(a.Fingerprint, b.Fingerprint) })

	return out
}

// relatedLocations gives the sorted locations of the group without the main location.
func relatedLocations(group []*candidate) []Location {
	main := group[0]
	related := map[string]Location{}

	add := func(l Location) {
		if main.location == nil || compareLocations(*main.location, l) != 0 {
			related[l.Path+":"+strconv.Itoa(l.Line)] = l
		}
	}

	for _, c := range group[1:] {
		if c.location != nil {
			add(*c.location)
		}
	}

	for _, l := range main.extraLocations {
		add(l)
	}

	list := make([]Location, 0, len(related))
	for _, l := range related {
		list = append(list, l)
	}

	slices.SortFunc(list, compareLocations)

	return list
}

// ownerOf gives the owner of the source node, else the owner of the rule, else nil.
func ownerOf(plan *policy.RulePlan, sourceID string, nodesByID map[string]*intent.NodeDeclaration) *string {
	if node := nodesByID[sourceID]; node != nil && node.Owner != "" {
		owner := node.Owner

		return &owner
	}

	if plan.Owner != "" {
		owner := plan.Owner

		return &owner
	}

	return nil
}

func buildFinding(plan *policy.RulePlan, fingerprint string, group []*candidate, nodesByID map[string]*intent.NodeDeclaration) Finding {
	main := group[0]
	finding := Finding{
		Fingerprint:      fingerprint,
		RuleID:           plan.RuleID,
		Severity:         plan.Severity,
		Kind:             main.kind,
		Message:          main.message,
		Source:           Endpoint{ArchitectureID: main.sourceID},
		Target:           Endpoint{ArchitectureID: main.targetID},
		EvidencePath:     main.evidencePath,
		Location:         main.location,
		RelatedLocations: relatedLocations(group),
		Owner:            ownerOf(plan, main.sourceID, nodesByID),
		Disposition:      "active",
	}

	if main.edge != nil {
		finding.Source.Observed = observedRef(&main.edge.From)
		finding.Target.Observed = observedRef(&main.edge.To)
		finding.Edge = edgeRef(main.edge)
	}

	if main.endNode != nil {
		finding.Target.Observed = observedRef(main.endNode)
	}

	return finding
}
