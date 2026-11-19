package exceptions_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	anchorSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headSHA      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	registerPath = "exceptions/waivers.yaml"
)

// waiverWorkspace is the workspace file at the anchor with one authority.
func waiverWorkspace(role, mechanism, members string) string {
	return "observation:\n  projects:\n    - name: a\n" + `assessments:
  trustAnchor: { ref: origin/main }
  authorities:
    - id: exception-board
      role: ` + role + `
      mechanism: ` + mechanism + `
` + members
}

const (
	reviewersAlice = "      reviewers: [alice@example.com]\n      paths: [exceptions/waivers.yaml]\n"
	signerAlice    = "      signers:\n        - { identity: alice@example.com, issuer: \"https://accounts.example.com\" }\n"
)

func anchorRepo(files map[string]string) *workspace.FakeGit {
	return &workspace.FakeGit{
		Rev:       headSHA,
		Refs:      map[string]string{"origin/main": anchorSHA},
		Ancestors: map[[2]string]bool{{anchorSHA, headSHA}: true},
		FilesAt:   map[string]map[string]string{anchorSHA: files},
	}
}

// candidateWaiver parses the scoped waiver of the register text.
func candidateWaiver(t *testing.T, text string) []exceptions.Waiver {
	t.Helper()

	list, err := exceptions.ParseWaivers(text, registerPath)
	if err != nil {
		t.Fatal(err)
	}

	return list
}

func verifier(t *testing.T, git workspace.GitPort, root, cosign string) assessment.Verifier { //nolint:ireturn // the interface is the point.
	t.Helper()

	anchor := assessment.ReadAnchor(context.Background(), &assessment.AnchorSource{
		Git: git, Ref: "origin/main", WorkspacePath: filepath.Join(root, "concordia.yaml"),
	})

	return assessment.NewAuthorities(&assessment.AuthorityOptions{
		Git: git, Anchor: anchor, Root: root,
		Getenv: func(k string) string {
			if k == "COSIGN" {
				return cosign
			}

			return ""
		},
	})
}

func verifyAll(t *testing.T, v assessment.Verifier, waivers []exceptions.Waiver) []exceptions.Verification {
	t.Helper()

	out, err := exceptions.VerifyApprovers(context.Background(), v, registerPath, waivers)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func wantNotApplied(t *testing.T, got *exceptions.Verification, reason string) {
	t.Helper()

	if got.Verified || got.Reason != reason {
		t.Fatalf("verification = %+v, want not verified with reason %s", got, reason)
	}

	if !isApproverWarning(got.Diagnostic) {
		t.Errorf("diagnostic = %+v, want a CO-EXC-009 warning that names the waiver and the approver", got.Diagnostic)
	}
}

// isApproverWarning says whether d is a CO-EXC-009 warning that names the waiver WV-9 and its approver.
func isApproverWarning(d *diag.Diagnostic) bool {
	if d == nil || d.Code != diag.EXC009 {
		return false
	}

	if d.Severity != diag.SeverityWarning {
		return false
	}

	return strings.Contains(d.Message, "alice@example.com") && strings.Contains(d.Message, "WV-9")
}

// isVerifiedVia says whether v is verified by the mechanism.
func isVerifiedVia(v *exceptions.Verification, mechanism string) bool {
	return v.Verified && v.Mechanism == mechanism
}

// Scenario: Approver not in the exception authority.
func TestApproverNotInTheExceptionAuthorityIsNotApplied(t *testing.T) {
	t.Parallel()

	register := scoped(same)
	git := anchorRepo(map[string]string{
		"concordia.yaml": waiverWorkspace("exception", "protected-review", "      reviewers: [bob@example.com]\n      paths: [exceptions/waivers.yaml]\n"),
		registerPath:     register,
	})

	got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, register))
	if len(got) != 1 {
		t.Fatalf("results = %+v", got)
	}

	wantNotApplied(t, &got[0], assessment.ReviewerNotListed)
}

// Scenario: Candidate adds its own waiver.
func TestAWaiverThatIsNotOnTheAnchorIsNotApplied(t *testing.T) {
	t.Parallel()

	git := anchorRepo(map[string]string{
		"concordia.yaml": waiverWorkspace("exception", "protected-review", reviewersAlice),
		registerPath:     "waivers: []\n",
	})

	got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, scoped(same)))
	wantNotApplied(t, &got[0], assessment.NotOnAnchor)

	if !strings.Contains(got[0].Detail, "trust anchor") {
		t.Errorf("the report says that the waiver is not on the trust anchor: %s", got[0].Detail)
	}

	// The register file itself is missing at the anchor.
	git = anchorRepo(map[string]string{"concordia.yaml": waiverWorkspace("exception", "protected-review", reviewersAlice)})

	got = verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, scoped(same)))
	wantNotApplied(t, &got[0], assessment.NotOnAnchor)
}

func TestAWaiverOnTheAnchorWithAnEqualEntryIsVerified(t *testing.T) {
	t.Parallel()

	register := scoped(same)
	git := anchorRepo(map[string]string{
		"concordia.yaml": waiverWorkspace("exception", "protected-review", reviewersAlice),
		// The layout of the file differs, the canonical entry does not.
		registerPath: "# merged\nkind: ExceptionRegister\n" + scoped(replacing(scopeFlow, "scope:\n      environment: production\n      subject: ASM-EDGE")),
	})

	got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, register))
	if !isVerifiedVia(&got[0], workspace.MechanismProtectedReview) || got[0].Authority != "exception-board" {
		t.Fatalf("verification = %+v", got[0])
	}

	if got[0].Diagnostic != nil {
		t.Fatalf("verification = %+v", got[0])
	}
}

func TestAChangedEntryDoesNotMatchItsCopyOnTheAnchor(t *testing.T) {
	t.Parallel()

	git := anchorRepo(map[string]string{
		"concordia.yaml": waiverWorkspace("exception", "protected-review", reviewersAlice),
		registerPath:     scoped(replacing("environment: production", "environment: staging")),
	})

	got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, scoped(same)))
	wantNotApplied(t, &got[0], assessment.DigestMismatch)
}

// Scenario: Review authority cannot approve a waiver.
func TestAReviewerAuthorityIsNotAnExceptionAuthority(t *testing.T) {
	t.Parallel()

	register := scoped(same)
	git := anchorRepo(map[string]string{
		"concordia.yaml": waiverWorkspace("reviewer", "protected-review", reviewersAlice),
		registerPath:     register,
	})

	got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, register))
	wantNotApplied(t, &got[0], assessment.AuthorityMismatch)
}

// Scenario: Candidate waives its own relaxation.
// Scenario: A31 a waiver for REV-GOV-001 needs a governance approver.
func TestAWaiverForTheGovernanceRuleNeedsAGovernanceApprover(t *testing.T) {
	t.Parallel()

	register := scoped(replacing("rule: REV-APR-001", "rule: REV-GOV-001"))

	for role, applied := range map[string]bool{"exception": false, "governance": true} {
		git := anchorRepo(map[string]string{
			"concordia.yaml": waiverWorkspace(role, "protected-review", reviewersAlice),
			registerPath:     register,
		})

		got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, register))
		if got[0].Verified != applied {
			t.Errorf("authority of role %s: %+v, want verified %v", role, got[0], applied)
		}

		if !applied {
			wantNotApplied(t, &got[0], assessment.AuthorityMismatch)
		}
	}

	if got := exceptions.RoleOf(&exceptions.Waiver{Rule: "REV-APR-001"}); got != workspace.RoleException {
		t.Errorf("role of another rule = %s", got)
	}
}

func TestAnUnreadableAnchorDoesNotApplyAWaiver(t *testing.T) {
	t.Parallel()

	git := anchorRepo(nil)
	git.Refs = nil

	got := verifyAll(t, verifier(t, git, t.TempDir(), ""), candidateWaiver(t, scoped(same)))
	wantNotApplied(t, &got[0], assessment.ReasonTrustAnchorUnreadable)
}

func TestFindingWaiversNeedNoVerification(t *testing.T) {
	t.Parallel()

	git := anchorRepo(map[string]string{"concordia.yaml": waiverWorkspace("exception", "protected-review", reviewersAlice)})

	w := waiver(1, "2026-12-31", idOne)
	if got := verifyAll(t, verifier(t, git, t.TempDir(), ""), []exceptions.Waiver{w}); len(got) != 0 {
		t.Errorf("results = %+v", got)
	}
}

// fakeCosign writes a cosign script that accepts verify-blob only for the identity.
func fakeCosign(t *testing.T, identity string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	script := filepath.Join(t.TempDir(), "cosign")
	text := `#!/bin/sh
[ "$1" = "verify-blob" ] || exit 9
id=""
while [ $# -gt 0 ]; do
  case "$1" in
    --certificate-identity) id="$2";;
  esac
  shift
done
[ "$id" = "` + identity + `" ] && exit 0
echo "no matching signatures" >&2
exit 1
`

	if err := os.WriteFile(script, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}

	return script
}

// Scenario: Signed entry with a matching signer.
func TestASignedWaiverWithAMatchingSignerIsApplied(t *testing.T) {
	t.Parallel()

	register := scoped(same)
	root := t.TempDir()
	bundle := filepath.Join(root, "exceptions", "signatures", "WV-9.sigstore.json")

	if err := os.MkdirAll(filepath.Dir(bundle), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(bundle, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The register is not on the anchor: the signature, not the place, proves the authority.
	git := anchorRepo(map[string]string{"concordia.yaml": waiverWorkspace("exception", "signed", signerAlice)})

	got := verifyAll(t, verifier(t, git, root, fakeCosign(t, "alice@example.com")), candidateWaiver(t, register))
	if !isVerifiedVia(&got[0], workspace.MechanismSigned) || got[0].Signer != "alice@example.com" {
		t.Fatalf("verification = %+v", got[0])
	}

	other := verifyAll(t, verifier(t, git, root, fakeCosign(t, "mallory@example.com")), candidateWaiver(t, register))
	wantNotApplied(t, &other[0], assessment.SignerNotListed)

	if err := os.Remove(bundle); err != nil {
		t.Fatal(err)
	}

	unsigned := verifyAll(t, verifier(t, git, root, fakeCosign(t, "alice@example.com")), candidateWaiver(t, register))
	wantNotApplied(t, &unsigned[0], assessment.NotSigned)
}
