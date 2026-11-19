package cli_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/testutil"
)

const baselineWorkspaceYAML = "metadata: {name: t}\nobservation:\n  projects:\n    - {name: t, root: .}\n"

func baselineFP(n int) string { return fmt.Sprintf("sha256:%064d", n) }

func baselineResults(ns ...int) string {
	findings := make([]string, len(ns))
	for i, n := range ns {
		findings[i] = fmt.Sprintf(`{"fingerprint":%q,"ruleId":"R-1","severity":"error","owner":null,"disposition":"active","dispositionRef":null}`, baselineFP(n))
	}

	return `{"run":{"sourceRevision":"abc","graphGeneration":"g"},"inputs":{"policies":[{"path":"p","digest":"sha256:x"}]},"findings":[` +
		strings.Join(findings, ",") + `]}`
}

func baselineDir(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	tree := map[string]string{"concordia.yaml": baselineWorkspaceYAML}

	for k, v := range files {
		tree[k] = v
	}

	testutil.WriteTree(t, dir, tree)

	return dir
}

func baselineFingerprints(t *testing.T, dir string) []string {
	t.Helper()

	var b struct {
		Findings []struct {
			Fingerprint string `json:"fingerprint"`
			Owner       string `json:"owner"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, "exceptions/concordia.baseline.json")), &b); err != nil {
		t.Fatal(err)
	}

	out := make([]string, len(b.Findings))
	for i, f := range b.Findings {
		out[i] = f.Fingerprint
	}

	return out
}

// baselineActiveFingerprints gives the fingerprints that stay active against the baseline, in the order of findings.
func baselineActiveFingerprints(b *exceptions.Baseline, fingerprints ...int) []string {
	findings := make([]exceptions.Key, len(fingerprints))
	for i, n := range fingerprints {
		findings[i] = exceptions.Key{Fingerprint: baselineFP(n), RuleID: "R-1"}
	}

	res := exceptions.ApplyDispositions(findings, b, nil, testNow(), nil)

	var active []string

	for i, a := range res.Assignments {
		if a.Disposition == exceptions.DispositionActive {
			active = append(active, findings[i].Fingerprint)
		}
	}

	return active
}

func TestBaselineBrownfieldFileBytesAreUnchangedAndOnlyTheNewFindingIsActive(t *testing.T) {
	t.Parallel()

	dir := baselineDir(t, map[string]string{"r1.json": baselineResults(1, 2, 3), "r2.json": baselineResults(1, 2, 3, 4)})

	if res := runIn(t, dir, "baseline", "create", "--from", "r1.json"); res.Code != 0 {
		t.Fatalf("create: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	path := filepath.Join(dir, "exceptions", "concordia.baseline.json")

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var b exceptions.Baseline
	if err = json.Unmarshal(before, &b); err != nil {
		t.Fatal(err)
	}

	if len(b.Findings) != 3 || b.Findings[0].Owner != "unassigned" {
		t.Errorf("baseline = %+v", b)
	}

	if active := baselineActiveFingerprints(&b, 1, 2, 3, 4); !slices.Equal(active, []string{baselineFP(4)}) {
		t.Errorf("active = %v, want only finding 4", active)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Errorf("the baseline file changed")
	}

	// A second create is refused without --force.
	if again := runIn(t, dir, "baseline", "create", "--from", "r1.json"); again.Code != 2 {
		t.Errorf("second create: exit code = %d, want 2", again.Code)
	}

	if forced := runIn(t, dir, "baseline", "create", "--from", "r1.json", "--force"); forced.Code != 0 {
		t.Errorf("forced create: exit code = %d, stderr = %q", forced.Code, forced.Stderr)
	}
}

func TestBaselineAcceptIsRefusedInCI(t *testing.T) {
	t.Parallel()

	dir := baselineDir(t, map[string]string{"r1.json": baselineResults(1)})
	runIn(t, dir, "baseline", "create", "--from", "r1.json")

	res := runWith(t, &runConfig{Dir: dir, Env: map[string]string{"CI": "1"}},
		"baseline", "update", "--from", "r1.json", "--accept", baselineFP(1), "--reason", "x", "--owner", "o")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-EXC-008") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestBaselineUpdateAcceptsAndPrunes(t *testing.T) {
	t.Parallel()

	dir := baselineDir(t, map[string]string{"r1.json": baselineResults(1), "r2.json": baselineResults(2)})
	runIn(t, dir, "baseline", "create", "--from", "r1.json")

	if res := runIn(t, dir, "baseline", "update", "--from", "r2.json", "--accept", baselineFP(9), "--reason", "x", "--owner", "o"); res.Code != 2 {
		t.Errorf("accept of an unknown finding: exit code = %d, want 2", res.Code)
	}

	if res := runIn(t, dir, "baseline", "update", "--from", "r2.json", "--accept", baselineFP(2), "--reason", "x", "--owner", "o"); res.Code != 0 {
		t.Fatalf("accept: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if got := baselineFingerprints(t, dir); !slices.Equal(got, []string{baselineFP(1), baselineFP(2)}) {
		t.Errorf("fingerprints = %v", got)
	}

	if res := runIn(t, dir, "baseline", "update", "--from", "r2.json", "--accept", baselineFP(2), "--reason", "x"); res.Code != 2 {
		t.Errorf("accept without --owner: exit code = %d, want 2", res.Code)
	}
}

func TestBaselineCreateRejectsBadOptions(t *testing.T) {
	t.Parallel()

	dir := baselineDir(t, map[string]string{"r1.json": baselineResults(1)})

	for _, args := range [][]string{
		{"baseline", "create", "--from", "r1.json", "--severity", "fatal"},
		{"baseline", "create", "--from", "r1.json", "--expires", "tomorrow"},
		{"baseline", "create", "--from", "missing.json"},
	} {
		if res := runIn(t, dir, args...); res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}
}

func TestBaselineCreateRunsTheCheckWithoutFrom(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	if res := s.run(t, "baseline", "create"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if got := baselineFingerprints(t, s.dir); len(got) != 1 {
		t.Errorf("fingerprints = %v, want the one active finding", got)
	}
}

// baselineResultsWithModes makes a results file with one finding for each enforcement mode (the number is the fingerprint).
func baselineResultsWithModes(modes map[int]string) string {
	findings := make([]string, 0, 4)

	for _, n := range []int{1, 2, 3, 4} {
		findings = append(findings, fmt.Sprintf(`{"fingerprint":%q,"ruleId":"R-1","severity":"error","owner":null,"disposition":"active","dispositionRef":null,"enforcement":%q}`,
			baselineFP(n), modes[n]))
	}

	return `{"run":{"sourceRevision":"abc","graphGeneration":"g"},"inputs":{"policies":[{"path":"p","digest":"sha256:x"}]},"findings":[` +
		strings.Join(findings, ",") + `]}`
}

// MIG-03: a shadow finding is recorded in the results only. It is not accepted into a baseline.
func TestBaselineCreateExcludesShadowFindings(t *testing.T) {
	t.Parallel()

	results := baselineResultsWithModes(map[int]string{1: "mandatory", 2: "advisory", 3: "shadow", 4: ""})
	dir := baselineDir(t, map[string]string{"r.json": results})

	if res := runIn(t, dir, "baseline", "create", "--from", "r.json"); res.Code != 0 {
		t.Fatalf("create: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	got := baselineFingerprints(t, dir)
	if !slices.Equal(got, []string{baselineFP(1), baselineFP(2), baselineFP(4)}) {
		t.Errorf("fingerprints = %v, want findings 1, 2, and 4 (3 is shadow)", got)
	}
}

func TestBaselineUpdateRefusesToAcceptAShadowFinding(t *testing.T) {
	t.Parallel()

	results := baselineResultsWithModes(map[int]string{1: "mandatory", 2: "advisory", 3: "shadow", 4: "mandatory"})
	dir := baselineDir(t, map[string]string{"r.json": results, "r0.json": baselineResults(1)})
	runIn(t, dir, "baseline", "create", "--from", "r0.json")

	if res := runIn(t, dir, "baseline", "update", "--from", "r.json", "--accept", baselineFP(3), "--reason", "x", "--owner", "o"); res.Code != 2 {
		t.Errorf("accept of a shadow finding: exit code = %d, want 2", res.Code)
	}

	if res := runIn(t, dir, "baseline", "update", "--from", "r.json", "--accept", baselineFP(2), "--reason", "x", "--owner", "o"); res.Code != 0 {
		t.Errorf("accept of an advisory finding: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
