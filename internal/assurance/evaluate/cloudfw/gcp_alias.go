package cloudfw

import (
	"fmt"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpAliasList is the alias IP range list of the first network interface of an instance.
const gcpAliasList = "network_interface[0].alias_ip_range"

// gcpAliasAddrs are the alias IP ranges of the first network interface. known is false when the plan does not give
// all of them: the configuration may set the list and it is unknown until apply, or a range is unknown or is only a
// size (`/24`) that the provider fills in on apply.
func gcpAliasAddrs(res *plan.Resource) (aliases []addrSet, known bool) {
	if gcpAliasListUnknown(res) {
		return nil, false
	}

	for i := range blockCount(res, gcpAliasList) {
		cidr, _ := attrString(res, fmt.Sprintf("%s[%d].ip_cidr_range", gcpAliasList, i))

		set := prefixSet(cidr)
		if !set.known() {
			return nil, false
		}

		aliases = append(aliases, set)
	}

	return aliases, true
}

// gcpAliasListUnknown reports whether the alias range list is unknown until apply, in whole elements or as a list
// that the configuration may set.
func gcpAliasListUnknown(res *plan.Resource) bool {
	return listUnknown(res, gcpAliasList) || unknownAt(res, gcpAliasList) && !leavesUnset(res, gcpAliasList)
}
