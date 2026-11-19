package conformance

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// hop is one edge of an adjacency list of the observed graph.
type hop struct {
	edge *observation.EvidenceEdge
	to   string
}

// pathMatch is a path from an observed node of a subject to an observed node of a target.
type pathMatch struct {
	start, end string
	// src and dst are the CALM IDs that the two ends bind to.
	src, dst string
	edges    []*observation.EvidenceEdge
}

func (m *pathMatch) key() string {
	return m.start + pairSeparator + m.end + pairSeparator + m.src + pairSeparator + m.dst
}

// adjacency gives the edges of the plan kinds that a path may use, by source node. A structural
// edge, an edge in an excluded file and an edge below rankOf of its kind are left out. Each list
// is sorted by native kind, target qualified name, line and edge ID.
func (r *planRun) adjacency(rankOf func(observation.CanonicalEdgeKind) int) map[string][]hop {
	kinds := kindSet(r.plan.Traversal.EdgeKinds)
	excluded := excludedFile(&r.input.ReconcileInput)
	nodes := r.input.Evidence.Nodes
	adj := map[string][]hop{}

	for _, edge := range r.input.Evidence.Edges {
		if !hasKind(kinds, edge.Kind) || IsStructuralKind(edge.Kind) || edge.Confidence.Rank() < rankOf(edge.Kind) {
			continue
		}

		if endsAreUsable(nodes, edge, excluded) {
			adj[edge.From] = append(adj[edge.From], hop{edge: edge, to: edge.To})
		}
	}

	for _, list := range adj {
		slices.SortStableFunc(list, func(a, b hop) int {
			return compareEdgeOrder(a.edge, nodes[a.to].QualifiedName, b.edge, nodes[b.to].QualifiedName)
		})
	}

	return adj
}

// endsAreUsable says whether both nodes of the edge are observed and neither is in an excluded file.
func endsAreUsable(nodes map[string]observation.ObservedNode, edge *observation.EvidenceEdge, excluded func(string) bool) bool {
	from, okFrom := nodes[edge.From]
	to, okTo := nodes[edge.To]

	if !okFrom || !okTo {
		return false
	}

	return !inExcludedFile(from.FilePath, excluded) && !inExcludedFile(to.FilePath, excluded)
}

// inExcludedFile says whether the file path is set and excluded.
func inExcludedFile(filePath string, excluded func(string) bool) bool {
	return filePath != "" && excluded(filePath)
}

// compareEdgeOrder orders edges by native kind, target qualified name, line and ID.
func compareEdgeOrder(left *observation.EvidenceEdge, leftTarget string, right *observation.EvidenceEdge, rightTarget string) int {
	return cmp.Or(
		compareStrings(left.NativeKind, right.NativeKind),
		compareStrings(leftTarget, rightTarget),
		cmp.Compare(spanLine(left), spanLine(right)),
		compareStrings(left.ID, right.ID),
	)
}

// startNodes gives the observed IDs of the nodes that bind to a subject, sorted.
func (r *planRun) startNodes() []string {
	var out []string

	for id, ids := range r.input.BindingSet.ByObserved {
		if _, ok := r.input.Evidence.Nodes[id]; !ok {
			continue
		}

		if slices.ContainsFunc(ids, func(x string) bool { return hasKey(r.subjects, x) }) {
			out = append(out, id)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// reach is how a breadth-first search reached a node.
type reach struct {
	edge   *observation.EvidenceEdge
	parent string
	depth  int
}

// pathMatches does a breadth-first search from each start node, up to the maximum depth of the plan.
// A node is reached once, by its first path. It gives the paths that end in a node of a target.
func (r *planRun) pathMatches(rankOf func(observation.CanonicalEdgeKind) int) []pathMatch {
	adj := r.adjacency(rankOf)
	related := composedRelated(r.input.Model)

	starts := r.startNodes()
	out := make([]pathMatch, 0, len(starts))

	for _, start := range starts {
		t := r.newTraversal()
		out = append(out, r.searchFrom(start, adj, related, t)...)

		r.noteTraversal(t)
	}

	return out
}

// pathSearch is the state of the breadth-first search from one start node.
type pathSearch struct {
	run     *planRun
	start   string
	sources []string
	adj     map[string][]hop
	related func(a, b string) bool
	trav    *traversal
	reached map[string]reach
	queue   []string
	out     []pathMatch
}

// A search that reaches the traversal limit stops: the matches found so far are real, and trav.stopped() says that others may be missing.
func (r *planRun) searchFrom(start string, adj map[string][]hop, related func(a, b string) bool, trav *traversal) []pathMatch {
	search := &pathSearch{
		run: r, start: start, adj: adj, related: related, trav: trav,
		sources: filterIDs(r.input.BindingSet.ByObserved[start], r.subjects),
		reached: map[string]reach{start: {}},
		queue:   []string{start},
	}

	for len(search.queue) > 0 {
		current := search.queue[0]
		search.queue = search.queue[1:]

		if search.reached[current].depth >= r.plan.Traversal.MaximumDepth {
			continue
		}

		if !search.expand(current) {
			break
		}
	}

	return search.out
}

// expand reaches the nodes next to current that the search has not reached, and gives false when the traversal limit stops the search.
func (search *pathSearch) expand(current string) bool {
	for _, next := range search.adj[current] {
		if _, seen := search.reached[next.to]; seen {
			continue
		}

		if !search.trav.visit() {
			return false
		}

		search.reached[next.to] = reach{parent: current, edge: next.edge, depth: search.reached[current].depth + 1}
		search.queue = append(search.queue, next.to)
		search.out = append(search.out, search.matchesAt(next.to)...)
	}

	return true
}

// matchesAt gives a match for each pair of a source and a target CALM ID of the node `end`.
func (search *pathSearch) matchesAt(end string) []pathMatch {
	var out []pathMatch

	targets := filterIDs(search.run.input.BindingSet.ByObserved[end], search.run.targets)

	for _, src := range search.sources {
		for _, dst := range targets {
			if src != dst && !search.related(src, dst) {
				out = append(out, pathMatch{start: search.start, end: end, src: src, dst: dst, edges: pathTo(end, search.start, search.reached)})
			}
		}
	}

	return out
}

// pathTo gives the edges from start to end along the first-parent links of a search.
func pathTo(end, start string, reached map[string]reach) []*observation.EvidenceEdge {
	path := []*observation.EvidenceEdge{}
	for at := end; at != start; at = reached[at].parent {
		path = append(path, reached[at].edge)
	}

	slices.Reverse(path)

	return path
}

// filterIDs keeps the IDs that are in set. The order is kept.
func filterIDs(ids []string, set map[string]struct{}) []string {
	var out []string

	for _, id := range ids {
		if hasKey(set, id) {
			out = append(out, id)
		}
	}

	return out
}

// boundEdgeOf makes the BoundEdge of an observed edge from the evidence and the binding set.
func (r *planRun) boundEdgeOf(edge *observation.EvidenceEdge) *BoundEdge {
	return &BoundEdge{
		Edge: edge, From: r.input.Evidence.Nodes[edge.From], To: r.input.Evidence.Nodes[edge.To],
		FromIDs: r.input.BindingSet.ByObserved[edge.From], ToIDs: r.input.BindingSet.ByObserved[edge.To],
	}
}

// denyPath gives a finding for each path of at most MaximumDepth edges from a subject to a target.
func (r *planRun) denyPath() []candidate {
	matches := r.pathMatches(r.rankFor)

	if r.anyRankAboveZero() {
		r.countSubThresholdPaths(matches)
	}

	out := make([]candidate, 0, len(matches))
	for i := range matches {
		out = append(out, r.pathCandidate(&matches[i]))
	}

	return out
}

// countSubThresholdPaths counts the paths that exist when no rank applies and that are not among the strong matches.
func (r *planRun) countSubThresholdPaths(strongMatches []pathMatch) {
	strong := make(map[string]struct{}, len(strongMatches))
	for i := range strongMatches {
		strong[strongMatches[i].key()] = struct{}{}
	}

	all := r.pathMatches(func(observation.CanonicalEdgeKind) int { return 0 })
	for i := range all {
		if !hasKey(strong, all[i].key()) {
			r.coverage.SubThresholdEvidence++
		}
	}
}

func (r *planRun) pathCandidate(match *pathMatch) candidate {
	nodes := r.input.Evidence.Nodes
	start, end := nodes[match.start], nodes[match.end]
	first := r.boundEdgeOf(match.edges[0])
	steps := edgeSteps(first)

	for _, edge := range match.edges[1:] {
		steps = append(steps, edgeSteps(r.boundEdgeOf(edge))[1:]...)
	}

	loc := edgeLocation(first)

	return candidate{
		fingerprint: r.fingerprintFor(Parts{
			RuleID:                   r.plan.RuleID,
			SourceArchitectureID:     match.src,
			TargetArchitectureID:     match.dst,
			CanonicalEdgeKind:        "path",
			SourceQualifiedName:      start.QualifiedName,
			TargetQualifiedName:      end.QualifiedName,
			NormalizedRepositoryPath: start.FilePath,
		}),
		kind:         KindForbiddenPath,
		sourceID:     match.src,
		targetID:     match.dst,
		message:      fmt.Sprintf("forbidden path %s -> %s (%d hops)", match.src, match.dst, len(match.edges)),
		edge:         first,
		endNode:      &end,
		evidencePath: steps,
		location:     &loc,
	}
}
