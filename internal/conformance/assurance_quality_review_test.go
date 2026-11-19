package conformance_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// The reviewed mode and the review dependency kinds of the assurance evidence, through the real pipeline (tasks 9.5
// and 10.2). The world has a quality assessment that depends on the admitted check result `unit-tests` and on two
// scenarios, a measured one and a reviewed one. The approval is made the way `assessment approve` makes it
// (assessment.Bind), so it is current until the check result or a scenario changes.

var qrNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const (
	qrApprovalPath   = "assessments/ASM-QUAL/approvals/1.yaml"
	qrAssessmentPath = "assessments/ASM-QUAL/v1.yaml"

	qrAssessments = "assessments:\n  paths: [assessments]\n  trustAnchor: {ref: origin/main}\n" +
		"  authorities:\n    - {id: sec, role: reviewer, mechanism: protected-review, reviewers: [alice@example.com], paths: [\"assessments/**\"]}\n"

	qrAssurance = "assurance:\n  producers:\n    - id: ci\n      claims: [check-result]\n" +
		"      authentication: {method: cosign-keyless, issuer: \"https://issuer.example\", identity: ci@example}\n" +
		"  verification:\n    results: [\"results/*.json\"]\n    maxAgeDays: 14\n" +
		"  quality:\n    scenarios: [\"quality/*.yaml\"]\n"
)

// qrWorld is one state of the workspace: the scenario threshold, the outcome of the check result, and the outcome that
// the assessment states. The approval is made against one state and checked against another.
type qrWorld struct {
	// outcome is the outcome of the check result `unit-tests` (`passed` or `failed`).
	outcome string
	// assessmentOutcome is the outcome that the quality assessment states.
	assessmentOutcome string
	// threshold is the threshold of the measured scenario.
	threshold float64
	// noAssessments leaves the `assessments` key out of the workspace.
	noAssessments bool
}

func qrBase() *qrWorld {
	return &qrWorld{threshold: 30, outcome: "passed", assessmentOutcome: "satisfactory"}
}

func (w *qrWorld) workspace() string {
	text := wsText(reviewEnvironments + qrAssurance)
	if !w.noAssessments {
		text += qrAssessments
	}

	return text
}

func (w *qrWorld) assessment() string {
	return `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-QUAL
version: 1
type: quality
title: Quality review
scope:
  calmNodes: [billing-service]
environment: prod
subject:
  summary: The billing quality scenarios
  dependencies:
    - kind: verification
      selector:
        check: [unit-tests]
  scenarios: [QS-FAILOVER, QS-RUNBOOK]
assumptions: []
evidence: []
outcome: ` + w.assessmentOutcome + `
state: ready-for-review
`
}

func (w *qrWorld) scenarios() string {
	measured := strings.Replace(measuredScenario("mandatory"), "threshold: 30", "threshold: "+strconv.FormatFloat(w.threshold, 'g', -1, 64), 1)

	return scenarioFile(measured, reviewedScenario("QS-RUNBOOK", "operability", "mandatory"))
}

// bundle is the signed check result of the world. The cosign of the run is a fake that accepts exactly this file.
func (w *qrWorld) bundle(t *testing.T) []byte {
	t.Helper()

	predicate := map[string]any{
		"check": "unit-tests", "outcome": w.outcome, "revision": "abc123", "environment": "prod",
		"evaluator": map[string]string{"name": "go-test", "version": "1.0.0"},
		"time":      qrNow.Add(-time.Hour).Format(time.RFC3339), "covers": []string{"billing-service"},
	}

	payload, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": statement.PredicateCheckResult, "predicate": predicate,
		"subject": []map[string]any{{"name": "subject", "digest": map[string]string{"sha256": strings.Repeat("c", 64)}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(map[string]any{
		"mediaType":            "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "aGludA=="}},
		"dsseEnvelope": map[string]any{
			"payloadType": statement.PayloadType,
			"payload":     base64.StdEncoding.EncodeToString(payload),
			"signatures":  []map[string]string{{"sig": "c2ln"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// fakeCosign writes a cosign that accepts a bundle exactly when its bytes are the signed ones.
func qrFakeCosign(t *testing.T, signed []byte) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	// The script compares the bundle with the signed bytes using POSIX `cmp`, so it needs no GNU coreutils.
	dir := t.TempDir()

	want := filepath.Join(dir, "signed.json")
	if err := os.WriteFile(want, signed, 0o600); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
if [ "$1" = version ]; then echo 'GitVersion:    v3.0.2'; exit 0; fi
bundle=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--bundle" ]; then bundle="$2"; fi
  shift
done
if cmp -s "$bundle" "` + want + `"; then echo "Verified OK" >&2; exit 0; fi
echo "Error: verifying blob attestation: no matching signatures" >&2
exit 1
`

	path := filepath.Join(dir, "cosign")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// setup builds the workspace of the world. approval is the text of the approval record, "" for none: the record is
// then also in the trust anchor, which is how `protected-review` verifies it.
func (w *qrWorld) setup(t *testing.T, approval string) *setup {
	t.Helper()

	bundle := w.bundle(t)
	files := map[string]string{
		"quality/scenarios.yaml": w.scenarios(), qrAssessmentPath: w.assessment(), "results/unit-tests.json": string(bundle),
	}

	if approval != "" {
		files[qrApprovalPath] = approval
	}

	s := newSetup(t, files, &setupOpts{ws: w.workspace(), graph: buildGraph(graphOpts{noEdges: true})})
	exe := qrFakeCosign(t, bundle)

	s.deps.Now = func() time.Time { return qrNow }
	s.deps.Getenv = func(key string) string {
		if key == "COSIGN" {
			return exe
		}

		return ""
	}

	if approval != "" {
		s.deps.CreateGit = func(string) workspace.GitPort {
			return &workspace.FakeGit{
				Rev: "abc123", Refs: map[string]string{"origin/main": "anchor1"}, Ancestors: map[[2]string]bool{{"anchor1", "abc123"}: true},
				FilesAt: map[string]map[string]string{"anchor1": {"concordia.yaml": w.workspace(), qrApprovalPath: approval}},
			}
		}
	}

	return s
}

func qrOptions(s *setup) *conformance.CheckOptions {
	return &conformance.CheckOptions{Env: s.env, Deps: s.deps, SkipReports: true, Rules: []string{"CO-BND-001"}}
}

// approve gives the text of the approval record that `assessment approve` would write for the world: the binding of
// the assessment to the facts of the world, signed by the reviewer of the authority.
func (w *qrWorld) approve(t *testing.T) string {
	t.Helper()

	s := w.setup(t, "")

	wsp, err := workspace.Load(s.env.WorkspacePath, "")
	if err != nil {
		t.Fatal(err)
	}

	set, err := assessment.Load(wsp.Root, []string{"assessments"})
	if err != nil {
		t.Fatal(err)
	}

	files, _, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil {
		t.Fatal(err)
	}

	facts, err := conformance.LoadReviewFacts(context.Background(), qrOptions(s))
	if err != nil {
		t.Fatalf("review facts: %v", err)
	}

	binding, err := assessment.Bind(context.Background(), &assessment.EvalInput{
		Set: set, Facts: facts, Now: func() time.Time { return qrNow }, PolicyDigest: assessment.PolicyDigest(files),
		AuthorityDigest: assessment.AuthorityDigest(wsp.Assessments.Authorities),
	}, "ASM-QUAL", 1)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	if len(binding.Unknown) > 0 {
		t.Fatalf("the subject cannot be computed: %+v", binding.Unknown)
	}

	record := &assessment.Approval{
		ID: "ASM-QUAL-v1-1", Decision: assessment.DecisionApproved, Assessment: binding.Assessment, Subject: binding.Subject, Scope: binding.Scope,
		Environment: binding.Environment, Assumptions: binding.Assumptions, Evidence: binding.Evidence, ContractVersion: binding.ContractVersion,
		CapabilityProfile: binding.CapabilityProfile, ProducedUnder: binding.ProducedUnder,
		Reviewer:  assessment.Reviewer{Identity: "alice@example.com", Authority: "sec"},
		DecidedAt: qrNow.Add(-30 * time.Minute), Previous: binding.Previous, Delta: binding.Delta,
	}
	record.File = qrApprovalPath

	text, err := assessment.RenderApproval(record)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

func (w *qrWorld) check(t *testing.T, approval string) *conformance.Run {
	t.Helper()

	run, err := conformance.RunCheck(context.Background(), qrOptions(w.setup(t, approval)))
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}

	return run
}

func qrAssessmentOf(t *testing.T, run *conformance.Run) *conformance.AssessmentEntry {
	t.Helper()

	if run.AssessmentCurrency == nil || len(run.AssessmentCurrency.Assessments) != 1 {
		t.Fatalf("assessment currency = %+v", run.AssessmentCurrency)
	}

	return &run.AssessmentCurrency.Assessments[0]
}

// qrRequireCurrency fails the test unless the one assessment of the run has the currency, and gives the entry.
func qrRequireCurrency(t *testing.T, run *conformance.Run, want string) *conformance.AssessmentEntry {
	t.Helper()

	entry := qrAssessmentOf(t, run)
	if entry.Currency != want {
		t.Fatalf("assessment = %s %v %+v", entry.Currency, entry.Reasons, entry.Causes)
	}

	return entry
}

// qrIsReviewed says whether the entry is the result of a scenario of the reviewed mode with the verdict.
func qrIsReviewed(entry *conformance.QualityEntry, verdict conformance.RuleVerdict) bool {
	return entry.Verdict == verdict && entry.Mode == "reviewed"
}

// qrCitesAssessment says whether the assessment `ASM-QUAL` is the one evidence of the entry.
func qrCitesAssessment(entry *conformance.QualityEntry) bool {
	if len(entry.Evidence) != 1 {
		return false
	}

	return entry.Evidence[0].Kind == "assessment" && entry.Evidence[0].Ref == "ASM-QUAL"
}

// qrIsReviewedModeUnavailable says whether the entry is `unknown` because the workspace has no assessments key.
func qrIsReviewedModeUnavailable(entry *conformance.QualityEntry) bool {
	if entry.Verdict != conformance.VerdictUnknown || entry.Reason != conformance.ReasonReviewedModeNotAvailable {
		return false
	}

	return len(entry.Details) == 1 && entry.Details[0] == "the workspace has no assessments key"
}

// Scenario: Approved current assessment. A current approved quality assessment with the outcome `satisfactory` makes
// the reviewed scenario `pass`, labelled `reviewed`, with the assessment as its evidence.
func TestAReviewedScenarioPassesWithACurrentApprovedAssessment(t *testing.T) {
	t.Parallel()

	state := qrBase()
	run := state.check(t, state.approve(t))

	qrRequireCurrency(t, run, "current")

	entry := qualityEntryOf(t, run, "QS-RUNBOOK")
	if !qrIsReviewed(entry, conformance.VerdictPass) || !qrCitesAssessment(entry) {
		t.Errorf("entry = %+v", entry)
	}

	// The measured scenario next to it has no measurement: the approval of the assessment does not satisfy it.
	if measured := qualityEntryOf(t, run, "QS-FAILOVER"); measured.Verdict != conformance.VerdictUnknown || measured.Mode != "measured" {
		t.Errorf("measured entry = %+v", measured)
	}
}

// Scenario: Approved assessment with an unsatisfactory outcome. It is a violation, never a pass.
func TestAnUnsatisfactoryApprovedAssessmentIsAViolation(t *testing.T) {
	t.Parallel()

	state := qrBase()
	state.assessmentOutcome = "unsatisfactory"

	run := state.check(t, state.approve(t))

	entry := qualityEntryOf(t, run, "QS-RUNBOOK")
	if !qrIsReviewed(entry, conformance.VerdictViolation) {
		t.Errorf("entry = %+v", entry)
	}

	if rule := qualityRuleOf(t, run, "QS-RUNBOOK"); rule.Verdict != conformance.VerdictViolation || run.Summary.ExitCode != 1 {
		t.Errorf("rule %s, exit %d", rule.Verdict, run.Summary.ExitCode)
	}
}

// Scenario: Assessment needs review. Without an approval the reviewed scenario is `unknown` and the state is the detail.
func TestAReviewedScenarioWithoutAnApprovalIsUnknown(t *testing.T) {
	t.Parallel()

	run := qrBase().check(t, "")

	entry := qualityEntryOf(t, run, "QS-RUNBOOK")
	if !qrIsReviewed(entry, conformance.VerdictUnknown) || len(entry.Details) == 0 {
		t.Errorf("entry = %+v", entry)
	}
}

// Scenario: Reviewed mode without assessments. A workspace with no `assessments` key has no review evaluation: the
// scenario is `unknown` with `reviewed-mode-not-available` and the detail says why. Structural and measured scenarios
// are unaffected.
func TestAReviewedScenarioWithoutAnAssessmentsKeyIsNotAvailable(t *testing.T) {
	t.Parallel()

	state := qrBase()
	state.noAssessments = true

	run := state.check(t, "")

	entry := qualityEntryOf(t, run, "QS-RUNBOOK")
	if !qrIsReviewedModeUnavailable(entry) {
		t.Errorf("entry = %+v", entry)
	}

	if measured := qualityEntryOf(t, run, "QS-FAILOVER"); measured.Reason != conformance.ReasonNoEvidenceSupplied {
		t.Errorf("the measured scenario changed: %+v", measured)
	}
}

// triggerOf finds the trigger of an assessment entry that names the item.
func triggerOf(entry *conformance.AssessmentEntry, item string) *impact.ReviewTrigger {
	for i := range entry.Triggers {
		for _, step := range entry.Triggers[i].Path {
			if strings.Contains(step, item) {
				return &entry.Triggers[i]
			}
		}

		if strings.Contains(entry.Triggers[i].Dependency, item) {
			return &entry.Triggers[i]
		}
	}

	return nil
}

// Scenario: A changed admitted check result. The result is an item of the kind `verification`, so the approval of the
// assessment that depends on it is `review-required` with the trigger class `implementation-verification` and a
// path that names the item.
func TestAChangedCheckResultMakesADependentApprovalReviewRequired(t *testing.T) {
	t.Parallel()

	approval := qrBase().approve(t)

	changed := qrBase()
	changed.outcome = "failed"

	run := changed.check(t, approval)

	entry := qrAssessmentOf(t, run)
	if entry.Currency != "review-required" {
		t.Fatalf("assessment = %s %v %+v", entry.Currency, entry.Reasons, entry.Causes)
	}

	trigger := triggerOf(entry, "check:unit-tests@prod")
	if trigger == nil || trigger.Class != impact.ClassImplementation {
		t.Fatalf("triggers = %+v", entry.Triggers)
	}

	// The reviewed scenario reads the same evaluation: it is no pass.
	if got := qualityEntryOf(t, run, "QS-RUNBOOK"); got.Verdict != conformance.VerdictUnknown {
		t.Errorf("reviewed entry = %+v", got)
	}
}

// Scenario: A changed scenario response measure. The scenario is an item of the kind `quality-scenario`.
func TestAChangedResponseMeasureMakesADependentApprovalReviewRequired(t *testing.T) {
	t.Parallel()

	approval := qrBase().approve(t)

	changed := qrBase()
	changed.threshold = 60

	run := changed.check(t, approval)

	entry := qrAssessmentOf(t, run)
	if entry.Currency != "review-required" {
		t.Fatalf("assessment = %s %v %+v", entry.Currency, entry.Reasons, entry.Causes)
	}

	trigger := triggerOf(entry, "scenario:QS-FAILOVER")
	if trigger == nil || trigger.Class != impact.ClassImplementation {
		t.Fatalf("triggers = %+v", entry.Triggers)
	}
}

// The approval stays current when nothing that it depends on changed.
func TestAnUnchangedWorldKeepsTheApprovalCurrent(t *testing.T) {
	t.Parallel()

	approval := qrBase().approve(t)

	if entry := qrAssessmentOf(t, qrBase().check(t, approval)); entry.Currency != "current" {
		t.Fatalf("assessment = %s %v %+v", entry.Currency, entry.Reasons, entry.Causes)
	}
}
