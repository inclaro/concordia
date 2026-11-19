package adr_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
)

// dec builds an evidence decision with a superseded_by value.
func dec(path, id, status, supersededBy string) adr.EvidenceDecision {
	d := decisionAt(path, id, status)
	d.SupersededBy = supersededBy

	return d
}

func histOf(decisions []adr.EvidenceDecision, edges []adr.SupersedesEdge, registered ...string) *adr.History {
	ev := supportedEvidence(decisions...)
	ev.Supersedes = edges

	return adr.BuildHistory(ev, nil, nil, registered)
}

// linkSummary renders links as `from>to:provenance:effect` (successor to predecessor).
func linkSummary(h *adr.History) []string {
	out := make([]string, 0, len(h.Links))
	for _, l := range h.Links {
		out = append(out, l.FromPath+">"+l.ToPath+":"+string(l.Provenance)+":"+string(l.Effect))
	}

	return out
}

func anomalyKinds(h *adr.History) []string {
	out := make([]string, 0, len(h.Anomalies))
	for _, a := range h.Anomalies {
		out = append(out, string(a.Kind)+" "+strings.Join(a.Paths, ","))
	}

	return out
}

const (
	p3  = "docs/adr/0003-orders-own-db.md"
	p7  = "docs/adr/0007-managed-db.md"
	p9  = "docs/adr/0009-other.md"
	p10 = "docs/adr/0010-final.md"
)

// linkProvenanceCase is one row of TestHistoryLinkProvenance.
type linkProvenanceCase struct {
	name  string
	decs  []adr.EvidenceDecision
	edges []adr.SupersedesEdge
	want  []string
	// competing says the targets are several successors of one decision.
	competing bool
}

// linkProvenanceEdgeAndMetadataCases are the links that one edge or one `superseded_by` value gives.
func linkProvenanceEdgeAndMetadataCases() []linkProvenanceCase {
	return []linkProvenanceCase{
		{ // SUP-V01
			name:  "edge only",
			decs:  []adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", ""), dec(p7, "ADR-7", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: p7, To: p3, Line: 12}},
			want:  []string{p7 + ">" + p3 + ":edge:effective"},
		},
		// Scenario: Superseded-by is a relative file name.
		{ // SUP-V02
			name: "metadata only, relative file name",
			decs: []adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", "0007-managed-db.md"), dec(p7, "ADR-7", "accepted", "")},
			want: []string{p7 + ">" + p3 + ":metadata:effective"},
		},
		{ // SUP-V03
			name:  "edge and metadata agree",
			decs:  []adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", "./0007-managed-db.md"), dec(p7, "ADR-7", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: p7, To: p3}},
			want:  []string{p7 + ">" + p3 + ":both:effective"},
		},
		{
			name: "bare canonical ID with leading zeros resolves by label",
			decs: []adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", "ADR-0007"), dec(p7, "ADR-7", "accepted", "")},
			want: []string{p7 + ">" + p3 + ":metadata:effective"},
		},
		{ // SUP-V16: the register spells ADR-0003, the provider says ADR-3
			name:  "label comparison is canonical",
			decs:  []adr.EvidenceDecision{dec(p3, "ADR-0003", "superseded", "adr-7"), dec(p7, "ADR-0007", "accepted", "")},
			edges: []adr.SupersedesEdge{{From: p7, To: p3}},
			want:  []string{p7 + ">" + p3 + ":both:effective"},
		},
	}
}

// linkProvenanceValueCases are the links that the shape of a `superseded_by` value gives.
func linkProvenanceValueCases() []linkProvenanceCase {
	return []linkProvenanceCase{
		// Scenario: Superseded-by names several decisions.
		{ // SUP-V04
			name: "superseded-by links two decision files", competing: true,
			decs: []adr.EvidenceDecision{
				dec(p3, "ADR-3", "superseded", "0007-managed-db.md, 0009-other.md"), dec(p7, "ADR-7", "accepted", ""),
				dec(p9, "ADR-9", "accepted", ""),
			},
			want: []string{p7 + ">" + p3 + ":metadata:effective", p9 + ">" + p3 + ":metadata:effective"},
		},
		{
			name: "semicolon and whitespace separators", competing: true,
			decs: []adr.EvidenceDecision{
				dec(p3, "ADR-3", "superseded", "0007-managed-db.md;0009-other.md\n"+"0010-final.md"),
				dec(p7, "ADR-7", "accepted", ""), dec(p9, "ADR-9", "accepted", ""), dec(p10, "ADR-10", "accepted", ""),
			},
			want: []string{
				p7 + ">" + p3 + ":metadata:effective", p9 + ">" + p3 + ":metadata:effective",
				p10 + ">" + p3 + ":metadata:effective",
			},
		},
		{
			name: "fragment, backticks, angle brackets, parent directory",
			decs: []adr.EvidenceDecision{
				dec(p3, "ADR-3", "superseded", "`0007-managed-db.md#decision`"),
				dec(p9, "ADR-9", "superseded", "<../adr/0010-final.md>"),
				dec(p7, "ADR-7", "accepted", ""), dec(p10, "ADR-10", "accepted", ""),
			},
			want: []string{p7 + ">" + p3 + ":metadata:effective", p10 + ">" + p9 + ":metadata:effective"},
		},
	}
}

func TestHistoryLinkProvenance(t *testing.T) {
	for _, tt := range slices.Concat(linkProvenanceEdgeAndMetadataCases(), linkProvenanceValueCases()) {
		t.Run(tt.name, func(t *testing.T) {
			all := make([]string, 0, len(tt.decs))
			for _, d := range tt.decs {
				all = append(all, d.Path)
			}

			h := histOf(tt.decs, tt.edges, all...)
			if got := linkSummary(h); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("links = %v, want %v", got, tt.want)
			}

			// Two successors of one decision compete (V04): the competing-successor rule decides.
			if tt.competing != (len(h.Anomalies) > 0) {
				t.Fatalf("anomalies = %v", anomalyKinds(h))
			}
		})
	}
}

func TestHistoryMissingTargets(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"file that is not a committed decision", "0099-unknown.md", "0099-unknown.md"},
		{"target escaping the project", "../../../../etc/passwd.md", "../../../../etc/passwd.md"},
		{"parent directory above the project root", "../../../0007-managed-db.md", "../../../0007-managed-db.md"},
		{"absolute path", "/docs/adr/0007-managed-db.md", "/docs/adr/0007-managed-db.md"},
		{"bare ID is only for non-paths", "ADR-0007.md", "ADR-0007.md"},
		{"bare ID with a separator is a path", "adr/ADR-0007", "adr/ADR-0007"},
		{"unknown bare ID", "ADR-0099", "ADR-0099"},
		{"only a fragment", "#section", "#section"},
		{"only separators", " , ; ", " , ; "},
		{"URL is never followed", "https://example.test/0007-managed-db.md", "https://example.test/0007-managed-db.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := histOf([]adr.EvidenceDecision{dec(p3, "ADR-3", "superseded", tt.value), dec(p7, "ADR-7", "accepted", "")}, nil, p3)

			wantSingleMissingTarget(t, h, tt.want)

			if len(h.Links) != 0 {
				t.Fatalf("links = %v, want none", linkSummary(h))
			}
		})
	}
}

// wantSingleMissingTarget wants exactly one missing-target anomaly on p3 with the value.
func wantSingleMissingTarget(t *testing.T, h *adr.History, value string) {
	t.Helper()

	if len(h.Anomalies) != 1 {
		t.Fatalf("anomalies = %+v, want one missing-target %q on %s", h.Anomalies, value, p3)
	}

	anomaly := h.Anomalies[0]
	if anomaly.Kind != adr.ReasonMissingTarget || anomaly.Value != value {
		t.Fatalf("anomalies = %+v, want one missing-target %q on %s", h.Anomalies, value, p3)
	}

	if !reflect.DeepEqual(anomaly.Paths, []string{p3}) {
		t.Fatalf("anomalies = %+v, want one missing-target %q on %s", h.Anomalies, value, p3)
	}
}

func TestHistoryMissingTargetNamesOnlyTheBadToken(t *testing.T) {
	h := histOf([]adr.EvidenceDecision{
		dec(p3, "ADR-3", "superseded", "0007-managed-db.md 0099-unknown.md"), dec(p7, "ADR-7", "accepted", ""),
	}, nil, p3)

	if len(h.Anomalies) != 1 || h.Anomalies[0].Value != "0099-unknown.md" {
		t.Fatalf("anomalies = %+v", h.Anomalies)
	}

	if got := linkSummary(h); !reflect.DeepEqual(got, []string{p7 + ">" + p3 + ":metadata:effective"}) {
		t.Fatalf("links = %v", got)
	}
}

func TestHistoryEdgeToUncommittedDecisionIsMissing(t *testing.T) {
	ev := supportedEvidence(dec(p7, "ADR-7", "accepted", ""), dec(p3, "ADR-3", "accepted", ""))
	ev.Supersedes = []adr.SupersedesEdge{{From: p7, To: p3}}

	h := adr.BuildHistory(ev, nil, func(p string) bool { return p == p7 }, []string{p7})
	if got := anomalyKinds(h); !reflect.DeepEqual(got, []string{"missing-target " + p7}) {
		t.Fatalf("anomalies = %+v", h.Anomalies)
	}

	if h.Anomalies[0].Value != p3 {
		t.Fatalf("anomalies = %+v", h.Anomalies)
	}

	// An edge from an uncommitted decision is ignored: its facts are unknown.
	ev.Supersedes = []adr.SupersedesEdge{{From: p3, To: p7}}

	h = adr.BuildHistory(ev, nil, func(p string) bool { return p == p7 }, []string{p7})
	if len(h.Anomalies) != 0 || len(h.Links) != 0 {
		t.Fatalf("edge from an uncommitted decision must be ignored: %+v %v", h.Anomalies, h.Links)
	}
}

func TestHistoryRelativeToHoldingADRInProjects(t *testing.T) {
	a := dec("@api/docs/adr/0003.md", "ADR-3", "superseded", "../adr/0007.md")
	a.Project = "api"
	b := dec("@api/docs/adr/0007.md", "ADR-7", "accepted", "")
	b.Project = "api"
	c := dec("@web/docs/adr/0003.md", "ADR-3", "superseded", "../../../api/docs/adr/0007.md")
	c.Project = "web"

	h := histOf([]adr.EvidenceDecision{a, b, c}, nil, a.Path, c.Path)

	if got := linkSummary(h); !reflect.DeepEqual(got, []string{b.Path + ">" + a.Path + ":metadata:effective"}) {
		t.Fatalf("links = %v", got)
	}

	// The web decision may not reach into another project; it is also a duplicate label with the api ADR-3.
	want := []string{"missing-target " + c.Path, "duplicate-id " + a.Path + "," + c.Path}
	if got := anomalyKinds(h); !reflect.DeepEqual(got, want) {
		t.Fatalf("anomalies = %v, want %v", got, want)
	}
}

func TestHistoryNodesAreCommittedOnly(t *testing.T) {
	ev := supportedEvidence(dec(p3, "ADR-3", "accepted", ""), dec(p7, "ADR-7", "accepted", ""))
	ev.Supersedes = []adr.SupersedesEdge{{From: p7, To: p3}}

	h := adr.BuildHistory(ev, nil, func(p string) bool { return p == p3 }, []string{p3, p7})
	if len(h.Nodes) != 1 || h.Nodes[0].Path != p3 {
		t.Fatalf("nodes = %+v, want only the committed decision", h.Nodes)
	}

	if state, reason, _ := h.Applicability(p7); state != adr.ApplicabilityUnknown || reason != adr.EvidenceNotCommitted {
		t.Fatalf("uncommitted registered path = (%s, %q)", state, reason)
	}
}

func TestHistoryNodeCarriesLifecycleFields(t *testing.T) {
	lc, err := adr.NewLifecycle(nil, map[string]string{"Approved": "accepted"})
	if err != nil {
		t.Fatal(err)
	}

	d := dec(p3, "ADR-0003", "unknown", "")
	d.StatusText = "Approved"

	ev := supportedEvidence(d)
	h := adr.BuildHistory(ev, lc, nil, []string{p3})

	if len(h.Nodes) != 1 {
		t.Fatalf("node = %+v", h.Nodes)
	}

	node := &h.Nodes[0]
	got := [5]string{node.Label, node.Mapped, string(node.Class), node.Decision.StatusText, node.Decision.Status}

	if want := [5]string{"ADR-3", "accepted", string(adr.ClassActive), "Approved", "unknown"}; got != want {
		t.Fatalf("node = %+v, want %v", h.Nodes, want)
	}
}
