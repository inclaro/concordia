package conformance

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

// composedRelated gives a function that says whether one CALM ID is an ancestor of the
// other in the `composed-of` relationships. The edge families skip such pairs: a
// container may use its own parts.
func composedRelated(model *intent.ArchitectureModel) func(a, b string) bool {
	parent := composedParents(model)

	return func(a, b string) bool { return isComposedAncestor(parent, a, b) || isComposedAncestor(parent, b, a) }
}

// composedParents maps each CALM ID to the container that is composed of it.
func composedParents(model *intent.ArchitectureModel) map[string]string {
	parent := map[string]string{}

	for i := range model.Relationships {
		rel := &model.Relationships[i]
		if rel.Kind != intent.RelComposedOf || rel.Container == "" {
			continue
		}

		for _, child := range rel.Targets {
			parent[child] = rel.Container
		}
	}

	return parent
}

// isComposedAncestor says whether candidate is on the chain of containers above node. A loop in the
// chain ends the walk.
func isComposedAncestor(parent map[string]string, node, candidate string) bool {
	seen := map[string]struct{}{}

	for cur, ok := parent[node]; ok; cur, ok = parent[cur] {
		if cur == candidate {
			return true
		}

		if _, loop := seen[cur]; loop {
			return false
		}

		seen[cur] = struct{}{}
	}

	return false
}

// pairCandidates makes one candidate for each bound edge pair of CALM IDs that pick accepts.
// Pairs of CALM nodes that contain each other are skipped.
func (r *planRun) pairCandidates(kind FindingKind, pick func(src, dst string, bound *BoundEdge) (string, bool)) []candidate {
	related := composedRelated(r.input.Model)

	var out []candidate

	for _, bound := range r.bound {
		for _, pair := range edgePairs(bound) {
			src, dst := pair[0], pair[1]
			if related(src, dst) {
				continue
			}

			if message, ok := pick(src, dst, bound); ok {
				out = append(out, r.edgeCandidate(kind, src, dst, bound, message))
			}
		}
	}

	return out
}

func (r *planRun) allowOnly() []candidate {
	return r.pairCandidates(KindDisallowedDep, func(src, dst string, _ *BoundEdge) (string, bool) {
		if !hasKey(r.subjects, src) || hasKey(r.targets, dst) {
			return "", false
		}

		return fmt.Sprintf("dependency %s -> %s is outside the allowed targets", src, dst), true
	})
}

func (r *planRun) independent() []candidate {
	return r.pairCandidates(KindPeerDependency, func(src, dst string, _ *BoundEdge) (string, bool) {
		if !hasKey(r.subjects, src) || !hasKey(r.subjects, dst) {
			return "", false
		}

		return fmt.Sprintf("peer dependency %s -> %s", src, dst), true
	})
}

func (r *planRun) publicAPIOnly() []candidate {
	isAPI := fsutil.GlobMatcher(r.plan.Predicate.APISourceRoots)

	return r.pairCandidates(KindNonPublicAPI, func(src, dst string, bound *BoundEdge) (string, bool) {
		if !hasKey(r.subjects, src) || !hasKey(r.targets, dst) {
			return "", false
		}

		file := fsutil.ToPosix(bound.To.FilePath)
		if isAPI(file) {
			return "", false
		}

		return fmt.Sprintf("%s uses %s outside its public API: %s", src, dst, file), true
	})
}

func (r *planRun) ownership() []candidate {
	owners := strings.Join(r.plan.Targets, ", ")

	return r.pairCandidates(KindOwnershipViolation, func(src, dst string, _ *BoundEdge) (string, bool) {
		if !hasKey(r.subjects, dst) || r.isOwnerOrSubject(src) {
			return "", false
		}

		return fmt.Sprintf("%s accesses %s, which only %s may access", src, dst, owners), true
	})
}

// isOwnerOrSubject says whether the CALM ID is one of the targets (the owners) or one of the subjects.
func (r *planRun) isOwnerOrSubject(id string) bool {
	return hasKey(r.targets, id) || hasKey(r.subjects, id)
}

// budget counts the findings of a measure and gives one finding when the count is above the maximum.
func (r *planRun) budget() ([]candidate, error) {
	budget := r.plan.Predicate.Budget
	if budget == nil {
		return nil, errNoBudget
	}

	var inner []candidate

	switch budget.Measure {
	case policy.BudgetForbiddenEdges:
		inner = r.forbiddenCandidates(newBoundaryAllow(&policy.BoundaryAllow{}))
	case policy.BudgetCycles:
		inner = r.cycleCandidates()
	default:
		return nil, fmt.Errorf("unknown budget measure %q", budget.Measure) //nolint:err113 // reported as the rule error verdict.
	}

	inner = r.dropSubThreshold(inner)
	found := aggregate(r.plan, inner, r.nodesByID)

	if len(found) <= budget.Max {
		return nil, nil
	}

	first := &found[0]

	extra := make([]Location, 0, len(found)-1)

	for i := 1; i < len(found); i++ {
		if loc := found[i].Location; loc != nil {
			extra = append(extra, *loc)
		}
	}

	return []candidate{{
		fingerprint: Fingerprint(Parts{
			RuleID:               r.plan.RuleID,
			SourceArchitectureID: strings.Join(r.plan.Subjects, ","),
			TargetArchitectureID: strings.Join(r.plan.Targets, ","),
			CanonicalEdgeKind:    "budget:" + budget.Measure,
		}),
		kind:           KindBudgetExceeded,
		sourceID:       first.Source.ArchitectureID,
		targetID:       first.Target.ArchitectureID,
		message:        fmt.Sprintf("%s count %d exceeds the budget %d", budget.Measure, len(found), budget.Max),
		evidencePath:   first.EvidencePath,
		location:       first.Location,
		extraLocations: extra,
	}}, nil
}

// missingCandidate completes a finding without an edge: something that the rule requires is absent.
func (r *planRun) missingCandidate(missing *candidate) candidate {
	missing.evidencePath = []EvidenceStep{}
	missing.location = &Location{Path: r.input.Model.SourceURI}

	return *missing
}

// requireEdge gives a finding for each subject without an edge of a plan kind to one of the targets.
func (r *planRun) requireEdge() []candidate {
	if !r.coverageOK {
		return nil
	}

	kinds := make([]string, len(r.plan.Traversal.EdgeKinds))
	for i, k := range r.plan.Traversal.EdgeKinds {
		kinds[i] = string(k)
	}

	var out []candidate

	for _, subject := range r.plan.Subjects {
		if r.hasEdgeTo(subject) {
			continue
		}

		out = append(out, r.missingCandidate(&candidate{
			fingerprint: UnobservedFingerprint(r.plan.RuleID, subject, r.plan.Targets, "require-edge"),
			kind:        KindMissingEdge,
			sourceID:    subject,
			targetID:    r.plan.Targets[0],
			message:     fmt.Sprintf("no %s edge from %s to any of %s", strings.Join(kinds, "/"), subject, strings.Join(r.plan.Targets, ", ")),
		}))
	}

	return out
}

// hasEdgeTo says whether a bound edge at or above the minimum confidence leads from subject to a target.
func (r *planRun) hasEdgeTo(subject string) bool {
	return slices.ContainsFunc(r.bound, func(bound *BoundEdge) bool {
		if bound.Edge.Confidence.Rank() < r.rankFor(bound.Edge.Kind) {
			return false
		}

		return slices.ContainsFunc(edgePairs(bound), func(p [2]string) bool { return p[0] == subject && hasKey(r.targets, p[1]) })
	})
}

// requireEntity gives a finding for each subject without an observed node of a label (and name).
func (r *planRun) requireEntity() ([]candidate, error) {
	pred := &r.plan.Predicate

	nameMatches, err := nameMatcher(pred.NamePattern)
	if err != nil {
		return nil, err
	}

	if !r.coverageOK {
		return nil, nil
	}

	var out []candidate

	for _, subject := range r.plan.Subjects {
		if r.hasEntityFor(subject, nameMatches) {
			continue
		}

		out = append(out, r.missingCandidate(&candidate{
			fingerprint: UnobservedFingerprint(r.plan.RuleID, subject, []string{}, "require-entity"),
			kind:        KindMissingEntity,
			sourceID:    subject,
			targetID:    subject,
			message:     fmt.Sprintf("no observed %s entity bound to %s", strings.Join(pred.EntityLabels, "/"), subject),
		}))
	}

	return out, nil
}

// nameMatcher compiles the optional name pattern of an entity rule; an empty pattern matches every name.
func nameMatcher(source string) (func(string) bool, error) {
	if source == "" {
		return func(string) bool { return true }, nil
	}

	pattern, err := regexp.Compile(source)
	if err != nil {
		return nil, fmt.Errorf("namePattern: %w", err)
	}

	return pattern.MatchString, nil
}

// hasEntityFor says whether an observed node of one of the entity labels (and a name that the matcher accepts) is bound to the subject.
func (r *planRun) hasEntityFor(subject string, nameMatches func(string) bool) bool {
	labels := r.plan.Predicate.EntityLabels
	nodes := r.input.Evidence.Nodes

	for id := range nodes {
		if matchesEntity(nodes[id].Label, nodes[id].QualifiedName, labels, nameMatches) && slices.Contains(r.input.BindingSet.ByObserved[id], subject) {
			return true
		}
	}

	return false
}

// matchesEntity says whether a node has one of the labels and a name that the matcher accepts.
func matchesEntity(label, qualifiedName string, labels []string, nameMatches func(string) bool) bool {
	return slices.Contains(labels, label) && nameMatches(qualifiedName)
}
