package adr_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
)

// applicabilityTriple is the comparable (state, reason, lags) answer of History.Applicability.
type applicabilityTriple struct {
	state  adr.ApplicabilityState
	reason string
	lags   bool
}

func applicabilityOf(h *adr.History, p string) (adr.ApplicabilityState, string, bool) {
	return h.Applicability(p)
}

// historyChainStates asserts that the chain p3 <- p7 <- p10 has exactly one active head and no lagging status.
func historyChainStates(t *testing.T, h *adr.History) {
	t.Helper()

	want := map[string]adr.ApplicabilityState{p3: adr.ApplicabilitySuperseded, p7: adr.ApplicabilitySuperseded, p10: adr.ApplicabilityActive}
	for p, state := range want {
		if got, _, lags := applicabilityOf(h, p); got != state || lags {
			t.Fatalf("%s = %s (lags %v), want %s", p, got, lags, state)
		}
	}
}

// historyChainLinks asserts the component size and the successor and predecessor links of the chain.
func historyChainLinks(t *testing.T, h *adr.History) {
	t.Helper()

	if len(h.Components) != 1 || len(h.Components[0]) != 3 {
		t.Fatalf("components = %v", h.Components)
	}

	if got := h.Successors(p3); !slices.Equal(got, []string{p7}) {
		t.Fatalf("successors of %s = %v", p3, got)
	}

	if got := h.Predecessors(p10); !slices.Equal(got, []string{p7}) {
		t.Fatalf("predecessors of %s = %v", p10, got)
	}

	if got := h.Successors("nowhere.md"); got == nil || len(got) != 0 {
		t.Fatalf("successors of an unknown path = %#v, want empty non-nil", got)
	}
}

func TestHistoryChainHasOneActiveHead(t *testing.T) { // SUP-V15
	h := histOf([]adr.EvidenceDecision{
		dec(p3, "ADR-3", "superseded", "0007-managed-db.md"), dec(p7, "ADR-7", "superseded", ""), dec(p10, "ADR-10", "accepted", ""),
	}, []adr.SupersedesEdge{{From: p10, To: p7}}, p3, p7, p10)

	historyChainStates(t, h)
	historyChainLinks(t, h)
}

// Scenario: Competing successors with a later date.
func TestHistoryAmbiguityGivesNoActiveDecision(t *testing.T) { // A13: no tie-break by date, number, or file order
	older := dec(p7, "ADR-7", "accepted", "")
	older.Date = "2025-01-01"
	newer := dec(p9, "ADR-9", "accepted", "")
	newer.Date = "2026-06-01"

	h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", ""), older, newer},
		[]adr.SupersedesEdge{{From: p9, To: p3}, {From: p7, To: p3}}, p3, p7, p9)

	for _, p := range []string{p3, p7, p9} {
		state, reason, lags := applicabilityOf(h, p)
		if got, want := (applicabilityTriple{state, reason, lags}), (applicabilityTriple{adr.ApplicabilityAmbiguous, "competing-successors", false}); got != want {
			t.Fatalf("%s = (%s, %q, %v), want ambiguous competing-successors", p, state, reason, lags)
		}
	}
}

// Scenario: Unrelated chain is unaffected.
func TestHistoryUnrelatedComponentIsUnaffected(t *testing.T) {
	h := histOf([]adr.EvidenceDecision{
		dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "accepted", ""),
		dec(p3, "ADR-3", "superseded", ""), dec(p7, "ADR-7", "accepted", ""),
	}, []adr.SupersedesEdge{{From: pA, To: pB}, {From: pB, To: pA}, {From: p7, To: p3}}, pA, p3, p7)

	if state, _, _ := applicabilityOf(h, pA); state != adr.ApplicabilityAmbiguous {
		t.Fatalf("cycle member = %s, want ambiguous", state)
	}

	if state, _, _ := applicabilityOf(h, p7); state != adr.ApplicabilityActive {
		t.Fatalf("unrelated head = %s, want active", state)
	}

	if state, _, _ := applicabilityOf(h, p3); state != adr.ApplicabilitySuperseded {
		t.Fatalf("unrelated predecessor = %s, want superseded", state)
	}

	if len(h.Components) != 2 {
		t.Fatalf("components = %v, want two", h.Components)
	}
}

func TestHistoryProposedSuccessorIsPending(t *testing.T) { // SUP-V13
	h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "accepted", ""), dec(p7, "ADR-7", "proposed", "")},
		[]adr.SupersedesEdge{{From: p7, To: p3}}, p3, p7)

	if got := linkSummary(h); len(got) != 1 || got[0] != p7+">"+p3+":edge:pending" {
		t.Fatalf("links = %v", got)
	}

	if state, _, lags := applicabilityOf(h, p3); state != adr.ApplicabilityActive || lags {
		t.Fatalf("predecessor = %s, want active", state)
	}

	if state, _, _ := applicabilityOf(h, p7); state != adr.ApplicabilityInactive {
		t.Fatalf("pending successor = %s, want inactive", state)
	}

	if len(h.Anomalies) != 0 {
		t.Fatalf("anomalies = %v", anomalyKinds(h))
	}
}

func TestHistoryPendingSuccessorOnSupersededPredecessor(t *testing.T) { // SUP-V17
	for _, status := range []string{"proposed", "draft"} {
		h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", ""), dec(p7, "ADR-7", status, "")},
			[]adr.SupersedesEdge{{From: p7, To: p3}}, p3, p7)

		if len(h.Anomalies) != 0 {
			t.Fatalf("%s: anomalies = %v, want none", status, anomalyKinds(h))
		}

		if got := linkSummary(h); len(got) != 1 || got[0] != p7+">"+p3+":edge:pending" {
			t.Fatalf("%s: links = %v", status, got)
		}

		// The predecessor keeps the applicability its own lifecycle gives: superseded status is not active.
		if state, reason, _ := applicabilityOf(h, p3); state != adr.ApplicabilityInactive || reason != "inactive" {
			t.Fatalf("%s: predecessor = (%s, %q), want inactive", status, state, reason)
		}
	}
}

func TestHistorySuccessorBecomesAccepted(t *testing.T) {
	h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "accepted", ""), dec(p7, "ADR-7", "accepted", "")},
		[]adr.SupersedesEdge{{From: p7, To: p3}}, p3, p7)

	if state, reason, _ := applicabilityOf(h, p3); state != adr.ApplicabilitySuperseded || reason != "superseded" {
		t.Fatalf("predecessor = (%s, %q)", state, reason)
	}
}

// Scenario: Predecessor status not updated.
func TestHistoryStatusLags(t *testing.T) { // SUP-V14
	h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "accepted", ""), dec(p7, "ADR-7", "accepted", "")},
		[]adr.SupersedesEdge{{From: p7, To: p3}}, p3, p7)

	state, _, lags := applicabilityOf(h, p3)
	if state != adr.ApplicabilitySuperseded || !lags {
		t.Fatalf("predecessor = (%s, lags %v), want superseded and lagging", state, lags)
	}

	if h.Nodes[0].Decision.Status != "accepted" || h.Nodes[0].Decision.StatusText != "accepted" {
		t.Fatalf("the original status must stay unchanged: %+v", h.Nodes[0].Decision)
	}

	if _, _, lags := applicabilityOf(h, p7); lags {
		t.Fatal("the head does not lag")
	}

	// A predecessor whose status already says superseded does not lag.
	h = histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", ""), dec(p7, "ADR-7", "accepted", "")},
		[]adr.SupersedesEdge{{From: p7, To: p3}}, p3, p7)
	if _, _, lags := applicabilityOf(h, p3); lags {
		t.Fatal("a predecessor with status superseded does not lag")
	}
}

func TestHistoryHeadWithInactiveLifecycleLeavesNoActiveMember(t *testing.T) {
	h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", ""), dec(p7, "ADR-7", "superseded", ""), dec(p10, "ADR-10", "proposed", "")},
		[]adr.SupersedesEdge{{From: p7, To: p3}, {From: p10, To: p7}}, p3, p7, p10)

	for p, want := range map[string]adr.ApplicabilityState{p3: adr.ApplicabilitySuperseded, p7: adr.ApplicabilityInactive, p10: adr.ApplicabilityInactive} {
		// p7 has only a pending successor, so its own lifecycle (superseded = not active) applies.
		if got, _, _ := applicabilityOf(h, p); p != p7 && got != want {
			t.Fatalf("%s = %s, want %s", p, got, want)
		}
	}

	for _, p := range []string{p3, p7, p10} {
		if state, _, _ := applicabilityOf(h, p); state == adr.ApplicabilityActive {
			t.Fatalf("%s is active, but no member may be", p)
		}
	}
}

func TestHistoryStatusMapDrivesTheClass(t *testing.T) {
	lc, err := adr.NewLifecycle(nil, map[string]string{"Replaced": "superseded", "Live": "accepted"})
	if err != nil {
		t.Fatal(err)
	}

	old := dec(p3, "ADR-3", "unknown", "")
	old.StatusText = "Replaced"
	cur := dec(p7, "ADR-7", "unknown", "")
	cur.StatusText = "Live"

	ev := supportedEvidence(old, cur)
	ev.Supersedes = []adr.SupersedesEdge{{From: p7, To: p3}}

	h := adr.BuildHistory(ev, lc, nil, []string{p3, p7})
	if len(h.Anomalies) != 0 {
		t.Fatalf("anomalies = %v", anomalyKinds(h))
	}

	if state, _, _ := h.Applicability(p7); state != adr.ApplicabilityActive {
		t.Fatalf("mapped successor = %s, want active", state)
	}
}
