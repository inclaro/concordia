package integration_test

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/conformance"
)

// OUT-06 and the schema of `results.json` (schemaVersion 1.1), on the two CBM recordings: `check` runs twice
// with another run id and another clock, and the result digest and the canonical JSON without the volatile
// fields stay the same.

// unmappedCount matches the count of the CO-OBS-016 message: `native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)`.
var unmappedCount = regexp.MustCompile(`\((\d+) edges\)$`)

// infraDeterminismRun runs `check` on a copy of the infra fixture with the recording of CBM 0.12.1 and the policy of the deploys test.
func infraDeterminismRun(t *testing.T, id string, now time.Time) *conformance.Run {
	t.Helper()

	return checkDeploysWorkspace(t, deploysWorkspace, func(opts *conformance.CheckOptions) {
		opts.Env.RunID = id
		opts.Deps.Now = func() time.Time { return now }
	})
}

func commerceDeterminismRun(t *testing.T, id string, now time.Time) *conformance.Run {
	t.Helper()

	return newCommerce(t).mustCheck(t, func(opts *conformance.CheckOptions) {
		opts.Env.RunID = id
		opts.Deps.Now = func() time.Time { return now }
	})
}

func TestResultsOfTheRecordingsAreDeterministicAndValid(t *testing.T) { // OUT-06, INT-03
	first := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	second := time.Date(2027, 3, 4, 5, 6, 7, 0, time.UTC)

	for name, run := range map[string]func(*testing.T, string, time.Time) *conformance.Run{"commerce": commerceDeterminismRun, "infra": infraDeterminismRun} {
		t.Run(name, func(t *testing.T) {
			a := run(t, "20260601T120000Z-aaaaaaaa", first)
			b := run(t, "20270304T050607Z-bbbbbbbb", second)

			if a.Run.ID == b.Run.ID || a.Run.StartedAt == b.Run.StartedAt {
				t.Fatalf("the volatile fields must differ: %+v %+v", a.Run, b.Run)
			}

			if a.Summary.ResultDigest == "" || a.Summary.ResultDigest != b.Summary.ResultDigest {
				t.Errorf("resultDigest %q and %q", a.Summary.ResultDigest, b.Summary.ResultDigest)
			}

			if stable(t, a) != stable(t, b) {
				t.Error("the canonical JSON differs after the volatile fields are removed")
			}

			digest, err := conformance.ResultDigest(a)
			if err != nil || digest != a.Summary.ResultDigest {
				t.Errorf("the digest of the run is %q (%v), results.json says %q", digest, err, a.Summary.ResultDigest)
			}

			text, err := conformance.CanonicalJSON(a)
			if err != nil {
				t.Fatal(err)
			}

			if errs := conformance.ValidateResultJSON(text); len(errs) > 0 {
				t.Errorf("results.json does not validate against the 1.1 schema: %v", errs)
			}

			assertCounters(t, a)
		})
	}
}

// assertCounters checks the observability counters against the facts that the run itself shows (A01).
func assertCounters(t *testing.T, run *conformance.Run) {
	t.Helper()

	obs := run.Observability

	unmapped := 0

	for i := range run.Diagnostics {
		if run.Diagnostics[i].Code != "CO-OBS-016" {
			continue
		}

		if m := unmappedCount.FindStringSubmatch(run.Diagnostics[i].Message); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatal(err)
			}

			unmapped += n
		}
	}

	if obs.UnsupportedFacts != unmapped || unmapped == 0 {
		t.Errorf("unsupportedFacts = %d, the CO-OBS-016 diagnostics count %d unmapped edges", obs.UnsupportedFacts, unmapped)
	}

	if obs.UnresolvedFacts < run.Coverage.Skipped.Unbound+run.Coverage.Skipped.EndpointMissing {
		t.Errorf("unresolvedFacts = %d, below the skipped facts %+v", obs.UnresolvedFacts, run.Coverage.Skipped)
	}

	if obs.NormalizedFacts == 0 || obs.NormalizedFacts > obs.IngestedFacts || obs.IngestedFacts < obs.UnsupportedFacts || obs.StageFailures != 0 {
		t.Errorf("counters = %+v", obs)
	}

	for _, area := range conformance.AreaOrder()[1:] {
		if v := run.Areas.Get(area).Verdict; v != conformance.AreaNotConfigured {
			t.Errorf("area %s = %s, want not-configured", area, v)
		}
	}
}
