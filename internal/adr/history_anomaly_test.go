package adr_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
)

const (
	pA = "docs/adr/0001-a.md"
	pB = "docs/adr/0002-b.md"
	pC = "docs/adr/0003-c.md"
	pD = "docs/adr/0004-d.md"
)

// anomalyCase is one history of decisions and the anomalies that it must report.
type anomalyCase struct {
	name  string
	value string
	decs  []adr.EvidenceDecision
	edges []adr.SupersedesEdge
	want  []string
}

func cycleAnomalyCases() []anomalyCase {
	return []anomalyCase{
		{ // SUP-V05
			name:  "cycle of two",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pA, To: pB}, {From: pB, To: pA}},
			want:  []string{"cycle " + pA + "," + pB},
		},
		{
			name:  "cycle through metadata and an edge",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", "0002-b.md"), dec(pB, "ADR-2", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pA, To: pB}},
			want:  []string{"cycle " + pA + "," + pB},
		},
		{
			name:  "cycle of three",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pA, To: pB}, {From: pB, To: pC}, {From: pC, To: pA}},
			want:  []string{"cycle " + pA + "," + pB + "," + pC},
		},
		{ // SUP-V06
			name:  "self-supersession by edge",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pA, To: pA}},
			want:  []string{"cycle " + pA},
		},
		{
			name: "self-supersession by metadata",
			decs: []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", "./0001-a.md")},
			want: []string{"cycle " + pA},
		},
	}
}

func statusAnomalyCases() []anomalyCase {
	return []anomalyCase{
		{ // SUP-V07
			name:  "missing target",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", "0099-unknown.md")},
			want:  []string{"missing-target " + pA},
			value: "0099-unknown.md",
		},
		{ // SUP-V08
			name: "superseded with no successor",
			decs: []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", "")},
			want: []string{"contradictory-status " + pA},
		},
		{ // SUP-V09
			name:  "superseded by a rejected decision",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "rejected", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{"contradictory-status " + pA + "," + pB},
		},
		{
			name:  "successor deprecated",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "deprecated", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{"contradictory-status " + pA + "," + pB},
		},
		{
			name:  "successor abandoned",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "abandoned", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{"contradictory-status " + pA + "," + pB},
		},
		{
			name:  "successor of unknown status",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "unknown", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{"contradictory-status " + pA + "," + pB},
		},
	}
}

func competingAnomalyCases() []anomalyCase {
	return []anomalyCase{
		// Scenario: Competing successors from edges.
		{ // SUP-V10
			name:  "competing successors by edges",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}, {From: pC, To: pA}},
			want:  []string{"competing-successors " + pA + "," + pB + "," + pC},
		},
		{ // SUP-V11
			name:  "edge and metadata name different successors",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", "0003-c.md"), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{"competing-successors " + pA + "," + pB + "," + pC},
		},
		{
			name:  "three successors where two compete",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "accepted", ""), dec(pD, "ADR-4", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}, {From: pC, To: pA}, {From: pD, To: pA}, {From: pD, To: pB}},
			want:  []string{"competing-successors " + pA + "," + pB + "," + pC + "," + pD},
		},
	}
}

func duplicateIDAnomalyCases() []anomalyCase {
	return []anomalyCase{
		// Scenario: Duplicate ADR IDs in committed evidence.
		{ // SUP-V12
			name: "duplicate ADR IDs",
			decs: []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec("docs/old/0001-copy.md", "ADR-0001", "accepted", "")},
			want: []string{"duplicate-id " + pA + ",docs/old/0001-copy.md"},
		},
		{
			name: "bare ID that names two decisions",
			decs: []adr.EvidenceDecision{
				dec(pA, "ADR-1", "superseded", "ADR-3"), dec(pC, "ADR-3", "accepted", ""), dec("old/0003.md", "ADR-0003", "accepted", ""),
			},
			want: []string{"duplicate-id " + pA + "," + pC + ",old/0003.md", "duplicate-id " + pC + ",old/0003.md"},
		},
	}
}

func quietAnomalyCases() []anomalyCase {
	return []anomalyCase{
		{ // SUP-V15
			name:  "valid chain is quiet",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "superseded", "0003-c.md"), dec(pC, "ADR-3", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}, {From: pC, To: pB}},
			want:  []string{},
		},
		{
			name:  "successor that reaches the other is not competing",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "superseded", ""), dec(pC, "ADR-3", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}, {From: pC, To: pB}, {From: pC, To: pA}},
			want:  []string{},
		},
		{
			name:  "fan-in is quiet",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "superseded", ""), dec(pC, "ADR-3", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: pC, To: pA}, {From: pC, To: pB}},
			want:  []string{},
		},
		{ // SUP-V17
			name:  "pending successor on a superseded predecessor is not contradictory",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "proposed", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{},
		},
		{
			name:  "draft successor is pending",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "draft", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}},
			want:  []string{},
		},
		{
			name:  "an active and a pending successor do not compete",
			decs:  []adr.EvidenceDecision{dec(pA, "ADR-1", "superseded", ""), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "proposed", "")},
			edges: []adr.SupersedesEdge{{From: pB, To: pA}, {From: pC, To: pA}},
			want:  []string{},
		},
	}
}

// checkAnomalyCase builds the history of the case with every decision registered, and checks the anomaly kinds,
// the value of the first anomaly, and the ambiguity of every path.
func checkAnomalyCase(t *testing.T, tt *anomalyCase) {
	t.Helper()

	all := make([]string, 0, len(tt.decs))
	for i := range tt.decs {
		all = append(all, tt.decs[i].Path)
	}

	h := histOf(tt.decs, tt.edges, all...)
	if got := anomalyKinds(h); !reflect.DeepEqual(got, tt.want) {
		t.Fatalf("anomalies = %v, want %v", got, tt.want)
	}

	if tt.value != "" && h.Anomalies[0].Value != tt.value {
		t.Fatalf("value = %q, want %q", h.Anomalies[0].Value, tt.value)
	}

	wantAmbiguous := len(tt.want) > 0
	for _, p := range all {
		if h.Ambiguous(p) != wantAmbiguous {
			t.Fatalf("Ambiguous(%s) = %v, want %v", p, h.Ambiguous(p), wantAmbiguous)
		}
	}
}

func TestHistoryAnomalies(t *testing.T) {
	tests := slices.Concat(
		cycleAnomalyCases(), statusAnomalyCases(), competingAnomalyCases(), duplicateIDAnomalyCases(), quietAnomalyCases(),
	)

	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			checkAnomalyCase(t, &tests[i])
		})
	}
}

func TestHistoryAnomaliesOnlyForRegisteredComponents(t *testing.T) {
	decs := []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "accepted", "")}
	edges := []adr.SupersedesEdge{{From: pA, To: pB}, {From: pB, To: pA}}

	h := histOf(decs, edges, pC)
	if len(h.Anomalies) != 0 || len(h.Nodes) != 1 {
		t.Fatalf("an unregistered cycle must not be reported: %+v", h)
	}

	if h.Ambiguous(pC) || h.Ambiguous(pA) {
		t.Fatalf("an unregistered cycle must not be reported: %+v", h)
	}

	if state, _, _ := h.Applicability(pA); state != adr.ApplicabilityUnknown {
		t.Fatalf("a path outside the analyzed components = %s, want unknown", state)
	}
}

func TestHistoryDuplicateIDWithRegisteredMemberMarksItsComponent(t *testing.T) {
	// The registered decision is alone in its component; its twin sits elsewhere.
	decs := []adr.EvidenceDecision{dec(pA, "ADR-1", "accepted", ""), dec("other/0001.md", "ADR-1", "accepted", "")}

	h := histOf(decs, nil, pA)
	if !h.Ambiguous(pA) {
		t.Fatalf("duplicate label must make the registered decision ambiguous: %v", anomalyKinds(h))
	}

	if state, reason, _ := h.Applicability(pA); state != adr.ApplicabilityAmbiguous || reason != "duplicate-id" {
		t.Fatalf("applicability = (%s, %q)", state, reason)
	}
}

func TestHistoryAnomalyOrderFollowsTheReasonList(t *testing.T) {
	decs := []adr.EvidenceDecision{
		dec(pA, "ADR-1", "superseded", "0099-x.md"), dec(pB, "ADR-2", "accepted", ""), dec(pC, "ADR-3", "accepted", ""),
	}
	edges := []adr.SupersedesEdge{{From: pB, To: pC}, {From: pC, To: pB}}

	h := histOf(decs, edges, pA, pB)
	kinds := make([]adr.Reason, 0, len(h.Anomalies))

	for _, a := range h.Anomalies {
		kinds = append(kinds, a.Kind)
	}

	if want := []adr.Reason{adr.ReasonCycle, adr.ReasonMissingTarget}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
}
