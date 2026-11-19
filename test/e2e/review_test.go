package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
)

// The acceptance scenarios A21 to A31 and A44 of the change add-review-currency on the real binary and the real CBM.
// Each test is a temporary git repository built from test/fixtures/review (see review_helpers_test.go): `main` is the
// trust anchor with the approval records that a case names, and the work happens on `candidate`. Every run uses the
// clock 2026-11-05T00:00:00Z. The tests run in parallel: each has its own CBM project.
//
// Scenario to test:
//
//	A21  TestReviewA21A24A44PublicRouteChange, TestReviewA21A26WorkloadIdentityChange
//	A22  TestReviewA22AuthorizationCodeChange
//	A23  TestReviewA23BindingDeleted, TestReviewA23ReferenceTargetRemoved
//	A24  TestReviewA21A24A44PublicRouteChange
//	A25  TestReviewA25ReviewedFlagIsNotAnApproval
//	A26  TestReviewA26EnvironmentMismatch, TestReviewA21A26WorkloadIdentityChange (digest mismatch),
//	     TestReviewA26ExpiredApproval, TestReviewA26RevokedApproval
//	A27  TestReviewA27UnsupportedSelector, TestReviewA27SelectorSupportLostAfterApproval
//	A28  TestReviewA28DigestContractChanged
//	A30  TestReviewA30TraversalLimit (the cycle cannot be written in documents; internal/impact TestA30CycleTerminatesOncePerPair)
//	A31  TestReviewA31CandidateEditsItsAuthority, TestReviewA31CandidateLowersEnforcement
//	A44  TestReviewA21A24A44PublicRouteChange
//	A29  TestReviewScopedWaiverRealPipeline (the waiver on the real pipeline)

const (
	rvIngressRoute = "public-entry-point:k8s:production/prod-eu/networking.k8s.io/Ingress/payments/api"
	rvEdgeIdentity = "runtime-identity:k8s:production/prod-eu/apps/Deployment/payments/api#serviceAccountName:core/ServiceAccount/payments/api-sa"
)

// rvEdge is the case with the approved runtime identity of the API service.
func rvEdge(name string) *rvSpec {
	return &rvSpec{
		name: name, docs: []string{"ASM-EDGE"}, approvals: []string{"ASM-EDGE-v1-1"},
		required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}},
	}
}

// rvTerraform is the case whose only dependency uses the selector `via: terraform`, which the capability profile of the
// provider does not map.
func rvTerraform(name string) *rvSpec {
	return &rvSpec{name: name, docs: []string{"ASM-TERRAFORM"}, required: []rvRequired{{id: "tf", assessment: "ASM-TERRAFORM"}}}
}

// rvTerraformApproval is an approval of ASM-TERRAFORM v1 for a dependency that the profile no longer maps: it is the
// record of ASM-ROUTE with the assessment, the subject digest, and no assumptions replaced, and with the digest of the
// authority configuration of the case when the case differs from the fixture authority (authorityDigest). The record binds the
// Ingress item as if an earlier provider had resolved `via: terraform` to it.
func rvTerraformApproval(t *testing.T, authorityDigest string) string {
	t.Helper()

	set, err := assessment.Load(filepath.Join(reviewFixtureDir, "assessments"), []string{"ASM-TERRAFORM"})
	if err != nil {
		t.Fatal(err)
	}

	route, err := os.ReadFile(filepath.Join(reviewFixtureDir, "records", "approvals", "ASM-ROUTE-v1-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	appr, err := assessment.ParseApproval("ASM-TERRAFORM-v1-1.yaml", route)
	if err != nil {
		t.Fatal(err)
	}

	appr.ID = "ASM-TERRAFORM-v1-1"
	appr.Assessment = assessment.AssessmentRef{ID: "ASM-TERRAFORM", Version: 1, Digest: set.Assessments[0].Digest}
	appr.Assumptions = nil
	appr.Subject.Digest = assessment.SubjectDigest(appr.Subject.Dependencies)

	if authorityDigest != "" {
		appr.ProducedUnder.AuthorityDigest = authorityDigest
	}

	record, err := assessment.RenderApproval(appr)
	if err != nil {
		t.Fatal(err)
	}

	return string(record)
}

const rvTerraformRecord = "assessments/approvals/ASM-TERRAFORM-v1-1.yaml"

// Scenario: A21 new public route.
// Scenario: A24 assessment file unchanged, ingress changed.
// Scenario: A44 only a Kubernetes manifest changed.
// Scenario: Currency not reduced.
func TestReviewA21A24A44PublicRouteChange(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{
		name: "route", docs: []string{"ASM-ROUTE", "ASM-ROUTE-1"}, approvals: []string{"ASM-ROUTE-v1-1"},
		required: []rvRequired{{id: "route", assessment: "ASM-ROUTE"}},
	})

	rvRequireRouteCurrent(t, r)
	rvExposeAdminPath(r)

	incremental := r.check(1, "--incremental")
	rvRequireRouteTrigger(t, rvRequireRouteMismatch(t, incremental))

	changed := r.check(1, "--changed")
	full := r.check(1)
	rvRequireChangedKeepsCurrency(t, changed, incremental, full)
}

// Scenario: A22 authorization code changes, topology does not.
func TestReviewA22AuthorizationCodeChange(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{name: "authz", docs: []string{"ASM-AUTHZ"}, approvals: []string{"ASM-AUTHZ-v1-1"}, required: []rvRequired{{id: "authz", assessment: "ASM-AUTHZ"}}})

	base := r.check(0)
	base.assessment(t, "ASM-AUTHZ").want(t, "current")

	r.write("app/main.go", r.read("app/main.go")+"\n// The handler now also checks the audience of the token.\n")
	r.commit("fix: check the audience")

	doc := r.check(1)
	a := doc.assessment(t, "ASM-AUTHZ")
	a.want(t, "review-required", "evidence digest mismatch")

	if !anyContains(a.Details, "EV-AUTHZ") {
		t.Errorf("details %v do not name the evidence EV-AUTHZ", a.Details)
	}

	// No dependency item changed: the identity of the workload is the one that was approved.
	if len(a.Triggers) != 0 || anyContains(a.Reasons, "subject digest mismatch") {
		t.Errorf("a code change gave a topology trigger: reasons %v, triggers %+v", a.Reasons, a.Triggers)
	}

	// The topology that the deployment trace shows did not move.
	if !sameJSON(base.Raw["deploymentTrace"], doc.Raw["deploymentTrace"]) {
		t.Error("the deployment trace changed with a change of application code")
	}

	rvRequireVerdict(t, doc, "REV-APR-001", "violation")
}

// Scenario: A23 binding deleted.
func TestReviewA23BindingDeleted(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvEdge("binding"))
	r.check(0).assessment(t, "ASM-EDGE").want(t, "current")

	// The variant of the fixture without the binding that attributes the api workloads to the api-service.
	r.write("bindings/concordia.bindings.yaml", fixtureFiles(t, filepath.Join(reviewFixtureDir, "variants", "removed-binding"))["bindings/concordia.bindings.yaml"])
	r.commit("chore: delete the binding of the api workloads")

	doc := r.check(1)
	a := doc.assessment(t, "ASM-EDGE")
	a.want(t, "review-required", "subject digest mismatch")

	rvRequireRemovedIdentityTrigger(t, a)
	rvRequireRemovalBlocks(t, doc)
}

// Scenario: Reference target declaration removed.
// Scenario: A23 binding deleted (the dependency is an undeclared reference after the target manifest is deleted).
func TestReviewA23ReferenceTargetRemoved(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{name: "secret", docs: []string{"ASM-SECRET"}, approvals: []string{"ASM-SECRET-v1-1"}, required: []rvRequired{{id: "secret", assessment: "ASM-SECRET"}}})
	r.check(0).assessment(t, "ASM-SECRET").want(t, "current")

	r.rvRemoveFile("k8s/prod/secret.yaml")
	r.commit("chore: delete the secret manifest")

	doc := r.check(1)
	a := doc.assessment(t, "ASM-SECRET")

	// The reference stays in the Deployment and the declaration is gone: CBM emits no edge. The missing edge is a
	// removal and never "no change".
	a.want(t, "review-required", "subject digest mismatch")

	rvRequireSecretRemoval(t, doc, a)
}

// Scenario: A25 manual reviewed flag without authorized approval.
func TestReviewA25ReviewedFlagIsNotAnApproval(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{name: "flag", docs: []string{"ASM-FLAG"}, required: []rvRequired{{id: "flag", assessment: "ASM-FLAG"}}})

	// The document is valid: `reviewed: true` is a known key without an effect.
	if res := r.cli(rvEnv(), "assessment", "validate"); res.code != 0 {
		t.Fatalf("assessment validate (%d): %s", res.code, res.output())
	}

	doc := r.check(1)
	a := doc.assessment(t, "ASM-FLAG")
	a.want(t, "review-required", "no approval")

	rvRequireFlagIsNotApproval(t, doc, a)
	rvRequireFlagStatus(t, r)
}

// Scenario: A26 approval for staging used for production.
func TestReviewA26EnvironmentMismatch(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{
		name: "env", docs: []string{"ASM-STAGING"}, approvals: []string{"ASM-STAGING-v1-1"},
		required: []rvRequired{{id: "stage", assessment: "ASM-STAGING", environment: "production"}},
	})

	doc := r.check(1)

	// The approval is for staging and it is current for the assessment of staging; it does not satisfy production.
	doc.assessment(t, "ASM-STAGING").want(t, "current")

	if req := doc.required(t, "stage"); !rvEnvironmentMismatch(req) {
		t.Errorf("required = %+v, want review-required with environment mismatch and a violation", req)
	}
}

// Scenario: A26 digest mismatch.
// Scenario: A21 new public route (a new workload identity touches the dependencies of an approved assessment).
func TestReviewA21A26WorkloadIdentityChange(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvEdge("identity"))
	r.check(0).assessment(t, "ASM-EDGE").want(t, "current")

	r.write("k8s/prod/serviceaccount-v2.yaml", "apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: api-sa-v2\n  namespace: payments\n")
	r.editFile("k8s/prod/deployment.yaml", "serviceAccountName: api-sa", "serviceAccountName: api-sa-v2")
	r.commit("feat: a new service account for the API")

	doc := r.check(1)
	a := doc.assessment(t, "ASM-EDGE")
	a.want(t, "review-required", "subject digest mismatch")

	rvRequireIdentityReplaced(t, a)
}

// Scenario: A26 expired approval.
// Scenario: Time-only expiry.
func TestReviewA26ExpiredApproval(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{name: "expiry", docs: []string{"ASM-EXPIRING"}, approvals: []string{"ASM-EXPIRING-v1-1"}, required: []rvRequired{{id: "exp", assessment: "ASM-EXPIRING"}}})

	r.check(0).assessment(t, "ASM-EXPIRING").want(t, "current")

	// No file changes between the two runs: only the clock moves.
	doc := r.checkWith(1, rvEnv("CONCORDIA_NOW=2026-11-15T00:00:00Z"))
	doc.assessment(t, "ASM-EXPIRING").want(t, "expired", "expired")

	if !anyContains(doc.assessment(t, "ASM-EXPIRING").Details, "expired at 2026-11-11T00:00:00Z") {
		t.Errorf("details %v do not name the expiry time", doc.assessment(t, "ASM-EXPIRING").Details)
	}

	if doc.required(t, "exp").Verdict != "violation" {
		t.Errorf("the required assessment has verdict %q, want violation", doc.required(t, "exp").Verdict)
	}
}

// Scenario: A26 revoked approval.
func TestReviewA26RevokedApproval(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvEdge("revoked"))
	record := rvRevokeOnCandidate(t, r)

	// Only on the candidate the revocation is not applied (protected-review); on the anchor the approval is revoked.
	r.toAnchor("docs: revoke ASM-EDGE-v1-1", map[string]string{rvEdgeRevoke: record})

	doc := r.check(1)
	a := doc.assessment(t, "ASM-EDGE")
	a.want(t, "revoked", "revoked")

	rvRequireRevoked(t, doc, a)
}

// Scenario: A27 provider upgrade loses selector support (a selector that the profile does not map, no approval yet).
func TestReviewA27UnsupportedSelector(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvTerraform("terraform"))

	// Without an approval the result is review-required (a change in the review state wins over an unknown cause), and
	// the cause and the warning say that the selector is not mapped.
	doc := r.check(1)
	a := doc.assessment(t, "ASM-TERRAFORM")
	a.want(t, "review-required", "no approval")

	if !rvFirstCauseIs(a, "unsupported-family", "via: terraform") {
		t.Errorf("causes = %+v, want unsupported-family for via: terraform", a.Causes)
	}

	if msgs := doc.messages("CO-SEC-002"); len(msgs) != 1 || !strings.Contains(msgs[0], "ASM-TERRAFORM@v1") {
		t.Errorf("CO-SEC-002 = %v", msgs)
	}
}

// Scenario: A27 provider upgrade loses selector support.
func TestReviewA27SelectorSupportLostAfterApproval(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvTerraform("terraform-approved"))
	r.toAnchor("docs: approve ASM-TERRAFORM v1", map[string]string{rvTerraformRecord: rvTerraformApproval(t, "")})

	// The approved dependency uses a selector that the current profile does not map: unknown, never an apparent pass,
	// and a mandatory gate exits 4.
	rvRequireTerraformUnknown(t, r.check(4))
}

// anyCause tells whether a cause of the assessment has a detail with the part.
func anyCause(a *rvAssessment, part string) bool {
	for _, c := range a.Causes {
		if strings.Contains(c.Detail, part) {
			return true
		}
	}

	return false
}

// Scenario: A28 digest contract removes a security field.
func TestReviewA28DigestContractChanged(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{name: "contract", docs: []string{"ASM-EDGE"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}}})

	record, err := os.ReadFile(filepath.Join(reviewFixtureDir, "records", "approvals", "ASM-EDGE-v1-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// The approval was bound under an earlier digest contract that no shipped equivalence covers.
	older := strings.Replace(string(record), "contractVersion: review-digest/1", "contractVersion: review-digest/0", 1)
	if older == string(record) {
		t.Fatal("the record has no contract version")
	}

	r.toAnchor("docs: approve ASM-EDGE v1 under an earlier digest contract", map[string]string{rvProductionEdge: older})

	doc := r.check(1)
	a := doc.assessment(t, "ASM-EDGE")
	a.want(t, "review-required", "digest contract changed")

	if !anyContains(a.Details, "review-digest/0") || !anyContains(a.Details, "review-digest/1") {
		t.Errorf("details %v do not name both contract versions", a.Details)
	}

	if a.Approval != nil && a.Currency == "current" {
		t.Error("the old approval was reused")
	}
}

// Scenario: A30 traversal limit reached.
// Scenario: Limit raised.
func TestReviewA30TraversalLimit(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{
		name: "limit", docs: []string{"ASM-ROUTE", "ASM-ROUTE-1"}, approvals: []string{"ASM-ROUTE-v1-1"},
		required: []rvRequired{{id: "route", assessment: "ASM-ROUTE"}}, limits: "  analysisLimits: { maxVisited: 1 }\n",
	})

	// An unchanged subject has no traversal: the approval is current whatever the limit.
	r.check(0).assessment(t, "ASM-ROUTE").want(t, "current")

	r.editFile("k8s/prod/ingress.yaml", "          - path: /\n", "          - path: /v2\n")
	r.commit("feat: change the public route")

	rvRequireLimitExceeded(t, r.check(4))

	// The limit is raised above the closure: the analysis completes and the currency is the real comparison.
	r.editFile("concordia.yaml", "maxVisited: 1 }", "maxVisited: 1000 }")
	r.commit("chore: raise the traversal limit")

	doc := r.check(1)
	doc.assessment(t, "ASM-ROUTE").want(t, "review-required", "subject digest mismatch")
}

// Scenario: A31 candidate removes its own authority.
func TestReviewA31CandidateEditsItsAuthority(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvEdge("governance"))
	r.check(0).assessment(t, "ASM-EDGE").want(t, "current")

	// The candidate lists its own reviewer: the authority is a relaxation, and it is read from the anchor.
	r.editFile("concordia.yaml", "reviewers: ["+rvReviewer+"]", "reviewers: [mallory@example.com]")
	r.commit("chore: the candidate lists its own reviewer")

	doc := r.check(1)
	rvRequireAuthorityRelaxation(t, doc)

	// The approval on the anchor still counts: the candidate's authority does not.
	doc.assessment(t, "ASM-EDGE").want(t, "current")
}

// Scenario: A31 candidate removes its own authority (a review rule lowered to advisory).
func TestReviewA31CandidateLowersEnforcement(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, rvEdge("advisory"))

	r.editFile("concordia.yaml", "{family: assessment-currency, mode: mandatory}", "{family: assessment-currency, mode: advisory}")
	r.commit("chore: review rules become advisory")

	// The governance rule is mandatory at the anchor: the candidate does not lower it for itself.
	doc := r.check(1)
	rvRequireVerdict(t, doc, "REV-GOV-001", "violation")

	if !rvEnforcementLoosened(doc) {
		t.Errorf("relaxations = %+v, want the looser enforcement entry", doc.Governance.Relaxations)
	}
}

// Scenario: A29 gate is waived only while the waiver applies (a scoped waiver on the real pipeline).
func TestReviewScopedWaiverRealPipeline(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	exception := "    - id: exception-board\n      role: exception\n      mechanism: protected-review\n      reviewers: [" + rvReviewer + "]\n      paths: [exceptions/**]\n"

	t.Run("violation", func(t *testing.T) {
		t.Parallel()

		rvWaiverViolation(t, exception)
	})

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()

		rvWaiverUnknown(t, exception)
	})
}
