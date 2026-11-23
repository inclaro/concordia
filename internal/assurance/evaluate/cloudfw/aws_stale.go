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

// awsInlineStale reports whether a changed rule resource of the direction may belong to the group: its owner is the
// group, or does not resolve (a deleted resource has no planned values).
func (e *Evaluator) awsInlineStale(group *plan.Resource, dir *awsDirection) bool {
	return slices.ContainsFunc(e.changedOfType(awsGroupRule, dir.vpcType), func(res *plan.Resource) bool {
		return awsChangeInDirection(res, dir) && e.awsMayOwn(res, group)
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

// awsMayOwn reports whether the group may be the owner of the rule resource.
func (e *Evaluator) awsMayOwn(rule, group *plan.Resource) bool {
	owner, ok := e.refOfType(rule, "security_group_id", &side{}, awsGroupTypes...)

	return !ok || owner == nil || owner == group
}

// staleMatch is maybe, with a gap, for a match on an inline rule that a changed rule resource may have made stale.
func (rule *awsRule) staleMatch(match tri, check *side) tri {
	if !rule.stale || match != triYes {
		return match
	}

	check.gaps = append(check.gaps, staleInlineGap(rule.owner, rule.element.list))

	return triMaybe
}
