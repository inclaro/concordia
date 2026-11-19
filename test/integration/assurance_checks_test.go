package integration_test

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/reporting"
)

// The check-result world is the infra workspace with one environment (`production`), the CALM service `api-service`
// (bound to the source root `app/**`, where the recording has a `TESTS_FILE` and `TESTS` association), a control that
// lists the check `unit-tests`, and an `assurance` block that trusts the producer `ci` for check results. The
// statements are signed bundles; the cosign that verifies them is a fake that accepts the bundles it was given.

var chkNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const (
	chkArchitecture = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {
      "unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.",
      "controls": {
        "verification-001": {
          "description": "The unit tests pass",
          "requirements": [{
            "requirement-url": "https://calm.finos.org/release/1.2/meta/control.json",
            "config": {"engine": "concordia", "check-ids": ["unit-tests"]}
          }]
        }
      }
    }
  ],
  "relationships": []
}
`

	chkBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-api-source
    architectureRef: calm:node:api-service
    required: false
    match:
      any:
        - sourceRoot: app/**
`

	chkAssurance = `assurance:
  producers:
    - id: ci
      claims: [check-result]
      authentication: {method: cosign-keyless, issuer: "https://issuer.example", identity: ci@example}
  verification:
    results: ["build/results/*.json"]
    maxAgeDays: 14
`

	chkRule = `  - id: CHK-001
    name: The unit tests pass for production
    severity: error
    require:
      checkResult: {check: unit-tests, environment: production, subject: {calmNodes: [api-service]}}
`
)

// chkResult is one check result file of the world.
type chkResult struct {
	file string
	// outcome is `passed` unless set.
	outcome string
	// revision is the evaluated revision unless set.
	revision string
	// environment is `production` unless set.
	environment string
	// untrusted signs the bundle for no declared producer: the fake cosign refuses it.
	untrusted bool
}

type chkWorld struct {
	// rules replaces the rules of the policy file.
	rules string
	// check replaces the `check` section of the workspace.
	check   string
	results []chkResult
}

func chkHead(t *testing.T) string {
	t.Helper()

	return strings.Replace(deploymentWorkspaceHead, "  files: []\n  packs:\n    - use: builtin:infrastructure\n", "  files: [policies/**/*.policy.yaml]\n", 1)
}

func chkRevision(t *testing.T) string {
	t.Helper()

	var rec observation.Recording
	if err := json.Unmarshal(mustRead(t, infraRecordingFile), &rec); err != nil {
		t.Fatal(err)
	}

	return rec.HeadSHA
}

func (w *chkWorld) spec(t *testing.T) *deploymentSpec {
	t.Helper()

	files := map[string]string{
		"concordia.yaml":              chkHead(t) + cmp.Or(w.check, deploymentCheck) + effEnvironment + chkAssurance,
		"policies/checks.policy.yaml": effPolicyHead + cmp.Or(w.rules, chkRule),
	}

	var signed [][]byte

	for _, result := range w.results {
		predicate := map[string]any{
			"check": "unit-tests", "outcome": cmp.Or(result.outcome, "passed"), "revision": cmp.Or(result.revision, chkRevision(t)),
			"environment": cmp.Or(result.environment, "production"),
			"evaluator":   map[string]string{"name": "go-test", "version": "1.0.0"},
			"time":        chkNow.Add(-time.Hour).Format(time.RFC3339), "covers": []string{"api-service"},
		}

		data := provBundle(t, statement.PredicateCheckResult, predicate, strings.Repeat("c", 64))
		files["build/results/"+result.file] = string(data)

		if !result.untrusted {
			signed = append(signed, data)
		}
	}

	exe := provFakeCosign(t, signed)

	return &deploymentSpec{
		files: files, architecture: chkArchitecture, bindings: chkBindings, environments: effEnvironment, withoutPack: true, noIndex: true,
		edit: func(opts *conformance.CheckOptions) {
			opts.Deps.Now = func() time.Time { return chkNow }
			opts.Deps.Getenv = func(key string) string {
				if key == "COSIGN" {
					return exe
				}

				return ""
			}
		},
	}
}

func (w *chkWorld) run(t *testing.T) *conformance.Run {
	t.Helper()

	return deploymentRun(t, w.spec(t)).run
}

// effOneOf gives the one obligation of a check rule.
func chkObligation(t *testing.T, run *conformance.Run) *conformance.ObligationResult {
	t.Helper()

	return effOne(t, run, "CHK-001")
}

// chkControl gives the control of the run with the id, or nil when the run has none.
func chkControl(run *conformance.Run, id string) *conformance.ControlResult {
	for i := range run.Controls {
		if run.Controls[i].ControlID == id {
			return &run.Controls[i]
		}
	}

	return nil
}

// chkIsUnknownWithoutEvidence reports whether the obligation is unknown with the reason that no evidence was supplied,
// and fails the test when it is not.
func chkIsUnknownWithoutEvidence(t *testing.T, got *conformance.ObligationResult) bool {
	t.Helper()

	if got.Verdict == conformance.VerdictUnknown && got.Reason == conformance.ReasonNoEvidenceSupplied {
		return true
	}

	t.Errorf("obligation = %s %q %q, want %s %q", got.Verdict, got.Reason, got.Detail, conformance.VerdictUnknown, conformance.ReasonNoEvidenceSupplied)

	return false
}

// chkEntryFacts is what an entry of the check result list says about one result.
type chkEntryFacts struct {
	integrity   assurance.Integrity
	producer    string
	check       string
	outcome     string
	environment string
	reasons     int
	admissible  bool
}

func chkFactsOf(entry *conformance.CheckResultEntry) chkEntryFacts {
	return chkEntryFacts{
		admissible: entry.Admissible, integrity: entry.Integrity, producer: entry.Producer, check: entry.Check,
		outcome: entry.Outcome, environment: entry.Environment, reasons: len(entry.Reasons),
	}
}

// An admissible passed result gives `pass` with the digest of the statement as evidence (8.6), and the result is listed
// with its admissibility, producer, and integrity (8.8).
func TestCheckResultAdmissiblePassedGivesPass(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{results: []chkResult{{file: "unit.json"}}}).run(t)

	got := chkObligation(t, run)
	if got.Verdict != conformance.VerdictPass {
		t.Fatalf("obligation = %s %q %q, want pass with the result digest", got.Verdict, got.Reason, got.Detail)
	}

	if !strings.Contains(got.Detail, "sha256:") {
		t.Fatalf("obligation = %s %q %q, want pass with the result digest", got.Verdict, got.Reason, got.Detail)
	}

	if len(run.Assurance.CheckResults) != 1 {
		t.Fatalf("check results = %+v, want one", run.Assurance.CheckResults)
	}

	want := chkEntryFacts{
		admissible: true, integrity: assurance.IntegrityAuthenticated, producer: "ci", check: "unit-tests",
		outcome: "passed", environment: "production",
	}
	if facts := chkFactsOf(&run.Assurance.CheckResults[0]); facts != want {
		t.Errorf("entry = %+v, want %+v", run.Assurance.CheckResults[0], want)
	}

	// The control lists the check and has its verdict from it.
	control := chkControl(run, "verification-001")
	if control == nil {
		t.Fatal("control = <nil>, want checkIds [unit-tests] and pass")
	}

	if !slices.Equal(control.CheckIDs, []string{"unit-tests"}) || control.Verdict != conformance.VerdictPass {
		t.Errorf("control = %+v, want checkIds [unit-tests] and pass", control)
	}
}

// chkSarifLevels gives the level of every SARIF result of the run, by rule id.
func chkSarifLevels(t *testing.T, sarif []byte) map[string][]string {
	t.Helper()

	var log struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}

	if err := json.Unmarshal(sarif, &log); err != nil {
		t.Fatal(err)
	}

	levels := map[string][]string{}

	for _, sarifRun := range log.Runs {
		for _, result := range sarifRun.Results {
			levels[result.RuleID] = append(levels[result.RuleID], result.Level)
		}
	}

	return levels
}

// A36: a trusted failed result for the evaluated revision is admissible and gives a violation. The failed check
// appears as a finding in SARIF and JUnit (8.8). The rule runs as `advisory`, because a `check-result` rule cannot be
// `mandatory` (CO-POL-018): SARIF shows a note and JUnit lists it without a failure.
func TestCheckResultFailedRunIsAViolationAndAFinding(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{
		results: []chkResult{{file: "unit.json", outcome: "failed"}},
		check:   deploymentCheck + "  enforcement:\n    - {rules: [CHK-001], mode: advisory}\n",
	}).run(t)

	got := chkObligation(t, run)
	if got.Verdict != conformance.VerdictViolation {
		t.Fatalf("obligation = %s %q %q, want violation", got.Verdict, got.Reason, got.Detail)
	}

	findings := findingsOf(run, "CHK-001")
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one", findings)
	}

	sarif, err := reporting.RenderSARIF(run)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(chkSarifLevels(t, sarif)["CHK-001"], "note") {
		t.Errorf("SARIF has no note result for CHK-001: %s", sarif)
	}

	junit := reporting.RenderJUnit(run)
	if !strings.Contains(junit, "CHK-001") || strings.Contains(junit, "<failure") {
		t.Errorf("JUnit must list CHK-001 without a failure (an advisory rule does not gate):\n%s", junit)
	}
}

// A36 different revision: a trusted passed result for another revision is inadmissible; the obligation is `unknown`
// with `no-evidence-supplied` and the admissibility reason is a detail, never a second reason.
func TestCheckResultOfAnotherRevisionIsInadmissible(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{results: []chkResult{{file: "unit.json", revision: strings.Repeat("e", 40)}}}).run(t)

	got := chkObligation(t, run)
	if !chkIsUnknownWithoutEvidence(t, got) {
		return
	}

	if !strings.Contains(got.Detail, string(assurance.DetailDifferentRevision)) {
		t.Fatalf("obligation = %s %q %q, want unknown no-evidence-supplied with different-revision", got.Verdict, got.Reason, got.Detail)
	}

	entry := run.Assurance.CheckResults[0]
	if entry.Admissible || !slices.Equal(entry.Reasons, []assurance.Detail{assurance.DetailDifferentRevision}) {
		t.Errorf("entry = %+v, want inadmissible with different-revision", entry)
	}
}

// An inadmissible failure (untrusted producer) is `unknown`, never a violation.
func TestCheckResultFromAnUntrustedProducerNeverGivesAViolation(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{results: []chkResult{{file: "unit.json", outcome: "failed", untrusted: true}}}).run(t)

	chkIsUnknownWithoutEvidence(t, chkObligation(t, run))

	if entry := run.Assurance.CheckResults[0]; entry.Admissible || len(entry.Reasons) == 0 {
		t.Errorf("entry = %+v, want inadmissible with a reason", entry)
	}
}

// chkRequireNoViolation fails the test for every rule of the run that is a violation: a missing result is never a
// failed check.
func chkRequireNoViolation(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Rules {
		if run.Rules[i].Verdict == conformance.VerdictViolation {
			t.Errorf("rule %s is a violation: a missing result is never a failed check", run.Rules[i].RuleID)
		}
	}
}

// 8.7: a results glob that matches nothing is the warning CO-PRV-011 and the obligation is `unknown` with
// `no-evidence-supplied`. Nothing is treated as a failed check, and the control that lists the check is unknown with
// the check named.
func TestEmptyResultGlobWarnsAndLeavesTheObligationUnknown(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{}).run(t)

	if !hasDiagnostic(run, diag.PRV011) {
		t.Errorf("no CO-PRV-011 warning in %+v", run.Diagnostics)
	}

	got := chkObligation(t, run)
	if !chkIsUnknownWithoutEvidence(t, got) {
		return
	}

	if !strings.Contains(got.Detail, "build/results/*.json") {
		t.Fatalf("obligation = %s %q %q, want unknown no-evidence-supplied that names the glob", got.Verdict, got.Reason, got.Detail)
	}

	chkRequireNoViolation(t, run)

	if len(run.Assurance.CheckResults) != 0 {
		t.Errorf("check results = %+v, want none", run.Assurance.CheckResults)
	}

	control := chkControl(run, "verification-001")
	if control == nil {
		t.Fatal("control = <nil>, want unknown with one reason no-evidence-supplied")
	}

	if control.Verdict != conformance.VerdictUnknown || len(control.Reasons) != 1 {
		t.Fatalf("control = %+v, want unknown with one reason no-evidence-supplied", control)
	}

	if control.Reasons[0].Code != string(conformance.ReasonNoEvidenceSupplied) {
		t.Errorf("control = %+v, want unknown with one reason no-evidence-supplied", control)
	}
}

// chkRequireCandidateShape reports whether the candidate is a TESTS or TESTS_FILE association with both endpoints, and
// whether it suggests a mapping to the node.
func chkRequireCandidateShape(t *testing.T, candidate *conformance.TestCandidate) (mapped bool) {
	t.Helper()

	if !slices.Contains([]string{"tests", "tests-file"}, candidate.Kind) {
		t.Errorf("candidate kind %q", candidate.Kind)
	}

	if candidate.Test == "" || candidate.Tested == "" {
		t.Errorf("candidate %+v lacks an endpoint", candidate)
	}

	return slices.Contains(candidate.Nodes, "api-service")
}

// chkRequireOnlyCandidateFields fails the test when the candidate has a field other than the two nodes, the kind, and
// the suggested mapping: nothing in it could pass for a result.
func chkRequireOnlyCandidateFields(t *testing.T, candidate conformance.TestCandidate) {
	t.Helper()

	data, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}

	for key := range fields {
		if !slices.Contains([]string{"kind", "test", "tested", "nodes"}, key) {
			t.Errorf("candidate has the field %q: CBM 0.12.1 gives these edges no properties", key)
		}
	}
}

// A36 TESTS: the recording has a TESTS association for the code of `api-service` and no check result exists. The
// obligation is `unknown` with `no-evidence-supplied` and never a pass; the association is listed as a candidate with
// the test node and the tested node only, and its suggested mapping to the node (8.4).
func TestTestsEdgeWithoutResultIsACandidateAndNeverAPass(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{}).run(t)

	got := chkObligation(t, run)
	if !chkIsUnknownWithoutEvidence(t, got) {
		return
	}

	if len(run.Assurance.Candidates) == 0 {
		t.Fatalf("no candidates; the recording has TESTS and TESTS_FILE edges")
	}

	mapped := false

	for i := range run.Assurance.Candidates {
		if chkRequireCandidateShape(t, &run.Assurance.Candidates[i]) {
			mapped = true
		}
	}

	if !mapped {
		t.Errorf("no candidate suggests a mapping to api-service: %+v", run.Assurance.Candidates)
	}

	if !strings.Contains(got.Detail, "candidate") {
		t.Errorf("detail %q does not show the candidate", got.Detail)
	}

	// The candidate holds the two nodes and a suggested mapping, nothing that could pass for a result.
	chkRequireOnlyCandidateFields(t, run.Assurance.Candidates[0])
}

// The list `assurance.checkResults` keeps every result, admissible or not, in a stable order (8.8).
func TestCheckResultsAreListedWithTheirAdmissibility(t *testing.T) {
	t.Parallel()

	run := (&chkWorld{results: []chkResult{
		{file: "unit.json"},
		{file: "other-revision.json", revision: strings.Repeat("e", 40)},
		{file: "untrusted.json", outcome: "failed", untrusted: true},
		{file: "staging.json", environment: "staging"},
	}}).run(t)

	entries := run.Assurance.CheckResults
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}

	admissible := 0

	for _, entry := range entries {
		if entry.Admissible {
			admissible++
		}

		if entry.Admissible != (len(entry.Reasons) == 0) {
			t.Errorf("entry %s is admissible = %v with reasons %v", entry.Path, entry.Admissible, entry.Reasons)
		}
	}

	if admissible != 2 {
		t.Errorf("admissible results = %d, want 2 (production and staging, both for the evaluated revision)", admissible)
	}

	provGolden(t, "check-results.json", entries)
}
