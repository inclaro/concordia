package adr

import (
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/inclaro/concordia/internal/canonjson"
)

// HistoryNode is one committed decision of the history. Path is the identity (design D16) and Label the
// canonical `adr_id`.
type HistoryNode struct {
	Path  string
	Label string
	// Mapped is the status that the lifecycle `statusMap` gave; "" when no mapping applied.
	Mapped string
	Class  LifecycleClass
	// Decision is the evidence entity, with its properties verbatim.
	Decision EvidenceDecision
}

// Link is one supersession link, successor to predecessor, by decision path.
type Link struct {
	FromPath   string
	ToPath     string
	Provenance Provenance
	Effect     LinkEffect
	// Line is the line of the edge, else of the decision that holds the `superseded_by` value.
	Line int
}

// History is the normalized supersession history of the components that contain a registered decision
// (design D5). Everything is sorted; the result does not depend on the order of the input.
type History struct {
	// Nodes are the committed decisions of the analyzed components, sorted by path.
	Nodes []HistoryNode
	// Links are sorted by from path, then to path.
	Links []Link
	// Anomalies are sorted by reason (in the order of Reasons), then paths, then value.
	Anomalies []Anomaly
	// Components are the analyzed components as sorted path lists, sorted by first path.
	Components [][]string

	ambiguous []bool
	nodeIdx   map[string]int
	comp      map[string]int
	succ      map[string][]string
	pred      map[string][]string
	effective map[string]bool
	reasons   []Reason
	supported bool
}

// linkAcc accumulates the provenance of one link.
type linkAcc struct {
	edge, meta bool
	line       int
}

// status is the effective status of the node: the mapped status, else the evidence status.
func (n *HistoryNode) status() string {
	if n.Mapped != "" {
		return n.Mapped
	}

	return n.Decision.Status
}

// BuildHistory builds the history (design D5). Nodes are the evidence decisions that committed reports true for
// (nil: all), keyed by path. Only components that contain a registered path are analyzed: their nodes, links,
// and anomalies are reported, and a component with an anomaly is ambiguous as a whole. evidence nil or not Supported
// gives an empty history whose applicability results are all unknown.
func BuildHistory(
	evidence *DecisionEvidence, lifecycle *Lifecycle, committed func(path string) bool, registeredPaths []string,
) *History {
	hist := &History{
		Nodes: []HistoryNode{}, Links: []Link{}, Anomalies: []Anomaly{}, Components: [][]string{},
		nodeIdx: map[string]int{}, comp: map[string]int{}, succ: map[string][]string{}, pred: map[string][]string{},
		effective: map[string]bool{},
	}
	if evidence == nil || !evidence.Supported {
		return hist
	}

	hist.supported = true

	bld := newBuilder(evidence, lifecycle, committed)
	bld.collectLinks()
	bld.detect()
	bld.publish(hist, registeredPaths)

	return hist
}

// builder holds the working state of BuildHistory.
type builder struct {
	ev      *DecisionEvidence
	nodes   []HistoryNode
	idx     map[string]int
	byLabel map[string][]string
	links   map[[2]string]*linkAcc
	effect  map[[2]string]LinkEffect
	raw     []Anomaly
	// succs maps a predecessor index to its successor indexes; sorted.
	succs [][]int
	preds [][]int
	dsu   []int
}

func newBuilder(ev *DecisionEvidence, lifecycle *Lifecycle, committed func(string) bool) *builder {
	if lifecycle == nil {
		lifecycle, _ = NewLifecycle(nil, nil)
	}

	bld := &builder{ev: ev, idx: map[string]int{}, byLabel: map[string][]string{}, links: map[[2]string]*linkAcc{}}

	decisions := slices.Clone(ev.Decisions)
	slices.SortStableFunc(decisions, func(x, y EvidenceDecision) int { return canonjson.CompareUTF16(x.Path, y.Path) })

	for i := range decisions {
		bld.addNode(&decisions[i], lifecycle, committed)
	}

	return bld
}

// addNode adds a committed decision that no earlier decision of the same path has.
func (b *builder) addNode(d *EvidenceDecision, lifecycle *Lifecycle, committed func(string) bool) {
	if _, dup := b.idx[d.Path]; dup || !isCommittedPath(committed, d.Path) {
		return
	}

	mapped, class := lifecycle.Classify(d.Status, d.StatusText)
	b.idx[d.Path] = len(b.nodes)
	b.nodes = append(b.nodes, HistoryNode{Path: d.Path, Label: d.Label, Mapped: mapped, Class: class, Decision: *d})
	b.byLabel[d.Label] = append(b.byLabel[d.Label], d.Path)
}

// isCommittedPath says whether committed (nil: all) reports true for the path.
func isCommittedPath(committed func(string) bool, p string) bool {
	return committed == nil || committed(p)
}

func (b *builder) addLink(from, to string, edge bool, line int) {
	key := [2]string{from, to}

	acc := b.links[key]
	if acc == nil {
		acc = &linkAcc{}
		b.links[key] = acc
	}

	if edge {
		acc.edge = true
		acc.line = line
	} else {
		acc.meta = true
		if !acc.edge {
			acc.line = line
		}
	}
}

// collectLinks reads the `supersedes` edges and the `superseded_by` values.
func (b *builder) collectLinks() {
	for _, edge := range b.ev.Supersedes {
		b.collectEdge(&edge)
	}

	for i := range b.nodes {
		b.collectSupersededBy(&b.nodes[i])
	}
}

// collectEdge adds the link of a `supersedes` edge between two decisions of the history; an edge to an unknown
// decision is an anomaly.
func (b *builder) collectEdge(edge *SupersedesEdge) {
	if _, ok := b.idx[edge.From]; !ok {
		return
	}

	if _, ok := b.idx[edge.To]; !ok {
		b.raw = append(b.raw, Anomaly{Kind: ReasonMissingTarget, Paths: []string{edge.From}, Value: edge.To})

		return
	}

	b.addLink(edge.From, edge.To, true, edge.Line)
}

// collectSupersededBy resolves the `superseded_by` value of a decision.
func (b *builder) collectSupersededBy(node *HistoryNode) {
	if strings.TrimSpace(node.Decision.SupersededBy) == "" {
		return
	}

	tokens := splitTargets(node.Decision.SupersededBy)
	if len(tokens) == 0 {
		b.raw = append(b.raw, Anomaly{Kind: ReasonMissingTarget, Paths: []string{node.Path}, Value: node.Decision.SupersededBy})

		return
	}

	for _, token := range tokens {
		b.resolveTarget(node, token)
	}
}

// splitTargets splits a `superseded_by` value on commas, semicolons, and whitespace.
func splitTargets(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
}

// resolveTarget resolves one `superseded_by` token: a path relative to the holding decision, or (only for a
// value that has no path separator and no extension) a canonical decision ID. The target is the successor.
func (b *builder) resolveTarget(holder *HistoryNode, token string) {
	missing := func() {
		b.raw = append(b.raw, Anomaly{Kind: ReasonMissingTarget, Paths: []string{holder.Path}, Value: token})
	}

	tgt := cleanSupersededByToken(token)
	if tgt == "" || strings.HasPrefix(tgt, "/") {
		missing()

		return
	}

	if !strings.Contains(tgt, "/") && path.Ext(tgt) == "" {
		b.resolveID(holder, tgt, missing)

		return
	}

	target, ok := b.relativeTarget(holder, tgt)
	if !ok {
		missing()

		return
	}

	b.addLink(target, holder.Path, false, holder.Decision.Line)
}

// cleanSupersededByToken removes the markup, the fragment, and the leading `./` of a `superseded_by` token.
func cleanSupersededByToken(token string) string {
	tgt := strings.Trim(token, "`<>")
	tgt, _, _ = strings.Cut(tgt, "#")

	for strings.HasPrefix(tgt, "./") {
		tgt = tgt[2:]
	}

	return tgt
}

// relativeTarget joins a path token to the directory of the holder, within the project of the holder. It says
// whether the result leaves the repository or is no decision of the history.
func (b *builder) relativeTarget(holder *HistoryNode, tgt string) (string, bool) {
	prefix := ""
	if holder.Decision.Project != "" {
		prefix = "@" + holder.Decision.Project + "/"
	}

	joined := path.Join(path.Dir(strings.TrimPrefix(holder.Path, prefix)), tgt)
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}

	target := prefix + joined
	_, ok := b.idx[target]

	return target, ok
}

// resolveID resolves a bare decision ID within the project of the holder.
func (b *builder) resolveID(holder *HistoryNode, decisionID string, missing func()) {
	var matches []string

	for _, p := range b.byLabel[CanonicalID(decisionID)] {
		if b.nodes[b.idx[p]].Decision.Project == holder.Decision.Project {
			matches = append(matches, p)
		}
	}

	switch len(matches) {
	case 0:
		missing()
	case 1:
		b.addLink(matches[0], holder.Path, false, holder.Decision.Line)
	default:
		paths := append([]string{holder.Path}, matches...)
		slices.SortFunc(paths, canonjson.CompareUTF16)
		b.raw = append(b.raw, Anomaly{Kind: ReasonDuplicateID, Paths: slices.Compact(paths), Value: CanonicalID(decisionID)})
	}
}

// detect classifies the links and finds every anomaly: the raw ones from link resolution, duplicate labels,
// contradictory statuses, cycles, and competing successors.
func (b *builder) detect() {
	count := len(b.nodes)
	b.succs, b.preds = make([][]int, count), make([][]int, count)
	b.dsu = make([]int, count)
	b.effect = make(map[[2]string]LinkEffect, len(b.links))

	for i := range b.dsu {
		b.dsu[i] = i
	}

	hasSuccessor := b.classifyLinks(b.sortedLinkKeys())
	b.flagSupersededWithoutSuccessor(hasSuccessor)
	b.flagDuplicateLabels()
	b.findCycles()
	b.findCompeting()
}

func compareLinkKeys(x, y [2]string) int {
	if c := canonjson.CompareUTF16(x[0], y[0]); c != 0 {
		return c
	}

	return canonjson.CompareUTF16(x[1], y[1])
}

// sortedLinkKeys are the keys of the links, sorted by from path, then to path.
func (b *builder) sortedLinkKeys() [][2]string {
	keys := slices.Collect(maps.Keys(b.links))
	slices.SortFunc(keys, compareLinkKeys)

	return keys
}

// classifyLinks fills the successor and predecessor indexes, the components, and the effect of every link. It
// gives, per node index, whether the node has a successor.
func (b *builder) classifyLinks(keys [][2]string) []bool {
	hasSuccessor := make([]bool, len(b.nodes))

	for _, key := range keys {
		from, to := b.idx[key[0]], b.idx[key[1]]
		b.succs[to] = append(b.succs[to], from)
		b.preds[from] = append(b.preds[from], to)
		b.union(from, to)
		hasSuccessor[to] = true

		b.classifyEffect(key, &b.nodes[from])
	}

	return hasSuccessor
}

// classifyEffect gives the effect of the link to its successor. Only a pending successor leaves the predecessor as
// it is; a successor that is neither active, pending, nor itself superseded is an anomaly, and the component is
// ambiguous as a whole.
func (b *builder) classifyEffect(key [2]string, succ *HistoryNode) {
	b.effect[key] = EffectEffective

	switch {
	case succ.Class == ClassPending:
		b.effect[key] = EffectPending
	case succ.Class == ClassInactive && succ.status() != StatusSuperseded:
		b.raw = append(b.raw, Anomaly{Kind: ReasonContradictoryStatus, Paths: sortedPair(key[0], key[1])})
	}
}

// flagSupersededWithoutSuccessor reports the decisions with status superseded that no link and no `superseded_by`
// value explains.
func (b *builder) flagSupersededWithoutSuccessor(hasSuccessor []bool) {
	for i := range b.nodes {
		node := &b.nodes[i]
		if !hasSuccessor[i] && node.isSupersededWithoutValue() {
			b.raw = append(b.raw, Anomaly{Kind: ReasonContradictoryStatus, Paths: []string{node.Path}})
		}
	}
}

// isSupersededWithoutValue says that the status is superseded while no `superseded_by` value names a successor.
func (n *HistoryNode) isSupersededWithoutValue() bool {
	return n.status() == StatusSuperseded && strings.TrimSpace(n.Decision.SupersededBy) == ""
}

func (b *builder) flagDuplicateLabels() {
	for label, paths := range b.byLabel {
		if len(paths) > 1 {
			b.raw = append(b.raw, Anomaly{Kind: ReasonDuplicateID, Paths: slices.Clone(paths), Value: label})
		}
	}
}

func sortedPair(a, c string) []string {
	if canonjson.CompareUTF16(a, c) > 0 {
		a, c = c, a
	}

	return []string{a, c}
}

// Applicability gives the applicability of a decision by path from the history: `ambiguous` (reason: the first
// anomaly of its component) for a component with an anomaly, `superseded` when an effective link names a
// successor, `active` for an active decision, else `inactive`. statusLags is true when the decision is
// superseded through an effective link while its own lifecycle class is still active. A path outside the
// analyzed components, and every path when the evidence is not supported, is `unknown`.
func (h *History) Applicability(decision string) (state ApplicabilityState, reason string, statusLags bool) {
	if h == nil || !h.supported {
		return ApplicabilityUnknown, EvidenceNoCapability, false
	}

	i, ok := h.nodeIdx[decision]
	if !ok {
		return ApplicabilityUnknown, EvidenceNotCommitted, false
	}

	node := &h.Nodes[i]

	switch {
	case h.ambiguous[h.comp[decision]]:
		return ApplicabilityAmbiguous, string(h.reasons[h.comp[decision]]), false
	case h.effective[decision]:
		return ApplicabilitySuperseded, string(ReasonSuperseded), node.Class == ClassActive
	case node.Class == ClassActive:
		return ApplicabilityActive, "", false
	default:
		return ApplicabilityInactive, string(ReasonInactive), false
	}
}

// Successors gives the paths of the decisions that name the decision as their predecessor, effective or
// pending, sorted.
func (h *History) Successors(decision string) []string {
	if h == nil {
		return []string{}
	}

	return append([]string{}, h.succ[decision]...)
}

// Predecessors gives the paths of the decisions that the decision supersedes, effective or pending, sorted.
func (h *History) Predecessors(decision string) []string {
	if h == nil {
		return []string{}
	}

	return append([]string{}, h.pred[decision]...)
}

// Ambiguous says whether the decision is in an analyzed component that has an anomaly.
func (h *History) Ambiguous(decision string) bool {
	if h == nil {
		return false
	}

	c, ok := h.comp[decision]

	return ok && h.ambiguous[c]
}
