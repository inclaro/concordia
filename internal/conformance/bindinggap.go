package conformance

import (
	"slices"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// NextActionResolveBinding is the next action of a binding gap finding.
const NextActionResolveBinding = "resolve binding"

// deployedLabels are the native labels of the entities that only a deployment
// binding can map to a CALM element. An unbound code file is normal and only
// counted; an unbound deployed resource that a rule needs is a gap.
var deployedLabels = map[string]struct{}{
	"Resource": {}, observation.LabelImage: {}, "Route": {}, "Chart": {},
}

// bindingGaps gives one binding gap finding for each deployed entity that
// the rule needs a binding for: an endpoint with no CALM ID, of an edge of the
// rule's kinds, whose other endpoint is bound to a subject or a target of the
// rule (every edge when the rule has neither). The fact is kept: the edge stays
// in the evidence set and is the evidence path of the finding. The finding has
// severity `info` and the next action "resolve binding"; it never makes a violation.
// A rule that takes all code kinds (kinds == nil) has no gaps.
func bindingGaps(plan *policy.RulePlan, input *ReconcileInput, kinds map[observation.CanonicalEdgeKind]struct{}) []Finding {
	if kinds == nil {
		return nil
	}

	scope := stringSet(append(slices.Clone(plan.Subjects), plan.Targets...))
	excluded := excludedFile(input)
	gaps := gapIndex{first: map[string]*BoundEdge{}}

	for _, edge := range input.Evidence.Edges {
		if _, ok := kinds[edge.Kind]; !ok {
			continue
		}

		if bound := gapEdge(input, excluded, edge); bound != nil {
			gaps.offer(input, scope, bound)
		}
	}

	slices.SortFunc(gaps.order, compareStrings)

	out := make([]Finding, 0, len(gaps.order))
	for _, id := range gaps.order {
		out = append(out, gapFinding(plan, id, gaps.first[id]))
	}

	return out
}

// gapIndex keeps, for each endpoint that needs a binding, the first edge that reaches it.
type gapIndex struct {
	first map[string]*BoundEdge
	order []string
}

// gapEdge binds an edge to its endpoint nodes; nil when an endpoint is missing or in an excluded file.
func gapEdge(input *ReconcileInput, excluded func(string) bool, edge *observation.EvidenceEdge) *BoundEdge {
	from, okFrom := input.Evidence.Nodes[edge.From]
	dest, okTo := input.Evidence.Nodes[edge.To]

	if !okFrom || !okTo || (from.FilePath != "" && excluded(from.FilePath)) || (dest.FilePath != "" && excluded(dest.FilePath)) {
		return nil
	}

	return &BoundEdge{
		Edge: edge, From: from, To: dest,
		FromIDs: input.BindingSet.ByObserved[edge.From], ToIDs: input.BindingSet.ByObserved[edge.To],
	}
}

// offer records the edge for each endpoint that needs a binding and whose other endpoint is in scope.
func (g *gapIndex) offer(input *ReconcileInput, scope map[string]struct{}, bound *BoundEdge) {
	for _, endpoint := range []struct {
		id    string
		node  *observation.ObservedNode
		other []string
	}{{bound.Edge.From, &bound.From, bound.ToIDs}, {bound.Edge.To, &bound.To, bound.FromIDs}} {
		if !needsBinding(input, endpoint.id, endpoint.node) || !inScope(scope, endpoint.other) {
			continue
		}

		known, seen := g.first[endpoint.id]
		if !seen {
			g.order = append(g.order, endpoint.id)
		}

		if !seen || compareEdges(bound, known) < 0 {
			g.first[endpoint.id] = bound
		}
	}
}

// needsBinding says whether an endpoint is a deployed entity with no CALM ID.
func needsBinding(input *ReconcileInput, id string, node *observation.ObservedNode) bool {
	if _, deployed := deployedLabels[node.Label]; !deployed || len(input.BindingSet.ByObserved[id]) > 0 {
		return false
	}

	return true
}

// inScope says whether the other endpoint is bound into the rule scope; an empty scope accepts any.
func inScope(scope map[string]struct{}, ids []string) bool {
	if len(scope) == 0 {
		return true
	}

	return slices.ContainsFunc(ids, func(id string) bool { _, ok := scope[id]; return ok })
}

func gapFinding(plan *policy.RulePlan, endpointID string, bound *BoundEdge) Finding {
	node := &bound.To
	if bound.Edge.From == endpointID {
		node = &bound.From
	}

	loc := edgeLocation(bound)
	if node.FilePath != "" {
		loc = Location{Path: fsutil.ToPosix(node.FilePath), Line: node.StartLine}
	}

	source, target := bound.FromIDs, bound.ToIDs

	finding := Finding{
		RuleID:   plan.RuleID,
		Severity: policy.SeverityInfo,
		Kind:     KindBindingGap,
		// The gap blocks a decision; it is never a violation. The rule verdict gates.
		Result: VerdictUnknown,
		Reason: ReasonBindingGap,
		Message: "no binding matches " + node.Label + " " + node.QualifiedName +
			": rule " + plan.RuleID + " needs a binding for it to decide on the " + string(bound.Edge.Kind) + " fact",
		NextAction:       NextActionResolveBinding,
		Source:           Endpoint{ArchitectureID: firstOrEmpty(source), Observed: observedRef(&bound.From)},
		Target:           Endpoint{ArchitectureID: firstOrEmpty(target), Observed: observedRef(&bound.To)},
		Edge:             edgeRef(bound),
		Location:         &loc,
		EvidencePath:     edgeSteps(bound),
		RelatedLocations: []Location{},
		Owner:            nil,
		Disposition:      exceptions.DispositionActive,
	}

	finding.Fingerprint = TypedFingerprint(&TypedParts{
		Parts: Parts{
			RuleID: plan.RuleID, SourceArchitectureID: finding.Source.ArchitectureID, TargetArchitectureID: finding.Target.ArchitectureID,
			CanonicalEdgeKind: "binding-gap", SourceQualifiedName: bound.From.QualifiedName, TargetQualifiedName: bound.To.QualifiedName,
			NormalizedRepositoryPath: loc.Path,
		},
		Environment: bound.Edge.Environment,
		Occurrence:  endpointID,
	})

	return finding
}

func firstOrEmpty(ids []string) string {
	if len(ids) == 0 {
		return ""
	}

	return ids[0]
}

// gapReasons gives the CO-BND-005 reason of a rule for each path with a gap.
func gapReasons(gaps []Finding) []Reason {
	seen := map[string]struct{}{}
	reasons := []Reason{}

	for i := range gaps {
		path := ""
		if gaps[i].Location != nil {
			path = gaps[i].Location.Path
		}

		if _, dup := seen[path]; dup {
			continue
		}

		seen[path] = struct{}{}

		reasons = append(reasons, Reason{Code: diag.BND005, Message: diag.New(diag.BND005, diag.Params{paramPath: path}).Message})
	}

	return reasons
}
