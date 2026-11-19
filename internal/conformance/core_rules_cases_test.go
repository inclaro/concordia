package conformance_test

import (
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// pair is a finding source and target.
type pair struct{ source, target string }

type coreCase struct {
	rule      string
	severity  policy.RuleSeverity
	kind      conformance.FindingKind
	violating coreGraph
	want      []pair
	clean     coreGraph
	// tagDriven rules select nodes by tag: they must be silent when the model has no tags.
	tagDriven bool
}

func coreReads(from, to string) coreEdge {
	return kindEdge(from, to, observation.KindReads, observation.ConfidenceSyntactic)
}

func coreCases() []coreCase {
	return []coreCase{
		coreCycleCase(),
		coreDeclaredCase(),
		coreDatabaseCase(),
		coreBoundaryCase(),
		coreLayerOrderCase(),
		coreLayerInfrastructureCase(),
		coreSharedCodeCase(),
		coreSecurityCase(),
		coreObservedCase(),
	}
}

func coreCycleCase() coreCase {
	services := []coreNode{cn("a", "service"), cn("b", "service")}

	return coreCase{
		rule: "CORE-CYC-001", severity: policy.SeverityError, kind: conformance.KindCycle,
		violating: coreGraph{nodes: services, edges: []coreEdge{imports("a", "b"), calls("b", "a")}},
		want:      []pair{{"a", "a,b"}}, // a cycle finding names the first node and the whole cycle.
		clean:     coreGraph{nodes: services, edges: []coreEdge{imports("a", "b")}},
	}
}

func coreDeclaredCase() coreCase {
	declared := intent.RelationshipDeclaration{
		ID: "r-api-db", Kind: intent.RelConnects, Source: "api", Targets: []string{"db"}, Pointer: "/relationships/r-api-db",
	}
	nodes := []coreNode{cn("api", "service"), cn("db", "database")}

	return coreCase{
		rule: "CORE-DECL-001", severity: policy.SeverityError, kind: conformance.KindUndeclared,
		violating: coreGraph{nodes: nodes, edges: []coreEdge{coreReads("api", "db")}},
		want:      []pair{{"api", "db"}},
		clean:     coreGraph{nodes: nodes, rels: []intent.RelationshipDeclaration{declared}, edges: []coreEdge{coreReads("api", "db")}},
	}
}

func coreDatabaseCase() coreCase {
	nodes := []coreNode{cn("web", "webclient"), cn("app", "service", "client"), cn("db", "database"), cn("api", "service")}

	return coreCase{
		rule: "CORE-DB-001", severity: policy.SeverityError, kind: conformance.KindForbiddenDependency,
		violating: coreGraph{nodes: nodes, edges: []coreEdge{coreReads("web", "db"), calls("app", "db")}},
		want:      []pair{{"app", "db"}, {"web", "db"}},
		clean: coreGraph{
			nodes: nodes,
			edges: []coreEdge{
				calls("web", "api"),
				calls("app", "api"),
				kindEdge("api", "db", observation.KindWrites, observation.ConfidenceSyntactic),
			},
		},
		tagDriven: false, // the webclient node type still selects the subject without tags.
	}
}

func coreBoundaryCase() coreCase {
	return coreCase{
		rule: "CORE-BND-001", severity: policy.SeverityError, kind: conformance.KindForbiddenDependency,
		violating: coreGraph{
			nodes: []coreNode{cn("s1", "service"), cn("s2", "service")},
			edges: []coreEdge{
				imports("s1", "s2"),
				kindEdge("s2", "s1", observation.KindInherits, observation.ConfidenceResolved),
			},
		},
		want: []pair{{"s1", "s2"}, {"s2", "s1"}},
		clean: coreGraph{
			nodes: []coreNode{cn("s1", "service"), cn("s2", "service"), cn("c", "service", "contract"), cn("k", "service", "shared-kernel")},
			edges: []coreEdge{
				imports("s1", "c"),
				kindEdge("s2", "k", observation.KindImplements, observation.ConfidenceResolved),
				// A call is not a code import: it is the job of the declaration rule.
				calls("s1", "s2"),
			},
		},
	}
}

func coreLayerOrderCase() coreCase {
	layers := []coreNode{cn("ui", "service", "presentation"), cn("app", "service", "application"), cn("dom", "service", "domain")}

	return coreCase{
		rule: "CORE-LYR-001", severity: policy.SeverityError, kind: conformance.KindLayerViolation,
		violating: coreGraph{nodes: layers, edges: []coreEdge{calls("dom", "ui"), imports("app", "ui"), imports("dom", "app")}},
		want:      []pair{{"app", "ui"}, {"dom", "app"}, {"dom", "ui"}},
		clean: coreGraph{
			nodes: layers,
			// Downward edges are allowed, also when they skip a layer.
			edges: []coreEdge{calls("ui", "app"), imports("app", "dom"), imports("ui", "dom")},
		},
		tagDriven: true,
	}
}

func coreLayerInfrastructureCase() coreCase {
	layers := []coreNode{cn("dom", "service", "domain"), cn("db", "service", "infrastructure"), cn("ui", "service", "presentation")}

	return coreCase{
		rule: "CORE-LYR-002", severity: policy.SeverityError, kind: conformance.KindForbiddenDependency,
		violating: coreGraph{nodes: layers, edges: []coreEdge{imports("dom", "db"), calls("dom", "ui")}},
		want:      []pair{{"dom", "db"}, {"dom", "ui"}},
		clean:     coreGraph{nodes: layers, edges: []coreEdge{imports("db", "dom"), calls("ui", "dom")}},
		tagDriven: true,
	}
}

func coreSharedCodeCase() coreCase {
	return coreCase{
		rule: "CORE-LYR-003", severity: policy.SeverityWarning, kind: conformance.KindForbiddenDependency,
		violating: coreGraph{
			nodes: []coreNode{cn("sh", "service", "shared"), cn("svc", "service"), cn("web", "webclient"), cn("sys", "system")},
			edges: []coreEdge{imports("sh", "svc"), calls("sh", "web"), imports("sh", "sys")},
		},
		want: []pair{{"sh", "svc"}, {"sh", "sys"}, {"sh", "web"}},
		clean: coreGraph{
			nodes: []coreNode{cn("sh", "service", "shared"), cn("sh2", "service", "shared"), cn("svc", "service"), cn("lib", "network")},
			// Other services depend on shared code, and shared code may use other shared code.
			edges: []coreEdge{imports("svc", "sh"), imports("sh", "sh2"), imports("sh", "lib")},
		},
		tagDriven: true,
	}
}

func coreSecurityCase() coreCase {
	return coreCase{
		rule: "CORE-SEC-001", severity: policy.SeverityError, kind: conformance.KindForbiddenDependency,
		violating: coreGraph{
			nodes: []coreNode{cn("pub", "service", "public"), cn("ext", "service", "external"), cn("sec", "service", "restricted")},
			edges: []coreEdge{
				calls("pub", "sec"),
				coreReads("ext", "sec"),
				// HTTP calls are heuristic in the CBM adapter: the rule counts them.
				kindEdge("pub", "sec", observation.KindCallsService, observation.ConfidenceHeuristic),
			},
		},
		want: []pair{{"ext", "sec"}, {"pub", "sec"}, {"pub", "sec"}}, // one finding for each edge kind.
		clean: coreGraph{
			nodes: []coreNode{cn("pub", "service", "public"), cn("gw", "service"), cn("sec", "service", "restricted")},
			edges: []coreEdge{calls("pub", "gw"), calls("gw", "sec")},
		},
		tagDriven: true,
	}
}

func coreObservedCase() coreCase {
	required := intent.RelationshipDeclaration{
		ID: "r-a-b", Kind: intent.RelConnects, Source: "a", Targets: []string{"b"}, Pointer: "/relationships/r-a-b",
		Observation: &intent.RelationshipObservation{Required: true},
	}
	services := []coreNode{cn("a", "service"), cn("b", "service")}
	rels := []intent.RelationshipDeclaration{required}

	return coreCase{
		rule: "CORE-OBS-001", severity: policy.SeverityWarning, kind: conformance.KindUnobserved,
		violating: coreGraph{nodes: services, rels: rels},
		want:      []pair{{"a", "b"}},
		clean:     coreGraph{nodes: services, rels: rels, edges: []coreEdge{imports("a", "b")}},
	}
}
