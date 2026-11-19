package assessment_test

import (
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
)

// recordWant reports a field whose value differs from the wanted one.
func recordWant[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

// recordRenderApproval renders an approval and fails the test when it cannot.
func recordRenderApproval(t *testing.T, a *assessment.Approval) []byte {
	t.Helper()

	text, err := assessment.RenderApproval(a)
	if err != nil {
		t.Fatal(err)
	}

	return text
}

// recordRenderRevocation renders a revocation and fails the test when it cannot.
func recordRenderRevocation(t *testing.T, r *assessment.Revocation) []byte {
	t.Helper()

	text, err := assessment.RenderRevocation(r)
	if err != nil {
		t.Fatal(err)
	}

	return text
}

// recordApprovalValues parses the approval text and gives the mapping of its document.
func recordApprovalValues(t *testing.T, text string) map[string]any {
	t.Helper()

	doc, err := assessment.ParseApproval("a.yaml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}

	values, err := doc.Doc()
	if err != nil {
		t.Fatal(err)
	}

	return values
}

// recordAssertApprovalCore checks the fields of the complete approval record.
func recordAssertApprovalCore(t *testing.T, a *assessment.Approval) {
	t.Helper()

	recordWant(t, "id", a.ID, "ASM-EDGE-v1-1")
	recordWant(t, "decision", a.Decision, assessment.DecisionApproved)
	recordWant(t, "environment", a.Environment, "production")
	recordWant(t, "assessment version", a.Assessment.Version, 1)
	recordWant(t, "contract version", a.ContractVersion, "review-digest/1")
	recordWant(t, "capability profile", a.CapabilityProfile, "cbm-0.12.1")
	recordWant(t, "reviewer identity", a.Reviewer.Identity, "alice@example.com")
	recordWant(t, "reviewer authority", a.Reviewer.Authority, "security-review")
	recordWant(t, "decided at", a.DecidedAt.Equal(decided), true)
	recordWant(t, "assumptions", len(a.Assumptions), 1)
	recordWant(t, "evidence", len(a.Evidence), 1)
	recordWant(t, "previous is nil", a.Previous == nil, true)
	recordWant(t, "delta is nil", a.Delta == nil, true)
	recordWant(t, "dependencies", len(a.Subject.Dependencies), 1)

	if len(a.Subject.Dependencies) == 1 {
		recordWant(t, "dependency kind", a.Subject.Dependencies[0].Kind, assessment.DepPublicEntryPoint)
	}

	recordAssertApprovalExpiry(t, a)
}

// recordAssertApprovalExpiry checks the expiry of the complete approval record.
func recordAssertApprovalExpiry(t *testing.T, a *assessment.Approval) {
	t.Helper()

	if a.Expiry == nil {
		t.Errorf("expiry is nil in approval = %+v", a)

		return
	}

	recordWant(t, "expiry year", a.Expiry.ExpiresAt.Year(), 2027)
	recordWant(t, "expiry conditions", len(a.Expiry.Conditions), 1)
}

// recordAssertApprovalDigest checks that the digest of an approval is the digest of its canonical JSON.
func recordAssertApprovalDigest(t *testing.T, a *assessment.Approval) {
	t.Helper()

	if !strings.HasPrefix(a.Digest, "sha256:") || len(a.CanonicalJSON()) == 0 {
		t.Errorf("digest = %q, canonical = %q", a.Digest, a.CanonicalJSON())
	}

	doc, err := a.Doc()
	if err != nil {
		t.Fatal(err)
	}

	again, err := assessment.RecordDigest(doc)
	if err != nil || again != a.Digest {
		t.Errorf("digest of Doc() = %s (%v), want %s", again, err, a.Digest)
	}
}

// recordAssertDelta checks the previous reference and the delta of the incremental approval.
func recordAssertDelta(t *testing.T, a *assessment.Approval) {
	t.Helper()

	if a.Previous == nil || a.Delta == nil {
		t.Fatalf("approval = %+v, delta = %+v", a.Previous, a.Delta)
	}

	recordWant(t, "previous approval", a.Previous.Approval, "ASM-EDGE-v1-0")
	recordWant(t, "added", len(a.Delta.Added), 1)
	recordWant(t, "removed", len(a.Delta.Removed), 0)
	recordWant(t, "modified", len(a.Delta.Modified), 1)

	if len(a.Delta.Modified) == 1 {
		recordWant(t, "previous digest", a.Delta.Modified[0].PreviousDigest, digestA)
	}
}

// recordAssertRevocation checks the fields of the complete revocation record.
func recordAssertRevocation(t *testing.T, r *assessment.Revocation) {
	t.Helper()

	recordWant(t, "id", r.ID, "ASM-EDGE-v1-1")
	recordWant(t, "approval id", r.Approval.ID, "ASM-EDGE-v1-1")
	recordWant(t, "reviewer authority", r.Reviewer.Authority, "security-review")
	recordWant(t, "decided at", r.DecidedAt.Equal(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)), true)
	recordWant(t, "reason is empty", r.Reason == "", false)
	recordWant(t, "digest is empty", r.Digest == "", false)
	recordWant(t, "canonical JSON is empty", len(r.CanonicalJSON()) == 0, false)
}

// recordRevokedLayout gives the files of the edge workspace with one approval and its revocation.
func recordRevokedLayout(t *testing.T) map[string]string {
	t.Helper()

	first := approvalFor(loadedEdge(t), "ASM-EDGE-v1-1", decided)
	text := recordRenderApproval(t, first)

	sealedFirst, err := assessment.ParseApproval("x.yaml", text)
	if err != nil {
		t.Fatal(err)
	}

	revText := recordRenderRevocation(t, revocationOf(sealedFirst, "ASM-EDGE-v1-1", decided.Add(time.Hour)))

	files := edgeFiles()
	files[assessment.ApprovalPath("assessments", "ASM-EDGE", 1, 1)] = string(text)
	files[assessment.RevocationPath("assessments", "ASM-EDGE-v1-1")] = string(revText)

	return files
}

// recordAssertRevokedSet checks that the loaded set holds the approval and its revocation, and that the review
// state is revoked.
func recordAssertRevokedSet(t *testing.T, set *assessment.Set) {
	t.Helper()

	recordWant(t, "approvals", len(set.Approvals), 1)
	recordWant(t, "revocations", len(set.Revocations), 1)

	if set.Approval("ASM-EDGE-v1-1") == nil {
		t.Fatal("the set does not hold the approval ASM-EDGE-v1-1")
	}

	if got := set.RevocationsFor("ASM-EDGE-v1-1"); len(got) != 1 {
		t.Errorf("RevocationsFor = %d", len(got))
	}

	review := assessment.ReviewOf(set.Assessments[0], set.Approvals, set.Revocations)
	if review.State != assessment.StateRevoked {
		t.Errorf("state = %s, want revoked", review.State)
	}
}

// recordAssertProducedUnderRequired checks that a document without one producedUnder field fails.
func recordAssertProducedUnderRequired(t *testing.T, field string) {
	t.Helper()

	values := recordApprovalValues(t, approvalYAML)

	produced, _ := values["producedUnder"].(map[string]any)
	delete(produced, field)

	_, err := assessment.ParseApproval("a.yaml", []byte(yamlOf(values)))
	_ = wantDiag(t, err, diag.ASM001, field)
}

// recordAssertProducedUnderBound checks that producedUnder is part of the record digest, and that a record
// without evaluators renders again.
func recordAssertProducedUnderBound(t *testing.T) {
	t.Helper()

	first, err := assessment.ParseApproval("a.yaml", []byte(approvalYAML))
	if err != nil {
		t.Fatal(err)
	}

	changed, err := assessment.ParseApproval("b.yaml", []byte(strings.Replace(approvalYAML, "normalize/1", "normalize/2", 1)))
	if err != nil || changed.Digest == first.Digest {
		t.Errorf("producedUnder is not part of the digest: %v", err)
	}

	first.ProducedUnder.EvaluatorVersions = nil

	if _, err := assessment.RenderApproval(first); err != nil {
		t.Errorf("RenderApproval with nil evaluator versions: %v", err)
	}
}
