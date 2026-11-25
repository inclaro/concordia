package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// changedRuleTypes are the rule resources that the inline rule list of their security group or NSG repeats. In a
// plan against existing state the inline list is the computed copy of all rules: it still holds a rule whose
// resource the plan deletes, and the old values of a rule that the plan updates.
var changedRuleTypes = []string{
	awsGroupRule, "aws_vpc_security_group_ingress_rule", "aws_vpc_security_group_egress_rule", azureNSGRule,
}

// changesRule reports whether the resource is a rule resource that the plan deletes or updates.
func changesRule(res *plan.Resource) bool {
	if !slices.Contains(changedRuleTypes, res.Type) {
		return false
	}

	return slices.ContainsFunc(res.Actions, func(action string) bool { return action == "delete" || action == "update" })
}

// removesRule reports whether the plan deletes the rule resource, alone or as a part of a replacement. Its inline
// copy then belongs to the owner that it had before the change.
func removesRule(res *plan.Resource) bool { return slices.Contains(res.Actions, "delete") }

// priorOwner is the known value of an owner attribute of the rule resource before the change (plan.Resource.Prior).
func priorOwner(res *plan.Resource, key string) (string, bool) {
	value, ok := res.Prior[key]

	return value, ok
}

// changedOfType are the rule resources of the types that the plan deletes or updates.
func (e *Evaluator) changedOfType(types ...string) []*plan.Resource {
	var out []*plan.Resource

	for _, res := range e.changed {
		if slices.Contains(types, res.Type) {
			out = append(out, res)
		}
	}

	return out
}

// staleInlineGap is the gap of a match on an inline rule of the list of the owner that a changed rule resource may
// have made stale.
func staleInlineGap(owner *plan.Resource, list string) assurance.Gap {
	return assurance.Gap{
		Kind: evaluate.GapUnknownAfterApply, Detail: owner.Address + "." + list + " may repeat a rule that the plan changes",
	}
}
