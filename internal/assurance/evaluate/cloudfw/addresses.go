package cloudfw

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// addrSet is the address set of an endpoint: the prefixes that hold every address it can have (its private IPs, or
// the ranges of its subnets), or unknown when it has none.
type addrSet struct {
	prefixes []netip.Prefix
}

// unknownAddrs is the address set of an endpoint whose addresses the plan does not give.
var unknownAddrs = addrSet{}

// known reports whether the plan gives the addresses of the endpoint.
func (a addrSet) known() bool { return len(a.prefixes) > 0 }

// prefixSet is the address set of a CIDR (a subnet range). Text that does not parse gives an unknown set.
func prefixSet(cidr string) addrSet {
	p, ok := parseRange(cidr)
	if !ok {
		return unknownAddrs
	}

	return addrSet{prefixes: []netip.Prefix{p}}
}

// hostSet is the address set of one IP address (a private IP), a /32 or a /128.
func hostSet(ip string) addrSet {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return unknownAddrs
	}

	addr = addr.Unmap()

	return addrSet{prefixes: []netip.Prefix{netip.PrefixFrom(addr, addr.BitLen())}}
}

// unionSet is the set of the addresses of all the sets: an endpoint in several subnets, or with several interfaces,
// can have an address in any of them. One unknown set, or no set, gives an unknown set.
func unionSet(sets ...addrSet) addrSet {
	var out addrSet

	for _, set := range sets {
		if !set.known() {
			return unknownAddrs
		}

		out.prefixes = append(out.prefixes, set.prefixes...)
	}

	slices.SortFunc(out.prefixes, netip.Prefix.Compare)
	out.prefixes = slices.Compact(out.prefixes)

	return out
}

// String lists the prefixes of the set.
func (a addrSet) String() string {
	texts := make([]string, 0, len(a.prefixes))
	for _, p := range a.prefixes {
		texts = append(texts, p.String())
	}

	return strings.Join(texts, ", ")
}

// containedIn reports whether the prefix holds the whole of the endpoint prefix.
func containedIn(endpoint, p netip.Prefix) bool {
	return p.Bits() <= endpoint.Bits() && p.Contains(endpoint.Addr())
}

// parseRange reads a CIDR or a single IP address, masked. A CIDR keeps its address family: an IPv4-mapped IPv6
// prefix such as `::ffff:10.0.0.0/104` is an IPv6 prefix, whose bits count from the start of the IPv6 address. A
// single IPv4-mapped address is the IPv4 host.
func parseRange(text string) (netip.Prefix, bool) {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "/") {
		set := hostSet(text)
		if !set.known() {
			return netip.Prefix{}, false
		}

		return set.prefixes[0], true
	}

	p, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, false
	}

	return p.Masked(), true
}

// matchPrefix matches a rule prefix (a CIDR or an IP) against the address set of an endpoint. The evaluator evaluates
// IPv4 paths: an IPv4 rule is yes when it contains every prefix of the set, no when it overlaps none of them, and
// maybe with a gap otherwise. An IPv6 rule (or an IPv6 endpoint prefix) is never evaluated: it is maybe, never yes
// and never no, so it cannot decide a result that an IPv4 rule does not decide.
func matchPrefix(rule string, endpoint addrSet, into *side) tri {
	prefix, ok := parseRange(rule)

	switch {
	case !ok:
		into.gap(evaluate.GapInvalidRange, fmt.Sprintf("%q is not a CIDR or an IP address", rule))

		return triMaybe
	case !prefix.Addr().Is4():
		into.gap(evaluate.GapAddressDependent, prefix.String()+": IPv6 ranges are not evaluated")

		return triMaybe
	case !endpoint.known():
		return matchUnknownSet(prefix, into)
	}

	return matchKnownSet(rule, prefix, endpoint, into)
}

// matchKnownSet matches an IPv4 rule prefix against every prefix of a known endpoint set.
func matchKnownSet(rule string, prefix netip.Prefix, endpoint addrSet, into *side) tri {
	if i := slices.IndexFunc(endpoint.prefixes, func(p netip.Prefix) bool { return !p.Addr().Is4() }); i >= 0 {
		into.gap(evaluate.GapAddressDependent, endpoint.prefixes[i].String()+": IPv6 ranges are not evaluated")

		return triMaybe
	}

	switch {
	case !slices.ContainsFunc(endpoint.prefixes, func(p netip.Prefix) bool { return !containedIn(p, prefix) }):
		return triYes
	case !slices.ContainsFunc(endpoint.prefixes, prefix.Overlaps):
		return triNo
	}

	into.gap(evaluate.GapAddressDependent, fmt.Sprintf("%s covers only part of %s", rule, endpoint))

	return triMaybe
}

// matchUnknownSet matches an IPv4 rule prefix against an endpoint whose addresses are unknown: only `0.0.0.0/0` is
// sure to match.
func matchUnknownSet(p netip.Prefix, s *side) tri {
	if p.Bits() == 0 {
		return triYes
	}

	s.gap(evaluate.GapAddressDependent, p.String()+" against an endpoint whose addresses are not known before apply")

	return triMaybe
}

// anyPrefix is the or of matchPrefix over the rule prefixes. An empty list matches nothing. It is yes when an IPv4
// range matches, and maybe when only an IPv6 range could.
func anyPrefix(rules []string, ep addrSet, s *side) tri {
	result := triNo
	for _, rule := range rules {
		result = or(result, matchPrefix(rule, ep, s))
	}

	return result
}
