package adr

import (
	"maps"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// publish fills h with the analyzed components.
func (b *builder) publish(hist *History, registered []string) {
	hist.Components = b.publishNodes(hist, b.analyzedRoots(registered))
	hist.renumberComponents()

	b.publishLinks(hist)
	b.publishAnomalies(hist)
}

// analyzedRoots are the sorted DSU roots of the components that contain a registered path.
func (b *builder) analyzedRoots(registered []string) []int {
	roots := map[int]bool{}

	for _, p := range registered {
		if i, ok := b.idx[p]; ok {
			roots[b.find(i)] = true
		}
	}

	return slices.Sorted(maps.Keys(roots))
}

// publishNodes adds the nodes of the components of the roots to hist and gives the member paths of each component.
func (b *builder) publishNodes(hist *History, roots []int) [][]string {
	compOf := make(map[int]int, len(roots))
	members := make([][]string, len(roots))

	for c, r := range roots {
		compOf[r] = c
	}

	for i := range b.nodes { // sorted by path, so every member list is sorted
		c, ok := compOf[b.find(i)]
		if !ok {
			continue
		}

		node := b.nodes[i]
		hist.nodeIdx[node.Path] = len(hist.Nodes)
		hist.Nodes = append(hist.Nodes, node)
		members[c] = append(members[c], node.Path)
	}

	return members
}

// renumberComponents sorts the components by first path. Components were numbered by DSU root; this numbers them
// by the sorted order so that comp indexes match.
func (h *History) renumberComponents() {
	slices.SortFunc(h.Components, func(x, y []string) int { return canonjson.CompareUTF16(x[0], y[0]) })

	order := map[string]int{}

	for c, m := range h.Components {
		for _, p := range m {
			order[p] = c
		}
	}

	h.comp = order
	h.ambiguous = make([]bool, len(h.Components))
	h.reasons = make([]Reason, len(h.Components))
}

// inComponent says whether the path is a node of an analyzed component.
func (h *History) inComponent(p string) bool {
	_, ok := h.comp[p]

	return ok
}

func (b *builder) publishLinks(hist *History) {
	keys := slices.DeleteFunc(b.sortedLinkKeys(), func(key [2]string) bool { return !hist.inComponent(key[0]) })

	for _, key := range keys {
		acc := b.links[key]
		hist.appendLink(Link{FromPath: key[0], ToPath: key[1], Provenance: acc.provenance(), Effect: b.effect[key], Line: acc.line})
	}
}

// appendLink adds a published link and indexes it by successor and predecessor.
func (h *History) appendLink(link Link) {
	h.Links = append(h.Links, link)
	h.succ[link.ToPath] = append(h.succ[link.ToPath], link.FromPath)
	h.pred[link.FromPath] = append(h.pred[link.FromPath], link.ToPath)

	if link.Effect == EffectEffective {
		h.effective[link.ToPath] = true
	}
}

// provenance says whether an edge, a `superseded_by` value, or both gave the link.
func (a *linkAcc) provenance() Provenance {
	switch {
	case a.edge && a.meta:
		return ProvenanceBoth
	case a.meta:
		return ProvenanceMetadata
	default:
		return ProvenanceEdge
	}
}

// reasonRank is the position of a reason in Reasons.
func reasonRank(r Reason) int {
	if i := slices.Index(Reasons, r); i >= 0 {
		return i
	}

	return len(Reasons)
}

func (b *builder) publishAnomalies(hist *History) {
	hist.Anomalies = append(hist.Anomalies, b.analyzedAnomalies(hist)...)
	slices.SortFunc(hist.Anomalies, compareAnomalies)

	for _, a := range hist.Anomalies {
		hist.markAmbiguous(a)
	}
}

// analyzedAnomalies are the raw anomalies that name a path of an analyzed component, without duplicates.
func (b *builder) analyzedAnomalies(hist *History) []Anomaly {
	seen := map[string]bool{}

	var out []Anomaly

	for _, a := range b.raw {
		key := anomalyKey(&a)
		if seen[key] || !slices.ContainsFunc(a.Paths, hist.inComponent) {
			continue
		}

		seen[key] = true

		out = append(out, a)
	}

	return out
}

func anomalyKey(a *Anomaly) string {
	return string(a.Kind) + "\x00" + strings.Join(a.Paths, "\x00") + "\x01" + a.Value
}

// compareAnomalies orders by reason (in the order of Reasons), then paths, then value.
func compareAnomalies(x, y Anomaly) int {
	if c := reasonRank(x.Kind) - reasonRank(y.Kind); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(strings.Join(x.Paths, "\x00"), strings.Join(y.Paths, "\x00")); c != 0 {
		return c
	}

	return canonjson.CompareUTF16(x.Value, y.Value)
}

// markAmbiguous marks every analyzed component that the anomaly names; the first anomaly gives the reason.
func (h *History) markAmbiguous(a Anomaly) {
	for _, p := range a.Paths {
		if c, ok := h.comp[p]; ok && !h.ambiguous[c] {
			h.ambiguous[c], h.reasons[c] = true, a.Kind
		}
	}
}
