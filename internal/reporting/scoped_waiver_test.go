package reporting_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/testutil"
)

// scopedRun is the sample run with one scoped waiver: verified, with a compensating evidence reference, covering the
// waived finding of the sample.
func scopedRun() *conformance.Run {
	run := testutil.SampleRun()

	run.Exceptions.ScopedWaivers = []conformance.ScopedWaiver{{
		ID: "WV-12", Rule: "REV-APR-001", Scope: exceptions.Scope{Subject: "ASM-EDGE", Environment: "production"},
		Owner: "team-platform", Expires: "2027-01-31", State: "active", Digest: "sha256:" + strings.Repeat("c", 64),
		Approver: exceptions.Approver{Identity: "alice@example.com", Authority: "exception-board"},
		Verification: exceptions.Verification{
			Verified: true, Mechanism: "protected-review", Authority: "exception-board", Signer: "alice@example.com",
		},
		CompensatingEvidence: []exceptions.EvidenceResult{{Path: "docs/mitigation.md", SHA256: "sha256:" + strings.Repeat("d", 64), State: exceptions.EvidenceResolved}},
		CoveredFindings:      []string{testutil.FPWaived},
	}}

	return run
}

// assertHasKeys checks that the record has every key.
func assertHasKeys(t *testing.T, record map[string]any, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if _, ok := record[key]; !ok {
			t.Errorf("the record lacks %q: %v", key, record)
		}
	}
}

// assertLacksKeys checks that the record has none of the keys: they are free text or a fingerprint of the waiver.
func assertLacksKeys(t *testing.T, record map[string]any, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if _, ok := record[key]; ok {
			t.Errorf("the record holds %q, which is free text or a fingerprint of the waiver: %v", key, record)
		}
	}
}

// assertScopedWaiverEvidence checks the verification of the scoped waiver record, and the finding that it covers.
func assertScopedWaiverEvidence(t *testing.T, record map[string]any) {
	t.Helper()

	verification, _ := record["verification"].(map[string]any)
	if verification["verified"] != true || verification["mechanism"] != "protected-review" {
		t.Errorf("verification = %v", verification)
	}

	if covered, _ := record["coveredFindings"].([]any); len(covered) != 1 || covered[0] != testutil.FPWaived {
		t.Errorf("coveredFindings = %v", record["coveredFindings"])
	}
}

// Scenario: Bundle records waivers.
func TestResultsRecordScopedWaivers(t *testing.T) {
	t.Parallel()

	var doc struct {
		Exceptions struct {
			ScopedWaivers []map[string]any `json:"scopedWaivers"`
		} `json:"exceptions"`
	}

	decodeJSON(t, renderJSON(t, scopedRun()), &doc)

	if len(doc.Exceptions.ScopedWaivers) != 1 {
		t.Fatalf("scopedWaivers = %v", doc.Exceptions.ScopedWaivers)
	}

	got := doc.Exceptions.ScopedWaivers[0]
	assertHasKeys(t, got, "id", "rule", "scope", "approver", "verification", "expires", "compensatingEvidence", "coveredFindings")
	assertLacksKeys(t, got, "reason", "issue", "finding", "content")
	assertScopedWaiverEvidence(t, got)
}

func TestResultsWithoutScopedWaiversHaveNoScopedWaiversKey(t *testing.T) {
	t.Parallel()

	notContains(t, string(renderJSON(t, testutil.SampleRun())), "scopedWaivers")
}
