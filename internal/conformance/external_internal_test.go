package conformance

import (
	"slices"
	"testing"
)

// The reasons of the assurance adapters are told apart by who can fix them: a claim, an evaluator, an assumption, or a
// mode that the workspace sets up is configuration; a missing result or rendering is evidence.
func TestAssuranceReasonsAreConfigurationOrEvidence(t *testing.T) {
	t.Parallel()

	configuration := []UnknownReason{ReasonClaimNotEnabled, ReasonNoEvaluatorAvailable, ReasonEnforcementNotDeclared, ReasonReviewedModeNotAvailable}
	evidence := []UnknownReason{ReasonNoEvidenceSupplied, ReasonRenderedEvidenceMissing}

	for _, reason := range append(slices.Clone(configuration), evidence...) {
		wantAction, wantKind := nextActionEvidence, MissingEvidence
		if slices.Contains(configuration, reason) {
			wantAction, wantKind = nextActionConfig, MissingCoverage
		}

		if got := nextActionFor(reason); got != wantAction {
			t.Errorf("%s: next action %q, want %q", reason, got, wantAction)
		}

		if got := missingFor(reason, "", "detail", wantAction); got.Kind != wantKind || got.Detail != "detail" {
			t.Errorf("%s: missing = %+v, want kind %q", reason, got, wantKind)
		}
	}
}

// Every unknown reason that Evaluate can give has a missing kind and a next action: the two switches stay in step with
// the closed set.
func TestEveryClosedReasonHasAMissingKind(t *testing.T) {
	t.Parallel()

	for _, reason := range UnknownReasons() {
		if got := missingFor(reason, "family", "d", "a"); got.Kind == "" {
			t.Errorf("%s has no missing kind", reason)
		}

		if nextActionFor(reason) == "" {
			t.Errorf("%s has no next action", reason)
		}
	}
}
