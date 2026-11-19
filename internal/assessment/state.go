package assessment

import "github.com/inclaro/concordia/internal/canonjson"

// Review is the review status of one assessment version: the state, the outcome, and the decision records the
// state comes from. The outcome is stored in the document and is never changed by a decision: an approved
// version can have the outcome unsatisfactory.
type Review struct {
	Approval   *Approval   `json:"-"`
	Revocation *Revocation `json:"-"`
	State      State       `json:"state"`
	Outcome    Outcome     `json:"outcome"`
}

// ReviewOf derives the review status of an assessment version from the decision records given. The caller passes
// only records whose authority is verified: an authored state never counts, and a record that is not passed
// does not count. Records that bind another version are ignored.
//
// The latest approval record by decision time (then id) decides: a `rejected` decision gives rejected, an
// `approved` decision gives approved, and a revocation that names that approval by id and digest gives revoked.
// Without a decision record the state is the authored one (an assessment never authors approved, rejected, or
// revoked, see CO-ASM-003), so an outcome of satisfactory without approval gives the authored draft or
// ready-for-review.
func ReviewOf(a *Assessment, approvals []*Approval, revocations []*Revocation) Review {
	review := Review{State: a.State, Outcome: a.Outcome}
	if review.State == "" {
		review.State = StateDraft
	}

	var latest *Approval

	for _, rec := range approvals {
		if rec.Assessment.ID != a.ID || rec.Assessment.Version != a.Version {
			continue
		}

		if latest == nil || later(rec, latest) {
			latest = rec
		}
	}

	if latest == nil {
		return review
	}

	review.Approval = latest

	if latest.Decision == DecisionRejected {
		review.State = StateRejected

		return review
	}

	review.State = StateApproved

	for _, rev := range revocations {
		if rev.Approval.ID != latest.ID || rev.Approval.Digest != latest.Digest {
			continue
		}

		if review.Revocation == nil || rev.DecidedAt.After(review.Revocation.DecidedAt) {
			review.Revocation = rev
		}
	}

	if review.Revocation != nil {
		review.State = StateRevoked
	}

	return review
}

// later says whether a was decided after b (ties by id).
func later(a, b *Approval) bool {
	if c := a.DecidedAt.Compare(b.DecidedAt); c != 0 {
		return c > 0
	}

	return canonjson.CompareUTF16(a.ID, b.ID) > 0
}
