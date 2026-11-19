package adr_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
)

const (
	primaryPath = "docs/adr/0001-primary-db.md"
	billingPath = "docs/adr/0001-billing-db.md"
	billingRev  = "b111111"
)

// twoProjects gives a primary project and the project `billing`, each with its own Git revision and tracked files,
// and the decision ADR-0001 in both.
func twoProjects(t *testing.T, register string, billingTracked []string, billingRevision string) *world {
	t.Helper()

	w := newWorld(t)
	w.addADRIn("p", "1", primaryPath, "ADR-1", "ADR-0001: Primary database", "accepted", "Accepted")
	w.addADRIn("billing", "5", billingPath, "ADR-1", "ADR-0001: Billing database", "accepted", "Accepted")
	w.register = register

	primary := &recordingGit{FakeGit: fakeGit(testRev, []string{primaryPath, billingPath})} // same relative path: another project's file
	billing := &recordingGit{FakeGit: fakeGit(billingRev, billingTracked)}

	w.projects = []conformance.DecisionProject{
		{Git: primary, Revision: testRev},
		{Git: billing, Name: "billing", Prefix: "@billing/", Revision: billingRevision},
	}

	return w
}

const unpinned = `  - id: ADR-0001
    governs: [{ref: 'calm:node:billing'}]
`

const pinnedToBilling = `  - id: ADR-0001
    project: billing
    governs: [{ref: 'calm:node:billing'}]
`

// multiprojectEntryView is the comparable part of a summary decision entry that the scenarios assert on.
type multiprojectEntryView struct {
	Project       string
	Path          string
	Title         string
	Status        string
	Presence      string
	Reason        string
	Applicability string
	Governs       string
}

// The same ID in two projects with no project pin is a duplicate in one history.
func TestMultiProjectSameIDWithoutAPinIsADuplicate(t *testing.T) {
	t.Parallel()

	out := twoProjects(t, unpinned, []string{billingPath}, billingRev).evaluate()

	entry := out.Summary.Decisions[0]

	got := multiprojectEntryView{Presence: entry.Presence.State, Reason: entry.Presence.Reason, Applicability: entry.Applicability.State}
	if want := (multiprojectEntryView{Presence: string(adr.PresenceUnverifiable), Reason: "duplicate-id", Applicability: "unknown"}); got != want {
		t.Errorf("entry = %+v", entry)
	}

	history := findingsOf(out, adr.RuleHistory)
	if len(history) != 1 {
		t.Fatalf("history findings = %+v", history)
	}

	if got := [2]string{string(history[0].Kind), history[0].Source.ArchitectureID}; got != [2]string{"adr-duplicate-id", "ADR-1"} {
		t.Fatalf("history findings = %+v", history)
	}

	wantVerdicts(t, out, map[string]conformance.RuleVerdict{
		adr.RulePresence: conformance.VerdictUnknown, adr.RuleHistory: conformance.VerdictViolation,
	})
}

// Scenario: Scoped entry.
// A pinned entry reads only the decision of its project and checks it in the revision of that project.
func TestMultiProjectPinnedEntryUsesItsOwnProjectAndRevision(t *testing.T) {
	t.Parallel()

	out := twoProjects(t, pinnedToBilling, []string{billingPath}, billingRev).evaluate()

	entry := out.Summary.Decisions[0]
	got := multiprojectEntryView{
		Project: entry.Project, Path: entry.Path, Title: entry.Title, Presence: entry.Presence.State, Applicability: entry.Applicability.State,
	}
	want := multiprojectEntryView{
		Project: "billing", Path: "@" + "billing/" + billingPath, Title: "ADR-0001: Billing database", Presence: "committed", Applicability: "active",
	}

	if got != want {
		t.Errorf("entry = %+v", entry)
	}

	wantVerdicts(t, out, map[string]conformance.RuleVerdict{
		adr.RulePresence: conformance.VerdictPass, adr.RuleApplicability: conformance.VerdictPass, adr.RuleHistory: conformance.VerdictPass,
	})

	// The primary decision is a candidate: it is not registered.
	if len(out.Summary.Candidates) != 1 || out.Summary.Candidates[0].Path != primaryPath {
		t.Errorf("candidates = %+v", out.Summary.Candidates)
	}
}

// multiprojectEntryByID returns the summary entry with the ID, or nil.
func multiprojectEntryByID(out *conformance.DecisionEvaluation, id string) *conformance.DecisionEntry {
	for i := range out.Summary.Decisions {
		if out.Summary.Decisions[i].ID == id {
			return &out.Summary.Decisions[i]
		}
	}

	return nil
}

// A path pin selects the primary file whose label (ADR-1) the register scopes only to billing. The pinned decision
// stays in the history (its history node is read, not missing), and since two committed files now carry the label
// ADR-1, the history reports the duplicate and selects no active decision.
func TestMultiProjectPathPinKeepsAScopedLabelInTheHistory(t *testing.T) {
	t.Parallel()

	register := pinnedToBilling + `  - id: ADR-0009
    path: ` + primaryPath + `
    governs: [{ref: 'calm:node:billing'}]
`

	out := twoProjects(t, register, []string{billingPath}, billingRev).evaluate()

	pinned := multiprojectEntryByID(out, "ADR-9")
	if pinned == nil {
		t.Fatalf("pinned entry = %+v", pinned)
	}

	got := multiprojectEntryView{
		Path: pinned.Path, Status: pinned.Status, Presence: pinned.Presence.State, Applicability: pinned.Applicability.State,
	}
	want := multiprojectEntryView{
		Path: primaryPath, Status: adr.StatusAccepted, Presence: string(adr.PresenceCommitted), Applicability: string(adr.ApplicabilityAmbiguous),
	}

	if got != want {
		t.Fatalf("pinned entry = %+v", pinned)
	}
}

// namedPrimary is twoProjects with a named primary project `web`: the register can scope an entry to it by name.
func namedPrimary(t *testing.T, register string) *world {
	t.Helper()

	w := twoProjects(t, register, []string{billingPath}, billingRev)
	w.projects[0].Name = "web"

	return w
}

// An entry scoped to the named primary project keeps its subjects and its scope: it reads only the primary decision
// (no duplicate with billing's ADR-1) and governs what the register declares.
func TestMultiProjectEntryScopedToTheNamedPrimaryProject(t *testing.T) {
	t.Parallel()

	out := namedPrimary(t, `  - id: ADR-0001
    project: web
    governs: [{ref: 'calm:node:billing'}]
`).evaluate()

	entry := out.Summary.Decisions[0]
	got := multiprojectEntryView{
		Project: entry.Project, Path: entry.Path, Presence: entry.Presence.State, Applicability: entry.Applicability.State,
		Governs: strings.Join(entry.Governs, ","),
	}
	want := multiprojectEntryView{
		Project: "web", Path: primaryPath, Presence: string(adr.PresenceCommitted), Applicability: string(adr.ApplicabilityActive), Governs: "calm:node:billing",
	}

	if got != want {
		t.Errorf("entry = %+v", entry)
	}
}

// multiprojectApplicabilitySources returns the sorted fingerprint sources of the applicability findings.
func multiprojectApplicabilitySources(out *conformance.DecisionEvaluation) []string {
	found := findingsOf(out, adr.RuleApplicability)
	sources := make([]string, 0, len(found))

	for i := range found {
		sources = append(sources, found[i].Source.ArchitectureID)
	}

	slices.Sort(sources)

	return sources
}

// multiprojectScopedEntry returns the entry of the named project `web` among the two decisions of out.
func multiprojectScopedEntry(out *conformance.DecisionEvaluation) conformance.DecisionEntry {
	if out.Summary.Decisions[0].Project == "web" {
		return out.Summary.Decisions[0]
	}

	return out.Summary.Decisions[1]
}

// An unscoped entry and an entry scoped to the named primary project with the same ID are two register entries:
// each keeps its own subjects, and their identities (summary project, finding source) stay distinct.
func TestMultiProjectPrimaryScopedAndUnscopedEntriesStayDistinct(t *testing.T) {
	t.Parallel()

	out := namedPrimary(t, `  - id: ADR-0001
    project: web
    governs: [{ref: 'calm:node:billing'}]
  - id: ADR-0001
    governs: [{ref: 'calm:node:billing'}]
`).evaluate()

	projects := make([]string, 0, len(out.Summary.Decisions))

	for _, entry := range out.Summary.Decisions {
		projects = append(projects, entry.Project)

		if len(entry.Governs) != 1 {
			t.Errorf("entry %q lost its subjects: %+v", entry.Project, entry)
		}
	}

	slices.Sort(projects)

	if !slices.Equal(projects, []string{"", "web"}) {
		t.Fatalf("decisions = %+v", out.Summary.Decisions)
	}

	scoped := multiprojectScopedEntry(out)
	if scoped.Path != primaryPath || scoped.Presence.State != string(adr.PresenceCommitted) {
		t.Errorf("scoped entry = %+v", scoped)
	}

	// Each entry has its own applicability finding, with its own fingerprint source.
	if sources := multiprojectApplicabilitySources(out); !slices.Equal(sources, []string{"ADR-1", "ADR-1@web"}) {
		t.Errorf("applicability finding sources = %v", sources)
	}
}

// The file is tracked in the primary revision under the same relative path, but not in the revision of billing:
// presence is judged in the revision of the project that owns the file.
func TestMultiProjectPresenceUsesTheRevisionOfTheOwner(t *testing.T) {
	t.Parallel()

	out := twoProjects(t, pinnedToBilling, nil, billingRev).evaluate()

	entry := out.Summary.Decisions[0]
	if got := (multiprojectEntryView{Presence: entry.Presence.State, Applicability: entry.Applicability.State}); got != (multiprojectEntryView{Presence: "index-only", Applicability: "unknown"}) {
		t.Errorf("entry = %+v", entry)
	}

	// A project with no revision gives unknown, whatever the primary revision is.
	noRev := twoProjects(t, pinnedToBilling, []string{billingPath}, "").evaluate()

	state := noRev.Summary.Decisions[0].Presence
	if got := (multiprojectEntryView{Presence: state.State, Reason: state.Reason}); got != (multiprojectEntryView{Presence: "unverifiable", Reason: adr.EvidenceNoRevision}) {
		t.Errorf("presence = %+v", state)
	}
}

// Dirty files are read per project and prefixed.
func TestMultiProjectDirtyFileOfTheOwner(t *testing.T) {
	t.Parallel()

	w := twoProjects(t, pinnedToBilling, []string{billingPath}, billingRev)
	git, ok := w.projects[1].Git.(*recordingGit)

	if !ok {
		t.Fatal("not a recording Git")
	}

	git.Dirty = []string{billingPath}

	out := w.evaluate()
	if state := out.Summary.Decisions[0].Presence; state.State != "uncommitted-edit" {
		t.Errorf("presence = %+v", state)
	}
}
