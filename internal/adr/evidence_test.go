package adr_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// fetchGraph freezes the fake graph and gives the evidence of a decision-governance fetch with the capabilities.
func fetchGraph(t *testing.T, g *testutil.FakeGraph) (observation.EvidenceQueryResult, observation.Capabilities) {
	t.Helper()

	client := observation.NewFakeClient(g.Handler, "")
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: g.Project})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"DEFINES", "SUPERSEDES", "MENTIONS", "REFERENCES_FILE"})
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	return res, provider.Capabilities(t.Context())
}

func supersedesProps() map[string]string {
	return map[string]string{"via": "markdown", "syntax": "supersedes", "tier": "exact", "line": "7", "count": "1"}
}

// evidenceDecisionPaths gives the paths of the decisions of the evidence, in order.
func evidenceDecisionPaths(ev *adr.DecisionEvidence) []string {
	paths := make([]string, 0, len(ev.Decisions))
	for i := range ev.Decisions {
		paths = append(paths, ev.Decisions[i].Path)
	}

	return paths
}

// evidenceWantValue stops the test when the value differs from the wanted one.
func evidenceWantValue[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
}

// evidenceWantSlice stops the test when the slice differs from the wanted one.
func evidenceWantSlice[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
}

// decisionPropertiesEvidence builds the evidence of a graph with a superseded decision, its successor, a lone
// decision, and the mentions of a code file.
func decisionPropertiesEvidence(t *testing.T) adr.DecisionEvidence {
	t.Helper()

	older := testutil.ADRNode("1", "docs/adr/0003-self-hosted.md", map[string]string{
		"adr_id": "ADR-3", "title": "ADR-0003: Self hosted", "status": "superseded", "status_text": "Superseded by [ADR-0007](0007-managed.md)",
		"date": "2026-01-10", "deciders": "Ada", "superseded_by": "0007-managed.md",
	})
	newer := testutil.ADRNode("2", "docs/adr/0007-managed.md", map[string]string{
		"adr_id": "ADR-7", "title": "ADR-0007: Managed", "status": "accepted", "status_text": "Accepted", "date": "2026-03-02",
	})
	lone := testutil.ADRNode("3", "docs/adr/0009-lone.md", map[string]string{"adr_id": "ADR-9", "status": "proposed", "status_text": "Proposed"})
	section := testutil.InfraNode("4", "Section", "docs.adr.0007-managed.Context", "docs/adr/0007-managed.md", nil)
	code := testutil.FileNode("5", "terraform/main.tf")

	edges := []testutil.FakeEdge{
		testutil.NewEdge(&newer, &older, "SUPERSEDES", supersedesProps()),
		testutil.NewEdge(&section, &code, "MENTIONS", map[string]string{"syntax": "code_path", "via": "markdown", "tier": "exact", "line": "9", "count": "1"}),
		testutil.NewEdge(&section, &code, "REFERENCES_FILE", map[string]string{"strategy": "md_backtick_path", "confidence": "0.9", "count": "1"}),
	}

	res, caps := fetchGraph(t, testutil.NewFakeGraph("p", []testutil.FakeNode{older, newer, lone, section, code}, edges))

	return adr.BuildEvidence(&res, caps, nil)
}

func TestBuildEvidenceKeepsDecisionPropertiesAndDirection(t *testing.T) {
	ev := decisionPropertiesEvidence(t)

	evidenceWantValue(t, "supported", ev.Supported, true)
	evidenceWantValue(t, "extraction complete", ev.ExtractionComplete, true)
	evidenceWantValue(t, "unresolved supersedes observable", ev.UnresolvedSupersedesObservable, false)

	want := []string{"docs/adr/0003-self-hosted.md", "docs/adr/0007-managed.md", "docs/adr/0009-lone.md"}
	evidenceWantSlice(t, "decision paths (a decision with no edge must be kept)", evidenceDecisionPaths(&ev), want)

	wantDecision := adr.EvidenceDecision{
		Path: "docs/adr/0003-self-hosted.md", ObservedID: "cbm:p:1", AdrID: "ADR-3", Label: "ADR-3", Title: "ADR-0003: Self hosted",
		Status: "superseded", StatusText: "Superseded by [ADR-0007](0007-managed.md)", Date: "2026-01-10", Deciders: "Ada",
		SupersededBy: "0007-managed.md", Line: 1,
	}
	evidenceWantValue(t, "decision", ev.Decisions[0], wantDecision)

	wantEdges := []adr.SupersedesEdge{{From: "docs/adr/0007-managed.md", To: "docs/adr/0003-self-hosted.md", Line: 7}}
	evidenceWantSlice(t, "supersedes (new to old)", ev.Supersedes, wantEdges)

	wantMentions := []adr.Mention{
		{From: "docs/adr/0007-managed.md", Subject: "terraform/main.tf", Kind: "mentions"},
		{From: "docs/adr/0007-managed.md", Subject: "terraform/main.tf", Kind: "references-file"},
	}
	evidenceWantSlice(t, "mentions", ev.Mentions, wantMentions)
}

func TestBuildEvidenceIsUnsupportedWithoutTheSupersedesFamily(t *testing.T) {
	node := testutil.ADRNode("1", "docs/adr/0001-a.md", map[string]string{"adr_id": "ADR-1", "status": "accepted"})
	res := observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{
		"cbm:p:1": {ObservedID: "cbm:p:1", Label: "ADR", FilePath: node.Path, StartLine: 1, Properties: node.Props},
	}}

	for name, caps := range map[string]observation.Capabilities{
		"no profile": {},
		"family unsupported": {Families: map[string]observation.FamilyState{
			"supersedes": {Level: observation.SupportUnsupported},
		}},
		"other families only": {Families: map[string]observation.FamilyState{
			"mentions": {Level: observation.SupportSupported},
		}},
	} {
		ev := adr.BuildEvidence(&res, caps, nil)
		if ev.Supported || ev.ExtractionComplete {
			t.Fatalf("%s: supported %v complete %v, want false", name, ev.Supported, ev.ExtractionComplete)
		}
	}

	supported := adr.BuildEvidence(&res, observation.Capabilities{Families: map[string]observation.FamilyState{
		"supersedes": {Level: observation.SupportSupported, NotPresent: true},
	}}, nil)
	if !supported.Supported || len(supported.Decisions) != 1 {
		t.Fatalf("a family with no edge in the graph is still supported: %+v", supported)
	}
}

// The pipeline prefixes the paths of every project other than the primary one; BuildEvidence keeps those paths and
// reads the project from the observed ID.
func TestBuildEvidenceKeepsTheProjectPathsOfThePipeline(t *testing.T) {
	res := observation.EvidenceQueryResult{
		Nodes: map[string]observation.ObservedNode{
			"cbm:main:1": {ObservedID: "cbm:main:1", Label: "ADR", FilePath: "docs/adr/0001-a.md", Properties: map[string]string{"adr_id": "ADR-1"}},
			"cbm:lib:1":  {ObservedID: "cbm:lib:1", Label: "ADR", FilePath: "@lib/docs/adr/0001-a.md", Properties: map[string]string{"adr_id": "ADR-0001"}},
			"cbm:lib:2":  {ObservedID: "cbm:lib:2", Label: "ADR", FilePath: "@lib/docs/adr/0002-b.md", Properties: map[string]string{"adr_id": "ADR-2"}},
		},
		Edges: []*observation.EvidenceEdge{
			{From: "cbm:lib:2", To: "cbm:lib:1", Kind: observation.KindSupersedes, NativeKind: "SUPERSEDES", Properties: map[string]string{"line": "4"}},
		},
	}
	projects := []conformance.DecisionProject{{Name: "main"}, {Name: "lib", Prefix: "@lib/"}}
	caps := observation.Capabilities{Families: map[string]observation.FamilyState{"supersedes": {Level: observation.SupportSupported}}}

	ev := adr.BuildEvidence(&res, caps, projects)

	evidenceWantSlice(t, "paths", evidenceDecisionPaths(&ev), []string{"@lib/docs/adr/0001-a.md", "@lib/docs/adr/0002-b.md", "docs/adr/0001-a.md"})
	evidenceWantValue(t, "decisions[0] project", ev.Decisions[0].Project, "lib")
	evidenceWantValue(t, "decisions[0] label", ev.Decisions[0].Label, "ADR-1")
	evidenceWantValue(t, "decisions[2] project", ev.Decisions[2].Project, "")
	evidenceWantSlice(t, "supersedes", ev.Supersedes, []adr.SupersedesEdge{{From: "@lib/docs/adr/0002-b.md", To: "@lib/docs/adr/0001-a.md", Line: 4}})
}

func TestApplyCoverageAddsStaleFilesAndIncompleteExtraction(t *testing.T) {
	ev := adr.DecisionEvidence{Supported: true, ExtractionComplete: true}

	ev.ApplyCoverage(&observation.FileCoverage{
		Stale:     []string{"docs/adr/0003-a.md"},
		Languages: map[string]observation.LanguageCoverage{".md": {Total: 3, Covered: 3}},
	})

	evidenceWantValue(t, "extraction complete", ev.ExtractionComplete, true)
	evidenceWantSlice(t, "stale", ev.Stale, []string{"docs/adr/0003-a.md"})

	// A dirty or excluded Markdown file is not missing evidence: it leaves extraction complete.
	ev.ApplyCoverage(&observation.FileCoverage{
		Stale:     []string{"@lib/docs/adr/0001-b.md"},
		Excluded:  []string{"vendor/README.md"},
		Languages: map[string]observation.LanguageCoverage{".md": {Total: 4, Covered: 2}},
	})

	evidenceWantValue(t, "extraction complete", ev.ExtractionComplete, true)
	evidenceWantSlice(t, "stale", ev.Stale, []string{"@lib/docs/adr/0001-b.md", "docs/adr/0003-a.md"})

	// A Markdown file in scope that the provider did not index makes extraction incomplete.
	ev.ApplyCoverage(&observation.FileCoverage{
		Unindexed: []string{"docs/adr/0009-c.MD"},
		Languages: map[string]observation.LanguageCoverage{".md": {Total: 1, Covered: 0}},
	})

	evidenceWantValue(t, "extraction complete", ev.ExtractionComplete, false)
}

func TestBuildEvidenceFromTheInfraRecording(t *testing.T) {
	client, err := observation.LoadReplayClient("../../test/fixtures/cbm-recordings/infra.json")
	if err != nil {
		t.Fatal(err)
	}

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: "concordia-fixture-infra", GitRevision: client.HeadSHA()})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"DEFINES", "SUPERSEDES", "MENTIONS", "REFERENCES_FILE"})
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	ev := adr.BuildEvidence(&res, provider.Capabilities(t.Context()), nil)

	evidenceWantValue(t, "supported", ev.Supported, true)
	evidenceWantValue(t, "decision count", len(ev.Decisions), 2)

	old, newer := ev.Decisions[0], ev.Decisions[1]
	evidenceWantValue(t, "old path", old.Path, "docs/adr/0001-use-self-hosted-postgres.md")
	evidenceWantValue(t, "old label", old.Label, "ADR-1")
	evidenceWantValue(t, "old status", old.Status, "superseded")
	evidenceWantValue(t, "old superseded by", old.SupersededBy, "0002-use-managed-postgres.md")
	evidenceWantValue(t, "newer label", newer.Label, "ADR-2")
	evidenceWantValue(t, "newer status", newer.Status, "accepted")

	want := []adr.SupersedesEdge{{From: newer.Path, To: old.Path, Line: 7}}
	evidenceWantSlice(t, "supersedes", ev.Supersedes, want)
}
