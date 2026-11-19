package conformance_test

import (
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func rolloutGolden() string {
	return filepath.Join("..", "..", "test", "golden", "rollout", "report.json")
}

func rolloutPlan(id, family string, predicate policy.PredicateFamily) policy.RulePlan {
	plan := policy.RulePlan{RuleID: id, Family: family}
	plan.Predicate.Family = predicate
	plan.EvidenceRequirements.OnUnknown = policy.UnknownFail

	return plan
}

func rolloutObligation(id, environment string, mode policy.Enforcement, verdict conformance.RuleVerdict) conformance.ObligationResult {
	result := conformance.ObligationResult{Verdict: verdict, Enforcement: mode, Occurrences: []conformance.OccurrenceResult{}}
	result.ID, result.Subject, result.Environment = id, "billing-service", environment
	result.Relation.Kind = observation.KindUsesImage

	return result
}

// rolloutWhy is the reason of an unknown obligation result, with its detail and next action.
type rolloutWhy struct {
	reason             conformance.UnknownReason
	detail, nextAction string
}

// rolloutUnknownObligation is an unknown obligation result with one occurrence of the same reason.
func rolloutUnknownObligation(id, environment string, mode policy.Enforcement, why rolloutWhy) conformance.ObligationResult {
	result := rolloutObligation(id, environment, mode, conformance.VerdictUnknown)
	result.Reason, result.Detail = why.reason, why.detail
	result.Occurrences = []conformance.OccurrenceResult{{
		Result: conformance.VerdictUnknown, Reason: why.reason, Kind: observation.KindUsesImage,
		Detail: why.detail, NextAction: why.nextAction,
	}}

	return result
}

// rolloutObligations are the obligation results of the typed rule: a violation and a binding gap in staging, an
// uncovered predicate and a pass in production.
func rolloutObligations() []conformance.ObligationResult {
	return []conformance.ObligationResult{
		rolloutObligation("ob-stg-1", "staging", policy.EnforcementAdvisory, conformance.VerdictViolation),
		rolloutUnknownObligation("ob-stg-2", "staging", policy.EnforcementAdvisory, rolloutWhy{
			conformance.ReasonBindingGap, "no binding maps Workload billing-api to a CALM element", conformance.NextActionResolveBinding,
		}),
		rolloutUnknownObligation("ob-prd-1", "production", policy.EnforcementShadow, rolloutWhy{
			conformance.ReasonCoverageIncomplete, "the coverage of uses-image is partial in infra", "supply evidence",
		}),
		rolloutObligation("ob-prd-2", "production", policy.EnforcementShadow, conformance.VerdictPass),
	}
}

// rolloutCoverage is the partial coverage of a rule with the unbound endpoints.
func rolloutCoverage() *conformance.Coverage {
	coverage := &conformance.Coverage{
		UnboundEndpoints: 2, Required: 0.9,
		Predicates: []observation.PredicateCoverage{{
			Predicate: "imports", Scope: observation.CoverageScope{Project: "demo"}, Status: observation.CoveragePartial,
			Reasons: []string{"index result unavailable"},
		}},
	}
	coverage.Files.Ratio = 0.5

	return coverage
}

func rolloutRules() []conformance.RuleResult {
	return []conformance.RuleResult{
		{
			RuleID: "CO-IMG-001", Verdict: conformance.VerdictViolation, Family: policy.FamilyTyped, Enforcement: policy.EnforcementAdvisory,
			Plan:        rolloutPlan("CO-IMG-001", "images", policy.FamilyTyped),
			Obligations: rolloutObligations(),
		},
		{
			RuleID: "CO-DEP-001", Verdict: conformance.VerdictViolation, Family: policy.FamilyBoundary, Enforcement: policy.EnforcementAdvisory,
			Plan: rolloutPlan("CO-DEP-001", "", policy.FamilyBoundary),
		},
		{
			RuleID: "CO-COV-001", Verdict: conformance.VerdictUnknown, Family: policy.FamilyBoundary, Enforcement: policy.EnforcementAdvisory,
			Plan:     rolloutPlan("CO-COV-001", "", policy.FamilyBoundary),
			Reasons:  []conformance.Reason{{Code: "CO-CON-020", Message: "file coverage is below the minimum"}},
			Coverage: rolloutCoverage(),
		},
		{
			RuleID: "CO-BND-001", Verdict: conformance.VerdictPass, Family: policy.FamilyBoundary,
			Plan: rolloutPlan("CO-BND-001", "", policy.FamilyBoundary),
		},
	}
}

// rolloutFindingSpec is what differs between the findings of the rollout run.
type rolloutFindingSpec struct {
	rule, fingerprint, environment string
	mode                           policy.Enforcement
	disposition                    exceptions.Disposition
}

func rolloutFindings() []conformance.Finding {
	specs := []rolloutFindingSpec{
		{"CO-IMG-001", strings.Repeat("a1", 32), "staging", policy.EnforcementAdvisory, exceptions.DispositionActive},
		{"CO-DEP-001", strings.Repeat("b2", 32), "", policy.EnforcementAdvisory, exceptions.DispositionActive},
		{"CO-DEP-001", strings.Repeat("c3", 32), "", policy.EnforcementAdvisory, exceptions.DispositionWaived},
		{"CO-BND-001", strings.Repeat("d4", 32), "", policy.EnforcementMandatory, exceptions.DispositionActive},
	}

	findings := make([]conformance.Finding, 0, len(specs))
	for _, spec := range specs {
		findings = append(findings, conformance.Finding{
			RuleID: spec.rule, Fingerprint: "sha256:" + spec.fingerprint, Severity: policy.SeverityError, Kind: conformance.KindForbiddenDependency,
			Message: spec.rule + " is violated", Disposition: spec.disposition, Environment: spec.environment, Enforcement: spec.mode,
		})
	}

	findings[0].ObligationID, findings[0].Subject = "ob-stg-1", "billing-service"

	return findings
}

// rolloutRun is a run of four rules: an advisory typed family in staging, a shadow one in production, and as-built rules.
func rolloutRun() *conformance.Run {
	run := &conformance.Run{Rules: rolloutRules(), Findings: rolloutFindings()}
	run.Run.ID = "20260601T000000Z-deadbeef"
	run.Summary.FailOn = diag.SeverityError

	return run
}
