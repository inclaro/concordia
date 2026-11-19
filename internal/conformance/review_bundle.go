package conformance

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// ReviewEvidence is what the evaluation bundle records about the review evaluation of a run (design D13): the
// verification results, the authority configuration that verification used, the trust anchor, and the evidence
// references that can no longer be resolved. It is not part of results.json.
type ReviewEvidence struct {
	Evaluation *assessment.Evaluation
	// Authorities is the authority configuration that verification used: the trust anchor's when it is readable.
	Authorities []workspace.Authority
	// AnchorRef and AnchorRevision are the configured trust anchor and its commit; the revision is empty when
	// the anchor could not be read.
	AnchorRef, AnchorRevision string
	AuthorityDigest           string
	// NotReproducible lists the evidence references that cannot be resolved or whose content differs from the
	// recorded digest.
	NotReproducible []UnresolvedEvidence
}

// UnresolvedEvidence is an evidence reference of an assessment that cannot be resolved. A historical
// reproduction of the assessment is not available for it.
type UnresolvedEvidence struct {
	Assessment string `json:"assessment"`
	Evidence   string `json:"evidence"`
	Ref        string `json:"ref"`
	// Digest is the digest that the document records; empty when it records none.
	Digest string `json:"digest,omitempty"`
	// Reason is `missing` or `digest-differs`.
	Reason string `json:"reason"`
}

// Reasons of an unresolved evidence reference.
const (
	EvidenceMissing       = "missing"
	EvidenceDigestDiffers = "digest-differs"
)

// reviewEvidence collects the bundle data of the review evaluation.
func (c *checker) reviewEvidence(wspace *workspace.Workspace) *ReviewEvidence {
	review := c.review
	if review == nil || review.eval == nil || review.set == nil {
		return nil
	}

	out := &ReviewEvidence{Evaluation: review.eval, Authorities: []workspace.Authority{}, AuthorityDigest: reviewAuthorityDigest(wspace, review.anchor)}

	switch {
	case review.anchor.Readable() && review.anchor.Config != nil:
		out.Authorities = append(out.Authorities, review.anchor.Config.Authorities...)
	case wspace.Assessments != nil && wspace.Assessments.TrustAnchor == nil:
		out.Authorities = append(out.Authorities, wspace.Assessments.Authorities...)
	}

	if review.anchor != nil {
		out.AnchorRef, out.AnchorRevision = review.anchor.Ref, review.anchor.Revision
	}

	for _, a := range review.set.Assessments {
		for _, evidence := range a.Evidence {
			if bad := unresolvedEvidence(wspace.Root, a, evidence); bad != nil {
				out.NotReproducible = append(out.NotReproducible, *bad)
			}
		}
	}

	slices.SortFunc(out.NotReproducible, func(x, y UnresolvedEvidence) int {
		return strings.Compare(x.Assessment+"\x00"+x.Evidence, y.Assessment+"\x00"+y.Evidence)
	})

	return out
}

// unresolvedEvidence checks one evidence reference: a workspace file that is missing, or whose digest differs from
// the recorded one. A reference that is not a file path (a URL, a ticket) is not checked here.
func unresolvedEvidence(root string, a *assessment.Assessment, evidence assessment.Evidence) *UnresolvedEvidence {
	if evidence.Ref == "" || strings.Contains(evidence.Ref, "://") {
		return nil
	}

	bad := &UnresolvedEvidence{Assessment: a.Key(), Evidence: evidence.ID, Ref: evidence.Ref, Digest: evidence.Digest}

	abs, err := fsutil.Confine(root, evidence.Ref)
	if err != nil {
		bad.Reason = EvidenceMissing

		return bad
	}

	if _, err := os.Stat(abs); err != nil {
		bad.Reason = EvidenceMissing

		return bad
	}

	if evidence.Digest == "" {
		return nil
	}

	if got, err := digest.OfFile(abs); err != nil || got != evidence.Digest {
		bad.Reason = EvidenceDigestDiffers

		return bad
	}

	return nil
}

// reviewVerificationSchemaVersion is the `schemaVersion` of the bundle file `verification/assessments.json`.
const reviewVerificationSchemaVersion = "1.0"

// ReviewVerificationJSON gives the content of the bundle file `verification/assessments.json`: the verification
// result of every decision record, the authority configuration that was used, the trust anchor revision, the
// digest-contract version, the currency of each assessment version, and the evidence references that cannot be
// resolved. It holds no assessment text and no secret value. It is nil for a run that evaluated no assessment.
func ReviewVerificationJSON(run *Run) ([]byte, error) {
	rev := run.ReviewEvidence
	if rev == nil {
		return nil, nil
	}

	type record struct {
		Record    string `json:"record"`
		Kind      string `json:"kind"`
		Mechanism string `json:"mechanism,omitempty"`
		Authority string `json:"authority,omitempty"`
		Signer    string `json:"signer,omitempty"`
		Reason    string `json:"reason,omitempty"`
		Detail    string `json:"detail,omitempty"`
		Applied   bool   `json:"applied"`
	}

	type version struct {
		ID       string `json:"id"`
		Currency string `json:"currency"`
		Approval string `json:"approval,omitempty"`
		Version  int    `json:"version"`
	}

	records := make([]record, 0, len(rev.Evaluation.Verification))
	for _, v := range rev.Evaluation.Verification {
		records = append(records, record{
			Record: v.RecordID, Kind: v.Kind, Applied: v.Applied, Mechanism: v.Mechanism, Authority: v.Authority, Signer: v.Signer,
			Reason: v.Reason, Detail: v.Detail,
		})
	}

	versions := make([]version, 0, len(rev.Evaluation.Assessments))

	for i := range rev.Evaluation.Assessments {
		r := &rev.Evaluation.Assessments[i]
		entry := version{ID: r.ID, Version: r.Version, Currency: string(r.Currency.State)}

		if r.Currency.Approval != nil {
			entry.Approval = r.Currency.Approval.ID
		}

		versions = append(versions, entry)
	}

	unresolved := rev.NotReproducible
	if unresolved == nil {
		unresolved = []UnresolvedEvidence{}
	}

	out, err := canonjson.Marshal(map[string]any{
		"schemaVersion":     reviewVerificationSchemaVersion,
		"evaluatedAt":       clock.ISO(rev.Evaluation.At),
		"contractVersion":   rev.Evaluation.ContractVersion,
		"capabilityProfile": rev.Evaluation.CapabilityProfile,
		"trustAnchor":       map[string]any{"ref": rev.AnchorRef, "revision": rev.AnchorRevision},
		"authorityDigest":   rev.AuthorityDigest,
		"authorities":       rev.Authorities,
		"verification":      records,
		"assessments":       versions,
		"notReproducible":   unresolved,
	})
	if err != nil {
		return nil, fmt.Errorf("encode assessment verification: %w", err)
	}

	return out, nil
}
