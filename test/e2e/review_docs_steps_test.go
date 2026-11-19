package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rvParseDoc reads the results of a check that ran with its own flags and wants the exit code.
func rvParseDoc(t *testing.T, res cliResult, wantCode int, what string) *rvDoc {
	t.Helper()

	doc := new(rvDoc)

	err := json.Unmarshal([]byte(res.stdout), doc)
	if err != nil || res.code != wantCode {
		t.Fatalf("%s: exit %d, want %d: %v %s", what, res.code, wantCode, err, res.stderr)
	}

	return doc
}

// rvWrote tells whether a command exited 0 and printed every part.
func rvWrote(res cliResult, parts ...string) bool {
	if res.code != 0 {
		return false
	}

	for _, part := range parts {
		if !strings.Contains(res.stdout, part) {
			return false
		}
	}

	return true
}

// rvWantParts reports each part that the text lacks. what names the text in the message.
func rvWantParts(t *testing.T, what, text string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%s lacks %q:\n%s", what, part, text)
		}
	}
}

// rvRemoveFile removes a file of the repository.
func (r *repo) rvRemoveFile(rel string) {
	r.t.Helper()

	if err := os.Remove(r.path(rel)); err != nil {
		r.t.Fatal(err)
	}
}

// rvNotAppliedIs tells whether an assessment has exactly one record that is not applied, for the reason.
func rvNotAppliedIs(a *rvAssessment, reason string) bool {
	return len(a.NotApplied) == 1 && a.NotApplied[0]["reason"] == reason
}

// rvVerifiedApproval tells whether an assessment has an approval of the mechanism that was verified.
func rvVerifiedApproval(a *rvAssessment, mechanism string) bool {
	return a.Approval != nil && a.Approval.Mechanism == mechanism && a.Approval.Verified
}

// rvDocsPrerequisites: the assessment version validates offline, and `check` does not pass before an approval.
func rvDocsPrerequisites(r *repo) {
	r.t.Helper()

	if res := r.cli(rvEnv(), "--offline", "assessment", "validate"); res.code != 0 {
		r.t.Fatalf("assessment validate (%d): %s", res.code, res.output())
	}

	before := r.check(1)
	if before.verdict("REV-APR-001") != "violation" {
		r.t.Errorf("before the approval REV-APR-001 = %q, want violation", before.verdict("REV-APR-001"))
	}
}

// rvDocsRefusals: what the commands refuse is exit 2 with CO-ASM-005 (CO-ASM-001 for an assessment that does not
// exist), and nothing is written.
func rvDocsRefusals(r *repo) {
	r.t.Helper()

	r.refused(r.cli(rvEnv(), "assessment", "approve", "ASM-EDGE"), "CO-ASM-005", "approve without --reviewer")
	r.refused(r.cli([]string{"CI=true", "CONCORDIA_NOW=" + rvNow}, "assessment", "approve", "ASM-EDGE", "--reviewer", rvReviewer), "CO-ASM-005", "approve with CI=true")
	r.refused(r.cli(rvEnv(), "assessment", "approve", "ASM-MISSING", "--reviewer", rvReviewer), "CO-ASM-001", "approve of an assessment that does not exist")
	r.refused(r.approve(nil, "--confirm-subject", "sha256:"+strings.Repeat("0", 64)), "CO-ASM-005", "approve with another digest")
	r.refused(r.cli(rvEnv(), "assessment", "approve", "ASM-TERRAFORM", "--reviewer", rvReviewer), "CO-ASM-005", "approve of a subject that cannot be computed")
	r.wantNoFile(rvEdgeRecord)
}

// rvDocsRejectedDecision: `--decision rejected` writes a rejection to the path that `--path` names.
func rvDocsRejectedDecision(r *repo, digest string) {
	r.t.Helper()

	rejected := "assessments/approvals/ASM-EDGE-rejected.yaml"

	res := r.approve(nil, "--confirm-subject", digest, "--decision", "rejected", "--path", rejected)
	if !rvWrote(res, "approval written: "+rejected) {
		r.t.Fatalf("approve --decision rejected (%d): %s", res.code, res.output())
	}

	if !strings.Contains(r.read(rejected), "decision: rejected") {
		r.t.Errorf("the record is not a rejection:\n%s", r.read(rejected))
	}

	r.rvRemoveFile(rejected)
}

// rvDocsApproveRecord writes the approval with `--expires` and `--condition` and gives the text of the record.
func rvDocsApproveRecord(r *repo, digest string) string {
	r.t.Helper()

	res := r.approve(nil, "--confirm-subject", digest, "--expires", "2027-01-31", "--condition", "a new public route is added")
	if !rvWrote(res, "approval written: "+rvEdgeRecord, "Record digest: sha256:", "merge it into the anchor branch") {
		r.t.Fatalf("approve (%d): %s", res.code, res.output())
	}

	record := r.read(rvEdgeRecord)
	rvWantParts(r.t, "the record", record, "expiresAt: \"2027-02-01T00:00:00Z\"", "a new public route is added", "authority: "+rvAuthority, "decision: approved")

	return record
}

// rvDocsNoOverwrite: a record is never overwritten; a file that exists is refused, and a second decision takes the next number.
func rvDocsNoOverwrite(r *repo, digest, record string) {
	r.t.Helper()

	r.refused(r.approve(nil, "--confirm-subject", digest, "--path", rvEdgeRecord), "CO-ASM-005", "approve over an existing record")

	const second = "assessments/approvals/ASM-EDGE-v1-2.yaml"

	if res := r.approve(nil, "--confirm-subject", digest); !rvWrote(res, "approval written: "+second) {
		r.t.Fatalf("a second approve (%d): %s", res.code, res.output())
	}

	r.rvRemoveFile(second)

	if r.read(rvEdgeRecord) != record {
		r.t.Error("the record was overwritten")
	}
}

// rvDocsRecordOnlyInCandidate: the record is in the candidate only, so it "does not exist at the trust anchor"
// (CO-ASM-006) and the gate stays as it was.
func rvDocsRecordOnlyInCandidate(r *repo) {
	r.t.Helper()

	r.commit("docs: approve ASM-EDGE v1 in the candidate")

	local := r.check(1)
	if !anyContains(local.messages("CO-ASM-006"), "does not exist at the trust anchor") || local.verdict("REV-APR-001") != "violation" {
		r.t.Errorf("a record in the candidate only: CO-ASM-006 %v, REV-APR-001 = %q", local.messages("CO-ASM-006"), local.verdict("REV-APR-001"))
	}

	if a := local.assessment(r.t, "ASM-EDGE"); !rvNotAppliedIs(a, "not-on-anchor") {
		r.t.Errorf("notApplied = %v, want not-on-anchor", a.NotApplied)
	}
}

// rvDocsApprovalOnAnchor: steps 3 and 4: the record and the assessment version go to the anchor, then the candidate
// is rebuilt on it. `assessment status` shows the same and exits 0 whatever the currency says.
func rvDocsApprovalOnAnchor(r *repo) {
	r.t.Helper()

	r.pullRequest("approve-asm-edge", "docs: approve ASM-EDGE v1", rvEdgeFile, rvEdgeRecord)

	current := r.check(0)
	a := current.assessment(r.t, "ASM-EDGE")
	a.want(r.t, "current")

	if !rvProtectedApproval(a) || current.verdict("REV-APR-001") != "pass" {
		r.t.Errorf("approval = %+v, REV-APR-001 = %q, want a verified protected-review approval and a pass", a.Approval, current.verdict("REV-APR-001"))
	}

	status := r.cli(rvEnv(), "assessment", "status", "--json")
	if status.code != 0 || !rvStateIsCurrent(status.stdout) {
		r.t.Errorf("assessment status --json (%d): %s", status.code, status.output())
	}
}

// rvProtectedApproval tells whether the approval of ASM-EDGE is the verified protected-review record 1.
func rvProtectedApproval(a *rvAssessment) bool {
	return rvVerifiedApproval(a, "protected-review") && a.Approval.ID == "ASM-EDGE-v1-1"
}

// rvStateIsCurrent tells whether the JSON text of `assessment status` shows the state current.
func rvStateIsCurrent(text string) bool {
	return strings.Contains(text, `"state":"current"`) || strings.Contains(text, `"state": "current"`)
}

// rvDocsRevocation: the first run prints the approval and its digest and writes nothing (exit 2), the second writes the
// record. With protected-review the revocation counts once it is on the anchor; then the assessment is revoked.
func rvDocsRevocation(r *repo) {
	r.t.Helper()

	revoke := []string{"assessment", "revoke", "ASM-EDGE-v1-1", "--reviewer", rvReviewer, "--reason", "gateway replaced"}

	res := r.cli(rvEnv(), revoke...)
	m := rvApprovalDigestRe.FindStringSubmatch(res.stdout)

	r.refused(res, "CO-ASM-005", "revoke without --confirm-subject")

	if m == nil {
		r.t.Fatalf("revoke printed no approval digest: %s", res.output())
	}

	r.wantNoFile(rvEdgeRevoke)

	if res := r.cli(rvEnv(), slices.Concat(revoke, []string{"--confirm-subject", m[1]})...); !rvWrote(res, "revocation written: "+rvEdgeRevoke) {
		r.t.Fatalf("revoke (%d): %s", res.code, res.output())
	}

	r.commit("docs: revoke ASM-EDGE-v1-1 in the candidate")

	r.pullRequest("revoke-asm-edge", "docs: revoke ASM-EDGE-v1-1", rvEdgeRevoke)

	revoked := r.check(1)
	revoked.assessment(r.t, "ASM-EDGE").want(r.t, "revoked", "revoked")
}

// rvDocsNotCountedCase is one reason of the table "When a record is not counted": the edit of the record on the anchor
// and in the candidate that causes it.
type rvDocsNotCountedCase struct {
	anchor    func(string) string
	candidate func(string) string
	name      string
	reason    string
	path      string
}

// rvDocsNotCountedCases are the reasons of the table.
func rvDocsNotCountedCases() []rvDocsNotCountedCase {
	return []rvDocsNotCountedCase{
		{name: "path-not-allowed", reason: "path-not-allowed", path: "assessments/records/ASM-EDGE-v1-1.yaml"},
		{name: "reviewer-not-listed", reason: "reviewer-not-listed", path: rvEdgeRecord, anchor: func(s string) string {
			return strings.Replace(s, "identity: "+rvReviewer, "identity: bob@example.com", 1)
		}},
		{name: "authority-not-configured", reason: "authority-not-configured", path: rvEdgeRecord, anchor: func(s string) string {
			return strings.Replace(s, "authority: "+rvAuthority, "authority: ghost-review", 1)
		}},
		{name: "digest-mismatch", reason: "digest-mismatch", path: rvEdgeRecord, candidate: func(s string) string {
			return strings.Replace(s, `decidedAt: "2026-11-01T10:00:00Z"`, `decidedAt: "2026-11-02T10:00:00Z"`, 1)
		}},
	}
}

// rvDocsEdited applies an optional edit to a text.
func rvDocsEdited(edit func(string) string, text string) string {
	if edit == nil {
		return text
	}

	return edit(text)
}

// rvDocsCheckNotCounted runs one case of the table: the record is changed and the assessment shows the reason.
func rvDocsCheckNotCounted(t *testing.T, record string, c rvDocsNotCountedCase) {
	t.Helper()

	r := newRvRepo(t, &rvSpec{name: "docs-" + c.name, docs: []string{"ASM-EDGE"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}}})

	onAnchor := rvDocsEdited(c.anchor, record)
	r.toAnchor("docs: a record that Concordia does not count", map[string]string{c.path: onAnchor})

	if c.candidate != nil {
		r.write(c.path, c.candidate(onAnchor))
		r.commit("docs: edit the record in the candidate")
	}

	doc := r.check(1)
	a := doc.assessment(t, "ASM-EDGE")
	a.want(t, "review-required", "no approval")

	if !rvNotAppliedIs(a, c.reason) {
		t.Errorf("notApplied = %v, want the reason %s", a.NotApplied, c.reason)
	}

	if len(doc.messages("CO-ASM-006")) != 1 {
		t.Errorf("CO-ASM-006 = %v", doc.messages("CO-ASM-006"))
	}
}

// rvDocsSignedFlow is the signed flow of the page: a repository, the fake cosign and the digest that the reviewer read.
type rvDocsSignedFlow struct {
	r      *repo
	digest string
	cosign []string
}

// env is the environment of a run that has the fake cosign.
func (f *rvDocsSignedFlow) env() []string {
	return rvEnv(f.cosign...)
}

// approveAs signs an approval of the reviewer and wants exit 0.
func (f *rvDocsSignedFlow) approveAs(reviewer, what string) {
	f.r.t.Helper()

	res := f.r.cli(f.env(), "assessment", "approve", "ASM-EDGE", "--reviewer", reviewer, "--confirm-subject", f.digest, "--sign")
	if res.code != 0 {
		f.r.t.Fatalf("%s (%d): %s", what, res.code, res.output())
	}
}

// refusals: a missing cosign is CO-WRK-014 (exit 5); signing with --offline, keyless or with a key, is CO-WRK-016 (exit 6).
func (f *rvDocsSignedFlow) refusals() {
	r := f.r
	r.t.Helper()

	r.refused2(r.approve([]string{"COSIGN=", "PATH=" + rvPathWithout(r.t)}, "--confirm-subject", f.digest, "--sign"), 5, "CO-WRK-014", "approve --sign without cosign")
	r.refused2(r.approve([]string{"COSIGN=" + filepath.Join(r.t.TempDir(), "no-cosign")}, "--confirm-subject", f.digest, "--sign"), 5, "CO-REP-003", "approve --sign with a COSIGN path that cannot start")
	r.wantNoFile(rvEdgeRecord + ".sigstore.json")
	r.refused2(r.cli(f.env(), "--offline", "assessment", "approve", "ASM-EDGE", "--reviewer", rvReviewer, "--confirm-subject", f.digest, "--sign"), 6, "CO-WRK-016", "approve --sign --offline")
}

// signedRecord: the signed record travels with the candidate; no second merge is needed.
func (f *rvDocsSignedFlow) signedRecord() {
	r := f.r
	r.t.Helper()

	res := r.approve(f.cosign, "--confirm-subject", f.digest, "--sign")
	if !rvWrote(res, "signature written: "+rvEdgeRecord+".sigstore.json") {
		r.t.Fatalf("approve --sign (%d): %s", res.code, res.output())
	}

	doc := r.checkWith(0, f.env())
	a := doc.assessment(r.t, "ASM-EDGE")
	a.want(r.t, "current")

	if !rvVerifiedApproval(a, "signed") || doc.verdict("REV-APR-001") != "pass" {
		r.t.Errorf("approval = %+v, want a verified signed approval", a.Approval)
	}
}

// offline: a signed record checked with --offline (keyless or keyed) is unknown with CO-ASM-011 (a warning), never
// exit 6; a mandatory gate exits 4.
func (f *rvDocsSignedFlow) offline() {
	r := f.r
	r.t.Helper()

	res := r.cli(f.env(), "--offline", "--format", "json", "check")
	offline := rvParseDoc(r.t, res, 4, "check --offline")
	a := offline.assessment(r.t, "ASM-EDGE")

	unknown := a.UnknownReason == "verification-unavailable" && offline.verdict("REV-APR-001") == "unknown"
	if !offline.hasDiagnostic("CO-ASM-011") || !unknown {
		r.t.Errorf("--offline: CO-ASM-011 %v, reason %q, REV-APR-001 %q", offline.hasDiagnostic("CO-ASM-011"), a.UnknownReason, offline.verdict("REV-APR-001"))
	}
}

// withoutBundle: without the bundle the record is not-signed.
func (f *rvDocsSignedFlow) withoutBundle() {
	r := f.r
	r.t.Helper()

	bundle := rvEdgeRecord + ".sigstore.json"
	saved := r.read(bundle)

	r.rvRemoveFile(bundle)

	unsigned := r.checkWith(1, f.env())
	if a := unsigned.assessment(r.t, "ASM-EDGE"); !rvNotAppliedIs(a, "not-signed") {
		r.t.Errorf("without the bundle: notApplied = %v, want not-signed", a.NotApplied)
	}

	r.write(bundle, saved)
}

// otherSigner: a reviewer whom no signer of the authority matches is signer-not-listed.
func (f *rvDocsSignedFlow) otherSigner() {
	r := f.r
	r.t.Helper()

	r.rvRemoveFile(rvEdgeRecord)
	r.rvRemoveFile(rvEdgeRecord + ".sigstore.json")
	f.approveAs("bob@example.com", "approve --sign as bob")

	other := r.checkWith(1, f.env())
	if a := other.assessment(r.t, "ASM-EDGE"); !rvNotAppliedIs(a, "signer-not-listed") {
		r.t.Errorf("a reviewer that no signer matches: notApplied = %v, want signer-not-listed", a.NotApplied)
	}
}
