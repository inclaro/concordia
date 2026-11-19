package impact_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// The fixtures of the benchmark of full and incremental evaluation (design D16). Two observed graphs feed it: the
// recording of the commerce fixture (the pilot graph) and a graph that internal/testutil/fakegraph.go generates. Every
// observed edge becomes one dependency item, the items are grouped into assessments, and a shared assumption per
// cluster of assessments makes one changed item reach several assessments. The evaluation is assessment.Evaluate: the
// benchmark measures the one evaluator in both modes.

const (
	commerceRecording = "../../test/fixtures/cbm-recordings/commerce.json"
	commerceProject   = "concordia-fixture-commerce"

	// groupSize is the number of items that one assessment owns, and clusterSize the number of assessments that
	// share an assumption.
	groupSize   = 10
	clusterSize = 10

	benchAuthority = "security-review"
)

// observed is a graph that the observation adapter read.
type observed struct {
	name string
	// edges are the identities of the observed edges, in the order of the adapter.
	edges []string
	// entities is the number of nodes of the graph, and links the number of its edges, as the provider froze them.
	entities, links int
}

// freeze reads the evidence of a client the way the pipeline does and keeps the edge identities.
func freeze(tb testing.TB, name string, client observation.Client, project, head string, kinds []string) *observed {
	tb.Helper()

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(context.Background(), observation.FreezeRequest{Project: project, GitRevision: head})
	if err != nil {
		tb.Fatalf("freeze %s: %v", name, err)
	}

	if kinds == nil {
		for _, t := range ref.Schema.EdgeTypes {
			kinds = append(kinds, t.Type)
		}
	}

	res, err := provider.FetchEdges(context.Background(), ref, kinds)
	if err != nil {
		tb.Fatalf("fetch %s: %v", name, err)
	}

	out := &observed{name: name, entities: ref.Nodes, links: ref.Edges}
	for _, e := range res.Edges {
		out.edges = append(out.edges, fmt.Sprintf("%s:%s->%s", e.Kind, e.From, e.To))
	}

	if len(out.edges) == 0 {
		tb.Fatalf("%s has no observed edges", name)
	}

	return out
}

// commerceGraph is the pilot graph: the recording of the commerce fixture.
func commerceGraph(tb testing.TB) *observed {
	tb.Helper()

	client, err := observation.LoadReplayClient(commerceRecording)
	if err != nil {
		tb.Fatal(err)
	}

	// The native kinds that the pipeline asked for when the recording was made.
	return freeze(tb, "commerce", client, commerceProject, client.HeadSHA(), []string{"CALLS", "DEFINES", "DEFINES_METHOD", "IMPORTS"})
}

// generatedGraph is a larger graph from internal/testutil/fakegraph.go: files in packages, each importing the files of
// the next packages, so the number of edges is about files*fanout.
func generatedGraph(tb testing.TB, files, fanout int) *observed {
	tb.Helper()

	nodes := make([]testutil.FakeNode, files)
	for i := range nodes {
		nodes[i] = testutil.FileNode(strconv.Itoa(i+1), fmt.Sprintf("src/pkg%03d/file%05d.ts", i%100, i))
	}

	var edges []testutil.FakeEdge

	for i := range nodes {
		for k := 1; k <= fanout; k++ {
			target := (i*7 + k*13) % files
			if target == i {
				continue
			}

			edges = append(edges, testutil.NewEdge(&nodes[i], &nodes[target], "IMPORTS", map[string]string{"line": strconv.Itoa(k)}))
		}
	}

	graph := testutil.NewFakeGraph("generated", nodes, edges)

	return freeze(tb, "generated", observation.NewFakeClient(graph.Handler, ""), "generated", graph.HeadSHA, nil)
}

// benchFacts is the fact source of the benchmark: one public-entry-point item per identity. A change of a host moves
// the digest of the item.
type benchFacts struct {
	items map[string]impact.Item
}

func hostOf(identity, variant string) string {
	sum := sha256.Sum256([]byte(identity + variant))

	return hex.EncodeToString(sum[:6]) + ".example.com"
}

func newBenchFacts(g *observed) *benchFacts {
	f := &benchFacts{items: make(map[string]impact.Item, len(g.edges))}
	for _, id := range g.edges {
		f.set(id, "")
	}

	return f
}

func (f *benchFacts) set(identity, variant string) {
	f.items[identity] = impact.V1().NewItem(impact.KindPublicEntryPoint, identity, impact.Facets{"host": {hostOf(identity, variant)}, "path": {"/"}}, nil, nil)
}

func (f *benchFacts) Items(_ context.Context, sel impact.Selector) (impact.Match, error) { //nolint:gocritic // implements impact.FactSource.
	if sel.Kind != impact.KindPublicEntryPoint {
		return impact.Match{State: impact.MatchEmpty, Items: []impact.Item{}}, nil
	}

	out := make([]impact.Item, 0, len(sel.Identities))

	for _, id := range sel.Identities {
		if it, ok := f.items[id]; ok {
			out = append(out, it)
		}
	}

	return impact.Match{State: impact.MatchFound, Items: out}, nil
}

func (f *benchFacts) FileDigest(context.Context, string, string) (digest string, found bool, err error) {
	return "", false, nil
}

func (f *benchFacts) Profile() impact.CapabilityProfile {
	return impact.CapabilityProfile{ID: "bench", Kinds: impact.Kinds()}
}

func (f *benchFacts) EvaluatorVersions() map[string]string {
	return map[string]string{impact.EvaluatorNormalization: "1"}
}

// approved verifies every approval of the set.
type approved struct{}

func (approved) VerifySet(_ context.Context, set *assessment.Set) (assessment.Verified, error) {
	out := assessment.Verified{Approvals: []*assessment.Approval{}, Revocations: []*assessment.Revocation{}}
	out.Approvals = append(out.Approvals, set.Approvals...)

	for _, ap := range set.Approvals {
		out.Results = append(out.Results, assessment.Verification{
			RecordID: ap.ID, Kind: assessment.RecordApproval, Applied: true, Authority: benchAuthority, Mechanism: "protected-review",
		})
	}

	return out, nil
}

// benchSet is a workspace of assessments over a graph, with an approval of every assessment.
type benchSet struct {
	graph *observed
	set   *assessment.Set
	facts *benchFacts
	// hubs are the items that an assumption shares between the assessments of a cluster.
	hubs []string
	// assessments is the number of assessment versions, and nodes the number of nodes of their dependency graph: the
	// items, the assessments, and the assumptions.
	assessments, nodes int
}

func quoted(values []string) string {
	return canonjson.MustString(values)
}

// buildSet writes the assessment documents of the graph, loads them, and approves every assessment for the items that
// the facts give.
func buildSet(tb testing.TB, g *observed) *benchSet {
	tb.Helper()

	groups := (len(g.edges) + groupSize - 1) / groupSize
	files := map[string]string{}
	hubs := []string{}

	for c := 0; c*clusterSize < groups; c++ {
		var hub []string

		for j := c * clusterSize; j < min((c+1)*clusterSize, groups); j++ {
			hub = append(hub, g.edges[j*groupSize])
		}

		hubs = append(hubs, hub...)

		files[fmt.Sprintf("assessments/assumptions/ASM-HUB-%04d.yaml", c)] = fmt.Sprintf(`apiVersion: concordia.io/v1alpha1
kind: Assumption
id: ASM-HUB-%04d
statement: The shared entries of cluster %d stay as reviewed.
scope:
  calmNodes: [bench]
owner: bench
verificationMethod: review
selectors:
  - kind: public-entry-point
    selector:
      identities: %s
`, c, c, quoted(hub))
	}

	for j := range groups {
		own := g.edges[j*groupSize : min((j+1)*groupSize, len(g.edges))]
		files[fmt.Sprintf("assessments/ASM-%05d/v1.yaml", j)] = fmt.Sprintf(`apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-%05d
version: 1
type: security
title: Group %d
scope:
  calmNodes: [bench]
environment: production
subject:
  summary: The entries of group %d
  dependencies:
    - kind: public-entry-point
      selector:
        identities: %s
assumptions: [ASM-HUB-%04d]
evidence: []
outcome: satisfactory
state: ready-for-review
`, j, j, j, quoted(own), j/clusterSize)
	}

	root := tb.TempDir()
	testutil.WriteTree(tb, root, files)

	set, err := assessment.Load(root, []string{"assessments"})
	if err != nil {
		tb.Fatal(err)
	}

	distinct := map[string]bool{}
	for _, id := range g.edges {
		distinct[id] = true
	}

	b := &benchSet{
		graph: g, set: set, facts: newBenchFacts(g), hubs: hubs, assessments: groups,
		nodes: len(distinct) + groups + (groups+clusterSize-1)/clusterSize,
	}
	b.approveAll(tb)

	return b
}

var (
	benchDecided = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	benchNow     = time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
)

// approveAll adds an approval of every assessment for the subject that the facts show.
func (b *benchSet) approveAll(tb testing.TB) {
	tb.Helper()

	in := b.input(nil)

	for _, doc := range b.set.Assessments {
		bind, err := assessment.Bind(context.Background(), in, doc.ID, doc.Version)
		if err != nil {
			tb.Fatal(err)
		}

		id := doc.ID + "-v1-0001"
		b.set.Approvals = append(b.set.Approvals, &assessment.Approval{
			ID: id, Decision: assessment.DecisionApproved, Assessment: bind.Assessment, Subject: bind.Subject, Scope: bind.Scope,
			Environment: bind.Environment, Assumptions: bind.Assumptions, Evidence: bind.Evidence,
			ContractVersion: bind.ContractVersion, CapabilityProfile: bind.CapabilityProfile, ProducedUnder: bind.ProducedUnder,
			Reviewer: assessment.Reviewer{Identity: "alice@example.com", Authority: benchAuthority}, DecidedAt: benchDecided,
			Location: assessment.Location{File: "assessments/approvals/" + id + ".yaml"},
			Digest:   "sha256:" + fmt.Sprintf("%064x", len(b.set.Approvals)+1),
		})
	}
}

// input is the evaluation input of the set. cache nil is a full evaluation.
func (b *benchSet) input(cache *assessment.Incremental) *assessment.EvalInput {
	return &assessment.EvalInput{
		Set: b.set, Verifier: approved{}, Facts: b.facts, Now: func() time.Time { return benchNow },
		PolicyDigest: "sha256:" + strings.Repeat("1", 64), AuthorityDigest: "sha256:" + strings.Repeat("2", 64),
		Incremental: cache,
	}
}

// change moves the digest of one hub item of the cluster, or restores it: the item is shared by the assessments of the
// cluster, so the change reaches every one of them.
func (b *benchSet) change(variant string) {
	b.facts.set(b.hubs[len(b.hubs)/2], variant)
}

// evaluate runs one evaluation.
func (b *benchSet) evaluate(tb testing.TB, cache *assessment.Incremental) *assessment.Evaluation {
	tb.Helper()

	ev, err := assessment.Evaluate(context.Background(), b.input(cache))
	if err != nil {
		tb.Fatal(err)
	}

	return ev
}

// semantic is the canonical JSON of an evaluation without what differs between the two modes by design: the cache
// report and the note that a cache could not be used.
func semantic(tb testing.TB, ev *assessment.Evaluation) string {
	tb.Helper()

	data, err := canonjson.Marshal(ev)
	if err != nil {
		tb.Fatal(err)
	}

	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		tb.Fatal(err)
	}

	delete(tree, "incremental")

	list, _ := tree["diagnostics"].([]any)
	list = slices.DeleteFunc(list, func(d any) bool {
		m, ok := d.(map[string]any)

		return ok && m["code"] == diag.ASM010
	})

	if len(list) == 0 {
		delete(tree, "diagnostics")
	} else {
		tree["diagnostics"] = list
	}

	text, err := canonjson.Marshal(tree)
	if err != nil {
		tb.Fatal(err)
	}

	return string(text)
}

// sameResult fails when two evaluations of the same inputs differ, and names the first line that differs.
func sameResult(tb testing.TB, what string, incremental, full *assessment.Evaluation) {
	tb.Helper()

	got, want := semantic(tb, incremental), semantic(tb, full)
	if got == want {
		return
	}

	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range min(len(gl), len(wl)) {
		if gl[i] != wl[i] {
			tb.Fatalf("%s: the incremental result differs from the full result at line %d: %q, want %q", what, i+1, gl[i], wl[i])
		}
	}

	tb.Fatalf("%s: the incremental result differs from the full result (%d and %d lines)", what, len(gl), len(wl))
}

// sizes are the sizes of the dependency closures of the assessments and of the traversals from the changed items.
type sizes struct {
	closureTotal, closureMax, visitedTotal, visitedMax int
}

func sizesOf(ev *assessment.Evaluation) sizes {
	var out sizes

	for i := range ev.Assessments {
		analysis := &ev.Assessments[i].Currency.Analysis

		out.closureTotal += analysis.ClosureSize
		out.closureMax = max(out.closureMax, analysis.ClosureSize)
		out.visitedTotal += analysis.Visited
		out.visitedMax = max(out.visitedMax, analysis.Visited)
	}

	return out
}

// cacheOf is the incremental setting with a cache in a new directory.
func cacheOf(tb testing.TB) *assessment.Incremental {
	tb.Helper()

	return &assessment.Incremental{Dir: filepath.Join(tb.TempDir(), "impact"), AnchorRevision: "abc123"}
}
