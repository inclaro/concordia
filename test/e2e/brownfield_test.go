package e2e_test

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	brownfieldProject = "e2e-brownfield"
	baselineRel       = "exceptions/concordia.baseline.json"
	refundRel         = "services/billing/src/Refund.ts"
	refundSource      = `import { ShipmentStore } from "../../fulfillment/src/internal/ShipmentStore";

export class Refund {
  refund(orderId: string): void {
    const store = new ShipmentStore();
    store.reserve(orderId);
  }
}
`
)

// brownfieldRun is the state the brownfield subtests hand to each other.
type brownfieldRun struct {
	repo            *repo
	baselineSha     string
	newFingerprints []string
	oldCount        int
}

// Brownfield ratchet, waivers with the clock, and a stale graph. The subtests run in sequence and share one repository.
func TestBrownfield(t *testing.T) {
	requireE2E(t)

	repo := newRepo(t, repoSpec{source: fixtureDir, oldName: "concordia-fixture-commerce", project: brownfieldProject, dirName: "brownfield"})

	if res := repo.gu("index"); res.code != 0 {
		t.Fatalf("index failed (%d): %s", res.code, res.stderr)
	}

	run := &brownfieldRun{repo: repo}

	t.Run("baseline create records every existing finding", run.baselineCreate)
	t.Run("a new file gives new active findings; the old ones stay baselined; the baseline file is unchanged", run.newFileStaysActive)
	t.Run("an expired waiver is reported as expired-waiver (exit 1); before its expiry the finding is waived", run.waiverExpiry)
	t.Run("a stale graph (new commit, no new index) gives exit 3 and CO-OBS-008", run.staleGraph)
}

func (b *brownfieldRun) baselineCreate(t *testing.T) {
	doc, _ := b.repo.checkJSON(nil)

	b.oldCount = len(doc.Findings)
	if b.oldCount < 4 {
		t.Fatalf("%d findings, want at least 4", b.oldCount)
	}

	requireBrownfieldRuleIDs(t, doc)

	if res := b.repo.gu("baseline", "create"); res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}

	b.requireBaselineMatches(t, doc)
	b.baselineSha = b.repo.sha(baselineRel)

	// With everything baselined, no finding is active: only the unknown rule fails the run.
	again, res := b.repo.checkJSON(nil)
	if !brownfieldAll(again.Summary.Active == 0, again.Summary.Baselined == b.oldCount, res.code == 4) {
		t.Errorf("active %d, baselined %d (want %d), exit %d (want 4)", again.Summary.Active, again.Summary.Baselined, b.oldCount, res.code)
	}
}

// brownfieldAll reports whether every condition holds.
func brownfieldAll(conditions ...bool) bool {
	return !slices.Contains(conditions, false)
}

// requireBrownfieldRuleIDs checks the findings come from exactly the four rules of the fixture.
func requireBrownfieldRuleIDs(t *testing.T, doc *checkDoc) {
	t.Helper()

	ruleIDs := map[string]bool{}
	for _, f := range doc.Findings {
		ruleIDs[f.RuleID] = true
	}

	want := []string{"CO-BND-001", "CO-CYC-001", "CO-DECL-001", "CO-LYR-001"}
	if !slices.Equal(slices.Sorted(maps.Keys(ruleIDs)), want) {
		t.Errorf("rule ids = %v", ruleIDs)
	}
}

// requireBaselineMatches checks the baseline file holds the fingerprints of the run.
func (b *brownfieldRun) requireBaselineMatches(t *testing.T, doc *checkDoc) {
	t.Helper()

	var baseline struct {
		Findings []struct {
			Fingerprint string `json:"fingerprint"`
			RuleID      string `json:"ruleId"`
		} `json:"findings"`
	}

	if err := json.Unmarshal([]byte(b.repo.read(baselineRel)), &baseline); err != nil {
		t.Fatal(err)
	}

	if len(baseline.Findings) != b.oldCount {
		t.Errorf("baseline has %d findings, want %d", len(baseline.Findings), b.oldCount)
	}

	inBaseline := make([]string, 0, len(baseline.Findings))
	for _, f := range baseline.Findings {
		inBaseline = append(inBaseline, f.Fingerprint)
	}

	inRun := make([]string, 0, len(doc.Findings))
	for _, f := range doc.Findings {
		inRun = append(inRun, f.Fingerprint)
	}

	slices.Sort(inBaseline)
	slices.Sort(inRun)

	if !slices.Equal(inBaseline, inRun) {
		t.Errorf("baseline fingerprints %v, run fingerprints %v", inBaseline, inRun)
	}
}

func (b *brownfieldRun) newFileStaysActive(t *testing.T) {
	b.repo.write(refundRel, refundSource)
	b.repo.commitAll("add Refund")

	if res := b.repo.gu("index"); res.code != 0 {
		t.Fatalf("index: exit %d: %s", res.code, res.stderr)
	}

	doc, res := b.repo.checkJSON(nil)
	if res.code != 1 {
		t.Fatalf("exit %d, want 1", res.code)
	}

	active := doc.findingsWhere(func(f *checkFinding) bool { return f.Disposition == "active" })
	if len(active) == 0 {
		t.Fatal("no active finding")
	}

	requireActiveAtRefund(t, active)
	b.requireOldFindingsBaselined(t, doc)

	if doc.Summary.Active != len(active) {
		t.Errorf("summary.active = %d, want %d", doc.Summary.Active, len(active))
	}

	if got := b.repo.sha(baselineRel); got != b.baselineSha {
		t.Errorf("baseline file changed: %s", got)
	}

	b.newFingerprints = b.newFingerprints[:0]
	for _, f := range active {
		b.newFingerprints = append(b.newFingerprints, f.Fingerprint)
	}

	slices.Sort(b.newFingerprints)
}

// requireActiveAtRefund checks every active finding is the boundary finding of the new file.
func requireActiveAtRefund(t *testing.T, active []*checkFinding) {
	t.Helper()

	for _, f := range active {
		if f.RuleID != "CO-BND-001" || f.locationPath() != refundRel {
			t.Errorf("active finding %s at %q", f.RuleID, f.locationPath())
		}
	}
}

// requireOldFindingsBaselined checks the findings that existed before the new file are all baselined.
func (b *brownfieldRun) requireOldFindingsBaselined(t *testing.T, doc *checkDoc) {
	t.Helper()

	baselined := doc.findingsWhere(func(f *checkFinding) bool { return f.Disposition == "baselined" })
	if len(baselined) != b.oldCount {
		t.Errorf("%d baselined, want %d", len(baselined), b.oldCount)
	}

	for _, f := range baselined {
		if f.locationPath() == refundRel {
			t.Errorf("baselined finding at %s", refundRel)
		}
	}
}

// ours returns the findings whose fingerprint was active after the new file.
func (b *brownfieldRun) ours(doc *checkDoc) []*checkFinding {
	return doc.findingsWhere(func(f *checkFinding) bool { return slices.Contains(b.newFingerprints, f.Fingerprint) })
}

func (b *brownfieldRun) waiverExpiry(t *testing.T) {
	b.addWaivers(t)
	containsAll(t, b.repo.read("exceptions/waivers.yaml"), "WV-1", "2026-12-31")

	expiredEnv := []string{"CONCORDIA_NOW=2027-01-01T00:00:00Z"}
	b.requireExpiredDocument(t, expiredEnv)
	b.requireExpiredCLI(t, expiredEnv)
	b.requireWaivedBeforeExpiry(t)
}

// addWaivers waives every new finding until the end of 2026.
func (b *brownfieldRun) addWaivers(t *testing.T) {
	t.Helper()

	for _, fp := range b.newFingerprints {
		res := b.repo.cli([]string{"CONCORDIA_NOW=2026-10-01T00:00:00Z"},
			"waiver", "add", "--finding", fp, "--owner", "team-billing", "--reason", "test", "--expires", "2026-12-31")
		if res.code != 0 {
			t.Fatalf("waiver add: exit %d: %s", res.code, res.stderr)
		}
	}
}

// requireExpiredDocument checks the results.json of a run after the waivers ran out.
func (b *brownfieldRun) requireExpiredDocument(t *testing.T, env []string) {
	t.Helper()

	expired, res := b.repo.checkJSON(env)
	if res.code != 1 {
		t.Errorf("expired check: exit %d, want 1", res.code)
	}

	ours := b.ours(expired)
	if len(ours) != len(b.newFingerprints) {
		t.Errorf("%d findings, want %d", len(ours), len(b.newFingerprints))
	}

	for _, f := range ours {
		requireExpiredWaiverFinding(t, f)
	}

	for _, w := range expired.Exceptions.Waivers {
		if w.State != "expired" {
			t.Errorf("waiver %s state %q", w.ID, w.State)
		}
	}

	if expired.Summary.ExpiredWaiver != len(b.newFingerprints) {
		t.Errorf("summary.expiredWaiver = %d, want %d", expired.Summary.ExpiredWaiver, len(b.newFingerprints))
	}

	codes := make([]string, 0, len(expired.Diagnostics))
	for _, d := range expired.Diagnostics {
		codes = append(codes, d.Code)
	}

	if !slices.Contains(codes, "CO-EXC-006") {
		t.Errorf("diagnostics = %v", codes)
	}
}

// requireExpiredWaiverFinding checks a finding names its expired waiver.
func requireExpiredWaiverFinding(t *testing.T, f *checkFinding) {
	t.Helper()

	waiverID := regexp.MustCompile(`^WV-\d+$`)

	if f.Disposition == "expired-waiver" && f.DispositionRef != nil && waiverID.MatchString(*f.DispositionRef) {
		return
	}

	t.Errorf("finding %s: disposition %q ref %v", f.Fingerprint, f.Disposition, f.DispositionRef)
}

// requireExpiredCLI checks the pretty output and `waiver validate` after the waivers ran out.
func (b *brownfieldRun) requireExpiredCLI(t *testing.T, env []string) {
	t.Helper()

	// The pretty output shows the waiver.
	pretty := b.repo.cli(env, "check")
	if pretty.code != 1 {
		t.Errorf("pretty check: exit %d, want 1", pretty.code)
	}

	containsAll(t, pretty.stdout, "expired-waiver", "WV-1")

	valid := b.repo.cli(env, "waiver", "validate")
	if valid.code != 2 {
		t.Errorf("waiver validate: exit %d, want 2", valid.code)
	}

	containsAll(t, valid.output(), "CO-EXC-006")
}

// requireWaivedBeforeExpiry checks the findings are waived one minute before the waivers end.
func (b *brownfieldRun) requireWaivedBeforeExpiry(t *testing.T) {
	t.Helper()

	waived, waivedRes := b.repo.checkJSON([]string{"CONCORDIA_NOW=2026-12-31T23:59:00Z"})

	for _, f := range b.ours(waived) {
		if f.Disposition != "waived" {
			t.Errorf("finding %s: disposition %q, want waived", f.Fingerprint, f.Disposition)
		}
	}

	if waived.Summary.Active != 0 || waived.Summary.Waived != len(b.newFingerprints) {
		t.Errorf("active %d, waived %d (want %d)", waived.Summary.Active, waived.Summary.Waived, len(b.newFingerprints))
	}

	// Only the unknown rule (CO-COV-001) is left.
	if waivedRes.code != 4 {
		t.Errorf("waived check: exit %d, want 4", waivedRes.code)
	}
}

func (b *brownfieldRun) staleGraph(t *testing.T) {
	b.repo.write("services/billing/src/Extra.ts", "export const extra = 1;\n")
	b.repo.commitAll("add Extra")

	res := b.repo.gu("check")
	if res.code != 3 {
		t.Fatalf("exit %d, want 3: %s", res.code, strings.TrimSpace(res.stderr))
	}

	containsAll(t, res.stderr, "CO-OBS-008")
}
