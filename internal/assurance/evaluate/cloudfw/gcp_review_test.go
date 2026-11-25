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
	return slices.Concat(gcpTypedScenarios(), gcpUnknownRuleScenarios(), gcpOwnRangeScenarios(), gcpDualStackScenarios(),
		gcpIPv6SettingScenarios())
}

// gcpDualStackScenarios are denies through address ranges: they decide only when the plan proves that an endpoint is
// IPv4-only (review 2, finding G; review 4, step 4). The fixture states IPV4_ONLY on every interface and subnetwork.
func gcpDualStackScenarios() []gcpScenario {
	mysqlOut := gcpFirewall + "deny_web_mysql_out priority 1000 deny"
	unknownStacks := gcpAll(gcpUnknownField(gcpAttr{gcpWeb, gcpInterfaces}, gcpStack),
		gcpUnknownField(gcpAttr{gcpDB, gcpInterfaces}, gcpStack))
	absentStacks := gcpAll(gcpStackType(gcpWeb, ""), gcpStackType(gcpDB, ""))
	configuredStacks := gcpInstancesConfigured(gcpStaticStack)
	unsetStacks := gcpInstancesConfigured(gcpStaticNoStack)
	dynamicInterfaces := gcpInstancesConfigured(gcpNoStaticInterface)

	return []gcpScenario{
		{
			name: "finding G: egress deny for two dual-stack interfaces", from: gcpWeb, to: gcpDB, port: 3306,
			mutate: gcpAll(gcpStackType(gcpWeb, "IPV4_IPV6"), gcpStackType(gcpDB, "IPV4_IPV6")),
			want:   cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
		{
			name: "egress deny for a dual-stack interface and an IPV4_ONLY peer", from: gcpWeb, to: gcpDB, port: 3306,
			mutate: gcpStackType(gcpWeb, "IPV4_IPV6"), want: cloudfw.VerdictDenied, rules: mysqlOut,
		},
		{
			name: "finding G: egress deny for interfaces that state IPV4_ONLY", from: gcpWeb, to: gcpDB, port: 3306,
			want: cloudfw.VerdictDenied, rules: mysqlOut,
		},
		{
			name: "egress deny for stack types that the configuration sets to values unknown until apply", from: gcpWeb,
			to: gcpDB, port: 3306, mutate: gcpAll(unknownStacks, configuredStacks),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
		{
			name: "egress deny for stack types unknown because the configuration does not set them", from: gcpWeb,
			to: gcpDB, port: 3306, mutate: gcpAll(unknownStacks, unsetStacks), want: cloudfw.VerdictDenied, rules: mysqlOut,
		},
		{
			name: "egress deny for unknown stack types of interfaces that the configuration does not show", from: gcpWeb,
			to: gcpDB, port: 3306, mutate: gcpAll(unknownStacks, dynamicInterfaces),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
		{
			name: "egress deny for absent interface stack types and IPV4_ONLY subnetworks", from: gcpWeb, to: gcpDB,
			port: 3306, mutate: absentStacks, want: cloudfw.VerdictDenied, rules: mysqlOut,
		},
		{
			name: "egress deny for absent interface stack types and configured unknown subnetwork stack types",
			from: gcpWeb, to: gcpDB, port: 3306, want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent,
			rules: mysqlOut, mutate: gcpAll(absentStacks, gcpUnknownSubnetStacks(true)),
		},
		{
			name: "egress deny for absent interface stack types and unset subnetwork stack types", from: gcpWeb,
			to: gcpDB, port: 3306, want: cloudfw.VerdictDenied, rules: mysqlOut,
			mutate: gcpAll(absentStacks, gcpUnknownSubnetStacks(false)),
		},
	}
}

// gcpIPv6SettingScenarios are unset stack types next to an IPv6 setting: the default IPV4_ONLY then does not hold
// (review 4, limits to record).
func gcpIPv6SettingScenarios() []gcpScenario {
	mysqlOut := gcpFirewall + "deny_web_mysql_out priority 1000 deny"
	unknownStacks := gcpAll(gcpUnknownField(gcpAttr{gcpWeb, gcpInterfaces}, gcpStack),
		gcpUnknownField(gcpAttr{gcpDB, gcpInterfaces}, gcpStack))
	absentStacks := gcpAll(gcpStackType(gcpWeb, ""), gcpStackType(gcpDB, ""))

	return []gcpScenario{
		{
			name: "egress deny for unset stack types of interfaces that configure ipv6_access_type", from: gcpWeb,
			to: gcpDB, port: 3306, mutate: gcpAll(unknownStacks, gcpInstancesConfigured(gcpStaticIPv6Access)),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
		{
			name: "egress deny for unset stack types of interfaces with an ipv6_access_config block", from: gcpWeb,
			to: gcpDB, port: 3306,
			mutate: gcpAll(unknownStacks, gcpInstancesConfigured(gcpStaticNoStack), gcpIPv6AccessConfigs),
			want:   cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
		{
			name: "egress deny for unset subnetwork stack types with an IPv6 access type", from: gcpWeb, to: gcpDB,
			port: 3306, mutate: gcpAll(absentStacks, gcpUnknownSubnetStacks(false), gcpSubnetsIPv6Access),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
	}
}

// gcpIPv6AccessConfigs gives the first interface of the web and db instances an external IPv6 access block.
func gcpIPv6AccessConfigs(in *cloudfw.Input) {
	set := func(res *plan.Resource) {
		nic, _ := res.Values[gcpInterfaces].([]any)[0].(map[string]any)
		nic["ipv6_access_config"] = []any{map[string]any{"network_tier": "PREMIUM"}}
	}

	gcpAll(gcpEdit(gcpWeb, set), gcpEdit(gcpDB, set))(in)
}

// gcpSubnetsIPv6Access gives both subnetworks an internal IPv6 access type.
func gcpSubnetsIPv6Access(in *cloudfw.Input) {
	set := func(res *plan.Resource) { res.Values["ipv6_access_type"] = "INTERNAL" }

	gcpAll(gcpEdit(gcpSubnetApp, set), gcpEdit(gcpSubnetData, set))(in)
}

// Attributes and subnetworks of the stack type scenarios.
const (
	gcpInterfaces        = "network_interface"
	gcpStack             = "stack_type"
	gcpInterfaceKey      = "network_interface[0].network"
	gcpInterfaceStackKey = "network_interface[0].stack_type"
	gcpSubnetApp         = "google_compute_subnetwork.app"
	gcpSubnetData        = "google_compute_subnetwork.data"
)

// gcpStackValue is a value of `stack_type`; "" means that the plan does not state one.
type gcpStackValue string

// gcpStackType sets the stack type of the first network interface of an instance; "" removes it.
func gcpStackType(address string, stack gcpStackValue) func(in *cloudfw.Input) {
	return gcpEdit(address, func(res *plan.Resource) {
		nic, _ := res.Values[gcpInterfaces].([]any)[0].(map[string]any)
		if stack == "" {
			delete(nic, gcpStack)

			return
		}

		nic[gcpStack] = string(stack)
	})
}

// gcpInterfaceConfig is how the configuration of an instance writes its network interface.
type gcpInterfaceConfig int

// Configurations of the network interface of an instance.
const (
	// gcpStaticStack is a static interface block that sets `stack_type`.
	gcpStaticStack gcpInterfaceConfig = iota
	// gcpStaticNoStack is a static interface block that does not set `stack_type`.
	gcpStaticNoStack
	// gcpNoStaticInterface is an interface that the configuration does not show, as a dynamic block gives.
	gcpNoStaticInterface
	// gcpStaticIPv6Access is a static interface block that does not set `stack_type` and sets `ipv6_access_type`.
	gcpStaticIPv6Access
)

// gcpConfiguredKeys are the configuration keys of an instance for each interface configuration.
var gcpConfiguredKeys = map[gcpInterfaceConfig][]string{
	gcpStaticStack:       {gcpInterfaceKey, gcpInterfaceStackKey},
	gcpStaticNoStack:     {gcpInterfaceKey},
	gcpNoStaticInterface: {"name"},
	gcpStaticIPv6Access:  {gcpInterfaceKey, "network_interface[0].ipv6_access_type"},
}

// gcpInstancesConfigured replaces the keys that the configuration of the web and db instances sets.
func gcpInstancesConfigured(config gcpInterfaceConfig) func(in *cloudfw.Input) {
	set := func(res *plan.Resource) { res.Configured = gcpConfiguredKeys[config] }

	return gcpAll(gcpEdit(gcpWeb, set), gcpEdit(gcpDB, set))
}

// gcpUnknownSubnetStacks makes the stack type of both subnetworks unknown until apply. configured says whether the
// configuration sets it (to a value unknown until apply).
func gcpUnknownSubnetStacks(configured bool) func(in *cloudfw.Input) {
	set := func(res *plan.Resource) {
		delete(res.Values, gcpStack)
		res.Unknown = append(res.Unknown, gcpStack)

		res.Configured = []string{"name"}
		if configured {
			res.Configured = append(res.Configured, gcpStack)
		}
	}

	return gcpAll(gcpEdit(gcpSubnetApp, set), gcpEdit(gcpSubnetData, set))
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
