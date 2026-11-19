package render

import "slices"

// computeSelections resolves the Service selectors and the NetworkPolicy pod selectors against the rendered
// workloads of the same namespace and environment. It reads labels from the rendered documents only, so it does
// not depend on which directory a file came from and does not need a code-graph edge (design D3, CBM O6). The
// result is a fact about selection. It never says that a workload is isolated or exposed.
//
// A Service with no selector selects nothing and has no fact. An empty NetworkPolicy pod selector selects every
// pod of the namespace and records Empty. A selector with unsupported syntax gives an Unsupported fact (the gap
// itself is recorded when the document is read), never an empty target list.
func computeSelections(resources []Resource) []Selection {
	var out []Selection

	for i := range resources {
		res := &resources[i]

		switch {
		case res.Service != nil && len(res.Service.Selector) > 0:
			sel := LabelSelector{MatchLabels: res.Service.Selector}
			out = append(out, Selection{
				Selector: res.ID, Kind: SelectionService, Targets: selectWorkloads(resources, res.ID.Namespace, sel),
			})
		case res.NetworkPolicy != nil:
			np := res.NetworkPolicy
			if len(np.Unsupported) > 0 {
				// The gap itself was recorded when the document was read.
				out = append(out, Selection{Selector: res.ID, Kind: SelectionNetworkPolicy, Unsupported: true})

				continue
			}

			out = append(out, Selection{
				Selector: res.ID, Kind: SelectionNetworkPolicy, Empty: np.PodSelector.IsEmpty(),
				Targets: selectWorkloads(resources, res.ID.Namespace, np.PodSelector),
			})
		}
	}

	slices.SortStableFunc(out, func(a, b Selection) int { return CompareResourceIDs(a.Selector, b.Selector) })

	return out
}

// selectWorkloads gives the identities of the workloads of the namespace whose pod labels match the selector.
func selectWorkloads(resources []Resource, namespace string, sel LabelSelector) []ResourceID {
	var targets []ResourceID

	for i := range resources {
		w := &resources[i]
		if w.Workload == nil || w.ID.Namespace != namespace {
			continue
		}

		if sel.Matches(w.Workload.PodLabels) {
			targets = append(targets, w.ID)
		}
	}

	slices.SortFunc(targets, CompareResourceIDs)

	return targets
}
