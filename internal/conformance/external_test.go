package conformance_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

const advisoryMode = policy.EnforcementAdvisory

func claimPlan(id string, edit func(*policy.RulePlan)) policy.RulePlan {
	return plan(policy.Predicate{
		Family: policy.FamilyAssuranceClaim, Maturity: policy.MaturityAdvisory,
		AssuranceClaim: &policy.ClaimRequirement{ID: id},
	}, func(p *policy.RulePlan) {
		p.RuleID, p.Severity, p.Traversal.EdgeKinds = "ASR-T-001", policy.SeverityWarning, nil
		if edit != nil {
			edit(p)
		}
	})
}

func accessPlan() policy.RulePlan {
	return plan(policy.Predicate{
		Family: policy.FamilyEffectiveAccess, Maturity: policy.MaturityAdvisory,
		EffectiveAccess: &policy.EffectiveAccess{Dimension: policy.DimensionNetwork, Expect: policy.ExpectDenied, Ports: []int{22}},
	}, func(p *policy.RulePlan) {
		p.RuleID, p.Traversal.EdgeKinds = "ASR-T-002", nil
		p.Subjects, p.Targets = []string{"billing"}, []string{"ledger"}
	})
}

func checkPlan() policy.RulePlan {
	return plan(policy.Predicate{
		Family: policy.FamilyCheckResult, Maturity: policy.MaturityAdvisory,
		CheckResult: &policy.CheckResultRequirement{Check: "unit-tests", Environment: "production"},
	}, func(p *policy.RulePlan) {
		p.RuleID, p.Traversal.EdgeKinds = "ASR-T-003", nil
	})
}

func evaluateExternal(t *testing.T, external *conformance.ExternalEvaluation, cfg conformance.EnforcementConfig, plans ...policy.RulePlan) conformance.EvaluateOutput {
	t.Helper()

	input := build(&scenario{plans: plans})
	input.External, input.Enforcement = external, cfg

	return conformance.Evaluate(input)
}

func rule(t *testing.T, out *conformance.EvaluateOutput, id string) conformance.RuleResult {
	t.Helper()

	i := slices.IndexFunc(out.RuleResults, func(r conformance.RuleResult) bool { return r.RuleID == id })
	if i < 0 {
		t.Fatalf("rule %s has no result: %+v", id, out.RuleResults)
	}

	return out.RuleResults[i]
}

// fake gives the outcomes of one rule, as the assurance evaluators would.
func fake(outcomes ...conformance.ExternalOutcome) *conformance.ExternalEvaluation {
	return &conformance.ExternalEvaluation{
		Claims: claims.Default(),
		Rule: func(*policy.RulePlan) ([]conformance.ExternalOutcome, bool) {
			return outcomes, true
		},
	}
}

func TestExternalRulePassesWhenEveryOutcomePasses(t *testing.T) {
	t.Parallel()

	out := evaluateExternal(t, fake(
		conformance.ExternalOutcome{Subject: "production", Environment: "production", Verdict: conformance.VerdictPass},
	), conformance.EnforcementConfig{}, claimPlan("complete-resource-set", nil))

	got := rule(t, &out, "ASR-T-001")
	extRequire(t, "verdict", got.Verdict, conformance.VerdictPass)
	extRequire(t, "finding count", got.FindingCount, 0)
	extRequire(t, "findings", len(out.Findings), 0)

	extRequire(t, "obligations", len(got.Obligations), 1)
	extWant(t, "obligation verdict", got.Obligations[0].Verdict, conformance.VerdictPass)
	extWant(t, "obligation environment", got.Obligations[0].Environment, "production")

	extWant(t, "family", got.Family, policy.FamilyAssuranceClaim)
	extWant(t, "area", conformance.AreaOfFamily(got.Family), conformance.AreaConformance)
}

// assertDriftFindingKind checks that the finding is an active violation of the conformance area.
func assertDriftFindingKind(t *testing.T, finding *conformance.Finding) {
	t.Helper()

	extWant(t, "kind", finding.Kind, conformance.KindObligationViolation)
	extWant(t, "result", finding.Result, conformance.VerdictViolation)
	extWant(t, "disposition", finding.Disposition, exceptions.DispositionActive)
	extWant(t, "area", finding.Area, conformance.AreaConformance)
}

// assertDriftFindingIdentity checks the subject, environment, occurrence, and obligation of the finding.
func assertDriftFindingIdentity(t *testing.T, finding *conformance.Finding) {
	t.Helper()

	extWant(t, "subject", finding.Subject, "billing")
	extWant(t, "environment", finding.Environment, "production")
	extWant(t, "occurrence", finding.Occurrence, "app")
	extWantText(t, "obligation id", finding.ObligationID)
}

// assertDriftFindingText checks the observed, expected, and next action text and the owner of the finding.
func assertDriftFindingText(t *testing.T, finding *conformance.Finding, observed ...string) {
	t.Helper()

	for _, text := range observed {
		extWantContains(t, "observed", finding.Observed, text)
	}

	extWantText(t, "expected", finding.Expected)
	extWantText(t, "message", finding.Message)
	extWant(t, "next action", finding.NextAction, "fix implementation")
	extWant(t, "owner", extOwner(finding.Owner), "team-billing")
}

func TestExternalViolationGivesOneTypedFindingForTheOutcome(t *testing.T) {
	t.Parallel()

	const (
		deployed = "sha256:aaaa"
		observed = "sha256:bbbb"
	)

	outcome := conformance.ExternalOutcome{
		Subject: "billing", Environment: "production", Occurrence: "app", Verdict: conformance.VerdictViolation,
		Detail:   "deployed " + deployed + ", observed " + observed,
		Location: &conformance.Location{Path: "deploy/billing.yaml", Line: 12},
	}

	pass := conformance.ExternalOutcome{Subject: "billing", Environment: "production", Occurrence: "sidecar", Verdict: conformance.VerdictPass}

	out := evaluateExternal(t, fake(pass, outcome), conformance.EnforcementConfig{}, claimPlan("runtime-drift", func(p *policy.RulePlan) { p.Severity = policy.SeverityError }))

	got := rule(t, &out, "ASR-T-001")
	extRequire(t, "verdict", got.Verdict, conformance.VerdictViolation)
	extRequire(t, "finding count", got.FindingCount, 1)
	extRequire(t, "findings", len(out.Findings), 1)

	finding := out.Findings[0]

	// The contract of the registry is experimental: an error rule gives a warning finding (design D7).
	extWant(t, "finding severity", finding.Severity, policy.SeverityWarning)
	extWant(t, "rule severity", got.Severity, policy.SeverityWarning)

	wantFingerprint := conformance.TypedFingerprint(&conformance.TypedParts{
		Parts:       conformance.Parts{RuleID: "ASR-T-001", SourceArchitectureID: "billing", CanonicalEdgeKind: "drift"},
		Environment: "production", Occurrence: "app",
	})
	extWant(t, "the typed fingerprint of kind drift", finding.Fingerprint, wantFingerprint)

	assertDriftFindingKind(t, &finding)
	assertDriftFindingIdentity(t, &finding)
	extWant(t, "location path", extLocationPath(finding.Location), "deploy/billing.yaml")
	assertDriftFindingText(t, &finding, deployed, observed)
}

func TestExternalUnknownKeepsItsReasonAndExplainsItInAFinding(t *testing.T) {
	t.Parallel()

	outcome := conformance.ExternalOutcome{
		Subject: "billing", Environment: "production", Verdict: conformance.VerdictUnknown,
		Reason: conformance.ReasonEnforcementNotDeclared, Detail: "network policy enforcement is not declared",
	}

	out := evaluateExternal(t, fake(outcome), conformance.EnforcementConfig{}, claimPlan("network-isolation", nil))

	got := rule(t, &out, "ASR-T-001")
	extRequire(t, "verdict", got.Verdict, conformance.VerdictUnknown)
	extWantReasons(t, got.Reasons, "enforcement-not-declared")

	extRequire(t, "findings", len(out.Findings), 1)

	finding := out.Findings[0]
	extWant(t, "kind", finding.Kind, conformance.KindObligationUnknown)
	extWant(t, "severity", finding.Severity, policy.SeverityInfo)
	extWant(t, "reason", finding.Reason, conformance.ReasonEnforcementNotDeclared)
	extWant(t, "next action", finding.NextAction, "fix analysis configuration")

	// The same rule that ignores unknown results explains nothing, and the verdict stays unknown.
	ignoring := claimPlan("network-isolation", func(p *policy.RulePlan) { p.EvidenceRequirements.OnUnknown = policy.UnknownIgnore })
	out = evaluateExternal(t, fake(outcome), conformance.EnforcementConfig{}, ignoring)

	extWant(t, "ignoring rule verdict", rule(t, &out, "ASR-T-001").Verdict, conformance.VerdictUnknown)
	extWant(t, "ignoring rule findings", len(out.Findings), 0)
}

func TestExternalRuleVerdictIsTheWorstOutcome(t *testing.T) {
	t.Parallel()

	pass := conformance.ExternalOutcome{Subject: "a", Verdict: conformance.VerdictPass}
	unknown := conformance.ExternalOutcome{Subject: "b", Verdict: conformance.VerdictUnknown, Reason: conformance.ReasonNoEvidenceSupplied, Detail: "no provenance for this digest"}
	violation := conformance.ExternalOutcome{Subject: "c", Verdict: conformance.VerdictViolation, Detail: "drift"}
	notApplicable := conformance.ExternalOutcome{Subject: "d", Verdict: conformance.VerdictNotApplicable}
	broken := conformance.ExternalOutcome{Subject: "e", Verdict: conformance.VerdictError, Detail: "evaluator failed"}

	for _, tc := range []struct { //nolint:govet // a test table.
		name     string
		outcomes []conformance.ExternalOutcome
		want     conformance.RuleVerdict
		findings int
	}{
		{"all pass", []conformance.ExternalOutcome{pass, pass}, conformance.VerdictPass, 0},
		{"pass and not applicable", []conformance.ExternalOutcome{pass, notApplicable}, conformance.VerdictPass, 0},
		{"pass and unknown", []conformance.ExternalOutcome{pass, unknown}, conformance.VerdictUnknown, 1},
		{"unknown and violation", []conformance.ExternalOutcome{unknown, violation, pass}, conformance.VerdictViolation, 2},
		{"violation and error", []conformance.ExternalOutcome{violation, broken}, conformance.VerdictError, 1},
		{"only not applicable", []conformance.ExternalOutcome{notApplicable}, conformance.VerdictNotApplicable, 0},
		{"no outcome", nil, conformance.VerdictNotApplicable, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := evaluateExternal(t, fake(tc.outcomes...), conformance.EnforcementConfig{}, claimPlan("exact-lineage", nil))

			extWant(t, "verdict", rule(t, &out, "ASR-T-001").Verdict, tc.want)
			extWant(t, "findings", len(out.Findings), tc.findings)
		})
	}
}

func TestExternalUnknownWithoutAClosedReasonIsAnError(t *testing.T) {
	t.Parallel()

	for _, reason := range []conformance.UnknownReason{"", "stale"} {
		out := evaluateExternal(t, fake(conformance.ExternalOutcome{Subject: "a", Verdict: conformance.VerdictUnknown, Reason: reason}),
			conformance.EnforcementConfig{}, claimPlan("exact-lineage", nil))

		got := rule(t, &out, "ASR-T-001")
		extWant(t, "verdict for reason "+string(reason), got.Verdict, conformance.VerdictError)
		extWantReasons(t, got.Reasons, diag.CON099)
	}
}

// assertUnknownWithReason checks that the first rule is unknown with the one reason, in its obligations and findings.
func assertUnknownWithReason(t *testing.T, out *conformance.EvaluateOutput, want conformance.UnknownReason, subjects int) {
	t.Helper()

	got := out.RuleResults[0]
	extRequire(t, "verdict", got.Verdict, conformance.VerdictUnknown)
	extWantReasons(t, got.Reasons, string(want))

	extWant(t, "reason is valid", want.Valid(), true)
	extRequire(t, "obligations", len(got.Obligations), subjects)
	extWant(t, "obligation reason", got.Obligations[0].Reason, want)

	extRequire(t, "findings", len(out.Findings), subjects)
	extWant(t, "finding reason", out.Findings[0].Reason, want)
}

func TestExternalRulesWithNoEvaluatorAreUnknownWithTheReasonOfTheirFamily(t *testing.T) {
	t.Parallel()

	enabled := &conformance.ExternalEvaluation{Claims: claims.Default()}

	for _, tc := range []struct { //nolint:govet // a test table.
		name     string
		external *conformance.ExternalEvaluation
		plan     policy.RulePlan
		want     conformance.UnknownReason
		subjects int
	}{
		{"effective access", nil, accessPlan(), conformance.ReasonNoEvaluatorAvailable, 1},
		{"check result", nil, checkPlan(), conformance.ReasonNoEvidenceSupplied, 1},
		{"claim without any contract", nil, claimPlan("exact-lineage", nil), conformance.ReasonClaimNotEnabled, 1},
		{"claim outside the registry", enabled, claimPlan("slsa-build-level-3", nil), conformance.ReasonClaimNotEnabled, 1},
		{"claim that the loaded contracts do not enable", &conformance.ExternalEvaluation{Claims: mustRegistry(t)}, claimPlan("exact-lineage", nil), conformance.ReasonClaimNotEnabled, 1},
		{"rendered claim", enabled, claimPlan("complete-resource-set", nil), conformance.ReasonRenderedEvidenceMissing, 1},
		{"provenance claim", enabled, claimPlan("exact-lineage", nil), conformance.ReasonNoEvidenceSupplied, 1},
		{"evaluator that answers false", &conformance.ExternalEvaluation{
			Claims: claims.Default(),
			Rule:   func(*policy.RulePlan) ([]conformance.ExternalOutcome, bool) { return nil, false },
		}, claimPlan("runtime-drift", nil), conformance.ReasonNoEvidenceSupplied, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := evaluateExternal(t, tc.external, conformance.EnforcementConfig{}, tc.plan)

			assertUnknownWithReason(t, &out, tc.want, tc.subjects)
		})
	}
}

func mustRegistry(t *testing.T) *claims.Registry {
	t.Helper()

	registry, err := claims.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	return registry
}

func TestAnEffectiveAccessRuleThatSelectsNothingIsNotApplicable(t *testing.T) {
	t.Parallel()

	noTargets := accessPlan()
	noTargets.Targets = []string{}

	noSubjects := accessPlan()
	noSubjects.Subjects = []string{}

	for name, p := range map[string]policy.RulePlan{"no targets": noTargets, "no subjects": noSubjects} {
		out := evaluateExternal(t, nil, conformance.EnforcementConfig{}, p)

		got := out.RuleResults[0]
		extWant(t, name+": verdict", got.Verdict, conformance.VerdictNotApplicable)
		extWant(t, name+": reasons", len(got.Reasons), 1)
	}
}

func TestExternalUnknownFollowsOnUnknownAndTheEnforcementMode(t *testing.T) {
	t.Parallel()

	mandatory := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Pack: "p", Mode: workspace.ModeMandatory}}}

	for _, tc := range []struct {
		name      string
		onUnknown policy.UnknownDisposition
		cfg       conformance.EnforcementConfig
		want      int
	}{
		{"mandatory and fail", policy.UnknownFail, mandatory, diag.Unknown},
		{"mandatory and warn", policy.UnknownWarn, mandatory, diag.OK},
		{"mandatory and ignore", policy.UnknownIgnore, mandatory, diag.OK},
		{"advisory by default", policy.UnknownFail, conformance.EnforcementConfig{}, diag.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := checkPlan()
			p.EvidenceRequirements.OnUnknown = tc.onUnknown

			out := evaluateExternal(t, nil, tc.cfg, p)
			extRequire(t, "verdict", rule(t, &out, "ASR-T-003").Verdict, conformance.VerdictUnknown)
			extWant(t, "exit code", conformance.ComputeExitCode(out.RuleResults, out.Findings, diag.SeverityError), tc.want)
		})
	}
}

// violationIn is a violation of the billing subject in the environment.
func violationIn(environment string) conformance.ExternalOutcome {
	return conformance.ExternalOutcome{Subject: "billing", Environment: environment, Verdict: conformance.VerdictViolation, Detail: "drift"}
}

func TestExternalViolationGatesOnlyWhereItsEnvironmentIsMandatory(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{
		{Pack: "p", Environment: "production", Mode: workspace.ModeMandatory},
		{Pack: "p", Mode: workspace.ModeAdvisory},
	}}

	p := claimPlan("runtime-drift", func(p *policy.RulePlan) { p.Severity = policy.SeverityError })

	staging := evaluateExternal(t, fake(violationIn("staging")), cfg, p)
	extWant(t, "exit code of an advisory environment", conformance.ComputeExitCode(staging.RuleResults, staging.Findings, diag.SeverityError), diag.OK)

	production := evaluateExternal(t, fake(violationIn("production"), violationIn("staging")), cfg, p)
	extWant(t, "exit code of a mandatory environment", conformance.ComputeExitCode(production.RuleResults, production.Findings, diag.SeverityWarning), diag.Violation)

	// The rule shows the strongest mode of its outcomes, and every finding shows the mode of its own environment.
	extWant(t, "rule enforcement", rule(t, &production, "ASR-T-001").Enforcement, policy.EnforcementMandatory)

	modes := map[string]policy.Enforcement{"production": policy.EnforcementMandatory, "staging": advisoryMode}

	for i := range production.Findings {
		f := &production.Findings[i]
		extWant(t, "mode of the finding in "+f.Environment, f.Enforcement, modes[f.Environment])
	}
}

func TestExternalFamiliesAreTypedForEnforcement(t *testing.T) {
	t.Parallel()

	var cfg conformance.EnforcementConfig

	rolled := conformance.EnforcementConfig{Rollback: &conformance.RunRollback{}}

	for _, p := range []policy.RulePlan{accessPlan(), checkPlan(), claimPlan("exact-lineage", nil)} {
		def := cfg.Resolve(&p, "production")
		extWant(t, string(p.Predicate.Family)+": default mode", def.Mode, policy.EnforcementAdvisory)
		extWant(t, string(p.Predicate.Family)+": default source", def.Source, conformance.SourceDefault)

		extWant(t, string(p.Predicate.Family)+": mode under a rollback", rolled.Resolve(&p, "production").Mode, policy.EnforcementShadow)
	}
}
