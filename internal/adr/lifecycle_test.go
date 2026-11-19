package adr_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/diag"
)

// Scenario: Non-active statuses.
// Scenario: Unrecognized status.
func TestLifecycleDefaultProfile(t *testing.T) {
	t.Parallel()

	lc, err := adr.NewLifecycle(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := lc.Active(); len(got) != 1 || got[0] != adr.StatusAccepted {
		t.Errorf("Active = %v, want [accepted]", got)
	}

	cases := map[string]adr.LifecycleClass{
		"accepted": adr.ClassActive, "implemented": adr.ClassInactive,
		"proposed": adr.ClassPending, "draft": adr.ClassPending,
		"rejected": adr.ClassInactive, "deprecated": adr.ClassInactive, "superseded": adr.ClassInactive,
		"abandoned": adr.ClassInactive, "unknown": adr.ClassInactive, "": adr.ClassInactive,
	}

	for status, want := range cases {
		if mapped, class := lc.Classify(status, "whatever"); class != want || mapped != "" {
			t.Errorf("Classify(%q) = %q, %q; want class %q, no mapping", status, mapped, class, want)
		}
	}
}

func TestLifecycleImplementedMayBeActive(t *testing.T) {
	t.Parallel()

	lc, err := adr.NewLifecycle([]string{"implemented", "accepted"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(lc.Active(), ","); got != "accepted,implemented" {
		t.Errorf("Active = %s", got)
	}

	if _, class := lc.Classify(adr.StatusImplemented, ""); class != adr.ClassActive {
		t.Errorf("implemented class = %q", class)
	}
}

// Scenario: Profile tries to activate a forbidden status.
func TestLifecycleRejectsForbiddenActiveStatuses(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"proposed", "rejected", "deprecated", "superseded", "draft", "abandoned", "unknown", "Accepted", "bogus"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			_, err := adr.NewLifecycle([]string{"accepted", status}, nil)
			de := wantDiag(t, err, diag.ADR005)

			if !strings.Contains(de.Diagnostic.Message, status) {
				t.Errorf("message %q does not name %q", de.Diagnostic.Message, status)
			}
		})
	}
}

// Scenario: Status text mapping.
func TestLifecycleStatusMap(t *testing.T) {
	t.Parallel()

	lc, err := adr.NewLifecycle(nil, map[string]string{"Ratified": "accepted", "Retired": "deprecated", "Wip": "draft"})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		status, text, mapped string
		class                adr.LifecycleClass
	}{
		{"unknown", "Ratified", "accepted", adr.ClassActive},
		{"unknown", "ratified", "accepted", adr.ClassActive},
		{"unknown", " RATIFIED ", "accepted", adr.ClassActive},
		{"accepted", "Retired", "deprecated", adr.ClassInactive},
		{"unknown", "Wip", "draft", adr.ClassPending},
		{"unknown", "Other", "", adr.ClassInactive},
		{"accepted", "Other", "", adr.ClassActive},
	}

	for _, tc := range cases {
		if mapped, class := lc.Classify(tc.status, tc.text); mapped != tc.mapped || class != tc.class {
			t.Errorf("Classify(%q, %q) = %q, %q; want %q, %q", tc.status, tc.text, mapped, class, tc.mapped, tc.class)
		}
	}
}

func TestLifecycleKeepsTheOriginalStatus(t *testing.T) {
	t.Parallel()

	lc, _ := adr.NewLifecycle(nil, map[string]string{"Ratified": "accepted"})
	ev := adr.EvidenceDecision{Status: "unknown", StatusText: "Ratified"}

	mapped, _ := lc.Classify(ev.Status, ev.StatusText)
	if mapped != "accepted" || ev.Status != "unknown" || ev.StatusText != "Ratified" {
		t.Errorf("mapped %q; original %q/%q must stay", mapped, ev.Status, ev.StatusText)
	}
}

func TestLifecycleRejectsABadStatusMap(t *testing.T) {
	t.Parallel()

	_, err := adr.NewLifecycle(nil, map[string]string{"Ratified": "approved"})
	_ = wantDiag(t, err, diag.ADR005)

	_, err = adr.NewLifecycle(nil, map[string]string{"Ratified": "accepted", "ratified": "draft"})
	_ = wantDiag(t, err, diag.ADR005)
}
