package workspace_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/workspace"
)

func TestLoadEnforcementAndRollback(t *testing.T) {
	t.Parallel()

	y := envHead + `check:
  enforcement:
    - {family: deployment-security, environment: staging, mode: mandatory}
    - {family: deployment-security, environment: production, mode: advisory}
    - {pack: infra, mode: shadow}
    - {rules: [CO-TYP-001, CO-TYP-002], mode: advisory}
  rollback:
    reason: false positives
    authorizedBy: jane
    expires: "2026-11-15"
`

	w, err := workspace.Load(writeWorkspace(t, y), "")
	if err != nil {
		t.Fatal(err)
	}

	want := []workspace.EnforcementEntry{
		{Family: "deployment-security", Environment: "staging", Mode: workspace.ModeMandatory},
		{Family: "deployment-security", Environment: "production", Mode: workspace.ModeAdvisory},
		{Pack: "infra", Mode: workspace.ModeShadow},
		{Rules: []string{"CO-TYP-001", "CO-TYP-002"}, Mode: workspace.ModeAdvisory},
	}

	if !reflect.DeepEqual(w.Check.Enforcement, want) {
		t.Errorf("enforcement = %+v, want %+v", w.Check.Enforcement, want)
	}

	if w.Check.Rollback == nil || *w.Check.Rollback != (workspace.Rollback{Reason: "false positives", AuthorizedBy: "jane", Expires: "2026-11-15"}) {
		t.Errorf("rollback = %+v", w.Check.Rollback)
	}
}

func TestLoadWithoutEnforcementHasNone(t *testing.T) {
	t.Parallel()

	w, err := workspace.Load(writeWorkspace(t, envHead), "")
	if err != nil {
		t.Fatal(err)
	}

	if len(w.Check.Enforcement) != 0 || w.Check.Rollback != nil {
		t.Errorf("check = %+v", w.Check)
	}
}

func TestLoadEnforcementInAProfileReplacesTheList(t *testing.T) {
	t.Parallel()

	y := envHead + `check:
  enforcement:
    - {family: a, mode: shadow}
profiles:
  ci:
    check:
      enforcement:
        - {family: a, mode: mandatory}
`

	w, err := workspace.Load(writeWorkspace(t, y), "ci")
	if err != nil {
		t.Fatal(err)
	}

	if len(w.Check.Enforcement) != 1 || w.Check.Enforcement[0].Mode != workspace.ModeMandatory {
		t.Errorf("enforcement = %+v", w.Check.Enforcement)
	}
}

func TestLoadEnforcementInvalidGivesWRK002(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unknown mode":       "check:\n  enforcement:\n    - {family: a, mode: strict}\n",
		"no mode":            "check:\n  enforcement:\n    - {family: a}\n",
		"no selector":        "check:\n  enforcement:\n    - {environment: prod, mode: shadow}\n",
		"bad family":         "check:\n  enforcement:\n    - {family: Bad_Family, mode: shadow}\n",
		"empty rules":        "check:\n  enforcement:\n    - {rules: [], mode: shadow}\n",
		"bad environment":    "check:\n  enforcement:\n    - {family: a, environment: Prod, mode: shadow}\n",
		"unknown entry key":  "check:\n  enforcement:\n    - {family: a, mode: shadow, bogus: 1}\n",
		"rollback no expiry": "check:\n  rollback: {reason: r, authorizedBy: a}\n",
		"rollback no reason": "check:\n  rollback: {authorizedBy: a, expires: \"2026-11-15\"}\n",
		"rollback no author": "check:\n  rollback: {reason: r, expires: \"2026-11-15\"}\n",
		"rollback bad shape": "check:\n  rollback: {reason: r, authorizedBy: a, expires: next week}\n",
		"rollback no date":   "check:\n  rollback: {reason: r, authorizedBy: a, expires: \"2026-13-45\"}\n",
		"rollback unknown":   "check:\n  rollback: {reason: r, authorizedBy: a, expires: \"2026-11-15\", bogus: 1}\n",
	}

	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.Load(writeWorkspace(t, envHead+extra), "")
			if de := failure(t, err); de.Diagnostic.Code != codeWRK002 || de.ExitCode != 2 {
				t.Errorf("%+v", de.Diagnostic)
			}
		})
	}
}

func TestRollbackIsActiveThroughTheEndOfTheExpiryDay(t *testing.T) {
	t.Parallel()

	r := workspace.Rollback{Reason: "r", AuthorizedBy: "a", Expires: "2026-11-15"}

	for at, want := range map[time.Time]bool{
		time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC):                      true,
		time.Date(2026, 11, 15, 23, 59, 59, 0, time.UTC):                   true,
		time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC):                      false,
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC):                        false,
		time.Date(2026, 11, 15, 23, 0, 0, 0, time.FixedZone("x", -5*3600)): false,
	} {
		if got := r.Active(at); got != want {
			t.Errorf("Active(%s) = %v, want %v", at, got, want)
		}
	}
}
