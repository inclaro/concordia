package assessment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// Signed verifies a record by its signature. The record is signed with `cosign sign-blob --bundle` over its
// canonical JSON (not over the file bytes), and the sigstore bundle sits beside the record as
// `<record>.sigstore.json`. Verification runs `cosign verify-blob` through internal/cosign for each signer of
// the authority until one verifies. The signers come from the trust anchor, and so does the file of a
// `publicKey` signer, so a candidate cannot add itself as a signer.
//
// A signed record may travel in the candidate change: the signature, not the location, proves the authority.
type Signed struct {
	Git     workspace.GitPort
	Anchor  *Anchor
	Getenv  func(string) string
	Root    string
	Offline bool
}

// Verify implements Verifier. It gives a CO-WRK-014 error (exit 5) when a signature has to be verified and
// cosign is not installed.
func (s *Signed) Verify(ctx context.Context, rec Record) (VerifyResult, error) { //nolint:gocritic // hugeParam: the Verifier contract takes the record by value.
	if !s.Anchor.Readable() {
		return anchorUnknown(&rec, s.Anchor), nil
	}

	auth, bad := lookup(s.Anchor, &rec)
	if bad != nil {
		return *bad, nil
	}

	if auth.Mechanism != MechanismSigned {
		return notApplied(&rec, &auth, AuthorityMismatch,
			fmt.Sprintf("authority '%s' uses the mechanism '%s', not '%s'", auth.ID, auth.Mechanism, MechanismSigned)), nil
	}

	bundlePath, ok := s.bundlePath(&rec)
	if !ok {
		return notApplied(&rec, &auth, NotSigned, "the signature bundle path is outside the workspace"), nil
	}

	if _, err := os.Stat(bundlePath); err != nil {
		return notApplied(&rec, &auth, NotSigned, fmt.Sprintf(
			"no signature bundle %s; sign the record with `concordia assessment approve --sign`", filepath.ToSlash(s.relative(bundlePath)))), nil
	}

	if len(auth.Signers) == 0 {
		return notApplied(&rec, &auth, SignerNotListed, fmt.Sprintf("authority '%s' lists no signer", auth.ID)), nil
	}

	// Offline: only the signers that need no sigstore trust root can be tried. Decide before looking for cosign
	// so that a missing cosign does not hide an unavailable verification.
	signers, unavailable := s.usableSigners(&auth)
	if len(signers) == 0 {
		return s.unavailable(&rec, &auth, unavailable), nil
	}

	exe, err := cosign.Path(s.getenv)
	if err != nil {
		return VerifyResult{}, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	blob, cleanup, err := writeTemp("record-*.json", rec.Canonical)
	if err != nil {
		return VerifyResult{}, diag.Fail(diag.WRK014, diag.Detail("cannot write the record for cosign: "+err.Error()))
	}
	defer cleanup()

	var failures []string

	for _, signer := range signers {
		opts, label, cleanKey, err := s.optionsOf(ctx, &signer)
		if err != nil {
			failures = append(failures, label+": "+err.Error())

			continue
		}

		verr := cosign.VerifyBlob(ctx, exe, blob, bundlePath, opts)

		cleanKey()

		if verr == nil {
			if signer.PublicKey == "" && signer.Identity != rec.Reviewer.Identity {
				failures = append(failures, fmt.Sprintf("%s: the record names reviewer '%s'", label, rec.Reviewer.Identity))

				continue
			}

			return VerifyResult{Applied: true, Mechanism: MechanismSigned, Authority: auth.ID, Signer: label}, nil
		}

		failures = append(failures, label+": "+verr.Error())
	}

	if len(unavailable) > 0 && len(failures) == 0 {
		return s.unavailable(&rec, &auth, unavailable), nil
	}

	return notApplied(&rec, &auth, SignerNotListed, fmt.Sprintf(
		"no signer of authority '%s' signed it (%s)", auth.ID, strings.Join(failures, "; "))), nil
}

// usableSigners splits the signers into those that can be verified here and those that cannot. With --offline no
// signer can: cosign 3 verifies every signature, keyed or keyless, against the Sigstore trusted root, which it fetches
// over the network.
func (s *Signed) usableSigners(auth *workspace.Authority) (usable []workspace.Signer, unavailable []string) {
	for _, signer := range auth.Signers {
		if s.Offline {
			unavailable = append(unavailable, signerLabel(&signer))

			continue
		}

		usable = append(usable, signer)
	}

	return usable, unavailable
}

// unavailable builds the `unknown` result of keyless verification that cannot run.
func (s *Signed) unavailable(rec *Record, auth *workspace.Authority, identities []string) VerifyResult {
	detail := fmt.Sprintf("verifying the signature of the signers %s needs network access (cosign fetches the Sigstore "+
		"trusted root) and --offline is set; verify without --offline, or use a protected-review authority for offline "+
		"runs", strings.Join(identities, ", "))
	d := diag.New(diag.ASM011, detail2(rec, detail), rec.source())

	return VerifyResult{
		Mechanism: MechanismSigned, Authority: auth.ID, Detail: detail, Diagnostic: &d,
		Unknown: &VerifyUnknown{Reason: ReasonVerificationUnavailable, Detail: detail},
	}
}

func detail2(rec *Record, detail string) diag.Params { return diag.Detail(rec.label() + ": " + detail) }

func (s *Signed) getenv(name string) string {
	if s.Getenv == nil {
		return ""
	}

	return s.Getenv(name)
}

// bundlePath gives the OS path of the sigstore bundle of the record, confined below the workspace root.
func (s *Signed) bundlePath(rec *Record) (string, bool) {
	rel := rec.Bundle
	if rel == "" {
		rel = cosign.BundlePath(rec.Path)
	}

	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", false
	}

	return filepath.Join(s.Root, filepath.FromSlash(rel)), true
}

func (s *Signed) relative(abs string) string {
	if rel, err := filepath.Rel(s.Root, abs); err == nil {
		return rel
	}

	return abs
}

// optionsOf gives the cosign options of one signer, a label for messages, and a cleanup of the key file. A
// `publicKey` that is a path is read from the trust anchor, never from the working tree. A key reference with
// a scheme (a KMS URI) is passed as is.
func (s *Signed) optionsOf(ctx context.Context, signer *workspace.Signer) (cosign.VerifyOptions, string, func(), error) { //nolint:gocritic // unnamedResult: the four results are told apart by their types and the comment above.
	noop := func() {}

	if signer.PublicKey == "" {
		return cosign.VerifyOptions{Identity: signer.Identity, Issuer: signer.Issuer}, signer.Identity, noop, nil
	}

	label := signer.PublicKey
	if strings.Contains(signer.PublicKey, "://") {
		return cosign.VerifyOptions{Key: signer.PublicKey}, label, noop, nil
	}

	content, found, err := s.Git.ShowFile(ctx, s.Anchor.Revision, filepath.ToSlash(signer.PublicKey))

	switch {
	case err != nil:
		return cosign.VerifyOptions{}, label, noop, fmt.Errorf("cannot read the key at the trust anchor: %w", err)
	case !found:
		return cosign.VerifyOptions{}, label, noop, errors.New("the key file is not at the trust anchor")
	}

	path, cleanup, err := writeTemp("key-*.pub", content)
	if err != nil {
		return cosign.VerifyOptions{}, label, noop, err
	}

	return cosign.VerifyOptions{Key: path}, label, cleanup, nil
}

// writeTemp writes the content to a temporary file and gives its path and the function that removes it.
func writeTemp(pattern string, content []byte) (string, func(), error) { //nolint:gocritic // unnamedResult: path, cleanup, error.
	tmp, err := os.CreateTemp("", "concordia-"+pattern)
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary file: %w", err)
	}

	cleanup := func() { _ = os.Remove(tmp.Name()) }

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()

		cleanup()

		return "", func() {}, fmt.Errorf("write temporary file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		cleanup()

		return "", func() {}, fmt.Errorf("close temporary file: %w", err)
	}

	return tmp.Name(), cleanup, nil
}

// signerLabel names a signer in messages: its public key reference, else its identity.
func signerLabel(signer *workspace.Signer) string {
	if signer.PublicKey != "" {
		return signer.PublicKey
	}

	return signer.Identity
}
