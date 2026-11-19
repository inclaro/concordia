package cli

import (
	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

// addReview adds the review evaluation of the run to the bundle (design D13): the assessment and assumption files
// as `assessments/<path>`, the approval and revocation files as `approvals/<path>`, and the verification result
// with the authority configuration and the trust anchor revision as `verification/assessments.json`. The files pass
// through redaction, so no secret value reaches the bundle; the records say where a value was removed.
func addReview(app *Context, wsp *workspace.Workspace, run *conformance.Run, byPath map[string][]byte) ([]redact.Record, error) {
	if run.ReviewEvidence == nil {
		return nil, nil
	}

	var records []redact.Record

	for _, group := range []struct {
		dir  string
		refs []conformance.InputRef
	}{{bundle.AssessmentsDir, run.Inputs.Assessments}, {bundle.ApprovalsDir, run.Inputs.Approvals}} {
		for _, ref := range group.refs {
			data, ok, err := readConfined(app, wsp, ref.Path, "assessment document")
			if err != nil {
				return nil, err
			}

			if !ok {
				continue
			}

			text, recs := redact.Default().Redact(ref.Path, string(data))
			for i := range recs {
				recs[i].Output = group.dir + ref.Path
			}

			records = append(records, recs...)
			byPath[group.dir+ref.Path] = []byte(text)
		}
	}

	verification, err := conformance.ReviewVerificationJSON(run)
	if err != nil {
		return nil, err //nolint:wrapcheck // already has context.
	}

	byPath[bundle.AssessmentVerificationPath] = verification

	return records, nil
}

// reviewProvenance gives the review part of the provenance: nil when the run evaluated no assessment.
func reviewProvenance(run *conformance.Run) *bundle.ReviewRef {
	rev := run.ReviewEvidence
	if rev == nil {
		return nil
	}

	return &bundle.ReviewRef{
		ContractVersion: rev.Evaluation.ContractVersion, CapabilityProfile: rev.Evaluation.CapabilityProfile,
		AuthorityDigest: rev.AuthorityDigest, TrustAnchor: rev.AnchorRevision,
	}
}

// reviewReferences lists the review files by digest and whether the bundle holds them, and the evidence references
// that cannot be resolved: those are marked not reproducible, so a historical reproduction is not claimed for them.
func reviewReferences(run *conformance.Run, have map[string]bool) []bundle.Reference {
	if run.ReviewEvidence == nil {
		return nil
	}

	var refs []bundle.Reference

	for _, group := range []struct {
		kind, dir string
		in        []conformance.InputRef
	}{{"assessment", bundle.AssessmentsDir, run.Inputs.Assessments}, {"approval", bundle.ApprovalsDir, run.Inputs.Approvals}} {
		for _, in := range group.in {
			ref := bundle.Reference{Kind: group.kind, Digest: in.Digest, Availability: bundle.Included}
			if !have[group.dir+in.Path] {
				ref.Availability, ref.Reason = bundle.Unavailable, bundle.ReasonNotIncluded
			}

			refs = append(refs, ref)
		}
	}

	for _, bad := range run.ReviewEvidence.NotReproducible {
		refs = append(refs, bundle.Reference{
			Kind: "assessment-evidence", Digest: bad.Digest, Availability: bundle.Unavailable, Reason: bundle.ReasonNotReproducible,
		})
	}

	return refs
}
