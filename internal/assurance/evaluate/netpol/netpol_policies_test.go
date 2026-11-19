package netpol_test

// The policies of the scenarios in netpol_scenarios_test.go. Each one selects pods of the shop namespace.
const (
	denyAllAPI = `
metadata: {name: api-deny, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
`
	allowWebToAPI = `
metadata: {name: api-from-web, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress:
    - from: [{podSelector: {matchLabels: {app: web}}}]
`
	allowProbeToAPI = `
metadata: {name: api-from-probe, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress:
    - from:
        - namespaceSelector: {matchLabels: {team: ops}}
          podSelector: {matchLabels: {app: probe}}
`
	fromProbeHere = `
metadata: {name: from-probe-here, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{from: [{podSelector: {matchLabels: {app: probe}}}]}]
`
	webEgressToDB = `
metadata: {name: web-egress, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{podSelector: {matchLabels: {app: db}}}]}]
`
	webEgressToAPI = `
metadata: {name: web-egress, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{podSelector: {matchLabels: {app: api}}}]}]
`
	tlsFromOps = `
metadata: {name: tls-from-ops, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress:
    - from: [{namespaceSelector: {matchLabels: {team: ops}}}]
      ports: [{protocol: TCP, port: 8443}]
`
	portFromOps = `
metadata: {name: tls-from-ops, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress:
    - from: [{namespaceSelector: {matchLabels: {team: ops}}}]
      ports: [{port: 8443}]
`
	defaultDeny = `
metadata: {name: default-deny, namespace: shop}
spec: {podSelector: {}}
`
	noEgress = `
metadata: {name: no-egress, namespace: shop}
spec: {podSelector: {}, policyTypes: [Egress]}
`
	webOutWithoutTypes = `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  egress: [{to: [{podSelector: {matchLabels: {app: api}}}]}]
`
	frontOrBack = `
metadata: {name: front-or-back, namespace: shop}
spec:
  podSelector:
    matchExpressions: [{key: tier, operator: In, values: [back]}]
  ingress: [{from: [{podSelector: {matchExpressions: [{key: app, operator: NotIn, values: [web]}]}}]}]
`
	existsAndAbsent = `
metadata: {name: exists, namespace: shop}
spec:
  podSelector: {matchExpressions: [{key: tier, operator: Exists}, {key: canary, operator: DoesNotExist}]}
`
	byMetadataName = `
metadata: {name: by-name, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{from: [{namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: ops}}}]}]
`
	anyNamespace = `
metadata: {name: any-ns, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{from: [{namespaceSelector: {}}]}]
`
	openPort = `
metadata: {name: open-port, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{ports: [{port: 8080}]}]
`
	allowAll = `
metadata: {name: allow-all, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{}]
`
	udpOnly = `
metadata: {name: udp-only, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{ports: [{protocol: UDP, port: 53}]}]
`
	portRange = `
metadata: {name: range, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{ports: [{port: 8000, endPort: 8100}]}]
`
	metricsByName = `
metadata: {name: by-name, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{ports: [{port: metrics}]}]
`
	webOutToDBPG = `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{podSelector: {matchLabels: {app: db}}}], ports: [{port: pg}]}]
`
	grpcByName = `
metadata: {name: by-name, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{ports: [{port: grpc}]}]
`
	mixedPorts = `
metadata: {name: mixed, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{ports: [{port: grpc}, {port: 8080}]}]
`
	httpsToAnyAddress = `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{ports: [{port: https}]}]
`
	ipBlockExcept = `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{ipBlock: {cidr: 0.0.0.0/0, except: [203.0.113.0/24]}}]}]
`
	namespaceSelectorEgress = `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{namespaceSelector: {}}]}]
`
	fromOffice = `
metadata: {name: api-from-office, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{from: [{ipBlock: {cidr: 198.51.100.0/24}}]}]
`
	ipBlockEverywhere = `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{ipBlock: {cidr: 0.0.0.0/0}}]}]
`
	emptyPeer = `
metadata: {name: bad, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress: [{from: [{}]}]
`
	bogusOperator = `
metadata: {name: bad, namespace: shop}
spec:
  podSelector: {matchExpressions: [{key: app, operator: Bogus}]}
`
)
