package exceptions

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	// signaturesDir is the directory beside the waiver register that holds the sigstore bundle of each signed
	// waiver: `<dir of the register>/signatures/<id>.sigstore.json`.
	signaturesDir = "signatures"
	// signatureSuffix is the end of the file name of the sigstore bundle of a waiver.
	signatureSuffix = ".sigstore.json"
	// RecordKind is the `assessment.Record` kind of a scoped waiver.
	RecordKind = "waiver"
)

// Evidence states in the results.
const (
	// EvidenceResolved: the file exists and has the listed digest.
	EvidenceResolved = "resolved"
	// EvidenceUnresolved: the file does not exist or cannot be read.
	EvidenceUnresolved = "unresolved"
	// EvidenceDigestMismatch: the file exists and has another digest.
	EvidenceDigestMismatch = "digest-mismatch"
)

// Verification is the outcome of approver verification for one scoped waiver.
type Verification struct {
	// Diagnostic is the CO-EXC-009 warning of a waiver that is not verified. nil when verified.
	Diagnostic *diag.Diagnostic `json:"-"`
	// WaiverID is the id of the waiver.
	WaiverID string `json:"-"`
	// Mechanism is `protected-review` or `signed`. Empty when no authority was found.
	Mechanism string `json:"mechanism,omitempty"`
	// Authority is the id of the authority that the waiver names.
	Authority string `json:"authority"`
	// Signer is the identity or key that matched, for a signed waiver, or the approver for protected-review.
	Signer string `json:"signer,omitempty"`
	// Reason says why the waiver is not verified: an `assessment` reason such as `not-on-anchor` or
	// `reviewer-not-listed`, or `trust-anchor-unreadable` or `verification-unavailable` when verification could
	// not decide. Empty when verified.
	Reason string `json:"reason,omitempty"`
	// Detail explains Reason.
	Detail string `json:"detail,omitempty"`
	// Verified is true when the waiver counts. A waiver that is not verified is not applied.
	Verified bool `json:"verified"`
}

// VerifyApprovers verifies the approver of each scoped waiver with v. The approver must be in an authority of the
// role `exception`, or of the role `governance` for a waiver of the rule REV-GOV-001, at the trust anchor. For
// `protected-review` the entry must exist in the register at the trust anchor with an equal canonical digest;
// for `signed` its canonical JSON must carry a signature from a signer of the authority in
// `<dir of the register>/signatures/<id>.sigstore.json`.
//
// register is the workspace-relative "/" path of the waiver register. The result has one value for each scoped
// waiver, in the order of the input; finding waivers need no approver and are skipped. The error is a condition
// that stops the run (a missing cosign), not a failed waiver.
func VerifyApprovers(ctx context.Context, verifier assessment.Verifier, register string, waivers []Waiver) ([]Verification, error) {
	out := make([]Verification, 0, len(waivers))

	for i := range waivers {
		entry := &waivers[i]
		if !entry.IsScoped() {
			continue
		}

		one, err := verifyApprover(ctx, verifier, register, entry)
		if err != nil {
			return nil, err
		}

		out = append(out, one)
	}

	return out, nil
}

// verifyApprover verifies one scoped waiver.
func verifyApprover(ctx context.Context, verifier assessment.Verifier, register string, entry *Waiver) (Verification, error) {
	canonical, err := entry.CanonicalJSON()
	if err != nil {
		return Verification{}, err
	}

	sum, err := entry.Digest()
	if err != nil {
		return Verification{}, err
	}

	missing := false
	rec := assessment.Record{
		Kind: RecordKind, ID: entry.ID, Path: register, Digest: sum, Canonical: canonical,
		Reviewer: assessment.Reviewer{Identity: entry.Approver.Identity, Authority: entry.Approver.Authority},
		Role:     RoleOf(entry),
		Bundle:   path.Join(path.Dir(register), signaturesDir, entry.ID+signatureSuffix),
		ParseAt: func(content []byte) (assessment.Anchored, error) {
			return anchoredWaiver(content, register, entry.ID, &missing)
		},
	}

	res, err := verifier.Verify(ctx, rec)
	if err != nil {
		return Verification{}, fmt.Errorf("verify the approver of waiver %s: %w", entry.ID, err)
	}

	one := Verification{
		WaiverID: entry.ID, Verified: res.Applied, Mechanism: res.Mechanism, Authority: res.Authority,
		Signer: res.Signer, Reason: res.Reason, Detail: res.Detail,
	}

	switch {
	case res.Applied:
		return one, nil
	case res.Unknown != nil:
		one.Reason, one.Detail = res.Unknown.Reason, res.Unknown.Detail
	case missing && res.Reason == assessment.DigestMismatch:
		one.Reason = assessment.NotOnAnchor
		one.Detail = fmt.Sprintf("waiver '%s' is not in %s at the trust anchor; merge it into the anchor first", entry.ID, register)
	}

	if one.Authority == "" {
		one.Authority = entry.Approver.Authority
	}

	d := diag.New(diag.EXC009, diag.Detail(fmt.Sprintf("%s: approver '%s' (authority '%s') is not verified: %s: %s",
		entry.ID, entry.Approver.Identity, entry.Approver.Authority, one.Reason, one.Detail)),
		diag.WithSource(diag.Source{URI: register}))
	one.Diagnostic = &d

	return one, nil
}

// RoleOf gives the authority role that the approver of the waiver needs: `governance` for a waiver of the
// governance rule REV-GOV-001, else `exception`.
func RoleOf(w *Waiver) string {
	if w.Rule == impact.RuleGovernance {
		return workspace.RoleGovernance
	}

	return workspace.RoleException
}

// anchoredWaiver reads the copy of waiver waiverID from the register as it is at the trust anchor. missing is set when
// the register there does not hold a scoped waiver with the waiverID.
func anchoredWaiver(content []byte, register, waiverID string, missing *bool) (assessment.Anchored, error) {
	list, err := ParseWaivers(string(content), register)
	if err != nil {
		return assessment.Anchored{}, err
	}

	for i := range list {
		found := &list[i]
		if found.ID != waiverID {
			continue
		}

		if !found.IsScoped() {
			*missing = true

			return assessment.Anchored{}, fmt.Errorf("waiver '%s' at the trust anchor is not a scoped waiver", waiverID)
		}

		sum, err := found.Digest()
		if err != nil {
			return assessment.Anchored{}, err
		}

		return assessment.Anchored{Digest: sum, Reviewer: assessment.Reviewer{Identity: found.Approver.Identity, Authority: found.Approver.Authority}}, nil
	}

	*missing = true

	return assessment.Anchored{}, errors.New("waiver '" + waiverID + "' is not in the register")
}

// EvidenceResult is the state of one compensating evidence reference.
type EvidenceResult struct {
	Path string `json:"path"`
	// SHA256 is the digest that the waiver lists.
	SHA256 string `json:"sha256"`
	// State is EvidenceResolved, EvidenceUnresolved, or EvidenceDigestMismatch.
	State string `json:"state"`
}

// ResolveEvidence checks the compensating evidence of a waiver under root (the workspace root): each file must exist
// and have the listed digest. It gives one result for each reference, in file order, and the CO-EXC-011 warning
// for each reference that does not resolve. Evidence never changes a verdict or the disposition.
func ResolveEvidence(root string, entry *Waiver) ([]EvidenceResult, []diag.Diagnostic) {
	results := make([]EvidenceResult, 0, len(entry.CompensatingEvidence))
	warnings := []diag.Diagnostic{}

	for _, ref := range entry.CompensatingEvidence {
		state := EvidenceResolved
		detail := ""

		switch actual, err := digest.OfFile(filepath.Join(root, filepath.FromSlash(ref.Path))); {
		case err != nil:
			state, detail = EvidenceUnresolved, "the file cannot be read"
		case actual != ref.SHA256:
			state, detail = EvidenceDigestMismatch, "the file has the digest "+actual
		}

		results = append(results, EvidenceResult{Path: ref.Path, SHA256: ref.SHA256, State: state})

		if state != EvidenceResolved {
			warnings = append(warnings, diag.New(diag.EXC011, diag.Detail(fmt.Sprintf("waiver %s: %s: %s", entry.ID, ref.Path, detail))))
		}
	}

	return results, warnings
}
