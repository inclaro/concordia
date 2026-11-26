package cloudfw_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// TestAWSEvidenceNotRead checks that a group, a rule, or an attachment that the evaluator cannot attribute makes the
// result unknown instead of allowed or denied, and that groups attached outside the endpoint count.
func TestAWSEvidenceNotRead(t *testing.T) {
	t.Parallel()

	ev := awsEvaluator(awsReadPlan(t, awsFixtureBytes(t)))

	for _, sc := range slices.Concat(awsInlineBlockScenarios(), awsAttachmentScenarios(), awsExistingStateScenarios()) {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			res := ev.Evaluate(awsQuery(&sc))
			awsAssert(t, &sc, &res)
		})
	}
}

// awsInlineBlockScenarios are inline blocks whose order or elements the plan does not give.
func awsInlineBlockScenarios() []awsScenario {
	return []awsScenario{
		{
			name: "inline blocks in another order than the configuration", from: "aws_security_group.f1_app",
			to: "aws_security_group.f1_db", port: 22, want: cloudfw.VerdictUnknown, reason: awsIncomplete,
			rules: "aws_security_group.f1_db ingress[0]",
			gaps: "unresolved-reference: aws_security_group.f1_db.ingress[*].security_groups: the blocks name different " +
				"resources, and the plan does not keep the configuration order of the blocks",
		},
		{
			name: "inline block list with an element unknown after apply", from: "aws_security_group.f1_app",
			to: "aws_security_group.f1_partial", port: 22, want: cloudfw.VerdictUnknown, reason: awsIncomplete,
			rules: "aws_security_group.f1_partial ingress",
			gaps:  "unknown-after-apply: aws_security_group.f1_partial.ingress",
		},
		{
			name: "group reference to a resource that is not a security group", from: "aws_instance.f2_default",
			to: "aws_lb.public", port: 443, want: cloudfw.VerdictUnknown, reason: awsIncomplete,
			gaps: "unresolved-reference: aws_instance.f2_default.vpc_security_group_ids names aws_vpc.main, a aws_vpc, " +
				"not one of aws_security_group, aws_default_security_group; unresolved-reference: " +
				"aws_vpc.main.default_security_group_id is an attribute that does not identify its resource; the " +
				"resource it names is not known from the plan",
		},
	}
}

// awsAttachmentScenarios are groups that an aws_network_interface_sg_attachment adds to an instance.
func awsAttachmentScenarios() []awsScenario {
	return []awsScenario{
		{
			name: "group attached to the primary network interface", from: "aws_instance.f8", to: "aws_lb.public",
			port: 443, want: cloudfw.VerdictAllowed,
			rules: "aws_security_group.f8_extra egress[0], aws_vpc_security_group_ingress_rule.gateway_https rule",
		},
		{
			name: "group of a network interface attached to the instance", from: "aws_instance.f8b", to: "aws_lb.public",
			port: 443, want: cloudfw.VerdictAllowed,
			rules: "aws_security_group.f8_extra egress[0], aws_vpc_security_group_ingress_rule.gateway_https rule",
		},
	}
}

// awsExistingStateScenarios are a plan against existing state: known IDs, no-op actions, and an inline rule that a
// rule resource repeats.
func awsExistingStateScenarios() []awsScenario {
	return []awsScenario{
		{
			name: "existing state: inline rule and rule resource both allow", from: "aws_instance.existing_app",
			to: "aws_db_instance.existing", port: 5432, want: cloudfw.VerdictAllowed,
			rules: "aws_security_group.existing_app egress[0], aws_security_group.existing_db ingress[0], " +
				"aws_security_group_rule.existing_db_from_app rule",
		},
		{
			name: "existing state: other port denied", from: "aws_instance.existing_app", to: "aws_db_instance.existing",
			port: 22, want: cloudfw.VerdictDenied,
		},
	}
}

// TestAWSUnresolvedAttachment checks that an aws_network_interface_sg_attachment whose network interface does not
// resolve makes a closed side of an instance unknown: it may attach a group that allows the traffic.
func TestAWSUnresolvedAttachment(t *testing.T) {
	t.Parallel()

	data := awsWithChange(t, awsFixtureBytes(t), map[string]any{
		"address": "aws_network_interface_sg_attachment.unknown", "mode": "managed",
		"type": "aws_network_interface_sg_attachment", "name": "unknown",
		"change": map[string]any{
			"actions": []any{"create"}, "after": map[string]any{"security_group_id": "sg-reporter"},
			"after_unknown": map[string]any{"id": true, "network_interface_id": true},
		},
	})
	sc := awsScenario{
		name: "unresolved attachment", from: "aws_instance.app", to: "aws_lb.public", port: 443,
		want: cloudfw.VerdictUnknown, reason: awsIncomplete,
		rules: "aws_network_interface_sg_attachment.unknown attachment",
		gaps: "unknown-after-apply: aws_network_interface_sg_attachment.unknown.network_interface_id; " +
			"unresolved-reference: aws_network_interface_sg_attachment.unknown.network_interface_id does not resolve, " +
			"so it may attach a security group to aws_instance.app",
	}

	res := awsEvaluator(awsReadPlan(t, data)).Evaluate(awsQuery(&sc))
	awsAssert(t, &sc, &res)
}

// awsWithChange is the fixture plan with one more resource change.
func awsWithChange(t *testing.T, data []byte, change map[string]any) []byte {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	changes, _ := doc["resource_changes"].([]any)
	doc["resource_changes"] = append(changes, change)

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// awsResourceOf finds a resource of a plan, for tests that check what the plan reader gives.
func awsResourceOf(p *plan.Plan, address string) *plan.Resource {
	for i := range p.Resources {
		if p.Resources[i].Address == address {
			return &p.Resources[i]
		}
	}

	return nil
}

// TestAWSFixtureShapes checks that the plan reader gives the fixture shapes that the scenarios rely on.
func TestAWSFixtureShapes(t *testing.T) {
	t.Parallel()

	p := awsReadPlan(t, awsFixtureBytes(t))

	partial := awsResourceOf(p, "aws_security_group.f1_partial")
	if partial == nil || !slices.Contains(partial.Unknown, "ingress[0]") {
		t.Fatalf("f1_partial unknown = %v", partial)
	}

	db := awsResourceOf(p, "aws_security_group.f1_db")
	if db == nil || !slices.Equal(db.References["ingress[0].security_groups"], []string{"aws_security_group.f1_app"}) {
		t.Fatalf("f1_db references = %v", db)
	}
}
