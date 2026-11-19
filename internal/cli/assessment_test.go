package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
)

const assessmentWorkspaceYAML = "metadata: {name: t}\nobservation:\n  projects:\n    - {name: t, root: .}\n"

const assessmentDocEdge = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-EDGE
version: 1
type: security
scope:
  calmNodes: [api-gateway]
environment: production
subject:
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

const assessmentDocAssumption = `apiVersion: concordia.io/v1alpha1
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

const assessmentDocQuality = `apiVersion: concordia.io/v1alpha1
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

func assessmentTree(extra map[string]string) map[string]string {
	files := map[string]string{
		"concordia.yaml":                          assessmentWorkspaceYAML,
		"assessments/ASM-EDGE/v1.yaml":            assessmentDocEdge,
		"assessments/assumptions/ASM-EDGE-1.yaml": assessmentDocAssumption,
		"assessments/ASM-LATENCY/v1.yaml":         assessmentDocQuality,
	}

	for k, v := range extra {
		files[k] = v
	}

	return files
}

func runAssessment(t *testing.T, files map[string]string, args ...string) result {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, files)

	return runWith(t, &runConfig{Dir: dir}, append([]string{"assessment", "validate"}, args...)...)
}

// Scenario: Valid security assessment.
func TestAssessmentValidateAcceptsValidFiles(t *testing.T) {
	t.Parallel()

	res := runAssessment(t, assessmentTree(nil))
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, want := range []string{"2 version(s)", "1 assumption(s)", "ASM-EDGE@v1", "ASM-LATENCY@v1", "authored state ready-for-review"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, res.Stdout)
		}
	}
}

func TestAssessmentValidateWithoutAnyDocumentExitsZero(t *testing.T) {
	t.Parallel()

	res := runAssessment(t, map[string]string{"concordia.yaml": assessmentWorkspaceYAML})
	if res.Code != 0 || !strings.Contains(res.Stdout, "no documents") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestAssessmentValidateUsesTheConfiguredPaths(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"concordia.yaml":       assessmentWorkspaceYAML + "assessments:\n  paths: [reviews]\n",
		"reviews/quality.yaml": assessmentDocQuality,
		"assessments/bad.yaml": "kind: nope\n", // outside the configured paths.
	}

	if res := runAssessment(t, files); res.Code != 0 || !strings.Contains(res.Stdout, "ASM-LATENCY@v1") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	files["concordia.yaml"] = assessmentWorkspaceYAML + "assessments:\n  paths: [reviews, missing]\n"

	res := runAssessment(t, files)
	if res.Code != 2 || !stderrNames(res.Stderr, "CO-ASM-001", "missing") {
		t.Errorf("missing path: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// The invalid cases of the load and state requirements each give exit 2 and a CO-ASM diagnostic.
func TestAssessmentValidateRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		file string
		text string
		code string
	}{
		{
			name: "missing field",
			file: "assessments/ASM-EDGE/v1.yaml",
			text: strings.Replace(assessmentDocEdge, "environment: production\n", "", 1),
			code: "CO-ASM-001",
		},
		{
			name: "unknown key",
			file: "assessments/ASM-EDGE/v1.yaml",
			text: strings.Replace(assessmentDocEdge, "outcome: satisfactory\n", "outcome: satisfactory\nsigned-off: true\n", 1),
			code: "CO-ASM-001",
		},
		{
			name: "unknown assumption reference",
			file: "assessments/ASM-EDGE/v1.yaml",
			text: strings.Replace(assessmentDocEdge, "[ASM-EDGE-1]", "[ASM-EDGE-1, ASM-GONE-2]", 1),
			code: "CO-ASM-001",
		},
		{
			name: "assumption with neither selector nor evidence",
			file: "assessments/assumptions/ASM-EDGE-1.yaml",
			text: strings.Replace(assessmentDocAssumption, "selectors:\n  - kind: public-entry-point\n    selector:\n      calmNodes: [api-gateway]\n", "", 1),
			code: "CO-ASM-001",
		},
		{
			name: "duplicate id and version",
			file: "assessments/ASM-EDGE/copy.yaml",
			text: assessmentDocEdge,
			code: "CO-ASM-001",
		},
		{
			name: "authored approved state",
			file: "assessments/ASM-EDGE/v1.yaml",
			text: strings.Replace(assessmentDocEdge, "state: ready-for-review", "state: approved", 1),
			code: "CO-ASM-003",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := runAssessment(t, assessmentTree(map[string]string{tc.file: tc.text}))
			if res.Code != 2 {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
			}

			if !strings.Contains(res.Stderr, tc.code) || !strings.Contains(res.Stderr, "assessments/") {
				t.Errorf("stderr = %q, want %s with a file", res.Stderr, tc.code)
			}
		})
	}
}

// Scenario: Edit of an approved version.
func TestAssessmentValidateRejectsAnEditedApprovedVersion(t *testing.T) {
	t.Parallel()

	approval := `apiVersion: concordia.io/v1alpha1
kind: AssessmentApproval
id: ASM-EDGE-v1-1
decision: approved
assessment:
  id: ASM-EDGE
  version: 1
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000001
subject:
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000002
  dependencies: []
scope:
  calmNodes: [api-gateway]
environment: production
assumptions: []
evidence: []
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
`

	res := runAssessment(t, assessmentTree(map[string]string{"assessments/approvals/ASM-EDGE-v1-1.yaml": approval}))
	if res.Code != 2 || !stderrNames(res.Stderr, "CO-ASM-002", "sha256:0000000000000000000000000000000000000000000000000000000000000001") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// assessmentCanonicalReport is the JSON report of `assessment validate` without diagnostics.
type assessmentCanonicalReport struct {
	Assessments []struct {
		ID            string `json:"id"`
		AuthoredState string `json:"authoredState"`
		Digest        string `json:"digest"`
		Version       int    `json:"version"`
	} `json:"assessments"`
	Approvals   []any `json:"approvals"`
	Diagnostics []any `json:"diagnostics"`
	OK          bool  `json:"ok"`
}

// wantReportChecks reports an error unless every check of the JSON report holds.
func wantReportChecks(t *testing.T, report any, checks ...bool) {
	t.Helper()

	if slices.Contains(checks, false) {
		t.Errorf("report = %+v", report)
	}
}

func TestAssessmentValidateJSONIsCanonicalAndStable(t *testing.T) {
	t.Parallel()

	files := assessmentTree(nil)

	first := runAssessment(t, files, "--json")
	second := runAssessment(t, files, "--json")

	if first.Code != 0 || first.Stdout != second.Stdout {
		t.Fatalf("exit code = %d, outputs equal = %v, stderr = %q", first.Code, first.Stdout == second.Stdout, first.Stderr)
	}

	var report assessmentCanonicalReport

	if err := json.Unmarshal([]byte(first.Stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, first.Stdout)
	}

	if len(report.Assessments) != 2 {
		t.Fatalf("report = %+v", report)
	}

	edge := report.Assessments[0]
	wantReportChecks(t, report, report.OK, edge.ID == "ASM-EDGE", edge.AuthoredState == "ready-for-review",
		strings.HasPrefix(edge.Digest, "sha256:"), report.Approvals != nil, report.Diagnostics != nil)

	if strings.Contains(first.Stdout, `"state"`) {
		t.Errorf("the report shows a review state for an unverified record:\n%s", first.Stdout)
	}
}

// assessmentDiagnosticsReport is the JSON report of `assessment validate` that holds diagnostics.
type assessmentDiagnosticsReport struct {
	Diagnostics []struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Source   struct {
			URI  string `json:"uri"`
			Line int    `json:"line"`
		} `json:"source"`
	} `json:"diagnostics"`
	OK bool `json:"ok"`
}

func TestAssessmentValidateJSONReportsDiagnostics(t *testing.T) {
	t.Parallel()

	files := assessmentTree(map[string]string{
		"assessments/ASM-EDGE/v1.yaml": strings.Replace(assessmentDocEdge, "environment: production\n", "", 1),
	})

	res := runAssessment(t, files, "--json")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var report assessmentDiagnosticsReport

	if err := json.Unmarshal([]byte(res.Stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, res.Stdout)
	}

	if len(report.Diagnostics) != 1 {
		t.Fatalf("report = %+v", report)
	}

	diagnostic := report.Diagnostics[0]
	wantReportChecks(t, report, !report.OK, diagnostic.Code == "CO-ASM-001", diagnostic.Severity == "error",
		diagnostic.Source.URI == "assessments/ASM-EDGE/v1.yaml", diagnostic.Source.Line != 0)
}

func TestAssessmentValidateNeverWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, assessmentTree(nil))

	res := runWith(t, &runConfig{Dir: dir}, "assessment", "validate")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, p := range []string{"build", ".concordia"} {
		if exists(dir, p) {
			t.Errorf("validate created %s", p)
		}
	}
}
