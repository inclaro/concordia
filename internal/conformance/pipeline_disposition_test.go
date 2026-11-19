package conformance_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/testutil"
)

func bndFingerprint(t *testing.T) string {
	t.Helper()

	run := newSetup(t, nil, nil).mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-BND-001"} })

	return run.Findings[0].Fingerprint
}

const baselinePath = "exceptions/concordia.baseline.json"

func writeBaseline(t *testing.T, dir string, revision *string, entries ...exceptions.BaselineEntry) {
	t.Helper()

	err := exceptions.WriteBaseline(filepath.Join(dir, filepath.FromSlash(baselinePath)), &exceptions.Baseline{
		SchemaVersion: "1.0",
		CreatedFrom:   exceptions.CreatedFrom{SourceRevision: revision, GraphGeneration: "cbm:demo:0000000000000000", PolicyDigest: "sha256:" + strings.Repeat("0", 64)},
		Findings:      entries,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDispositionBrownfieldBaselinedStaysVisible(t *testing.T) {
	t.Parallel()

	fp := bndFingerprint(t)
	s := newSetup(t, nil, &setupOpts{graph: buildGraph(graphOpts{refund: true})})
	rev := "abc123"
	writeBaseline(t, s.dir, &rev, exceptions.BaselineEntry{
		Fingerprint: fp, RuleID: "CO-BND-001", Owner: "team-billing", Reason: "Inherited before enforcement", CreatedAt: "2026-01-01T00:00:00Z",
	})

	before := sha(testutil.ReadFile(t, filepath.Join(s.dir, baselinePath)))
	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-BND-001"} })

	got := map[string]exceptions.Disposition{}
	for _, f := range run.Findings {
		got[f.Location.Path] = f.Disposition
	}

	if len(run.Findings) != 2 {
		t.Fatalf("dispositions %v", got)
	}

	if got[billingFile] != exceptions.DispositionBaselined || got[refundFile] != exceptions.DispositionActive {
		t.Fatalf("dispositions %v", got)
	}

	s2 := run.Summary
	expectAll(t, "summary %+v", s2, s2.Active == 1, s2.Baselined == 1, s2.ExitCode == 1)

	if run.Inputs.Baseline == nil || run.Inputs.Baseline.Path != baselinePath {
		t.Errorf("baseline input %+v", run.Inputs.Baseline)
	}

	if sha(testutil.ReadFile(t, filepath.Join(s.dir, baselinePath))) != before {
		t.Error("the baseline bytes changed")
	}
}

func TestDispositionFullyBaselinedExitsZeroAndResolvedEntryIsListed(t *testing.T) {
	t.Parallel()

	fp := bndFingerprint(t)
	entry := exceptions.BaselineEntry{Fingerprint: fp, RuleID: "CO-BND-001", Owner: "o", Reason: "r", CreatedAt: "2026-01-01T00:00:00Z"}

	s := newSetup(t, nil, nil)
	writeBaseline(t, s.dir, nil, entry)

	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-BND-001"} })
	sum := run.Summary

	if sum.Active != 0 || sum.Baselined != 1 || sum.ExitCode != 0 {
		t.Errorf("summary %+v resolved %v", run.Summary, run.Exceptions.ResolvedBaselineEntries)
	}

	if len(run.Exceptions.ResolvedBaselineEntries) != 0 {
		t.Errorf("summary %+v resolved %v", run.Summary, run.Exceptions.ResolvedBaselineEntries)
	}

	fixed := newSetup(t, nil, &setupOpts{graph: buildGraph(graphOpts{noEdges: true})})
	writeBaseline(t, fixed.dir, nil, entry)

	run2 := fixed.mustCheck(t, func(o *conformance.CheckOptions) { o.Rules = []string{"CO-BND-001"} })
	if !slices.Equal(run2.Exceptions.ResolvedBaselineEntries, []string{fp}) || run2.Summary.ExitCode != 0 {
		t.Errorf("resolved %v exit %d", run2.Exceptions.ResolvedBaselineEntries, run2.Summary.ExitCode)
	}
}

func TestDispositionBaselineEntryOfUnknownRuleIsNotResolved(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	writeBaseline(t, s.dir, nil, exceptions.BaselineEntry{
		Fingerprint: "sha256:" + strings.Repeat("9", 64), RuleID: "CO-COV-001", Owner: "o", Reason: "r", CreatedAt: "2026-01-01T00:00:00Z",
	})

	if run := s.mustCheck(t, nil); len(run.Exceptions.ResolvedBaselineEntries) != 0 {
		t.Fatalf("resolved %v", run.Exceptions.ResolvedBaselineEntries)
	}
}

func TestDispositionWaiverBoundary(t *testing.T) {
	fp := bndFingerprint(t)
	waivers := `
apiVersion: concordia.io/v1alpha1
kind: ExceptionRegister
waivers:
  - {id: WV-1, finding: "` + fp + `", rule: CO-BND-001, owner: team-billing, reason: "tracked in ARCH-1", expires: 2026-12-31}
`
	s := newSetup(t, map[string]string{"exceptions/waivers.yaml": waivers}, nil)
	rules := func(o *conformance.CheckOptions) { o.Rules = []string{"CO-BND-001"} }

	t.Setenv("CONCORDIA_NOW", "2026-12-31T23:59:00Z")
	assertWaiverActive(t, s.mustCheck(t, rules), fp)

	t.Setenv("CONCORDIA_NOW", "2027-01-01T00:00:00Z")
	assertWaiverExpired(t, s.mustCheck(t, rules))
}

// dispositionRefOf gives the waiver or baseline entry that the finding names, "" when it names none.
func dispositionRefOf(f *conformance.Finding) string {
	if f.DispositionRef == nil {
		return ""
	}

	return *f.DispositionRef
}

// assertWaiverActive checks that the waiver applies on its last day: the finding is waived and the run passes.
func assertWaiverActive(t *testing.T, active *conformance.Run, fingerprint string) {
	t.Helper()

	f := &active.Findings[0]
	if f.Disposition != exceptions.DispositionWaived || dispositionRefOf(f) != "WV-1" {
		t.Errorf("active: %+v exit %d", f, active.Summary.ExitCode)
	}

	if active.Summary.ExitCode != 0 {
		t.Errorf("active: %+v exit %d", f, active.Summary.ExitCode)
	}

	wantWaiver := conformance.AppliedWaiver{
		ID: "WV-1", Rule: "CO-BND-001", Finding: fingerprint, Owner: "team-billing", Reason: "tracked in ARCH-1", Expires: "2026-12-31", State: "active",
	}
	if len(active.Exceptions.Waivers) != 1 || active.Exceptions.Waivers[0] != wantWaiver {
		t.Errorf("waivers %+v", active.Exceptions.Waivers)
	}
}

// assertWaiverExpired checks that the waiver has lapsed the day after: the finding keeps the waiver as expired and the run fails.
func assertWaiverExpired(t *testing.T, expired *conformance.Run) {
	t.Helper()

	f := &expired.Findings[0]
	if f.Disposition != exceptions.DispositionExpiredWaiver || dispositionRefOf(f) != "WV-1" {
		t.Errorf("expired: %+v", f)
	}

	sum := expired.Summary
	if sum.ExpiredWaiver != 1 || sum.ExitCode != 1 {
		t.Errorf("summary %+v", sum)
	}

	if expired.Exceptions.Waivers[0].State != "expired" {
		t.Errorf("summary %+v", sum)
	}

	if !slices.ContainsFunc(expired.Diagnostics, func(d diagCode) bool { return d.Code == "CO-EXC-006" }) {
		t.Error("no CO-EXC-006")
	}

	if expired.Inputs.Waivers == nil || expired.Inputs.Waivers.Path != "exceptions/waivers.yaml" {
		t.Errorf("waivers input %+v", expired.Inputs.Waivers)
	}
}

func TestDispositionOrphanedWaiverOnlyWhenRuleRan(t *testing.T) {
	t.Parallel()

	waivers := `
waivers:
  - {id: WV-9, finding: "sha256:` + strings.Repeat("a", 64) + `", rule: CO-BND-001, owner: o, reason: r, expires: 2030-01-01}
  - {id: WV-8, finding: "sha256:` + strings.Repeat("b", 64) + `", rule: CO-COV-001, owner: o, reason: r, expires: 2030-01-01}
`
	run := newSetup(t, map[string]string{"exceptions/waivers.yaml": waivers}, nil).mustCheck(t, nil)

	// CO-BND-001 ran (violation) and its waiver matches nothing. CO-COV-001 is unknown, so WV-8 is not judged.
	if !slices.Equal(run.Exceptions.OrphanedWaivers, []string{"WV-9"}) {
		t.Errorf("orphaned %v", run.Exceptions.OrphanedWaivers)
	}

	n := 0

	for _, d := range run.Diagnostics {
		if d.Code == "CO-EXC-003" {
			n++
		}
	}

	if n != 1 {
		t.Errorf("CO-EXC-003 count %d", n)
	}
}

func TestUseBaselineAndWaiversNeedExistingFiles(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)

	if e := s.failureOf(t, func(o *conformance.CheckOptions) { o.UseBaseline = "nope.json" }); e.Diagnostic.Code != "CO-EXC-001" {
		t.Errorf("baseline: %s", e.Diagnostic.Code)
	}

	if e := s.failureOf(t, func(o *conformance.CheckOptions) { o.Waivers = "nope.yaml" }); e.Diagnostic.Code != "CO-EXC-002" {
		t.Errorf("waivers: %s", e.Diagnostic.Code)
	}
}

func TestInvalidBaselineStopsBeforeAnyQuery(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{baselinePath: "{}"}, nil)

	if e := s.failureOf(t, nil); e.Diagnostic.Code != "CO-EXC-001" {
		t.Errorf("code %s", e.Diagnostic.Code)
	}

	if len(s.graph.Queries) != 0 {
		t.Errorf("queries %v", s.graph.Queries)
	}
}
