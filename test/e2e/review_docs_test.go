package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Task 11.4: the documented commands of docs/how-to/review-and-approve-assessments.md and the review flow of
// docs/how-to/run-in-ci.md, run on a clean temporary git checkout of test/fixtures/review against the real binary and
// the real CBM, each with the exit code that the page documents. The origin of a repository is a bare repository in a
// temporary directory, so `origin/main` is the trust anchor as in the pages. The signed flow uses the fake cosign of
// review_authority_test.go (no cosign release is pinned).

const (
	rvEdgeFile   = "assessments/ASM-EDGE/v1.yaml"
	rvEdgeRecord = "assessments/approvals/ASM-EDGE-v1-1.yaml"
	rvEdgeRevoke = "assessments/revocations/ASM-EDGE-v1-1.yaml"
)

var rvApprovalDigestRe = regexp.MustCompile(`Approval digest: (sha256:[0-9a-f]{64})`)

// approve runs `concordia assessment approve ASM-EDGE --reviewer alice@example.com <args>`.
func (r *repo) approve(env []string, args ...string) cliResult {
	r.t.Helper()

	return r.cli(append(rvEnv(), env...), append([]string{"assessment", "approve", "ASM-EDGE", "--reviewer", rvReviewer}, args...)...)
}

// refused wants exit 2 with the diagnostic and no new file in the repository.
func (r *repo) refused(res cliResult, code, what string) {
	r.t.Helper()

	if res.code != 2 || !strings.Contains(res.stderr, code) {
		r.t.Errorf("%s: exit %d, want 2 with %s: %s", what, res.code, code, res.output())
	}
}

// subject prints the subject of the assessment and gives the digest that the documentation tells the reviewer to read.
func (r *repo) subject() string {
	r.t.Helper()

	res := r.approve(nil)
	r.refused(res, "CO-ASM-005", "approve without --confirm-subject")

	m := reviewDigestRe.FindStringSubmatch(res.stdout)
	if m == nil {
		r.t.Fatalf("approve printed no subject digest:\n%s", res.stdout)
	}

	rvWantParts(r.t, "the subject", res.stdout, "Assessment ASM-EDGE@v1", "Digest contract: review-digest/1", "capability profile: cbm-0.12.1", "Dependencies (1):", "runtime-identity", "Assumptions: 0, evidence: 0")

	return m[1]
}

func (r *repo) wantNoFile(rel string) {
	r.t.Helper()

	if _, err := os.Stat(r.path(rel)); err == nil {
		r.t.Errorf("%s was written", rel)
	}
}

// pullRequest simulates the pull request of step 3: a branch from `origin/main` with the files of the candidate, merged
// into main by a fast-forward push (the review on the forge is not part of Concordia), then the candidate is rebuilt
// on the anchor (step 4).
func (r *repo) pullRequest(branch, message string, files ...string) {
	r.t.Helper()

	r.git("switch", "-q", "-c", branch, "origin/main")
	r.git(append([]string{"checkout", "candidate", "--"}, files...)...)
	r.commitAll(message)
	r.git("push", "-q", "origin", branch+":main")
	r.git("switch", "-q", "candidate")
	r.git("fetch", "-q", "origin")
	r.git("merge", "-q", "--no-edit", "origin/main")

	if res := r.gu("index"); res.code != 0 {
		r.t.Fatalf("index after the merge (%d): %s", res.code, res.stderr)
	}
}

func TestReviewDocsProtectedReviewFlow(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{
		name: "docs-protected", docs: []string{"ASM-EDGE", "ASM-TERRAFORM"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}},
		anchor: "origin/main", candidateDocs: true,
	})

	rvDocsPrerequisites(r)
	rvDocsRefusals(r)

	// Step 1: look at the subject. Step 2: write the record with the digest that was read; `--expires`, `--condition`,
	// and `--decision rejected` as documented.
	digest := r.subject()

	rvDocsRejectedDecision(r, digest)

	record := rvDocsApproveRecord(r, digest)

	rvDocsNoOverwrite(r, digest, record)
	rvDocsRecordOnlyInCandidate(r)
	rvDocsApprovalOnAnchor(r)
	rvDocsRevocation(r)
}

// A record that is not counted shows its reason (the table "When a record is not counted"): the approval record of the
// fixture is changed in the way that each reason names.
func TestReviewDocsRecordsNotCounted(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	record, err := os.ReadFile(filepath.Join(reviewFixtureDir, "records", "approvals", "ASM-EDGE-v1-1.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range rvDocsNotCountedCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			rvDocsCheckNotCounted(t, string(record), c)
		})
	}
}

func TestReviewDocsSignedFlow(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := newRvRepo(t, &rvSpec{
		name: "docs-signed", docs: []string{"ASM-EDGE"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}},
		anchor: "origin/main", candidateDocs: true, signed: true,
	})

	flow := &rvDocsSignedFlow{r: r, cosign: []string{"COSIGN=" + fakeReviewCosign(t)}}
	flow.digest = r.subject()

	flow.refusals()
	flow.signedRecord()
	flow.offline()
	flow.withoutBundle()
	flow.otherSigner()
}

// refused2 wants the exit code and the diagnostic.
func (r *repo) refused2(res cliResult, code int, diagCode, what string) {
	r.t.Helper()

	if res.code != code || !strings.Contains(res.stderr, diagCode) {
		r.t.Errorf("%s: exit %d, want %d with %s: %s", what, res.code, code, diagCode, res.output())
	}
}

// The review flow of docs/how-to/run-in-ci.md: the exit codes of the table and the shallow clone.
func TestReviewDocsCIFlow(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	t.Run("shallow-clone", rvDocsShallowClone)
	t.Run("advisory", rvDocsAdvisory)
	t.Run("mandatory", rvDocsMandatory)
	t.Run("configuration", rvDocsConfiguration)
	t.Run("documents", rvDocsDocuments)
	t.Run("incremental", rvDocsIncremental)
}

// rvPathWithout gives a PATH directory that has git and the CBM executable and no cosign.
func rvPathWithout(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for _, name := range []string{"git", cbmExecutable} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(path, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}
