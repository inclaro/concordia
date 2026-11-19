package adr_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/adr"
)

func TestEvaluateApplicabilityTable(t *testing.T) {
	const (
		old = "docs/adr/0003.md"
		cur = "docs/adr/0007.md"
		pro = "docs/adr/0010.md"
		rej = "docs/adr/0011.md"
		lag = "docs/adr/0012.md"
	)

	ev := supportedEvidence(
		decisionAt(old, "ADR-3", "accepted"),
		decisionAt(cur, "ADR-7", "accepted"),
		decisionAt(pro, "ADR-10", "proposed"),
		decisionAt(rej, "ADR-11", "rejected"),
		decisionAt(lag, "ADR-12", "accepted"),
	)
	ev.Supersedes = []adr.SupersedesEdge{{From: cur, To: old}, {From: cur, To: lag}}
	h := adr.BuildHistory(ev, nil, nil, []string{old, cur, pro, rej, lag})

	committed := func(p string) adr.PresenceResult {
		return adr.PresenceResult{State: adr.PresenceCommitted, Path: p}
	}

	tests := []struct {
		name     string
		state    adr.ApplicabilityState
		reason   string
		presence adr.PresenceResult
		lags     bool
	}{
		{"active head", adr.ApplicabilityActive, "", committed(cur), false},
		{"superseded with lagging text", adr.ApplicabilitySuperseded, "superseded", committed(old), true},
		{"proposed is inactive", adr.ApplicabilityInactive, "inactive", committed(pro), false},
		{"rejected is inactive", adr.ApplicabilityInactive, "inactive", committed(rej), false},
		{"index-only text is unknown", adr.ApplicabilityUnknown, adr.EvidenceNotCommitted, adr.PresenceResult{State: adr.PresenceIndexOnly, Reason: "index-only", Path: cur}, false},
		{"uncommitted edit is unknown", adr.ApplicabilityUnknown, adr.EvidenceNotCommitted, adr.PresenceResult{State: adr.PresenceUncommittedEdit, Reason: "uncommitted-edit", Path: cur}, false},
		{"absent is unknown", adr.ApplicabilityUnknown, adr.EvidenceNotCommitted, adr.PresenceResult{State: adr.PresenceAbsent, Reason: "absent"}, false},
		{"path mismatch is unknown", adr.ApplicabilityUnknown, adr.EvidenceNotCommitted, adr.PresenceResult{State: adr.PresencePathMismatch, Reason: "path-mismatch"}, false},
		{"not recognized is unknown", adr.ApplicabilityUnknown, adr.EvidenceNotCommitted, adr.PresenceResult{State: adr.PresenceNotRecognized, Reason: "not-recognized"}, false},
		{"unverifiable carries its reason", adr.ApplicabilityUnknown, adr.EvidenceNoRevision, adr.PresenceResult{State: adr.PresenceUnverifiable, Reason: adr.EvidenceNoRevision}, false},
		{"duplicate carries its reason", adr.ApplicabilityUnknown, "duplicate-id", adr.PresenceResult{State: adr.PresenceUnverifiable, Reason: "duplicate-id"}, false},
		{"committed path outside the history", adr.ApplicabilityUnknown, adr.EvidenceNotCommitted, committed("docs/adr/none.md"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, reason, lags := adr.EvaluateApplicability(&adr.Entry{}, tt.presence, h, nil)
			if state != tt.state || reason != tt.reason || lags != tt.lags {
				t.Fatalf("got (%s, %q, %v), want (%s, %q, %v)", state, reason, lags, tt.state, tt.reason, tt.lags)
			}
		})
	}
}

func TestEvaluateApplicabilityStatusMapAndAmbiguity(t *testing.T) {
	lc, err := adr.NewLifecycle(nil, map[string]string{"Approved": "accepted"})
	if err != nil {
		t.Fatal(err)
	}

	mapped := decisionAt("a.md", "ADR-1", "unknown")
	mapped.StatusText = "Approved"

	ev := supportedEvidence(mapped, decisionAt("b.md", "ADR-2", "accepted"), decisionAt("c.md", "ADR-3", "accepted"))
	ev.Supersedes = []adr.SupersedesEdge{{From: "b.md", To: "c.md"}, {From: "a.md", To: "c.md"}}
	h := adr.BuildHistory(ev, lc, nil, []string{"a.md", "b.md", "c.md"})

	for _, p := range []string{"a.md", "b.md", "c.md"} {
		state, reason, _ := adr.EvaluateApplicability(&adr.Entry{}, adr.PresenceResult{State: adr.PresenceCommitted, Path: p}, h, lc)
		if state != adr.ApplicabilityAmbiguous || reason != "competing-successors" {
			t.Fatalf("%s = (%s, %q), want ambiguous competing-successors", p, state, reason)
		}
	}

	solo := adr.BuildHistory(supportedEvidence(mapped), lc, nil, []string{"a.md"})
	if state, _, _ := solo.Applicability("a.md"); state != adr.ApplicabilityActive {
		t.Fatalf("statusMap-ed decision = %s, want active", state)
	}
}

func TestApplicabilityIsUnknownWithoutSupport(t *testing.T) {
	h := adr.BuildHistory(&adr.DecisionEvidence{}, nil, nil, []string{"a.md"})

	if state, reason, _ := h.Applicability("a.md"); state != adr.ApplicabilityUnknown || reason != adr.EvidenceNoCapability {
		t.Fatalf("unsupported = (%s, %q)", state, reason)
	}

	var none *adr.History
	if state, _, _ := none.Applicability("a.md"); state != adr.ApplicabilityUnknown || none.Ambiguous("a.md") {
		t.Fatal("a nil history must be unknown and not ambiguous")
	}
}
