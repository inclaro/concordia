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

	files := func(run *conformance.Run) map[string]string {
		dir := t.TempDir()
		tree := map[string]string{}

		for _, ref := range inputRefsOf(&run.Inputs) {
			tree[ref.Path] = "x\n"
		}

		testutil.WriteTree(t, dir, tree)

		entries, _, _, err := bundleEntries(t.Context(), &Context{}, &workspace.Workspace{Root: dir}, run, &exportBundleOptions{snippets: snippetsNone})
		if err != nil {
			t.Fatal(err)
		}

		out := map[string]string{}
		for _, e := range entries {
			out[e.Path] = string(e.Data)
		}

		return out
	}

	got := files(run)

	scoped, ok := got[bundle.ScopedWaiversPath]
	if !ok || !strings.Contains(scoped, `"WV-12"`) || !strings.Contains(scoped, testutil.FPWaived) || !strings.Contains(scoped, `"verified": true`) {
		t.Errorf("the bundle lacks the scoped waivers: %v", scoped)
	}

	if _, ok := files(testutil.SampleRun())[bundle.ScopedWaiversPath]; ok {
		t.Error("a run without a scoped waiver has no scoped waiver file")
	}
}
