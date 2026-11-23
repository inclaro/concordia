package cloudfw

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

// protocolAny is the normalized protocol of a rule that matches every protocol.
const protocolAny = "*"

// normProtocol gives the lower-case protocol name: `-1`, `all`, and `*` become protocolAny, `6` becomes `tcp`, `17`
// becomes `udp`.
func normProtocol(protocol string) string {
	switch p := strings.ToLower(strings.TrimSpace(protocol)); p {
	case "-1", "all", protocolAny:
		return protocolAny
	case "6":
		return "tcp"
	case "17":
		return "udp"
	default:
		return p
	}
}

// queryProtocol is the normalized protocol of the query; TCP when it is not set.
func queryProtocol(q *Query) string {
	if q.Protocol == "" {
		return "tcp"
	}

	return normProtocol(q.Protocol)
}

// matchProtocol is yes when the rule protocol is every protocol or the protocol of the query, else no. ICMP and
// other protocols never match a TCP or UDP query.
func matchProtocol(rule string, q *Query) tri {
	if p := normProtocol(rule); p == protocolAny || p == queryProtocol(q) {
		return triYes
	}

	return triNo
}

// matchPortRange matches the port against from-to. A rule for every protocol (`-1`) matches every port.
func matchPortRange(protocol string, from, to, port int) tri {
	if normProtocol(protocol) == protocolAny {
		return triYes
	}

	if inRange(from, to, port) {
		return triYes
	}

	return triNo
}

// inRange reports whether from <= n <= to.
func inRange(from, to, n int) bool { return from <= n && n <= to }

// matchPortSpec matches the port against a port text: `*`, `80`, or `1000-2000`. Text that does not parse gives
// maybe with a gap.
func matchPortSpec(spec string, port int, s *side) tri {
	spec = strings.TrimSpace(spec)
	if spec == protocolAny {
		return triYes
	}

	from, to, ok := parsePortSpec(spec)
	if !ok {
		s.gap(evaluate.GapUnsupportedKind, fmt.Sprintf("port range %q", spec))

		return triMaybe
	}

	return matchPortRange("", from, to, port)
}

func parsePortSpec(spec string) (from, to int, ok bool) {
	low, high, isRange := strings.Cut(spec, "-")

	from, err := strconv.Atoi(strings.TrimSpace(low))
	if err != nil {
		return 0, 0, false
	}

	if !isRange {
		return from, from, true
	}

	to, err = strconv.Atoi(strings.TrimSpace(high))

	return from, to, err == nil && from <= to
}

// anyPortSpec is the or of matchPortSpec over the port texts. An empty list matches no port.
func anyPortSpec(specs []string, port int, s *side) tri {
	result := triNo
	for _, spec := range specs {
		result = or(result, matchPortSpec(spec, port, s))
	}

	return result
}
