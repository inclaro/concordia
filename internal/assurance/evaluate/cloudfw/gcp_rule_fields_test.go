package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/redact"
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

// gcpUnsetRangeScenarios are rules whose ranges the configuration does not set.
func gcpUnsetRangeScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "egress deny whose destination ranges the configuration does not set", from: gcpWeb,
			to: gcpDB, port: 3306, want: cloudfw.VerdictDenied, rules: gcpFirewall + "deny_web_mysql_out priority 1000 deny",
			mutate: gcpEdit(gcpFirewall+"deny_web_mysql_out", func(res *plan.Resource) {
				delete(res.Values, "destination_ranges")
				res.Unknown = append(res.Unknown, "destination_ranges")
				res.Configured = []string{"deny", "direction", "name", "network", "target_tags"}
			}),
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
		{name: "redacted target tags", from: gcpWeb, port: 5432, mutate: gcpRedactTop(gcpAttr{gcpWebToDB, "target_tags"})},
		{name: "redacted source tags", from: gcpWeb, port: 5432, mutate: gcpRedactTop(gcpAttr{gcpWebToDB, "source_tags"})},
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
		resources := in.Plans[0].Resources
		for i := range resources {
			if resources[i].Address == address {
				edit(&resources[i])
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

// gcpRedactTop puts the redaction marker that the plan reader writes for a sensitive value in place of a top-level
// attribute.
func gcpRedactTop(attr gcpAttr) func(in *cloudfw.Input) {
	return gcpEdit(attr.address, func(res *plan.Resource) { res.Values[attr.path] = redact.Marker(redact.ClassSecretData) })
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
