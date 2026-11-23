package cloudfw_test

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

const (
	gcpFirewall      = "google_compute_firewall."
	gcpWebToDB       = gcpFirewall + "allow_web_to_db"
	gcpRedisDeny     = gcpFirewall + "deny_web_redis"
	gcpBatchDNS      = gcpFirewall + "allow_batch_dns"
	gcpInstanceNet   = "network_interface[0].network"
	gcpMetricsAllow  = gcpFirewall + "allow_metrics_accounts priority 1000 allow, " + gcpImplied + "allow"
	gcpNotChecked    = "-"
	gcpWrongTypeNode = "google_compute_subnetwork.app"
)

// gcpAttr is one attribute of one resource of the fixture.
type gcpAttr struct {
	address string
	path    string
}

// gcpReviewScenarios are the regression cases of the review: typed references, attributes that are unknown until
// apply, and the ranges of the rule for the instance that it applies to.
func gcpReviewScenarios() []gcpScenario {
	return slices.Concat(gcpTypedScenarios(), gcpUnknownRuleScenarios(), gcpOwnRangeScenarios(), gcpDualStackScenarios())
}

// gcpDualStackScenarios are denies through address ranges for an instance that may have IPv6 addresses (review 2,
// finding G).
func gcpDualStackScenarios() []gcpScenario {
	mysqlOut := gcpFirewall + "deny_web_mysql_out priority 1000 deny"

	return []gcpScenario{
		{
			name: "finding G: egress deny for a dual-stack interface", from: gcpWeb, to: gcpDB, port: 3306,
			mutate: gcpStackType(gcpWeb, "IPV4_IPV6"), want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent,
			rules: mysqlOut,
		},
		{
			name: "finding G: egress deny for an interface that states IPV4_ONLY", from: gcpWeb, to: gcpDB, port: 3306,
			mutate: gcpStackType(gcpWeb, "IPV4_ONLY"), want: cloudfw.VerdictDenied, rules: mysqlOut,
		},
	}
}

// gcpStackType sets the stack type of the first network interface of an instance.
func gcpStackType(address, stack string) func(in *cloudfw.Input) {
	return gcpEdit(address, func(res *plan.Resource) {
		nic, _ := res.Values["network_interface"].([]any)[0].(map[string]any)
		nic["stack_type"] = stack
	})
}

// gcpTypedScenarios are references that name a resource of a type that the attribute cannot hold.
func gcpTypedScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "network reference to a resource that is not a network", from: gcpWeb, to: gcpDB, port: 5432,
			mutate: gcpAll(gcpSetReference(gcpAttr{gcpWeb, gcpInstanceNet}, gcpWrongTypeNode),
				gcpSetReference(gcpAttr{gcpDB, gcpInstanceNet}, gcpWrongTypeNode)),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapUnresolvedReference, rules: "",
		},
	}
}

// gcpUnknownRuleScenarios are firewall rules with an attribute that is unknown until apply.
func gcpUnknownRuleScenarios() []gcpScenario {
	scenarios := []gcpScenario{
		{
			name: "unknown ports of an allow rule", from: gcpBatch, port: 5353, protocol: cloudfw.ProtocolUDP,
			mutate: gcpUnknownField(gcpAttr{gcpBatchDNS, "allow"}, "ports"),
		},
		{
			name: "unknown protocol of an allow rule", from: gcpBatch, port: 53, protocol: cloudfw.ProtocolUDP,
			mutate: gcpUnknownField(gcpAttr{gcpBatchDNS, "allow"}, "protocol"),
		},
		{
			name: "unknown priority of a deny rule", from: gcpWeb, port: 6379,
			mutate: gcpUnknownTop(gcpAttr{gcpRedisDeny, "priority"}),
		},
		{
			name: "unknown direction of an egress deny rule", from: gcpWeb, port: 3306,
			mutate: gcpUnknownTop(gcpAttr{gcpFirewall + "deny_web_mysql_out", "direction"}),
		},
		{
			name: "unknown disabled flag of an allow-all rule", from: gcpWeb, port: 22,
			mutate: gcpUnknownTop(gcpAttr{gcpFirewall + "allow_all_disabled", "disabled"}),
		},
		{name: "unknown allow list", from: gcpWeb, port: 5432, mutate: gcpUnknownTop(gcpAttr{gcpWebToDB, "allow"})},
		{
			name: "unknown element of a deny list", from: gcpWeb, port: 6379,
			mutate: gcpUnknownElement(gcpAttr{gcpRedisDeny, "deny"}),
		},
		{name: "unknown source tags", from: gcpWeb, port: 5432, mutate: gcpUnknownTop(gcpAttr{gcpWebToDB, "source_tags"})},
	}

	for i := range scenarios {
		scenarios[i].to, scenarios[i].want = gcpDB, cloudfw.VerdictUnknown
		scenarios[i].gap, scenarios[i].rules = evaluate.GapUnknownAfterApply, gcpNotChecked
	}

	return scenarios
}

// gcpOwnRangeScenarios are rules whose ranges for the instance they apply to exclude it.
func gcpOwnRangeScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "ingress deny for another destination range does not apply", from: gcpWeb, to: gcpDB, port: 9060,
			want: cloudfw.VerdictAllowed, rules: gcpMetricsAllow,
		},
		{
			name: "egress deny for another source range does not apply", from: gcpWeb, to: gcpDB, port: 9070,
			want: cloudfw.VerdictAllowed, rules: gcpMetricsAllow,
		},
	}
}

// gcpAll applies the mutations in order.
func gcpAll(mutations ...func(in *cloudfw.Input)) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		for _, mutate := range mutations {
			mutate(in)
		}
	}
}

// gcpEdit changes the resource at the address.
func gcpEdit(address string, edit func(res *plan.Resource)) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) {
		for i := range in.Resources {
			if in.Resources[i].Address == address {
				edit(&in.Resources[i])
			}
		}
	}
}

// gcpSetReference makes the configuration of the attribute name other resources.
func gcpSetReference(attr gcpAttr, refs ...string) func(in *cloudfw.Input) {
	return gcpEdit(attr.address, func(res *plan.Resource) { res.References[attr.path] = refs })
}

// gcpUnknownTop makes a top-level attribute unknown until apply.
func gcpUnknownTop(attr gcpAttr) func(in *cloudfw.Input) {
	return gcpEdit(attr.address, func(res *plan.Resource) {
		delete(res.Values, attr.path)
		res.Unknown = append(res.Unknown, attr.path)
	})
}

// gcpUnknownField makes a field of the first block of the list unknown until apply.
func gcpUnknownField(list gcpAttr, field string) func(in *cloudfw.Input) {
	return gcpEdit(list.address, func(res *plan.Resource) {
		block, _ := res.Values[list.path].([]any)[0].(map[string]any)
		delete(block, field)
		res.Unknown = append(res.Unknown, list.path+"[0]."+field)
	})
}

// gcpUnknownElement makes the first element of the list unknown until apply; the plan reader leaves it out.
func gcpUnknownElement(list gcpAttr) func(in *cloudfw.Input) {
	return gcpEdit(list.address, func(res *plan.Resource) {
		res.Values[list.path] = []any{}
		res.Unknown = append(res.Unknown, list.path+"[0]")
	})
}
