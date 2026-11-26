package cloudfw_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

func TestAWSSecurityGroupSemantics(t *testing.T) {
	t.Parallel()

	ev := awsEvaluator(awsReadPlan(t, awsFixtureBytes(t)))

	for _, sc := range slices.Concat(awsReferenceScenarios(), awsPeerScenarios(), awsEndpointScenarios()) {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			res := ev.Evaluate(awsQuery(&sc))
			awsAssert(t, &sc, &res)
		})
	}
}

// awsReferenceScenarios are group references with known IDs and IDs that the configuration resolves.
func awsReferenceScenarios() []awsScenario {
	return []awsScenario{
		{
			name: "group reference allows the port", from: "aws_instance.app", to: "aws_elasticache_cluster.cache", port: 6379,
			want: cloudfw.VerdictAllowed, rules: "aws_security_group.app egress[0], aws_security_group.cache ingress[0]",
		},
		{
			name: "no rule allows the port", from: "aws_instance.app", to: "aws_elasticache_cluster.cache", port: 5432,
			want: cloudfw.VerdictDenied,
		},
		{
			name: "protocol of the rule must match", from: "aws_instance.app", to: "aws_elasticache_cluster.cache",
			protocol: cloudfw.ProtocolUDP, port: 6379, want: cloudfw.VerdictDenied,
		},
		{
			name: "group reference unknown after apply resolved by configuration",
			from: "aws_lb.public", to: "aws_db_instance.ledger", port: 5432, want: cloudfw.VerdictAllowed,
			rules: "aws_vpc_security_group_egress_rule.gateway_out rule, " +
				"aws_vpc_security_group_ingress_rule.ledger_from_gateway rule",
		},
		{
			name: "standalone rule allows only its port", from: "aws_lb.public", to: "aws_db_instance.ledger", port: 22,
			want: cloudfw.VerdictDenied,
		},
		{
			name: "data source group is unresolved", from: "aws_instance.legacy", to: "aws_db_instance.ledger", port: 5432,
			want: cloudfw.VerdictUnknown, reason: awsIncomplete,
			gaps: "unresolved-reference: data.aws_security_group.shared is a data source; its rules are not in the plan",
		},
		{
			name: "inline rules unknown after apply and no rule allows", from: "aws_instance.batch",
			to: "aws_instance.dynamic", port: 9090, want: cloudfw.VerdictUnknown, reason: awsIncomplete,
			rules: "aws_security_group.dynamic ingress",
			gaps:  "unknown-after-apply: aws_security_group.dynamic.ingress",
		},
		{
			name: "inline rules unknown after apply and a rule resource allows", from: "aws_instance.batch",
			to: "aws_instance.dynamic", port: 8080, want: cloudfw.VerdictAllowed,
			rules: "aws_security_group.batch egress[0], aws_vpc_security_group_ingress_rule.dynamic_from_batch rule",
		},
	}
}

// awsPeerScenarios are rules that match the peer by CIDR, by `self`, or by a prefix list.
func awsPeerScenarios() []awsScenario {
	return []awsScenario{
		{
			name: "CIDR that overlaps the source subnet partly", from: "aws_instance.batch", to: "aws_db_instance.ledger",
			port: 5432, want: cloudfw.VerdictUnknown, reason: awsUnsupported,
			rules: "aws_security_group_rule.ledger_from_batch rule",
			gaps:  "address-dependent-range: 10.20.3.0/25 covers only part of 10.20.3.0/24",
		},
		{
			name: "open ingress still needs an egress rule at the source", from: "aws_instance.app", to: "aws_lb.public",
			port: 443, want: cloudfw.VerdictDenied,
		},
		{
			name: "prefix list is unsupported", from: "aws_instance.reporter", to: "aws_lb.public", port: 443,
			want: cloudfw.VerdictUnknown, reason: awsUnsupported,
			rules: "aws_security_group.reporter egress[0]", gaps: "unsupported-policy-kind: prefix list",
		},
		{
			name: "self rule allows members of the same group", from: "aws_instance.worker_a", to: "aws_instance.worker_b",
			port: 7000, want: cloudfw.VerdictAllowed,
			rules: "aws_security_group.workers egress[0], aws_security_group.workers ingress[0]",
		},
		{
			name: "self rule does not allow other groups", from: "aws_instance.existing_app", to: "aws_instance.worker_b",
			port: 7000,
			want: cloudfw.VerdictDenied,
		},
	}
}

// awsEndpointScenarios are endpoints bound directly to a group, and endpoint types the evaluator does not support.
func awsEndpointScenarios() []awsScenario {
	return []awsScenario{
		{
			name: "security group bound directly", from: "aws_security_group.app", to: "aws_security_group.cache",
			port: 6379, want: cloudfw.VerdictAllowed,
			rules: "aws_security_group.app egress[0], aws_security_group.cache ingress[0]",
		},
		{
			name: "unsupported endpoint type", from: "aws_instance.app", to: "aws_s3_bucket.archive", port: 443,
			want: cloudfw.VerdictUnknown, reason: awsUnsupported,
			gaps: "unsupported-resource-type: aws_s3_bucket.archive: resource type aws_s3_bucket is not supported",
		},
	}
}

// TestAWSPlanVariants checks queries against changed copies of the fixture plan.
func TestAWSPlanVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(t *testing.T, data []byte) *plan.Plan
		awsScenario
	}{
		{
			mutate: awsIncompletePlan,
			awsScenario: awsScenario{
				name: "plan with complete false", from: "aws_lb.public", to: "aws_db_instance.ledger", port: 5432,
				want: cloudfw.VerdictUnknown, reason: awsIncomplete,
				gaps: `incomplete-plan: the plans of the environment have coverage "partial"`,
			},
		},
		{
			mutate: awsWithoutGatewayRule,
			awsScenario: awsScenario{
				name: "removing the gateway rule denies the database port", from: "aws_lb.public",
				to: "aws_db_instance.ledger", port: 5432, want: cloudfw.VerdictDenied,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := awsEvaluator(tt.mutate(t, awsFixtureBytes(t))).Evaluate(awsQuery(&tt.awsScenario))
			awsAssert(t, &tt.awsScenario, &res)
		})
	}
}

// awsIncompletePlan reads the fixture with `complete: false`.
func awsIncompletePlan(t *testing.T, data []byte) *plan.Plan {
	t.Helper()

	changed := bytes.Replace(data, []byte(`"complete": true`), []byte(`"complete": false`), 1)
	if bytes.Equal(changed, data) {
		t.Fatal("fixture has no complete flag")
	}

	return awsReadPlan(t, changed)
}

// awsWithoutGatewayRule reads the fixture without the rule that opens the database to the gateway group.
func awsWithoutGatewayRule(t *testing.T, data []byte) *plan.Plan {
	t.Helper()

	p := awsReadPlan(t, data)
	p.Resources = slices.DeleteFunc(p.Resources, func(r plan.Resource) bool {
		return r.Address == "aws_vpc_security_group_ingress_rule.ledger_from_gateway"
	})

	return p
}
