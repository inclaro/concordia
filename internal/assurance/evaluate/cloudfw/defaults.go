package cloudfw

import (
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// A provider computes some attributes that the configuration does not set, and the plan shows them unknown until
// apply. For a few of them the value is a documented default that does not depend on other resources, and the
// evaluator reads that default instead of the unknown value: GCP `stack_type` (IPV4_ONLY) and the own ranges of a
// GCP firewall rule (none, so every address). The plan must have a configuration for the resource, and the
// attribute must not be one that a `dynamic` block can set: the configuration of a plan does not show dynamic blocks,
// so the inference is never made for a block list, and an attribute inside a block only when every block of the
// list is written statically.

// leavesUnset reports whether the plan has a configuration for the resource that does not set the attribute.
func leavesUnset(res *plan.Resource, path string) bool {
	return res.Configured != nil && !slices.Contains(res.Configured, planPath(path))
}

// unsetComputed reports whether the attribute is unknown until apply only because the configuration, which the plan
// has for the resource, does not set it.
func unsetComputed(res *plan.Resource, path string) bool {
	return leavesUnset(res, path) && unknownAt(res, path)
}

// staticBlocks reports whether the configuration writes every block of the list statically: the plan lists as many
// blocks as the configuration has, and at least one. A dynamic block adds blocks that the configuration does not show.
func staticBlocks(res *plan.Resource, list string) bool {
	count := blockCount(res, list)
	if count == 0 || listUnknown(res, list) {
		return false
	}

	return configuredBlocks(res, list) == count
}

// configuredBlocks is the number of blocks of the list that the configuration writes statically.
func configuredBlocks(res *plan.Resource, list string) int {
	blocks := 0
	prefix := planPath(list) + "["

	for _, key := range res.Configured {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}

		index, _, _ := strings.Cut(rest, "]")
		if n, err := strconv.Atoi(index); err == nil {
			blocks = max(blocks, n+1)
		}
	}

	return blocks
}
