package adr_test

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/yamlparse"
)

// variantsFile is the table of the supersession variants SUP-V01 to SUP-V17. Its decisions are the `ADR` nodes
// that CBM 0.12.1 gave for test/fixtures/adr/docs/adr/variants (recording adr.json).
const (
	variantsFile      = "testdata/variants.yaml"
	variantsRecording = "../../test/fixtures/cbm-recordings/adr.json"
	variantsProject   = "concordia-fixture-adr"
	variantsDir       = "docs/adr/variants/"
)

// variantDecision is one `ADR` node of a variant.
type variantDecision struct {
	Path         string `json:"path"`
	AdrID        string `json:"adrId"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	StatusText   string `json:"statusText"`
	Date         string `json:"date"`
	SupersededBy string `json:"supersededBy"`
	Line         int    `json:"line"`
}

// variantEdge is one `supersedes` edge of a variant: From is the new decision.
type variantEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Line int    `json:"line"`
}

// variantLink is an expected history link.
type variantLink struct {
	Successor   string `json:"successor"`
	Predecessor string `json:"predecessor"`
	Provenance  string `json:"provenance"`
	Effect      string `json:"effect"`
}

// variantAnomaly is an expected history anomaly.
type variantAnomaly struct {
	Kind  string   `json:"kind"`
	Value string   `json:"value"`
	Paths []string `json:"paths"`
}

// variantExpect is the expected outcome of the history evaluation of a variant with every decision committed.
type variantExpect struct {
	Applicability map[string]string `json:"applicability"`
	Matches       map[string]string `json:"matches"`
	Links         []variantLink     `json:"links"`
	Anomalies     []variantAnomaly  `json:"anomalies"`
	Heads         []string          `json:"heads"`
	StatusLags    []string          `json:"statusLags"`
	Ambiguous     bool              `json:"ambiguous"`
}

// variant is one row of the variants table.
type variant struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Note        string            `json:"note"`
	RegisterIDs []string          `json:"registerIds"`
	Decisions   []variantDecision `json:"decisions"`
	Supersedes  []variantEdge     `json:"supersedes"`
	Expect      variantExpect     `json:"expect"`
}

// loadVariants reads testdata/variants.yaml.
func loadVariants(tb testing.TB) []variant {
	tb.Helper()

	text, err := os.ReadFile(variantsFile)
	if err != nil {
		tb.Fatalf("read %s: %v", variantsFile, err)
	}

	doc, problems := yamlparse.Parse(text)
	if len(problems) > 0 {
		tb.Fatalf("parse %s: %v", variantsFile, problems)
	}

	data, err := json.Marshal(doc)
	if err != nil {
		tb.Fatalf("encode %s: %v", variantsFile, err)
	}

	var file struct {
		Variants []variant `json:"variants"`
	}

	if err := json.Unmarshal(data, &file); err != nil {
		tb.Fatalf("decode %s: %v", variantsFile, err)
	}

	return file.Variants
}

// capsWithSupersedes are the capabilities of a build with the `supersedes` family.
func capsWithSupersedes() observation.Capabilities {
	return observation.Capabilities{Families: map[string]observation.FamilyState{
		"supersedes": {Level: observation.SupportSupported},
	}}
}

// Evidence builds the DecisionEvidence of the variant from synthetic normalized evidence: one provider `ADR` node
// per decision and one `supersedes` fact per edge, like the adapter gives them.
func (v *variant) Evidence(tb testing.TB) adr.DecisionEvidence {
	tb.Helper()

	result := observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{}}
	ids := map[string]string{}

	for i := range v.Decisions {
		d := &v.Decisions[i]
		id := "cbm:" + variantsProject + ":" + strconv.Itoa(i+1)
		ids[d.Path] = id
		result.Nodes[id] = observation.ObservedNode{
			ObservedID: id, Label: observation.LabelDecision, FilePath: d.Path, StartLine: d.Line,
			Properties: map[string]string{
				"adr_id": d.AdrID, "title": d.Title, "status": d.Status, "status_text": d.StatusText, "date": d.Date,
				"superseded_by": d.SupersededBy,
			},
		}
	}

	for _, e := range v.Supersedes {
		from, to := ids[e.From], ids[e.To]
		if from == "" || to == "" {
			tb.Fatalf("%s: edge %s -> %s names a decision that the variant does not list", v.ID, e.From, e.To)
		}

		result.Edges = append(result.Edges, &observation.EvidenceEdge{
			From: from, To: to, Kind: observation.KindSupersedes, NativeKind: "SUPERSEDES",
			Properties: map[string]string{"line": strconv.Itoa(e.Line)},
		})
	}

	return adr.BuildEvidence(&result, capsWithSupersedes(), nil)
}

func TestVariantsParseIntoDecisionEvidence(t *testing.T) {
	variants := loadVariants(t)

	if len(variants) != 17 {
		t.Fatalf("variants = %d, want 17", len(variants))
	}

	for i := range variants {
		v := &variants[i]

		if want := "SUP-V" + twoDigits(i+1); v.ID != want {
			t.Fatalf("variant %d has ID %s, want %s", i+1, v.ID, want)
		}

		t.Run(v.ID, func(t *testing.T) {
			ev := v.Evidence(t)
			v.wantEvidenceShape(t, &ev)

			byPath := evidenceDecisionsByPath(t, ev.Decisions)
			wantEdgesNameDecisions(t, ev.Supersedes, byPath)
			v.wantExpectNamesDecisions(t, byPath)
		})
	}
}

// wantEvidenceShape wants supported, complete evidence with the decisions and edges of the variant.
func (v *variant) wantEvidenceShape(t *testing.T, ev *adr.DecisionEvidence) {
	t.Helper()

	if !ev.Supported || !ev.ExtractionComplete {
		t.Fatalf("evidence = %+v", *ev)
	}

	if len(ev.Decisions) != len(v.Decisions) || len(ev.Supersedes) != len(v.Supersedes) {
		t.Fatalf("evidence = %+v", *ev)
	}
}

// evidenceDecisionsByPath indexes the decisions by path; every label is the canonical form of its ADR ID.
func evidenceDecisionsByPath(t *testing.T, decisions []adr.EvidenceDecision) map[string]adr.EvidenceDecision {
	t.Helper()

	byPath := map[string]adr.EvidenceDecision{}

	for i := range decisions {
		d := &decisions[i]
		byPath[d.Path] = *d

		if d.Label != adr.CanonicalID(d.AdrID) {
			t.Fatalf("%s: label %q is not the canonical form of %q", d.Path, d.Label, d.AdrID)
		}
	}

	return byPath
}

// wantEdgesNameDecisions wants both ends of every edge to be a decision.
func wantEdgesNameDecisions(t *testing.T, edges []adr.SupersedesEdge, byPath map[string]adr.EvidenceDecision) {
	t.Helper()

	for _, e := range edges {
		if _, ok := byPath[e.From]; !ok {
			t.Fatalf("edge from %s names no decision", e.From)
		}

		if _, ok := byPath[e.To]; !ok {
			t.Fatalf("edge to %s names no decision", e.To)
		}
	}
}

// wantExpectNamesDecisions wants the expected outcome to name only decisions of the variant.
func (v *variant) wantExpectNamesDecisions(t *testing.T, byPath map[string]adr.EvidenceDecision) {
	t.Helper()

	for path := range v.Expect.Applicability {
		if _, ok := byPath[path]; !ok {
			t.Fatalf("expected applicability of %s, which the variant does not list", path)
		}
	}

	if len(v.Expect.Applicability) != len(v.Decisions) {
		t.Fatalf("applicability lists %d decisions, the variant has %d", len(v.Expect.Applicability), len(v.Decisions))
	}

	for _, l := range v.Expect.Links {
		if _, ok := byPath[l.Successor]; !ok {
			t.Fatalf("link successor %s is not a decision of the variant", l.Successor)
		}

		if _, ok := byPath[l.Predecessor]; !ok {
			t.Fatalf("link predecessor %s is not a decision of the variant", l.Predecessor)
		}
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}

	return strconv.Itoa(n)
}

// TestVariantsMatchTheRecording compares every variant with the `ADR` nodes and `SUPERSEDES` edges of the CBM
// 0.12.1 recording of the fixture repository: the table is what the provider gave, not what was assumed.
func TestVariantsMatchTheRecording(t *testing.T) {
	recorded := loadRecordedVariantEvidence(t)

	byPath := map[string]adr.EvidenceDecision{}
	for i := range recorded.Decisions {
		byPath[recorded.Decisions[i].Path] = recorded.Decisions[i]
	}

	edges := map[adr.SupersedesEdge]bool{}
	for _, e := range recorded.Supersedes {
		edges[e] = true
	}

	variants := loadVariants(t)
	listed := 0

	for i := range variants {
		variants[i].wantDecisionsRecorded(t, byPath)
		variants[i].wantEdgesRecorded(t, edges)

		listed += len(variants[i].Decisions)
	}

	wantTableListsEveryRecordedEdge(t, variants, edges)

	if listed == 0 {
		t.Fatal("no decision listed")
	}
}

// loadRecordedVariantEvidence gives the evidence of the CBM recording of the variants fixture.
func loadRecordedVariantEvidence(t *testing.T) adr.DecisionEvidence {
	t.Helper()

	client, err := observation.LoadReplayClient(variantsRecording)
	if err != nil {
		t.Fatal(err)
	}

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: variantsProject, GitRevision: client.HeadSHA()})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	kinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, e := range ref.Schema.EdgeTypes {
		kinds = append(kinds, e.Type)
	}

	res, err := provider.FetchEdges(t.Context(), ref, kinds)
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	recorded := adr.BuildEvidence(&res, provider.Capabilities(t.Context()), nil)
	if !recorded.Supported {
		t.Fatal("the recording has no supersedes family")
	}

	return recorded
}

// wantDecisionsRecorded wants every decision of the variant to be a recorded `ADR` node, field for field.
func (v *variant) wantDecisionsRecorded(t *testing.T, byPath map[string]adr.EvidenceDecision) {
	t.Helper()

	for _, d := range v.Decisions {
		got, ok := byPath[d.Path]
		if !ok {
			t.Fatalf("%s: %s is not in the recording", v.ID, d.Path)
		}

		want := variantDecision{
			Path: got.Path, AdrID: got.AdrID, Title: got.Title, Status: got.Status, StatusText: got.StatusText,
			Date: got.Date, SupersededBy: got.SupersededBy, Line: got.Line,
		}
		if d != want {
			t.Fatalf("%s: decision = %+v, the recording has %+v", v.ID, d, want)
		}
	}
}

// wantEdgesRecorded wants every edge of the variant to be a recorded `SUPERSEDES` edge.
func (v *variant) wantEdgesRecorded(t *testing.T, edges map[adr.SupersedesEdge]bool) {
	t.Helper()

	for _, e := range v.Supersedes {
		if !edges[adr.SupersedesEdge(e)] {
			t.Fatalf("%s: edge %+v is not in the recording", v.ID, e)
		}
	}
}

// wantTableListsEveryRecordedEdge wants every variant edge of the recording to be in the table: the variants list
// them all.
func wantTableListsEveryRecordedEdge(t *testing.T, variants []variant, edges map[adr.SupersedesEdge]bool) {
	t.Helper()

	inTable := 0

	for i := range variants {
		inTable += len(variants[i].Supersedes)
	}

	variantEdges := 0

	for e := range edges {
		if strings.HasPrefix(e.From, variantsDir) {
			variantEdges++
		}
	}

	if inTable != variantEdges {
		t.Fatalf("the table lists %d edges, the recording has %d between variant files", inTable, variantEdges)
	}
}
