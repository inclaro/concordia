package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// awsInlineRulesOf are the inline rules of the direction of a group, marked stale when a rule resource that the plan
// deletes or updates may belong to the group in that direction.
func (e *Evaluator) awsInlineRulesOf(group *plan.Resource, dir *awsDirection) []awsRule {
	rules := awsInlineRules(group, dir)
	if !e.awsInlineStale(group, dir) {
		return rules
	}

	for i := range rules {
		rules[i].stale = true
	}

	return rules
}

// awsInlineStale reports whether a changed rule resource of the direction may belong to the group: before the change
// or after it, its owner is the group or does not resolve.
func (e *Evaluator) awsInlineStale(group *plan.Resource, dir *awsDirection) bool {
	return slices.ContainsFunc(e.changedOfType(awsGroupRule, dir.vpcType), func(res *plan.Resource) bool {
		return e.awsMayOwn(res, group, dir)
	})
}

// awsChangeInDirection reports whether a changed rule resource may be a rule of the direction. An
// aws_security_group_rule whose `type` the plan does not give may be one of either.
func awsChangeInDirection(res *plan.Resource, dir *awsDirection) bool {
	if res.Type != awsGroupRule {
		return true
	}

	kind, ok := attrString(res, "type")

	return !ok || kind == dir.block
}

// awsMayOwn reports whether the group may be the owner of the rule resource in the direction: after the change
// through its planned `type` and `security_group_id`, and, for a resource that the plan deletes or replaces, before
// the change through the known ID that the plan gives for it. A deleted resource without that ID may belong to any
// group. The plan does not give the `type` before the change, and a new `type` replaces the resource, so before the
// change the rule may have been of either direction.
func (e *Evaluator) awsMayOwn(rule, group *plan.Resource, dir *awsDirection) bool {
	if e.awsMayOwnAfter(rule, group, dir) {
		return true
	}

	return removesRule(rule) && awsOwnedBefore(rule, group)
}

// awsMayOwnAfter reports whether the rule resource exists after the change, may be a rule of the direction, and may
// belong to the group.
func (e *Evaluator) awsMayOwnAfter(rule, group *plan.Resource, dir *awsDirection) bool {
	if rule.Values == nil || !awsChangeInDirection(rule, dir) {
		return false
	}

	return e.awsOwnedAfter(rule, group)
}

// awsOwnedAfter reports whether the group may be the owner of the rule resource after the change. When the planned
// `security_group_id` and the ID of the group are both known, they decide. Otherwise the owner may be the group when
// `security_group_id` does not resolve, is absent, or names the group.
func (e *Evaluator) awsOwnedAfter(rule, group *plan.Resource) bool {
	ownerID, ownerKnown := attrString(rule, "security_group_id")
	if groupID, groupKnown := attrString(group, attrID); ownerKnown && groupKnown {
		return ownerID == groupID
	}

	owner, ok := e.refOfType(rule, "security_group_id", &side{}, awsGroupTypes...)

	return !ok || owner == nil || owner == group
}

// awsOwnedBefore reports whether the group may be the owner of the rule resource before the change: the owner ID is
// not known, the ID of the group is not known, or the two are the same.
func awsOwnedBefore(rule, group *plan.Resource) bool {
	ownerID, known := priorOwner(rule, "security_group_id")
	if !known {
		return true
	}

	groupID, hasID := attrString(group, attrID)

	return !hasID || groupID == ownerID
}

// staleMatch is maybe, with a gap, for a match on an inline rule that a changed rule resource may have made stale.
func (rule *awsRule) staleMatch(match tri, check *side) tri {
	if !rule.stale || match != triYes {
		return match
	}

	check.gaps = append(check.gaps, staleInlineGap(rule.owner, rule.element.list))

	return triMaybe
}
