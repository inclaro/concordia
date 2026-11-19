package conformance_test

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

func node(id string) intent.NodeDeclaration {
	return intent.NodeDeclaration{
		ID: id, NodeType: "service", Name: id, Description: id,
		Tags: []string{}, Bindings: []intent.ExtBinding{}, Pointer: "/nodes/" + id,
	}
}

func connects(id, source, target string, obs *intent.RelationshipObservation) intent.RelationshipDeclaration {
	return intent.RelationshipDeclaration{
		ID: id, Kind: intent.RelConnects, Source: source, Targets: []string{target},
		Pointer: "/relationships/" + id, Observation: obs,
	}
}

func obs(qn, filePath string) *observation.ObservedNode {
	return &observation.ObservedNode{ObservedID: "cbm:p:" + qn, Label: "Module", QualifiedName: qn, FilePath: filePath, StartLine: 1}
}

var edgeSeq atomic.Int64

func edge(from, to *observation.ObservedNode) *observation.EvidenceEdge {
	return edgeOf(from, to, observation.KindImports, observation.ConfidenceSyntactic, 3)
}

func edgeOf(from, to *observation.ObservedNode, kind observation.CanonicalEdgeKind, confidence observation.Confidence, line int) *observation.EvidenceEdge {
	native := "CALLS"
	if kind == observation.KindImports {
		native = "IMPORTS"
	}

	return &observation.EvidenceEdge{
		ID:               "e" + strconv.FormatInt(edgeSeq.Add(1), 10),
		From:             from.ObservedID,
		To:               to.ObservedID,
		Kind:             kind,
		NativeKind:       native,
		Confidence:       confidence,
		ResolutionMethod: "cbm:" + string(kind),
		SourceSpan:       &observation.SourceSpan{Path: from.FilePath, Line: line},
		Analyzer:         observation.AnalyzerDescriptor{Name: "t", Version: "0", AdapterSchema: "concordia-cbm/v2"},
		GraphGeneration:  "g",
		Properties:       map[string]string{},
	}
}

func plan(predicate policy.Predicate, edit func(*policy.RulePlan)) policy.RulePlan { //nolint:gocritic // a test helper; the predicate is a small literal.
	p := policy.RulePlan{
		RuleID:   "R-1",
		Name:     "rule",
		Severity: policy.SeverityError,
		Pack:     policy.PackRef{ID: "p", Version: "1"},
		Subjects: []string{},
		Targets:  []string{},
		Traversal: policy.Traversal{
			EdgeKinds:    []observation.CanonicalEdgeKind{observation.KindImports, observation.KindCalls},
			MinimumDepth: 1, MaximumDepth: 1, Direction: "outbound",
		},
		Predicate:            predicate,
		EvidenceRequirements: policy.EvidenceRequirements{MinimumConfidence: observation.ConfidenceSyntactic, MinimumCoverage: 0.9, OnUnknown: policy.UnknownFail},
		UnsupportedEdgeKinds: []observation.CanonicalEdgeKind{},
		Aggregation:          "edge",
	}
	if edit != nil {
		edit(&p)
	}

	return p
}

var roots = map[string][]string{
	"billing":     {"services/billing/**"},
	"fulfillment": {"services/fulfillment/**", "packages/fulfillment-contract/**"},
	"ledger":      {"services/ledger/**"},
}

var fileToNode = [][2]string{
	{"services/billing/", "billing"},
	{"services/fulfillment/", "fulfillment"},
	{"packages/fulfillment-contract/", "fulfillment"},
	{"services/ledger/", "ledger"},
}

type scenario struct {
	sizes         map[string]int64
	nodes         []intent.NodeDeclaration
	relationships []intent.RelationshipDeclaration
	observed      []*observation.ObservedNode
	edges         []*observation.EvidenceEdge
	plans         []policy.RulePlan
	extraFiles    []string
	cbmFiles      []string
	dirty         []string
	exclude       []string
}

func build(s *scenario) *conformance.EvaluateInput {
	nodes := s.nodes
	if nodes == nil {
		billing := node("billing")
		billing.Owner = "team-billing"
		nodes = []intent.NodeDeclaration{billing, node("fulfillment"), node("ledger")}
	}

	model := &intent.ArchitectureModel{
		SourceURI: "architecture/a.calm.json", Digest: "sha256:0", SchemaVersion: "1.2",
		Nodes: nodes, Relationships: s.relationships, Controls: []intent.ControlAttachment{}, Metadata: map[string]any{},
	}

	byObserved := map[string][]string{}
	files := map[string]struct{}{}
	nodeMap := map[string]observation.ObservedNode{}
	observedFiles := make([]string, 0, len(s.observed))

	for _, o := range s.observed {
		nodeMap[o.ObservedID] = *o
		files[o.FilePath] = struct{}{}
		observedFiles = append(observedFiles, o.FilePath)

		for _, h := range fileToNode {
			if strings.HasPrefix(o.FilePath, h[0]) {
				byObserved[o.ObservedID] = []string{h[1]}

				break
			}
		}
	}

	workspaceFiles := make([]string, 0, len(files)+len(s.extraFiles))

	for f := range files {
		workspaceFiles = append(workspaceFiles, f)
	}

	workspaceFiles = append(workspaceFiles, s.extraFiles...)

	cbmFiles := s.cbmFiles
	if cbmFiles == nil {
		cbmFiles = observedFiles
	}

	return &conformance.EvaluateInput{
		Plans:    s.plans,
		Snapshot: conformance.Snapshot{Project: "p", GraphGeneration: "cbm:p:0"},
		ReconcileInput: conformance.ReconcileInput{
			Model:      model,
			BindingSet: binding.Set{ByObserved: byObserved, MatchCounts: map[string]int{}},
			Evidence:   conformance.EvidenceSet{Nodes: nodeMap, Edges: s.edges},
			CoverageInputs: conformance.CoverageInputs{
				WorkspaceFiles:  workspaceFiles,
				Include:         []string{"services/**", "packages/**"},
				Exclude:         s.exclude,
				CbmFiles:        cbmFiles,
				DirtyFiles:      s.dirty,
				Sizes:           s.sizes,
				MinimumCoverage: 0.9,
			},
			BindingRootsByNode: roots,
			SupportedCanonicalKinds: []observation.CanonicalEdgeKind{
				observation.KindImports, observation.KindCalls, observation.KindInherits, observation.KindContains, observation.KindDefines,
			},
		},
	}
}

type (
	diagCode   = diag.Diagnostic
	workspaceT = workspace.Workspace
	loggerT    = logger.Logger
)

func policyUnknown(s string) policy.UnknownDisposition { return policy.UnknownDisposition(s) }
