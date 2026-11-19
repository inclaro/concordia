package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
)

// coreNode is a CALM node of a core rule test.
type coreNode struct {
	id, nodeType string
	tags         []string
}

// coreEdge is an observed edge between the code of two CALM nodes.
type coreEdge struct {
	from, to   string
	kind       observation.CanonicalEdgeKind
	confidence observation.Confidence
}

// coreGraph is a model and an observed graph. Each node has one observed file.
type coreGraph struct {
	nodes []coreNode
	rels  []intent.RelationshipDeclaration
	edges []coreEdge
}

func cn(id, nodeType string, tags ...string) coreNode {
	return coreNode{id: id, nodeType: nodeType, tags: tags}
}

func imports(from, to string) coreEdge {
	return coreEdge{from: from, to: to, kind: observation.KindImports, confidence: observation.ConfidenceSyntactic}
}

func calls(from, to string) coreEdge {
	return coreEdge{from: from, to: to, kind: observation.KindCalls, confidence: observation.ConfidenceSyntactic}
}

func kindEdge(from, to string, kind observation.CanonicalEdgeKind, confidence observation.Confidence) coreEdge {
	return coreEdge{from: from, to: to, kind: kind, confidence: confidence}
}

// withoutTags gives the same graph with every tag removed.
func (g coreGraph) withoutTags() coreGraph {
	nodes := make([]coreNode, len(g.nodes))
	for i, n := range g.nodes {
		nodes[i] = coreNode{id: n.id, nodeType: n.nodeType}
	}

	return coreGraph{nodes: nodes, rels: g.rels, edges: g.edges}
}

func coreFile(id string) string { return "src/" + id + "/index.ts" }

func coreObserved(id string) *observation.ObservedNode {
	return obs("src."+id, coreFile(id))
}

// coreWorld is the architecture model and the observed evidence of a core graph.
type coreWorld struct {
	model      *intent.ArchitectureModel
	byObserved map[string][]string
	observed   map[string]*observation.ObservedNode
	nodes      map[string]observation.ObservedNode
	roots      map[string][]string
	files      []string
}

func newCoreWorld(graph *coreGraph) *coreWorld {
	w := &coreWorld{
		model: &intent.ArchitectureModel{
			SourceURI: "architecture/a.calm.json", Digest: "sha256:0", SchemaVersion: "1.2",
			Relationships: graph.rels, Controls: []intent.ControlAttachment{}, Metadata: map[string]any{},
		},
		byObserved: map[string][]string{},
		observed:   map[string]*observation.ObservedNode{},
		nodes:      map[string]observation.ObservedNode{},
		roots:      map[string][]string{},
		files:      make([]string, 0, len(graph.nodes)),
	}
	if w.model.Relationships == nil {
		w.model.Relationships = []intent.RelationshipDeclaration{}
	}

	for _, n := range graph.nodes {
		w.addNode(n)
	}

	return w
}

// addNode declares the node in the model and observes one file for it.
func (w *coreWorld) addNode(n coreNode) {
	tags := n.tags
	if tags == nil {
		tags = []string{}
	}

	w.model.Nodes = append(w.model.Nodes, intent.NodeDeclaration{
		ID: n.id, NodeType: n.nodeType, Name: n.id, Description: n.id, Tags: tags,
		Bindings: []intent.ExtBinding{}, Pointer: "/nodes/" + n.id,
	})

	o := coreObserved(n.id)
	w.observed[n.id] = o
	w.nodes[o.ObservedID] = *o
	w.byObserved[o.ObservedID] = []string{n.id}
	w.roots[n.id] = []string{"src/" + n.id + "/**"}

	w.files = append(w.files, coreFile(n.id))
}

// edges are the observed edges of the graph between the observed nodes of the world.
func (w *coreWorld) edges(graph *coreGraph) []*observation.EvidenceEdge {
	edges := make([]*observation.EvidenceEdge, 0, len(graph.edges))
	for _, e := range graph.edges {
		edges = append(edges, edgeOf(w.observed[e.from], w.observed[e.to], e.kind, e.confidence, 3))
	}

	return edges
}

// compileCoreRule compiles the plan of one rule of the core pack for the model.
func compileCoreRule(t *testing.T, ruleID string, model *intent.ArchitectureModel, caps []observation.CanonicalEdgeKind) []policy.RulePlan {
	t.Helper()

	text, ok := packs.Source("core")
	if !ok {
		t.Fatal("builtin pack core is missing")
	}

	compiled, err := policy.CompilePolicies(
		[]policy.PolicyFile{{Path: "builtin:core", Text: string(text)}}, model,
		policy.ProviderCaps{SupportedCanonicalKinds: caps}, &policy.CompileOverrides{RuleIDs: []string{ruleID}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	if len(compiled.Diagnostics) > 0 || len(compiled.Plans) != 1 {
		t.Fatalf("compile %s: plans %d diagnostics %+v", ruleID, len(compiled.Plans), compiled.Diagnostics)
	}

	return compiled.Plans
}

// evaluateCore compiles the core pack for the graph and evaluates one rule.
func evaluateCore(t *testing.T, ruleID string, graph *coreGraph) conformance.EvaluateOutput {
	t.Helper()

	world := newCoreWorld(graph)
	caps := observation.SupportedCanonicalKinds()
	plans := compileCoreRule(t, ruleID, world.model, caps)

	return conformance.Evaluate(&conformance.EvaluateInput{
		Plans:    plans,
		Snapshot: conformance.Snapshot{Project: "p", GraphGeneration: "cbm:p:0"},
		ReconcileInput: conformance.ReconcileInput{
			Model:      world.model,
			BindingSet: binding.Set{ByObserved: world.byObserved, MatchCounts: map[string]int{}},
			Evidence:   conformance.EvidenceSet{Nodes: world.nodes, Edges: world.edges(graph)},
			CoverageInputs: conformance.CoverageInputs{
				WorkspaceFiles: world.files, Include: []string{"src/**"}, CbmFiles: world.files, MinimumCoverage: 0.95,
			},
			BindingRootsByNode:      world.roots,
			SupportedCanonicalKinds: caps,
		},
	})
}

func findingPairs(out *conformance.EvaluateOutput) []pair {
	pairs := make([]pair, 0, len(out.Findings))
	for i := range out.Findings {
		pairs = append(pairs, pair{out.Findings[i].Source.ArchitectureID, out.Findings[i].Target.ArchitectureID})
	}

	slices.SortFunc(pairs, func(a, b pair) int {
		if a.source != b.source {
			return strings.Compare(a.source, b.source)
		}

		return strings.Compare(a.target, b.target)
	})

	return pairs
}

func TestCoreRuleFiresOnViolation(t *testing.T) {
	t.Parallel()

	for _, tc := range coreCases() {
		t.Run(tc.rule, func(t *testing.T) {
			t.Parallel()

			out := evaluateCore(t, tc.rule, &tc.violating)
			requireCoreViolation(t, &tc, &out)
		})
	}
}

// requireCoreViolation checks that the output has the violation verdict, the findings of the case, and the core pack.
func requireCoreViolation(t *testing.T, tc *coreCase, out *conformance.EvaluateOutput) {
	t.Helper()

	result := &out.RuleResults[0]
	if result.Verdict != conformance.VerdictViolation {
		t.Fatalf("verdict %s, reasons %v", result.Verdict, reasonCodes(result))
	}

	if got := findingPairs(out); !slices.Equal(got, tc.want) {
		t.Fatalf("findings %v, want %v", got, tc.want)
	}

	for i := range out.Findings {
		requireCoreFinding(t, tc, &out.Findings[i])
	}

	if result.Policy.ID != "core" || result.Policy.Version != "1.0.0" {
		t.Errorf("policy %+v", result.Policy)
	}
}

// requireCoreFinding checks the rule, severity, kind, and disposition of one finding of a case.
func requireCoreFinding(t *testing.T, tc *coreCase, f *conformance.Finding) {
	t.Helper()

	type findingShape struct {
		rule        string
		severity    policy.RuleSeverity
		kind        conformance.FindingKind
		disposition string
	}

	got := findingShape{f.RuleID, f.Severity, f.Kind, string(f.Disposition)}
	if got != (findingShape{tc.rule, tc.severity, tc.kind, "active"}) {
		t.Errorf("finding %+v", f)
	}
}

func TestCoreRuleIsQuietOnCleanGraph(t *testing.T) {
	t.Parallel()

	for _, tc := range coreCases() {
		t.Run(tc.rule, func(t *testing.T) {
			t.Parallel()

			out := evaluateCore(t, tc.rule, &tc.clean)
			result := &out.RuleResults[0]

			if result.Verdict != conformance.VerdictPass || len(out.Findings) != 0 {
				t.Fatalf("verdict %s, findings %v, reasons %v", result.Verdict, findingPairs(&out), reasonCodes(result))
			}
		})
	}
}

// Tag conventions carry the meaning of most rules: a model without tags gives no finding.
func TestTagDrivenCoreRulesAreInertWithoutTags(t *testing.T) {
	t.Parallel()

	for _, tc := range coreCases() {
		if !tc.tagDriven {
			continue
		}

		t.Run(tc.rule, func(t *testing.T) {
			t.Parallel()

			stripped := tc.violating.withoutTags()
			out := evaluateCore(t, tc.rule, &stripped)
			result := &out.RuleResults[0]

			if len(out.Findings) != 0 || result.Verdict != conformance.VerdictNotApplicable {
				t.Fatalf("verdict %s, findings %v", result.Verdict, findingPairs(&out))
			}
		})
	}
}

func TestCoreObservedRequiredIgnoresOptionalRelationships(t *testing.T) {
	t.Parallel()

	optional := intent.RelationshipDeclaration{
		ID: "r-a-b", Kind: intent.RelConnects, Source: "a", Targets: []string{"b"}, Pointer: "/relationships/r-a-b",
		Observation: &intent.RelationshipObservation{Required: false},
	}

	out := evaluateCore(t, "CORE-OBS-001", &coreGraph{
		nodes: []coreNode{cn("a", "service"), cn("b", "service")},
		rels:  []intent.RelationshipDeclaration{optional},
	})

	if out.RuleResults[0].Verdict != conformance.VerdictPass || len(out.Findings) != 0 {
		t.Fatalf("verdict %s, findings %v", out.RuleResults[0].Verdict, findingPairs(&out))
	}
}

func TestCoreSecurityRuleCountsHeuristicEdgeAlone(t *testing.T) {
	t.Parallel()

	out := evaluateCore(t, "CORE-SEC-001", &coreGraph{
		nodes: []coreNode{cn("pub", "service", "public"), cn("sec", "service", "restricted")},
		edges: []coreEdge{kindEdge("pub", "sec", observation.KindCallsService, observation.ConfidenceHeuristic)},
	})

	if out.RuleResults[0].Verdict != conformance.VerdictViolation || len(out.Findings) != 1 {
		t.Fatalf("verdict %s, findings %v", out.RuleResults[0].Verdict, findingPairs(&out))
	}
}

func TestCoreDeclaredRuleRejectsKindThatTheRelationshipDoesNotAllow(t *testing.T) {
	t.Parallel()

	narrow := intent.RelationshipDeclaration{
		ID: "r-api-db", Kind: intent.RelConnects, Source: "api", Targets: []string{"db"}, Pointer: "/relationships/r-api-db",
		Observation: &intent.RelationshipObservation{AllowedEdgeKinds: []observation.CanonicalEdgeKind{observation.KindReads}},
	}

	out := evaluateCore(t, "CORE-DECL-001", &coreGraph{
		nodes: []coreNode{cn("api", "service"), cn("db", "database")},
		rels:  []intent.RelationshipDeclaration{narrow},
		edges: []coreEdge{kindEdge("api", "db", observation.KindWrites, observation.ConfidenceSyntactic)},
	})

	if len(out.Findings) != 1 || out.Findings[0].Kind != conformance.KindContradiction {
		t.Fatalf("findings %+v", out.Findings)
	}
}
