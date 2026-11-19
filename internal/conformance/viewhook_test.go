package conformance_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/testutil"
)

const (
	hookViewID   = "billing-context"
	hookViewPath = "docs/billing.md"

	hookViews = `
views:
  - id: ` + hookViewID + `
    kind: system-context
    origin: authored
    owner: team-billing
    scope: [billing-service]
    abstraction: system
    source:
      path: ` + hookViewPath + `
      format: mermaid
      selector: {title: Billing}
    mapping:
      elements: {billing: billing-service}
`
)

// fakeViews is a view evaluator with a fixed output: presence of the view is a violation of its source file.
func fakeViews(called *int) conformance.EvaluateViewsFunc {
	return func(_ context.Context, in *conformance.ViewEvalInput) (*conformance.ViewEvaluation, error) {
		*called++

		if !hookInputIsComplete(in) {
			return nil, context.Canceled
		}

		owner := "team-billing"
		plan := hookRulePlan(in.OnUnknown)

		return &conformance.ViewEvaluation{
			StructurizrVersion: "2025.11.09",
			Views:              []conformance.ViewResult{hookViewResult(owner)},
			Rules:              []conformance.RuleResult{hookRuleResult(&plan)},
			Findings:           []conformance.Finding{hookFinding(owner)},
		}, nil
	}
}

// hookInputIsComplete says whether the hook got the state, the git facts and the one declared view of the fixture.
func hookInputIsComplete(in *conformance.ViewEvalInput) bool {
	if in.State == nil || in.State.Reconcile == nil {
		return false
	}

	return in.Git != nil && len(in.State.Workspace.Views) == 1
}

func hookRulePlan(onUnknown policy.UnknownDisposition) policy.RulePlan {
	return policy.RulePlan{
		RuleID: "VIEW-PRES-001", Name: "A declared view is committed", Severity: policy.SeverityError, Family: string(policy.FamilyView),
		Pack: policy.PackRef{ID: "views", Version: "1.0.0"}, Predicate: policy.Predicate{Family: policy.FamilyView},
		EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown}, Subjects: []string{hookViewID}, Targets: []string{},
	}
}

func hookViewResult(owner string) conformance.ViewResult {
	digest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	return conformance.ViewResult{
		ID: hookViewID, Kind: "system-context", Origin: "authored", Owner: owner, Abstraction: "system", Severity: "error",
		Scope: []string{"billing-service"}, SemanticDigest: &digest, DigestContract: "concordia-view-semantic/1",
		ProjectionContract: "concordia-view-projection/1", Assessment: conformance.ViewAssessmentCurrency{State: conformance.AssessmentCurrencyNotEvaluated},
		Presence: conformance.ViewPresence{
			Status: conformance.VerdictViolation, Reasons: []string{"not committed"},
			Files: []conformance.ViewPresenceFile{{Path: hookViewPath, Role: "source", Reason: "not committed"}},
		},
		Freshness:      conformance.ViewFreshness{Status: conformance.VerdictNotApplicable, Reasons: []string{}, DifferingDigests: []string{}, ManifestDifferences: []string{}, Artifacts: []conformance.ViewArtifact{}},
		Correspondence: conformance.ViewCorrespondence{Status: conformance.VerdictPass, Method: "structured-source", Source: conformance.ViewLocation{Path: hookViewPath}, Against: []string{}, Compared: []string{}, ByConstruction: []string{}, Differences: []conformance.ViewDifference{}, Unknown: []conformance.ViewUnknownReason{}, Lost: []conformance.ViewLostCoverage{}, Reasons: []string{}},
		Depicts:        conformance.ViewDepicts{Elements: []string{}, Relationships: []string{}},
	}
}

func hookRuleResult(plan *policy.RulePlan) conformance.RuleResult {
	return conformance.RuleResult{
		RuleID: "VIEW-PRES-001", Name: plan.Name, Severity: policy.SeverityError, Policy: plan.Pack, Family: policy.FamilyView,
		Verdict: conformance.VerdictViolation, Reasons: []conformance.Reason{}, Subjects: []string{hookViewID}, Targets: []string{}, Plan: *plan, FindingCount: 1,
	}
}

func hookFinding(owner string) conformance.Finding {
	return conformance.Finding{
		Owner: &owner, Fingerprint: conformance.Fingerprint(conformance.Parts{
			RuleID: "VIEW-PRES-001", SourceArchitectureID: hookViewID, TargetArchitectureID: "source", CanonicalEdgeKind: "view:presence:absent", NormalizedRepositoryPath: hookViewPath,
		}),
		RuleID: "VIEW-PRES-001", Severity: policy.SeverityError, Kind: "view-absent", Message: "view " + hookViewID + ": source " + hookViewPath + ": not committed",
		Source: conformance.Endpoint{ArchitectureID: hookViewID}, Target: conformance.Endpoint{ArchitectureID: "source"},
		EvidencePath: []conformance.EvidenceStep{}, RelatedLocations: []conformance.Location{}, Location: &conformance.Location{Path: hookViewPath},
		Result: conformance.VerdictViolation, NextAction: "supply evidence", Area: conformance.AreaDiagramCurrency,
		Related: []conformance.RelatedRef{{Kind: "view", ID: hookViewID}},
	}
}

// A workspace without views does not call the hook: its result is the result of a run with no hook at all, has no
// `views` key and no `toolchain.structurizr`, and shows the area as not configured.
func TestRunWithoutViewsIsTheRunOfBefore(t *testing.T) {
	t.Parallel()

	without := newSetup(t, nil, nil)
	with := newSetup(t, nil, nil)

	called := 0
	with.deps.EvaluateViews = fakeViews(&called)

	before := without.mustCheck(t, nil)
	after := with.mustCheck(t, nil)

	if called != 0 {
		t.Errorf("the hook ran %d times for a workspace without views", called)
	}

	if stable(t, before) != stable(t, after) {
		t.Error("a run with the hook installed differs from a run without it")
	}

	var tree map[string]any
	if err := json.Unmarshal(canonicalOf(t, after), &tree); err != nil {
		t.Fatal(err)
	}

	if _, present := tree["views"]; present {
		t.Error("a run without views has a `views` key")
	}

	if got := after.Areas.DiagramCurrency.Verdict; got != conformance.AreaNotConfigured {
		t.Errorf("areas.diagramCurrency = %s, want not-configured", got)
	}
}

// The output of the hook reaches results.json and the run manifest, goes through the dispositions and the exit
// code, and validates against the schema.
func TestViewEvaluationReachesTheResult(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, &setupOpts{ws: wsText(hookViews)})
	called := 0
	s.deps.EvaluateViews = fakeViews(&called)

	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.SkipReports = false })

	if called != 1 {
		t.Fatalf("the hook ran %d times", called)
	}

	if len(run.Views) != 1 || run.Views[0].ID != hookViewID {
		t.Fatalf("views = %+v", run.Views)
	}

	assertHookRuleResult(t, run)
	assertHookFindingResult(t, run)

	if run.Summary.ExitCode != diag.Violation {
		t.Errorf("exit code = %d, want %d", run.Summary.ExitCode, diag.Violation)
	}

	assertHookArea(t, run.Areas.DiagramCurrency)

	if run.Toolchain.Structurizr != "2025.11.09" {
		t.Errorf("toolchain.structurizr = %q", run.Toolchain.Structurizr)
	}

	assertHookResultsJSON(t, s.dir)
	assertHookManifest(t, s.dir, run.Run.ID)
}

func assertHookRuleResult(t *testing.T, run *conformance.Run) {
	t.Helper()

	var rule *conformance.RuleResult

	for i := range run.Rules {
		if run.Rules[i].RuleID == "VIEW-PRES-001" {
			rule = &run.Rules[i]
		}
	}

	if rule == nil {
		t.Fatalf("rule = %+v", rule)
	}

	if rule.Verdict != conformance.VerdictViolation || rule.Enforcement != policy.EnforcementMandatory {
		t.Fatalf("rule = %+v", rule)
	}
}

func assertHookFindingResult(t *testing.T, run *conformance.Run) {
	t.Helper()

	var finding *conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == "VIEW-PRES-001" {
			finding = &run.Findings[i]
		}
	}

	if finding == nil {
		t.Fatalf("finding = %+v", finding)
	}

	if finding.Disposition != exceptions.DispositionActive || finding.Enforcement != policy.EnforcementMandatory {
		t.Fatalf("finding = %+v", finding)
	}

	if finding.Area != conformance.AreaDiagramCurrency {
		t.Fatalf("finding = %+v", finding)
	}
}

func assertHookArea(t *testing.T, area conformance.AreaResult) {
	t.Helper()

	if area.Verdict != conformance.AreaVerdict(conformance.VerdictViolation) {
		t.Errorf("area = %+v", area)
	}

	for key, want := range map[string]int{"views": 1, "violation": 1, "pass": 1, "notApplicable": 1, "findings": 1} {
		if area.Counts[key] != want {
			t.Errorf("area = %+v", area)
		}
	}
}

func assertHookResultsJSON(t *testing.T, dir string) {
	t.Helper()

	results := testutil.ReadFile(t, filepath.Join(dir, "build", "concordia", "results.json"))
	if errs := conformance.ValidateResultJSON([]byte(results)); len(errs) > 0 {
		t.Errorf("results.json does not validate: %v", errs)
	}

	var tree struct {
		SchemaVersion string `json:"schemaVersion"`
		Views         []struct {
			ID string `json:"id"`
		} `json:"views"`
	}

	err := json.Unmarshal([]byte(results), &tree)
	if err != nil || len(tree.Views) != 1 {
		t.Fatalf("results.json views = %+v, err %v", tree, err)
	}

	if tree.Views[0].ID != hookViewID || tree.SchemaVersion != "1.1" {
		t.Errorf("results.json views = %+v, err %v", tree, err)
	}
}

// The run manifest records the Structurizr version of the verified export (task 4.4).
func assertHookManifest(t *testing.T, dir, runID string) {
	t.Helper()

	var manifest struct {
		Toolchain struct {
			Structurizr string `json:"structurizr"`
		} `json:"toolchain"`
	}

	readJSON(t, filepath.Join(dir, ".concordia", "runs", runID, "manifest.json"), &manifest)

	if manifest.Toolchain.Structurizr != "2025.11.09" {
		t.Errorf("manifest toolchain.structurizr = %q", manifest.Toolchain.Structurizr)
	}
}

// Violation blocks, a waiver applies, and unknown follows `check.unknown`: the findings of the view rules take the
// existing path (spec "Results carry view results and use the defined exit codes").
func TestViewFindingsFollowTheExistingExitCodePath(t *testing.T) {
	t.Parallel()

	finding := func() conformance.Finding {
		return conformance.Finding{
			RuleID: "VIEW-FRESH-001", Severity: policy.SeverityError, Disposition: exceptions.DispositionActive,
			Enforcement: policy.EnforcementMandatory, Result: conformance.VerdictViolation,
		}
	}
	rule := func(verdict conformance.RuleVerdict, onUnknown policy.UnknownDisposition) conformance.RuleResult {
		return conformance.RuleResult{
			RuleID: "VIEW-FRESH-001", Verdict: verdict, Family: policy.FamilyView, Enforcement: policy.EnforcementMandatory,
			Plan: policy.RulePlan{EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown}},
		}
	}

	t.Run("Violation blocks", func(t *testing.T) {
		t.Parallel()

		f := finding()
		if got := conformance.ComputeExitCode([]conformance.RuleResult{rule(conformance.VerdictViolation, policy.UnknownFail)}, []conformance.Finding{f}, diag.SeverityError); got != diag.Violation {
			t.Errorf("exit code %d, want %d", got, diag.Violation)
		}

		warning := finding()
		warning.Severity = policy.SeverityWarning

		if got := conformance.ComputeExitCode([]conformance.RuleResult{rule(conformance.VerdictViolation, policy.UnknownFail)}, []conformance.Finding{warning}, diag.SeverityError); got != diag.OK {
			t.Errorf("a warning finding exits %d, want 0 below failOn", got)
		}
	})

	t.Run("Unknown follows policy", func(t *testing.T) {
		t.Parallel()

		for onUnknown, want := range map[policy.UnknownDisposition]int{policy.UnknownFail: diag.Unknown, policy.UnknownWarn: diag.OK, policy.UnknownIgnore: diag.OK} {
			if got := conformance.ComputeExitCode([]conformance.RuleResult{rule(conformance.VerdictUnknown, onUnknown)}, nil, diag.SeverityError); got != want {
				t.Errorf("onUnknown %s: exit code %d, want %d", onUnknown, got, want)
			}
		}
	})

	t.Run("Waiver applies", func(t *testing.T) {
		t.Parallel()

		waived := finding()
		waived.Disposition = exceptions.DispositionWaived

		if got := conformance.ComputeExitCode([]conformance.RuleResult{rule(conformance.VerdictViolation, policy.UnknownFail)}, []conformance.Finding{waived}, diag.SeverityError); got != diag.OK {
			t.Errorf("a waived finding exits %d, want 0", got)
		}
	})
}

// The waiver of a view finding applies through the pipeline: the disposition is `waived` and the run passes.
func TestWaiverOfViewFindingApplies(t *testing.T) {
	called := 0

	probe := newSetup(t, nil, &setupOpts{ws: wsText(hookViews)})
	probe.deps.EvaluateViews = fakeViews(&called)

	fingerprint := hookPresenceFingerprint(probe.mustCheck(t, nil))

	waivers := `
apiVersion: concordia.io/v1alpha1
kind: ExceptionRegister
waivers:
  - {id: WV-9, finding: "` + fingerprint + `", rule: VIEW-PRES-001, owner: team-billing, reason: "the diagram is committed with the next release", expires: 2026-12-31}
`
	s := newSetup(t, map[string]string{"exceptions/waivers.yaml": waivers}, &setupOpts{ws: wsText(hookViews)})
	s.deps.EvaluateViews = fakeViews(&called)

	t.Setenv("CONCORDIA_NOW", "2026-06-01T00:00:00Z")

	// The other rules of the fixture do not matter here: one that gives no finding and an ignored unknown.
	run := s.mustCheck(t, func(o *conformance.CheckOptions) {
		o.Rules = []string{"CO-COV-001"}
		o.Unknown = policy.UnknownIgnore
	})

	if len(run.Findings) != 1 {
		t.Fatalf("findings = %d, want the view finding only", len(run.Findings))
	}

	assertHookFindingWaived(t, &run.Findings[0])

	if run.Summary.ExitCode != diag.OK {
		t.Errorf("exit code = %d, want 0", run.Summary.ExitCode)
	}
}

// hookPresenceFingerprint gives the fingerprint of the finding of the view presence rule, "" when the run has none.
func hookPresenceFingerprint(run *conformance.Run) string {
	fingerprint := ""

	for i := range run.Findings {
		if run.Findings[i].RuleID == "VIEW-PRES-001" {
			fingerprint = run.Findings[i].Fingerprint
		}
	}

	return fingerprint
}

func assertHookFindingWaived(t *testing.T, f *conformance.Finding) {
	t.Helper()

	if f.RuleID != "VIEW-PRES-001" || f.Disposition != exceptions.DispositionWaived {
		t.Errorf("finding = %+v", f)
	}

	if f.DispositionRef == nil || *f.DispositionRef != "WV-9" {
		t.Errorf("finding = %+v", f)
	}
}
