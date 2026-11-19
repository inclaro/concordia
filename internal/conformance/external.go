package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

// The rules of three families are evaluated outside the predicate evaluators: `effective-access`, `check-result`, and
// `assurance-claim`. Their results come from the assurance adapters and evaluators (rendered documents, admitted
// statements and check results), which the assurance evidence loader reads once per run. This file is the one seam
// between those evaluators and the rule results: the pipeline fills an ExternalEvaluation, Evaluate turns its outcomes
// into the rule verdict (the worst outcome) and one finding for each outcome that is not a pass, and controls read the
// check verdicts from the same value. Without an evaluation (a workspace with no `assurance` key) every rule of the
// three families is `unknown` with the reason that says which input is missing, never `pass`.

// ExternalOutcome is the result of one subject of an external rule: a pair of workloads for effective access, a
// check (and optionally a CALM element) for a check result, an environment or a container occurrence for a claim.
type ExternalOutcome struct {
	// Location is the file that the outcome is about (a rendered source, a statement). nil when none.
	Location *Location
	// Subject is the CALM ID or the claim subject (an environment, a container occurrence) that the result is about.
	// It is the source of the finding. Empty when the rule has no subject.
	Subject string
	// Target is the CALM ID that the subject reaches or depends on (effective access), or empty.
	Target string
	// Environment is the declared environment of the outcome. Empty when it has none. The enforcement mode of the
	// outcome is the mode of the rule in this environment.
	Environment string
	// Occurrence is the discriminator of an occurrence inside the subject (for example a container name).
	Occurrence string
	// Verdict is pass, violation, unknown, not-applicable, or error.
	Verdict RuleVerdict
	// Reason is the closed reason of an `unknown` outcome. An unknown outcome with a reason outside the closed set is
	// an `error`, because every unknown result has exactly one reason.
	Reason UnknownReason
	// Detail says why: the admissibility details of the evidence, or what is missing. For a violation it is the
	// observed evidence, for example both digests of a drift.
	Detail string
	// Message is the finding text of a violation or unknown. Empty: Concordia writes one from the rule and the detail.
	Message string
	// Expected is what the rule requires for this subject. Empty: Concordia writes it from the rule.
	Expected string
	// Kind is the fingerprint kind of the finding (D25): `lineage`, `drift`, `isolation`, `verification`, or
	// `quality`. Empty: the kind of the family of the rule.
	Kind string
	// Related lists the decisions, views, and assessments that the finding relates to.
	Related []RelatedRef
}

// ExternalRuleFunc evaluates one rule of an external family. The bool is false when the evaluator has nothing to say
// about the rule (no evaluator is enabled for it, or its evidence was not loaded): the rule is then `unknown` with the
// default reason of its family. A true result with no outcome means that the rule selects nothing (`not-applicable`).
// It is called once per rule and run, with the plan that Evaluate got.
type ExternalRuleFunc func(plan *policy.RulePlan) ([]ExternalOutcome, bool)

// ExternalCheck is the verdict of one check that a control lists in `check-ids`, taken from the admitted check
// results of the run (the worst verdict over the environments that the results cover).
type ExternalCheck struct {
	// Verdict is pass, violation, unknown, not-applicable, or error.
	Verdict RuleVerdict
	// Reason is the closed reason of an `unknown` verdict.
	Reason UnknownReason
	// Detail says why, for example the admissibility details of the results that were left out.
	Detail string
}

// ExternalEvaluation is what the assurance evidence gives the check pipeline. The nil pointer is valid and means
// that no assurance evidence exists.
type ExternalEvaluation struct {
	// Claims is the registry of the contracts that the workspace loaded. nil: no contract is loaded (the workspace has
	// no `assurance` key), so every claim is not enabled.
	Claims *claims.Registry
	// Rule evaluates the rules of the three external families. nil: no rule has an evaluator.
	Rule ExternalRuleFunc
	// Checks maps a check identifier to its verdict. A check with no entry has no admissible result.
	Checks map[string]ExternalCheck
	// Locate gives the repository location of the rendered source of an environment, nil when the environment has
	// none. An outcome that names no file of its own gets it, so that no finding about rendered evidence is without a
	// location (spec rendered-infrastructure-evidence, "Locations point to repository sources").
	Locate func(environment string) *Location
}

// noContracts is the registry of a run that loaded no contract.
var noContracts = sync.OnceValue(func() *claims.Registry {
	registry, err := claims.New(nil)
	if err != nil {
		// An empty registry is always valid: an error here is a defect that the tests catch.
		panic(err.Error())
	}

	return registry
})

// externalFamilies are the families that ExternalEvaluation gives the results of.
var externalFamilies = []policy.PredicateFamily{policy.FamilyEffectiveAccess, policy.FamilyCheckResult, policy.FamilyAssuranceClaim}

// isExternal says whether the results of the rule come from ExternalEvaluation.
func isExternal(plan *policy.RulePlan) bool {
	return slices.Contains(externalFamilies, plan.Predicate.Family)
}

func (e *ExternalEvaluation) registry() *claims.Registry {
	if e == nil || e.Claims == nil {
		return noContracts()
	}

	return e.Claims
}

// Fingerprint kinds of the external families (design D25).
const (
	kindIsolation    = "isolation"
	kindVerification = "verification"
	kindLineage      = "lineage"
	kindDrift        = "drift"
)

// kindOf gives the fingerprint kind of an outcome: its own, else the kind of the family of the rule (the claim id for
// a claim that has no kind of its own).
func kindOf(plan *policy.RulePlan, outcome *ExternalOutcome) string {
	if outcome.Kind != "" {
		return outcome.Kind
	}

	switch plan.Predicate.Family {
	case policy.FamilyEffectiveAccess:
		return kindIsolation
	case policy.FamilyCheckResult:
		return kindVerification
	case policy.FamilyAssuranceClaim:
		switch id := claims.ID(plan.Predicate.AssuranceClaim.ID); id {
		case claims.ExactLineage:
			return kindLineage
		case claims.RuntimeDrift:
			return kindDrift
		case claims.CompleteResourceSet, claims.ContainerOccurrenceVersions, claims.ResolvedSelections, claims.PlannedResources,
			claims.DeployedDigest, claims.DeploymentSucceeded, claims.NetworkIsolation, claims.RBACGrants,
			claims.CheckOutcome, claims.QualityScenarioVerdict:
			return string(id)
		default:
			return string(id)
		}
	case policy.FamilyBoundary, policy.FamilyLayer, policy.FamilyDeclaredOnly, policy.FamilyObservedRequired,
		policy.FamilyAcyclic, policy.FamilyDenyPath, policy.FamilyAllowOnly, policy.FamilyIndependent,
		policy.FamilyPublicAPIOnly, policy.FamilyOwnership, policy.FamilyBudget, policy.FamilyRequireEdge,
		policy.FamilyRequireEntity, policy.FamilyTyped, policy.FamilyView, policy.FamilyDecisionGovernance,
		policy.FamilyAssessmentCurrency:
	}

	return string(plan.Predicate.Family)
}

// missingEvidenceReason gives the reason of a claim that has a loaded contract and no evidence: the claims that read
// rendered documents say that rendered evidence is missing, the others that no evidence was supplied.
func missingEvidenceReason(id string) UnknownReason {
	switch claims.ID(id) {
	case claims.CompleteResourceSet, claims.ContainerOccurrenceVersions, claims.ResolvedSelections:
		return ReasonRenderedEvidenceMissing
	case claims.PlannedResources, claims.ExactLineage, claims.DeployedDigest, claims.DeploymentSucceeded, claims.RuntimeDrift,
		claims.NetworkIsolation, claims.RBACGrants, claims.CheckOutcome, claims.QualityScenarioVerdict:
		return ReasonNoEvidenceSupplied
	default:
		return ReasonNoEvidenceSupplied
	}
}

// outcomesOf gives the outcomes of an external rule and the severity of its findings. A claim that no loaded contract
// enables is `unknown` with `claim-not-enabled`, whatever the evaluator could say; a claim of an experimental contract
// caps the severity at `warning` (design D7). A rule that has no evaluator gets the default outcomes of its family.
func (e *ExternalEvaluation) outcomesOf(plan *policy.RulePlan) ([]ExternalOutcome, policy.RuleSeverity) {
	severity := plan.Severity

	if claim := plan.Predicate.AssuranceClaim; plan.Predicate.Family == policy.FamilyAssuranceClaim && claim != nil {
		found := e.registry().Lookup(claim.ID)
		if !found.Enabled {
			return []ExternalOutcome{{
				Environment: claim.Environment, Verdict: VerdictUnknown, Reason: ReasonClaimNotEnabled, Detail: found.Detail,
			}}, severity
		}

		severity = found.Severity(severity)
	}

	if e != nil && e.Rule != nil {
		if outcomes, evaluated := e.Rule(plan); evaluated {
			return outcomes, severity
		}
	}

	return defaultOutcomes(plan), severity
}

// defaultOutcomes are the `unknown` outcomes of a rule that has no evaluator or no evidence. Each family names the
// input that is missing: an effective-access rule needs an evaluator, a check-result rule needs a result, and a claim
// needs the evidence of its contract.
func defaultOutcomes(plan *policy.RulePlan) []ExternalOutcome {
	switch plan.Predicate.Family {
	case policy.FamilyEffectiveAccess:
		access := plan.Predicate.EffectiveAccess

		var out []ExternalOutcome

		for _, subject := range plan.Subjects {
			for _, target := range plan.Targets {
				out = append(out, ExternalOutcome{
					Subject: subject, Target: target, Verdict: VerdictUnknown, Reason: ReasonNoEvaluatorAvailable,
					Detail: fmt.Sprintf("no evaluator is enabled for effective %s access", access.Dimension),
				})
			}
		}

		return out
	case policy.FamilyCheckResult:
		check := plan.Predicate.CheckResult
		detail := "no admissible result for check " + check.Check

		if len(plan.Subjects) == 0 {
			return []ExternalOutcome{{Environment: check.Environment, Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied, Detail: detail}}
		}

		out := make([]ExternalOutcome, 0, len(plan.Subjects))
		for _, subject := range plan.Subjects {
			out = append(out, ExternalOutcome{
				Subject: subject, Environment: check.Environment, Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied, Detail: detail,
			})
		}

		return out
	case policy.FamilyAssuranceClaim:
		claim := plan.Predicate.AssuranceClaim

		return []ExternalOutcome{{
			Environment: claim.Environment, Verdict: VerdictUnknown, Reason: missingEvidenceReason(claim.ID),
			Detail: "no evidence was supplied for claim " + claim.ID,
		}}
	case policy.FamilyBoundary, policy.FamilyLayer, policy.FamilyDeclaredOnly, policy.FamilyObservedRequired,
		policy.FamilyAcyclic, policy.FamilyDenyPath, policy.FamilyAllowOnly, policy.FamilyIndependent,
		policy.FamilyPublicAPIOnly, policy.FamilyOwnership, policy.FamilyBudget, policy.FamilyRequireEdge,
		policy.FamilyRequireEntity, policy.FamilyTyped, policy.FamilyView, policy.FamilyDecisionGovernance,
		policy.FamilyAssessmentCurrency:
	}

	return nil
}

// normalize makes an outcome valid: a verdict outside the set, or an `unknown` with no closed reason, is an `error`
// that says so. Every unknown result has exactly one reason (typed-obligations spec).
func (o *ExternalOutcome) normalize() {
	switch o.Verdict {
	case VerdictPass, VerdictViolation, VerdictNotApplicable, VerdictError:
		return
	case VerdictUnknown:
		if o.Reason.Valid() {
			return
		}

		o.Detail = fmt.Sprintf("an evaluator gave the result unknown with the reason %q, which is not in the closed set: %s", o.Reason, o.Detail)
	default:
		o.Detail = fmt.Sprintf("an evaluator gave the verdict %q, which is not a rule verdict: %s", o.Verdict, o.Detail)
	}

	o.Verdict, o.Reason = VerdictError, ""
}

// notApplicableCode gives the reason code when an external rule selects nothing to check. Only an effective-access
// rule has selectors of its own; a claim and a check name their subjects themselves.
func notApplicableCode(plan *policy.RulePlan) (string, bool) {
	if plan.Predicate.Family != policy.FamilyEffectiveAccess {
		return "", false
	}

	switch {
	case len(plan.Subjects) == 0:
		return diag.CON022, true
	case len(plan.Targets) == 0:
		return diag.CON025, true
	}

	return "", false
}

// obligationIDOf is the stable identity of the obligation of one outcome: the rule, the subject, the target, the
// environment, and the occurrence.
func obligationIDOf(ruleID string, o *ExternalOutcome) string {
	const length = 16

	return digest.SHA256HexString(strings.Join([]string{ruleID, o.Subject, o.Target, o.Environment, o.Occurrence}, "\x1f"))[:length]
}

// ruleResult gives the result of an external rule and its findings. The verdict of the rule is the worst verdict of
// its outcomes (error, violation, unknown, pass); it is `not-applicable` when the rule selects nothing or has no
// outcome. Each outcome is an obligation result with the enforcement mode of the rule in its environment, so an
// advisory violation in one environment never hides a mandatory unknown in another. A finding is made for each
// outcome that is a violation or unknown (the unknown findings of a rule that ignores unknown results are left out).
func (e *ExternalEvaluation) ruleResult(plan *policy.RulePlan, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) (RuleResult, []Finding) {
	if code, skip := notApplicableCode(plan); skip {
		result := externalBase(plan, input)
		result.Verdict = VerdictNotApplicable
		result.Reasons = []Reason{{Code: code, Message: diag.New(code, nil).Message}}

		return result, nil
	}

	outcomes, severity := e.outcomesOf(plan)

	// A finding about rendered documents that names no file points at the declared source of its environment. Other
	// findings (check results, plans, quality) have no rendered source, so they keep their own location or none.
	if e != nil && e.Locate != nil && ReadsRenderedEvidence(plan) {
		for i := range outcomes {
			if outcomes[i].Location == nil && outcomes[i].Environment != "" {
				outcomes[i].Location = e.Locate(outcomes[i].Environment)
			}
		}
	}

	return outcomesResult(plan, severity, outcomes, input, nodesByID)
}

// ReadsRenderedEvidence says whether the findings of a rule are about rendered documents: an effective-access rule,
// or a claim about the rendered resources or their container occurrences. Only those findings may be located at the
// declared rendering source of their environment.
func ReadsRenderedEvidence(plan *policy.RulePlan) bool {
	switch plan.Predicate.Family { //nolint:exhaustive // only these two families read rendered documents.
	case policy.FamilyEffectiveAccess:
		return true
	case policy.FamilyAssuranceClaim:
		if plan.Predicate.AssuranceClaim == nil {
			return false
		}

		switch claims.ID(plan.Predicate.AssuranceClaim.ID) { //nolint:exhaustive // the claims about rendered documents.
		case claims.CompleteResourceSet, claims.ContainerOccurrenceVersions, claims.ResolvedSelections,
			claims.ExactLineage, claims.DeployedDigest, claims.DeploymentSucceeded, claims.RuntimeDrift:
			return true
		}
	}

	return false
}

// externalBase is the rule result of an external rule before its outcomes are known.
func externalBase(plan *policy.RulePlan, input *EvaluateInput) RuleResult {
	return RuleResult{
		RuleID: plan.RuleID, Name: plan.Name, Severity: plan.Severity, Policy: plan.Pack, Family: plan.Predicate.Family,
		Subjects: nonNilStrings(plan.Subjects), Targets: nonNilStrings(plan.Targets), Plan: *plan, Reasons: []Reason{},
		Enforcement: input.Enforcement.Resolve(plan, "").Mode,
	}
}

// outcomesResult makes the result of a rule and its findings from the outcomes of its subjects, at the severity that
// the caller decided. The quality scenarios use it too: their obligations are outcomes of a rule that Concordia
// writes itself (assurance_quality.go).
func outcomesResult(
	plan *policy.RulePlan, severity policy.RuleSeverity, outcomes []ExternalOutcome, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration,
) (RuleResult, []Finding) {
	cfg := &input.Enforcement
	result := externalBase(plan, input)
	result.Severity = severity

	if len(outcomes) == 0 {
		result.Verdict = VerdictNotApplicable
		result.Reasons = []Reason{{Code: diag.CON022, Message: diag.New(diag.CON022, nil).Message}}

		return result, nil
	}

	var (
		verdicts []RuleVerdict
		findings []Finding
	)

	counted := policy.EnforcementShadow
	obligations := make([]ObligationResult, 0, len(outcomes))

	for i := range outcomes {
		outcome := outcomes[i]
		outcome.normalize()

		mode := cfg.Resolve(plan, outcome.Environment).Mode

		obligation := ObligationResult{
			Verdict: outcome.Verdict, Reason: outcome.Reason, Detail: outcome.Detail, Occurrences: []OccurrenceResult{},
			Enforcement: mode,
			Obligation: Obligation{
				ID: obligationIDOf(plan.RuleID, &outcome), RuleID: plan.RuleID, Subject: outcome.Subject,
				Environment: outcome.Environment, Bindings: []string{},
			},
		}

		counted = Stronger(counted, mode)

		verdicts = append(verdicts, outcome.Verdict)
		result.Reasons = appendReason(result.Reasons, &obligation)
		obligations = append(obligations, obligation)

		if found, ok := externalFinding(plan, severity, &outcome, &obligation, nodesByID); ok {
			findings = append(findings, found)
		}
	}

	slices.SortStableFunc(obligations, func(a, b ObligationResult) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	findings = dedupeFindings(findings)
	result.Verdict = WorstVerdict(verdicts)
	result.Enforcement = counted
	result.Obligations = obligations
	result.FindingCount = len(findings)

	return result, findings
}

// externalFinding makes the finding of one outcome. A violation is a finding at the severity of the rule; an unknown is
// an `info` finding that shows the reason and the next action and never gates by itself (the verdict of the rule
// gates). Its fingerprint is the typed fingerprint over the rule, the subject and target, the environment, the
// occurrence, and the kind of the family (design D25).
func externalFinding(
	plan *policy.RulePlan, severity policy.RuleSeverity, outcome *ExternalOutcome, obligation *ObligationResult, nodesByID map[string]*intent.NodeDeclaration,
) (Finding, bool) {
	kind, edgeKind := KindObligationViolation, kindOf(plan, outcome)

	finding := Finding{
		RuleID: plan.RuleID, Severity: severity, Kind: kind, Disposition: exceptions.DispositionActive,
		ObligationID: obligation.ID, Subject: outcome.Subject, Environment: outcome.Environment, Occurrence: outcome.Occurrence,
		Result: outcome.Verdict, Enforcement: obligation.Enforcement, Location: outcome.Location,
		EvidencePath: []EvidenceStep{}, RelatedLocations: []Location{}, Related: outcome.Related,
		Owner:  ownerOf(plan, outcome.Subject, nodesByID),
		Source: Endpoint{ArchitectureID: outcome.Subject}, Target: Endpoint{ArchitectureID: outcome.Target},
	}

	switch outcome.Verdict {
	case VerdictViolation:
		finding.NextAction = nextActionFix
		finding.Observed = cmp.Or(outcome.Detail, "the evidence does not satisfy the rule")
	case VerdictUnknown:
		if plan.EvidenceRequirements.OnUnknown == policy.UnknownIgnore {
			return Finding{}, false
		}

		finding.Kind, finding.Severity, finding.Reason = KindObligationUnknown, policy.SeverityInfo, outcome.Reason
		finding.NextAction = nextActionFor(outcome.Reason)
		finding.Observed = fmt.Sprintf("unknown (%s): %s", outcome.Reason, outcome.Detail)
		edgeKind = "unknown:" + string(outcome.Reason) + ":" + edgeKind
	case VerdictPass, VerdictNotApplicable, VerdictError:
		return Finding{}, false
	}

	finding.Expected = cmp.Or(outcome.Expected, externalExpected(plan, outcome))
	finding.Message = cmp.Or(outcome.Message, fmt.Sprintf("%s (%s): %s", externalSubjectText(plan, outcome), environmentText(outcome.Environment), finding.Observed))
	finding.Fingerprint = TypedFingerprint(&TypedParts{
		Parts: Parts{
			RuleID: plan.RuleID, SourceArchitectureID: outcome.Subject, TargetArchitectureID: outcome.Target, CanonicalEdgeKind: edgeKind,
		},
		Environment: outcome.Environment, Occurrence: outcome.Occurrence,
	})

	return finding, true
}

// externalSubjectText names what the outcome is about, for the message of a finding.
func externalSubjectText(plan *policy.RulePlan, outcome *ExternalOutcome) string {
	switch plan.Predicate.Family {
	case policy.FamilyEffectiveAccess:
		access := plan.Predicate.EffectiveAccess

		return fmt.Sprintf("effective %s access from %s to %s", access.Dimension, cmp.Or(outcome.Subject, "the workloads"), cmp.Or(outcome.Target, "the targets"))
	case policy.FamilyCheckResult:
		return "check " + plan.Predicate.CheckResult.Check
	case policy.FamilyAssuranceClaim:
		text := "claim " + plan.Predicate.AssuranceClaim.ID
		if outcome.Subject != "" {
			text += " for " + outcome.Subject
		}

		if outcome.Occurrence != "" {
			text += " (" + outcome.Occurrence + ")"
		}

		return text
	case policy.FamilyBoundary, policy.FamilyLayer, policy.FamilyDeclaredOnly, policy.FamilyObservedRequired,
		policy.FamilyAcyclic, policy.FamilyDenyPath, policy.FamilyAllowOnly, policy.FamilyIndependent,
		policy.FamilyPublicAPIOnly, policy.FamilyOwnership, policy.FamilyBudget, policy.FamilyRequireEdge,
		policy.FamilyRequireEntity, policy.FamilyTyped, policy.FamilyView, policy.FamilyDecisionGovernance,
		policy.FamilyAssessmentCurrency:
	}

	return "rule " + plan.RuleID
}

// externalExpected says what the rule requires of the subject of the outcome.
func externalExpected(plan *policy.RulePlan, outcome *ExternalOutcome) string {
	switch plan.Predicate.Family {
	case policy.FamilyEffectiveAccess:
		access := plan.Predicate.EffectiveAccess
		text := fmt.Sprintf("effective %s access from %s to %s is %s", access.Dimension, cmp.Or(outcome.Subject, "the subjects"), cmp.Or(outcome.Target, "the targets"), access.Expect)

		if len(access.Ports) > 0 {
			ports := make([]string, len(access.Ports))
			for i, port := range access.Ports {
				ports[i] = strconv.Itoa(port)
			}

			text += " on ports " + strings.Join(ports, ", ")
		}

		return text
	case policy.FamilyCheckResult:
		return "check " + plan.Predicate.CheckResult.Check + " has an admissible passing result in " + environmentText(outcome.Environment)
	case policy.FamilyAssuranceClaim:
		return "claim " + plan.Predicate.AssuranceClaim.ID + " holds for " + cmp.Or(outcome.Subject, "every subject") + " in " + environmentText(outcome.Environment)
	case policy.FamilyBoundary, policy.FamilyLayer, policy.FamilyDeclaredOnly, policy.FamilyObservedRequired,
		policy.FamilyAcyclic, policy.FamilyDenyPath, policy.FamilyAllowOnly, policy.FamilyIndependent,
		policy.FamilyPublicAPIOnly, policy.FamilyOwnership, policy.FamilyBudget, policy.FamilyRequireEdge,
		policy.FamilyRequireEntity, policy.FamilyTyped, policy.FamilyView, policy.FamilyDecisionGovernance,
		policy.FamilyAssessmentCurrency:
	}

	return "rule " + plan.RuleID + " is satisfied"
}

// checkVerdict gives the verdict of a check. A check with no result is `unknown` with `no-evidence-supplied`: a link
// from a control to a check must not fulfil the control without an admissible passing result.
func (e *ExternalEvaluation) checkVerdict(id string) ExternalCheck {
	if e != nil {
		if check, ok := e.Checks[id]; ok {
			outcome := ExternalOutcome{Verdict: check.Verdict, Reason: check.Reason, Detail: check.Detail}
			outcome.normalize()

			return ExternalCheck{Verdict: outcome.Verdict, Reason: outcome.Reason, Detail: outcome.Detail}
		}
	}

	return ExternalCheck{Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied, Detail: "no admissible result"}
}
