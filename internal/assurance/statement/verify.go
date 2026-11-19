package statement

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// Signature is how far the signature of a statement is verified.
type Signature string

// Signature states.
const (
	// SignatureNone: the statement carries no signature that cosign can verify (a bare statement or a DSSE envelope
	// without a Sigstore bundle).
	SignatureNone Signature = "none"
	// SignatureNoProducer: the statement is signed, but the workspace declares no producer to verify it with.
	SignatureNoProducer Signature = "no-producer"
	// SignatureUnavailable: verification could not run because of --offline without a trusted root file. cosign was
	// not started.
	SignatureUnavailable Signature = "unavailable"
	// SignatureFailed: cosign ran and no declared producer verified the signature (also: the statement was changed
	// after signing).
	SignatureFailed Signature = "failed"
	// SignatureVerified: a declared producer verified the signature.
	SignatureVerified Signature = "verified"
)

// SubjectCheck says whether the subject digest was compared with the digest of the artifact or file it names.
type SubjectCheck string

// Subject check states.
const (
	// SubjectUnchecked: no digest was given to compare. Provenance statements are like this: they name an image
	// digest, and the lineage evaluator links them to an occurrence by that digest.
	SubjectUnchecked SubjectCheck = "unchecked"
	SubjectMatches   SubjectCheck = "matches"
	SubjectDiffers   SubjectCheck = "differs"
)

// Verification is the outcome of verifying one statement: the checks that were made and what they found.
type Verification struct {
	Signature Signature
	// ProducerID is the id of the declared producer whose signature verified, when Signature is SignatureVerified.
	ProducerID string
	Subject    SubjectCheck
	// Failure is the reason of a failed signature, for the diagnostic. It is empty otherwise.
	Failure string
}

// Diagnostics gives the CO-PRV-009 warnings of the verification of the file at path: one for a failed signature and
// one for a subject digest that differs.
func (v Verification) Diagnostics(path string) []diag.Diagnostic {
	var out []diag.Diagnostic

	source := diag.WithSource(diag.Source{URI: path})

	if v.Signature == SignatureFailed {
		out = append(out, diag.New(diag.PRV009, diag.Detail(path+": "+v.Failure), source))
	}

	if v.Subject == SubjectDiffers {
		out = append(out, diag.New(diag.PRV009, diag.Detail(path+": the subject digest does not match the digest of the artifact it names"), source))
	}

	return out
}

// Verifier verifies statements with cosign. It finds cosign once, and only when a verification needs it. A Verifier
// is safe for concurrent use.
type Verifier struct {
	Getenv func(string) string
	exe    string
	mu     sync.Mutex
}

// VerifyInput is what one verification needs.
type VerifyInput struct {
	Expected    *Digest
	Path        string
	Root        string
	TrustedRoot string
	Loaded      Loaded
	Producers   []workspace.Producer
	Offline     bool
}

// Verify checks the subject digest and the signature of a statement against the declared producers.
//
// --offline is decided before cosign is looked for (design D24): without a trusted root file the signature stays
// unverified (SignatureUnavailable) whether cosign exists or not. Only a verification that has to run and cannot
// find cosign gives an error, the CO-WRK-014 error with exit code 5.
//
// The producers are tried one by one; those trusted for the claim kind of the statement come first, then the others
// by id, so that a statement from a producer that may not attest this kind is still verified (and reported as
// not trusted for the claim). When none verifies, the signature failed: cosign cannot tell a changed statement from
// the signature of an undeclared identity, so the two look the same.
func (v *Verifier) Verify(ctx context.Context, input *VerifyInput) (Verification, error) {
	out := Verification{Signature: SignatureNone, Subject: SubjectUnchecked}

	if input.Expected != nil {
		out.Subject = SubjectDiffers
		if input.Loaded.Statement.Names(*input.Expected) {
			out.Subject = SubjectMatches
		}
	}

	if input.Loaded.Form != FormBundle {
		return out, nil
	}

	switch {
	case len(input.Producers) == 0:
		out.Signature = SignatureNoProducer

		return out, nil
	case input.Offline && input.TrustedRoot == "":
		out.Signature = SignatureUnavailable

		return out, nil
	}

	exe, err := v.path()
	if err != nil {
		return Verification{}, err
	}

	predicateType := input.Loaded.Statement.PredicateType
	kind := ClaimKind(predicateType)

	var failure string

	ordered := orderProducers(input.Producers, kind)

	for i := range ordered {
		err := cosign.VerifyBlobAttestation(ctx, exe, input.Path, predicateType, signer(&ordered[i], input))
		if err == nil {
			out.Signature = SignatureVerified
			out.ProducerID = ordered[i].ID

			return out, nil
		}

		if failure == "" {
			failure = err.Error()
		}
	}

	out.Signature = SignatureFailed
	out.Failure = failure

	return out, nil
}

// path gives the cosign executable, looked up once.
func (v *Verifier) path() (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.exe != "" {
		return v.exe, nil
	}

	getenv := v.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	exe, err := cosign.Path(getenv)
	if err != nil {
		var failure *diag.Error
		if errors.As(err, &failure) {
			return "", failure
		}

		return "", err //nolint:wrapcheck // cosign.Path gives only a *diag.Error.
	}

	v.exe = exe

	return exe, nil
}

// orderProducers puts the producers trusted for the claim kind first, then the others, each group by id.
func orderProducers(producers []workspace.Producer, kind string) []workspace.Producer {
	sorted := slices.Clone(producers)
	slices.SortStableFunc(sorted, func(a, b workspace.Producer) int {
		aTrusted, bTrusted := slices.Contains(a.Claims, kind), slices.Contains(b.Claims, kind)

		switch {
		case aTrusted && !bTrusted:
			return -1
		case !aTrusted && bTrusted:
			return 1
		}

		return canonjson.CompareUTF16(a.ID, b.ID)
	})

	return sorted
}

// signer turns the authentication of a producer into cosign verification options.
func signer(producer *workspace.Producer, input *VerifyInput) cosign.VerifyOptions {
	opts := cosign.VerifyOptions{TrustedRoot: input.TrustedRoot}

	switch producer.Authentication.Method {
	case workspace.AuthCosignKey:
		opts.Key = producer.Authentication.Key
		// A key that is a file is workspace-relative; a KMS or env:// reference has a scheme and is passed as is.
		if !strings.Contains(opts.Key, "://") && !filepath.IsAbs(opts.Key) {
			opts.Key = filepath.Join(input.Root, filepath.FromSlash(opts.Key))
		}
	case workspace.AuthCosignKeyless:
		opts.Identity = producer.Authentication.Identity
		opts.Issuer = producer.Authentication.Issuer
	}

	return opts
}
