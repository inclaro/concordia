package assessment_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
)

const securityAssessment = `# Edge exposure review.
apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-EDGE
version: 1
type: security
title: Edge exposure
scope:
  calmNodes: [api-gateway]
environment: production
subject:
  summary: Public entry points of the gateway
  dependencies:
    - kind: public-entry-point
      selector:
        calmNodes: [api-gateway]
assumptions: [ASM-EDGE-1]
evidence:
  - id: EV-1
    ref: docs/security/edge-review.md
outcome: satisfactory
state: ready-for-review
`

const qualityAssessment = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-LATENCY
version: 1
type: quality
scope:
  calmNodes: [checkout]
environment: production
subject:
  scenarios: [QS-LATENCY-1]
assumptions: []
evidence: []
outcome: inconclusive
`

const edgeAssumption = `apiVersion: concordia.io/v1alpha1
kind: Assumption
id: ASM-EDGE-1
statement: Only the gateway is reachable from the internet.
scope:
  calmNodes: [api-gateway]
owner: security-team
verificationMethod: review
selectors:
  - kind: public-entry-point
    selector:
      calmNodes: [api-gateway]
`

// workspace writes the files below a temporary workspace root and gives the root.
func workspace(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, files)

	return dir
}

// edgeFiles is a valid assessments tree.
func edgeFiles() map[string]string {
	return map[string]string{
		"assessments/ASM-EDGE/v1.yaml":            securityAssessment,
		"assessments/assumptions/ASM-EDGE-1.yaml": edgeAssumption,
	}
}

func loadEdge(t *testing.T, files map[string]string) (*assessment.Set, error) {
	t.Helper()

	return assessment.Load(workspace(t, files), []string{"assessments"})
}

// wantDiag fails unless err is a *diag.Error with the code, exit code 2, and a message part.
func wantDiag(t *testing.T, err error, code, part string) *diag.Error {
	t.Helper()

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want a *diag.Error %s", err, code)
	}

	if failure.Diagnostic.Code != code {
		t.Fatalf("code = %s (%s), want %s", failure.Diagnostic.Code, failure.Diagnostic.Message, code)
	}

	if failure.ExitCode != diag.InvalidInput {
		t.Errorf("exit code = %d, want %d", failure.ExitCode, diag.InvalidInput)
	}

	if !strings.Contains(failure.Diagnostic.Message, part) {
		t.Errorf("message = %q, want it to contain %q", failure.Diagnostic.Message, part)
	}

	return failure
}

// edit replaces old with repl in one file of the tree and fails when old is not there.
func edit(t *testing.T, files map[string]string, file, old, repl string) map[string]string {
	t.Helper()

	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}

	if !strings.Contains(out[file], old) {
		t.Fatalf("%s does not contain %q", file, old)
	}

	out[file] = strings.Replace(out[file], old, repl, 1)

	return out
}

const (
	digestA = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	digestB = "sha256:0000000000000000000000000000000000000000000000000000000000000002"
)

var decided = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

// approvalFor gives a complete approval of the loaded version.
func approvalFor(a *assessment.Assessment, id string, at time.Time) *assessment.Approval {
	return &assessment.Approval{
		ID:       id,
		Decision: assessment.DecisionApproved,
		Assessment: assessment.AssessmentRef{
			ID: a.ID, Version: a.Version, Digest: a.Digest,
		},
		Subject: assessment.ApprovalSubject{
			Digest: digestA,
			Dependencies: []assessment.Item{
				{Kind: assessment.DepPublicEntryPoint, Identity: "k8s:ingress/api", Digest: digestB},
			},
		},
		Scope:             a.Scope,
		Environment:       a.Environment,
		Assumptions:       []assessment.Ref{{ID: "ASM-EDGE-1", Digest: digestA}},
		Evidence:          []assessment.Ref{{ID: "EV-1", Digest: digestB}},
		ContractVersion:   "review-digest/1",
		CapabilityProfile: "cbm-0.12.1",
		Reviewer:          assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
		DecidedAt:         at,
		ProducedUnder: assessment.ProducedUnder{
			NormalizationVersion: "normalize/1",
			EvaluatorVersions:    map[string]string{"review-currency": "1.0.0"},
			PolicyDigest:         digestA,
			AuthorityDigest:      digestB,
		},
		Location: assessment.Location{File: "assessments/approvals/" + id + ".yaml"},
	}
}
