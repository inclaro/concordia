package netpol_test

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
)

// allScenarios are the scenarios of TestNetworkPolicySemantics, in the order of the groups below.
func allScenarios() []scenario {
	return slices.Concat(
		unionScenarios(), directionScenarios(), selectorScenarios(), ruleScenarios(), namedPortScenarios(), peerScenarios(),
	)
}

// unionScenarios are policies that select one pod and allow sources in union.
func unionScenarios() []scenario {
	return []scenario{
		{
			name: "selected for ingress without rules denies every source", policies: []string{denyAllAPI},
			query: q(wl("shop", "web"), wl("shop", "api"), 8080), want: netpol.Denied, deciders: "api-deny:ingress",
		},
		{
			name: "pod that no policy selects is open", policies: []string{denyAllAPI},
			query: q(wl("shop", "web"), wl("shop", "db"), 5432), want: netpol.Allowed, deciders: "",
		},
		{
			name: "no policy at all is open", query: q(wl("shop", "web"), wl("shop", "api"), 8080),
			want: netpol.Allowed, deciders: "",
		},
		{
			name: "union: first source allowed", policies: []string{allowWebToAPI, allowProbeToAPI},
			query: q(wl("shop", "web"), wl("shop", "api"), 8080), want: netpol.Allowed, deciders: "api-from-web:ingress",
		},
		{
			name: "union: second source allowed", policies: []string{allowWebToAPI, allowProbeToAPI},
			query: q(wl("ops", "probe"), wl("shop", "api"), 8080), want: netpol.Allowed, deciders: "api-from-probe:ingress",
		},
		{
			name: "union: third source denied", policies: []string{allowWebToAPI, allowProbeToAPI},
			query: q(wl("shop", "db"), wl("shop", "api"), 8080), want: netpol.Denied, deciders: "api-from-probe:ingress,api-from-web:ingress",
		},
	}
}

// directionScenarios are policies where both ends, the namespace of the policy, or a port decide.
func directionScenarios() []scenario {
	return []scenario{
		{
			name:     "pod selector without namespace selector stays in the policy namespace",
			policies: []string{fromProbeHere},
			query:    q(wl("ops", "probe"), wl("shop", "api"), 80), want: netpol.Denied, deciders: "-",
		},
		{
			name:     "destination allows but source egress does not",
			policies: []string{allowWebToAPI, webEgressToDB},
			query:    q(wl("shop", "web"), wl("shop", "api"), 8080), want: netpol.Denied, deciders: "web-egress:egress",
		},
		{
			name:     "destination and source both allow",
			policies: []string{allowWebToAPI, webEgressToAPI},
			query:    q(wl("shop", "web"), wl("shop", "api"), 8080), want: netpol.Allowed, deciders: "api-from-web:ingress,web-egress:egress",
		},
		{
			name:     "namespace selector on port 8443: matching port",
			policies: []string{tlsFromOps},
			query:    q(wl("ops", "probe"), wl("shop", "api"), 8443), want: netpol.Allowed, deciders: "tls-from-ops:ingress",
		},
		{
			name:     "namespace selector on port 8443: other port",
			policies: []string{tlsFromOps},
			query:    q(wl("ops", "probe"), wl("shop", "api"), 80), want: netpol.Denied, deciders: "tls-from-ops:ingress",
		},
		{
			name:     "namespace selector on port 8443: non-matching namespace",
			policies: []string{portFromOps},
			query:    q(wl("free", "loner"), wl("shop", "api"), 8443), want: netpol.Denied, deciders: "-",
		},
	}
}

// selectorScenarios are policies that select pods and namespaces by labels and expressions, and the policy types.
func selectorScenarios() []scenario {
	return []scenario{
		{
			name:     "empty podSelector selects every pod of the namespace",
			policies: []string{defaultDeny},
			query:    q(wl("ops", "probe"), wl("shop", "db"), 5432), want: netpol.Denied, deciders: "default-deny:ingress",
		},
		{
			name:     "empty podSelector does not reach other namespaces",
			policies: []string{defaultDeny},
			query:    q(wl("shop", "web"), wl("free", "loner"), 80), want: netpol.Allowed, deciders: "",
		},
		{
			name:     "default deny egress only affects egress",
			policies: []string{noEgress},
			query:    q(wl("shop", "web"), wl("free", "loner"), 80), want: netpol.Denied, deciders: "no-egress:egress",
		},
		{
			name:     "egress rules without policyTypes imply Egress and Ingress",
			policies: []string{webOutWithoutTypes},
			query:    q(wl("ops", "probe"), wl("shop", "web"), 80), want: netpol.Denied, deciders: "web-out:ingress",
		},
		{
			name:     "matchExpressions In selects",
			policies: []string{frontOrBack},
			query:    q(wl("shop", "db"), wl("shop", "api"), 80), want: netpol.Allowed, deciders: "front-or-back:ingress",
		},
		{
			name:     "matchExpressions NotIn excludes source",
			policies: []string{frontOrBack},
			query:    q(wl("shop", "web"), wl("shop", "api"), 80), want: netpol.Denied, deciders: "front-or-back:ingress",
		},
		{
			name:     "matchExpressions Exists and DoesNotExist",
			policies: []string{existsAndAbsent},
			query:    q(wl("ops", "probe"), wl("shop", "db"), 80), want: netpol.Denied, deciders: "exists:ingress",
		},
		{
			name:     "namespace selector reads the implicit metadata.name label",
			policies: []string{byMetadataName},
			query:    q(wl("ops", "probe"), wl("shop", "api"), 80), want: netpol.Allowed, deciders: "by-name:ingress",
		},
		{
			name:     "empty namespaceSelector matches every namespace",
			policies: []string{anyNamespace},
			query:    q(wl("free", "loner"), wl("shop", "api"), 80), want: netpol.Allowed, deciders: "any-ns:ingress",
		},
	}
}

// ruleScenarios are rules with ports, protocols and port ranges, or with no peers at all.
func ruleScenarios() []scenario {
	return []scenario{
		{
			name:     "rule without from allows every source on the port",
			policies: []string{openPort},
			query:    q(wl("free", "loner"), wl("shop", "api"), 8080), want: netpol.Allowed, deciders: "open-port:ingress",
		},
		{
			name:     "empty rule allows everything",
			policies: []string{allowAll},
			query:    q(wl("free", "loner"), wl("shop", "api"), 1), want: netpol.Allowed, deciders: "allow-all:ingress",
		},
		{
			name:     "port protocol must match",
			policies: []string{udpOnly},
			query:    q(wl("free", "loner"), wl("shop", "api"), 53), want: netpol.Denied, deciders: "udp-only:ingress",
		},
		{
			name:     "endPort range includes both ends",
			policies: []string{portRange},
			query:    q(wl("free", "loner"), wl("shop", "api"), 8100), want: netpol.Allowed, deciders: "range:ingress",
		},
		{
			name:     "endPort range excludes the next port",
			policies: []string{portRange},
			query:    q(wl("free", "loner"), wl("shop", "api"), 8101), want: netpol.Denied, deciders: "range:ingress",
		},
	}
}

// namedPortScenarios are rules that name a container port.
func namedPortScenarios() []scenario {
	return []scenario{
		{
			name:     "named port resolved on the destination (ingress)",
			policies: []string{metricsByName},
			query:    q(wl("free", "loner"), wl("shop", "api"), 9090), want: netpol.Allowed, deciders: "by-name:ingress",
		},
		{
			name:     "named port resolves to another number",
			policies: []string{metricsByName},
			query:    q(wl("free", "loner"), wl("shop", "api"), 8080), want: netpol.Denied, deciders: "by-name:ingress",
		},
		{
			name:     "named port resolved on the destination (egress)",
			policies: []string{webOutToDBPG},
			query:    q(wl("shop", "web"), wl("shop", "db"), 5432), want: netpol.Allowed, deciders: "web-out:egress",
		},
		{
			name:     "named port that no container of the destination defines is unknown",
			policies: []string{grpcByName},
			query:    q(wl("free", "loner"), wl("shop", "api"), 50051), want: netpol.Unknown, deciders: "by-name:ingress",
		},
		{
			name:     "an allowing rule beats an unresolvable named port",
			policies: []string{mixedPorts},
			query:    q(wl("free", "loner"), wl("shop", "api"), 8080), want: netpol.Allowed, deciders: "mixed:ingress",
		},
		{
			name:     "named port toward an address is unknown",
			policies: []string{httpsToAnyAddress},
			query:    q(wl("shop", "web"), addr("203.0.113.10"), 443), want: netpol.Unknown, deciders: "web-out:egress",
		},
	}
}

// peerScenarios are ipBlock peers, peers that cannot match an address, and invalid peers.
func peerScenarios() []scenario {
	return []scenario{
		{
			name:     "ipBlock allows an address outside the except range",
			policies: []string{ipBlockExcept},
			query:    q(wl("shop", "web"), addr("198.51.100.7"), 443), want: netpol.Allowed, deciders: "web-out:egress",
		},
		{
			name:     "ipBlock except denies the address",
			policies: []string{ipBlockExcept},
			query:    q(wl("shop", "web"), addr("203.0.113.10"), 443), want: netpol.Denied, deciders: "web-out:egress",
		},
		{
			name:     "pod selector peers never match an address",
			policies: []string{namespaceSelectorEgress},
			query:    q(wl("shop", "web"), addr("198.51.100.7"), 443), want: netpol.Denied, deciders: "web-out:egress",
		},
		{
			name:     "ingress from an external address through ipBlock",
			policies: []string{fromOffice},
			query:    q(addr("198.51.100.7"), wl("shop", "api"), 8080), want: netpol.Allowed, deciders: "api-from-office:ingress",
		},
		{
			name:     "ipBlock toward a workload is cluster dependent without pod ranges",
			policies: []string{ipBlockEverywhere},
			query:    q(wl("shop", "web"), wl("shop", "db"), 5432), want: netpol.Unknown, deciders: "web-out:egress",
		},
		{
			name:     "invalid peer is unknown",
			policies: []string{emptyPeer},
			query:    q(wl("shop", "web"), wl("shop", "api"), 80), want: netpol.Unknown, deciders: "bad:ingress",
		},
		{
			name:     "invalid selector operator is unknown",
			policies: []string{bogusOperator},
			query:    q(wl("shop", "web"), wl("shop", "api"), 80), want: netpol.Unknown, deciders: "bad:ingress",
		},
	}
}
