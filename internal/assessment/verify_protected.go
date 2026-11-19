package assessment

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/inclaro/concordia/internal/workspace"
)

// ProtectedReview verifies a record by where it is. The record counts only if it exists at the trust anchor
// revision with the same digest, its path is inside the `paths` of the authority, and its reviewer is in the
// `reviewers` of the authority, as the anchor lists them. Everything is read from local git data through
// GitPort; there is no network access and no forge API.
//
// The strength of this mechanism is the protection of the anchor branch in the forge. Concordia verifies the
// location and the binding of the record. It does not verify who pressed the merge button.
type ProtectedReview struct {
	Git    workspace.GitPort
	Anchor *Anchor
}

// Verify implements Verifier.
func (p *ProtectedReview) Verify(ctx context.Context, rec Record) (VerifyResult, error) { //nolint:gocritic // hugeParam: the Verifier contract takes the record by value.
	if !p.Anchor.Readable() {
		return anchorUnknown(&rec, p.Anchor), nil
	}

	auth, bad := lookup(p.Anchor, &rec)
	if bad != nil {
		return *bad, nil
	}

	if auth.Mechanism != MechanismProtectedReview {
		return notApplied(&rec, &auth, AuthorityMismatch,
			fmt.Sprintf("authority '%s' uses the mechanism '%s', not '%s'", auth.ID, auth.Mechanism, MechanismProtectedReview)), nil
	}

	content, found, err := p.Git.ShowFile(ctx, p.Anchor.Revision, rec.Path)
	if err != nil {
		// A revision that was resolved but cannot be read is an unreadable anchor, not a verdict on the record.
		res := anchorUnknown(&rec, p.Anchor.withReadError(rec.Path, err))

		return res, nil
	}

	if !found {
		return notApplied(&rec, &auth, NotOnAnchor, fmt.Sprintf(
			"%s does not exist at the trust anchor '%s' (%s); merge the record into the anchor first",
			rec.Path, p.Anchor.Ref, shortRevision(p.Anchor.Revision))), nil
	}

	if !pathAllowed(auth.Paths, rec.Path) {
		return notApplied(&rec, &auth, PathNotAllowed, fmt.Sprintf(
			"%s is outside the paths of authority '%s'", rec.Path, auth.ID)), nil
	}

	anchored, err := parseAnchored(&rec, content)
	if err != nil {
		return notApplied(&rec, &auth, DigestMismatch,
			"the copy at the trust anchor cannot be read: "+errText(err)), nil
	}

	if anchored.Digest != rec.Digest {
		return notApplied(&rec, &auth, DigestMismatch, fmt.Sprintf(
			"%s differs from its copy at the trust anchor (anchor digest %s, candidate digest %s)",
			rec.Path, anchored.Digest, rec.Digest)), nil
	}

	if !slices.Contains(auth.Reviewers, anchored.Reviewer.Identity) {
		return notApplied(&rec, &auth, ReviewerNotListed, fmt.Sprintf(
			"reviewer '%s' is not listed for authority '%s' at the trust anchor",
			anchored.Reviewer.Identity, auth.ID)), nil
	}

	if anchored.Reviewer.Authority != auth.ID {
		return notApplied(&rec, &auth, AuthorityMismatch, fmt.Sprintf(
			"the copy at the trust anchor names authority '%s'", anchored.Reviewer.Authority)), nil
	}

	return VerifyResult{
		Applied: true, Mechanism: MechanismProtectedReview, Authority: auth.ID, Signer: anchored.Reviewer.Identity,
	}, nil
}

// withReadError gives the anchor as unreadable because a file cannot be read from it.
func (a *Anchor) withReadError(path string, err error) *Anchor {
	c := *a
	c.Unreadable = &VerifyUnknown{
		Reason: ReasonTrustAnchorUnreadable,
		Detail: fmt.Sprintf("cannot read %s at the trust anchor '%s': %s", path, a.Ref, errText(err)),
	}

	return &c
}

// pathAllowed says whether the path matches one of the globs. No glob allows nothing.
func pathAllowed(globs []string, path string) bool {
	for _, g := range globs {
		if ok, err := doublestar.Match(g, path); err == nil && ok {
			return true
		}
	}

	return false
}

// parseAnchored reads the copy of the record at the trust anchor.
func parseAnchored(rec *Record, content []byte) (Anchored, error) {
	switch {
	case rec.ParseAt != nil:
		return rec.ParseAt(content)
	case rec.Kind == RecordApproval:
		a, err := ParseApproval(rec.Path, content)
		if err != nil {
			return Anchored{}, err
		}

		return Anchored{Digest: a.Digest, Reviewer: a.Reviewer}, nil
	case rec.Kind == RecordRevocation:
		r, err := ParseRevocation(rec.Path, content)
		if err != nil {
			return Anchored{}, err
		}

		return Anchored{Digest: r.Digest, Reviewer: r.Reviewer}, nil
	}

	return Anchored{}, errors.New("record kind '" + rec.Kind + "' needs ParseAt")
}
