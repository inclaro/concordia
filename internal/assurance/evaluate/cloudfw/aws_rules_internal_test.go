package cloudfw

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// TestChangedRuleResourceMirroredInline: a rule resource that the plan deletes still has a copy in the
// inline list of its group, so the copy must not allow.
func TestChangedRuleResourceMirroredInline(t *testing.T) {
	t.Parallel()

	inline := func(actions []string, values map[string]any) []plan.Resource {
		rule := resourceOf("aws_security_group_rule.db_from_app", "aws_security_group_rule", values)
		rule.Actions = actions

		return []plan.Resource{
			group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
			group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"security_groups": []any{idApp}})}, []any{}),
			rule,
		}
	}
	unchanged := map[string]any{
		"type": "ingress", "security_group_id": idDB, "protocol": "tcp", "from_port": "5432", "to_port": "5432",
		"source_security_group_id": idApp,
	}

	runQueryCases(t, []queryCase{
		{
			name: "deleted rule resource", resources: inline([]string{"delete"}, nil), from: awsAppGroup, to: awsDBGroup,
			want: VerdictUnknown,
			gaps: []string{evaluate.GapUnknownAfterApply + ": " + awsDBGroup + ".ingress may repeat a rule that the plan changes"},
		},
		{
			name: "unchanged rule resource", resources: inline([]string{"no-op"}, unchanged), from: awsAppGroup, to: awsDBGroup,
			want: VerdictAllowed,
		},
	})
}

// TestDeletedRuleTaintsOnlyItsOwner: a rule resource that the plan deletes or replaces makes the
// inline list of the group that owned it before the change stale, and no other group. Without a known owner before
// the change, every group may hold it.
func TestDeletedRuleTaintsOnlyItsOwner(t *testing.T) {
	t.Parallel()

	removed := func(actions []string, values map[string]any, prior map[string]string) []plan.Resource {
		rule := resourceOf("aws_security_group_rule.old", "aws_security_group_rule", values)
		rule.Actions, rule.Prior = actions, prior

		return []plan.Resource{
			group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
			group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"security_groups": []any{idApp}})}, []any{}),
			group(awsExtraGroup, idExtra, []any{}, []any{}),
			rule,
		}
	}
	stale := []string{evaluate.GapUnknownAfterApply + ": " + awsDBGroup + ".ingress may repeat a rule that the plan changes"}
	elsewhere := map[string]any{"type": "ingress", "security_group_id": idExtra, "protocol": "tcp"}
	deleted := []string{"delete"}
	replaced := []string{"delete", "create"}

	runQueryCases(t, []queryCase{
		{
			name: "deleted from another group", resources: removed(deleted, nil, map[string]string{"security_group_id": idExtra}),
			from: awsAppGroup, to: awsDBGroup, want: VerdictAllowed,
		},
		{
			name: "deleted from the group", resources: removed(deleted, nil, map[string]string{"security_group_id": idDB}),
			from: awsAppGroup, to: awsDBGroup, want: VerdictUnknown, gaps: stale,
		},
		{
			name: "deleted without a known owner", resources: removed(deleted, nil, nil),
			from: awsAppGroup, to: awsDBGroup, want: VerdictUnknown, gaps: stale,
		},
		{
			name:      "replaced, moving from the group to another",
			resources: removed(replaced, elsewhere, map[string]string{"security_group_id": idDB}),
			from:      awsAppGroup, to: awsDBGroup, want: VerdictUnknown, gaps: stale,
		},
		{
			name:      "replaced within another group",
			resources: removed(replaced, elsewhere, map[string]string{"security_group_id": idExtra}),
			from:      awsAppGroup, to: awsDBGroup, want: VerdictAllowed,
		},
	})
}

// TestRuleResourceWithUnknownType: an aws_security_group_rule whose `type` is unknown
// until apply may be a rule of either direction.
func TestRuleResourceWithUnknownType(t *testing.T) {
	t.Parallel()

	rule := resourceOf("aws_security_group_rule.any", awsGroupRule, map[string]any{
		"security_group_id": idDB, "protocol": "tcp", "from_port": "5432", "to_port": "5432", "cidr_blocks": []any{"0.0.0.0/0"},
	})
	rule.Unknown = []string{"type"}

	runQueryCases(t, []queryCase{{
		name: "unknown type", from: awsAppGroup, to: awsDBGroup, want: VerdictUnknown,
		resources: []plan.Resource{
			group(awsAppGroup, idApp, []any{}, []any{allTraffic()}), group(awsDBGroup, idDB, []any{}, []any{}), rule,
		},
		gaps: []string{evaluate.GapUnknownAfterApply + ": aws_security_group_rule.any.type"},
	}})
}

// TestInlineRuleWithUnknownSelf: an inline rule whose `self` is unknown until apply may
// select its own group.
func TestInlineRuleWithUnknownSelf(t *testing.T) {
	t.Parallel()

	db := group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{})}, []any{allTraffic()})
	db.Unknown = []string{"ingress[0].self"}

	runQueryCases(t, []queryCase{{
		name: "unknown self", resources: []plan.Resource{db}, from: awsDBGroup, to: awsDBGroup, want: VerdictUnknown,
		gaps: []string{evaluate.GapUnknownAfterApply + ": " + awsDBGroup + ".ingress[0].self"},
	}})
}

// TestChangedRuleOwnerAndDirection: a rule resource that the plan replaces with a new `type` may have been a rule of
// the other direction of its old group, whose inline list may still copy it; a changed rule whose known group ID is
// another group's does not make the inline list of this group stale.
func TestChangedRuleOwnerAndDirection(t *testing.T) {
	t.Parallel()

	world := func(actions []string, values map[string]any, prior map[string]string) []plan.Resource {
		rule := resourceOf("aws_security_group_rule.moved", awsGroupRule, values)
		rule.Actions, rule.Prior = actions, prior

		return []plan.Resource{
			group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
			group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"security_groups": []any{idApp}})}, []any{}),
			rule,
		}
	}
	egress := map[string]any{"type": "egress", "security_group_id": idDB, "protocol": "tcp"}
	elsewhere := map[string]any{"type": "ingress", "security_group_id": "sg-other", "protocol": "tcp"}

	runQueryCases(t, []queryCase{
		{
			name:      "replaced from ingress to egress of the group",
			resources: world([]string{"delete", "create"}, egress, map[string]string{"security_group_id": idDB}),
			from:      awsAppGroup, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnknownAfterApply + ": " + awsDBGroup + ".ingress may repeat a rule that the plan changes"},
		},
		{
			name:      "updated rule of a group outside the plan",
			resources: world([]string{"update"}, elsewhere, nil),
			from:      awsAppGroup, to: awsDBGroup, want: VerdictAllowed,
		},
	})
}
