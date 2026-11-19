package integration_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// The review-dependency tests of the CBM facts (task 1.7). They replay the recordings of
// test/fixtures/review/cbm-0.12.1 through conformance.ReviewFacts and the impact package. The currency verdict of an
// assessment (`review-required`) comes from assessment.Evaluate and is covered with the pipeline hook; here the
// removed-item trigger that drives it is asserted on impact.Diff.

func noFiles(string) (digest string, found bool, err error) { return "", false, nil }

// sealedEntries gives the comparison entries of the items of a match.
func sealedEntries(t *testing.T, match *impact.Match) []impact.Entry {
	t.Helper()

	sealed, err := impact.V1().SealAll(match.Items, noFiles)
	if err != nil {
		t.Fatal(err)
	}

	entries := make([]impact.Entry, 0, len(sealed))
	for i := range sealed {
		entries = append(entries, sealed[i].Entry())
	}

	return entries
}

// reviewHasSecretItem reports whether the match has the Secret reference of the production workload as an item.
func reviewHasSecretItem(match *impact.Match) bool {
	return slices.Contains(reviewIdentities(match), reviewSecretItem)
}

// reviewIsUndeclaredTarget reports whether the match is unknown because a target is not declared.
func reviewIsUndeclaredTarget(match *impact.Match) bool {
	return match.State == impact.MatchUnknown && match.Reason == impact.ReasonTargetNotDeclared
}

// reviewHasRemovedSecretItem reports whether one of the changes removes the Secret reference item.
func reviewHasRemovedSecretItem(changes []impact.Change) bool {
	for i := range changes {
		if changes[i].Identity == reviewSecretItem && changes[i].Op == impact.ChangeRemoved {
			return true
		}
	}

	return false
}

// Scenario: Undeclared target is unknown.
func TestReviewUndeclaredTargetGivesUnknown(t *testing.T) {
	t.Parallel()

	run := replayReview(t, reviewBaselineFile, nil)

	// The Deployment api refers to the Secret missing-secret, which no manifest declares: there is no edge.
	match := reviewItems(t, run.facts, &impact.Selector{
		Kind: impact.KindSecretReference, Environment: "production", Match: map[string][]string{"reference": {"core/Secret/payments/missing-secret"}},
	})

	if !reviewIsUndeclaredTarget(&match) || len(match.Items) != 0 {
		t.Fatalf("missing-secret: %+v", match)
	}

	if !strings.Contains(match.Detail, "missing-secret") || !slices.Contains(match.LossModes, observation.LossTargetNotDeclared) {
		t.Errorf("detail %q, loss modes %v", match.Detail, match.LossModes)
	}

	// The workload's secret references: the declared one is an item, the undeclared one makes the dependency unknown.
	all := reviewItems(t, run.facts, &impact.Selector{Kind: impact.KindSecretReference, Environment: "production", Identities: []string{reviewProdIdentity}})
	if !reviewIsUndeclaredTarget(&all) || !reviewHasSecretItem(&all) {
		t.Errorf("secret references of the workload: %+v", all)
	}
}

// Scenario: Secret declaration deleted.
func TestReviewDeletedSecretDeclarationIsARemovalTrigger(t *testing.T) {
	t.Parallel()

	baseline := replayReview(t, reviewBaselineFile, nil)
	variant := replayReview(t, reviewVariantFile, []string{reviewSecretFile})
	selector := impact.Selector{Kind: impact.KindSecretReference, Environment: "production", Identities: []string{reviewProdIdentity}}

	approved := reviewItems(t, baseline.facts, &selector)
	if !reviewHasSecretItem(&approved) {
		t.Fatalf("the approved items lack the Secret reference: %v", reviewIdentities(&approved))
	}

	// The reference stays in the manifest and the declaration is gone: no edge, and the gap is not silence.
	current := reviewItems(t, variant.facts, &selector)
	if reviewHasSecretItem(&current) {
		t.Fatalf("the Secret reference survived its declaration: %v", reviewIdentities(&current))
	}

	if !reviewIsUndeclaredTarget(&current) {
		t.Errorf("current match: %+v", current)
	}

	diff := impact.Diff(sealedEntries(t, &approved), sealedEntries(t, &current), nil)

	if !slices.ContainsFunc(diff.Removed, func(c impact.Change) bool { return c.Identity == reviewSecretItem }) {
		t.Fatalf("removed = %+v", diff.Removed)
	}

	if len(diff.Undisposed) != len(diff.Removed) || !reviewHasRemovedSecretItem(diff.Triggers()) {
		t.Errorf("triggers = %+v", diff.Triggers())
	}
}

// reviewEntryPointDigest gives the digest of the only public entry point of the host in the environment.
func reviewEntryPointDigest(t *testing.T, run *reviewRun, env string) string {
	t.Helper()

	match := reviewItems(t, run.facts, &impact.Selector{
		Kind: impact.KindPublicEntryPoint, Environment: env, Match: map[string][]string{"host": {"api.example.com"}},
	})
	if len(match.Items) != 1 || !strings.Contains(match.Items[0].Identity, "k8s:"+env+"/") {
		t.Fatalf("%s entry points = %v", env, reviewIdentities(&match))
	}

	sealed, err := impact.V1().Seal(match.Items[0], noFiles)
	if err != nil {
		t.Fatal(err)
	}

	return sealed.Digest
}

// Scenario: Same host and path in two environments.
func TestReviewSameHostTwoEnvironmentsHaveTwoItems(t *testing.T) {
	t.Parallel()

	run := replayReview(t, reviewBaselineFile, nil)

	digests := map[string]string{}
	for _, env := range []string{"production", "staging"} {
		digests[env] = reviewEntryPointDigest(t, run, env)
	}

	// Equal host and path, but the backend is the Service of each environment: an assessment that depends on the
	// production item has a digest that no change of the staging Ingress can move (the unit test of
	// internal/conformance changes the staging Ingress and compares the digests).
	if digests["production"] == digests["staging"] {
		t.Errorf("the two environments have one digest: %v", digests)
	}

	baselineProd := reviewItems(t, run.facts, &impact.Selector{Kind: impact.KindPublicEntryPoint, Environment: "production"})
	diff := impact.Diff(sealedEntries(t, &baselineProd), sealedEntries(t, &baselineProd), nil)

	if len(diff.Triggers()) != 0 {
		t.Errorf("an unchanged environment triggers: %+v", diff.Triggers())
	}
}

// Scenario: Two containers share an image repository.
func TestReviewSharedImageRepositoryIsPartialAndNotCurrent(t *testing.T) {
	t.Parallel()

	run := replayReview(t, reviewBaselineFile, nil)

	match := reviewItems(t, run.facts, &impact.Selector{Kind: impact.KindImplementation, Environment: "production", Identities: []string{reviewProdIdentity}})

	if match.State != impact.MatchUnknown || match.Reason != impact.ReasonCoverageIncomplete {
		t.Fatalf("image dependency: %+v", match)
	}

	if match.Coverage != string(observation.CoveragePartial) || !slices.Equal(match.LossModes, []string{observation.LossMergedOccurrences}) {
		t.Errorf("coverage %q, loss modes %v", match.Coverage, match.LossModes)
	}

	// Only the surviving occurrence (sidecar, 1.3.0) is an item; the lost one (app, 1.2.0) is named by the detail.
	if len(match.Items) != 1 || !slices.Equal(match.Items[0].Facets["subject"], []string{"registry.example.com/payments/api:1.3.0"}) {
		t.Errorf("items = %v", match.Items)
	}

	if !strings.Contains(match.Detail, `"app"`) {
		t.Errorf("detail = %q", match.Detail)
	}
}

// reviewIsTerraformResourceReference reports whether the edge is the Terraform `resource` reference.
func reviewIsTerraformResourceReference(e *observation.EvidenceEdge) bool {
	return e.Facets["family"] == "terraform" && e.Facets["terraformRef"] == "resource"
}

// reviewIsUnsupportedTerraformStorage reports whether the match is unsupported for the Terraform family.
func reviewIsUnsupportedTerraformStorage(match *impact.Match) bool {
	if match.State != impact.MatchUnsupported || match.Reason != impact.ReasonUnsupportedFamily {
		return false
	}

	return match.Profile == "cbm-0.12.1" && len(match.Items) == 0
}

// Scenario: Unmapped Terraform reference.
func TestReviewUnmappedTerraformReferenceIsUnsupported(t *testing.T) {
	t.Parallel()

	run := replayReview(t, reviewBaselineFile, nil)

	// The Terraform `resource` reference exists in the graph (O5) ...
	if edges := run.edges(observation.KindInfrastructureDependency, reviewIsTerraformResourceReference); len(edges) == 0 {
		t.Fatal("the recording has no Terraform resource reference")
	}

	// ... but the profile maps it to no dependency kind.
	match := reviewItems(t, run.facts, &impact.Selector{Kind: impact.KindStorageReference, Match: map[string][]string{"via": {"terraform"}}})

	if !reviewIsUnsupportedTerraformStorage(&match) {
		t.Errorf("terraform storage-reference: %+v", match)
	}
}

const reviewAssessment = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-API
version: 1
type: security
title: API secrets
scope:
  calmNodes: [api-service]
environment: production
subject:
  summary: The Secret that the production api workload reads
  dependencies:
    - kind: secret-reference
      selector:
        identity: [` + reviewProdIdentity + `]
        reference: [` + reviewSecretObjKey + `]
assumptions: []
evidence: []
outcome: satisfactory
state: ready-for-review
`

// approvedAll verifies every approval of the set.
type approvedAll struct{}

func (approvedAll) VerifySet(_ context.Context, set *assessment.Set) (assessment.Verified, error) {
	out := assessment.Verified{Approvals: []*assessment.Approval{}, Revocations: []*assessment.Revocation{}}

	for _, ap := range set.Approvals {
		out.Approvals = append(out.Approvals, ap)
		out.Results = append(out.Results, assessment.Verification{
			RecordID: ap.ID, Kind: assessment.RecordApproval, Applied: true, Authority: "security-review", Mechanism: "protected-review",
		})
	}

	return out, nil
}

// reviewAssessmentWorld is the assessment set of ASM-API and the clock that its evaluations share.
type reviewAssessmentWorld struct {
	set *assessment.Set
	now time.Time
}

func newReviewAssessmentWorld(t *testing.T) *reviewAssessmentWorld {
	t.Helper()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"assessments/ASM-API/v1.yaml": reviewAssessment})

	set, err := assessment.Load(root, []string{"assessments"})
	if err != nil {
		t.Fatal(err)
	}

	return &reviewAssessmentWorld{set: set, now: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)}
}

func (w *reviewAssessmentWorld) input(facts impact.FactSource) *assessment.EvalInput {
	return &assessment.EvalInput{Set: w.set, Verifier: approvedAll{}, Facts: facts, Now: func() time.Time { return w.now }}
}

// approve records a verified approval of the subject as the binding shows it.
func (w *reviewAssessmentWorld) approve(bind *assessment.Binding) {
	w.set.Approvals = append(w.set.Approvals, &assessment.Approval{
		ID: "ASM-API-v1-0001", Decision: assessment.DecisionApproved, Assessment: bind.Assessment, Subject: bind.Subject, Scope: bind.Scope,
		Environment: bind.Environment, Assumptions: bind.Assumptions, Evidence: bind.Evidence,
		ContractVersion: bind.ContractVersion, CapabilityProfile: bind.CapabilityProfile, ProducedUnder: bind.ProducedUnder,
		Reviewer:  assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
		DecidedAt: w.now.Add(-24 * time.Hour), Location: assessment.Location{File: "assessments/approvals/ASM-API-v1-0001.yaml"},
		Digest: "sha256:" + strings.Repeat("a", 64),
	})
}

// Scenario: Secret declaration deleted (the removal trigger and the currency of the assessment).
func TestReviewDeletedSecretDeclarationMakesTheAssessmentReviewRequired(t *testing.T) {
	t.Parallel()

	world := newReviewAssessmentWorld(t)
	baseline := replayReview(t, reviewBaselineFile, nil)
	variant := replayReview(t, reviewVariantFile, []string{reviewSecretFile})

	// A verified approval binds the subject as the baseline shows it.
	bind, err := assessment.Bind(t.Context(), world.input(baseline.facts), "ASM-API", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(bind.Unknown) != 0 || len(bind.Subject.Dependencies) != 1 || bind.Subject.Dependencies[0].Identity != reviewSecretItem {
		t.Fatalf("binding = %+v", bind)
	}

	world.approve(bind)

	current := evaluateReview(t, world.input(baseline.facts))
	if current.Currency.State != assessment.CurrencyCurrent {
		t.Fatalf("baseline: state %s, reasons %v, causes %+v", current.Currency.State, current.Currency.Reasons, current.Currency.Causes)
	}

	// The Secret manifest is deleted and the reference stays: the baseline item is removed, never "no change".
	after := evaluateReview(t, world.input(variant.facts))

	if after.Currency.State == assessment.CurrencyCurrent {
		t.Fatalf("the assessment is current after the Secret declaration was deleted: %+v", after.Currency)
	}

	if !reviewHasRemovedSecretItem(after.Currency.Undisposed) {
		t.Errorf("undisposed = %+v", after.Currency.Undisposed)
	}

	if after.Currency.State != assessment.CurrencyReviewRequired {
		t.Errorf("state = %s (unknown %q, reasons %v), want review-required", after.Currency.State, after.Currency.UnknownReason, after.Currency.Reasons)
	}
}

func evaluateReview(t *testing.T, in *assessment.EvalInput) *assessment.Result {
	t.Helper()

	ev, err := assessment.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	for i := range ev.Assessments {
		if ev.Assessments[i].ID == "ASM-API" {
			return &ev.Assessments[i]
		}
	}

	t.Fatal("no result for ASM-API")

	return nil
}
