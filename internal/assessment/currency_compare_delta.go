package assessment

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// deltaProblem is why the delta of an incremental approval is not the exact difference of the two subjects.
type deltaProblem struct {
	// unlisted is the item that the record leaves out, nil when the problem is not about one item.
	unlisted *Item
	// detail is the reason shown with the approval.
	detail string
}

// checkIncremental validates an incremental approval: the recorded delta must be exactly the difference between
// the previous subject and the new one (security-assessments, "Incremental approval states an exact delta").
func (e *evaluator) checkIncremental(approval *Approval, cur *Currency) {
	if approval.Previous == nil {
		return
	}

	problem := e.deltaProblemOf(approval)
	if problem == nil {
		return
	}

	cur.addReason(DetailDeltaInvalid, fmt.Sprintf("approval %s: %s", approval.ID, problem.detail))

	detail := fmt.Sprintf("incremental approval %s is not valid: %s", approval.ID, problem.detail)
	if problem.unlisted != nil {
		detail = fmt.Sprintf("incremental approval %s does not list %s %s", approval.ID, problem.unlisted.Kind, problem.unlisted.Identity)
	}

	e.note(diag.ASM006, diag.Detail(detail), diag.WithSource(diag.Source{URI: approval.File, Line: approval.Line}))
}

// deltaProblemOf finds the first reason why the delta of an incremental approval is not exact, nil when it is.
func (e *evaluator) deltaProblemOf(approval *Approval) *deltaProblem {
	prev := e.approvalByID(approval.Previous.Approval)
	if prev == nil {
		return &deltaProblem{detail: fmt.Sprintf("the previous approval %s is not a verified approval", approval.Previous.Approval)}
	}

	if prev.Subject.Digest != approval.Previous.SubjectDigest {
		return &deltaProblem{detail: "the previous subject digest differs from the subject digest of " + prev.ID}
	}

	want := deltaOf(prev.Subject.Dependencies, approval.Subject.Dependencies)

	got := approval.Delta
	if got == nil {
		got = &Delta{}
	}

	if problem := understatedDelta(&want, got); problem != nil {
		return problem
	}

	// The record lists something that did not change.
	if len(got.Added) != len(want.Added) || len(got.Removed) != len(want.Removed) || len(got.Modified) != len(want.Modified) {
		return &deltaProblem{detail: "the delta lists an item that the two subjects do not differ in"}
	}

	return nil
}

// understatedDelta finds an item that the recomputed delta names and the record leaves out: the approval
// understates the change.
func understatedDelta(want, got *Delta) *deltaProblem {
	if i := firstUnlisted(want.Added, got.Added); i >= 0 {
		return unlistedItem("added", want.Added[i])
	}

	if i := firstUnlisted(want.Removed, got.Removed); i >= 0 {
		return unlistedItem("removed", want.Removed[i])
	}

	if i := firstUnlisted(want.Modified, got.Modified); i >= 0 {
		m := want.Modified[i]

		return unlistedItem("modified", Item{Kind: m.Kind, Identity: m.Identity, Digest: m.Digest})
	}

	return nil
}

// unlistedItem is the problem of a delta that does not list an item of the given change.
func unlistedItem(change string, it Item) *deltaProblem {
	return &deltaProblem{
		detail:   fmt.Sprintf("the delta does not list the %s item %s %s", change, it.Kind, it.Identity),
		unlisted: &it,
	}
}

// firstUnlisted gives the index of the first wanted entry that the listed entries do not contain, -1 when all are.
func firstUnlisted[T comparable](want, listed []T) int {
	return slices.IndexFunc(want, func(w T) bool { return !slices.Contains(listed, w) })
}

// approvalByID gives the verified approval with the id.
func (e *evaluator) approvalByID(approvalID string) *Approval {
	for _, approval := range e.verified.Approvals {
		if approval.ID == approvalID {
			return approval
		}
	}

	return nil
}

// deltaOf computes the exact delta between two lists of items.
func deltaOf(previous, next []Item) Delta {
	res := impact.Diff(entriesOf(previous, nil), entriesOf(next, nil), nil)
	out := Delta{Added: []Item{}, Removed: []Item{}, Modified: []ModifiedItem{}}

	for _, c := range res.Added {
		out.Added = append(out.Added, Item{Kind: DependencyKind(c.Kind), Identity: c.Identity, Digest: c.CurrentDigest})
	}

	for _, c := range res.Removed {
		out.Removed = append(out.Removed, Item{Kind: DependencyKind(c.Kind), Identity: c.Identity, Digest: c.BaselineDigest})
	}

	for _, c := range res.Modified {
		out.Modified = append(out.Modified, ModifiedItem{
			Kind: DependencyKind(c.Kind), Identity: c.Identity, PreviousDigest: c.BaselineDigest, Digest: c.CurrentDigest,
		})
	}

	return out
}
