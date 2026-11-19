// Package cosign runs the cosign executable to sign and to verify a blob. It is the one place that knows the
// cosign command line: `concordia export bundle --sign`, `concordia export verify --verify-signature`,
// `concordia assessment approve --sign`, and the verification of signed approval records all use it.
//
// The package starts cosign through internal/procexec and never uses a shell. Verification runs
// `cosign verify-blob --bundle <bundle> [--key K | --certificate-identity I --certificate-oidc-issuer S]
// [--trusted-root R] <blob>`; the signed in-toto statements of internal/assurance/statement use
// `cosign verify-blob-attestation` (VerifyBlobAttestation) with the same signer options. With cosign 3 (the pinned version), signing writes to the public transparency log and
// verification fetches the Sigstore trusted root, so both need network access unless a trusted root file is given.
package cosign

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
)

const (
	// Timeout is the time that one cosign call may take.
	Timeout = 5 * time.Minute

	// stderrTail is the number of trailing characters of the cosign error output that a diagnostic keeps.
	stderrTail = 2000

	// BundleSuffix is appended to the path of a signed blob to give the path of its sigstore bundle.
	BundleSuffix = ".sigstore.json"

	// flagBundle is the flag that gives the sigstore bundle path.
	flagBundle = "--bundle"

	// EnvVar names the environment variable that overrides the cosign executable.
	EnvVar = "COSIGN"
)

// Path gives the cosign executable: $COSIGN, else the one on PATH. getenv reads the environment. It gives a
// CO-WRK-014 error (exit 5) when cosign is not found.
func Path(getenv func(string) string) (string, error) {
	if p := getenv(EnvVar); p != "" {
		return p, nil
	}

	p, err := exec.LookPath("cosign")
	if err != nil {
		return "", diag.Fail(diag.WRK014, diag.Detail("cosign was not found on PATH"))
	}

	return p, nil
}

// BundlePath gives the path of the sigstore bundle of a signed blob.
func BundlePath(blob string) string { return blob + BundleSuffix }

// SignBlob signs the file blob with `cosign sign-blob --yes --bundle` and gives the path of the bundle, which is
// BundlePath(blob). key is a cosign key reference; empty means keyless signing. Failures are CO-REP-003 errors
// (exit 5).
func SignBlob(ctx context.Context, cosign, blob, key string) (string, error) {
	bundle := BundlePath(blob)

	if err := SignBlobTo(ctx, cosign, blob, bundle, key); err != nil {
		return "", err
	}

	return bundle, nil
}

// SignBlobTo is SignBlob with the bundle written to the given path. A signed record uses it: the blob is the
// canonical JSON of the record in a temporary file, and the bundle goes beside the record file.
func SignBlobTo(ctx context.Context, cosign, blob, bundle, key string) error {
	args := []string{"sign-blob", "--yes", flagBundle, bundle}
	if key != "" {
		args = append(args, "--key", key)
	}

	args = append(args, blob)

	res, err := procexec.Run(ctx, cosign, args, procexec.Options{Timeout: Timeout})

	var spawn *procexec.SpawnError

	switch {
	case errors.As(err, &spawn):
		return diag.Fail(diag.REP003, diag.Detail(spawn.Error()))
	case err != nil:
		return diag.Fail(diag.REP003, diag.Detail(err.Error()))
	case res.TimedOut:
		return diag.Fail(diag.REP003, diag.Detail("cosign timed out after "+Timeout.String()))
	case res.Code != 0:
		return diag.Fail(diag.REP003, diag.Detail(lastChars(res.Stderr, stderrTail)))
	}

	return nil
}

// VerifyOptions say which signer a signature must come from. Key is a cosign public key reference. Identity and
// Issuer give the certificate identity and the OIDC issuer of a keyless signature. All empty lets cosign decide
// (it then rejects a keyless signature, because cosign needs an identity to match).
type VerifyOptions struct {
	Key      string
	Identity string
	Issuer   string
	// TrustedRoot is a Sigstore trusted root file. cosign 3 checks every signature (keyed or keyless) against the
	// trusted root, which it fetches over the network unless this file is given; with it, verification runs offline.
	TrustedRoot string
}

// Keyless says whether the options verify a keyless signature.
func (o VerifyOptions) Keyless() bool { return o.Key == "" }

// ErrorKind classifies a failed verification.
type ErrorKind string

// The kinds of verification failure.
const (
	// KindMissingBundle: the bundle file does not exist.
	KindMissingBundle ErrorKind = "missing-bundle"
	// KindCannotRun: cosign could not start or timed out.
	KindCannotRun ErrorKind = "cannot-run"
	// KindNotVerified: cosign ran and rejected the signature.
	KindNotVerified ErrorKind = "not-verified"
)

// VerifyError is a verification that did not succeed. Detail is the sentence of the CO-REP-004 diagnostic.
type VerifyError struct {
	Kind   ErrorKind
	Detail string
}

// Error gives the detail.
func (f *VerifyError) Error() string { return f.Detail }

// Diagnostic gives the CO-REP-004 error (exit 2) of the failure.
func (f *VerifyError) Diagnostic() *diag.Error { return diag.Fail(diag.REP004, diag.Detail(f.Detail)) }

// VerifyBlob runs `cosign verify-blob` for the file blob and its sigstore bundle. It gives nil when cosign
// accepts the signature and a *VerifyError otherwise.
func VerifyBlob(ctx context.Context, cosign, blob, bundle string, opts VerifyOptions) error {
	if _, err := os.Stat(bundle); err != nil {
		return &VerifyError{Kind: KindMissingBundle, Detail: "signature file " + bundle + " not found"}
	}

	args := append([]string{"verify-blob", flagBundle, bundle}, signerArgs(opts)...)
	args = append(args, blob)

	return runVerify(ctx, cosign, args)
}

// VerifyBlobAttestation runs `cosign verify-blob-attestation --bundle <bundle> --type <predicateType>
// --check-claims=false` for the sigstore bundle of an in-toto attestation. It gives nil when cosign accepts the
// signature and the predicate type, and a *VerifyError otherwise.
//
// cosign checks the signature, the signer, and the predicate type only. It does not compare the subject digest:
// the caller compares it (design D5), because the subject of an attestation is often an image digest or a commit,
// not a file that cosign could hash.
func VerifyBlobAttestation(ctx context.Context, cosign, bundle, predicateType string, opts VerifyOptions) error {
	if _, err := os.Stat(bundle); err != nil {
		return &VerifyError{Kind: KindMissingBundle, Detail: "signature file " + bundle + " not found"}
	}

	args := append([]string{"verify-blob-attestation", flagBundle, bundle, "--type", predicateType, "--check-claims=false"}, signerArgs(opts)...)

	return runVerify(ctx, cosign, args)
}

// signerArgs are the flags that say who must have signed and which trusted root to use.
func signerArgs(opts VerifyOptions) []string {
	args := make([]string, 0, 8)

	if opts.Key != "" {
		args = append(args, "--key", opts.Key)
	}

	if opts.Identity != "" {
		args = append(args, "--certificate-identity", opts.Identity)
	}

	if opts.Issuer != "" {
		args = append(args, "--certificate-oidc-issuer", opts.Issuer)
	}

	if opts.TrustedRoot != "" {
		args = append(args, "--trusted-root", opts.TrustedRoot)
	}

	return args
}

// runVerify starts cosign with a verification command line and classifies the outcome.
func runVerify(ctx context.Context, cosign string, args []string) error {
	res, err := procexec.Run(ctx, cosign, args, procexec.Options{Timeout: Timeout})

	switch {
	case err != nil:
		return &VerifyError{Kind: KindCannotRun, Detail: "cosign could not run: " + err.Error()}
	case res.TimedOut:
		return &VerifyError{Kind: KindCannotRun, Detail: "cosign timed out after " + Timeout.String()}
	case res.Code != 0:
		return &VerifyError{Kind: KindNotVerified, Detail: "signature not verified: " + lastChars(res.Stderr, stderrTail)}
	}

	return nil
}

func lastChars(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[len(s)-n:]
	}

	return s
}
