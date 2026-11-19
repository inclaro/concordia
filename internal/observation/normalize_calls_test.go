package observation_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

// normSubtest is one named case of a test whose body is a function.
type normSubtest struct {
	run  func(t *testing.T)
	name string
}

func normRunSubtests(t *testing.T, cases []normSubtest) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func normExpectConfidence(t *testing.T, what string, got, want observation.Confidence) {
	t.Helper()

	if got != want {
		t.Fatalf("%s: confidence = %s, want %s", what, got, want)
	}
}

func normExpectSourceSpan(t *testing.T, e *observation.EvidenceEdge, want observation.SourceSpan) {
	t.Helper()

	if e.SourceSpan == nil {
		t.Fatalf("source span = %+v", e.SourceSpan)
	}

	if *e.SourceSpan != want {
		t.Fatalf("source span = %+v", e.SourceSpan)
	}
}

func TestNormalizeCalls(t *testing.T) {
	normRunSubtests(t, []normSubtest{
		{name: "strategy lsp_* is resolved", run: normCallsLSPResolved},
		{name: "lsp_ts_import (seen in real CBM data) is resolved", run: normCallsLSPTSImportResolved},
		{name: "numeric confidence 0.5 or more is syntactic, lower is heuristic", run: normCallsNumericConfidence},
		{name: "CALLS without a strategy has the plain resolution method", run: normCallsPlainMethod},
	})
}

func normCallsLSPResolved(t *testing.T) {
	t.Helper()

	e := mustNorm(t, "CALLS", map[string]string{"strategy": "lsp_direct", "confidence": "0.2", "line": "12"})

	got := [3]string{string(e.Kind), string(e.Confidence), e.ResolutionMethod}
	if got != [3]string{string(observation.KindCalls), string(observation.ConfidenceResolved), "cbm:CALLS:lsp_direct"} {
		t.Fatalf("edge = %+v", e)
	}

	normExpectSourceSpan(t, e, observation.SourceSpan{Path: "src/a.ts", Line: 12})
}

func normCallsLSPTSImportResolved(t *testing.T) {
	t.Helper()

	got := mustNorm(t, "CALLS", map[string]string{"strategy": "lsp_ts_import", "confidence": "0.95"}).Confidence

	normExpectConfidence(t, "lsp_ts_import", got, observation.ConfidenceResolved)
}

func normCallsNumericConfidence(t *testing.T) {
	t.Helper()

	syntactic := mustNorm(t, "CALLS", map[string]string{"strategy": "suffix_match", "confidence": "0.5"}).Confidence
	heuristic := mustNorm(t, "CALLS", map[string]string{"strategy": "suffix_match", "confidence": "0.28"}).Confidence

	normExpectConfidence(t, "0.5", syntactic, observation.ConfidenceSyntactic)
	normExpectConfidence(t, "0.28", heuristic, observation.ConfidenceHeuristic)
	normExpectConfidence(t, "no values", observation.CallsConfidence("", ""), observation.ConfidenceHeuristic)
	normExpectConfidence(t, "x", observation.CallsConfidence("suffix_match", "x"), observation.ConfidenceHeuristic)
}

func normCallsPlainMethod(t *testing.T) {
	t.Helper()

	if got := mustNorm(t, "CALLS", nil).ResolutionMethod; got != "cbm:CALLS" {
		t.Fatalf("method = %s", got)
	}
}

func TestNormalizeEdgeDetails(t *testing.T) {
	normRunSubtests(t, []normSubtest{
		{name: "the line falls back to the start line of the source node", run: normDetailLineFallback},
		{name: "a source node without a path gives no source span", run: normDetailNoPathNoSpan},
		{name: "copies the other edge columns as strings and drops empty ones", run: normDetailCopiesColumns},
		{name: "an unknown native type gives nothing", run: normDetailUnknownNative},
		{name: "supports the infrastructure kinds but not uses-type or constructs", run: normDetailSupportedKinds},
		{name: "makes stable ids", run: normDetailStableIDs},
	})
}

func normDetailLineFallback(t *testing.T) {
	t.Helper()

	normExpectSourceSpan(t, mustNorm(t, "IMPORTS", nil), observation.SourceSpan{Path: "src/a.ts", Line: 3})
}

func normDetailNoPathNoSpan(t *testing.T) {
	t.Helper()

	in := &observation.NormalizeInput{NativeKind: "IMPORTS", From: observation.ObservedNode{ObservedID: "cbm:p:9"}, To: toNode}
	if e, _ := observation.NormalizeEdge(in); e.SourceSpan != nil {
		t.Fatalf("source span = %+v", e.SourceSpan)
	}
}

func normDetailCopiesColumns(t *testing.T) {
	t.Helper()

	e := mustNorm(t, "CALLS", map[string]string{"strategy": "lsp_direct", "args": "", "callee": "g", "line": "4"})

	want := map[string]string{"callee": "g", "line": "4", "strategy": "lsp_direct"}
	if !reflect.DeepEqual(e.Properties, want) {
		t.Fatalf("properties = %v", e.Properties)
	}
}

func normDetailUnknownNative(t *testing.T) {
	t.Helper()

	if _, ok := norm(t, "HAS_BRANCH", nil); ok {
		t.Fatal("HAS_BRANCH is mapped")
	}
}

func normDetailSupportedKinds(t *testing.T) {
	t.Helper()

	supported := observation.SupportedCanonicalKinds()

	for _, k := range []observation.CanonicalEdgeKind{observation.KindUsesType, observation.KindConstructs, observation.KindUnsupported, observation.KindAmbiguous} {
		if slices.Contains(supported, k) {
			t.Fatalf("%s is supported", k)
		}
	}

	for _, k := range []observation.CanonicalEdgeKind{observation.KindImports, observation.KindDeploysArtifact, observation.KindReferencesSecret, observation.KindTests} {
		if !slices.Contains(supported, k) {
			t.Fatalf("%s is not supported", k)
		}
	}

	if !slices.IsSorted(supported) {
		t.Fatalf("supported kinds are not sorted: %v", supported)
	}
}

func normDetailStableIDs(t *testing.T) {
	t.Helper()

	first, second := mustNorm(t, "IMPORTS", nil), mustNorm(t, "IMPORTS", nil)
	if first.ID != second.ID {
		t.Fatal("id is not stable")
	}

	if mustNorm(t, "IMPORTS", nil).ID == mustNorm(t, "CALLS", nil).ID {
		t.Fatal("ids of different edges are equal")
	}
}
