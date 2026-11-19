package netpol

import (
	"fmt"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// portsMatch is whether the destination port of the query is among the rule ports. An empty list matches every port.
func (e *Evaluator) portsMatch(ctx *dirCtx, ports []Port) tri {
	return anyEntryMatches(ports, func(entry *Port) tri { return e.portMatch(ctx, entry) })
}

func (e *Evaluator) portMatch(ctx *dirCtx, entry *Port) tri {
	proto := entry.Protocol
	if proto == "" {
		proto = ProtocolTCP
	}

	if proto != ctx.proto {
		return triNo
	}

	switch {
	case entry.Port == nil:
		return triYes
	case entry.Port.IsName:
		return e.namedPortMatch(ctx, entry.Port.Name, proto)
	}

	return numberedPortMatch(ctx.port, entry)
}

// numberedPortMatch is whether the port is the number of the entry, or within its range when it has an endPort.
func numberedPortMatch(port int, entry *Port) tri {
	last := entry.Port.Number
	if entry.EndPort != nil {
		last = *entry.EndPort
	}

	if port >= entry.Port.Number && port <= last {
		return triYes
	}

	return triNo
}

// namedPortMatch resolves a port name against the containers of the destination workload. The destination is the
// workload that the policy selects for ingress, and the peer for egress. An address has no containers.
func (e *Evaluator) namedPortMatch(ctx *dirCtx, name, proto string) tri {
	dest := ctx.pod
	if ctx.dir == DirectionEgress {
		dest = ctx.peer.workload
	}

	if dest == nil {
		ctx.addGap(evaluate.GapUnresolvedPort,
			fmt.Sprintf("the named port %q cannot be resolved for a destination outside the rendered workloads", name))

		return triMaybe
	}

	found := false

	for _, cp := range dest.Ports {
		if cp.Name != name || containerProtocol(cp) != proto {
			continue
		}

		found = true

		if cp.ContainerPort == ctx.port {
			return triYes
		}
	}

	if !found {
		ctx.addGap(evaluate.GapUnresolvedPort,
			fmt.Sprintf("no container of workload %s/%s defines the named port %q (%s)", dest.Namespace, dest.Name, name, proto))

		return triMaybe
	}

	return triNo
}

func containerProtocol(cp ContainerPort) string {
	if cp.Protocol == "" {
		return ProtocolTCP
	}

	return cp.Protocol
}
