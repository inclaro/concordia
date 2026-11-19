package assessment_test

import (
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
)

func versionDigest(t *testing.T, files map[string]string) string {
	t.Helper()

	set, err := loadEdge(t, files)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return set.Assessment("ASM-EDGE", 1).Digest
}

func TestVersionDigestIgnoresCommentsFormattingStateAndKeyOrder(t *testing.T) {
	t.Parallel()

	base := versionDigest(t, edgeFiles())

	for _, tc := range []struct {
		files map[string]string
		name  string
	}{
		{
			// Scenario: comment-only edit keeps the digest.
			name:  "comment only",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "# Edge exposure review.", "# Reviewed again on Friday.\n# More words."),
		},
		{
			name:  "authored state",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "state: ready-for-review", "state: draft"),
		},
		{
			name:  "no authored state",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "state: ready-for-review\n", ""),
		},
		{
			name:  "flow style and quoting",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "environment: production", "environment: \"production\""),
		},
		{
			name: "key order",
			files: edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "id: ASM-EDGE\nversion: 1\n",
				"version: 1\nid: ASM-EDGE\n"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := versionDigest(t, tc.files); got != base {
				t.Errorf("digest = %s, want the unchanged %s", got, base)
			}
		})
	}
}

func TestVersionDigestChangesWithContent(t *testing.T) {
	t.Parallel()

	base := versionDigest(t, edgeFiles())

	for name, files := range map[string]map[string]string{
		"outcome":     edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "outcome: satisfactory", "outcome: unsatisfactory"),
		"scope":       edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "calmNodes: [api-gateway]\nenvironment", "calmNodes: [api-gateway, billing]\nenvironment"),
		"environment": edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "environment: production", "environment: staging"),
		"evidence":    edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "edge-review.md", "other.md"),
	} {
		if got := versionDigest(t, files); got == base {
			t.Errorf("%s: digest did not change", name)
		}
	}
}

func TestVersionDigestMatchesTheDocumentFunction(t *testing.T) {
	t.Parallel()

	a, err := assessment.ParseAssessment("v1.yaml", []byte(securityAssessment))
	if err != nil {
		t.Fatal(err)
	}

	doc := map[string]any{"b": "2", "a": "1", "state": "draft"}

	d1, err := assessment.VersionDigest(doc)
	if err != nil {
		t.Fatal(err)
	}

	doc["state"] = "ready-for-review"

	d2, err := assessment.VersionDigest(doc)
	if err != nil || d1 != d2 {
		t.Errorf("state changes the version digest: %s vs %s (%v)", d1, d2, err)
	}

	if a.Digest == "" {
		t.Error("ParseAssessment did not set the digest")
	}
}

// approvalRecord renders and returns the approval of the loaded version, as it would be committed.
func approvalRecord(t *testing.T, a *assessment.Assessment, id string) string {
	t.Helper()

	text, err := assessment.RenderApproval(approvalFor(a, id, decided))
	if err != nil {
		t.Fatalf("RenderApproval: %v", err)
	}

	return string(text)
}

// Scenario: Edit of an approved version.
func TestEditOfAnApprovedVersionFailsNamingBothDigests(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	approved := set.Assessment("ASM-EDGE", 1)
	files := edgeFiles()
	files["assessments/approvals/ASM-EDGE-v1-1.yaml"] = approvalRecord(t, approved, "ASM-EDGE-v1-1")

	if _, err := loadEdge(t, files); err != nil {
		t.Fatalf("the unedited approved version must load: %v", err)
	}

	edited := edit(t, files, "assessments/ASM-EDGE/v1.yaml", "outcome: satisfactory", "outcome: unsatisfactory")
	_, err = loadEdge(t, edited)
	failure := wantDiag(t, err, diag.ASM002, "ASM-EDGE@v1")

	editedSet, _ := assessment.LoadAll(workspace(t, edited), []string{"assessments"})
	newDigest := editedSet.Assessment("ASM-EDGE", 1).Digest

	for _, want := range []string{approved.Digest, newDigest, "ASM-EDGE-v1-1"} {
		if !strings.Contains(failure.Diagnostic.Message, want) {
			t.Errorf("message %q does not contain %q", failure.Diagnostic.Message, want)
		}
	}

	if failure.Diagnostic.Source == nil || failure.Diagnostic.Source.URI != "assessments/ASM-EDGE/v1.yaml" {
		t.Errorf("source = %+v", failure.Diagnostic.Source)
	}

	// A comment-only edit of the approved version is not an edit.
	commented := edit(t, files, "assessments/ASM-EDGE/v1.yaml", "# Edge exposure review.", "# New comment.")
	if _, err := loadEdge(t, commented); err != nil {
		t.Errorf("comment-only edit of an approved version failed: %v", err)
	}
}

// A rejection binds a digest as well.
func TestEditOfARejectedVersionFails(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	rejection := approvalFor(set.Assessment("ASM-EDGE", 1), "ASM-EDGE-v1-1", decided)
	rejection.Decision = assessment.DecisionRejected

	text, err := assessment.RenderApproval(rejection)
	if err != nil {
		t.Fatal(err)
	}

	files := edgeFiles()
	files["assessments/approvals/ASM-EDGE-v1-1.yaml"] = string(text)
	files = edit(t, files, "assessments/ASM-EDGE/v1.yaml", "evidence:\n  - id: EV-1", "evidence:\n  - id: EV-2")

	_, err = loadEdge(t, files)
	_ = wantDiag(t, err, diag.ASM002, "rejected")
}

// Scenario: New version for a changed subject.
func TestANewVersionLoadsBesideTheApprovedOne(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	v1 := set.Assessment("ASM-EDGE", 1)
	files := edgeFiles()
	files["assessments/approvals/ASM-EDGE-v1-1.yaml"] = approvalRecord(t, v1, "ASM-EDGE-v1-1")
	files["assessments/ASM-EDGE/v2.yaml"] = strings.Replace(
		strings.Replace(securityAssessment, "version: 1", "version: 2", 1),
		"calmNodes: [api-gateway]\nenvironment", "calmNodes: [api-gateway, billing]\nenvironment", 1)

	both, err := loadEdge(t, files)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(both.Assessments) != 2 || both.Assessments[0].Version != 1 || both.Assessments[1].Version != 2 {
		t.Fatalf("assessments = %+v", both.Assessments)
	}

	if got := both.ApprovalsFor("ASM-EDGE", 1); len(got) != 1 {
		t.Errorf("v1 approvals = %d, want 1", len(got))
	}

	if got := both.ApprovalsFor("ASM-EDGE", 2); len(got) != 0 {
		t.Errorf("v2 approvals = %d, want none", len(got))
	}

	if versions := both.Versions("ASM-EDGE"); len(versions) != 2 {
		t.Errorf("Versions = %d", len(versions))
	}
}

func TestCheckApprovedVersionEditedWithRecordsFromElsewhere(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	a := set.Assessment("ASM-EDGE", 1)
	rec := approvalFor(a, "R-1", decided)

	if err := assessment.CheckApprovedVersionEdited(set, []*assessment.Approval{rec}); err != nil {
		t.Errorf("matching digest: %v", err)
	}

	rec.Assessment.Digest = digestA

	failure := assessment.CheckApprovedVersionEdited(set, []*assessment.Approval{rec})
	if failure == nil || failure.Diagnostic.Code != diag.ASM002 || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("mismatch: %v", failure)
	}

	other := approvalFor(a, "R-2", decided.Add(time.Hour))
	other.Assessment.Version = 9

	if err := assessment.CheckApprovedVersionEdited(set, []*assessment.Approval{other}); err != nil {
		t.Errorf("a record for a version that the set does not hold is not checked here: %v", err)
	}
}

func TestAnApprovalOfAnUnknownVersionFails(t *testing.T) {
	t.Parallel()

	set, err := loadEdge(t, edgeFiles())
	if err != nil {
		t.Fatal(err)
	}

	rec := approvalFor(set.Assessment("ASM-EDGE", 1), "ASM-EDGE-v7-1", decided)
	rec.Assessment.Version = 7

	text, err := assessment.RenderApproval(rec)
	if err != nil {
		t.Fatal(err)
	}

	files := edgeFiles()
	files["assessments/approvals/ASM-EDGE-v7-1.yaml"] = string(text)

	_, err = loadEdge(t, files)
	_ = wantDiag(t, err, diag.ASM001, "ASM-EDGE@v7")
}
