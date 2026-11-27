package cloudfw

import (
	"encoding/json"
	"slices"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// The IPv6 settings of an interface and of a subnetwork (google provider 8.x). The default stack type IPV4_ONLY of
// an unset `stack_type` holds only when none of them is set.
var (
	gcpInterfaceIPv6 = []string{
		"network_interface.0.ipv6_address", "network_interface.0.internal_ipv6_prefix_length",
		"network_interface.0.ipv6_access_type",
	}
	gcpSubnetIPv6 = []string{"ipv6_access_type", "external_ipv6_prefix", "internal_ipv6_prefix", "ipv6_cidr_range"}
)

// gcpIPv6AccessConfig is the block list of the external IPv6 access of an interface.
const gcpIPv6AccessConfig = "network_interface.0.ipv6_access_config"

// gcpInterfaceWithoutIPv6 reports whether the first interface has no IPv6 setting: none of its IPv6 attributes is set
// (see gcpWithoutIPv6), and its `ipv6_access_config` list is not configured and known to be empty. The list is not
// computed, so an unknown list may come from a dynamic block.
func gcpInterfaceWithoutIPv6(res *plan.Resource) bool {
	if !leavesUnset(res, gcpIPv6AccessConfig) || unknownAt(res, gcpIPv6AccessConfig) {
		return false
	}

	return blockCount(res, gcpIPv6AccessConfig) == 0 && gcpWithoutIPv6(res, gcpInterfaceIPv6)
}

// gcpWithoutIPv6 reports whether no attribute of the paths is set: the configuration does not set it, and the plan
// gives it no value. A value that is unknown until apply only because the configuration does not set it is computed
// from the stack type, so it does not count.
func gcpWithoutIPv6(res *plan.Resource, paths []string) bool {
	return !slices.ContainsFunc(paths, func(path string) bool {
		return !leavesUnset(res, path) || !emptyValue(attr(res, path))
	})
}

// emptyValue reports whether a planned value is absent, null, empty, or zero.
func emptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case json.Number:
		return typed.String() == "0"
	case float64:
		return typed == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}

	return false
}
