package cloudfw_test

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpDualStackScenarios are denies through address ranges: they decide only when the plan proves that an endpoint is
// IPv4-only. The fixture states IPV4_ONLY on every interface and subnetwork.
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
			name: "egress deny for two dual-stack interfaces", from: gcpWeb, to: gcpDB, port: 3306,
			mutate: gcpAll(gcpStackType(gcpWeb, "IPV4_IPV6"), gcpStackType(gcpDB, "IPV4_IPV6")),
			want:   cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent, rules: mysqlOut,
		},
		{
			name: "egress deny for a dual-stack interface and an IPV4_ONLY peer", from: gcpWeb, to: gcpDB, port: 3306,
			mutate: gcpStackType(gcpWeb, "IPV4_IPV6"), want: cloudfw.VerdictDenied, rules: mysqlOut,
		},
		{
			name: "egress deny for interfaces that state IPV4_ONLY", from: gcpWeb, to: gcpDB, port: 3306,
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

// gcpIPv6SettingScenarios are unset stack types next to an IPv6 setting: the default IPV4_ONLY then does not hold.
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
