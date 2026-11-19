package assessment

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// The mechanisms that verify the authority of a record.
const (
	MechanismProtectedReview = workspace.MechanismProtectedReview
	MechanismSigned          = workspace.MechanismSigned
)

// The reasons of an `unknown` verification (a member of the closed reason set of typed obligations).
const (
	// ReasonTrustAnchorUnreadable: the trust anchor is missing, shallow, or not an ancestor of HEAD.
	ReasonTrustAnchorUnreadable = "trust-anchor-unreadable"
	// ReasonVerificationUnavailable: the signature cannot be verified here, for example keyless with `--offline`.
	ReasonVerificationUnavailable = "verification-unavailable"
)

// The reasons that a record is not applied. A record that is not applied is shown with its reason and counts
// for nothing.
const (
	// NotOnAnchor: protected-review, the record does not exist at the trust anchor revision.
	NotOnAnchor = "not-on-anchor"
	// PathNotAllowed: the record path is outside the `paths` of the authority.
	PathNotAllowed = "path-not-allowed"
	// ReviewerNotListed: the reviewer is not in `reviewers` of the authority at the trust anchor.
	ReviewerNotListed = "reviewer-not-listed"
	// DigestMismatch: the record differs from its copy at the trust anchor.
	DigestMismatch = "digest-mismatch"
	// AuthorityNotConfigured: the authority that the record names does not exist at the trust anchor.
	AuthorityNotConfigured = "authority-not-configured"
	// AuthorityMismatch: the authority has another role or another mechanism than the record needs.
	AuthorityMismatch = "authority-mismatch"
	// NotSigned: signed authority, the record has no signature bundle.
	NotSigned = "not-signed"
	// SignerNotListed: no signer of the authority signed the record.
	SignerNotListed = "signer-not-listed"
)

// Record kinds that the verifiers know. A caller may use another kind for an entry of its own (a waiver) when it
// gives ParseAt.
const (
	RecordApproval   = "approval"
	RecordRevocation = "revocation"
)

// Record is one entry whose authority is verified: an approval, a revocation, or an entry of another register
// (for example a scoped waiver, task 9.3) that names its approver.
type Record struct {
	ParseAt   func(content []byte) (Anchored, error)
	Reviewer  Reviewer
	Kind      string
	ID        string
	Path      string
	Digest    string
	Role      string
	Bundle    string
	Canonical []byte
	Line      int
}

// Anchored is a record as it exists at the trust anchor.
type Anchored struct {
	Digest   string
	Reviewer Reviewer
}

// ApprovalRecord gives the Record of a loaded approval.
func ApprovalRecord(a *Approval) Record {
	return Record{
		Kind: RecordApproval, ID: a.ID, Path: a.File, Line: a.Line, Digest: a.Digest,
		Canonical: a.CanonicalJSON(), Reviewer: a.Reviewer, Role: workspace.RoleReviewer,
	}
}

// RevocationRecord gives the Record of a loaded revocation.
func RevocationRecord(r *Revocation) Record {
	return Record{
		Kind: RecordRevocation, ID: r.ID, Path: r.File, Line: r.Line, Digest: r.Digest,
		Canonical: r.CanonicalJSON(), Reviewer: r.Reviewer, Role: workspace.RoleReviewer,
	}
}

// VerifyUnknown says that verification could not decide. Reason is one of the Reason* constants.
type VerifyUnknown struct {
	Reason string
	Detail string
}

// VerifyResult is the outcome of one record.
//
// Exactly one of three cases holds: Applied is true (the record counts); Unknown is set (verification could not
// decide: the obligation is `unknown`, never a pass); or neither (the record is not applied, Reason says why and
// Diagnostic is the CO-ASM-006 warning).
type VerifyResult struct {
	Unknown    *VerifyUnknown
	Diagnostic *diag.Diagnostic
	Mechanism  string
	Authority  string
	Signer     string
	Reason     string
	Detail     string
	Applied    bool
}

// Verifier decides whether the authority of one record is verified. The implementations are ProtectedReview,
// Signed, and Authorities (which picks by the mechanism of the authority that the record names).
//
// The error is for conditions that stop the run, not for a record that fails: a missing cosign gives CO-WRK-014
// (exit 5).
type Verifier interface {
	Verify(ctx context.Context, rec Record) (VerifyResult, error)
}

// roleOf gives the role that the record needs.
func (r *Record) roleOf() string {
	if r.Role == "" {
		return workspace.RoleReviewer
	}

	return r.Role
}

// source gives the location of the record for a diagnostic.
func (r *Record) source() diag.Option {
	return diag.WithSource(diag.Source{URI: r.Path, Line: r.Line})
}

// label names the record in a message.
func (r *Record) label() string {
	if r.ID != "" {
		return fmt.Sprintf("%s '%s'", r.Kind, r.ID)
	}

	return r.Kind + " in " + r.Path
}

// notApplied builds the result of a record that is not applied (CO-ASM-006).
func notApplied(rec *Record, auth *workspace.Authority, reason, detail string) VerifyResult {
	d := diag.New(diag.ASM006, diag.Detail(rec.label()+": "+detail), rec.source())

	out := VerifyResult{Reason: reason, Detail: detail, Authority: rec.Reviewer.Authority, Diagnostic: &d}
	if auth != nil {
		out.Mechanism = auth.Mechanism
	}

	return out
}

// anchorUnknown builds the result of a record that cannot be verified because the anchor is unreadable.
func anchorUnknown(rec *Record, a *Anchor) VerifyResult {
	out := VerifyResult{Authority: rec.Reviewer.Authority, Unknown: a.Unreadable, Detail: a.Unreadable.Detail}
	if a.Diagnostic != nil {
		d := *a.Diagnostic
		out.Diagnostic = &d
	}

	return out
}

// lookup finds the authority that the record names at the anchor and checks its role. It gives a result when
// the record cannot be verified (the authority is missing or has another role).
func lookup(a *Anchor, rec *Record) (workspace.Authority, *VerifyResult) {
	auth, ok := a.Authority(rec.Reviewer.Authority)
	if !ok {
		res := notApplied(rec, nil, AuthorityNotConfigured, fmt.Sprintf(
			"authority '%s' is not configured at the trust anchor '%s'", rec.Reviewer.Authority, a.Ref))

		return workspace.Authority{}, &res
	}

	if auth.Role != rec.roleOf() {
		res := notApplied(rec, &auth, AuthorityMismatch, fmt.Sprintf(
			"authority '%s' has the role '%s' and the record needs '%s'", auth.ID, auth.Role, rec.roleOf()))

		return workspace.Authority{}, &res
	}

	return auth, nil
}

// Authorities verifies records with the mechanism of the authority that each record names. The authority
// configuration comes from the trust anchor.
type Authorities struct {
	Anchor    *Anchor
	Protected Verifier
	Signed    Verifier
}

// AuthorityOptions build Authorities.
type AuthorityOptions struct {
	Git     workspace.GitPort
	Anchor  *Anchor
	Getenv  func(string) string
	Root    string
	Offline bool
}

// NewAuthorities builds the verifier of a run: protected-review from git and signed through cosign.
func NewAuthorities(opts *AuthorityOptions) *Authorities {
	return &Authorities{
		Anchor:    opts.Anchor,
		Protected: &ProtectedReview{Git: opts.Git, Anchor: opts.Anchor},
		Signed:    &Signed{Git: opts.Git, Anchor: opts.Anchor, Root: opts.Root, Getenv: opts.Getenv, Offline: opts.Offline},
	}
}

// Verify implements Verifier. The mechanism comes from the authority at the anchor, never from the record.
func (a *Authorities) Verify(ctx context.Context, rec Record) (VerifyResult, error) { //nolint:gocritic // hugeParam: the Verifier contract takes the record by value.
	if !a.Anchor.Readable() {
		return anchorUnknown(&rec, a.Anchor), nil
	}

	auth, bad := lookup(a.Anchor, &rec)
	if bad != nil {
		return *bad, nil
	}

	switch auth.Mechanism {
	case MechanismSigned:
		return a.Signed.Verify(ctx, rec)
	default:
		return a.Protected.Verify(ctx, rec)
	}
}

// Verification is the outcome for one record of a set.
type Verification struct {
	RecordID  string
	Kind      string
	Mechanism string
	Authority string
	Signer    string
	Reason    string
	Detail    string
	Applied   bool
}

// Verified is the outcome for the records of a Set.
type Verified struct {
	// Approvals and Revocations hold ONLY the verified records. Results lists every record, applied or not.
	Approvals   []*Approval
	Revocations []*Revocation
	Results     []Verification
	// Unknown is set when at least one record could not be judged (the first reason in record order). The
	// currency of what depends on those records is `unknown`.
	Unknown *VerifyUnknown
	// Diagnostics are the CO-ASM-006, -007, and -011 warnings, sorted, without repeats.
	Diagnostics []diag.Diagnostic
}

// SetVerifier verifies all the records of a Set.
type SetVerifier interface {
	VerifySet(ctx context.Context, set *Set) (Verified, error)
}

// VerifySet verifies every approval and revocation of the set. A revocation is verified like an approval. The
// result lists the records in id order.
func (a *Authorities) VerifySet(ctx context.Context, set *Set) (Verified, error) {
	return VerifySet(ctx, a, set)
}

// VerifySet verifies the approvals and the revocations of the set with the verifier.
func VerifySet(ctx context.Context, v Verifier, set *Set) (Verified, error) { //nolint:gocognit // one loop over each record kind; the bookkeeping is in the closure.
	out := Verified{
		Approvals: []*Approval{}, Revocations: []*Revocation{}, Results: []Verification{}, Diagnostics: []diag.Diagnostic{},
	}

	seen := map[string]bool{}

	record := func(rec Record, id string) (VerifyResult, error) {
		res, err := v.Verify(ctx, rec)
		if err != nil {
			return res, err
		}

		out.Results = append(out.Results, Verification{
			RecordID: id, Kind: rec.Kind, Applied: res.Applied, Mechanism: res.Mechanism, Authority: res.Authority,
			Signer: res.Signer, Reason: reasonOf(&res), Detail: res.Detail,
		})

		if res.Unknown != nil && out.Unknown == nil {
			out.Unknown = res.Unknown
		}

		if res.Diagnostic != nil {
			key := diag.FormatDiagnostic(*res.Diagnostic)
			if !seen[key] {
				seen[key] = true

				out.Diagnostics = append(out.Diagnostics, *res.Diagnostic)
			}
		}

		return res, nil
	}

	for _, ap := range set.Approvals {
		res, err := record(ApprovalRecord(ap), ap.ID)
		if err != nil {
			return out, err
		}

		if res.Applied {
			out.Approvals = append(out.Approvals, ap)
		}
	}

	for _, rv := range set.Revocations {
		res, err := record(RevocationRecord(rv), rv.ID)
		if err != nil {
			return out, err
		}

		if res.Applied {
			out.Revocations = append(out.Revocations, rv)
		}
	}

	slices.SortStableFunc(out.Results, func(x, y Verification) int {
		if c := strings.Compare(x.Kind, y.Kind); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(x.RecordID, y.RecordID)
	})

	out.Diagnostics = diag.Sort(out.Diagnostics)

	return out, nil
}

// reasonOf gives the reason of a result: the unknown reason, the not-applied reason, or "".
func reasonOf(r *VerifyResult) string {
	if r.Unknown != nil {
		return r.Unknown.Reason
	}

	return r.Reason
}
