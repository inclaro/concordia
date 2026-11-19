package observation_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

var (
	testAnalyzer = observation.AnalyzerDescriptor{Name: "codebase-memory-mcp", Version: "0.12.1", AdapterSchema: observation.AdapterSchema}
	fromNode     = observation.ObservedNode{ObservedID: "cbm:p:1", Label: "Function", QualifiedName: "a.f", FilePath: "src/a.ts", StartLine: 3}
	toNode       = observation.ObservedNode{ObservedID: "cbm:p:2", Label: "Function", QualifiedName: "b.g", FilePath: "src/b.ts", StartLine: 9}
)

func norm(tb testing.TB, nativeKind string, props map[string]string) (*observation.EvidenceEdge, bool) {
	tb.Helper()

	return observation.NormalizeEdge(&observation.NormalizeInput{
		NativeKind:      nativeKind,
		From:            fromNode,
		To:              toNode,
		Properties:      props,
		GraphGeneration: "cbm:p:0",
		Analyzer:        testAnalyzer,
	})
}

func mustNorm(tb testing.TB, nativeKind string, props map[string]string) *observation.EvidenceEdge {
	tb.Helper()

	e, ok := norm(tb, nativeKind, props)
	if !ok {
		tb.Fatalf("%s is not mapped", nativeKind)
	}

	return e
}

// endpoint is one end of a test edge: a label, the file of the node, and the node `kind`.
type endpoint struct {
	label, file, kind string
}

func (e endpoint) node(id string) observation.ObservedNode {
	return observation.ObservedNode{ObservedID: "cbm:p:" + id, Label: e.label, QualifiedName: "n" + id, FilePath: e.file, Kind: e.kind}
}

// normCase is a native edge shape: the native type, its properties, and its two endpoints.
type normCase struct {
	native   string
	props    map[string]string
	from, to endpoint
}

func normBetween(tb testing.TB, c *normCase) *observation.EvidenceEdge {
	tb.Helper()

	e, ok := observation.NormalizeEdge(&observation.NormalizeInput{
		NativeKind: c.native, From: c.from.node("1"), To: c.to.node("2"), Properties: c.props,
		GraphGeneration: "cbm:p:0", Analyzer: testAnalyzer,
	})
	if !ok {
		tb.Fatalf("%s is not mapped", c.native)
	}

	return e
}

func TestAdapterSchemaAndNormalizationVersion(t *testing.T) {
	if observation.AdapterSchema != "concordia-cbm/v2" {
		t.Fatalf("adapter schema = %s", observation.AdapterSchema)
	}

	if observation.NormalizationVersion != "2.1" {
		t.Fatalf("normalization version = %s", observation.NormalizationVersion)
	}
}

func TestNormalizeConflictNamesBothDiscriminators(t *testing.T) {
	e := normBetween(t, &normCase{
		native: "USAGE", props: map[string]string{"via": "terraform", "ref": "env"},
		from: endpoint{label: "Class", file: "terraform/main.tf"}, to: endpoint{label: "Resource", kind: "Secret"},
	})

	reason := e.Facets["ambiguity"]

	for _, want := range []string{"via=terraform", "ref=env"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("ambiguity = %q", reason)
		}
	}
}

func TestOccurrenceAndConfidenceStayApart(t *testing.T) {
	e := normBetween(t, &normCase{
		native: "USAGE", props: map[string]string{"ref": "image", "container": "sidecar", "image": "r/api:1.3.0"},
		from: endpoint{label: "Resource", file: "k8s/d.yaml", kind: "Deployment"}, to: endpoint{label: "Image"},
	})

	if e.Occurrence != "sidecar" {
		t.Fatalf("occurrence %q confidence %s", e.Occurrence, e.Confidence)
	}

	if e.Confidence != observation.ConfidenceSyntactic {
		t.Fatalf("occurrence %q confidence %s", e.Occurrence, e.Confidence)
	}

	// A resolved confidence does not decide the role (EVD-03/EVD-08).
	calls := mustNorm(t, "CALLS", map[string]string{"strategy": "lsp_direct"})
	if calls.Confidence != observation.ConfidenceResolved {
		t.Fatalf("calls = %+v", calls)
	}

	if calls.Role != observation.RoleParsedSourceFact {
		t.Fatalf("calls = %+v", calls)
	}

	tests := normBetween(t, &normCase{native: "TESTS", from: endpoint{label: "Function", file: "a_test.go"}, to: endpoint{label: "Function", file: "a.go"}})
	if tests.Role != observation.RoleCandidateVerificationArtifact {
		t.Fatalf("TESTS role = %s", tests.Role)
	}
}

func TestEnvironmentVariableValuesAreNeverFacts(t *testing.T) {
	e := normBetween(t, &normCase{
		native: "CONFIGURES", props: map[string]string{"strategy": "env_set", "container": "app", "source": "value", "line": "23"},
		from: endpoint{label: "Resource", file: "k8s/d.yaml", kind: "Deployment"}, to: endpoint{label: "EnvVar"},
	})

	if _, has := e.Properties["value"]; has {
		t.Fatalf("properties = %v", e.Properties)
	}

	if len(e.Facets) != 0 {
		t.Fatalf("facets = %v", e.Facets)
	}
}

func TestUnsupportedFact(t *testing.T) {
	e := observation.NormalizeUnsupported(&observation.NormalizeInput{
		NativeKind: "HAS_BRANCH", From: fromNode, To: toNode, Properties: map[string]string{"branch": "main", "head_sha": ""},
		GraphGeneration: "cbm:p:0", Analyzer: testAnalyzer,
	})

	got := [4]string{string(e.Kind), e.NativeKind, e.From, e.To}
	if got != [4]string{string(observation.KindUnsupported), "HAS_BRANCH", fromNode.ObservedID, toNode.ObservedID} {
		t.Fatalf("fact = %+v", e)
	}

	if !reflect.DeepEqual(e.Properties, map[string]string{"branch": "main"}) {
		t.Fatalf("properties = %v", e.Properties)
	}

	if e.SourceSpan == nil {
		t.Fatalf("fact = %+v", e)
	}

	if e.SourceSpan.Path != "src/a.ts" || e.NormalizationVersion != "2.1" {
		t.Fatalf("fact = %+v", e)
	}

	if observation.IsMappedNative("HAS_BRANCH") {
		t.Fatal("IsMappedNative is wrong")
	}

	if !observation.IsMappedNative("DEPLOYS") {
		t.Fatal("IsMappedNative is wrong")
	}
}

func normExpectFileTypes(t *testing.T) {
	t.Helper()

	files := map[string]string{
		"src/a.go": ".go", "docker/Dockerfile": "Dockerfile", "docker/Dockerfile.dev": "Dockerfile", "api.dockerfile": "Dockerfile",
		"Makefile": "Makefile", "kustomize/base/kustomization.yaml": "kustomization.yaml", "k8s/a.yaml": ".yaml", "": "",
		"terraform/prod.tfvars": ".tfvars",
	}

	for file, want := range files {
		if got := observation.FileTypeOf(file); got != want {
			t.Errorf("FileTypeOf(%q) = %q, want %q", file, got, want)
		}
	}
}

func normExpectLabelClasses(t *testing.T) {
	t.Helper()

	classes := []struct {
		label, file string
		want        observation.LabelClass
	}{
		{"Function", "src/a.go", observation.ClassCode},
		{"File", "src/a.ts", observation.ClassCode},
		{"Function", "", observation.ClassCode},
		{"Module", "docker/Dockerfile", observation.ClassInfrastructure},
		{"Class", "terraform/main.tf", observation.ClassInfrastructure},
		{"Module", ".github/workflows/ci.yaml", observation.ClassInfrastructure},
		{"Function", "Makefile", observation.ClassInfrastructure},
		{"Resource", "", observation.ClassInfrastructure},
		{"Image", "", observation.ClassInfrastructure},
		{"Section", "docs/a.md", observation.ClassDocumentation},
		{"ADR", "docs/adr/1.md", observation.ClassDocumentation},
		{"File", "docs/a.md", observation.ClassDocumentation},
		{"Diagram", "docs/a.md", observation.ClassDiagram},
		{"DiagramElement", "docs/a.md", observation.ClassDiagram},
	}

	for _, c := range classes {
		if got := observation.LabelClassOf(c.label, c.file); got != c.want {
			t.Errorf("LabelClassOf(%q, %q) = %s, want %s", c.label, c.file, got, c.want)
		}
	}
}

func TestLabelClassAndFileType(t *testing.T) {
	normExpectFileTypes(t)
	normExpectLabelClasses(t)
}

func TestNativeToCanonicalTable(t *testing.T) {
	rows := []struct {
		native, kind, confidence, method string
	}{
		{"IMPORTS", "imports", "syntactic", "cbm:IMPORTS"},
		{"CALL_REFERENCE", "references-callable", "resolved", "cbm:CALL_REFERENCE"},
		{"USAGE", "ambiguous-usage", "heuristic", "cbm:USAGE"},
		{"INHERITS", "inherits", "resolved", "cbm:INHERITS"},
		{"IMPLEMENTS", "implements", "resolved", "cbm:IMPLEMENTS"},
		{"DEFINES", "defines", "resolved", "cbm:DEFINES"},
		{"DEFINES_METHOD", "defines", "resolved", "cbm:DEFINES_METHOD"},
		{"CONTAINS_FILE", "contains", "resolved", "cbm:CONTAINS_FILE"},
		{"CONTAINS_FOLDER", "contains", "resolved", "cbm:CONTAINS_FOLDER"},
		{"HTTP_CALLS", "calls-service", "heuristic", "cbm:HTTP_CALLS"},
		{"ASYNC_CALLS", "dispatches-async", "heuristic", "cbm:ASYNC_CALLS"},
		{"EMITS", "emits-event", "heuristic", "cbm:EMITS"},
		{"LISTENS_ON", "listens-event", "heuristic", "cbm:LISTENS_ON"},
		{"DATA_FLOWS", "data-flows", "heuristic", "cbm:DATA_FLOWS"},
		{"READS", "reads", "syntactic", "cbm:READS"},
		{"WRITES", "writes", "syntactic", "cbm:WRITES"},
		{"CONFIGURES", "configures-env-access", "syntactic", "cbm:CONFIGURES"},
		{"HANDLES", "handles-route", "heuristic", "cbm:HANDLES"},
		{"CROSS_HTTP_CALLS", "calls-service", "heuristic", "cbm:CROSS_HTTP_CALLS"},
		{"CROSS_ASYNC_CALLS", "dispatches-async", "heuristic", "cbm:CROSS_ASYNC_CALLS"},
		{"CROSS_CHANNEL", "emits-event", "heuristic", "cbm:CROSS_CHANNEL"},
	}

	for _, r := range rows {
		t.Run(r.native, func(t *testing.T) {
			props := map[string]string{}
			if r.native == "CONFIGURES" {
				props["strategy"] = "env_access"
			}

			e := mustNorm(t, r.native, props)

			got := [4]string{string(e.Kind), string(e.Confidence), e.ResolutionMethod, e.NativeKind}
			if got != [4]string{r.kind, r.confidence, r.method, r.native} {
				t.Fatalf("edge = %+v", e)
			}
		})
	}
}

func TestNativeKindsOfCanonical(t *testing.T) {
	cases := map[observation.CanonicalEdgeKind][]string{
		observation.KindImports:                  {"IMPORTS"},
		observation.KindDefines:                  {"DEFINES", "DEFINES_METHOD"},
		observation.KindUsesType:                 {},
		observation.KindDeploysArtifact:          {"DEPLOYS"},
		observation.KindCallsService:             {"CROSS_HTTP_CALLS", "HTTP_CALLS"},
		observation.KindAmbiguousUsage:           {"USAGE"},
		observation.KindReferencesSecret:         {"USAGE"},
		observation.KindSupersedes:               {"SUPERSEDES"},
		observation.KindInfrastructureDependency: {"DEPENDS_ON", "USAGE"},
	}

	for kind, want := range cases {
		if got := observation.NativeKindsOfCanonical(kind); !reflect.DeepEqual(got, want) {
			t.Errorf("NativeKindsOfCanonical(%s) = %v, want %v", kind, got, want)
		}
	}
}

func TestConfidenceRankAndKinds(t *testing.T) {
	order := []observation.Confidence{
		observation.ConfidenceUnknown, observation.ConfidenceHeuristic, observation.ConfidenceSyntactic,
		observation.ConfidenceRuntime, observation.ConfidenceResolved,
	}
	for i, c := range order {
		if c.Rank() != i {
			t.Errorf("Rank(%s) = %d, want %d", c, c.Rank(), i)
		}
	}

	if !observation.IsCanonicalEdgeKind("calls-service") || observation.IsCanonicalEdgeKind("CALLS") {
		t.Error("IsCanonicalEdgeKind is wrong")
	}
}

func TestObservedNodes(t *testing.T) {
	t.Run("removes the project prefix and parses labels", func(t *testing.T) {
		if got := observation.StripProjectPrefix("proj", "proj.src.a.f"); got != "src.a.f" {
			t.Fatalf("got %s", got)
		}

		if got := observation.StripProjectPrefix("proj", "other.src"); got != "other.src" {
			t.Fatalf("got %s", got)
		}

		n := observation.ToObservedNode("proj", &observation.NodeColumns{
			ID: "7", Labels: `["Module"]`, QualifiedName: "proj.src.b", FilePath: "src/b.ts", StartLine: "1",
		}, "")

		want := observation.ObservedNode{ObservedID: "cbm:proj:7", Label: "Module", QualifiedName: "src.b", FilePath: "src/b.ts", StartLine: 1}
		if !reflect.DeepEqual(n, want) {
			t.Fatalf("node = %+v", n)
		}
	})

	t.Run("start line 0 is no line", func(t *testing.T) {
		n := observation.ToObservedNode("p", &observation.NodeColumns{ID: "1", Labels: `["File"]`, QualifiedName: "p.a", FilePath: "a", StartLine: "0"}, "")
		if n.StartLine != 0 {
			t.Fatalf("start line = %d", n.StartLine)
		}
	})

	t.Run("a label that is not JSON is one label; the fallback is used without labels", func(t *testing.T) {
		if got := observation.ToObservedNode("p", &observation.NodeColumns{ID: "1", Labels: "Module"}, "").Label; got != "Module" {
			t.Fatalf("label = %s", got)
		}

		if got := observation.ToObservedNode("p", &observation.NodeColumns{ID: "1", Labels: nil}, "File").Label; got != "File" {
			t.Fatalf("fallback label = %s", got)
		}
	})
}
