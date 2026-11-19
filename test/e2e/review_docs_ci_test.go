package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rvDocsApprovedSpec is a repository with ASM-EDGE approved and required.
func rvDocsApprovedSpec(name string) *rvSpec {
	return &rvSpec{name: name, docs: []string{"ASM-EDGE"}, approvals: []string{"ASM-EDGE-v1-1"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}}, anchor: "origin/main"}
}

// rvDocsUnapprovedSpec is a repository with ASM-EDGE required and no approval.
func rvDocsUnapprovedSpec(name string) *rvSpec {
	return &rvSpec{name: name, docs: []string{"ASM-EDGE"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}}}
}

// rvDocsShallowCheckout makes the CI checkout of the candidate with depth 1 next to the repository r.
func rvDocsShallowCheckout(t *testing.T, r *repo) *repo {
	t.Helper()

	// The candidate is one commit ahead of the anchor.
	r.write("docs/notes.md", "A change of the candidate.\n")
	r.commit("docs: a note")
	r.git("push", "-q", "origin", "candidate")
	r.check(0).assessment(t, "ASM-EDGE").want(t, "current")

	origin := strings.TrimSpace(r.gitOut("remote", "get-url", "origin"))

	// A CI job checks out the candidate with depth 1: the anchor is not there, so the approval is unknown (exit 4).
	ci := &repo{t: t, dir: filepath.Join(t.TempDir(), "ci"), project: r.project}
	r.git("clone", "-q", "--depth=1", "--single-branch", "--branch", "candidate", "file://"+origin, ci.dir)

	if res := ci.gu("index"); res.code != 0 {
		t.Fatalf("index in the CI checkout (%d): %s", res.code, res.stderr)
	}

	return ci
}

// rvDocsAnchorUnreadable wants the unknown currency of an assessment whose trust anchor is not in the checkout.
func rvDocsAnchorUnreadable(t *testing.T, ci *repo) {
	t.Helper()

	doc := ci.check(4)
	a := doc.assessment(t, "ASM-EDGE")

	if !rvUnknownWithDiagnostic(doc, a, "trust-anchor-unreadable", "CO-ASM-007") || doc.verdict("REV-APR-001") != "unknown" {
		t.Errorf("without the anchor: currency %q reason %q, CO-ASM-007 %v, REV-APR-001 %q", a.Currency, a.UnknownReason, doc.hasDiagnostic("CO-ASM-007"), doc.verdict("REV-APR-001"))
	}

	if !anyContains(doc.messages("CO-ASM-007"), "origin/main") {
		t.Errorf("CO-ASM-007 = %v, want the ref origin/main", doc.messages("CO-ASM-007"))
	}
}

// rvUnknownWithDiagnostic tells whether an assessment is unknown for the reason and the results have the diagnostic.
func rvUnknownWithDiagnostic(doc *rvDoc, a *rvAssessment, reason, diagnostic string) bool {
	return a.Currency == "unknown" && a.UnknownReason == reason && doc.hasDiagnostic(diagnostic)
}

// rvDocsShallowClone: the exit codes of the shallow clone until the page's refspec and `--deepen` give the anchor.
func rvDocsShallowClone(t *testing.T) {
	t.Parallel()

	r := newRvRepo(t, rvDocsApprovedSpec("docs-ci-shallow"))
	ci := rvDocsShallowCheckout(t, r)

	rvDocsAnchorUnreadable(t, ci)

	// A plain `git fetch origin main` does not create origin/main in a shallow clone.
	ci.git("fetch", "-q", "--depth=50", "origin", "main")

	if still := ci.check(4); still.assessment(t, "ASM-EDGE").UnknownReason != "trust-anchor-unreadable" {
		t.Errorf("after a plain fetch: %+v", still.assessment(t, "ASM-EDGE"))
	}

	// The refspec of the page creates it, and the anchor is then an ancestor only after the clone is deepened.
	ci.git("fetch", "-q", "--depth=50", "origin", "main:refs/remotes/origin/main")

	notAncestor := ci.check(4)
	if !anyContains(notAncestor.messages("CO-ASM-007"), "is not an ancestor of HEAD") {
		t.Errorf("before the clone is deepened: CO-ASM-007 = %v, want the anchor is not an ancestor of HEAD", notAncestor.messages("CO-ASM-007"))
	}

	ci.git("fetch", "-q", "--deepen=50")

	ci.check(0).assessment(t, "ASM-EDGE").want(t, "current")
}

// rvDocsAdvisory: the rules are advisory, so the verdict and the finding stay and the exit code is 0.
func rvDocsAdvisory(t *testing.T) {
	t.Parallel()

	spec := rvDocsUnapprovedSpec("docs-ci-advisory")
	spec.enforcement = "advisory"

	r := newRvRepo(t, spec)

	doc := r.check(0)
	doc.assessment(t, "ASM-EDGE").want(t, "review-required", "no approval")

	if doc.verdict("REV-APR-001") != "violation" || len(doc.Findings) == 0 {
		t.Errorf("advisory: REV-APR-001 = %q, findings %d; the verdict and the finding stay", doc.verdict("REV-APR-001"), len(doc.Findings))
	}
}

// rvDocsMandatory: the same with the rules mandatory is exit 1, and check never writes the cache without --incremental.
func rvDocsMandatory(t *testing.T) {
	t.Parallel()

	r := newRvRepo(t, rvDocsUnapprovedSpec("docs-ci-mandatory"))

	r.check(1)

	if _, err := os.Stat(r.path(".concordia/cache")); err == nil {
		t.Error("a check without --incremental wrote .concordia/cache")
	}
}

// rvDocsConfigChange is a change of concordia.yaml and the diagnostic that the run then gives.
type rvDocsConfigChange struct {
	change func(string) string
	code   string
}

// rvDocsConfigChanges are the configurations that make the review rules unusable.
func rvDocsConfigChanges() []rvDocsConfigChange {
	return []rvDocsConfigChange{
		{code: "CO-ASM-004", change: func(s string) string { return strings.Replace(s, "  trustAnchor: { ref: main }\n", "", 1) }},
		{code: "CO-ASM-004", change: func(s string) string {
			return strings.Replace(s, "  authorities:\n    - id: "+rvAuthority+"\n      role: reviewer\n      mechanism: protected-review\n"+
				"      reviewers: ["+rvReviewer+"]\n      paths: [assessments/approvals/**, assessments/revocations/**]\n", "  authorities: []\n", 1)
		}},
		{code: "CO-POL-018", change: func(s string) string {
			return strings.Replace(s, "{family: assessment-currency, mode: mandatory}", "{family: assessment-currency, environment: ghost, mode: mandatory}", 1)
		}},
	}
}

// rvNoResultWithDiagnostic tells whether a run exited 2 with the diagnostic and wrote no result.
func rvNoResultWithDiagnostic(res cliResult, code string) bool {
	return res.code == 2 && strings.Contains(res.stderr, code) && strings.TrimSpace(res.stdout) == ""
}

// rvDocsConfiguration: mandatory review rules without a trust anchor or an authority (CO-ASM-004), or without a
// guarantee of the enforcement check (CO-POL-018), are exit 2 and give no result.
func rvDocsConfiguration(t *testing.T) {
	t.Parallel()

	r := newRvRepo(t, rvDocsUnapprovedSpec("docs-ci-config"))
	original := r.read("concordia.yaml")

	for _, c := range rvDocsConfigChanges() {
		changed := c.change(original)
		if changed == original {
			t.Fatalf("%s: the change did not apply", c.code)
		}

		r.write("concordia.yaml", changed)

		res := r.cli(rvEnv(), "--format", "json", "check")
		if !rvNoResultWithDiagnostic(res, c.code) {
			t.Errorf("%s: exit %d, stdout %q, stderr %s; want exit 2 with %s and no result", c.code, res.code, res.stdout, res.stderr, c.code)
		}
	}
}

// rvDocsDocuments: an assessment document that is not valid (CO-ASM-001) or an approved version that was edited
// (CO-ASM-002) is exit 2.
func rvDocsDocuments(t *testing.T) {
	t.Parallel()

	r := newRvRepo(t, rvDocsApprovedSpec("docs-ci-documents"))
	original := r.read(rvEdgeFile)

	r.write(rvEdgeFile, original+"bogus: true\n")
	r.refused(r.cli(rvEnv(), "--format", "json", "check"), "CO-ASM-001", "an unknown key")

	r.write(rvEdgeFile, strings.Replace(original, "title: Runtime identity of the API service", "title: Another title", 1))
	r.refused(r.cli(rvEnv(), "--format", "json", "check"), "CO-ASM-002", "an edited approved version")
}

// rvDocsDamageCache overwrites every cache file of the impact analysis, like a damaged restore of a CI cache.
func rvDocsDamageCache(r *repo) {
	r.t.Helper()

	files, err := filepath.Glob(r.path(".concordia/cache/impact/*"))
	if err != nil || len(files) == 0 {
		r.t.Fatalf("no cache file under .concordia/cache/impact: %v", err)
	}

	for _, f := range files {
		if err := os.WriteFile(f, []byte("damaged"), fileMode); err != nil {
			r.t.Fatal(err)
		}
	}
}

// rvDocsIncremental: check --incremental uses the cache, CO-ASM-010 notes a damaged restore, and the result is the same
// as the one of a full run.
func rvDocsIncremental(t *testing.T) {
	t.Parallel()

	r := newRvRepo(t, rvDocsApprovedSpec("docs-ci-incremental"))

	first := r.check(0, "--incremental")
	second := r.check(0, "--incremental")

	if first.Analysis.Mode != "incremental" || second.Analysis.CacheHits == 0 {
		t.Errorf("analysis: first %+v, second %+v; want the second run to reuse the cache", first.Analysis, second.Analysis)
	}

	rvDocsDamageCache(r)

	damaged := r.check(0, "--incremental")
	if !damaged.hasDiagnostic("CO-ASM-010") || !sameJSON(damaged.currencySection(), r.check(0).currencySection()) {
		t.Errorf("a damaged cache: CO-ASM-010 %v; want the note, exit 0, and the result of a full run", damaged.hasDiagnostic("CO-ASM-010"))
	}
}
