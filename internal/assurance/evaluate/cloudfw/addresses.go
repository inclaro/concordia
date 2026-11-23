package cloudfw

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// addrSet is the address set of an endpoint: one prefix, or unknown.
type addrSet struct {
	prefix netip.Prefix
	known  bool
}

// unknownAddrs is the address set of an endpoint whose addresses the plan does not give.
var unknownAddrs = addrSet{}

// prefixSet is the address set of a CIDR (a subnet range). Text that does not parse gives an unknown set.
func prefixSet(cidr string) addrSet {
	p, ok := parseRange(cidr)

	return addrSet{prefix: p, known: ok}
}

// hostSet is the address set of one IP address (a private IP), a /32 or a /128.
func hostSet(ip string) addrSet {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return unknownAddrs
	}

	addr = addr.Unmap()

	return addrSet{prefix: netip.PrefixFrom(addr, addr.BitLen()), known: true}
}

// commonSet is the set that all the sets share when they are known and equal, else unknown. Several subnets of one
// endpoint give an unknown set unless they have the same prefix.
func commonSet(sets ...addrSet) addrSet {
	if len(sets) == 0 {
		return unknownAddrs
	}

	for _, set := range sets {
		if !set.known || set.prefix != sets[0].prefix {
			return unknownAddrs
		}
	}

	return sets[0]
}

// containedIn reports whether the prefix holds every address of the known set.
func (a addrSet) containedIn(p netip.Prefix) bool {
	return p.Bits() <= a.prefix.Bits() && p.Contains(a.prefix.Addr())
}

// parseRange reads a CIDR or a single IP address, masked.
func parseRange(text string) (netip.Prefix, bool) {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "/") {
		set := hostSet(text)

		return set.prefix, set.known
	}

	p, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, false
	}

	return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked(), true
}

// matchPrefix matches a rule prefix (a CIDR or an IP) against the address set of an endpoint: yes when the rule
// contains the set, no when they do not overlap, and maybe with a gap otherwise.
func matchPrefix(rule string, endpoint addrSet, into *side) tri {
	prefix, ok := parseRange(rule)

	switch {
	case !ok:
		into.gap(evaluate.GapInvalidRange, fmt.Sprintf("%q is not a CIDR or an IP address", rule))

		return triMaybe
	case !endpoint.known:
		return matchUnknownSet(prefix, into)
	case endpoint.containedIn(prefix):
		return triYes
	case !prefix.Overlaps(endpoint.prefix):
		return triNo
	}

	into.gap(evaluate.GapAddressDependent, fmt.Sprintf("%s covers only part of %s", rule, endpoint.prefix))

	return triMaybe
}

// matchUnknownSet matches a rule prefix against an endpoint whose addresses are unknown: only an all-addresses
// prefix is sure to match.
func matchUnknownSet(p netip.Prefix, s *side) tri {
	if p.Bits() == 0 {
		return triYes
	}

	s.gap(evaluate.GapAddressDependent, p.String()+" against an endpoint whose addresses are not known before apply")

	return triMaybe
}

// anyPrefix is the or of matchPrefix over the rule prefixes. An empty list matches nothing.
func anyPrefix(rules []string, ep addrSet, s *side) tri {
	result := triNo
	for _, rule := range rules {
		result = or(result, matchPrefix(rule, ep, s))
	}

	return result
}
