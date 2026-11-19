package conformance

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// CoverageInputs are the file lists that file coverage needs.
type CoverageInputs struct {
	// Sizes are the sizes in bytes of the workspace files, by "/" path.
	Sizes          map[string]int64
	WorkspaceFiles []string
	Include        []string
	Exclude        []string
	// CbmFiles are the file paths of the CBM File nodes.
	CbmFiles   []string
	DirtyFiles []string
	// MinimumCoverage is the workspace `check.minimumCoverage`.
	MinimumCoverage float64
}

// EvidenceSet is the observed graph part that a run evaluates.
type EvidenceSet struct {
	Nodes map[string]observation.ObservedNode
	Edges []*observation.EvidenceEdge
}

// ReconcileInput is the input of Reconcile.
type ReconcileInput struct {
	Model *intent.ArchitectureModel
	// BindingRootsByNode are the `sourceRoot` globs of the bindings of each CALM node.
	BindingRootsByNode      map[string][]string
	SupportedCanonicalKinds []observation.CanonicalEdgeKind
	CoverageInputs          CoverageInputs
	Evidence                EvidenceSet
	BindingSet              binding.Set
}

// BoundEdge is a candidate edge whose endpoints both bind to at least one CALM ID.
type BoundEdge struct {
	Edge    *observation.EvidenceEdge
	From    observation.ObservedNode
	To      observation.ObservedNode
	FromIDs []string
	ToIDs   []string
}

const pairSeparator = "\x00"

// IsStructuralKind says whether an edge kind only gives structure (no dependency).
func IsStructuralKind(kind observation.CanonicalEdgeKind) bool {
	return kind == observation.KindContains || kind == observation.KindDefines
}

// compareStrings orders strings like the JavaScript default sort and `<` (UTF-16 code units).
func compareStrings(a, b string) int { return canonjson.CompareUTF16(a, b) }

func spanLine(e *observation.EvidenceEdge) int {
	if e.SourceSpan == nil {
		return 0
	}

	return e.SourceSpan.Line
}

func compareEdges(a, b *BoundEdge) int {
	return cmp.Or(
		compareStrings(a.Edge.NativeKind, b.Edge.NativeKind),
		compareStrings(a.From.QualifiedName, b.From.QualifiedName),
		compareStrings(a.To.QualifiedName, b.To.QualifiedName),
		cmp.Compare(spanLine(a.Edge), spanLine(b.Edge)),
		compareStrings(a.Edge.ID, b.Edge.ID),
	)
}

// EdgeScan is the result of scanEdges.
type EdgeScan struct {
	Bound []*BoundEdge
	// HalfBound are the edges with exactly one bound end. Only ScanEdges fills it.
	HalfBound []*BoundEdge
	// Skipped counts the candidate edges that the scan leaves out, by reason.
	Skipped SkipCounts
}

// excludedFile gives the function that says whether a path is in an excluded file.
func excludedFile(input *ReconcileInput) func(string) bool {
	if len(input.CoverageInputs.Exclude) > 0 {
		return fsutil.GlobMatcher(input.CoverageInputs.Exclude)
	}

	return func(string) bool { return false }
}

// scanEdges gives the candidate edges of the given kinds (nil = the code kinds except
// `contains` and `defines`: infrastructure, reference, documentation, diagram, protocol
// and test facts never enter the pair reconciliation), sorted. Edges with an endpoint in
// an excluded file are dropped.
func scanEdges(input *ReconcileInput, kinds map[observation.CanonicalEdgeKind]struct{}) EdgeScan {
	return scanEdgesWith(input, kinds, false)
}

// ScanEdges is scanEdges for the view projections (internal/views): it resolves the endpoints
// of the edges in the same way as the reconciliation, and it also gives the edges with exactly
// one bound end (EdgeScan.HalfBound), so that a view can report the coverage that the unbound
// end takes away. Unbound edges stay counted in Skipped.Unbound.
func ScanEdges(input *ReconcileInput, kinds map[observation.CanonicalEdgeKind]struct{}) EdgeScan {
	return scanEdgesWith(input, kinds, true)
}

// scanKind says whether a scan over kinds (nil: the code kinds without the structural ones) takes the kind.
func scanKind(kinds map[observation.CanonicalEdgeKind]struct{}, kind observation.CanonicalEdgeKind) bool {
	if kinds != nil {
		_, ok := kinds[kind]

		return ok
	}

	return !IsStructuralKind(kind) && observation.IsCodeKind(kind)
}

// edgeScanner holds the state of one scanEdgesWith call.
type edgeScanner struct {
	input         *ReconcileInput
	kinds         map[observation.CanonicalEdgeKind]struct{}
	excluded      func(string) bool
	scan          EdgeScan
	keepHalfBound bool
}

func scanEdgesWith(input *ReconcileInput, kinds map[observation.CanonicalEdgeKind]struct{}, keepHalfBound bool) EdgeScan {
	sc := &edgeScanner{input: input, kinds: kinds, excluded: excludedFile(input), keepHalfBound: keepHalfBound}

	for _, edge := range input.Evidence.Edges {
		sc.take(edge)
	}

	slices.SortStableFunc(sc.scan.Bound, compareEdges)

	return sc.scan
}

// take sorts one evidence edge into the bound edges, the half-bound edges, or a skip count.
func (sc *edgeScanner) take(edge *observation.EvidenceEdge) {
	if !scanKind(sc.kinds, edge.Kind) {
		sc.scan.Skipped.Unsupported += unsupportedKind(edge.Kind)

		return
	}

	from, okFrom := sc.input.Evidence.Nodes[edge.From]
	dest, okTo := sc.input.Evidence.Nodes[edge.To]

	if !okFrom || !okTo {
		sc.scan.Skipped.EndpointMissing++

		return
	}

	if sc.inExcludedFile(&from) || sc.inExcludedFile(&dest) {
		sc.scan.Skipped.OutOfScope++

		return
	}

	sc.place(&BoundEdge{
		Edge: edge, From: from, To: dest,
		FromIDs: sc.input.BindingSet.ByObserved[edge.From], ToIDs: sc.input.BindingSet.ByObserved[edge.To],
	})
}

// inExcludedFile says whether the node sits in a file that the coverage excludes.
func (sc *edgeScanner) inExcludedFile(node *observation.ObservedNode) bool {
	return node.FilePath != "" && sc.excluded(node.FilePath)
}

// place keeps the edge as bound, as half-bound when the scan asks for it, or counts it as unbound.
func (sc *edgeScanner) place(bound *BoundEdge) {
	hasFrom, hasTo := len(bound.FromIDs) > 0, len(bound.ToIDs) > 0
	if hasFrom && hasTo {
		sc.scan.Bound = append(sc.scan.Bound, bound)

		return
	}

	sc.scan.Skipped.Unbound++

	if sc.keepHalfBound && (hasFrom || hasTo) {
		sc.scan.HalfBound = append(sc.scan.HalfBound, bound)
	}
}

// coverageForNodes gives the file coverage of the files that the bindings of
// nodeIDs cover (the full workspace include when there are none).
func coverageForNodes(input *ReconcileInput, nodeIDs []string) observation.FileCoverage {
	c := &input.CoverageInputs
	covIn := observation.FileCoverageInput{
		WorkspaceFiles: c.WorkspaceFiles,
		Include:        c.Include,
		Exclude:        c.Exclude,
		CbmFiles:       c.CbmFiles,
		DirtyFiles:     c.DirtyFiles,
		Sizes:          c.Sizes,
		ScopeGlobs:     bindingScopeGlobs(input, nodeIDs),
	}

	return observation.ComputeFileCoverage(&covIn)
}

// bindingScopeGlobs gives the sorted source root globs of the bindings of nodeIDs, nil when there are none.
func bindingScopeGlobs(input *ReconcileInput, nodeIDs []string) []string {
	globs := map[string]struct{}{}

	for _, id := range nodeIDs {
		for _, g := range input.BindingRootsByNode[id] {
			globs[g] = struct{}{}
		}
	}

	if len(globs) == 0 {
		return nil
	}

	list := make([]string, 0, len(globs))
	for g := range globs {
		list = append(list, g)
	}

	slices.SortFunc(list, compareStrings)

	return list
}

func pairKey(a, b string) string { return a + pairSeparator + b }

// permittingRelationships gives the relationships that permit an observed edge a -> b:
// `connects` source to destination, `interacts` actor to node, `composed-of` and
// `deployed-in` in either direction.
func permittingRelationships(model *intent.ArchitectureModel) map[string][]*intent.RelationshipDeclaration {
	out := map[string][]*intent.RelationshipDeclaration{}
	add := func(a, b string, r *intent.RelationshipDeclaration) {
		k := pairKey(a, b)
		out[k] = append(out[k], r)
	}

	for i := range model.Relationships {
		r := &model.Relationships[i]

		switch {
		case isDeclaredConnection(r):
			for _, target := range r.Targets {
				add(r.Source, target, r)
			}
		case isContainment(r):
			for _, target := range r.Targets {
				add(r.Container, target, r)
				add(target, r.Container, r)
			}
		}
	}

	return out
}

// isDeclaredConnection says whether r is a `connects` or `interacts` relationship with a source.
func isDeclaredConnection(r *intent.RelationshipDeclaration) bool {
	return (r.Kind == intent.RelConnects || r.Kind == intent.RelInteracts) && r.Source != ""
}

// isContainment says whether r is a `composed-of` or `deployed-in` relationship with a container.
func isContainment(r *intent.RelationshipDeclaration) bool {
	return (r.Kind == intent.RelComposedOf || r.Kind == intent.RelDeployedIn) && r.Container != ""
}

func kindAllowedBy(rel *intent.RelationshipDeclaration, kind observation.CanonicalEdgeKind) bool {
	if rel.Observation == nil || rel.Observation.AllowedEdgeKinds == nil {
		return true
	}

	return slices.Contains(rel.Observation.AllowedEdgeKinds, kind)
}

func confidenceOkFor(rel *intent.RelationshipDeclaration, edge *observation.EvidenceEdge) bool {
	if rel.Observation == nil || rel.Observation.MinimumConfidence == "" {
		return true
	}

	return edge.Confidence.Rank() >= rel.Observation.MinimumConfidence.Rank()
}

// edgePairs gives the [a, b] pairs of distinct CALM IDs of a bound edge.
func edgePairs(e *BoundEdge) [][2]string {
	var out [][2]string

	for _, a := range e.FromIDs {
		for _, b := range e.ToIDs {
			if a != b {
				out = append(out, [2]string{a, b})
			}
		}
	}

	return out
}

type observedPair struct {
	from, to string
	edges    []*BoundEdge
}

func sortedKinds(edges []*BoundEdge) []observation.CanonicalEdgeKind {
	seen := map[observation.CanonicalEdgeKind]struct{}{}
	kinds := []observation.CanonicalEdgeKind{}

	for _, e := range edges {
		if _, ok := seen[e.Edge.Kind]; !ok {
			seen[e.Edge.Kind] = struct{}{}
			kinds = append(kinds, e.Edge.Kind)
		}
	}

	slices.SortFunc(kinds, func(a, b observation.CanonicalEdgeKind) int { return compareStrings(string(a), string(b)) })

	return kinds
}

// reconciler holds the state of one Reconcile call.
type reconciler struct {
	input      *ReconcileInput
	permitting map[string][]*intent.RelationshipDeclaration
	covCache   map[string]bool
}

// sufficient says whether the coverage of a CALM node reaches the minimum.
func (rc *reconciler) sufficient(id string) bool {
	v, ok := rc.covCache[id]
	if !ok {
		v = coverageForNodes(rc.input, []string{id}).Ratio >= rc.input.CoverageInputs.MinimumCoverage
		rc.covCache[id] = v
	}

	return v
}

// observedGrouping collects the bound edges by directed CALM pair, in first-seen order.
type observedGrouping struct {
	byKey map[string]*observedPair
	order []*observedPair
}

// add puts the edge into the group of each of its CALM pairs.
func (g *observedGrouping) add(edge *BoundEdge) {
	for _, p := range edgePairs(edge) {
		k := pairKey(p[0], p[1])

		group, ok := g.byKey[k]
		if !ok {
			group = &observedPair{from: p[0], to: p[1]}
			g.byKey[k] = group
			g.order = append(g.order, group)
		}

		group.edges = append(group.edges, edge)
	}
}

// groupObserved groups the bound edges of code kinds by directed CALM pair, in first-seen
// order. An edge of another kind never enters a group, so it never creates a classification.
func groupObserved(bound []*BoundEdge) (map[string]*observedPair, []*observedPair) {
	g := &observedGrouping{byKey: map[string]*observedPair{}}

	for _, edge := range bound {
		if observation.IsCodeKind(edge.Edge.Kind) {
			g.add(edge)
		}
	}

	return g.byKey, g.order
}

func (rc *reconciler) classifyObserved(group *observedPair) ReconciliationEntry {
	rels := rc.permitting[pairKey(group.from, group.to)]
	entry := ReconciliationEntry{From: group.from, To: group.to, ObservedKinds: sortedKinds(group.edges), EdgeCount: len(group.edges)}

	if len(rels) == 0 {
		entry.Classification = ClassUndeclared
	} else {
		ok := slices.IndexFunc(rels, func(rel *intent.RelationshipDeclaration) bool {
			return slices.ContainsFunc(group.edges, func(e *BoundEdge) bool { return kindAllowedBy(rel, e.Edge.Kind) && confidenceOkFor(rel, e.Edge) })
		})

		if ok >= 0 {
			entry.Classification = ClassConforming
			entry.RelationshipID = rels[ok].ID
		} else {
			entry.Classification = ClassContradiction
			entry.RelationshipID = rels[0].ID
		}
	}

	if !rc.sufficient(group.from) || !rc.sufficient(group.to) {
		entry.Classification = ClassUnknown
	}

	return entry
}

// unobservedEntries gives the declared (connects / interacts) pairs without any observed edge.
func (rc *reconciler) unobservedEntries(observed map[string]*observedPair) []ReconciliationEntry {
	var entries []ReconciliationEntry

	for i := range rc.input.Model.Relationships {
		rel := &rc.input.Model.Relationships[i]
		if !isDeclaredConnection(rel) {
			continue
		}

		entries = append(entries, rc.unobservedTargets(rel, observed)...)
	}

	return entries
}

// unobservedTargets gives an entry for each target of rel that no observed edge reaches from its source.
func (rc *reconciler) unobservedTargets(rel *intent.RelationshipDeclaration, observed map[string]*observedPair) []ReconciliationEntry {
	var entries []ReconciliationEntry

	for _, target := range rel.Targets {
		if _, ok := observed[pairKey(rel.Source, target)]; ok {
			continue
		}

		class := ClassUnobserved
		if !rc.sufficient(rel.Source) || !rc.sufficient(target) {
			class = ClassUnknown
		}

		entries = append(entries, ReconciliationEntry{
			From: rel.Source, To: target, RelationshipID: rel.ID, Classification: class,
			ObservedKinds: []observation.CanonicalEdgeKind{}, EdgeCount: 0,
		})
	}

	return entries
}

// Reconcile classifies each directed CALM pair. The result is sorted by (from, to).
func Reconcile(input *ReconcileInput) []ReconciliationEntry {
	rc := &reconciler{input: input, permitting: permittingRelationships(input.Model), covCache: map[string]bool{}}
	observed, order := groupObserved(scanEdges(input, nil).Bound)

	entries := make([]ReconciliationEntry, 0, len(order))
	for _, group := range order {
		entries = append(entries, rc.classifyObserved(group))
	}

	entries = append(entries, rc.unobservedEntries(observed)...)

	slices.SortStableFunc(entries, func(x, y ReconciliationEntry) int {
		return cmp.Or(compareStrings(x.From, y.From), compareStrings(x.To, y.To), compareStrings(x.RelationshipID, y.RelationshipID))
	})

	return entries
}

// splitPair undoes pairKey.
func splitPair(k string) (first, second string) {
	first, second, _ = strings.Cut(k, pairSeparator)

	return first, second
}
