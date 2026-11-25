package cli

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// scopedBundleFiles gives the files of the bundle of the run by their path, with every input of the run present.
func scopedBundleFiles(t *testing.T, run *conformance.Run) map[string]string {
	t.Helper()

	dir := t.TempDir()
	tree := map[string]string{}

	for _, ref := range inputRefsOf(&run.Inputs) {
		tree[ref.Path] = "x\n"
	}

	testutil.WriteTree(t, dir, tree)

	job := &exportJob{app: &Context{}, wsp: &workspace.Workspace{Root: dir}, run: run, opts: &exportBundleOptions{snippets: snippetsNone}}

	entries, _, _, err := bundleEntries(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]string{}
	for _, e := range entries {
		out[e.Path] = string(e.Data)
	}

	return out
}

// Scenario: Bundle records waivers.
func TestExportBundleHoldsTheScopedWaivers(t *testing.T) {
	t.Parallel()

	run := testutil.SampleRun()
	run.Exceptions.ScopedWaivers = []conformance.ScopedWaiver{{
		ID: "WV-12", Rule: "REV-APR-001", Scope: exceptions.Scope{Subject: "ASM-EDGE"}, Owner: "team-platform", Expires: "2027-01-31",
		State: "active", Digest: "sha256:" + strings.Repeat("c", 64),
		Approver:             exceptions.Approver{Identity: "alice@example.com", Authority: "exception-board"},
		Verification:         exceptions.Verification{Verified: true, Mechanism: "protected-review", Authority: "exception-board"},
		CompensatingEvidence: []exceptions.EvidenceResult{}, CoveredFindings: []string{testutil.FPWaived},
	}}

	scoped, ok := scopedBundleFiles(t, run)[bundle.ScopedWaiversPath]
	if !ok {
		t.Fatal("the bundle lacks the scoped waivers")
	}

	for _, want := range []string{`"WV-12"`, testutil.FPWaived, `"verified": true`} {
		if !strings.Contains(scoped, want) {
			t.Errorf("the scoped waivers lack %s: %v", want, scoped)
		}
	}

	if _, ok := scopedBundleFiles(t, testutil.SampleRun())[bundle.ScopedWaiversPath]; ok {
		t.Error("a run without a scoped waiver has no scoped waiver file")
	}
}
