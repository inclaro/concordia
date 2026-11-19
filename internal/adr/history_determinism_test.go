package adr_test

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/canonjson"
)

// mixedHistory gives decisions and edges with every anomaly kind, several components, and both link sources.
func mixedHistory() ([]adr.EvidenceDecision, []adr.SupersedesEdge, []string) {
	decs := []adr.EvidenceDecision{
		// valid chain with metadata and edge
		dec("a/0001.md", "ADR-1", "superseded", "0002.md"), dec("a/0002.md", "ADR-2", "superseded", ""), dec("a/0003.md", "ADR-3", "accepted", ""),
		// competing successors
		dec("b/0010.md", "ADR-10", "superseded", ""), dec("b/0011.md", "ADR-11", "accepted", ""), dec("b/0012.md", "ADR-12", "accepted", ""),
		// cycle
		dec("c/0020.md", "ADR-20", "accepted", ""), dec("c/0021.md", "ADR-21", "accepted", ""), dec("c/0022.md", "ADR-22", "accepted", ""),
		// missing target, contradictory
		dec("d/0030.md", "ADR-30", "superseded", "0099.md"), dec("d/0031.md", "ADR-31", "superseded", ""),
		dec("d/0032.md", "ADR-32", "accepted", ""), dec("d/0033.md", "ADR-33", "rejected", ""),
		// duplicate label
		dec("e/0040.md", "ADR-40", "accepted", ""), dec("e/other-0040.md", "ADR-0040", "accepted", ""),
		// pending, lagging
		dec("f/0050.md", "ADR-50", "accepted", ""), dec("f/0051.md", "ADR-51", "proposed", ""), dec("f/0052.md", "ADR-52", "accepted", ""),
		// unregistered component with a cycle
		dec("z/0090.md", "ADR-90", "accepted", ""), dec("z/0091.md", "ADR-91", "accepted", ""),
	}
	edges := []adr.SupersedesEdge{
		{From: "a/0002.md", To: "a/0001.md"}, {From: "a/0003.md", To: "a/0002.md"},
		{From: "b/0011.md", To: "b/0010.md"}, {From: "b/0012.md", To: "b/0010.md"},
		{From: "c/0020.md", To: "c/0021.md"}, {From: "c/0021.md", To: "c/0022.md"}, {From: "c/0022.md", To: "c/0020.md"},
		{From: "d/0033.md", To: "d/0031.md"},
		{From: "f/0051.md", To: "f/0050.md"}, {From: "f/0052.md", To: "f/0050.md"},
		{From: "z/0090.md", To: "z/0091.md"}, {From: "z/0091.md", To: "z/0090.md"},
	}
	registered := []string{"a/0001.md", "a/0003.md", "b/0010.md", "c/0021.md", "d/0030.md", "d/0031.md", "e/0040.md", "f/0050.md", "f/0051.md"}

	return decs, edges, registered
}

// canonicalHistory renders every observable result of a history as canonical JSON.
func canonicalHistory(t *testing.T, h *adr.History, probe []string) string {
	t.Helper()

	type applic struct {
		Path, State, Reason string
		Successors          []string
		Predecessors        []string
		Lags, Ambiguous     bool
	}

	probe = slices.Sorted(slices.Values(probe))
	results := make([]applic, 0, len(probe))

	for _, p := range probe {
		s, r, l := h.Applicability(p)
		results = append(results, applic{
			Successors: h.Successors(p), Predecessors: h.Predecessors(p), Path: p, State: string(s), Reason: r,
			Lags: l, Ambiguous: h.Ambiguous(p),
		})
	}

	out, err := canonjson.Marshal(map[string]any{"history": h, "applicability": results})
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// Scenario: Deterministic order.
func TestHistoryDeterminismUnderShuffle(t *testing.T) {
	decs, edges, registered := mixedHistory()
	probe := slices.Clone(registered)
	slices.Sort(probe)

	build := func(d []adr.EvidenceDecision, e []adr.SupersedesEdge, r []string) string {
		ev := supportedEvidence(d...)
		ev.Supersedes = e

		return canonicalHistory(t, adr.BuildHistory(ev, nil, nil, r), probe)
	}

	baseline := build(decs, edges, registered)

	// Sanity: the scenario exercises every anomaly kind and is not trivially empty.
	for _, kind := range []adr.Reason{adr.ReasonCycle, adr.ReasonMissingTarget, adr.ReasonDuplicateID, adr.ReasonContradictoryStatus, adr.ReasonCompetingSuccessors} {
		if !strings.Contains(baseline, string(kind)) {
			t.Fatalf("scenario lacks %s: %s", kind, baseline)
		}
	}

	rng := rand.New(rand.NewSource(42))

	for i := range 200 {
		d, e, r := slices.Clone(decs), slices.Clone(edges), slices.Clone(registered)
		rng.Shuffle(len(d), func(a, b int) { d[a], d[b] = d[b], d[a] })
		rng.Shuffle(len(e), func(a, b int) { e[a], e[b] = e[b], e[a] })
		rng.Shuffle(len(r), func(a, b int) { r[a], r[b] = r[b], r[a] })

		if got := build(d, e, r); got != baseline {
			t.Fatalf("shuffle %d changed the canonical output:\n%s\nwant\n%s", i, got, baseline)
		}
	}
}

func TestPresenceDeterminismUnderShuffle(t *testing.T) {
	entries := []adr.Entry{testEntry("ADR-1", "", ""), testEntry("ADR-2", "x/0002.md", ""), testEntry("ADR-3", "", ""), testEntry("ADR-4", "", "")}
	decs := []adr.EvidenceDecision{
		decisionAt("x/0001.md", "ADR-1", "accepted"), decisionAt("x/0002.md", "ADR-2", "accepted"),
		decisionAt("x/0003.md", "ADR-3", "accepted"), decisionAt("y/0003.md", "ADR-3", "accepted"),
	}
	git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{"x/0001.md", "x/0002.md", "x/0003.md", "y/0003.md"}, "x/0002.md"))

	run := func(d []adr.EvidenceDecision, e []adr.Entry) string {
		ev := supportedEvidence(d...)
		res := adr.EvaluatePresence(e, ev, git)

		out, err := canonjson.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}

		return string(out)
	}

	sorted := slices.Clone(decs)
	slices.SortFunc(sorted, func(a, b adr.EvidenceDecision) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	baseline := run(sorted, entries)
	rng := rand.New(rand.NewSource(7))

	for i := range 200 {
		d, e := slices.Clone(sorted), slices.Clone(entries)
		rng.Shuffle(len(e), func(a, b int) { e[a], e[b] = e[b], e[a] })

		// Evidence arrives sorted by path (the adr.DecisionEvidence contract); the entries may arrive in any order.
		if got := run(d, e); got != baseline {
			t.Fatalf("shuffle %d changed the presence output:\n%s\nwant\n%s", i, got, baseline)
		}
	}
}

// chainEvidence gives n decisions where decision i supersedes decision i-1: the link of an even i is an edge,
// the link of an odd i is `superseded_by` metadata of the predecessor; the head is accepted.
func chainEvidence(n int) *adr.DecisionEvidence {
	decs := make([]adr.EvidenceDecision, n)
	path := func(i int) string { return fmt.Sprintf("docs/adr/%06d-step.md", i) }

	var edges []adr.SupersedesEdge

	for i := range n {
		status := "superseded"
		if i == n-1 {
			status = "accepted"
		}

		decs[i] = dec(path(i), fmt.Sprintf("ADR-%d", i+1), status, "")

		if i+1 < n && i%2 == 0 {
			decs[i].SupersededBy = fmt.Sprintf("%06d-step.md", i+1)
		}
	}

	for i := 1; i < n; i++ {
		if i%2 == 0 {
			edges = append(edges, adr.SupersedesEdge{From: path(i), To: path(i - 1)})
		}
	}

	ev := supportedEvidence(decs...)
	ev.Supersedes = edges

	return ev
}

func TestHistoryChainOf20000DecisionsDoesNotOverflow(t *testing.T) {
	const n = 20000

	ev := chainEvidence(n)
	first, last := ev.Decisions[0].Path, ev.Decisions[n-1].Path

	h := adr.BuildHistory(ev, nil, nil, []string{first, last})
	if len(h.Anomalies) != 0 || len(h.Nodes) != n || len(h.Links) != n-1 || len(h.Components) != 1 {
		t.Fatalf("chain: %d anomalies, %d nodes, %d links, %d components", len(h.Anomalies), len(h.Nodes), len(h.Links), len(h.Components))
	}

	if state, _, _ := h.Applicability(first); state != adr.ApplicabilitySuperseded {
		t.Fatalf("tail = %s, want superseded", state)
	}

	if state, _, _ := h.Applicability(last); state != adr.ApplicabilityActive {
		t.Fatalf("head = %s, want active", state)
	}
}

func TestHistoryChainClosedIntoOneCycleIsFoundIteratively(t *testing.T) {
	const n = 20000

	ev := chainEvidence(n)
	ev.Supersedes = append(ev.Supersedes, adr.SupersedesEdge{From: ev.Decisions[0].Path, To: ev.Decisions[n-1].Path})

	h := adr.BuildHistory(ev, nil, nil, []string{ev.Decisions[5].Path})

	var cycle *adr.Anomaly

	for i := range h.Anomalies {
		if h.Anomalies[i].Kind == adr.ReasonCycle {
			cycle = &h.Anomalies[i]
		}
	}

	if cycle == nil || len(cycle.Paths) != n || !h.Ambiguous(ev.Decisions[5].Path) {
		t.Fatalf("expected one cycle of %d decisions, got anomalies %d", n, len(h.Anomalies))
	}
}

func BenchmarkHistoryChain20000(b *testing.B) {
	ev := chainEvidence(20000)
	registered := []string{ev.Decisions[0].Path, ev.Decisions[19999].Path}

	b.ResetTimer()

	for range b.N {
		if h := adr.BuildHistory(ev, nil, nil, registered); len(h.Nodes) != 20000 {
			b.Fatal("short history")
		}
	}
}
