package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// The review rules of a run (task 8.3): the pack is enabled, assessments are required, and `check` evaluates the
// currency once and maps it to the verdicts of design D8.

const (
	reviewPolicyKey = "policies:\n  files: [\"policies/*.policy.yaml\"]\n  packs:\n    - use: builtin:review-currency\n"

	reviewAssessment = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-BILL
version: 1
type: security
title: Billing review
scope:
  calmNodes: [billing-service]
environment: prod
subject:
  summary: The billing service
  dependencies:
    - kind: protocol
      selector:
        calmNodes: [billing-service]
assumptions: []
evidence: []
outcome: satisfactory
state: ready-for-review
`

	reviewRequired = "  required:\n    - {id: REQ-BILL, assessment: ASM-BILL, type: security, environment: prod, authority: sec}\n"
)

// reviewKeys is the `assessments` key of a workspace with one authority and a required assessment. The trust anchor
// is set when anchor is.
func reviewKeys(anchor bool) string {
	text := "assessments:\n  paths: [assessments]\n"
	if anchor {
		text += "  trustAnchor: {ref: origin/main}\n"
	}

	return text + "  authorities:\n    - {id: sec, role: reviewer, mechanism: protected-review, reviewers: [alice@example.com], paths: [\"assessments/**\"]}\n" + reviewRequired
}

const reviewEnvironments = "environments:\n  - {id: prod, sources: [\"k8s/prod/**\"]}\n"

// reviewSetup is the demo workspace with the review pack, the environment prod, and the given extra keys.
func reviewSetup(t *testing.T, extra string, files map[string]string) *setup {
	t.Helper()

	tree := map[string]string{"assessments/ASM-BILL/v1.yaml": reviewAssessment}
	for k, v := range files {
		tree[k] = v
	}

	return newSetup(t, tree, &setupOpts{ws: reviewWorkspace(extra), graph: buildGraph(graphOpts{noEdges: true})})
}

func onlyReview(o *conformance.CheckOptions) {
	o.Rules = []string{"REV-APR-001", "REV-CAP-001", "REV-DSP-001", "REV-GOV-001"}
}

func reviewRuleOf(t *testing.T, run *conformance.Run, id string) *conformance.RuleResult {
	t.Helper()

	for i := range run.Rules {
		if run.Rules[i].RuleID == id {
			return &run.Rules[i]
		}
	}

	t.Fatalf("no rule %s in the run", id)

	return nil
}

// checkMarker is the `check` line of wsText: a `check:` block in extra replaces it.
const checkMarker = "check: {minimumCoverage: 0.5, unknown: fail, failOn: error}\n"

// reviewWorkspace is the demo workspace with the review pack and the environment prod. A `check:` block in extra
// replaces the default one.
func reviewWorkspace(extra string) string {
	text := wsText(reviewPolicyKey + reviewEnvironments)
	if i := strings.Index(extra, "check:"); i >= 0 {
		return strings.Replace(text, checkMarker, "", 1) + extra
	}

	return text + extra
}

func checkBlock(mode string) string {
	return "check:\n  minimumCoverage: 0.5\n  unknown: fail\n  failOn: error\n  enforcement:\n    - {pack: review-currency, mode: " + mode + "}\n"
}

func enforcementEntry(mode string) string { return checkBlock(mode) }

// requireResultsSchemaValid checks that the results.json of the run validates against the schema.
func requireResultsSchemaValid(t *testing.T, run *conformance.Run) {
	t.Helper()

	if errs := conformance.ValidateResultJSON(canonicalOf(t, run)); len(errs) > 0 {
		t.Fatalf("results.json does not validate: %v", errs)
	}
}

// isAdvisoryReviewViolation says whether the rule is an advisory violation of the review-currency family.
func isAdvisoryReviewViolation(rule *conformance.RuleResult) bool {
	return allConditionsHold(rule.Verdict == conformance.VerdictViolation, rule.Enforcement == policy.EnforcementAdvisory, rule.Family == policy.FamilyAssessmentCurrency)
}

// lastFindingOf gives the last finding of the rule, nil when it has none.
func lastFindingOf(run *conformance.Run, ruleID string) *conformance.Finding {
	var found *conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == ruleID {
			found = &run.Findings[i]
		}
	}

	return found
}

// isAdvisoryApprovalFinding says whether the finding is the advisory violation of the required assessment ASM-BILL.
func isAdvisoryApprovalFinding(f *conformance.Finding) bool {
	if f == nil || f.Location == nil {
		return false
	}

	return allConditionsHold(
		f.Enforcement == policy.EnforcementAdvisory, f.Result == conformance.VerdictViolation, f.Location.Path == "assessments/ASM-BILL/v1.yaml",
		f.NextAction == "get review", f.Area == conformance.AreaAssessmentCurrency, f.Subject == "ASM-BILL",
	)
}

// hasViolatingReviewSections says whether the run has the review sections, with one required assessment in violation.
func hasViolatingReviewSections(run *conformance.Run) bool {
	cur := run.AssessmentCurrency
	if cur == nil || len(cur.Required) != 1 {
		return false
	}

	return cur.Required[0].Verdict == conformance.VerdictViolation && run.Governance != nil && run.Analysis != nil
}

// requireNeutralMessage checks that a review message never says vulnerable or compliant.
func requireNeutralMessage(t *testing.T, message string) {
	t.Helper()

	if strings.Contains(message, "vulnerable") || strings.Contains(message, "compliant") {
		t.Errorf("message = %q", message)
	}
}

// requireReviewRequiredArea checks that the area is a violation with one assessment that needs review.
func requireReviewRequiredArea(t *testing.T, run *conformance.Run) {
	t.Helper()

	area := run.Areas.AssessmentCurrency
	if area.Verdict != conformance.AreaVerdict(conformance.VerdictViolation) || area.Counts["reviewRequired"] != 1 {
		t.Errorf("area = %+v", area)
	}
}

// Scenario: Advisory shows without blocking.
func TestRequiredAssessmentWithoutApprovalIsAnAdvisoryViolation(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(false), nil)
	run := s.mustCheck(t, onlyReview)

	rule := reviewRuleOf(t, run, "REV-APR-001")
	if !isAdvisoryReviewViolation(rule) {
		t.Fatalf("REV-APR-001 = %+v", rule)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Fatalf("exit code = %d, want 0 for an advisory rule", run.Summary.ExitCode)
	}

	found := lastFindingOf(run, "REV-APR-001")
	if !isAdvisoryApprovalFinding(found) {
		t.Fatalf("finding = %+v", found)
	}

	requireNeutralMessage(t, found.Message)
	requireReviewRequiredArea(t, run)

	if !hasViolatingReviewSections(run) {
		t.Fatalf("sections = %+v %+v %+v", run.AssessmentCurrency, run.Governance, run.Analysis)
	}

	requireResultsSchemaValid(t, run)
}

// gitWithAnchor sets a FakeGit factory whose trust anchor origin/main is readable: its workspace file is anchorWorkspace.
func (s *setup) gitWithAnchor(anchorWorkspace string) {
	s.gitWithAnchorFiles(map[string]string{"concordia.yaml": anchorWorkspace})
}

// gitWithAnchorFiles sets a FakeGit factory whose trust anchor origin/main is readable and holds the given files.
func (s *setup) gitWithAnchorFiles(files map[string]string) {
	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{
			Rev: "abc123", Refs: map[string]string{"origin/main": "anchor1"}, Ancestors: map[[2]string]bool{{"anchor1", "abc123"}: true},
			FilesAt: map[string]map[string]string{"anchor1": files},
		}
	}
}

// flakyShowGit answers the first read of the anchor workspace file and fails every later one, like a transient git
// error between ReadAnchor and the governance comparison.
type flakyShowGit struct {
	reads *atomic.Int32
	workspace.FakeGit
}

func (g *flakyShowGit) ShowFile(ctx context.Context, rev, path string) (data []byte, found bool, err error) {
	if path == "concordia.yaml" && g.reads.Add(1) > 1 {
		return nil, false, errors.New("transient git failure")
	}

	return g.FakeGit.ShowFile(ctx, rev, path)
}

// The anchor resolves, but its workspace file cannot be read again for the governance comparison: REV-GOV-001 is
// unknown with trust-anchor-unreadable, never a pass with an empty change list.
func TestGovernanceIsUnknownWhenTheAnchorWorkspaceCannotBeCompared(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(true), nil)
	anchorText := testutil.ReadFile(t, filepath.Join(s.dir, "concordia.yaml"))

	var reads atomic.Int32

	s.deps.CreateGit = func(string) workspace.GitPort {
		return &flakyShowGit{
			FakeGit: workspace.FakeGit{
				Rev: "abc123", Refs: map[string]string{"origin/main": "anchor1"}, Ancestors: map[[2]string]bool{{"anchor1", "abc123"}: true},
				FilesAt: map[string]map[string]string{"anchor1": {"concordia.yaml": anchorText}},
			},
			reads: &reads,
		}
	}

	run := s.mustCheck(t, onlyReview)

	if rule := reviewRuleOf(t, run, "REV-GOV-001"); rule.Verdict != conformance.VerdictUnknown {
		t.Fatalf("REV-GOV-001 = %+v, want unknown", rule)
	}
}

// Scenario: A25 manual reviewed flag without authorized approval.
// Scenario: Review required blocks.
// A required assessment that only claims a review has no verified approval: the mandatory gate blocks with exit 1.
func TestMandatoryReviewViolationBlocks(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(true)+enforcementEntry("mandatory"), nil)
	run := s.mustCheck(t, onlyReview)

	if rule := reviewRuleOf(t, run, "REV-APR-001"); rule.Verdict != conformance.VerdictViolation || rule.Enforcement != policy.EnforcementMandatory {
		t.Fatalf("REV-APR-001 = %+v", rule)
	}

	if run.Summary.ExitCode != diag.Violation {
		t.Fatalf("exit code = %d, want 1", run.Summary.ExitCode)
	}
}

func approvalFor(t *testing.T, s *setup) string {
	t.Helper()

	set, err := assessment.Load(s.dir, []string{"assessments"})
	if err != nil {
		t.Fatal(err)
	}

	return `apiVersion: concordia.io/v1alpha1
kind: AssessmentApproval
id: ASM-BILL-v1-1
decision: approved
assessment:
  id: ASM-BILL
  version: 1
  digest: ` + set.Assessments[0].Digest + `
subject:
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000002
  dependencies: []
scope:
  calmNodes: [billing-service]
environment: prod
assumptions: []
evidence: []
contractVersion: review-digest/1
capabilityProfile: none
reviewer:
  identity: alice@example.com
  authority: sec
decidedAt: 2026-05-01T10:00:00Z
producedUnder:
  normalizationVersion: normalize/1
  evaluatorVersions: {}
  policyDigest: sha256:0000000000000000000000000000000000000000000000000000000000000003
  authorityDigest: sha256:0000000000000000000000000000000000000000000000000000000000000004
`
}

// approvalRecordWithApproveDigests is the approval of the setup with the digests of the helpers that `assessment
// approve` uses (the policy digest, the authority digest, the subject digest).
func approvalRecordWithApproveDigests(t *testing.T, base *setup) string {
	t.Helper()

	wsp, err := workspace.Load(base.env.WorkspacePath, "")
	if err != nil {
		t.Fatal(err)
	}

	files, _, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil {
		t.Fatal(err)
	}

	return strings.NewReplacer(
		"sha256:0000000000000000000000000000000000000000000000000000000000000002", assessment.SubjectDigest(nil),
		"sha256:0000000000000000000000000000000000000000000000000000000000000003", assessment.PolicyDigest(files),
		"sha256:0000000000000000000000000000000000000000000000000000000000000004", assessment.AuthorityDigest(wsp.Assessments.Authorities),
		"capabilityProfile: none", "capabilityProfile: cbm-0.12.1",
		"normalizationVersion: normalize/1", "normalizationVersion: \""+observation.NormalizationVersion+"\"",
	).Replace(approvalFor(t, base))
}

// isUnreadableAnchorUnknown says whether the rule is unknown for the one reason `trust-anchor-unreadable`.
func isUnreadableAnchorUnknown(rule *conformance.RuleResult) bool {
	if rule.Verdict != conformance.VerdictUnknown || len(rule.Reasons) != 1 {
		return false
	}

	return rule.Reasons[0].Code == "trust-anchor-unreadable"
}

// isUnreadableAnchorFinding says whether the finding is the unknown that an unreadable trust anchor gives.
func isUnreadableAnchorFinding(f *conformance.Finding) bool {
	return allConditionsHold(f.Result == conformance.VerdictUnknown, f.Reason == conformance.ReasonTrustAnchorUnreadable, f.NextAction == "fix analysis configuration")
}

// Scenario: Trust anchor cannot be read.
// An approval cannot be verified without the trust anchor: the currency is unknown with the reason
// `trust-anchor-unreadable`, and a mandatory gate exits 4.
func TestUnknownCurrencyBlocksAMandatoryGate(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(true)+enforcementEntry("mandatory"), nil)
	s = reviewSetup(t, reviewKeys(true)+enforcementEntry("mandatory"), map[string]string{"assessments/ASM-BILL/approvals/1.yaml": approvalFor(t, s)})

	run := s.mustCheck(t, onlyReview)

	rule := reviewRuleOf(t, run, "REV-APR-001")
	if !isUnreadableAnchorUnknown(rule) {
		t.Fatalf("REV-APR-001 = %s %+v", rule.Verdict, rule.Reasons)
	}

	if run.Summary.ExitCode != diag.Unknown {
		t.Fatalf("exit code = %d, want 4", run.Summary.ExitCode)
	}

	for i := range run.Findings {
		f := &run.Findings[i]
		if f.RuleID == "REV-APR-001" && !isUnreadableAnchorFinding(f) {
			t.Errorf("finding = %+v", f)
		}
	}

	// Scenario: Unknown mandatory currency not hidden. The summary is not an all-clear.
	if run.Summary.Verdict != conformance.VerdictUnknown || run.Areas.AssessmentCurrency.Verdict != conformance.AreaVerdict(conformance.VerdictUnknown) {
		t.Errorf("summary %s, area %+v", run.Summary.Verdict, run.Areas.AssessmentCurrency)
	}
}

// Scenario: Mandatory gate loses coverage at run time.
// A mandatory review rule whose assessment depends on a kind that the run has no coverage record for: the verified
// approval cannot be compared, the currency is `unknown` with `coverage-incomplete`, the run does not pass, and the
// exit code is 4.
func TestAMandatoryGateThatLosesCoverageIsUnknownAndExitsFour(t *testing.T) {
	t.Parallel()

	keys := reviewKeys(true)
	assessmentText := strings.Replace(reviewAssessment, "kind: protocol", "kind: runtime-identity", 1)
	base := reviewSetup(t, keys+enforcementEntry("mandatory"), map[string]string{"assessments/ASM-BILL/v1.yaml": assessmentText})

	record := approvalRecordWithApproveDigests(t, base)

	const path = "assessments/ASM-BILL/approvals/1.yaml"

	s := reviewSetup(t, keys+enforcementEntry("mandatory"), map[string]string{"assessments/ASM-BILL/v1.yaml": assessmentText, path: record})
	s.gitWithAnchorFiles(map[string]string{"concordia.yaml": reviewWorkspace(keys + enforcementEntry("mandatory")), path: record})

	run := s.mustCheck(t, onlyReview)

	entry := run.AssessmentCurrency.Assessments[0]
	if entry.Currency != "unknown" || entry.UnknownReason != "coverage-incomplete" {
		t.Fatalf("entry = %+v", entry)
	}

	if rule := reviewRuleOf(t, run, "REV-APR-001"); rule.Verdict != conformance.VerdictUnknown {
		t.Fatalf("REV-APR-001 = %s %+v", rule.Verdict, rule.Reasons)
	}

	if run.Summary.Verdict == conformance.VerdictPass || run.Summary.ExitCode != diag.Unknown {
		t.Errorf("summary = %+v, want no pass and exit 4", run.Summary)
	}
}

// Scenario: Mandatory without authority.
func TestMandatoryReviewRuleWithoutAnAuthorityIsRefused(t *testing.T) {
	t.Parallel()

	keys := "assessments:\n  paths: [assessments]\n  trustAnchor: {ref: origin/main}\n" + reviewRequired
	s := reviewSetup(t, keys+enforcementEntry("mandatory"), nil)

	failure := s.failureOf(t, onlyReview)
	if failure.Diagnostic.Code != diag.ASM004 || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("diagnostic = %+v, want CO-ASM-004 with exit 2", failure)
	}
}

// Scenario: Rollback covers review rules.
// A live violation of a mandatory review rule gives exit 1; with an active rollback the rule runs as shadow, the
// finding and the verdict are kept, the rollback is recorded, and the exit code is 0.
func TestRollbackRunsReviewRulesAsShadow(t *testing.T) {
	t.Parallel()

	keys := reviewKeys(true) + checkBlock("mandatory") +
		"  rollback:\n    reason: review migration\n    authorizedBy: jane.doe\n    expires: \"2099-01-01\"\n"
	s := reviewSetup(t, keys, nil)
	run := s.mustCheck(t, onlyReview)

	rule := reviewRuleOf(t, run, "REV-APR-001")
	if rule.Verdict != conformance.VerdictViolation || rule.Enforcement != policy.EnforcementShadow {
		t.Fatalf("REV-APR-001 = %s %s, want a shadow violation", rule.Verdict, rule.Enforcement)
	}

	if run.Summary.ExitCode != diag.OK {
		t.Fatalf("exit code = %d, want 0 under a rollback", run.Summary.ExitCode)
	}

	if rb := run.Summary.Rollback; rb == nil || !strings.Contains(rb.Message, "review rules ran as shadow") {
		t.Fatalf("rollback = %+v", rb)
	}

	requireShadowViolationFindings(t, run, "REV-APR-001")

	if run.Areas.AssessmentCurrency.Verdict == conformance.AreaVerdict(conformance.VerdictPass) {
		t.Errorf("area = %+v: a rollback never shows a pass", run.Areas.AssessmentCurrency)
	}
}

// requireShadowViolationFindings checks that every finding of the rule is a shadow violation.
func requireShadowViolationFindings(t *testing.T, run *conformance.Run, ruleID string) {
	t.Helper()

	for i := range run.Findings {
		f := &run.Findings[i]
		if f.RuleID != ruleID {
			continue
		}

		if f.Enforcement != policy.EnforcementShadow || f.Result != conformance.VerdictViolation {
			t.Errorf("finding = %+v", f)
		}
	}
}

// Scenario: A31 candidate removes its own authority.
// The candidate lowers the review rules from mandatory to advisory. The trust anchor still says mandatory: the gate
// keeps the stronger mode, and REV-GOV-001 has the verdict violation.
func TestACandidateCannotLowerItsOwnReviewEnforcement(t *testing.T) {
	t.Parallel()

	anchor := reviewWorkspace(reviewKeys(true) + checkBlock("mandatory"))
	candidate := reviewKeys(true) + enforcementEntry("advisory")

	s := reviewSetup(t, candidate, nil)
	s.gitWithAnchor(anchor)

	run := s.mustCheck(t, onlyReview)

	gov := reviewRuleOf(t, run, "REV-GOV-001")
	if gov.Verdict != conformance.VerdictViolation {
		t.Fatalf("REV-GOV-001 = %s %+v, want a violation", gov.Verdict, gov.Reasons)
	}

	if gov.Enforcement != policy.EnforcementMandatory || run.Summary.ExitCode != diag.Violation {
		t.Errorf("enforcement %s, exit %d: the anchor's mandatory mode still gates", gov.Enforcement, run.Summary.ExitCode)
	}

	if !hasEnforcementRelaxation(run.Governance) {
		t.Errorf("governance = %+v", run.Governance)
	}
}

// hasEnforcementRelaxation says whether the first change of the governance report is a relaxation of the enforcement.
func hasEnforcementRelaxation(report *conformance.GovernanceReport) bool {
	if report == nil || len(report.Relaxations) == 0 {
		return false
	}

	first := report.Relaxations[0]

	return first.Relaxation && first.Kind == "enforcement"
}

// Scenario: Stricter change. A candidate equal to the trust anchor has no relaxation and REV-GOV-001 passes.
func TestACandidateEqualToTheAnchorHasNoRelaxation(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(true), nil)
	s.gitWithAnchor(reviewWorkspace(reviewKeys(true)))

	run := s.mustCheck(t, onlyReview)
	if gov := reviewRuleOf(t, run, "REV-GOV-001"); gov.Verdict != conformance.VerdictPass {
		t.Fatalf("REV-GOV-001 = %s %+v", gov.Verdict, gov.Reasons)
	}

	if !isAnchoredReviewInputs(&run.Inputs) {
		t.Errorf("inputs = %+v", run.Inputs)
	}
}

// isAnchoredReviewInputs says whether the inputs name the trust anchor, the authority digest, and one assessment file.
func isAnchoredReviewInputs(inputs *conformance.RunInputs) bool {
	return allConditionsHold(inputs.TrustAnchor == "anchor1", inputs.AuthorityDigest != "", len(inputs.Assessments) == 1)
}

// Scenario: A44 only a Kubernetes manifest changed.
// Scenario: Currency not reduced.
// `check --changed` reduces edge findings and never the currency: the assessment is evaluated and its currency equals
// the one of a full run.
func TestChangedRunDoesNotReduceAssessmentCurrency(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(false), nil)
	full := s.mustCheck(t, onlyReview)

	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{
			Rev: "abc123", Files: []string{billingFile},
			Changed: workspace.ChangeSet{Ref: "HEAD", Base: "abc123abc123abc123", Files: []string{"k8s/prod/deploy.yaml"}},
		}
	}

	changed := s.mustCheck(t, func(o *conformance.CheckOptions) {
		onlyReview(o)
		o.Changed = "HEAD"
	})

	if rule := reviewRuleOf(t, changed, "REV-APR-001"); rule.ChangedScope != "full" || rule.Verdict != conformance.VerdictViolation {
		t.Fatalf("REV-APR-001 = %+v", rule)
	}

	// The evaluation time is the wall clock of each run.
	full.AssessmentCurrency.EvaluatedAt, changed.AssessmentCurrency.EvaluatedAt = "", ""

	a, b := currencyJSON(t, full), currencyJSON(t, changed)
	if !bytes.Equal(a, b) {
		t.Fatalf("currency differs between a full and a changed run:\n%s\n%s", a, b)
	}

	if changed.Areas.AssessmentCurrency.Verdict != full.Areas.AssessmentCurrency.Verdict {
		t.Errorf("area %+v, want %+v", changed.Areas.AssessmentCurrency, full.Areas.AssessmentCurrency)
	}
}

// currencyJSON gives the JSON of the assessment currency section of the run.
func currencyJSON(t *testing.T, run *conformance.Run) []byte {
	t.Helper()

	data, err := json.Marshal(run.AssessmentCurrency)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// Scenario: No assessments required.
func TestRulesAreNotApplicableWithoutRequiredAssessments(t *testing.T) {
	t.Parallel()

	for name, extra := range map[string]string{
		"no assessments key": "",
		"nothing required":   "assessments:\n  paths: [assessments]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := reviewSetup(t, extra, nil)
			run := s.mustCheck(t, onlyReview)

			for _, id := range []string{"REV-APR-001", "REV-DSP-001", "REV-CAP-001", "REV-GOV-001"} {
				if rule := reviewRuleOf(t, run, id); rule.Verdict != conformance.VerdictNotApplicable {
					t.Errorf("%s = %s, want not-applicable", id, rule.Verdict)
				}
			}

			if run.Areas.AssessmentCurrency.Verdict != conformance.AreaNotConfigured {
				t.Errorf("area = %+v, want not-configured", run.Areas.AssessmentCurrency)
			}

			if run.Summary.ExitCode != diag.OK {
				t.Errorf("exit code = %d", run.Summary.ExitCode)
			}
		})
	}
}

// Scenario: Record on the trust anchor.
// Scenario: Current approval passes.
// An approval that carries the digests of the helpers `assessment approve` uses (the policy digest, the authority
// digest, the subject digest) and sits on the trust anchor is verified, and the currency is `current` when nothing
// changed. A mismatch of the helpers would make every approval of `approve` review-required.
func TestAnApprovalWithTheApproveDigestsIsCurrent(t *testing.T) {
	t.Parallel()

	keys := reviewKeys(true)
	record := approvalRecordWithApproveDigests(t, reviewSetup(t, keys, nil))

	const path = "assessments/ASM-BILL/approvals/1.yaml"

	s := reviewSetup(t, keys, map[string]string{path: record})
	s.gitWithAnchorFiles(map[string]string{"concordia.yaml": reviewWorkspace(keys), path: record})

	run := s.mustCheck(t, onlyReview)

	entry := &run.AssessmentCurrency.Assessments[0]
	if !isCurrentVerifiedApproval(entry) {
		t.Fatalf("entry = %+v approval %+v", entry, entry.Approval)
	}

	if rule := reviewRuleOf(t, run, "REV-APR-001"); rule.Verdict != conformance.VerdictPass {
		t.Fatalf("REV-APR-001 = %s %+v", rule.Verdict, rule.Reasons)
	}

	if run.Areas.AssessmentCurrency.Verdict != conformance.AreaVerdict(conformance.VerdictPass) || len(run.Inputs.Approvals) != 1 {
		t.Errorf("area %+v, inputs %+v", run.Areas.AssessmentCurrency, run.Inputs)
	}
}

// isCurrentVerifiedApproval says whether the entry is current on a verified approval of the protected-review mechanism.
func isCurrentVerifiedApproval(entry *conformance.AssessmentEntry) bool {
	if entry.Currency != "current" || entry.Approval == nil {
		return false
	}

	return entry.Approval.Verified && entry.Approval.Mechanism == "protected-review"
}

// The schema accepts the full shape of the review sections: a trigger, an approval, a not-applied record, a limit stop,
// a relaxation, and a disposition.
func TestResultsSchemaAcceptsTheFullReviewSections(t *testing.T) {
	t.Parallel()

	run := reviewSetup(t, reviewKeys(false), nil).mustCheck(t, onlyReview)

	entry := &run.AssessmentCurrency.Assessments[0]
	entry.Triggers = []impact.ReviewTrigger{{
		Class: impact.ClassExposure, Change: impact.ChangeModified, Dependency: "protocol:billing", Assumption: []string{"A-1"}, Assessment: "ASM-BILL@v1",
		Path: []string{"protocol:billing", "ASM-BILL@v1"}, Evidence: []string{"A-1"},
		Related: []impact.Related{{Class: impact.ClassBoundary, Change: impact.ChangeAdded, Dependency: "protocol:x"}}, Action: impact.ActionGetReview,
	}}
	entry.Approval = &conformance.CurrencyApproval{
		ID: "AP-1", Decision: "approved", Reviewer: "alice@example.com", Authority: "sec", Mechanism: "protected-review", Verified: true,
		DecidedAt: "2026-05-01T10:00:00Z", ExpiresAt: "2027-05-01T10:00:00Z", Conditions: []string{"a route is added"},
	}
	entry.NotApplied = []conformance.CurrencyNotApplied{{Record: "AP-2", Kind: "approval", Authority: "sec", Reason: "not-on-anchor", Detail: "d"}}
	entry.Causes = []conformance.CurrencyCause{{Reason: "limit-exceeded", Detail: "d"}}
	entry.Analysis.Incomplete = &impact.Incomplete{Limit: impact.LimitDepth, Configured: 2, Visited: 3}
	run.Analysis.Incomplete = entry.Analysis.Incomplete
	run.Governance.Relaxations = []conformance.GovernanceChange{{Kind: "enforcement", Subject: "pack=review-currency", Change: "modified", Comparison: "looser", Relaxation: true, Digest: "sha256:x", Detail: "d"}}
	run.Governance.Dispositions = []conformance.DispositionEntry{{Assessment: "ASM-BILL", Kind: "protocol", Identity: "billing", State: "disposed", Disposition: "moved", MovedTo: "billing-2"}}

	requireResultsSchemaValid(t, run)
}

// isPlainFullAnalysis says whether the analysis is a full one with no cache counters.
func isPlainFullAnalysis(a *conformance.AnalysisReport) bool {
	return a != nil && a.Mode == conformance.AnalysisFull && a.CacheHits == 0 && a.CacheMisses == 0
}

// incrementalReviewOnly selects the review rules of an incremental run.
func incrementalReviewOnly(o *conformance.CheckOptions) {
	onlyReview(o)
	o.Incremental = true
}

// Scenario: Plain check writes nothing under .concordia/cache; `--incremental` does, and `analysis` says so.
func TestOnlyAnIncrementalRunWritesTheImpactCache(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(false), nil)
	cacheDir := filepath.Join(s.dir, ".concordia", "cache", "impact")

	plain := s.mustCheck(t, onlyReview)
	if _, err := os.Stat(cacheDir); err == nil {
		t.Fatal("a plain check wrote the impact cache")
	}

	if !isPlainFullAnalysis(plain.Analysis) {
		t.Fatalf("analysis = %+v", plain.Analysis)
	}

	incremental := s.mustCheck(t, incrementalReviewOnly)

	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no cache file after an incremental run: %v", err)
	}

	if incremental.Analysis.Mode != conformance.AnalysisIncremental || incremental.Analysis.CacheMisses == 0 {
		t.Errorf("analysis = %+v", incremental.Analysis)
	}

	again := s.mustCheck(t, incrementalReviewOnly)

	if again.Analysis.CacheHits == 0 {
		t.Errorf("second analysis = %+v, want cache hits", again.Analysis)
	}

	requireResultsSchemaValid(t, again)
}

// Scenario: Counters present.
// A run that evaluates assessments shows the analysis counters in the run manifest and in results.json.
func TestAnalysisCountersAreInTheManifestAndInResults(t *testing.T) {
	t.Parallel()

	s := reviewSetup(t, reviewKeys(false), nil)
	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		onlyReview(o)
		o.Incremental = true
		o.SkipReports = false
	})

	keys := []string{"visited", "closureSize", "invalidations", "cacheHits", "cacheMisses", "incompleteAnalyses"}

	var tree struct {
		Analysis map[string]any `json:"analysis"`
	}

	if err := json.Unmarshal(canonicalOf(t, run), &tree); err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		Analysis map[string]any `json:"analysis"`
	}

	readJSON(t, filepath.Join(s.dir, ".concordia", "runs", run.Run.ID, "manifest.json"), &manifest)

	for name, got := range map[string]map[string]any{"results.json": tree.Analysis, "manifest.json": manifest.Analysis} {
		for _, key := range keys {
			if _, ok := got[key].(float64); !ok {
				t.Errorf("%s analysis.%s = %v, want a number", name, key, got[key])
			}
		}
	}

	if misses, _ := manifest.Analysis["cacheMisses"].(float64); misses == 0 {
		t.Errorf("manifest analysis = %v, want cache misses of an incremental run", manifest.Analysis)
	}
}

// Each unit of a review rule is an obligation with the mode of its environment: the ids are those of the findings,
// the list is sorted, and the rule verdict stays the worst.
func TestReviewRulesExposeTheirObligations(t *testing.T) {
	t.Parallel()

	run := reviewSetup(t, reviewKeys(false), nil).mustCheck(t, onlyReview)

	rule := reviewRuleOf(t, run, "REV-APR-001")
	if rule.Verdict != conformance.VerdictViolation || len(rule.Obligations) != 1 {
		t.Fatalf("REV-APR-001 = %s with %d obligations", rule.Verdict, len(rule.Obligations))
	}

	obl := &rule.Obligations[0]
	if !isAdvisoryApprovalObligation(obl) {
		t.Fatalf("obligation = %+v", obl)
	}

	for i := range run.Findings {
		if f := &run.Findings[i]; f.RuleID == "REV-APR-001" && f.ObligationID != obl.ID {
			t.Errorf("finding obligation id %s, want %s", f.ObligationID, obl.ID)
		}
	}

	requireResultsSchemaValid(t, run)
}

// isAdvisoryApprovalObligation says whether the obligation is the advisory violation of the assessment ASM-BILL in prod.
func isAdvisoryApprovalObligation(obl *conformance.ObligationResult) bool {
	return allConditionsHold(
		obl.Verdict == conformance.VerdictViolation, obl.Environment == "prod", obl.Subject == "ASM-BILL",
		obl.Enforcement == policy.EnforcementAdvisory, obl.Occurrences != nil,
	)
}
