package e2e_test

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/procexec"
)

// Helpers of the review-currency suite (review_test.go, review_docs_test.go). A review repository is the infra fixture
// with the workspace of test/fixtures/review/workspace over it, the assessment documents and the approval records that
// a case names, and a local `main` branch as the trust anchor. The work happens on the branch `candidate`.
//
// The approval records of test/fixtures/review/records/approvals were written by `concordia assessment approve` with
// CONCORDIA_NOW=2026-11-01T10:00:00Z and the authority `security-review` below; they bind the subjects that the fixture
// workspace has. Every run of the suite uses the clock rvNow, so an approval is neither in the future nor expired
// (except where a case says otherwise).

const (
	reviewFixtureDir = "../fixtures/review"
	rvNow            = "2026-11-05T00:00:00Z"
	rvReviewer       = "alice@example.com"
	rvAuthority      = "security-review"
	rvProductionEdge = "assessments/approvals/ASM-EDGE-v1-1.yaml"
)

// rvRequired is one `assessments.required` entry of the production environment unless it names another.
type rvRequired struct {
	id, assessment, environment string
}

// rvSpec is a review repository.
type rvSpec struct {
	name string
	// limits is a line of the assessments block, for example "  analysisLimits: { maxVisited: 1 }\n".
	limits string
	// authorities are additional authority list items under `assessments.authorities`.
	authorities string
	// extra is appended to the workspace file as top-level keys.
	extra string
	// anchor is the trust anchor ref (default "main"). "origin/main" gives the repository a bare origin that has `main`.
	anchor string
	// enforcement is the mode of the review rules (default "mandatory").
	enforcement string
	// docs are directories of test/fixtures/review/assessments (an assessment id) or assumption ids.
	docs []string
	// approvals are the file names (without .yaml) of test/fixtures/review/records/approvals.
	approvals []string
	required  []rvRequired
	// candidateDocs keeps the assessment documents and the approval records out of `main`: they are in the candidate only.
	candidateDocs bool
	// signed replaces the protected-review authority by a signed authority with the keyless signer alice@example.com.
	signed bool
}

const rvSignedAuthority = "platform-signers"

func (s *rvSpec) anchorRef() string {
	if s.anchor == "" {
		return reviewBase
	}

	return s.anchor
}

func (s *rvSpec) authorityID() string {
	if s.signed {
		return rvSignedAuthority
	}

	return rvAuthority
}

func (s *rvSpec) config() string {
	var b strings.Builder

	b.WriteString("assessments:\n  paths: [assessments]\n  trustAnchor: { ref: " + s.anchorRef() + " }\n" + s.limits)
	b.WriteString("  authorities:\n")

	if s.signed {
		b.WriteString("    - id: " + rvSignedAuthority + "\n      role: reviewer\n      mechanism: signed\n      signers:\n" +
			"        - { identity: " + rvReviewer + ", issuer: \"https://accounts.example.com\" }\n")
	} else {
		b.WriteString("    - id: " + rvAuthority + "\n      role: reviewer\n      mechanism: protected-review\n" +
			"      reviewers: [" + rvReviewer + "]\n      paths: [assessments/approvals/**, assessments/revocations/**]\n")
	}

	b.WriteString(s.authorities)
	b.WriteString("  required:\n")

	for _, req := range s.required {
		env := req.environment
		if env == "" {
			env = "production"
		}

		b.WriteString("    - { id: " + req.id + ", assessment: " + req.assessment + ", type: security, scope: { calmNodes: [api-service] }, environment: " +
			env + ", authority: " + s.authorityID() + " }\n")
	}

	b.WriteString(s.extra)

	return b.String()
}

// fixtureFiles reads a directory of the review fixture into a map of relative paths.
func fixtureFiles(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := map[string]string{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		files[filepath.ToSlash(rel)] = string(data)

		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	return files
}

// newRvRepo makes the review repository with the case on `main` (the trust anchor), switches to `candidate`, and indexes.
func newRvRepo(t *testing.T, spec *rvSpec) *repo {
	t.Helper()

	project := reviewProjectPrefix + spec.name
	files := fixtureFiles(t, filepath.Join(reviewFixtureDir, "workspace"))

	text := strings.Replace(files["concordia.yaml"], infraProjectOld, project, 1)
	if text == files["concordia.yaml"] {
		t.Fatal("the review workspace names no fixture project")
	}

	if spec.enforcement != "" {
		text = strings.Replace(text, "mode: mandatory}", "mode: "+spec.enforcement+"}", 1)
	}

	files["concordia.yaml"] = text + spec.config()

	// The documents and records that a case names.
	docFiles := map[string]string{}
	assessments := filepath.Join(reviewFixtureDir, "assessments")

	for _, doc := range spec.docs {
		if _, err := os.Stat(filepath.Join(assessments, doc)); err == nil {
			for rel, content := range fixtureFiles(t, filepath.Join(assessments, doc)) {
				docFiles["assessments/"+doc+"/"+rel] = content
			}

			continue
		}

		data, err := os.ReadFile(filepath.Join(assessments, "assumptions", doc+".yaml"))
		if err != nil {
			t.Fatalf("unknown review document %q: %v", doc, err)
		}

		docFiles["assessments/assumptions/"+doc+".yaml"] = string(data)
	}

	for _, name := range spec.approvals {
		data, err := os.ReadFile(filepath.Join(reviewFixtureDir, "records", "approvals", name+".yaml"))
		if err != nil {
			t.Fatalf("unknown approval record %q: %v", name, err)
		}

		docFiles["assessments/approvals/"+name+".yaml"] = string(data)
	}

	if !spec.candidateDocs {
		maps.Copy(files, docFiles)
	}

	r := newDeploymentRepo(t, project, spec.name, files)

	if strings.HasPrefix(spec.anchorRef(), "origin/") {
		r.addOrigin()
	}

	r.git("switch", "-q", "-c", "candidate")

	if spec.candidateDocs {
		for rel, content := range docFiles {
			r.write(rel, content)
		}

		r.commitAll("docs: the assessments")
	}

	if res := r.gu("index"); res.code != 0 {
		t.Fatalf("index (%d): %s", res.code, res.stderr)
	}

	return r
}

// rvEnv is the environment of the runs: the evaluation clock, and no CI variable (approve refuses to run in CI).
func rvEnv(extra ...string) []string {
	return append([]string{"CI=", "CONCORDIA_NOW=" + rvNow}, extra...)
}

// The part of results.json that the review suite reads.
type rvDoc struct {
	Raw               map[string]any `json:"-"`
	AssessmentCurrent struct {
		Assessments []rvAssessment     `json:"assessments"`
		Required    []rvRequiredResult `json:"required"`
	} `json:"assessmentCurrency"`
	Governance struct {
		Relaxations []struct {
			Kind       string `json:"kind"`
			Subject    string `json:"subject"`
			Change     string `json:"change"`
			Comparison string `json:"comparison"`
			Relaxation bool   `json:"relaxation"`
		} `json:"relaxations"`
		Dispositions []struct {
			Assessment string `json:"assessment"`
			Kind       string `json:"kind"`
			Identity   string `json:"identity"`
			State      string `json:"state"`
		} `json:"dispositions"`
	} `json:"governance"`
	Analysis struct {
		Mode        string `json:"mode"`
		CacheHits   int    `json:"cacheHits"`
		CacheMisses int    `json:"cacheMisses"`
	} `json:"analysis"`
	Diagnostics []struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Severity string `json:"severity"`
	} `json:"diagnostics"`
	Rules []struct {
		RuleID  string `json:"ruleId"`
		Verdict string `json:"verdict"`
		Reasons []struct {
			Code string `json:"code"`
		} `json:"reasons"`
	} `json:"rules"`
	Findings []struct {
		RuleID      string `json:"ruleId"`
		Disposition string `json:"disposition"`
		Verdict     string `json:"verdict"`
		Message     string `json:"message"`
		Related     []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"related"`
	} `json:"findings"`
	Exceptions struct {
		ScopedWaivers []struct {
			ID           string   `json:"id"`
			State        string   `json:"state"`
			Verification any      `json:"verification"`
			Fingerprints []string `json:"fingerprints"`
		} `json:"scopedWaivers"`
	} `json:"exceptions"`
	Summary struct {
		Verdict  string `json:"verdict"`
		ExitCode int    `json:"exitCode"`
		Waived   int    `json:"waived"`
	} `json:"summary"`
	Code int `json:"-"`
}

type rvTrigger struct {
	Class      string   `json:"class"`
	Change     string   `json:"change"`
	Dependency string   `json:"dependency"`
	Assessment string   `json:"assessment"`
	Action     string   `json:"action"`
	Assumption []string `json:"assumption"`
	Path       []string `json:"path"`
	Evidence   []string `json:"evidence"`
}

type rvAssessment struct {
	ID            string `json:"id"`
	Currency      string `json:"currency"`
	UnknownReason string `json:"unknownReason"`
	Approval      *struct {
		ID        string `json:"id"`
		Mechanism string `json:"mechanism"`
		Verified  bool   `json:"verified"`
	} `json:"approval"`
	Reasons []string `json:"reasons"`
	Details []string `json:"details"`
	Causes  []struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	} `json:"causes"`
	NotApplied  []map[string]any `json:"notApplied"`
	Regressions []map[string]any `json:"regressions"`
	Triggers    []rvTrigger      `json:"triggers"`
	Version     int              `json:"version"`
}

type rvRequiredResult struct {
	ID            string   `json:"id"`
	Assessment    string   `json:"assessment"`
	Currency      string   `json:"currency"`
	UnknownReason string   `json:"unknownReason"`
	Verdict       string   `json:"verdict"`
	Reasons       []string `json:"reasons"`
}

// assessment gives the entry of an assessment id (the only version of the fixtures).
func (d *rvDoc) assessment(t *testing.T, id string) *rvAssessment {
	t.Helper()

	for i := range d.AssessmentCurrent.Assessments {
		if d.AssessmentCurrent.Assessments[i].ID == id {
			return &d.AssessmentCurrent.Assessments[i]
		}
	}

	t.Fatalf("results have no assessment %s: %+v", id, d.AssessmentCurrent.Assessments)

	return nil
}

func (d *rvDoc) required(t *testing.T, id string) *rvRequiredResult {
	t.Helper()

	for i := range d.AssessmentCurrent.Required {
		if d.AssessmentCurrent.Required[i].ID == id {
			return &d.AssessmentCurrent.Required[i]
		}
	}

	t.Fatalf("results have no required assessment %s: %+v", id, d.AssessmentCurrent.Required)

	return nil
}

func (d *rvDoc) verdict(rule string) string {
	for _, r := range d.Rules {
		if r.RuleID == rule {
			return r.Verdict
		}
	}

	return ""
}

func (d *rvDoc) hasDiagnostic(code string) bool {
	for _, diag := range d.Diagnostics {
		if diag.Code == code {
			return true
		}
	}

	return false
}

// currencySection is the assessmentCurrency object of the results without the evaluation time.
func (d *rvDoc) currencySection() map[string]any {
	section, _ := d.Raw["assessmentCurrency"].(map[string]any)
	out := map[string]any{}

	for k, v := range section {
		if k != "evaluatedAt" {
			out[k] = v
		}
	}

	return out
}

// check runs `concordia --format json check <args>` with the suite clock and wants the exit code.
func (r *repo) check(wantCode int, args ...string) *rvDoc {
	r.t.Helper()

	return r.checkWith(wantCode, rvEnv(), args...)
}

func (r *repo) checkWith(wantCode int, env []string, args ...string) *rvDoc {
	r.t.Helper()

	res := r.cli(env, append([]string{"--format", "json", "check"}, args...)...)

	doc := new(rvDoc)
	if err := json.Unmarshal([]byte(res.stdout), doc); err != nil {
		r.t.Fatalf("check %v (exit %d) wrote no JSON: %v\nstderr: %s", args, res.code, err, res.stderr)
	}

	if err := json.Unmarshal([]byte(res.stdout), &doc.Raw); err != nil {
		r.t.Fatal(err)
	}

	doc.Code = res.code

	if wantCode >= 0 && res.code != wantCode {
		r.t.Fatalf("check %v: exit %d, want %d\nstderr: %s\nassessments: %+v", args, res.code, wantCode, res.stderr, doc.AssessmentCurrent.Assessments)
	}

	return doc
}

// want checks the currency and the reasons of an assessment.
func (a *rvAssessment) want(t *testing.T, currency string, reasons ...string) {
	t.Helper()

	if a.Currency != currency {
		t.Errorf("%s: currency %q (reasons %v, details %v, causes %+v), want %q", a.ID, a.Currency, a.Reasons, a.Details, a.Causes, currency)
	}

	for _, reason := range reasons {
		if !contains(a.Reasons, reason) {
			t.Errorf("%s: reasons %v lack %q", a.ID, a.Reasons, reason)
		}
	}
}

// editFile replaces text in a file of the repository and fails when the text is absent.
func (r *repo) editFile(rel, from, to string) {
	r.t.Helper()

	content := r.read(rel)
	if !strings.Contains(content, from) {
		r.t.Fatalf("%s has no %q", rel, from)
	}

	r.write(rel, strings.Replace(content, from, to, 1))
}

// sameJSON reports whether two decoded values are equal.
func sameJSON(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

// toAnchor commits the files to `main` (the trust anchor) and rebuilds the candidate on it.
func (r *repo) toAnchor(message string, files map[string]string) {
	r.t.Helper()

	r.git("switch", "-q", reviewBase)

	for rel, content := range files {
		r.write(rel, content)
	}

	r.commitAll(message)
	r.git("switch", "-q", "candidate")
	r.git("merge", "-q", "--no-edit", reviewBase)

	if res := r.gu("index"); res.code != 0 {
		r.t.Fatalf("index after the merge (%d): %s", res.code, res.stderr)
	}
}

// commit commits the working tree and indexes the new revision (a check on a stale graph is exit 3).
func (r *repo) commit(message string) {
	r.t.Helper()

	r.commitAll(message)

	if res := r.gu("index"); res.code != 0 {
		r.t.Fatalf("index (%d): %s", res.code, res.stderr)
	}
}

// messages gives the messages of the diagnostics with the code.
func (d *rvDoc) messages(code string) []string {
	var out []string

	for _, diag := range d.Diagnostics {
		if diag.Code == code {
			out = append(out, diag.Message)
		}
	}

	return out
}

// anyContains reports whether one of the values contains the part.
func anyContains(values []string, part string) bool {
	for _, v := range values {
		if strings.Contains(v, part) {
			return true
		}
	}

	return false
}

// undisposed gives the identities of the removed items that no verified approval accounts for.
func (d *rvDoc) undisposed(assessmentID string) []string {
	var out []string

	for _, disp := range d.Governance.Dispositions {
		if disp.Assessment == assessmentID && disp.State == "undisposed" {
			out = append(out, disp.Kind+":"+disp.Identity)
		}
	}

	return out
}

// trigger gives the first trigger of the assessment whose dependency or evidence contains the part.
func (a *rvAssessment) triggerFor(part string) *rvTrigger {
	for i := range a.Triggers {
		t := &a.Triggers[i]
		if strings.Contains(t.Dependency, part) || anyContains(t.Evidence, part) {
			return t
		}
	}

	return nil
}

// addOrigin gives the repository a bare `origin` with its branches, so that `origin/main` is a ref.
func (r *repo) addOrigin() {
	r.t.Helper()

	origin := filepath.Join(r.t.TempDir(), "origin.git")

	r.git("clone", "-q", "--bare", r.dir, origin)
	r.git("remote", "add", "origin", origin)
	r.git("fetch", "-q", "origin")
}

// gitOut runs git in the repository and gives its output.
func (r *repo) gitOut(args ...string) string {
	r.t.Helper()

	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)

	res, err := procexec.Run(r.t.Context(), "git", args, procexec.Options{Dir: r.dir, Env: env, Timeout: gitTimeout})
	if err != nil || res.Code != 0 {
		r.t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, res.Stderr)
	}

	return res.Stdout
}
