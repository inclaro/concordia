package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The documented steps of docs/how-to/review-and-approve-assessments.md on a temporary git repository (a copy of
// test/fixtures/infra with the "trace" deployment workspace), for each mechanism. The gate is the rule REV-APR-001
// of the `review-currency` pack: it passes only after the documented approval step.

const (
	reviewProjectPrefix = "e2e-review-"
	reviewRule          = "REV-APR-001"
	reviewRecord        = "assessments/approvals/ASM-EDGE-v1-1.yaml"
	reviewAssessment    = "assessments/ASM-EDGE/v1.yaml"
	reviewBase          = "main"

	// reviewEdgeDocument assesses the runtime identity of the api-service workloads in production.
	reviewEdgeDocument = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-EDGE
version: 1
type: security
title: Runtime identity of the API service
scope:
  calmNodes: [api-service]
environment: production
subject:
  summary: The identity that the API service runs under in production
  dependencies:
    - kind: runtime-identity
      selector:
        calmNodes: [api-service]
assumptions: []
evidence: []
outcome: satisfactory
state: ready-for-review
`

	reviewProtectedAuthority = `assessments:
  paths: [assessments]
  trustAnchor: { ref: main }
  authorities:
    - id: security-review
      role: reviewer
      mechanism: protected-review
      reviewers: [alice@example.com]
      paths: [assessments/approvals/**, assessments/revocations/**]
  required:
    - { id: edge, assessment: ASM-EDGE, type: security, scope: { calmNodes: [api-service] }, environment: production, authority: security-review }
`

	reviewSignedAuthority = `assessments:
  paths: [assessments]
  trustAnchor: { ref: main }
  authorities:
    - id: platform-signers
      role: reviewer
      mechanism: signed
      signers:
        - { identity: alice@example.com, issuer: "https://accounts.example.com" }
  required:
    - { id: edge, assessment: ASM-EDGE, type: security, scope: { calmNodes: [api-service] }, environment: production, authority: platform-signers }
`
)

var reviewDigestRe = regexp.MustCompile(`Subject digest: (sha256:[0-9a-f]{64})`)

// newReviewRepo makes the infra repository with the review workspace on the base branch, and commits the assessment
// version (the state before any approval). It then switches to a candidate branch.
func newReviewRepo(t *testing.T, name, authority string) *repo {
	t.Helper()

	project := reviewProjectPrefix + name
	c := deploymentCases()[0]
	files := c.files(project)

	text := strings.Replace(files["concordia.yaml"], "    - use: builtin:infrastructure\n",
		"    - use: builtin:infrastructure\n    - use: builtin:review-currency\n", 1)
	if text == files["concordia.yaml"] {
		t.Fatal("the deployment workspace has no policy pack list to extend")
	}

	files["concordia.yaml"] = text + authority
	files[reviewAssessment] = reviewEdgeDocument

	r := newDeploymentRepo(t, project, name, files)

	// The base branch is the trust anchor; the candidate branch is where the work happens.
	r.git("switch", "-q", "-c", "candidate")

	if res := r.gu("index"); res.code != 0 {
		t.Fatalf("index (%d): %s", res.code, res.stderr)
	}

	return r
}

// reviewCI makes sure that the CI variable of the host does not reach the commands: approve refuses to run in CI.
var reviewCI = []string{"CI="}

// gate gives the verdict of the review rule and the diagnostic codes of a check run.
func (r *repo) gate(env ...string) (verdict string, codes []string) {
	r.t.Helper()

	doc, res := r.checkJSON(append(append([]string{}, reviewCI...), env...))

	rule := doc.rule(reviewRule)
	if rule == nil {
		r.t.Fatalf("check has no %s result (exit %d): %s", reviewRule, res.code, res.stderr)
	}

	for _, d := range doc.Diagnostics {
		codes = append(codes, d.Code)
	}

	return rule.Verdict, codes
}

// examine runs the first documented approve step and gives the subject digest that it prints.
func (r *repo) examine(authority string, env ...string) string {
	r.t.Helper()

	args := []string{"assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com"}
	if authority != "" {
		args = append(args, "--authority", authority)
	}

	res := r.cli(append(append([]string{}, reviewCI...), env...), args...)
	if res.code != 2 || !strings.Contains(res.stderr, "CO-ASM-005") {
		r.t.Fatalf("approve without --confirm-subject: exit %d, want 2 with CO-ASM-005: %s", res.code, res.output())
	}

	if _, err := os.Stat(r.path(reviewRecord)); err == nil {
		r.t.Fatal("approve without --confirm-subject wrote a record")
	}

	m := reviewDigestRe.FindStringSubmatch(res.stdout)
	if m == nil {
		r.t.Fatalf("approve printed no subject digest:\n%s", res.stdout)
	}

	return m[1]
}

func TestReviewAuthorityProtectedReview(t *testing.T) {
	requireE2E(t)

	r := newReviewRepo(t, "protected", reviewProtectedAuthority)

	// Before the approval: the gate is not satisfied. The assessment exists on the anchor, so no anchor problem.
	if verdict, _ := r.gate(); verdict != "violation" {
		t.Fatalf("before the approval: %s verdict = %q, want violation", reviewRule, verdict)
	}

	// Step 1 and 2: examine the subject, then write the record with the digest.
	digest := r.examine("")

	res := r.cli(reviewCI, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com", "--confirm-subject", digest)
	if res.code != 0 || !strings.Contains(res.stdout, "approval written: "+reviewRecord) {
		t.Fatalf("approve (%d): %s", res.code, res.output())
	}

	record := r.read(reviewRecord)

	// The record is only in the candidate: it is not applied and the gate is still not satisfied.
	verdict, codes := r.gate()
	if verdict != "violation" || !contains(codes, "CO-ASM-006") {
		t.Fatalf("record only in the candidate: verdict %q, diagnostics %v; want violation with CO-ASM-006", verdict, codes)
	}

	// Step 3: the record goes to the anchor, with the assessment version, and nothing else.
	if err := os.Remove(r.path(reviewRecord)); err != nil {
		t.Fatal(err)
	}

	r.git("switch", "-q", reviewBase)
	r.write(reviewRecord, record)
	r.commitAll("docs: approve ASM-EDGE v1")

	// Step 4: the candidate is rebuilt on the anchor.
	r.git("switch", "-q", "candidate")
	r.git("merge", "-q", "--no-edit", reviewBase)

	if res := r.gu("index"); res.code != 0 {
		t.Fatalf("index after the merge (%d): %s", res.code, res.stderr)
	}

	if verdict, codes := r.gate(); verdict != "pass" {
		t.Fatalf("after the approval is on the anchor: %s verdict = %q (diagnostics %v), want pass", reviewRule, verdict, codes)
	}

	// A change to the assessed subject makes the approval stale again.
	r.write("k8s/prod/deployment.yaml", strings.Replace(r.read("k8s/prod/deployment.yaml"), "serviceAccountName: api-sa", "serviceAccountName: api-sa-v2", 1))
	r.commitAll("feat: change the API service account")

	if res := r.gu("index"); res.code != 0 {
		t.Fatalf("index (%d): %s", res.code, res.stderr)
	}

	if verdict, _ := r.gate(); verdict != "violation" {
		t.Errorf("after the subject changed: %s verdict = %q, want violation", reviewRule, verdict)
	}

	// CI refuses to record a decision.
	if res := r.cli([]string{"CI=true"}, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com", "--confirm-subject", digest); res.code != 2 ||
		!strings.Contains(res.stderr, "CO-ASM-005") {
		t.Errorf("approve in CI: exit %d: %s", res.code, res.output())
	}
}

// fakeReviewCosign writes a cosign that signs by writing a bundle and verifies only the identity alice@example.com.
func fakeReviewCosign(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	script := filepath.Join(t.TempDir(), "cosign")
	text := `#!/bin/sh
cmd="$1"
id=""
while [ $# -gt 0 ]; do
  case "$1" in
    --bundle) if [ "$cmd" = "sign-blob" ]; then echo '{}' > "$2"; fi;;
    --certificate-identity) id="$2";;
  esac
  shift
done
if [ "$cmd" = "verify-blob" ] && [ "$id" != "alice@example.com" ]; then exit 1; fi
exit 0
`

	if err := os.WriteFile(script, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}

	return script
}

func TestReviewAuthoritySigned(t *testing.T) {
	requireE2E(t)

	r := newReviewRepo(t, "signed", reviewSignedAuthority)
	env := []string{"COSIGN=" + fakeReviewCosign(t)}

	if verdict, _ := r.gate(env...); verdict != "violation" {
		t.Fatalf("before the approval: %s verdict = %q, want violation", reviewRule, verdict)
	}

	digest := r.examine("", env...)

	// The record is signed. It stays in the candidate: the signature, not the location, proves the authority.
	res := r.cli(append(append([]string{}, reviewCI...), env...), "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--confirm-subject", digest, "--sign", "--sign-key", "review.key")
	if res.code != 0 || !strings.Contains(res.stdout, "signature written: "+reviewRecord+".sigstore.json") {
		t.Fatalf("approve --sign (%d): %s", res.code, res.output())
	}

	if verdict, codes := r.gate(env...); verdict != "pass" {
		t.Fatalf("after the signed approval: %s verdict = %q (diagnostics %v), want pass", reviewRule, verdict, codes)
	}

	// Without the bundle the record is not applied.
	bundle := reviewRecord + ".sigstore.json"
	saved := r.read(bundle)

	if err := os.Remove(r.path(bundle)); err != nil {
		t.Fatal(err)
	}

	if verdict, codes := r.gate(env...); verdict != "violation" || !contains(codes, "CO-ASM-006") {
		t.Errorf("without the bundle: verdict %q, diagnostics %v; want violation with CO-ASM-006", verdict, codes)
	}

	r.write(bundle, saved)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}

	return false
}
