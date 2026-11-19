package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
)

const exportAssessment = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-BILL
version: 1
type: security
title: Billing review
scope:
  calmNodes: [billing-service]
environment: prod
subject:
  summary: The billing service
  dependencies:
    - kind: protocol
      selector:
        calmNodes: [billing-service]
assumptions: []
evidence:
  - id: EV-1
    ref: docs/security/gone.md
    digest: sha256:0000000000000000000000000000000000000000000000000000000000000009
  - id: EV-2
    ref: docs/security/token.md
outcome: satisfactory
state: ready-for-review
`

const exportReviewWorkspace = `environments:
  - {id: prod, sources: ["k8s/prod/**"]}
assessments:
  paths: [assessments]
  authorities:
    - {id: sec, role: reviewer, mechanism: protected-review, reviewers: [alice@example.com], paths: ["assessments/**"]}
  required:
    - {id: REQ-BILL, assessment: ASM-BILL, type: security, environment: prod, authority: sec}
`

// Scenario: OUT-04 approvals in bundle.
// Scenario: Reference no longer resolvable.
// The bundle holds the assessment version, the verification result, the authority used, and the digest contract; an
// evidence reference that cannot be resolved is not reproducible; no secret value appears.
func TestExportBundleRecordsAssessments(t *testing.T) {
	t.Parallel()

	const secret = "AKIAABCDEFGHIJKLMNOP"

	s := newChkSetup(t, map[string]string{
		"concordia.yaml":               chkWorkspaceText(exportReviewWorkspace),
		"assessments/ASM-BILL/v1.yaml": exportAssessment + "notes: \"key " + secret + "\"\n",
		"docs/security/token.md":       "present\n",
	}, nil)

	res := s.run(t, "export", "bundle")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	files := extracted(t, s)

	for _, want := range []string{"assessments/assessments/ASM-BILL/v1.yaml", "verification/assessments.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("bundle lacks %s", want)
		}
	}

	wantNoText(t, files, secret)
	wantExportVerification(t, files["verification/assessments.json"])
	wantExportReviewManifest(t, filepath.Join(s.dir, "build", "concordia", "conformance.tar.gz"))
}

// exportNotReproducible is an evidence reference of verification/assessments.json that cannot be resolved.
type exportNotReproducible struct {
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

// exportVerification is verification/assessments.json of a bundle.
type exportVerification struct {
	ContractVersion string `json:"contractVersion"`
	AuthorityDigest string `json:"authorityDigest"`
	Authorities     []struct {
		ID string `json:"id"`
	} `json:"authorities"`
	Assessments []struct {
		ID       string `json:"id"`
		Currency string `json:"currency"`
	} `json:"assessments"`
	NotReproducible []exportNotReproducible `json:"notReproducible"`
}

// hasExpectedAuthority says whether the file has the digest contract and one authority with its digest.
func (v *exportVerification) hasExpectedAuthority() bool {
	return v.ContractVersion == "review-digest/1" && v.AuthorityDigest != "" && len(v.Authorities) == 1
}

// hasExpectedAssessment says whether the file has the one assessment ASM-BILL.
func (v *exportVerification) hasExpectedAssessment() bool {
	return len(v.Assessments) == 1 && v.Assessments[0].ID == "ASM-BILL"
}

// wantExportVerification checks the authority, the assessment, and the unresolved evidence of the verification file.
func wantExportVerification(t *testing.T, text string) {
	t.Helper()

	var verification exportVerification
	if err := json.Unmarshal([]byte(text), &verification); err != nil {
		t.Fatal(err)
	}

	if !verification.hasExpectedAuthority() || !verification.hasExpectedAssessment() {
		t.Errorf("verification = %+v", verification)
	}

	if !slices.Equal(verification.NotReproducible, []exportNotReproducible{{Evidence: "EV-1", Reason: "missing"}}) {
		t.Errorf("notReproducible = %+v", verification.NotReproducible)
	}
}

// wantExportReviewManifest checks the review provenance, the reproduction, and the references of the bundle manifest.
func wantExportReviewManifest(t *testing.T, bundlePath string) {
	t.Helper()

	manifest, err := bundle.Open(bundlePath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if manifest.Provenance.Review == nil || manifest.Provenance.Review.ContractVersion != "review-digest/1" {
		t.Errorf("provenance review = %+v", manifest.Provenance.Review)
	}

	if manifest.Reproduction.Available || !strings.Contains(strings.Join(manifest.Reproduction.Unavailable, ","), "assessment-evidence") {
		t.Errorf("reproduction = %+v, want assessment-evidence not reproducible", manifest.Reproduction)
	}

	marked := slices.ContainsFunc(manifest.References, func(r bundle.Reference) bool {
		return r.Kind == "assessment-evidence" && r.Reason == bundle.ReasonNotReproducible
	})
	if !marked {
		t.Errorf("references = %+v", manifest.References)
	}
}

func TestExportBundleWithoutAssessmentsHasNoReviewFiles(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	if res := s.run(t, "export", "bundle"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for name := range extracted(t, s) {
		if hasReviewFilePrefix(name) {
			t.Errorf("bundle holds %s without assessments", name)
		}
	}
}

// hasReviewFilePrefix says whether a bundle file is one of the assessment, approval, or verification files.
func hasReviewFilePrefix(name string) bool {
	return slices.ContainsFunc([]string{"assessments/", "approvals/", "verification/"}, func(prefix string) bool {
		return strings.HasPrefix(name, prefix)
	})
}
