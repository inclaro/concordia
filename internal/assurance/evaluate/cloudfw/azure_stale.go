package cloudfw

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// azureFreshInline are the inline rules of the NSG without those that a rule resource of the NSG repeats by name:
// the planned values of the resource win over the copy in the list. The rules are marked stale when a rule resource
// that the plan deletes or updates may belong to the NSG.
func (e *Evaluator) azureFreshInline(nsg *plan.Resource, standalone []azureRule) []azureRule {
	names := azureOwnedNames(standalone)
	stale := e.azureNSGStale(nsg)

	inline := slices.DeleteFunc(azureInlineRules(nsg), func(rule azureRule) bool {
		name, ok := attrString(rule.res, rule.base+"name")

		return ok && slices.Contains(names, name)
	})

	for i := range inline {
		inline[i].stale = stale
	}

	return inline
}

// azureOwnedNames are the known names of the rule resources that surely belong to the NSG.
func azureOwnedNames(standalone []azureRule) []string {
	var names []string

	for _, rule := range standalone {
		if name, ok := attrString(rule.res, "name"); ok && rule.owned == triYes {
			names = append(names, name)
		}
	}

	return names
}

// azureNSGStale reports whether a rule resource that the plan deletes or updates may belong to the NSG.
func (e *Evaluator) azureNSGStale(nsg *plan.Resource) bool {
	return slices.ContainsFunc(e.changedOfType(azureNSGRule), func(res *plan.Resource) bool {
		return e.azureChangedOwner(res, nsg) != triNo
	})
}

// azureChangedOwner is yes or maybe when the changed rule resource may belong to the NSG: after the change through
// its planned owner, and, for a resource that the plan deletes or replaces, before the change through the known NSG
// name and resource group that the plan gives for it. A deleted resource without them may belong to any NSG.
func (e *Evaluator) azureChangedOwner(res, nsg *plan.Resource) tri {
	owner := triNo
	if res.Values != nil {
		owner = e.azureRuleOwner(res, nsg, &side{})
	}

	if !removesRule(res) {
		return owner
	}

	name, hasName := priorOwner(res, "network_security_group_name")
	group, hasGroup := priorOwner(res, "resource_group_name")

	if !hasName || !hasGroup {
		return triMaybe
	}

	return or(owner, e.azureOwnerByName(nsg, name, group, &side{}))
}

// decision is the result of the first rule that matches: its access, or maybe when the match is uncertain, when the
// rule is an inline copy that the plan may have changed, or when an IPv4 deny decided and the plan does not prove
// that an endpoint is IPv4-only.
func (r *azureRule) decision(pair *azurePair, matched tri, report *side) tri {
	switch {
	case matched != triYes:
		return matched
	case r.stale:
		report.gaps = append(report.gaps, staleInlineGap(r.res, azureRulesAttr))

		return triMaybe
	case r.unprovenDeny(pair):
		report.gap(evaluate.GapAddressDependent, fmt.Sprintf(
			"%s %s: an IPv4 deny decided, and the plan does not show that either endpoint is IPv4-only", r.address, r.label()))

		return triMaybe
	}

	return r.access()
}
