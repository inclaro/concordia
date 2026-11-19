package cli

import (
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
)

func watchRun(code int, verdict conformance.RuleVerdict, fingerprints ...string) *conformance.Run {
	run := &conformance.Run{
		Rules:   []conformance.RuleResult{{RuleID: "R1", Verdict: verdict}},
		Summary: conformance.RunSummary{ExitCode: code},
	}
	for _, fp := range fingerprints {
		run.Findings = append(run.Findings, conformance.Finding{
			Fingerprint: fp, RuleID: "R1", Message: "m", Disposition: exceptions.DispositionActive,
			Location: &conformance.Location{Path: "a.go", Line: 3},
		})
	}

	return run
}

func types(list []transition) []string {
	out := make([]string, len(list))
	for i := range list {
		out[i] = list[i].Type
	}

	return out
}

func TestTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prev, next *conformance.Run
		want       []string
	}{
		{"first cycle", nil, watchRun(1, "violation", "f1"), []string{"new", "summary"}},
		{"verdict change", watchRun(1, "violation", "f1"), watchRun(0, "pass"), []string{"verdict", "resolved", "summary"}},
		{"new finding", watchRun(1, "violation", "f1"), watchRun(1, "violation", "f1", "f2"), []string{"new"}},
		{"unchanged", watchRun(1, "violation", "f1"), watchRun(1, "violation", "f1"), nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := types(transitions(tc.prev, tc.next))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestTickDecisionStableChangeStartsCycle(t *testing.T) {
	t.Parallel()

	gen := func() string { return "g" }
	s := &tickState{lastKey: "a", lastGeneration: "g"}

	steps := []struct {
		key  string
		want tickAction
	}{
		{"a", tickNone},
		{"b", tickNone},          // changed: pending
		{"b", tickIndexAndCheck}, // stable: cycle
		{"b", tickNone},          // lastKey updated: nothing more
		{"c", tickNone},          // changed again
		{"d", tickNone},          // still changing: new pending
		{"d", tickIndexAndCheck},
	}

	for i, st := range steps {
		if got := s.decide(st.key, gen); got != st.want {
			t.Fatalf("step %d key %q: got %d want %d", i, st.key, got, st.want)
		}
	}
}

func TestTickDecisionRevertedChangeAndGeneration(t *testing.T) {
	t.Parallel()

	generation := "g1"
	gen := func() string { return generation }
	s := &tickState{lastKey: "a", lastGeneration: "g1"}

	if got := s.decide("b", gen); got != tickNone {
		t.Fatalf("pending: got %d", got)
	}

	if got := s.decide("a", gen); got != tickNone || s.pending {
		t.Fatalf("revert: got %d pending %v", got, s.pending)
	}

	generation = "g2"

	if got := s.decide("a", gen); got != tickCheckOnly {
		t.Fatalf("generation change: got %d", got)
	}
}
