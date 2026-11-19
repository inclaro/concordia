package assessment_test

import (
	"context"

	"github.com/inclaro/concordia/internal/assessment"
)

// verifiedAll verifies every record of the set with the authority security-review.
type verifiedAll struct {
	rejects map[string]bool
	unknown string
}

func (v verifiedAll) VerifySet(_ context.Context, set *assessment.Set) (assessment.Verified, error) {
	out := assessment.Verified{Approvals: []*assessment.Approval{}, Revocations: []*assessment.Revocation{}}

	for _, ap := range set.Approvals {
		res := v.verifyApproval(ap)
		if res.Applied {
			out.Approvals = append(out.Approvals, ap)
		}

		out.Results = append(out.Results, res)
	}

	for _, rv := range set.Revocations {
		out.Revocations = append(out.Revocations, rv)
		out.Results = append(out.Results, assessment.Verification{RecordID: rv.ID, Kind: assessment.RecordRevocation, Applied: true, Authority: "security-review"})
	}

	if v.unknown != "" {
		out.Unknown = &assessment.VerifyUnknown{Reason: v.unknown}
	}

	return out, nil
}

// verifyApproval gives the verification result of one approval: unknown, rejected, or applied.
func (v verifiedAll) verifyApproval(ap *assessment.Approval) assessment.Verification {
	res := assessment.Verification{RecordID: ap.ID, Kind: assessment.RecordApproval, Authority: "security-review", Mechanism: "protected-review"}

	switch {
	case v.unknown != "":
		res.Reason = v.unknown
		res.Detail = "the trust anchor cannot be read"
	case v.rejects[ap.ID]:
		res.Reason = assessment.NotOnAnchor
	default:
		res.Applied = true
	}

	return res
}
