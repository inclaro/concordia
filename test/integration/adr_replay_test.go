package integration_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

// These tests replay the recording of the decision fixture (test/fixtures/adr) that `mise run record-cbm` made
// with codebase-memory-mcp 0.12.1 (cmd/record-cbm/adr.go). They assert observation O12 on a repository whose ADR
// files are not all linked by an edge, and need no CBM.

const (
	adrRecordingFile = "../fixtures/cbm-recordings/adr.json"
	adrProject       = "concordia-fixture-adr"
	adrDir           = "docs/adr/"
	adrOldFile       = adrDir + "0003-orders-own-database.md"
	adrNewFile       = adrDir + "0007-orders-managed-database.md"
)

// replayADR replays the index call and the whole graph of the recording.
func replayADR(t *testing.T) (graph *cbmprobe.Graph, index cbmprobe.IndexResult) {
	t.Helper()

	client, err := observation.LoadReplayClient(adrRecordingFile)
	if err != nil {
		t.Fatalf("load the recording: %v", err)
	}

	index, err = cbmprobe.Index(t.Context(), client, "/anywhere/else", adrProject)
	if err != nil {
		t.Fatalf("replay the index call: %v", err)
	}

	graph, err = cbmprobe.Read(t.Context(), client, adrProject)
	if err != nil {
		t.Fatalf("replay the graph: %v", err)
	}

	return graph, index
}

// adrProp is one expected property of an ADR node.
type adrProp struct{ key, want string }

// requireADRProps fails unless node carries every wanted property; label names the node in a failure.
func requireADRProps(t *testing.T, label string, node *cbmprobe.Node, wants ...adrProp) {
	t.Helper()

	for _, w := range wants {
		requireProp(t, label+" "+w.key, node.Prop(w.key), w.want)
	}
}

func adrNodesByPath(graph *cbmprobe.Graph) map[string]*cbmprobe.Node {
	byPath := map[string]*cbmprobe.Node{}
	for _, n := range graph.NodesOf("ADR") {
		byPath[n.FilePath] = n
	}

	return byPath
}

func TestADRRecordingReproducesObservationO12(t *testing.T) {
	graph, index := replayADR(t)
	byPath := adrNodesByPath(graph)

	t.Run("adr_id comes from the number in the title", func(t *testing.T) { requireADRIDFromTitle(t, byPath) })
	t.Run("status is normalized and status_text is the raw text", func(t *testing.T) { requireADRStatus(t, byPath) })
	t.Run("superseded_by is link text, not an ID", func(t *testing.T) { requireADRSupersededBy(t, byPath) })
	t.Run("SUPERSEDES goes from 0007 to 0003 with via, syntax, tier and line", func(t *testing.T) {
		requireADRSupersedesEdge(t, graph, byPath)
	})
	t.Run("adr_present is false next to indexed ADR nodes", func(t *testing.T) {
		requireADRPresentFalse(t, byPath, &index)
	})
}

func requireADRIDFromTitle(t *testing.T, byPath map[string]*cbmprobe.Node) {
	t.Helper()

	old, newer := byPath[adrOldFile], byPath[adrNewFile]
	if old == nil || newer == nil {
		t.Fatalf("ADR nodes = %v", byPath)
	}

	// 0003 gives ADR-3 and 0007 gives ADR-7: the leading zeros of the file name are dropped.
	requireProp(t, "0003 adr_id", old.Prop("adr_id"), "ADR-3")
	requireProp(t, "0007 adr_id", newer.Prop("adr_id"), "ADR-7")
	requireProp(t, "0003 title", old.Prop("title"), "ADR-0003: Orders owns its database")

	// The title number decides, not the file name: ADR-01601 in 1601-flags-old.md gives ADR-1601.
	flags := byPath[adrDir+"variants/v16/1601-flags-old.md"]
	if flags == nil {
		t.Fatal("no node for the V16 file")
	}

	requireProp(t, "V16 adr_id", flags.Prop("adr_id"), "ADR-1601")
	requireProp(t, "V16 title", flags.Prop("title"), "ADR-01601: Old flags")
}

func requireADRStatus(t *testing.T, byPath map[string]*cbmprobe.Node) {
	t.Helper()

	requireADRProps(t, "0003", byPath[adrOldFile],
		adrProp{"status", "accepted"}, adrProp{"status_text", "Accepted"},
		adrProp{"date", "2026-02-01"}, adrProp{"deciders", "Team Orders"})
	requireADRProps(t, "V02", byPath[adrDir+"variants/v02/0201-queue-self.md"],
		adrProp{"status", "superseded"}, adrProp{"status_text", "Superseded by ADR-0202"})
	requireADRProps(t, "V13", byPath[adrDir+"variants/v13/1302-cdn-new.md"], adrProp{"status", "proposed"})
	requireADRProps(t, "V09", byPath[adrDir+"variants/v09/0902-mail-new.md"], adrProp{"status", "rejected"})
}

func requireADRSupersededBy(t *testing.T, byPath map[string]*cbmprobe.Node) {
	t.Helper()

	requireADRProps(t, "V02", byPath[adrDir+"variants/v02/0201-queue-self.md"],
		adrProp{"superseded_by", "0202-queue-managed.md"})

	// Several links are joined with a comma; a `./` prefix and a fragment are kept verbatim.
	requireADRProps(t, "V04", byPath[adrDir+"variants/v04/0401-auth-basic.md"],
		adrProp{"superseded_by", "0402-auth-token.md, 0403-auth-oidc.md"})
	requireADRProps(t, "V16", byPath[adrDir+"variants/v16/1601-flags-old.md"],
		adrProp{"superseded_by", "./1602-flags-new.md#decision"})

	// A link to a file that is not a decision is kept too.
	requireADRProps(t, "V07", byPath[adrDir+"variants/v07/0701-tracing-old.md"],
		adrProp{"superseded_by", "0799-tracing-gone.md"})

	// A decision with no link has no property.
	requireADRProps(t, "0007", byPath[adrNewFile], adrProp{"superseded_by", ""})
}

func requireADRSupersedesEdge(t *testing.T, graph *cbmprobe.Graph, byPath map[string]*cbmprobe.Node) {
	t.Helper()

	var found *cbmprobe.Edge

	for _, e := range graph.EdgesOf("SUPERSEDES") {
		if e.From.FilePath == adrNewFile {
			found = e
		}
	}

	if found == nil {
		t.Fatal("no SUPERSEDES edge from 0007")
	}

	if found.To != byPath[adrOldFile] || found.From != byPath[adrNewFile] {
		t.Fatalf("SUPERSEDES goes %s -> %s, want 0007 -> 0003", found.From.FilePath, found.To.FilePath)
	}

	for name, want := range map[string]string{"via": "markdown", "syntax": "supersedes", "tier": "exact", "line": "9"} {
		requireProp(t, "SUPERSEDES "+name, found.Prop(name), want)
	}
}

func requireADRPresentFalse(t *testing.T, byPath map[string]*cbmprobe.Node, index *cbmprobe.IndexResult) {
	t.Helper()

	// The editable ADR store of CBM (manage_adr) is separate from ADR nodes: 37 ADR files are indexed and
	// the index result still reports that the store has no ADR.
	if len(byPath) < 37 {
		t.Fatalf("ADR nodes = %d, want at least 37", len(byPath))
	}

	if index.ADRPresent {
		t.Error("adr_present is true; the ADR store is separate from the ADR nodes")
	}
}

// adrEvidenceFromRecording freezes the recording and builds the decision evidence the adapter gives conformance.
func adrEvidenceFromRecording(t *testing.T) adr.DecisionEvidence {
	t.Helper()

	client, err := observation.LoadReplayClient(adrRecordingFile)
	if err != nil {
		t.Fatalf("load the recording: %v", err)
	}

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: "0.12.1"})

	ref, err := provider.Freeze(t.Context(), observation.FreezeRequest{Project: adrProject, GitRevision: client.HeadSHA()})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}

	res, err := provider.FetchEdges(t.Context(), ref, []string{"DEFINES", "SUPERSEDES", "MENTIONS", "REFERENCES_FILE"})
	if err != nil {
		t.Fatalf("fetch edges: %v", err)
	}

	ev := adr.BuildEvidence(&res, provider.Capabilities(t.Context()), nil)
	if !ev.Supported || ev.UnresolvedSupersedesObservable {
		t.Fatalf("supported = %v, unresolved observable = %v", ev.Supported, ev.UnresolvedSupersedesObservable)
	}

	return ev
}

func TestADRRecordingGivesEveryDecisionToTheAdapter(t *testing.T) {
	ev := adrEvidenceFromRecording(t)

	byPath := map[string]adr.EvidenceDecision{}
	for i := range ev.Decisions {
		byPath[ev.Decisions[i].Path] = ev.Decisions[i]
	}

	// A decision that no edge touches is in the evidence: the V08 file has no link at all.
	lone, ok := byPath[adrDir+"variants/v08/0801-batch-old.md"]
	if !ok || !isUnlinkedSupersededDecision(&lone) {
		t.Fatalf("decision without an edge = %+v, present %v", lone, ok)
	}

	old := byPath[adrOldFile]
	if !isADR3Decision(&old) {
		t.Fatalf("0003 = %+v", old)
	}

	if !hasADRSupersedesEdge(&ev, adrNewFile, adrOldFile, 9) {
		t.Fatalf("no edge 0007 -> 0003 in %+v", ev.Supersedes)
	}

	// 0007 mentions the orders source file (candidate evidence only).
	if !hasADRMention(&ev, adrNewFile, "services/orders/src/orders.ts") {
		t.Fatalf("mentions = %+v", ev.Mentions)
	}
}

func isUnlinkedSupersededDecision(d *adr.EvidenceDecision) bool {
	return d.Label == "ADR-801" && d.Status == "superseded" && d.SupersededBy == ""
}

func isADR3Decision(d *adr.EvidenceDecision) bool {
	return d.Label == "ADR-3" && d.Deciders == "Team Orders" && d.Line == 1 && strings.HasPrefix(d.Title, "ADR-0003")
}

func hasADRSupersedesEdge(ev *adr.DecisionEvidence, from, to string, line int) bool {
	for _, e := range ev.Supersedes {
		if e.From != from || e.To != to {
			continue
		}

		if e.Line == line {
			return true
		}
	}

	return false
}

func hasADRMention(ev *adr.DecisionEvidence, from, subject string) bool {
	for _, m := range ev.Mentions {
		if m.From == from && m.Subject == subject {
			return true
		}
	}

	return false
}
