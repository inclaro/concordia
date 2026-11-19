package assessment_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	ws "github.com/inclaro/concordia/internal/workspace"
)

const (
	anchorSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headSHA    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	approvalAt = "assessments/approvals/security/ASM-EDGE-v1-1.yaml"
)

// protectedWorkspace is the workspace file with one protected-review authority that lists alice.
func protectedWorkspace(reviewers string) string {
	return "observation:\n  projects:\n    - name: a\n" + `assessments:
  trustAnchor: { ref: origin/main }
  authorities:
    - id: security-review
      role: reviewer
      mechanism: protected-review
      reviewers: [` + reviewers + `]
      paths: [assessments/approvals/security/**, assessments/revocations/**]
  required:
    - { id: edge, assessment: ASM-EDGE, type: security, environment: production, authority: security-review }
check:
  enforcement:
    - { pack: review-currency, mode: advisory }
`
}

// anchorRepo is a FakeGit whose anchor origin/main is an ancestor of HEAD and holds the files.
func anchorRepo(files map[string]string) *ws.FakeGit {
	return &ws.FakeGit{
		Rev:       headSHA,
		Refs:      map[string]string{"origin/main": anchorSHA},
		Ancestors: map[[2]string]bool{{anchorSHA, headSHA}: true},
		FilesAt:   map[string]map[string]string{anchorSHA: files},
	}
}

func readAnchor(t *testing.T, git ws.GitPort, ref string) *assessment.Anchor {
	t.Helper()

	return assessment.ReadAnchor(context.Background(), &assessment.AnchorSource{
		Git: git, Ref: ref, WorkspacePath: filepath.Join(t.TempDir(), "concordia.yaml"),
	})
}

func parseApprovalText(t *testing.T, file, text string) *assessment.Approval {
	t.Helper()

	a, err := assessment.ParseApproval(file, []byte(text))
	if err != nil {
		t.Fatalf("ParseApproval: %v", err)
	}

	return a
}

func verifyOne(t *testing.T, v assessment.Verifier, rec assessment.Record) assessment.VerifyResult { //nolint:gocritic // hugeParam: the Verifier contract takes the record by value.
	t.Helper()

	res, err := v.Verify(context.Background(), rec)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	return res
}

// verifyProtectedApproval verifies the approval text, parsed as the file, by the protected-review mechanism against
// an anchor that holds the files.
func verifyProtectedApproval(t *testing.T, anchorFiles map[string]string, file, text string) assessment.VerifyResult {
	t.Helper()

	git := anchorRepo(anchorFiles)
	anchor := readAnchor(t, git, "origin/main")
	approval := parseApprovalText(t, file, text)

	return verifyOne(t, &assessment.ProtectedReview{Git: git, Anchor: anchor}, assessment.ApprovalRecord(approval))
}

// verifyIsWarning tells whether the diagnostic is a warning with the code.
func verifyIsWarning(d *diag.Diagnostic, code string) bool {
	if d == nil {
		return false
	}

	return d.Code == code && d.Severity == diag.SeverityWarning
}

// verifyIsNotApplied tells whether the record was judged and is not applied for the reason.
func verifyIsNotApplied(res *assessment.VerifyResult, reason string) bool {
	if res.Applied || res.Unknown != nil {
		return false
	}

	return res.Reason == reason
}

// verifyIsUnknown tells whether the record could not be judged.
func verifyIsUnknown(res *assessment.VerifyResult) bool {
	return !res.Applied && res.Unknown != nil
}

// verifyIsUnknownFor tells whether the record could not be judged for the reason.
func verifyIsUnknownFor(res *assessment.VerifyResult, reason string) bool {
	return verifyIsUnknown(res) && res.Unknown.Reason == reason
}

// verifyIsAppliedBy tells whether the record is applied by the mechanism.
func verifyIsAppliedBy(res *assessment.VerifyResult, mechanism string) bool {
	return res.Applied && res.Mechanism == mechanism
}

// verifyIsApplied tells whether the record is applied by the mechanism and the authority.
func verifyIsApplied(res *assessment.VerifyResult, mechanism, authority string) bool {
	return verifyIsAppliedBy(res, mechanism) && res.Authority == authority
}

func wantNotApplied(t *testing.T, res *assessment.VerifyResult, reason string) {
	t.Helper()

	if !verifyIsNotApplied(res, reason) {
		t.Fatalf("result = %+v, want not applied with reason %s", res, reason)
	}

	if !verifyIsWarning(res.Diagnostic, diag.ASM006) {
		t.Errorf("diagnostic = %+v, want a CO-ASM-006 warning", res.Diagnostic)
	}
}

// verifyWriteFile writes the text to the path, creating the directories above it.
func verifyWriteFile(t *testing.T, path, text string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// verifyWriteWorkspace writes the workspace file text to a new directory and gives its path.
func verifyWriteWorkspace(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "concordia.yaml")
	verifyWriteFile(t, path, text)

	return path
}

// verifyLoadWorkspace loads the workspace file.
func verifyLoadWorkspace(t *testing.T, path string) *ws.Workspace {
	t.Helper()

	candidate, err := ws.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}

	return candidate
}

// verifyBundlePath gives the path of the signature bundle of the approval under the root.
func verifyBundlePath(root string) string {
	return filepath.Join(root, filepath.FromSlash(approvalAt)+".sigstore.json")
}

// signedWorkspace has one signed authority with the signers.
func signedWorkspace(signers string) string {
	return "observation:\n  projects:\n    - name: a\n" + `assessments:
  trustAnchor: { ref: origin/main }
  authorities:
    - id: security-review
      role: reviewer
      mechanism: signed
      signers:
` + signers
}

const keylessSigner = "        - { identity: alice@example.com, issuer: \"https://accounts.example.com\" }\n"

// fakeCosignScript is the cosign script of fakeCosign with the placeholders for the arguments file, the key
// content, and the identity.
const fakeCosignScript = `#!/bin/sh
echo "$@" >> "@ARGS@"
[ "$1" = "verify-blob" ] || exit 9
id=""; key=""
while [ $# -gt 0 ]; do
  case "$1" in
    --certificate-identity) id="$2";;
    --key) key="$2";;
  esac
  shift
done
if [ -n "$key" ]; then
  [ "$(cat "$key")" = "@KEY@" ] && exit 0
  echo "invalid signature" >&2; exit 1
fi
[ "$id" = "@IDENTITY@" ] && exit 0
echo "no matching signatures" >&2
exit 1
`

// fakeCosign writes a cosign script that accepts verify-blob only for the identity and, with a key, only for
// a key file with the content. It records its arguments in args.txt.
func fakeCosign(t *testing.T, identity, keyContent string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "cosign")
	text := strings.NewReplacer(
		"@ARGS@", filepath.Join(dir, "args.txt"), "@KEY@", keyContent, "@IDENTITY@", identity,
	).Replace(fakeCosignScript)

	if err := os.WriteFile(script, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}

	return script
}

// signedCase is the setup of a signed authority: the git, the workspace root with the record and, optionally, its
// signature bundle, and the record.
type signedCase struct {
	git  *ws.FakeGit
	root string
	rec  assessment.Record
}

// newSignedCase sets up the signers at the anchor, with the extra files, and the record under a new root.
func newSignedCase(t *testing.T, signers string, withBundle bool, extra map[string]string) *signedCase {
	t.Helper()

	files := map[string]string{"concordia.yaml": signedWorkspace(signers)}
	for k, v := range extra {
		files[k] = v
	}

	c := &signedCase{
		git:  anchorRepo(files),
		root: t.TempDir(),
		rec:  assessment.ApprovalRecord(parseApprovalText(t, approvalAt, approvalYAML)),
	}

	if withBundle {
		verifyWriteFile(t, verifyBundlePath(c.root), "{}")
	}

	return c
}

// verifier gives the signed verifier of the case, with the cosign that the COSIGN variable names.
func (c *signedCase) verifier(t *testing.T, cosign string, offline bool) *assessment.Signed {
	t.Helper()

	return &assessment.Signed{
		Git: c.git, Anchor: readAnchor(t, c.git, "origin/main"), Root: c.root, Offline: offline,
		Getenv: func(k string) string {
			if k == "COSIGN" {
				return cosign
			}

			return ""
		},
	}
}

// verify verifies the record of the case online with the cosign.
func (c *signedCase) verify(t *testing.T, cosign string) assessment.VerifyResult {
	t.Helper()

	return verifyOne(t, c.verifier(t, cosign, false), c.rec)
}

// verifyIsToolchainFailure tells whether the error is CO-WRK-014 with exit 5.
func verifyIsToolchainFailure(err error) bool {
	var failure *diag.Error
	if !errors.As(err, &failure) {
		return false
	}

	return failure.Diagnostic.Code == diag.WRK014 && failure.ExitCode == diag.Toolchain
}
