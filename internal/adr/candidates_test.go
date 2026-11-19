package adr_test

import (
	"reflect"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
)

// Scenario: ADR mentions a service.
// Scenario: CALM adrs array.
// Scenario: ADR in the index but not in the register.
func TestCollectCandidates(t *testing.T) {
	reg := &adr.Register{Entries: []adr.Entry{{ID: "ADR-3", Path: "docs/adr/0003.md"}}}
	ev := supportedEvidence(
		decisionAt("docs/adr/0003.md", "ADR-3", "accepted"),
		decisionAt("docs/adr/0042-unlisted.md", "ADR-42", "accepted"),
	)
	ev.Mentions = []adr.Mention{
		{From: "docs/adr/0003.md", Subject: "services/orders/main.go", Kind: "references-file"},
		{From: "docs/adr/0003.md", Subject: "orders.Service", Kind: "mentions"},
	}
	model := &intent.ArchitectureModel{ADRs: []string{
		"docs/adr/0003.md",
		"https://example.test/repo/blob/main/docs/adr/0003.md#context",
		"docs/adr/0042-unlisted.md",
		"https://wiki.example.test/adr-9",
		"ADR-0003",
	}}
	matched := map[string]bool{"docs/adr/0003.md": true}

	got := adr.CollectCandidates(ev, reg, model, matched)

	want := []conformance.DecisionCandidate{
		{Kind: "calm-adrs-entry", Path: "https://wiki.example.test/adr-9"},
		{Kind: "calm-adrs-entry", ID: "ADR-42", Path: "docs/adr/0042-unlisted.md"},
		{Kind: "mention", ID: "ADR-3", Path: "docs/adr/0003.md", Subject: "orders.Service"},
		{Kind: "mention", ID: "ADR-3", Path: "docs/adr/0003.md", Subject: "services/orders/main.go"},
		{Kind: "unregistered-decision", ID: "ADR-42", Path: "docs/adr/0042-unlisted.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %+v\nwant %+v", got, want)
	}
}

func TestCollectCandidatesIsEmptyNotNil(t *testing.T) {
	got := adr.CollectCandidates(nil, nil, nil, nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("candidates = %#v, want empty non-nil", got)
	}
}

func TestCollectCandidatesDoNotChangeVerdicts(t *testing.T) {
	const p = "docs/adr/0003.md"

	reg := &adr.Register{Entries: []adr.Entry{testEntry("ADR-3", "", "")}}
	plain := supportedEvidence(decisionAt(p, "ADR-3", "accepted"))
	noisy := supportedEvidence(decisionAt(p, "ADR-3", "accepted"), decisionAt("docs/adr/0100.md", "ADR-100", "accepted"))
	noisy.Mentions = []adr.Mention{{From: p, Subject: "services/orders", Kind: "mentions"}}
	model := &intent.ArchitectureModel{ADRs: []string{"docs/adr/0100.md", "docs/adr/0200.md"}}
	git := factsFrom(t, nil, "", "", fakeGit(testRev, []string{p, "docs/adr/0100.md"}))

	verdict := func(ev *adr.DecisionEvidence, withCandidates bool) []any {
		if withCandidates {
			if len(adr.CollectCandidates(ev, reg, model, map[string]bool{p: true})) == 0 {
				t.Fatal("expected candidates")
			}
		}

		pres := adr.EvaluatePresence(reg.Entries, ev, git)[adr.EntryKey(&reg.Entries[0])]
		h := adr.BuildHistory(ev, nil, func(path string) bool { return git.Tracked[path] }, []string{p})
		state, reason, lags := adr.EvaluateApplicability(&reg.Entries[0], pres, h, nil)

		return []any{pres.State, pres.Reason, pres.Path, state, reason, lags, h.Anomalies, h.Links}
	}

	without := verdict(plain, false)
	with := verdict(noisy, true)

	if !reflect.DeepEqual(without, with) {
		t.Fatalf("verdict changed by candidates:\nwithout %+v\nwith    %+v", without, with)
	}
}
