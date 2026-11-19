package observation

import "go.yaml.in/yaml/v3"

// selectorShape is what the cross-check knows about a selector: never its labels.
type selectorShape struct {
	empty       bool
	expressions bool
	labels      bool
}

func shapeOf(sel *yaml.Node) (selectorShape, bool) {
	if sel == nil || sel.Kind != yaml.MappingNode {
		return selectorShape{}, false
	}

	_, exprs := field(sel, "matchExpressions")
	_, labels := field(sel, "matchLabels")

	return selectorShape{
		empty:       len(sel.Content) == 0,
		expressions: exprs != nil && len(exprs.Content) > 0,
		labels:      labels != nil && labels.Kind == yaml.MappingNode && len(labels.Content) > 0,
	}, true
}

// isNonEmptyMapping says whether a node is a mapping with at least one key.
func isNonEmptyMapping(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.MappingNode && len(n.Content) > 0
}

// selectorSite is a place where a selector is, with the line of its key.
type selectorSite struct {
	node *yaml.Node
	line int
	// join says that the provider is expected to give a selection edge for this selector.
	join bool
}

func selectorSites(doc *manifestDoc) []selectorSite {
	spec := fieldVal(doc.root, "spec")

	switch doc.kind {
	case kindNetPolicy:
		return networkPolicySites(doc, spec)
	case kindPDB:
		if k, v := field(spec, "selector"); v != nil {
			return []selectorSite{{node: v, line: doc.line(k), join: true}}
		}
	case kindService:
		if k, v := field(spec, "selector"); isNonEmptyMapping(v) {
			return []selectorSite{{node: v, line: doc.line(k), join: true}}
		}
	}

	return nil
}

func networkPolicySites(doc *manifestDoc, spec *yaml.Node) []selectorSite {
	var sites []selectorSite

	if k, v := field(spec, "podSelector"); v != nil {
		sites = append(sites, selectorSite{node: v, line: doc.line(k), join: true})
	}

	for _, dir := range []struct{ rules, peers string }{{"ingress", "from"}, {"egress", "to"}} {
		for _, rule := range items(fieldVal(spec, dir.rules)) {
			for _, peer := range items(fieldVal(rule, dir.peers)) {
				sites = appendPeerSites(sites, doc, peer)
			}
		}
	}

	return sites
}

// appendPeerSites adds the pod and namespace selectors of one network policy peer.
func appendPeerSites(sites []selectorSite, doc *manifestDoc, peer *yaml.Node) []selectorSite {
	for _, key := range []string{"podSelector", "namespaceSelector"} {
		if k, v := field(peer, key); v != nil {
			sites = append(sites, selectorSite{node: v, line: doc.line(k)})
		}
	}

	return sites
}

func (c *crossChecker) selectorGaps(doc *manifestDoc, env string, base *CoverageGap) {
	nodes := c.provider.resources(doc.path, doc.kind, doc.name, env)
	selected := c.provider.isSelected(nodes)

	for _, site := range selectorSites(doc) {
		gap := *base
		gap.Line = site.line

		if !fillSelectorGap(&gap, doc, &site, len(nodes) > 0 && !selected) {
			continue
		}

		c.gaps = append(c.gaps, gap)
	}
}

// fillSelectorGap sets the reason of the gap that a selector gives. It gives false when the selector gives no gap.
// unjoined says that the provider has the document and gives no selection edge for it.
func fillSelectorGap(gap *CoverageGap, doc *manifestDoc, site *selectorSite, unjoined bool) bool {
	shape, ok := shapeOf(site.node)
	if ok {
		if reason := shapeLoss(shape); reason != "" {
			gap.Reason = reason

			return true
		}
	}

	if !site.join || !unjoined || (doc.kind != kindService && !shape.labels) {
		return false
	}

	gap.Reason = LossSelectorJoinDirectoryScoped
	gap.Detail = "selector not joined; causes not excluded: target in another directory, competing declaration, undeclared target"

	return true
}

// shapeLoss gives the loss of a selector by its shape alone, "" when the shape gives none.
func shapeLoss(shape selectorShape) string {
	switch {
	case shape.empty:
		return LossEmptySelector
	case shape.expressions:
		return LossUnsupportedSelectorExpression
	}

	return ""
}
