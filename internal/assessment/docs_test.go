package assessment_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
)

const referencePage = "../../docs/reference/assessments.md"

func readPage(t *testing.T) string {
	t.Helper()

	page, err := os.ReadFile(referencePage)
	if err != nil {
		t.Fatal(err)
	}

	return string(page)
}

var sampleFence = regexp.MustCompile("(?s)```yaml file=(\\S+)\n(.*?)\n```")

// samples gives the sample files of the page: the fenced blocks whose info string is `yaml file=<path>`.
func samples(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for _, m := range sampleFence.FindAllStringSubmatch(readPage(t), -1) {
		out[m[1]] = m[2] + "\n"
	}

	return out
}

// firstColumn gives the first column of the rows of the table that follows a line starting with the header.
func firstColumn(t *testing.T, header string) []string {
	t.Helper()

	var (
		out   []string
		inTab bool
	)

	for _, line := range strings.Split(readPage(t), "\n") {
		switch {
		case strings.HasPrefix(line, header):
			inTab = true
		case inTab && strings.HasPrefix(line, "|---"):
		case inTab && strings.HasPrefix(line, "|"):
			cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
			out = append(out, strings.Trim(strings.TrimSpace(cell), "`"))
		default:
			inTab = false
		}
	}

	return out
}

// The sample files of the page load as one valid set, and they agree with each other: the approval binds the
// digests of the sample assessment and assumption, and the revocation binds the digest of the sample approval.
func TestSamplesOfTheReferencePageLoad(t *testing.T) {
	t.Parallel()

	files := samples(t)
	docsRequireSampleFiles(t, files)

	set, errs := assessment.LoadAll(workspace(t, files), []string{"assessments"})
	for _, err := range errs {
		t.Errorf("load: %v", err)
	}

	if len(errs) > 0 {
		return
	}

	if len(set.Assessments) != 1 || len(set.Assumptions) != 1 || len(set.Approvals) != 1 || len(set.Revocations) != 1 {
		t.Fatalf("loaded %d/%d/%d/%d documents", len(set.Assessments), len(set.Assumptions), len(set.Approvals), len(set.Revocations))
	}

	docsCheckSampleBindings(t, set)
	docsCheckSampleStates(t, set)
}

// docsRequireSampleFiles fails the test when the page lacks one of the four sample records.
func docsRequireSampleFiles(t *testing.T, files map[string]string) {
	t.Helper()

	for _, want := range []string{
		"assessments/ASM-EDGE/v1.yaml", "assessments/assumptions/ASM-EDGE-1.yaml",
		"assessments/approvals/ASM-EDGE-v1-1.yaml", "assessments/revocations/ASM-EDGE-v1-1.yaml",
	} {
		if _, ok := files[want]; !ok {
			t.Fatalf("the page has no sample file %s", want)
		}
	}
}

// docsCheckSampleBindings checks that the digests that the sample records bind are the digests of the samples.
func docsCheckSampleBindings(t *testing.T, set *assessment.Set) {
	t.Helper()

	asm, approval, revocation := set.Assessments[0], set.Approvals[0], set.Revocations[0]

	if approval.Assessment.Digest != asm.Digest {
		t.Errorf("the sample approval binds %s, the sample assessment has the digest %s", approval.Assessment.Digest, asm.Digest)
	}

	if approval.Assumptions[0].Digest != set.Assumptions[0].Digest {
		t.Errorf("the sample approval binds the assumption digest %s, the sample assumption has %s",
			approval.Assumptions[0].Digest, set.Assumptions[0].Digest)
	}

	if revocation.Approval.Digest != approval.Digest {
		t.Errorf("the sample revocation binds %s, the sample approval has the digest %s", revocation.Approval.Digest, approval.Digest)
	}
}

// docsCheckSampleStates checks the review state of the sample version with and without its revocation.
func docsCheckSampleStates(t *testing.T, set *assessment.Set) {
	t.Helper()

	asm := set.Assessments[0]

	if review := assessment.ReviewOf(asm, set.Approvals, set.Revocations); review.State != assessment.StateRevoked {
		t.Errorf("state = %s, want revoked", review.State)
	}

	// Without the revocation the sample version is approved.
	if review := assessment.ReviewOf(asm, set.Approvals, nil); review.State != assessment.StateApproved {
		t.Errorf("state = %s, want approved", review.State)
	}
}

func TestDocumentedDependencyKindsAreTheConstants(t *testing.T) {
	t.Parallel()

	documented := firstColumn(t, "| Kind | Meaning |")

	want := make([]string, 0, len(assessment.DependencyKinds))
	for _, k := range assessment.DependencyKinds {
		want = append(want, string(k))
	}

	if !slices.Equal(documented, want) {
		t.Errorf("documented kinds = %v, want %v", documented, want)
	}
}

func TestDocumentedOutcomesAndStatesAreTheConstants(t *testing.T) {
	t.Parallel()

	outcomes := make([]string, 0, len(assessment.Outcomes))
	for _, o := range assessment.Outcomes {
		outcomes = append(outcomes, string(o))
	}

	if got := firstColumn(t, "| Outcome |"); !slices.Equal(got, outcomes) {
		t.Errorf("documented outcomes = %v, want %v", got, outcomes)
	}

	states := make([]string, 0, len(assessment.States))
	for _, s := range assessment.States {
		states = append(states, string(s))
	}

	if got := firstColumn(t, "| State |"); !slices.Equal(got, states) {
		t.Errorf("documented states = %v, want %v", got, states)
	}
}

func TestDocumentedKindsAndSchemaIDsAreTheConstants(t *testing.T) {
	t.Parallel()

	page := readPage(t)

	for _, kind := range assessment.Kinds {
		if !strings.Contains(page, "| `"+kind+"` |") {
			t.Errorf("the kinds table does not list %s", kind)
		}
	}

	for _, id := range []string{
		assessment.AssessmentSchemaID, assessment.AssumptionSchemaID, assessment.ApprovalSchemaID, assessment.RevocationSchemaID,
	} {
		if !strings.Contains(page, "`"+id+"`") {
			t.Errorf("the page does not name the schema ID %s", id)
		}
	}
}

func TestDocumentedDiagnosticsExist(t *testing.T) {
	t.Parallel()

	for _, code := range firstColumn(t, "| Code |") {
		entry, ok := diag.Lookup(code)
		if !ok {
			t.Errorf("documented diagnostic %s is not in the catalog", code)

			continue
		}

		if entry.Severity != diag.SeverityError || entry.ExitCode != diag.InvalidInput {
			t.Errorf("%s: severity %s, exit code %d, the page says error and 2", code, entry.Severity, entry.ExitCode)
		}
	}
}
