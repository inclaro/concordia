package netpol

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// peersMatch is whether the other end of the traffic is one of the peers. An empty list matches every peer.
func (e *Evaluator) peersMatch(ctx *dirCtx, pol *NetworkPolicy, peers []Peer) tri {
	return anyEntryMatches(peers, func(peer *Peer) tri { return e.peerMatch(ctx, pol, peer) })
}

func (e *Evaluator) peerMatch(ctx *dirCtx, pol *NetworkPolicy, peer *Peer) tri {
	hasSelector := peer.PodSelector != nil || peer.NamespaceSelector != nil
	hasBlock := peer.IPBlock != nil

	// A peer sets exactly one of an ipBlock and selectors.
	if hasBlock == hasSelector {
		ctx.addGap(evaluate.GapInvalidPeer,
			fmt.Sprintf("NetworkPolicy %s has a peer that sets an ipBlock together with selectors, or nothing", refText(pol)))

		return triMaybe
	}

	if hasBlock {
		return e.ipBlockMatch(ctx, pol, peer.IPBlock)
	}

	if ctx.peer.workload == nil {
		// Pod and namespace selectors select pods; an address outside the cluster is not one.
		return triNo
	}

	podMatch := triYes
	if peer.PodSelector != nil {
		podMatch = e.matchSelector(ctx, peer.PodSelector, ctx.peer.workload.Labels, "podSelector of NetworkPolicy "+refText(pol))
	}

	return and(e.peerNamespaceMatch(ctx, pol, peer), podMatch)
}

// peerNamespaceMatch is whether the namespace of the peer workload is one that the peer selects.
func (e *Evaluator) peerNamespaceMatch(ctx *dirCtx, pol *NetworkPolicy, peer *Peer) tri {
	if peer.NamespaceSelector == nil {
		// Without a namespace selector the pod selector is limited to the namespace of the policy.
		if ctx.peer.workload.Namespace != pol.Metadata.Namespace {
			return triNo
		}

		return triYes
	}

	labels := map[string]string{metadataNameLabel: ctx.peer.ns.Name}
	for k, v := range ctx.peer.ns.Labels {
		labels[k] = v
	}

	return e.matchSelector(ctx, peer.NamespaceSelector, labels, "namespaceSelector of NetworkPolicy "+refText(pol))
}

// ipBlockMatch matches an ipBlock against the other end. For an address it is a CIDR test; for a workload the pod
// address is not known, so the block counts only when it provably does not overlap the pod network.
func (e *Evaluator) ipBlockMatch(ctx *dirCtx, pol *NetworkPolicy, block *IPBlock) tri {
	cidr, excepts, ok := parseIPBlock(ctx, pol, block)
	if !ok {
		return triMaybe
	}

	if ctx.peer.workload == nil {
		if blockContains(cidr, excepts, ctx.peer.addr) {
			return triYes
		}

		return triNo
	}

	if e.disjointFromPods(cidr, excepts) {
		return triNo
	}

	ctx.addGap(evaluate.GapClusterRange,
		fmt.Sprintf("NetworkPolicy %s uses the ipBlock %s; whether it contains the address of workload %s/%s depends on the cluster",
			refText(pol), block.CIDR, ctx.peer.workload.Namespace, ctx.peer.workload.Name))

	return triMaybe
}

// parseIPBlock reads the masked CIDR and excepts of an ipBlock. It records a gap and reports false for a malformed one.
func parseIPBlock(ctx *dirCtx, pol *NetworkPolicy, block *IPBlock) (cidr netip.Prefix, excepts []netip.Prefix, ok bool) {
	cidr, err := netip.ParsePrefix(block.CIDR)
	if err != nil {
		ctx.addGap(evaluate.GapInvalidRange,
			fmt.Sprintf("NetworkPolicy %s has the ipBlock cidr %q, which is not a CIDR", refText(pol), block.CIDR))

		return netip.Prefix{}, nil, false
	}

	excepts = make([]netip.Prefix, 0, len(block.Except))

	for _, ex := range block.Except {
		prefix, err := netip.ParsePrefix(ex)
		if err != nil {
			ctx.addGap(evaluate.GapInvalidRange,
				fmt.Sprintf("NetworkPolicy %s has the ipBlock except %q, which is not a CIDR", refText(pol), ex))

			return netip.Prefix{}, nil, false
		}

		excepts = append(excepts, prefix.Masked())
	}

	return cidr.Masked(), excepts, true
}

// blockContains reports whether the address is in the CIDR and in none of the excepts.
func blockContains(cidr netip.Prefix, excepts []netip.Prefix, addr netip.Addr) bool {
	return cidr.Contains(addr) && !slices.ContainsFunc(excepts, func(x netip.Prefix) bool { return x.Contains(addr) })
}

// disjointFromPods reports whether the block (CIDR minus excepts) provably contains no pod address. That needs the
// pod ranges of the cluster; without them nothing is provable.
func (e *Evaluator) disjointFromPods(cidr netip.Prefix, excepts []netip.Prefix) bool {
	if len(e.podRanges) == 0 || e.rangeErr {
		return false
	}

	for _, pod := range e.podRanges {
		if cidr.Overlaps(pod) && !exceptCoversOverlap(cidr, pod, excepts) {
			return false
		}
	}

	return true
}

// exceptCoversOverlap reports whether an except covers the whole overlap of the block and the pod range. The overlap
// is the smaller of the two prefixes, so an except that contains it covers it.
func exceptCoversOverlap(cidr, pod netip.Prefix, excepts []netip.Prefix) bool {
	overlap := pod
	if cidr.Bits() > pod.Bits() {
		overlap = cidr
	}

	return slices.ContainsFunc(excepts, func(x netip.Prefix) bool { return x.Bits() <= overlap.Bits() && x.Contains(overlap.Addr()) })
}
