package conformance

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/policy"
)

// The reasons that the provenance package sets are members of the closed set: a reason outside it would turn a claim
// into an `error`, because every unknown result has exactly one reason.
func TestProvenanceReasonsAreInTheClosedSet(t *testing.T) {
	t.Parallel()

	for _, reason := range []string{provenance.ReasonNoEvidenceSupplied, provenance.ReasonEvidenceConflict} {
		if !UnknownReason(reason).Valid() {
			t.Errorf("%q is not a member of the closed set of unknown reasons", reason)
		}
	}
}

func TestRenderedEnvironmentOutcomes(t *testing.T) {
	t.Parallel()

	complete := &render.Result{Environment: "production", Path: "deploy/prod", Coverage: assurance.CoverageComplete}
	partial := &render.Result{
		Environment: "production", Path: "deploy/other", Coverage: assurance.CoveragePartial,
		Gaps: []assurance.Gap{{Kind: "remote-base", Detail: "https://example.invalid/base"}},
	}
	failed := &render.Result{Environment: "production", Path: "deploy/broken", Coverage: assurance.CoverageFailed}
	unsupported := &render.Result{
		Environment: "production", Path: "deploy/prod", Coverage: assurance.CoverageComplete,
		Selections: []render.Selection{{Selector: render.ResourceID{Kind: "Service", Name: "web"}, Unsupported: true}},
	}

	tests := []struct {
		name    string
		id      claims.ID
		verdict RuleVerdict
		reason  UnknownReason
		detail  string
		sources []*render.Result
	}{
		{"no source", claims.CompleteResourceSet, VerdictUnknown, ReasonRenderedEvidenceMissing, "no rendering source", nil},
		{"complete", claims.CompleteResourceSet, VerdictPass, "", "complete", []*render.Result{complete}},
		{"partial names its gaps", claims.CompleteResourceSet, VerdictUnknown, ReasonRenderedEvidenceMissing, "remote-base", []*render.Result{complete, partial}},
		{"failed", claims.CompleteResourceSet, VerdictUnknown, ReasonRenderedEvidenceMissing, "failed coverage", []*render.Result{failed}},
		{"occurrence versions", claims.ContainerOccurrenceVersions, VerdictPass, "", "complete", []*render.Result{complete}},
		{"selection of unsupported syntax", claims.ResolvedSelections, VerdictUnknown, ReasonRenderedEvidenceMissing, "Service", []*render.Result{unsupported}},
		{"resolved selections", claims.ResolvedSelections, VerdictPass, "", "complete", []*render.Result{complete}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := renderedEnvironment(tt.id, "production", tt.sources)
			if got.Verdict != tt.verdict || got.Reason != tt.reason || got.Environment != "production" || !strings.Contains(got.Detail, tt.detail) {
				t.Errorf("outcome = %+v, want %s %s containing %q", got, tt.verdict, tt.reason, tt.detail)
			}
		})
	}
}

// A claim that has its own evaluator and family is not evaluated here: the family default applies and gives unknown,
// never a pass.
func TestClaimsOfOtherEvaluatorsAreLeftToTheFamilyDefault(t *testing.T) {
	t.Parallel()

	chk := &checker{assurance: &assuranceRun{ev: &evidence.Evidence{}}}

	for _, id := range []claims.ID{
		claims.PlannedResources, claims.NetworkIsolation, claims.RBACGrants, claims.CheckOutcome, claims.QualityScenarioVerdict,
	} {
		plan := &policy.RulePlan{Predicate: policy.Predicate{
			Family: policy.FamilyAssuranceClaim, AssuranceClaim: &policy.ClaimRequirement{ID: string(id)},
		}}

		if outcomes, evaluated := chk.claimOutcomes(plan); evaluated || outcomes != nil {
			t.Errorf("%s: outcomes = %+v, evaluated = %v", id, outcomes, evaluated)
		}
	}
}

func TestLineageEntriesWithoutRenderedOccurrencesAreEmptyNotNil(t *testing.T) {
	t.Parallel()

	run := &assuranceRun{ev: &evidence.Evidence{}}

	if entries := run.lineageEntries(); entries == nil || len(entries) != 0 {
		t.Errorf("entries = %#v, want an empty list", entries)
	}
}
