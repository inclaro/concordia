package assessment_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	ws "github.com/inclaro/concordia/internal/workspace"
)

// Scenario: Record on the trust anchor.
func TestProtectedReviewVerifiesARecordOnTheAnchor(t *testing.T) {
	t.Parallel()

	res := verifyProtectedApproval(t, map[string]string{
		"concordia.yaml": protectedWorkspace("alice@example.com, bob@example.com"),
		approvalAt:       approvalYAML,
	}, approvalAt, approvalYAML)

	if !verifyIsApplied(&res, assessment.MechanismProtectedReview, "security-review") || res.Diagnostic != nil {
		t.Fatalf("result = %+v, want applied with protected-review", res)
	}
}

// Scenario: Record only in the candidate change.
func TestProtectedReviewDoesNotApplyARecordOnlyInTheCandidate(t *testing.T) {
	t.Parallel()

	res := verifyProtectedApproval(t, map[string]string{"concordia.yaml": protectedWorkspace("alice@example.com")},
		approvalAt, approvalYAML)
	wantNotApplied(t, &res, assessment.NotOnAnchor)

	if !strings.Contains(res.Detail, "trust anchor") {
		t.Errorf("detail = %q, want it to say that the record is not on the trust anchor", res.Detail)
	}
}

func TestProtectedReviewDoesNotApplyARecordThatTheAnchorDoesNotBack(t *testing.T) {
	t.Parallel()

	const other = "assessments/approvals/other/ASM-EDGE-v1-1.yaml"

	alice := protectedWorkspace("alice@example.com")

	for _, tc := range []struct {
		name        string
		anchorFiles map[string]string
		file, text  string
		reason      string
	}{
		{
			name:        "reviewer not listed",
			anchorFiles: map[string]string{"concordia.yaml": protectedWorkspace("bob@example.com"), approvalAt: approvalYAML},
			file:        approvalAt, text: approvalYAML, reason: assessment.ReviewerNotListed,
		},
		{
			name:        "path not allowed",
			anchorFiles: map[string]string{"concordia.yaml": alice, other: approvalYAML},
			file:        other, text: approvalYAML, reason: assessment.PathNotAllowed,
		},
		{
			name: "digest must match the anchor copy",
			anchorFiles: map[string]string{
				"concordia.yaml": alice, approvalAt: strings.Replace(approvalYAML, "decision: approved", "decision: rejected", 1),
			},
			file: approvalAt, text: approvalYAML, reason: assessment.DigestMismatch,
		},
		{
			name:        "unknown authority",
			anchorFiles: map[string]string{"concordia.yaml": alice, approvalAt: approvalYAML},
			file:        approvalAt, reason: assessment.AuthorityNotConfigured,
			text: strings.Replace(approvalYAML, "authority: security-review", "authority: nobody", 1),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := verifyProtectedApproval(t, tc.anchorFiles, tc.file, tc.text)
			wantNotApplied(t, &res, tc.reason)
		})
	}
}

// verifyUnreadableAnchors are the ways that a trust anchor cannot be read, each building the git and the ref.
func verifyUnreadableAnchors(files map[string]string) map[string]func() (ws.GitPort, string) {
	return map[string]func() (ws.GitPort, string){
		"ref missing in a shallow clone": func() (ws.GitPort, string) {
			g := anchorRepo(files)
			g.Refs = map[string]string{}

			return g, "origin/main"
		},
		"not an ancestor of HEAD": func() (ws.GitPort, string) {
			g := anchorRepo(files)
			g.Ancestors = nil

			return g, "origin/main"
		},
		"ancestry cannot be computed": func() (ws.GitPort, string) {
			g := anchorRepo(files)
			g.AncestorErr = errors.New("fatal: bad object")

			return g, "origin/main"
		},
		"no anchor configured": func() (ws.GitPort, string) { return anchorRepo(files), "" },
		"workspace file not at the anchor": func() (ws.GitPort, string) {
			return anchorRepo(map[string]string{approvalAt: approvalYAML}), "origin/main"
		},
	}
}

// Scenario: Trust anchor cannot be read.
func TestAnUnreadableAnchorGivesUnknown(t *testing.T) {
	t.Parallel()

	files := map[string]string{"concordia.yaml": protectedWorkspace("alice@example.com"), approvalAt: approvalYAML}
	approval := parseApprovalText(t, approvalAt, approvalYAML)

	for name, build := range verifyUnreadableAnchors(files) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			git, ref := build()
			anchor := readAnchor(t, git, ref)

			if anchor.Readable() {
				t.Fatal("anchor is readable")
			}

			res := verifyOne(t, &assessment.ProtectedReview{Git: git, Anchor: anchor}, assessment.ApprovalRecord(approval))
			if !verifyIsUnknownFor(&res, assessment.ReasonTrustAnchorUnreadable) {
				t.Fatalf("result = %+v, want unknown with trust-anchor-unreadable", res)
			}

			if res.Diagnostic == nil || res.Diagnostic.Code != diag.ASM007 {
				t.Errorf("diagnostic = %+v, want CO-ASM-007", res.Diagnostic)
			}
		})
	}
}

// verifyNamesTheRefAndTheFix tells whether the diagnostic names the anchor ref and says how to fetch it.
func verifyNamesTheRefAndTheFix(d *diag.Diagnostic) bool {
	if d == nil {
		return false
	}

	return strings.Contains(d.Message, "origin/main") && strings.Contains(d.Suggestion, "git fetch")
}

func TestTheUnreadableAnchorDiagnosticNamesTheRefAndTheFix(t *testing.T) {
	t.Parallel()

	git := anchorRepo(nil)
	git.Refs = map[string]string{}
	anchor := readAnchor(t, git, "origin/main")

	if !verifyNamesTheRefAndTheFix(anchor.Diagnostic) {
		t.Errorf("diagnostic = %+v", anchor.Diagnostic)
	}
}

// Scenario: Candidate adds itself as a reviewer.
func TestACandidateCannotAddItselfAsReviewer(t *testing.T) {
	t.Parallel()

	// The anchor lists only bob. The candidate working tree lists mallory and carries mallory's approval, which
	// is also in the candidate HEAD but not on the anchor.
	const mallory = "mallory@example.com"

	record := strings.Replace(approvalYAML, "alice@example.com", mallory, 1)
	bob := map[string]string{"concordia.yaml": protectedWorkspace("bob@example.com")}

	res := verifyProtectedApproval(t, bob, approvalAt, record)
	wantNotApplied(t, &res, assessment.NotOnAnchor)

	// Even when the approval is already on the anchor, the reviewer list of the anchor decides.
	onAnchor := map[string]string{"concordia.yaml": bob["concordia.yaml"], approvalAt: record}

	res = verifyProtectedApproval(t, onAnchor, approvalAt, record)
	wantNotApplied(t, &res, assessment.ReviewerNotListed)

	// The candidate working tree is a governance change against the anchor.
	anchor := readAnchor(t, anchorRepo(onAnchor), "origin/main")
	candidate := verifyLoadWorkspace(t, verifyWriteWorkspace(t, protectedWorkspace("bob@example.com, "+mallory)))

	diffs := anchor.Differences(candidate)
	if len(diffs) != 1 || diffs[0].Item != "authority security-review" {
		t.Errorf("differences = %+v, want the changed authority", diffs)
	}
}

// verifyAnchorIsAt tells whether the anchor is readable and its revision and HEAD are the revision.
func verifyAnchorIsAt(anchor *assessment.Anchor, revision string) bool {
	if !anchor.Readable() {
		return false
	}

	return anchor.Revision == revision && anchor.Head == revision
}

// Scenario: Candidate equals trust anchor.
func TestRunningOnTheAnchorReportsNoGovernanceChange(t *testing.T) {
	t.Parallel()

	text := protectedWorkspace("alice@example.com")
	git := anchorRepo(map[string]string{"concordia.yaml": text})
	git.Rev = anchorSHA // HEAD is the anchor itself.

	path := verifyWriteWorkspace(t, text)

	anchor := assessment.ReadAnchor(context.Background(), &assessment.AnchorSource{
		Git: git, Ref: "origin/main", WorkspacePath: path,
	})
	if !verifyAnchorIsAt(anchor, anchorSHA) {
		t.Fatalf("anchor = %+v", anchor)
	}

	if diffs := anchor.Differences(verifyLoadWorkspace(t, path)); len(diffs) != 0 {
		t.Errorf("differences = %+v, want none", diffs)
	}

	if got := anchor.Required(); len(got) != 1 || got[0].ID != "edge" {
		t.Errorf("required = %+v", got)
	}

	if len(anchor.Enforcement) != 1 || anchor.Enforcement[0].Pack != "review-currency" {
		t.Errorf("enforcement = %+v", anchor.Enforcement)
	}
}

func TestTheAnchorReportsRelaxedEnforcementAndRequiredChanges(t *testing.T) {
	t.Parallel()

	git := anchorRepo(map[string]string{"concordia.yaml": protectedWorkspace("alice@example.com")})
	anchor := readAnchor(t, git, "origin/main")

	changed := strings.Replace(protectedWorkspace("alice@example.com"), "mode: advisory", "mode: shadow", 1)
	changed = strings.Replace(changed, "id: edge,", "id: other,", 1)

	diffs := anchor.Differences(verifyLoadWorkspace(t, verifyWriteWorkspace(t, changed)))
	items := make([]string, 0, len(diffs))

	for _, d := range diffs {
		items = append(items, d.Item)
	}

	if got := strings.Join(items, ","); got != "enforcement,required edge,required other" {
		t.Errorf("differences = %s", got)
	}
}

// verifySetOf verifies the set against the anchor of the git with the default authorities.
func verifySetOf(t *testing.T, git ws.GitPort, set *assessment.Set) assessment.Verified {
	t.Helper()

	anchor := readAnchor(t, git, "origin/main")

	got, err := assessment.NewAuthorities(&assessment.AuthorityOptions{Git: git, Anchor: anchor, Root: t.TempDir()}).
		VerifySet(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// verifiedKeepsOnlyTheApproval tells whether the one approval was kept, the revocation was dropped, both records
// have a result, and one diagnostic says why.
func verifiedKeepsOnlyTheApproval(got *assessment.Verified) bool {
	if len(got.Approvals) != 1 || len(got.Revocations) != 0 {
		return false
	}

	if len(got.Results) != 2 || len(got.Diagnostics) != 1 {
		return false
	}

	return got.Unknown == nil
}

// verifyIsRejectedRevocation tells whether the result is a revocation that is not applied because its reviewer is
// not listed.
func verifyIsRejectedRevocation(r *assessment.Verification) bool {
	if r.Kind != "revocation" || r.Applied {
		return false
	}

	return r.Reason == assessment.ReviewerNotListed
}

func TestVerifySetKeepsOnlyVerifiedRecords(t *testing.T) {
	t.Parallel()

	revocation := strings.Replace(revocationYAML, "alice@example.com", "mallory@example.com", 1)
	revPath := "assessments/revocations/ASM-EDGE-v1-1.yaml"
	git := anchorRepo(map[string]string{
		"concordia.yaml": protectedWorkspace("alice@example.com"),
		approvalAt:       approvalYAML,
		revPath:          revocation,
	})

	rev, err := assessment.ParseRevocation(revPath, []byte(revocation))
	if err != nil {
		t.Fatal(err)
	}

	got := verifySetOf(t, git, &assessment.Set{
		Approvals:   []*assessment.Approval{parseApprovalText(t, approvalAt, approvalYAML)},
		Revocations: []*assessment.Revocation{rev},
	})

	if !verifiedKeepsOnlyTheApproval(&got) {
		t.Fatalf("verified = %+v", got)
	}

	if r := &got.Results[1]; !verifyIsRejectedRevocation(r) {
		t.Errorf("revocation result = %+v", r)
	}
}

// verifiedIsUnreadable tells whether no approval was kept and the unreadable anchor is the one reason and the one
// diagnostic.
func verifiedIsUnreadable(got *assessment.Verified) bool {
	if len(got.Approvals) != 0 || len(got.Diagnostics) != 1 {
		return false
	}

	return got.Unknown != nil && got.Unknown.Reason == assessment.ReasonTrustAnchorUnreadable
}

func TestVerifySetWithAnUnreadableAnchorIsUnknownWithOneDiagnostic(t *testing.T) {
	t.Parallel()

	git := anchorRepo(nil)
	git.Refs = map[string]string{}

	got := verifySetOf(t, git, &assessment.Set{
		Approvals:   []*assessment.Approval{parseApprovalText(t, approvalAt, approvalYAML)},
		Revocations: []*assessment.Revocation{},
	})

	if !verifiedIsUnreadable(&got) {
		t.Fatalf("verified = %+v", got)
	}
}
