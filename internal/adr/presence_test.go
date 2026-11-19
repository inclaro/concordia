package adr_test

import (
	"context"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/workspace"
)

const testRev = "c0ffee"

// decisionAt builds an evidence decision with the label of its ID.
func decisionAt(path, id, status string) adr.EvidenceDecision {
	return adr.EvidenceDecision{
		Path: path, ObservedID: "adr:" + path, AdrID: id, Label: adr.CanonicalID(id), Title: "Title " + id,
		Status: status, StatusText: status,
	}
}

// supportedEvidence gives complete, supported evidence with the decisions.
func supportedEvidence(decisions ...adr.EvidenceDecision) *adr.DecisionEvidence {
	return &adr.DecisionEvidence{
		Supported: true, ExtractionComplete: true, Decisions: decisions,
		Supersedes: []adr.SupersedesEdge{}, Mentions: []adr.Mention{}, Stale: []string{},
	}
}

// factsFrom derives the adr.GitFacts of a project from a FakeGit, as the evaluation does through the Git port.
func factsFrom(t *testing.T, facts *adr.GitFacts, project, prefix string, g *workspace.FakeGit) *adr.GitFacts {
	t.Helper()

	if facts == nil {
		facts = &adr.GitFacts{Revisions: map[string]string{}, Tracked: map[string]bool{}, Dirty: map[string]bool{}}
	}

	ctx := context.Background()

	rev, err := g.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}

	facts.Revisions[project] = rev

	tracked, err := g.ListFilesAt(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range tracked {
		facts.Tracked[prefix+p] = true
	}

	dirty, err := g.DirtyFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range dirty {
		facts.Dirty[prefix+p] = true
	}

	return facts
}

func fakeGit(rev string, tracked []string, dirty ...string) *workspace.FakeGit {
	files := map[string]string{}
	for _, p := range tracked {
		files[p] = "text"
	}

	return &workspace.FakeGit{Rev: rev, FilesAt: map[string]map[string]string{rev: files}, Dirty: dirty}
}

func testEntry(id, path, project string) adr.Entry {
	return adr.Entry{ID: adr.CanonicalID(id), Path: path, Project: project}
}

// presenceADR3 is the path of the ADR-3 file of the presence cases.
const presenceADR3 = "docs/adr/0003-orders-db.md"

// presenceCase is one row of TestPresenceStates.
type presenceCase struct {
	name   string
	state  adr.PresenceState
	reason string
	path   string
	ev     func() *adr.DecisionEvidence
	git    *workspace.FakeGit
	entry  adr.Entry
}

// presenceADR3Evidence is the evidence of the one accepted ADR-3.
func presenceADR3Evidence() *adr.DecisionEvidence {
	return supportedEvidence(decisionAt(presenceADR3, "ADR-3", "accepted"))
}

// presenceTrackedCases are the cases where the ADR file is tracked, missing, or edited.
func presenceTrackedCases() []presenceCase {
	return []presenceCase{
		{
			name: "committed ADR", entry: testEntry("ADR-0003", "", ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, []string{presenceADR3}),
			state: adr.PresenceCommitted, path: presenceADR3,
		},
		{
			name: "committed by path pin", entry: testEntry("ADR-0003", presenceADR3, ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, []string{presenceADR3}),
			state: adr.PresenceCommitted, path: presenceADR3,
		},
		{
			name: "index-only: untracked file", entry: testEntry("ADR-3", "", ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, []string{"README.md"}),
			state: adr.PresenceIndexOnly, reason: "index-only", path: presenceADR3,
		},
		{
			name: "index-only: deleted file", entry: testEntry("ADR-3", presenceADR3, ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, nil, presenceADR3),
			state: adr.PresenceIndexOnly, reason: "index-only", path: presenceADR3,
		},
		// Scenario: ADR is absent.
		{
			name: "absent with complete coverage", entry: testEntry("ADR-9", "", ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, []string{presenceADR3}),
			state: adr.PresenceAbsent, reason: "absent",
		},
		{
			name: "absent pinned path", entry: testEntry("ADR-9", "docs/adr/0009.md", ""),
			ev:    func() *adr.DecisionEvidence { return supportedEvidence() },
			git:   fakeGit(testRev, nil),
			state: adr.PresenceAbsent, reason: "absent", path: "docs/adr/0009.md",
		},
		// Scenario: Uncommitted edit of a tracked ADR.
		{
			name: "dirty tracked file is an uncommitted edit", entry: testEntry("ADR-3", "", ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, []string{presenceADR3}, presenceADR3),
			state: adr.PresenceUncommittedEdit, reason: "uncommitted-edit", path: presenceADR3,
		},
		{
			name: "stale index is an uncommitted edit", entry: testEntry("ADR-3", "", ""),
			ev: func() *adr.DecisionEvidence {
				ev := presenceADR3Evidence()
				ev.Stale = []string{presenceADR3}

				return ev
			},
			git:   fakeGit(testRev, []string{presenceADR3}),
			state: adr.PresenceUncommittedEdit, reason: "uncommitted-edit", path: presenceADR3,
		},
	}
}

// presenceUnverifiableCases are the cases where the evidence or the Git facts cannot decide, and the cases of the
// tracked files that the provider does not recognize.
func presenceUnverifiableCases() []presenceCase {
	return []presenceCase{
		{
			name: "no revision", entry: testEntry("ADR-3", "", ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit("", nil),
			state: adr.PresenceUnverifiable, reason: "no-revision",
		},
		// Scenario: Provider has no ADR capability.
		{
			name: "provider without ADR capability", entry: testEntry("ADR-3", "", ""),
			ev:    func() *adr.DecisionEvidence { return &adr.DecisionEvidence{ExtractionComplete: true} },
			git:   fakeGit(testRev, []string{presenceADR3}),
			state: adr.PresenceUnverifiable, reason: "no-capability",
		},
		{
			name: "incomplete extraction without entity", entry: testEntry("ADR-9", "", ""),
			ev: func() *adr.DecisionEvidence {
				ev := supportedEvidence()
				ev.ExtractionComplete = false

				return ev
			},
			git:   fakeGit(testRev, nil),
			state: adr.PresenceUnverifiable, reason: "extraction-incomplete",
		},
		{
			name: "incomplete extraction with entity still decides", entry: testEntry("ADR-3", "", ""),
			ev: func() *adr.DecisionEvidence {
				ev := presenceADR3Evidence()
				ev.ExtractionComplete = false

				return ev
			},
			git:   fakeGit(testRev, []string{presenceADR3}),
			state: adr.PresenceCommitted, path: presenceADR3,
		},
		// Scenario: Tracked file not recognized as an ADR.
		{
			name: "tracked but not recognized", entry: testEntry("ADR-9", "docs/adr/0009.md", ""),
			ev:    func() *adr.DecisionEvidence { return supportedEvidence() },
			git:   fakeGit(testRev, []string{"docs/adr/0009.md"}),
			state: adr.PresenceNotRecognized, reason: "not-recognized", path: "docs/adr/0009.md",
		},
		{
			name: "path pin does not match", entry: testEntry("ADR-3", "docs/adr/other.md", ""),
			ev:    presenceADR3Evidence,
			git:   fakeGit(testRev, []string{presenceADR3, "docs/adr/other.md"}),
			state: adr.PresencePathMismatch, reason: "path-mismatch", path: "docs/adr/other.md",
		},
		{
			name: "duplicate label for an unpinned entry", entry: testEntry("ADR-3", "", ""),
			ev: func() *adr.DecisionEvidence {
				return supportedEvidence(
					decisionAt(presenceADR3, "ADR-3", "accepted"), decisionAt("docs/old/0003.md", "ADR-0003", "accepted"))
			},
			git:   fakeGit(testRev, []string{presenceADR3, "docs/old/0003.md"}),
			state: adr.PresenceUnverifiable, reason: "duplicate-id",
		},
	}
}

func TestPresenceStates(t *testing.T) {
	for _, tt := range slices.Concat(presenceTrackedCases(), presenceUnverifiableCases()) {
		t.Run(tt.name, func(t *testing.T) {
			ev := tt.ev()
			got := adr.EvaluatePresence([]adr.Entry{tt.entry}, ev, factsFrom(t, nil, "", "", tt.git))[adr.EntryKey(&tt.entry)]

			evalWantEqual(t, "state", got.State, tt.state)
			evalWantEqual(t, "reason", got.Reason, tt.reason)
			evalWantEqual(t, "path", got.Path, tt.path)
		})
	}
}

func TestPresencePathMismatchNamesBothPaths(t *testing.T) {
	e := testEntry("ADR-3", "docs/adr/pinned.md", "")
	ev := supportedEvidence(decisionAt("docs/adr/0003.md", "ADR-3", "accepted"))
	git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{"docs/adr/0003.md"}))

	got := adr.EvaluatePresence([]adr.Entry{e}, ev, git)[adr.EntryKey(&e)]
	evalWantEqual(t, "path", got.Path, "docs/adr/pinned.md")
	evalWantSlice(t, "other paths", got.OtherPaths, []string{"docs/adr/0003.md"})
}

func TestPresenceRegisterIDWithLeadingZeros(t *testing.T) {
	e := testEntry("ADR-0003", "", "")
	ev := supportedEvidence(decisionAt("docs/adr/0003.md", "ADR-3", "accepted"))
	git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{"docs/adr/0003.md"}))

	if got := adr.EvaluatePresence([]adr.Entry{e}, ev, git)[adr.EntryKey(&e)]; got.State != adr.PresenceCommitted {
		t.Fatalf("presence = %+v, want committed", got)
	}
}

// Scenario: ADR file is renamed.
func TestPresenceADRRenamedKeepsTheRegisterID(t *testing.T) {
	e := testEntry("ADR-0003", "", "")
	before, after := "docs/adr/0003-orders.md", "docs/decisions/0003-orders-db.md"

	for _, p := range []string{before, after} {
		ev := supportedEvidence(decisionAt(p, "ADR-3", "accepted"))
		git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{p}))

		got := adr.EvaluatePresence([]adr.Entry{e}, ev, git)[adr.EntryKey(&e)]
		evalWantEqual(t, "state at "+p, got.State, adr.PresenceCommitted)
		evalWantEqual(t, "path at "+p, got.Path, p)
		evalRequireEqual(t, "decision set at "+p, got.Decision != nil, true)
		evalWantEqual(t, "label at "+p, got.Decision.Label, "ADR-3")
	}
}

func TestPresenceDuplicateWithOneTrackedCandidateUsesTheTrackedOne(t *testing.T) {
	e := testEntry("ADR-3", "", "")
	ev := supportedEvidence(
		decisionAt("docs/adr/0003.md", "ADR-3", "accepted"), decisionAt("scratch/0003.md", "ADR-0003", "accepted"))
	git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{"docs/adr/0003.md"}))

	got := adr.EvaluatePresence([]adr.Entry{e}, ev, git)[adr.EntryKey(&e)]
	if got.State != adr.PresenceCommitted || got.Path != "docs/adr/0003.md" {
		t.Fatalf("presence = %+v, want the tracked candidate", got)
	}
}

func TestPresenceDuplicateReportsEveryCandidatePath(t *testing.T) {
	e := testEntry("ADR-3", "", "")
	ev := supportedEvidence(decisionAt("b/0003.md", "ADR-3", "accepted"), decisionAt("a/0003.md", "ADR-3", "accepted"))
	git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{"a/0003.md", "b/0003.md"}))

	got := adr.EvaluatePresence([]adr.Entry{e}, ev, git)[adr.EntryKey(&e)]
	evalWantEqual(t, "state", got.State, adr.PresenceUnverifiable)
	evalWantEqual(t, "reason", got.Reason, "duplicate-id")
	evalWantEqual(t, "no decision", got.Decision == nil, true)
	evalRequireEqual(t, "other paths", len(got.OtherPaths), 2)
	evalWantEqual(t, "first other path", got.OtherPaths[0], "a/0003.md")
}

func TestPresenceMultiProject(t *testing.T) {
	api := decisionAt("@api/docs/adr/0001.md", "ADR-1", "accepted")
	api.Project = "api"
	web := decisionAt("@web/docs/adr/0001.md", "ADR-1", "accepted")
	web.Project = "web"

	ev := supportedEvidence(api, web)
	git := factsFrom(t, nil, "api", "@api/", fakeGit("aaa", []string{"docs/adr/0001.md"}))
	git = factsFrom(t, git, "web", "@web/", fakeGit("bbb", []string{"docs/adr/0001.md"}))

	pinned := testEntry("ADR-1", "", "api")
	other := testEntry("ADR-1", "", "web")
	shared := testEntry("ADR-1", "", "")
	withPath := testEntry("ADR-1", "docs/adr/0001.md", "api")

	got := adr.EvaluatePresence([]adr.Entry{pinned, other, shared, withPath}, ev, git)

	if r := got[adr.EntryKey(&pinned)]; r.State != adr.PresenceCommitted || r.Path != "@api/docs/adr/0001.md" {
		t.Fatalf("api entry = %+v", r)
	}

	if r := got[adr.EntryKey(&other)]; r.State != adr.PresenceCommitted || r.Path != "@web/docs/adr/0001.md" {
		t.Fatalf("web entry = %+v", r)
	}

	if r := got[adr.EntryKey(&shared)]; r.State != adr.PresenceUnverifiable || r.Reason != "duplicate-id" {
		t.Fatalf("unpinned entry with one label in two projects = %+v", r)
	}

	if r := got[adr.EntryKey(&withPath)]; r.State != adr.PresenceCommitted {
		t.Fatalf("path pin is project-relative: %+v", r)
	}
}

func TestPresenceWithoutEvidenceOrFacts(t *testing.T) {
	e := testEntry("ADR-3", "", "")

	if r := adr.EvaluatePresence([]adr.Entry{e}, nil, nil)[adr.EntryKey(&e)]; r.State != adr.PresenceUnverifiable || r.Reason != "no-revision" {
		t.Fatalf("nil facts = %+v", r)
	}

	git := factsFrom(t, nil, "", "", fakeGit(testRev, nil))
	if r := adr.EvaluatePresence([]adr.Entry{e}, nil, git)[adr.EntryKey(&e)]; r.State != adr.PresenceUnverifiable || r.Reason != "no-capability" {
		t.Fatalf("nil evidence = %+v", r)
	}
}
