package exceptions_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/exceptions"
)

func TestDispositions(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")

	t.Run("active waiver, expired waiver, baseline, active", func(t *testing.T) {
		t.Parallel()

		r := exceptions.ApplyDispositions(
			[]exceptions.Key{finding(1), finding(2), finding(3), finding(4)},
			baselineOf(3),
			[]exceptions.Waiver{waiver(1, "2026-12-31", idOne), waiver(2, "2026-05-31", "WV-2")},
			now, nil,
		)

		want := []exceptions.Assignment{
			{Disposition: exceptions.DispositionWaived, Ref: idOne},
			{Disposition: exceptions.DispositionExpiredWaiver, Ref: "WV-2"},
			{Disposition: exceptions.DispositionBaselined, Ref: "baseline"},
			{Disposition: exceptions.DispositionActive},
		}
		wantAssignments(t, r.Assignments, want)
		wantDiagnosticCodes(t, &r, codeExpired)
	})

	t.Run("waiver wins over baseline", func(t *testing.T) {
		t.Parallel()

		r := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, baselineOf(1), []exceptions.Waiver{waiver(1, "2026-12-31", idOne)}, now, nil)
		wantFirstDisposition(t, &r, exceptions.DispositionWaived)
	})

	t.Run("an active waiver wins over an expired one", func(t *testing.T) {
		t.Parallel()

		r := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, nil, []exceptions.Waiver{waiver(1, "2026-01-01", "WV-1"), waiver(1, "2026-12-31", "WV-2")}, now, nil)

		want := []exceptions.Assignment{{Disposition: exceptions.DispositionWaived, Ref: "WV-2"}}
		wantAssignments(t, r.Assignments, want)
		wantNoWarnings(t, &r)
	})

	t.Run("of two expired waivers the later expiry is the reference", func(t *testing.T) {
		t.Parallel()

		r := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, nil, []exceptions.Waiver{waiver(1, "2026-03-01", "WV-2"), waiver(1, "2026-01-01", "WV-1")}, now, nil)

		want := []exceptions.Assignment{{Disposition: exceptions.DispositionExpiredWaiver, Ref: "WV-2"}}
		wantAssignments(t, r.Assignments, want)
		wantDiagnosticCodes(t, &r, codeExpired)
	})

	t.Run("a waiver for another rule does not match", func(t *testing.T) {
		t.Parallel()

		other := waiver(1, "2026-12-31", idOne)
		other.Rule = ruleTwo

		r := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, nil, []exceptions.Waiver{other}, now, nil)
		wantFirstDisposition(t, &r, exceptions.DispositionActive)
		wantOrphaned(t, &r, idOne)
	})
}

func TestDispositionsReportResolvedBaselineEntriesAndOrphans(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")

	t.Run("resolved baseline entries and orphaned waivers", func(t *testing.T) {
		t.Parallel()

		r := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, baselineOf(1, 9), []exceptions.Waiver{waiver(7, "2026-12-31", idOne)}, now, []string{ruleOne})
		wantResolved(t, &r, fp(9))
		wantOrphaned(t, &r, idOne)

		if !slices.Contains(codes(r.Diagnostics), "CO-EXC-003") {
			t.Errorf("diagnostics = %v", codes(r.Diagnostics))
		}
	})

	t.Run("rulesRun limits resolved baseline entries", func(t *testing.T) {
		t.Parallel()

		none := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, baselineOf(1, 9), nil, now, []string{})
		wantResolved(t, &none)

		other := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, baselineOf(1, 9), nil, now, []string{ruleTwo})
		wantResolved(t, &other)
	})
}

func TestWaiverExpiryBoundary(t *testing.T) {
	t.Parallel()

	w := waiver(1, "2026-12-31", idOne)
	if !exceptions.IsWaiverActive(&w, at(t, "2026-12-31T23:59:00Z")) {
		t.Error("waiver must be active on the last day")
	}

	if exceptions.IsWaiverActive(&w, at(t, "2027-01-01T00:00:00Z")) {
		t.Error("waiver must be expired at the start of the next day")
	}
}

func wantDiagnosticCodes(t *testing.T, r *exceptions.Result, want ...string) {
	t.Helper()

	if got := codes(r.Diagnostics); !slices.Equal(got, want) {
		t.Errorf("diagnostics = %v", got)
	}
}

func wantFirstDisposition(t *testing.T, r *exceptions.Result, want exceptions.Disposition) {
	t.Helper()

	if r.Assignments[0].Disposition != want {
		t.Errorf("disposition = %v", r.Assignments[0])
	}
}

func wantNoWarnings(t *testing.T, r *exceptions.Result) {
	t.Helper()

	if len(r.Diagnostics) != 0 || len(r.OrphanedWaivers) != 0 {
		t.Errorf("diagnostics = %v, orphaned = %v", codes(r.Diagnostics), r.OrphanedWaivers)
	}
}

func wantOrphaned(t *testing.T, r *exceptions.Result, want ...string) {
	t.Helper()

	if !slices.Equal(r.OrphanedWaivers, want) {
		t.Errorf("orphaned = %v", r.OrphanedWaivers)
	}
}

func wantResolved(t *testing.T, r *exceptions.Result, want ...string) {
	t.Helper()

	if !slices.Equal(r.ResolvedBaselineEntries, want) {
		t.Errorf("resolved = %v", r.ResolvedBaselineEntries)
	}
}
