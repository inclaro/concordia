package assessment_test

import (
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
)

func loadedEdge(t *testing.T) *assessment.Assessment {
	t.Helper()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	return set.Assessment("ASM-EDGE", 1)
}

func revocationOf(a *assessment.Approval, id string, at time.Time) *assessment.Revocation {
	return &assessment.Revocation{
		ID:        id,
		Approval:  assessment.Ref{ID: a.ID, Digest: a.Digest},
		Reviewer:  assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
		DecidedAt: at,
		Reason:    "the gateway moved",
	}
}

// sealed gives an approval that went through the document form, so that it has its digest.
func sealed(t *testing.T, a *assessment.Approval) *assessment.Approval {
	t.Helper()

	text, err := assessment.RenderApproval(a)
	if err != nil {
		t.Fatal(err)
	}

	out, err := assessment.ParseApproval(a.File, text)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// Scenario: Authored approved state is ignored.
func TestAnAuthoredApprovedStateFails(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"approved", "rejected", "revoked", "review-required", "expired"} {
		files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "state: ready-for-review", "state: "+state)

		_, err := loadEdge(t, files)
		failure := wantDiag(t, err, diag.ASM003, state)

		if failure.Diagnostic.Source == nil || failure.Diagnostic.Source.Line != 22 {
			t.Errorf("%s: source = %+v, want line 22", state, failure.Diagnostic.Source)
		}
	}

	for _, state := range []string{"draft", "ready-for-review"} {
		files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "state: ready-for-review", "state: "+state)
		if _, err := loadEdge(t, files); err != nil {
			t.Errorf("authored %s: %v", state, err)
		}
	}
}

// Scenario: Outcome does not imply approval.
func TestAnOutcomeOfSatisfactoryWithoutApprovalDerivesReadyForReview(t *testing.T) {
	t.Parallel()

	a := loadedEdge(t)

	review := assessment.ReviewOf(a, nil, nil)
	if review.State != assessment.StateReadyForReview || review.Outcome != assessment.OutcomeSatisfactory || review.Approval != nil {
		t.Errorf("review = %+v", review)
	}

	draft := *a
	draft.State = assessment.StateDraft

	if got := assessment.ReviewOf(&draft, nil, nil); got.State != assessment.StateDraft || got.Outcome != assessment.OutcomeSatisfactory {
		t.Errorf("draft review = %+v", got)
	}

	// A record for another version does not count.
	other := approvalFor(a, "R-1", decided)
	other.Assessment.Version = 2

	if got := assessment.ReviewOf(a, []*assessment.Approval{other}, nil); got.State != assessment.StateReadyForReview {
		t.Errorf("record of another version: state = %s", got.State)
	}
}

// Scenario: Unsatisfactory outcome can be approved as a record.
func TestAnUnsatisfactoryOutcomeWithAnApprovalKeepsBothValues(t *testing.T) {
	t.Parallel()

	files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "outcome: satisfactory", "outcome: unsatisfactory")

	set, err := loadEdge(t, files)
	if err != nil {
		t.Fatal(err)
	}

	a := set.Assessment("ASM-EDGE", 1)
	rec := sealed(t, approvalFor(a, "R-1", decided))

	review := assessment.ReviewOf(a, []*assessment.Approval{rec}, nil)
	if review.State != assessment.StateApproved || review.Outcome != assessment.OutcomeUnsatisfactory || review.Approval != rec {
		t.Errorf("review = %+v", review)
	}
}

func TestReviewOfDerivesRejectedAndRevoked(t *testing.T) {
	t.Parallel()

	a := loadedEdge(t)

	approval := sealed(t, approvalFor(a, "R-1", decided))
	revocation := revocationOf(approval, "V-1", decided.Add(time.Hour))

	if got := assessment.ReviewOf(a, []*assessment.Approval{approval}, []*assessment.Revocation{revocation}); got.State != assessment.StateRevoked ||
		got.Revocation != revocation {
		t.Errorf("revoked: %+v", got)
	}

	// A revocation that binds another digest of the approval does not apply.
	stale := revocationOf(approval, "V-2", decided.Add(time.Hour))
	stale.Approval.Digest = digestA

	if got := assessment.ReviewOf(a, []*assessment.Approval{approval}, []*assessment.Revocation{stale}); got.State != assessment.StateApproved {
		t.Errorf("digest mismatch: state = %s", got.State)
	}

	rejection := approvalFor(a, "R-2", decided.Add(2*time.Hour))
	rejection.Decision = assessment.DecisionRejected
	rejected := sealed(t, rejection)

	// The latest decision wins, and an older revocation of the older approval does not touch it.
	got := assessment.ReviewOf(a, []*assessment.Approval{approval, rejected}, []*assessment.Revocation{revocation})
	if got.State != assessment.StateRejected || got.Approval != rejected {
		t.Errorf("rejected after approved: %+v", got)
	}

	later := sealed(t, approvalFor(a, "R-3", decided.Add(3*time.Hour)))

	got = assessment.ReviewOf(a, []*assessment.Approval{later, approval, rejected}, []*assessment.Revocation{revocation})
	if got.State != assessment.StateApproved || got.Approval != later {
		t.Errorf("approved after rejected: %+v", got)
	}

	// Equal times: the higher id decides.
	tie1 := sealed(t, approvalFor(a, "R-4", decided))
	tie2 := approvalFor(a, "R-5", decided)
	tie2.Decision = assessment.DecisionRejected

	if got := assessment.ReviewOf(a, []*assessment.Approval{tie1, sealed(t, tie2)}, nil); got.State != assessment.StateRejected {
		t.Errorf("tie: state = %s", got.State)
	}
}

// Scenario: A25 manual reviewed flag without authorized approval.
func TestAReviewedFlagLoadsAndNeverChangesTheState(t *testing.T) {
	t.Parallel()

	files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "outcome: satisfactory\n",
		"outcome: satisfactory\nreviewed: true\nnotes: reviewed by security\n")

	set, err := loadEdge(t, files)
	if err != nil {
		t.Fatalf("a file with reviewed: true must load: %v", err)
	}

	a := set.Assessment("ASM-EDGE", 1)
	if !a.Reviewed || a.Notes != "reviewed by security" {
		t.Errorf("reviewed = %v, notes = %q", a.Reviewed, a.Notes)
	}

	review := assessment.ReviewOf(a, nil, nil)
	if review.State != assessment.StateReadyForReview || review.Approval != nil {
		t.Errorf("review = %+v, want ready-for-review without an approval", review)
	}

	if a.Digest == loadedEdge(t).Digest {
		t.Error("the flag is part of the document, so it is part of the version digest")
	}
}
