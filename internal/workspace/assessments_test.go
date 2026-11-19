package workspace_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

const reviewWorkspace = envHead + `trust:
  dimensions: [tenant]
  classifications: [public, internal, restricted]
exceptions:
  maxExpiryDays: 30
assessments:
  paths: [assessments]
  trustAnchor: { ref: origin/main }
  authorities:
    - id: security-review
      role: reviewer
      mechanism: protected-review
      reviewers: [alice@example.com, bob@example.com]
      paths: [assessments/approvals/security/**]
    - id: platform-signers
      role: exception
      mechanism: signed
      signers:
        - { identity: release-bot@example.com, issuer: "https://accounts.example.com" }
        - { publicKey: keys/review.pub }
    - id: governance-board
      role: governance
      reviewers: [carol@example.com]
      paths: [assessments/approvals/governance/**]
  required:
    - { id: edge-exposure, assessment: ASM-EDGE, type: security, scope: { calmNodes: [api-gateway], tags: [edge] }, environment: production, authority: security-review }
    - { id: latency, assessment: ASM-LAT, type: quality, environment: production, authority: platform-signers }
  analysisLimits: { maxDepth: 64, maxVisited: 100000, maxSeconds: 120 }
profiles:
  ci:
    assessments:
      trustAnchor: { ref: refs/remotes/origin/main }
`

// expectWRK002Case reports a load failure that is not a CO-WRK-002 error with the invalid-input exit code.
func expectWRK002Case(t *testing.T, name string, de *diag.Error) {
	t.Helper()

	if de.Diagnostic.Code != codeWRK002 || de.ExitCode != diag.InvalidInput {
		t.Errorf("%s: %+v", name, de)
	}
}

// expectReviewerAuthority checks the authority that names reviewers and uses protected-review.
func expectReviewerAuthority(t *testing.T, review *workspace.Authority) {
	t.Helper()

	expectEqual(t, "reviewer role", review.Role, workspace.RoleReviewer)
	expectEqual(t, "reviewer mechanism", review.Mechanism, workspace.MechanismProtectedReview)
	expectEqual(t, "reviewer count", len(review.Reviewers), 2)
	expectEqual(t, "reviewer path count", len(review.Paths), 1)
}

// expectSignedAuthority checks the authority that uses signers.
func expectSignedAuthority(t *testing.T, signed *workspace.Authority) {
	t.Helper()

	wantSigners := []workspace.Signer{{Identity: "release-bot@example.com", Issuer: "https://accounts.example.com"}, {PublicKey: "keys/review.pub"}}

	expectEqual(t, "signed role", signed.Role, workspace.RoleException)
	expectEqual(t, "signed mechanism", signed.Mechanism, workspace.MechanismSigned)
	expectEqual(t, "signers", signed.Signers, wantSigners)
}

// expectGovernanceAuthority checks the authority that names no mechanism, so it uses protected-review.
func expectGovernanceAuthority(t *testing.T, governance *workspace.Authority) {
	t.Helper()

	expectEqual(t, "governance role", governance.Role, workspace.RoleGovernance)
	expectEqual(t, "governance mechanism", governance.Mechanism, workspace.MechanismProtectedReview)
	expectEqual(t, "governance signers are given", governance.Signers != nil, true)
}

// expectEdgeRequirement checks the first required assessment of the review workspace.
func expectEdgeRequirement(t *testing.T, edge *workspace.RequiredAssessment) {
	t.Helper()

	expectEqual(t, "edge id", edge.ID, "edge-exposure")
	expectEqual(t, "edge assessment", edge.Assessment, "ASM-EDGE")
	expectEqual(t, "edge type", edge.Type, workspace.AssessmentTypeSecurity)
	expectEqual(t, "edge environment", edge.Environment, "production")
	expectEqual(t, "edge authority", edge.Authority, "security-review")
	expectEqual(t, "edge calm nodes", edge.Scope.CalmNodes, []string{"api-gateway"})
	expectEqual(t, "edge tags", edge.Scope.Tags, []string{"edge"})
	expectEqual(t, "edge node types are given", edge.Scope.NodeTypes != nil, true)
}

// expectQualityRequirement checks the second required assessment of the review workspace.
func expectQualityRequirement(t *testing.T, quality *workspace.RequiredAssessment) {
	t.Helper()

	expectEqual(t, "quality type", quality.Type, workspace.AssessmentTypeQuality)
	expectEqual(t, "quality calm nodes", quality.Scope.CalmNodes, []string{})
}

// Scenario: trust and assessments are valid workspace keys.
func TestLoadReviewKeys(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, reviewWorkspace, "")

	expectEqual(t, "trust", w.Trust, &workspace.Trust{Dimensions: []string{"tenant"}, Classifications: []string{"public", "internal", "restricted"}})
	expectEqual(t, "maxExpiryDays", w.Exceptions.MaxExpiryDays, 30)

	a := w.Assessments
	requireEqual(t, "assessments are given", a != nil, true)
	requireEqual(t, "trust anchor is given", a.TrustAnchor != nil, true)
	requireEqual(t, "trust anchor ref", a.TrustAnchor.Ref, "origin/main")
	requireEqual(t, "paths", a.Paths, []string{"assessments"})
	expectEqual(t, "analysisLimits", a.AnalysisLimits, workspace.AnalysisLimits{MaxDepth: 64, MaxVisited: 100000, MaxSeconds: 120})

	requireEqual(t, "authority count", len(a.Authorities), 3)
	expectReviewerAuthority(t, &a.Authorities[0])
	expectSignedAuthority(t, &a.Authorities[1])
	expectGovernanceAuthority(t, &a.Authorities[2])

	requireEqual(t, "required count", len(a.Required), 2)
	expectEdgeRequirement(t, &a.Required[0])
	expectQualityRequirement(t, &a.Required[1])
}

func TestLoadReviewKeysDefaultsAndAbsence(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, envHead, "")

	expectEqual(t, "trust is absent", w.Trust == nil, true)
	expectEqual(t, "assessments are absent", w.Assessments == nil, true)
	expectEqual(t, "maxExpiryDays", w.Exceptions.MaxExpiryDays, 0)

	w = mustLoadWorkspace(t, envHead+"assessments: {}\n", "")

	a := w.Assessments
	requireEqual(t, "assessments are given", a != nil, true)
	expectEqual(t, "paths", a.Paths, workspace.DefaultAssessmentPaths())
	expectEqual(t, "trust anchor is absent", a.TrustAnchor == nil, true)
	expectEqual(t, "authorities are given", a.Authorities != nil, true)
	expectEqual(t, "required are given", a.Required != nil, true)
	expectEqual(t, "analysisLimits", a.AnalysisLimits, workspace.AnalysisLimits{})
}

func TestLoadProfileMergesAssessments(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, reviewWorkspace, "ci")

	expectEqual(t, "trust anchor ref", w.Assessments.TrustAnchor.Ref, "refs/remotes/origin/main")
	expectEqual(t, "authority count", len(w.Assessments.Authorities), 3)
}

// Scenario: unknown keys and invalid values are rejected with exit 2.
func TestLoadReviewKeysInvalidGivesWRK002(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unknown trust key":              "trust:\n  zones: [a]\n",
		"unknown assessments key":        "assessments:\n  enforcement: mandatory\n",
		"assessments.limits":             "assessments:\n  limits: { maxDepth: 1 }\n",
		"unknown analysisLimits key":     "assessments:\n  analysisLimits: { maxNodes: 1 }\n",
		"zero analysisLimits value":      "assessments:\n  analysisLimits: { maxDepth: 0 }\n",
		"unknown trustAnchor key":        "assessments:\n  trustAnchor: { ref: main, depth: 1 }\n",
		"trustAnchor without ref":        "assessments:\n  trustAnchor: {}\n",
		"role outside the enum":          "assessments:\n  authorities:\n    - { id: a, role: approver }\n",
		"authority without role":         "assessments:\n  authorities:\n    - { id: a }\n",
		"mechanism outside the enum":     "assessments:\n  authorities:\n    - { id: a, role: reviewer, mechanism: forge }\n",
		"unknown authority key":          "assessments:\n  authorities:\n    - { id: a, role: reviewer, teams: [x] }\n",
		"signer with identity only":      "assessments:\n  authorities:\n    - { id: a, role: reviewer, mechanism: signed, signers: [{ identity: x }] }\n",
		"signer with key and identity":   "assessments:\n  authorities:\n    - { id: a, role: reviewer, mechanism: signed, signers: [{ identity: x, issuer: y, publicKey: k }] }\n",
		"required type outside enum":     "assessments:\n  required:\n    - { id: r, assessment: A, type: privacy, environment: e, authority: a }\n",
		"required without authority":     "assessments:\n  required:\n    - { id: r, assessment: A, type: security, environment: e }\n",
		"unknown required scope key":     "assessments:\n  required:\n    - { id: r, assessment: A, type: security, environment: e, authority: a, scope: { services: [s] } }\n",
		"maxExpiryDays zero":             "exceptions:\n  maxExpiryDays: 0\n",
		"maxExpiryDays not an integer":   "exceptions:\n  maxExpiryDays: soon\n",
		"unknown exceptions key":         "exceptions:\n  maxAge: 3\n",
		"duplicate classification":       "trust:\n  classifications: [a, a]\n",
		"dimension name with a space":    "trust:\n  dimensions: [\"my zone\"]\n",
		"unknown key in the profile":     "profiles:\n  ci:\n    trust:\n      bogus: 1\n",
		"trust dimensions not a list":    "trust:\n  dimensions: zone\n",
		"required scope tags not a list": "assessments:\n  required:\n    - { id: r, assessment: A, type: security, environment: e, authority: a, scope: { tags: edge } }\n",
	}

	for name, body := range cases {
		expectWRK002Case(t, name, loadFailure(t, envHead+body))
	}
}

func TestLoadDuplicateAuthorityAndRequiredIDsGiveWRK002(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"duplicate authority": "assessments:\n  authorities:\n    - { id: a, role: reviewer }\n    - { id: a, role: governance }\n",
		"duplicate required": "assessments:\n  required:\n" +
			"    - { id: r, assessment: A, type: security, environment: e, authority: a }\n" +
			"    - { id: r, assessment: B, type: quality, environment: e, authority: a }\n",
	}

	for name, body := range cases {
		de := loadFailure(t, envHead+body)

		expectWRK002Case(t, name, de)
		expectContains(t, name+" message", de.Diagnostic.Message, "appears two times")
	}
}

// The top-level `limits` key is the run limit of the foundation. It is unchanged and separate from
// `assessments.analysisLimits`.
func TestTopLevelLimitsUnchangedByAssessments(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, envHead+"limits:\n  traversal: {maxVisited: 7}\nassessments:\n  analysisLimits: {maxVisited: 99}\n", "")

	want := workspace.DefaultLimits()
	want.Traversal.MaxVisited = 7

	expectEqual(t, "limits", w.Limits, want)
	expectEqual(t, "analysisLimits maxVisited", w.Assessments.AnalysisLimits.MaxVisited, 99)

	de := loadFailure(t, envHead+"limits:\n  maxDepth: 3\n")

	expectEqual(t, "top-level limits.maxDepth exit code", de.ExitCode, diag.InvalidInput)
}

func TestBuiltinTrustDimensions(t *testing.T) {
	t.Parallel()

	expectEqual(t, "dimensions", workspace.BuiltinTrustDimensions(), []string{"organization", "account", "principal", "zone", "region"})
}
