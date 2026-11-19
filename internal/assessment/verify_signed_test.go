package assessment_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	ws "github.com/inclaro/concordia/internal/workspace"
)

// signedCheckCosignArguments checks the arguments that the fake cosign recorded for a keyless verification.
func signedCheckCosignArguments(t *testing.T, cosign, root string) {
	t.Helper()

	args, err := os.ReadFile(filepath.Join(filepath.Dir(cosign), "args.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"verify-blob --bundle " + verifyBundlePath(root),
		"--certificate-identity alice@example.com", "--certificate-oidc-issuer https://accounts.example.com",
	} {
		if !strings.Contains(string(args), want) {
			t.Errorf("cosign arguments %q lack %q", args, want)
		}
	}
}

// Scenario: Verified signed approval.
func TestSignedVerifiesAMatchingSigner(t *testing.T) {
	t.Parallel()

	c := newSignedCase(t, keylessSigner, true, nil)
	cosign := fakeCosign(t, "alice@example.com", "")

	res := c.verify(t, cosign)
	if !verifyIsApplied(&res, assessment.MechanismSigned, "security-review") || res.Signer != "alice@example.com" {
		t.Fatalf("result = %+v, want applied with signed", res)
	}

	signedCheckCosignArguments(t, cosign, c.root)
}

// Scenario: Signer not in the configured authority.
func TestSignedDoesNotApplyAWrongSigner(t *testing.T) {
	t.Parallel()

	c := newSignedCase(t, keylessSigner, true, nil)

	res := c.verify(t, fakeCosign(t, "someone-else@example.com", ""))
	wantNotApplied(t, &res, assessment.SignerNotListed)

	if !strings.Contains(res.Detail, "alice@example.com") {
		t.Errorf("detail = %q, want it to name the signer", res.Detail)
	}
}

func TestSignedNeedsTheBundle(t *testing.T) {
	t.Parallel()

	c := newSignedCase(t, keylessSigner, false, nil)

	res := c.verify(t, fakeCosign(t, "alice@example.com", ""))
	wantNotApplied(t, &res, assessment.NotSigned)
}

func TestSignedReviewerMustBeTheKeylessSigner(t *testing.T) {
	t.Parallel()

	signer := "        - { identity: bot@example.com, issuer: \"https://accounts.example.com\" }\n"
	c := newSignedCase(t, signer, true, nil)

	res := c.verify(t, fakeCosign(t, "bot@example.com", ""))
	wantNotApplied(t, &res, assessment.SignerNotListed)
}

// Scenario: cosign missing.
func TestSignedWithoutCosignStopsWithExit5(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	c := newSignedCase(t, keylessSigner, true, nil)

	_, err := c.verifier(t, "", false).Verify(context.Background(), c.rec)
	if !verifyIsToolchainFailure(err) {
		t.Fatalf("err = %v, want CO-WRK-014 with exit 5", err)
	}
}

// Scenario: Keyless verification offline.
// cosign 3 verifies every signature, keyed or keyless, against the Sigstore trusted root, which it fetches over the
// network. With --offline no signer can be verified: the result is unknown (verification-unavailable), never a pass
// and never a network attempt.
func TestSignedVerificationOfflineIsUnknown(t *testing.T) {
	t.Parallel()

	for name, signers := range map[string]string{
		"keyless": keylessSigner,
		"key":     "        - { publicKey: keys/review.pub }\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newSignedCase(t, signers, true, map[string]string{"keys/review.pub": "trusted-key"})

			// cosign is not even needed: the answer does not depend on it.
			res, err := c.verifier(t, "/nonexistent/cosign", true).Verify(context.Background(), c.rec)
			if err != nil {
				t.Fatal(err)
			}

			if !verifyIsUnknownFor(&res, assessment.ReasonVerificationUnavailable) {
				t.Fatalf("result = %+v, want unknown with verification-unavailable", res)
			}

			if !verifyIsWarning(res.Diagnostic, diag.ASM011) {
				t.Errorf("diagnostic = %+v, want a CO-ASM-011 warning", res.Diagnostic)
			}
		})
	}
}

func TestSignedKeyIsReadFromTheAnchorNotTheWorkingTree(t *testing.T) {
	t.Parallel()

	signers := "        - { publicKey: keys/review.pub }\n"
	c := newSignedCase(t, signers, true, map[string]string{"keys/review.pub": "trusted-key"})
	cosign := fakeCosign(t, "", "trusted-key")

	// The working tree has another key under the same name: it is not used.
	verifyWriteFile(t, filepath.Join(c.root, "keys", "review.pub"), "attacker-key")

	res := c.verify(t, cosign)
	if !res.Applied || res.Signer != "keys/review.pub" {
		t.Fatalf("result = %+v, want applied by the anchor key", res)
	}

	// A key that the anchor does not have gives no verified signer.
	c = newSignedCase(t, signers, true, nil)

	res = c.verify(t, cosign)
	wantNotApplied(t, &res, assessment.SignerNotListed)
}

// verifyMixedAuthorities is a workspace with a protected-review authority by default and a signed authority.
const verifyMixedAuthorities = "observation:\n  projects:\n    - name: a\n" + `assessments:
  trustAnchor: { ref: origin/main }
  authorities:
    - id: security-review
      role: reviewer
      reviewers: [alice@example.com]
      paths: [assessments/approvals/security/**, assessments/revocations/**]
    - id: signers
      role: reviewer
      mechanism: signed
      signers:
` + keylessSigner

func TestAuthoritiesPicksTheMechanismFromTheAnchor(t *testing.T) {
	t.Parallel()

	git := anchorRepo(map[string]string{"concordia.yaml": verifyMixedAuthorities, approvalAt: approvalYAML})
	anchor := readAnchor(t, git, "origin/main")

	root := t.TempDir()
	verifyWriteFile(t, verifyBundlePath(root), "{}")

	auth := assessment.NewAuthorities(&assessment.AuthorityOptions{Git: git, Anchor: anchor, Root: root, Offline: true})

	// The default mechanism is protected-review.
	approval := parseApprovalText(t, approvalAt, approvalYAML)

	res := verifyOne(t, auth, assessment.ApprovalRecord(approval))
	if !verifyIsAppliedBy(&res, assessment.MechanismProtectedReview) {
		t.Errorf("protected-review result = %+v", res)
	}

	// The record names a signed authority: keyless offline is unknown.
	signedApproval := parseApprovalText(t, approvalAt, strings.Replace(approvalYAML, "authority: security-review", "authority: signers", 1))

	res = verifyOne(t, auth, assessment.ApprovalRecord(signedApproval))
	if !verifyIsUnknown(&res) || res.Mechanism != assessment.MechanismSigned {
		t.Errorf("signed result = %+v", res)
	}

	// A role other than the one the record needs is not applied.
	rec := assessment.ApprovalRecord(approval)
	rec.Role = ws.RoleException

	res = verifyOne(t, auth, rec)
	wantNotApplied(t, &res, assessment.AuthorityMismatch)
}
