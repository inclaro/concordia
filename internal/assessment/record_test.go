package assessment_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
)

const approvalYAML = `apiVersion: concordia.io/v1alpha1
kind: AssessmentApproval
id: ASM-EDGE-v1-1
decision: approved
assessment:
  id: ASM-EDGE
  version: 1
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000001
subject:
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000002
  dependencies:
    - kind: public-entry-point
      identity: k8s:ingress/api
      digest: sha256:0000000000000000000000000000000000000000000000000000000000000003
scope:
  calmNodes: [api-gateway]
environment: production
assumptions:
  - id: ASM-EDGE-1
    digest: sha256:0000000000000000000000000000000000000000000000000000000000000004
evidence:
  - id: EV-1
    digest: sha256:0000000000000000000000000000000000000000000000000000000000000005
contractVersion: review-digest/1
capabilityProfile: cbm-0.12.1
reviewer:
  identity: alice@example.com
  authority: security-review
decidedAt: 2026-05-01T10:00:00Z
producedUnder:
  normalizationVersion: normalize/1
  evaluatorVersions:
    review-currency: 1.0.0
  policyDigest: sha256:0000000000000000000000000000000000000000000000000000000000000006
  authorityDigest: sha256:0000000000000000000000000000000000000000000000000000000000000007
expiry:
  expiresAt: 2027-05-01T10:00:00Z
  conditions:
    - a new public route is added
`

// Scenario: Complete approval record.
func TestParseCompleteApproval(t *testing.T) {
	t.Parallel()

	a := parseApprovalText(t, "assessments/approvals/ASM-EDGE-v1-1.yaml", approvalYAML)

	recordAssertApprovalCore(t, a)
	recordAssertApprovalDigest(t, a)
}

// Scenario: Approval missing the environment.
func TestAnApprovalWithoutAnEnvironmentFails(t *testing.T) {
	t.Parallel()

	text := strings.Replace(approvalYAML, "environment: production\n", "", 1)

	_, err := assessment.ParseApproval("a.yaml", []byte(text))
	_ = wantDiag(t, err, diag.ASM001, "environment")

	text = strings.Replace(approvalYAML, "environment: production", "environment: \"\"", 1)
	_, err = assessment.ParseApproval("a.yaml", []byte(text))
	_ = wantDiag(t, err, diag.ASM001, "/environment")

	// The same check protects a record that Concordia writes.
	a := approvalFor(loadedEdge(t), "R-1", decided)
	a.Environment = ""

	if _, err := assessment.RenderApproval(a); err == nil {
		t.Error("RenderApproval accepted a record without an environment")
	}
}

func TestEveryBoundFieldIsRequired(t *testing.T) {
	t.Parallel()

	for _, field := range []string{
		"id", "decision", "assessment", "subject", "scope", "environment", "assumptions", "evidence",
		"contractVersion", "capabilityProfile", "reviewer", "decidedAt", "producedUnder",
	} {
		values := recordApprovalValues(t, approvalYAML)
		delete(values, field)

		_, err := assessment.ParseApproval("a.yaml", []byte(yamlOf(values)))
		_ = wantDiag(t, err, diag.ASM001, field)
	}

	for name, repl := range map[string][2]string{
		"assessment digest":   {"  digest: sha256:0000000000000000000000000000000000000000000000000000000000000001\n", ""},
		"subject digest":      {"  digest: sha256:0000000000000000000000000000000000000000000000000000000000000002\n", ""},
		"dependency digest":   {"      digest: sha256:0000000000000000000000000000000000000000000000000000000000000003\n", ""},
		"assumption digest":   {"    digest: sha256:0000000000000000000000000000000000000000000000000000000000000004\n", ""},
		"evidence digest":     {"    digest: sha256:0000000000000000000000000000000000000000000000000000000000000005\n", ""},
		"reviewer identity":   {"  identity: alice@example.com\n", ""},
		"reviewer authority":  {"  authority: security-review\n", ""},
		"digest not a digest": {"sha256:0000000000000000000000000000000000000000000000000000000000000004", "md5:abc"},
		"decision time":       {"decidedAt: 2026-05-01T10:00:00Z", "decidedAt: yesterday"},
		"unknown key":         {"decision: approved\n", "decision: approved\nreviewed: true\n"},
	} {
		text := strings.Replace(approvalYAML, repl[0], repl[1], 1)
		if text == approvalYAML {
			t.Fatalf("%s: the replacement did nothing", name)
		}

		if _, err := assessment.ParseApproval("a.yaml", []byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// yamlOf renders a mapping as JSON, which is YAML.
func yamlOf(values map[string]any) string {
	data, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}

	return string(data)
}

// Scenario: digest is stable under key reordering.
func TestApprovalDigestIsStableUnderKeyReordering(t *testing.T) {
	t.Parallel()

	reordered := `kind: AssessmentApproval
apiVersion: concordia.io/v1alpha1
decidedAt: "2026-05-01T10:00:00Z"
producedUnder:
  authorityDigest: sha256:0000000000000000000000000000000000000000000000000000000000000007
  policyDigest: sha256:0000000000000000000000000000000000000000000000000000000000000006
  evaluatorVersions: {review-currency: 1.0.0}
  normalizationVersion: normalize/1
reviewer: {authority: security-review, identity: alice@example.com}
capabilityProfile: cbm-0.12.1
contractVersion: review-digest/1
evidence: [{digest: sha256:0000000000000000000000000000000000000000000000000000000000000005, id: EV-1}]
assumptions:
  - {digest: sha256:0000000000000000000000000000000000000000000000000000000000000004, id: ASM-EDGE-1}
expiry:
  conditions: [a new public route is added]
  expiresAt: "2027-05-01T10:00:00Z"
environment: production
scope: {calmNodes: [api-gateway]}
subject:
  dependencies:
    - {identity: k8s:ingress/api, kind: public-entry-point, digest: sha256:0000000000000000000000000000000000000000000000000000000000000003}
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000002
assessment:
  version: 1
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000001
  id: ASM-EDGE
decision: approved
id: ASM-EDGE-v1-1
`

	first := parseApprovalText(t, "a.yaml", approvalYAML)
	second := parseApprovalText(t, "b.yaml", reordered)

	if first.Digest != second.Digest || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Errorf("digests differ: %s vs %s", first.Digest, second.Digest)
	}

	// A content change changes it.
	changed := parseApprovalText(t, "c.yaml", strings.Replace(approvalYAML, "environment: production", "environment: staging", 1))
	if changed.Digest == first.Digest {
		t.Error("the digest ignored a content change")
	}

	// A rendered record has the same digest as the record that was rendered.
	again := parseApprovalText(t, "d.yaml", string(recordRenderApproval(t, first)))
	if again.Digest != first.Digest {
		t.Errorf("render round trip changed the digest: %s vs %s", again.Digest, first.Digest)
	}
}

func TestQuotedAndUnquotedTimestampsGiveTheSameRecord(t *testing.T) {
	t.Parallel()

	quoted := strings.Replace(approvalYAML, "decidedAt: 2026-05-01T10:00:00Z", `decidedAt: "2026-05-01T10:00:00Z"`, 1)

	a, err := assessment.ParseApproval("a.yaml", []byte(approvalYAML))
	if err != nil {
		t.Fatal(err)
	}

	b, err := assessment.ParseApproval("b.yaml", []byte(quoted))
	if err != nil {
		t.Fatal(err)
	}

	if a.Digest != b.Digest {
		t.Errorf("digests differ: %s vs %s", a.Digest, b.Digest)
	}
}

func TestApprovalSemanticChecks(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		old, repl, part string
	}{
		"expiry before the decision": {"expiresAt: 2027-05-01T10:00:00Z", "expiresAt: 2026-04-01T10:00:00Z", "not after decidedAt"},
		"duplicate dependency item": {
			"      digest: sha256:0000000000000000000000000000000000000000000000000000000000000003\n",
			"      digest: sha256:0000000000000000000000000000000000000000000000000000000000000003\n" +
				"    - kind: public-entry-point\n      identity: k8s:ingress/api\n      digest: sha256:0000000000000000000000000000000000000000000000000000000000000003\n",
			"listed twice",
		},
		"duplicate assumption": {
			"    digest: sha256:0000000000000000000000000000000000000000000000000000000000000004\n",
			"    digest: sha256:0000000000000000000000000000000000000000000000000000000000000004\n" +
				"  - id: ASM-EDGE-1\n    digest: sha256:0000000000000000000000000000000000000000000000000000000000000004\n",
			"bound twice",
		},
		"previous without delta": {"decidedAt: 2026-05-01T10:00:00Z\n", "decidedAt: 2026-05-01T10:00:00Z\nprevious: {approval: X-1, subjectDigest: " + digestA + "}\n", "delta"},
		"previous names the record": {
			"decidedAt: 2026-05-01T10:00:00Z\n",
			"decidedAt: 2026-05-01T10:00:00Z\nprevious: {approval: ASM-EDGE-v1-1, subjectDigest: " + digestA + "}\ndelta: {added: [], removed: [], modified: []}\n",
			"names the record itself",
		},
	} {
		_, err := assessment.ParseApproval("a.yaml", []byte(strings.Replace(approvalYAML, tc.old, tc.repl, 1)))
		if err == nil || !strings.Contains(err.Error(), tc.part) {
			t.Errorf("%s: error = %v, want it to contain %q", name, err, tc.part)
		}
	}
}

func TestIncrementalApprovalCarriesTheDelta(t *testing.T) {
	t.Parallel()

	text := strings.Replace(approvalYAML, "decidedAt: 2026-05-01T10:00:00Z\n", `decidedAt: 2026-05-01T10:00:00Z
previous:
  approval: ASM-EDGE-v1-0
  subjectDigest: `+digestA+`
delta:
  added:
    - kind: public-entry-point
      identity: k8s:ingress/api
      digest: `+digestB+`
  removed: []
  modified:
    - kind: role-binding
      identity: k8s:rolebinding/api
      previousDigest: `+digestA+`
      digest: `+digestB+`
`, 1)

	a := parseApprovalText(t, "a.yaml", text)

	recordAssertDelta(t, a)

	// The record survives the render round trip with its delta.
	back, err := assessment.ParseApproval("b.yaml", recordRenderApproval(t, a))
	if err != nil || back.Digest != a.Digest {
		t.Errorf("round trip: %v", err)
	}
}

const revocationYAML = `apiVersion: concordia.io/v1alpha1
kind: AssessmentRevocation
id: ASM-EDGE-v1-1
approval:
  id: ASM-EDGE-v1-1
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000001
reviewer:
  identity: alice@example.com
  authority: security-review
decidedAt: 2026-06-01T10:00:00Z
reason: The gateway moved to another zone.
`

func TestParseRevocation(t *testing.T) {
	t.Parallel()

	r, err := assessment.ParseRevocation("assessments/revocations/ASM-EDGE-v1-1.yaml", []byte(revocationYAML))
	if err != nil {
		t.Fatalf("ParseRevocation: %v", err)
	}

	recordAssertRevocation(t, r)

	for _, field := range []string{"approval", "reviewer", "decidedAt", "reason", "id"} {
		doc, _ := r.Doc()
		delete(doc, field)

		_, err := assessment.ParseRevocation("r.yaml", []byte(yamlOf(doc)))
		_ = wantDiag(t, err, diag.ASM001, field)
	}

	again, err := assessment.ParseRevocation("r.yaml", recordRenderRevocation(t, r))
	if err != nil || again.Digest != r.Digest {
		t.Errorf("round trip: %v", err)
	}
}

func TestARevocationOfAnUnknownApprovalFails(t *testing.T) {
	t.Parallel()

	files := edgeFiles()
	files["assessments/revocations/ASM-EDGE-v1-1.yaml"] = revocationYAML

	_, err := loadEdge(t, files)
	_ = wantDiag(t, err, diag.ASM001, "names the approval")
}

func TestRecordsLoadFromTheLayout(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, recordRevokedLayout(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	recordAssertRevokedSet(t, set)

	for want, got := range map[string]string{
		"assessments/ASM-EDGE/v3.yaml":               assessment.AssessmentPath("assessments", "ASM-EDGE", 3),
		"assessments/assumptions/ASM-EDGE-1.yaml":    assessment.AssumptionPath("assessments", "ASM-EDGE-1"),
		"assessments/approvals/ASM-EDGE-v1-2.yaml":   assessment.ApprovalPath("assessments", "ASM-EDGE", 1, 2),
		"assessments/revocations/ASM-EDGE-v1-2.yaml": assessment.RevocationPath("assessments", "ASM-EDGE-v1-2"),
	} {
		if got != want {
			t.Errorf("path = %s, want %s", got, want)
		}
	}
}

func TestEveryProducedUnderFieldIsRequired(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"normalizationVersion", "evaluatorVersions", "policyDigest", "authorityDigest"} {
		recordAssertProducedUnderRequired(t, field)
	}

	// An empty evaluator mapping is allowed, a digest that is not a sha256 digest is not.
	empty := strings.Replace(approvalYAML, "  evaluatorVersions:\n    review-currency: 1.0.0\n", "  evaluatorVersions: {}\n", 1)
	if _, err := assessment.ParseApproval("a.yaml", []byte(empty)); err != nil {
		t.Errorf("empty evaluatorVersions: %v", err)
	}

	bad := strings.Replace(approvalYAML, "policyDigest: sha256:0000000000000000000000000000000000000000000000000000000000000006", "policyDigest: md5:abc", 1)
	if _, err := assessment.ParseApproval("a.yaml", []byte(bad)); err == nil {
		t.Error("a policyDigest that is not sha256 was accepted")
	}

	// The values are bound by the record digest, and a record without evaluators renders and loads again.
	recordAssertProducedUnderBound(t)
}
