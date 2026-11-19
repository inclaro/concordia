package assessment_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/trust"
)

// Scenario: Valid security assessment.
func TestLoadValidSecurityAssessment(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(set.Assessments) != 1 || len(set.Assumptions) != 1 {
		t.Fatalf("loaded %d assessments and %d assumptions", len(set.Assessments), len(set.Assumptions))
	}

	a := set.Assessment("ASM-EDGE", 1)
	if a == nil {
		t.Fatal("assessment ASM-EDGE@v1 not loaded")
	}

	loadCheckEdgeAssessment(t, a)

	if set.Assumption("ASM-EDGE-1") == nil {
		t.Error("assumption ASM-EDGE-1 not loaded")
	}

	if len(set.Files) != 2 || set.Files[0].Kind != assessment.KindAssessment {
		t.Errorf("files = %+v", set.Files)
	}
}

// loadCheckEdgeAssessment checks the authored fields, the location, and the digest of the loaded edge assessment.
func loadCheckEdgeAssessment(t *testing.T, a *assessment.Assessment) {
	t.Helper()

	gotFields := fmt.Sprintf("%s/%s/%s", a.Type, a.Outcome, a.State)
	if wantFields := fmt.Sprintf("%s/%s/%s", assessment.TypeSecurity, assessment.OutcomeSatisfactory, assessment.StateReadyForReview); gotFields != wantFields {
		t.Errorf("type/outcome/state = %s/%s/%s", a.Type, a.Outcome, a.State)
	}

	if a.File != "assessments/ASM-EDGE/v1.yaml" || a.Line != 2 {
		t.Errorf("location = %s:%d, want assessments/ASM-EDGE/v1.yaml:2", a.File, a.Line)
	}

	if !strings.HasPrefix(a.Digest, "sha256:") || len(a.Digest) != len("sha256:")+64 {
		t.Errorf("digest = %q", a.Digest)
	}
}

// Scenario: Quality assessment with a scenario subject.
func TestLoadQualityAssessmentWithScenarioSubject(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, map[string]string{"assessments/ASM-LATENCY/v1.yaml": qualityAssessment})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	a := set.Assessment("ASM-LATENCY", 1)
	if a == nil || a.Type != assessment.TypeQuality {
		t.Fatalf("assessment = %+v", a)
	}

	declared := set.Declared(a)
	if !loadDeclaresScenarioDependency(declared) {
		t.Errorf("declared = %+v, want one quality-scenario dependency QS-LATENCY-1", declared)
	}

	if a.State != assessment.StateDraft {
		t.Errorf("state = %s, want the default draft", a.State)
	}
}

// loadDeclaresScenarioDependency tells whether the assessment declares exactly the dependency that its scenario
// subject gives.
func loadDeclaresScenarioDependency(declared []assessment.DeclaredDependency) bool {
	if len(declared) != 1 {
		return false
	}

	dep := declared[0]
	if dep.Kind != assessment.DepQualityScenario || dep.From != "scenario" {
		return false
	}

	return dep.Selector["identity"][0] == "QS-LATENCY-1"
}

// Scenario: Assessment depends on an assumption.
func TestDeclaredIncludesTheSelectorsOfListedAssumptions(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	declared := set.Declared(set.Assessment("ASM-EDGE", 1))
	if len(declared) != 2 {
		t.Fatalf("declared = %+v", declared)
	}

	if declared[0].From != "subject" || !loadDeclaredFromListedAssumption(declared[1]) {
		t.Errorf("declared = %+v", declared)
	}
}

// loadDeclaredFromListedAssumption tells whether the dependency is the public entry point of the listed assumption.
func loadDeclaredFromListedAssumption(dep assessment.DeclaredDependency) bool {
	return dep.From == "assumption:ASM-EDGE-1" && dep.Kind == assessment.DepPublicEntryPoint
}

// loadInvalidCase is a document set that Load rejects with CO-ASM-001, at a line when line is not 0.
type loadInvalidCase struct {
	name  string
	files map[string]string
	part  string
	line  int
}

func TestLoadRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()

	for _, tc := range slices.Concat(loadInvalidFieldCases(t), loadInvalidShapeCases()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadEdge(t, tc.files)
			failure := wantDiag(t, err, diag.ASM001, tc.part)

			if failure.Diagnostic.Source == nil || failure.Diagnostic.Source.URI == "" {
				t.Fatalf("no source in %+v", failure.Diagnostic)
			}

			if tc.line != 0 && failure.Diagnostic.Source.Line != tc.line {
				t.Errorf("line = %d, want %d (%s)", failure.Diagnostic.Source.Line, tc.line, failure.Diagnostic.Message)
			}
		})
	}
}

// loadInvalidFieldCases are the edge documents with one field or reference that is missing, unknown, or out of range.
func loadInvalidFieldCases(t *testing.T) []loadInvalidCase {
	t.Helper()

	return []loadInvalidCase{
		{
			name:  "missing field",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "environment: production\n", ""),
			part:  "environment",
			line:  2,
		},
		{
			// Scenario: Unknown key.
			name:  "unknown key",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "outcome: satisfactory\n", "outcome: satisfactory\nsigned-off: true\n"),
			part:  "signed-off",
			line:  2,
		},
		{
			name:  "unknown key inside the subject",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "  summary: Public", "  approved: yes\n  summary: Public"),
			part:  "approved",
			line:  12,
		},
		{
			// Scenario: Unknown assumption reference.
			name:  "unknown assumption reference",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "assumptions: [ASM-EDGE-1]", "assumptions: [ASM-EDGE-1, ASM-NOPE-9]"),
			part:  "ASM-NOPE-9",
			line:  2,
		},
		{
			name: "assumption with neither selector nor evidence",
			files: edit(t, edgeFiles(), "assessments/assumptions/ASM-EDGE-1.yaml", `selectors:
  - kind: public-entry-point
    selector:
      calmNodes: [api-gateway]
`, ""),
			part: "neither selectors nor evidence",
			line: 1,
		},
		{
			// Scenario: SEC-03 unknown kind.
			name: "unknown dependency kind",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "kind: public-entry-point\n      selector",
				"kind: firewall-rule\n      selector"),
			part: "/subject/dependencies/0/kind",
			line: 14,
		},
		{
			name:  "authored outcome outside the enum",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "outcome: satisfactory", "outcome: fine"),
			part:  "/outcome",
			line:  21,
		},
	}
}

// loadInvalidShapeCases are the files that are not an assessment document at all.
func loadInvalidShapeCases() []loadInvalidCase {
	return []loadInvalidCase{
		{
			name:  "not a mapping",
			files: map[string]string{"assessments/x.yaml": "- a\n- b\n"},
			part:  "mapping",
		},
		{
			name:  "unknown kind",
			files: map[string]string{"assessments/x.yaml": "apiVersion: concordia.io/v1alpha1\nkind: Policy\n"},
			part:  "unknown kind",
			line:  2,
		},
		{
			name:  "syntax error",
			files: map[string]string{"assessments/x.yaml": "kind: [\n"},
			part:  "cannot parse",
		},
	}
}

// Scenario: Duplicate version.
func TestLoadRejectsADuplicateIDAndVersion(t *testing.T) {
	t.Parallel()

	files := edgeFiles()
	files["assessments/copy/v1.yaml"] = securityAssessment

	_, err := loadEdge(t, files)
	failure := wantDiag(t, err, diag.ASM001, "ASM-EDGE@v1 is declared twice")

	for _, loc := range []string{"assessments/ASM-EDGE/v1.yaml:2", "assessments/copy/v1.yaml:2"} {
		if !strings.Contains(failure.Diagnostic.Message, loc) {
			t.Errorf("message %q does not name %s", failure.Diagnostic.Message, loc)
		}
	}
}

func TestLoadRejectsADuplicateAssumption(t *testing.T) {
	t.Parallel()

	files := edgeFiles()
	files["assessments/assumptions/other.yaml"] = edgeAssumption

	_, err := loadEdge(t, files)
	_ = wantDiag(t, err, diag.ASM001, "assumption ASM-EDGE-1 is declared twice")
}

// Scenario: SEC-03 each listed kind is accepted.
func TestEveryDependencyKindIsAccepted(t *testing.T) {
	t.Parallel()

	var deps strings.Builder

	for _, kind := range assessment.DependencyKinds {
		fmt.Fprintf(&deps, "    - kind: %s\n      selector: {identity: x}\n", kind)
	}

	text := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml",
		"    - kind: public-entry-point\n      selector:\n        calmNodes: [api-gateway]\n", deps.String())

	if _, err := loadEdge(t, text); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestDependencyKindsEqualTheTrustVocabulary(t *testing.T) {
	t.Parallel()

	got := make([]string, 0, len(assessment.DependencyKinds))
	for _, k := range assessment.DependencyKinds {
		got = append(got, string(k))
	}

	if want := trust.DependencyKinds(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("DependencyKinds = %v, want the trust vocabulary %v", got, want)
	}
}

func TestLoadAllReportsEveryProblem(t *testing.T) {
	t.Parallel()

	files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "environment: production\n", "")
	files["assessments/ASM-LATENCY/v1.yaml"] = strings.Replace(qualityAssessment, "outcome: inconclusive", "outcome: nope", 1)

	set, errs := assessment.LoadAll(workspace(t, files), []string{"assessments"})
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want 2", errs)
	}

	if len(set.Assessments) != 0 || len(set.Assumptions) != 1 {
		t.Errorf("valid documents = %d assessments, %d assumptions", len(set.Assessments), len(set.Assumptions))
	}
}

func TestLoadPathItems(t *testing.T) {
	t.Parallel()

	root := workspace(t, map[string]string{
		"a/ASM-EDGE/v1.yaml":           securityAssessment,
		"a/assumptions/ASM-EDGE-1.yml": edgeAssumption,
		"a/README.md":                  "# not yaml\n",
		"b/v1.yaml":                    qualityAssessment,
	})

	for _, tc := range []struct {
		name  string
		paths []string
		want  int
	}{
		{"directory", []string{"a"}, 2},
		{"directory with ./ and a trailing slash", []string{"./a/"}, 2},
		{"file", []string{"b/v1.yaml"}, 1},
		{"glob", []string{"b/*.yaml"}, 1},
		{"two items, one file twice", []string{"a", "a/ASM-EDGE/v1.yaml", "b"}, 3},
		{"no paths", nil, 0},
	} {
		set, err := assessment.Load(root, tc.paths)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if got := len(set.Assessments) + len(set.Assumptions); got != tc.want {
			t.Errorf("%s: %d documents, want %d", tc.name, got, tc.want)
		}
	}

	_, err := assessment.Load(root, []string{"missing"})
	_ = wantDiag(t, err, diag.ASM001, "assessment path not found: missing")
}

func TestLoadIsDeterministic(t *testing.T) {
	t.Parallel()

	root := workspace(t, edgeFiles())

	first, err := assessment.Load(root, []string{"assessments"})
	if err != nil {
		t.Fatal(err)
	}

	second, err := assessment.Load(root, []string{"assessments"})
	if err != nil {
		t.Fatal(err)
	}

	if fmt.Sprintf("%+v", first.Files) != fmt.Sprintf("%+v", second.Files) ||
		first.Assessments[0].Digest != second.Assessments[0].Digest {
		t.Error("two loads of the same tree differ")
	}
}
