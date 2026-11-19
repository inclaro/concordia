package conformance

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

// Quality scenarios (design D11, D21, D26) are evaluated once per run, after the rules, the decision hook, and the
// review evaluation, because the three modes read what those produced: the structural mode reads rule verdicts, the
// reviewed mode reads the currency of the review evaluation, and the ADR links read the decision summary.
//
// A scenario becomes one rule result, like a review rule does (mergeReview): the rule `quality:<scenario id>` has a
// single obligation (the scenario in its environment), the enforcement of the scenario, and a finding of the kind
// `quality` when the obligation is a violation or unknown. So the dispositions, `check.failOn`, `check.unknown`, and
// the exit code treat a scenario like every other obligation, and a waiver changes the disposition of its finding and
// never the verdict. An area that `assurance.quality.requiredAreas` lists and that no scenario covers becomes the
// rule `quality:area:<area>`, a violation that an empty scenario set cannot remove. These rules report in the
// `qualityEvaluation` area and not in `conformance` (AreaOfRule).

// The ids of the rules that quality scenarios make. A rule id of a policy never contains a colon, so they cannot
// clash.
const (
	qualityRulePrefix = "quality:"
	qualityAreaPrefix = qualityRulePrefix + "area:"
)

// Fingerprint kind of the findings of scenarios (design D25).
const kindQuality = "quality"

// The owners of a link and the kind of an evaluated-by reference that name a scenario, a finding, or an assessment.
const (
	ownerScenario      = "scenario"
	ownerFinding       = "finding"
	evidenceAssessment = "assessment"
)

// isQualityRule says whether the rule was made for a quality scenario or a required area.
func isQualityRule(ruleID string) bool { return strings.HasPrefix(ruleID, qualityRulePrefix) }

// qualityOutcome is the result of one scenario with what the report and the rule need besides the verdict.
type qualityOutcome struct {
	scenario *quality.Scenario
	result   quality.Result
	// evidence are the evaluated-by references: the rules of a structural scenario, the measurement files of a
	// measured one, and the assessments of a reviewed one.
	evidence []QualityEvidence
}

// evaluateQuality evaluates the quality scenarios and adds their rule results and findings to the output of the
// evaluation. It fills the quality sections of the assurance report. A run without scenarios is left as it is.
//
// An unknown `addressedBy` decision with a registered decision summary is invalid input (CO-QUA-002, exit code 2):
// only the registered decisions have an applicability state.
func (c *checker) evaluateQuality(inp *inputs, evaluated *EvaluateOutput, input *EvaluateInput) error {
	state := c.assurance
	if !hasQualityScenarios(state) {
		return nil
	}

	set := state.ev.Scenarios

	links, errs := quality.ResolveLinks(set, c.decisionStates(), canonicalDecisionID)
	if len(errs) > 0 {
		return &evidence.ConfigError{Errors: errs}
	}

	outcomes, results := c.scenarioOutcomes(set, ruleResultsOf(evaluated.RuleResults))
	coverage := quality.Coverage(set, results, requiredQualityAreas(state))
	nodesByID := nodeDeclarationsByID(inp.model)

	made := &qualityRules{}
	fingerprints := made.addScenarios(outcomes, links, input, nodesByID)
	areaFindings := made.addRequiredAreas(coverage, input, nodesByID)

	completeFindings(made.findings, made.rules, input, evaluated.Reconciliation)
	made.mergeInto(evaluated)

	state.report.Quality = qualityEntries(outcomes, links, fingerprints)
	state.report.QualityAreas = qualityAreaEntries(coverage, links, areaFindings)

	return nil
}

// hasQualityScenarios says whether the assurance state of the run holds scenarios to evaluate and a report to fill.
func hasQualityScenarios(state *assuranceRun) bool {
	if state == nil || state.ev == nil {
		return false
	}

	return state.ev.Scenarios != nil && state.report != nil
}

// scenarioOutcomes evaluates every scenario of the set, and keeps the result of each by scenario id.
func (c *checker) scenarioOutcomes(set *quality.Set, rules map[string]quality.RuleResult) (outcomes []qualityOutcome, results map[string]quality.Result) {
	outcomes = make([]qualityOutcome, 0, len(set.Scenarios))
	results = make(map[string]quality.Result, len(set.Scenarios))

	for _, scenario := range set.Scenarios {
		outcome := c.scenarioOutcome(scenario, rules)
		outcomes = append(outcomes, outcome)
		results[scenario.ID] = outcome.result
	}

	return outcomes, results
}

// requiredQualityAreas lists the areas that `assurance.quality.requiredAreas` requires.
func requiredQualityAreas(state *assuranceRun) []string {
	if state.cfg == nil || state.cfg.Quality == nil {
		return nil
	}

	return state.cfg.Quality.RequiredAreas
}

func nodeDeclarationsByID(model *intent.ArchitectureModel) map[string]*intent.NodeDeclaration {
	nodesByID := make(map[string]*intent.NodeDeclaration, len(model.Nodes))
	for i := range model.Nodes {
		nodesByID[model.Nodes[i].ID] = &model.Nodes[i]
	}

	return nodesByID
}

// qualityRules are the rule results and the findings that scenarios and required areas make.
type qualityRules struct {
	rules    []RuleResult
	findings []Finding
}

// add keeps one rule with its findings and gives the fingerprints of the findings.
func (q *qualityRules) add(rule *RuleResult, found []Finding) []string {
	q.rules = append(q.rules, *rule)
	q.findings = append(q.findings, found...)

	fingerprints := make([]string, 0, len(found))
	for j := range found {
		fingerprints = append(fingerprints, found[j].Fingerprint)
	}

	return fingerprints
}

// addScenarios makes the rule of every scenario and gives the fingerprints of its findings by scenario id.
func (q *qualityRules) addScenarios(
	outcomes []qualityOutcome, links []quality.Link, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration,
) map[string][]string {
	fingerprints := map[string][]string{}

	for i := range outcomes {
		id := outcomes[i].scenario.ID
		rule, found := scenarioRule(&outcomes[i], links, input, nodesByID)
		fingerprints[id] = append(fingerprints[id], q.add(&rule, found)...)
	}

	return fingerprints
}

// addRequiredAreas makes the rule of every required area that no scenario covers and gives the fingerprints of its
// findings by area.
func (q *qualityRules) addRequiredAreas(
	coverage []quality.AreaCoverage, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration,
) map[string][]string {
	areaFindings := map[string][]string{}

	for i := range coverage {
		if coverage[i].Verdict != quality.AreaViolation || len(coverage[i].Scenarios) > 0 {
			continue
		}

		rule, found := requiredAreaRule(&coverage[i], input, nodesByID)
		areaFindings[coverage[i].Area] = append(areaFindings[coverage[i].Area], q.add(&rule, found)...)
	}

	return areaFindings
}

// mergeInto adds the rules and the findings to the output of the evaluation, in their fixed order.
func (q *qualityRules) mergeInto(evaluated *EvaluateOutput) {
	evaluated.RuleResults = append(evaluated.RuleResults, q.rules...)
	slices.SortStableFunc(evaluated.RuleResults, func(x, y RuleResult) int { return compareStrings(x.RuleID, y.RuleID) })

	evaluated.Findings = append(evaluated.Findings, q.findings...)
	slices.SortStableFunc(evaluated.Findings, func(x, y Finding) int {
		return cmp.Or(compareStrings(x.RuleID, y.RuleID), compareStrings(x.Fingerprint, y.Fingerprint))
	})
}

// scenarioOutcome evaluates one scenario in its own mode.
func (c *checker) scenarioOutcome(scenario *quality.Scenario, rules map[string]quality.RuleResult) qualityOutcome {
	out := qualityOutcome{scenario: scenario}

	switch scenario.Mode {
	case quality.ModeStructural:
		out.result = quality.EvaluateStructural(scenario, rules)
		out.evidence = qualityEvidenceOf("rule", scenario.Criterion.Rules)
	case quality.ModeMeasured:
		measured := quality.EvaluateMeasured(scenario, &quality.MeasuredInput{
			Context: c.assurance.ctx, Producers: c.assurance.cfg.Producers, Measurements: c.assurance.ev.Measurements,
		})
		out.result = measured.Result
		c.diags = append(c.diags, measured.Diagnostics...)
		out.evidence = qualityEvidenceOf("measurement", measured.Result.Evidence)
	case quality.ModeReviewed:
		out.result = c.reviewedResult(scenario)
		out.evidence = qualityEvidenceOf(evidenceAssessment, out.result.Evidence)
	}

	closeUnknownReason(&out.result)

	return out
}

// reviewedResult evaluates a reviewed scenario against the currency of the review evaluation.
func (c *checker) reviewedResult(scenario *quality.Scenario) quality.Result {
	eval := c.currencyEvaluation()

	var currency []assessment.SubjectCurrency
	if eval != nil {
		currency = eval.CurrencyFor(scenario.ID)
	}

	return quality.EvaluateReviewed(scenario, eval != nil, currency)
}

// qualityEvidenceOf lists the evidence of one kind for the references.
func qualityEvidenceOf(kind string, refs []string) []QualityEvidence {
	out := make([]QualityEvidence, 0, len(refs))

	for _, ref := range refs {
		out = append(out, QualityEvidence{Kind: kind, Ref: ref})
	}

	return out
}

// closeUnknownReason keeps an unknown result inside the closed set of reasons. The reasons of the quality package are
// literals of the closed set; anything else is a defect of the caller that must not leave an unknown result without
// its one reason.
func closeUnknownReason(result *quality.Result) {
	if result.Verdict != quality.VerdictUnknown || UnknownReason(result.Reason).Valid() {
		return
	}

	result.Details = append(result.Details, fmt.Sprintf("the reason %q is not in the closed set", result.Reason))
	result.Reason = quality.ReasonNoEvidenceSupplied
}

// ruleResultsOf maps the verdicts of the rules of the run onto the small type of the quality package: the verdict, the
// closed unknown reason, and the first thing the rule says. A rule of the code families that is `unknown` is unknown
// because its coverage is, and says so.
func ruleResultsOf(results []RuleResult) map[string]quality.RuleResult {
	out := make(map[string]quality.RuleResult, len(results))

	for i := range results {
		rule := &results[i]
		if isQualityRule(rule.RuleID) {
			continue
		}

		entry := quality.RuleResult{Verdict: quality.Verdict(rule.Verdict)}

		if rule.Verdict == VerdictUnknown {
			entry.Reason = ruleUnknownReason(rule)
		}

		if len(rule.Reasons) > 0 {
			entry.Detail = rule.Reasons[0].Message
		}

		out[rule.RuleID] = entry
	}

	return out
}

// ruleUnknownReason gives the closed unknown reason of an unknown rule: the one its obligations or reasons carry, else
// the coverage of its evidence. "" says that the rule gave none.
func ruleUnknownReason(rule *RuleResult) string {
	if reason := obligationUnknownReason(rule); reason != "" {
		return reason
	}

	for _, reason := range rule.Reasons {
		if UnknownReason(reason.Code).Valid() {
			return reason.Code
		}
	}

	if rule.Coverage != nil {
		return string(ReasonCoverageIncomplete)
	}

	return ""
}

// obligationUnknownReason gives the reason of the first unknown obligation that carries a closed one, else "".
func obligationUnknownReason(rule *RuleResult) string {
	for i := range rule.Obligations {
		if rule.Obligations[i].Verdict == VerdictUnknown && rule.Obligations[i].Reason.Valid() {
			return string(rule.Obligations[i].Reason)
		}
	}

	return ""
}

// decisionStates gives the state of each registered decision of the decision summary of this run, keyed by id. nil
// means that the run does not evaluate ADR governance (no decision register).
func (c *checker) decisionStates() map[string]quality.DecisionState {
	summary := c.decisionSummary()
	if summary == nil {
		return nil
	}

	states := make(map[string]quality.DecisionState, len(summary.Decisions))

	for i := range summary.Decisions {
		entry := &summary.Decisions[i]
		states[entry.ID] = decisionStateOf(entry.Applicability.State)
	}

	return states
}

// decisionSummary is the decision summary of this run, nil when the run has none.
func (c *checker) decisionSummary() *DecisionSummary {
	if c.decisions == nil || c.decisions.evaluation == nil {
		return nil
	}

	return c.decisions.evaluation.Summary
}

// decisionStateOf maps the applicability state of a decision onto the states that the links read. A state that is
// not one of the five is not evidence of an active decision.
func decisionStateOf(state string) quality.DecisionState {
	switch quality.DecisionState(state) {
	case quality.DecisionActive, quality.DecisionInactive, quality.DecisionSuperseded, quality.DecisionAmbiguous:
		return quality.DecisionState(state)
	case quality.DecisionUnknown:
	}

	return quality.DecisionUnknown
}

// canonicalIDPattern splits a decision id into a prefix and a number.
var canonicalIDPattern = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)-0*(\d+)$`)

// canonicalDecisionID is the canonical form of a decision id: the prefix in upper case and the number without leading
// zeros, so `ADR-0007` and `adr-7` are `ADR-7`. It is the function of internal/adr (`adr.CanonicalID`), which this
// package cannot import because internal/adr imports it; a test compares the two.
func canonicalDecisionID(id string) string {
	id = strings.TrimSpace(id)

	m := canonicalIDPattern.FindStringSubmatch(id)
	if m == nil {
		return strings.ToUpper(id)
	}

	n, err := strconv.ParseUint(m[2], 10, 64)
	if err != nil {
		return strings.ToUpper(m[1]) + "-" + m[2]
	}

	return strings.ToUpper(m[1]) + "-" + strconv.FormatUint(n, 10)
}

// scenarioPlan is the plan of the rule of a scenario (design D11): `mandatory` is severity `error` and `onUnknown:
// fail`, `advisory` is severity `warning` and `onUnknown: warn`. The enforcement of the scenario is the mode that the
// rule declares, so `check.enforcement` can still set another one, as it can for a typed rule. The claim is the claim
// of the quality contract: the rule is Concordia's own, so no policy file declares it.
func scenarioPlan(scenario *quality.Scenario) policy.RulePlan {
	severity, onUnknown := policy.SeverityWarning, policy.UnknownWarn
	if scenario.Enforcement == quality.EnforcementMandatory {
		severity, onUnknown = policy.SeverityError, policy.UnknownFail
	}

	subjects := slices.Clone(scenario.Scope)
	slices.SortFunc(subjects, canonjson.CompareUTF16)

	return policy.RulePlan{
		RuleID: qualityRulePrefix + scenario.ID, Name: "Quality scenario " + scenario.ID, Severity: severity, Owner: scenario.Owner,
		Pack:        qualityPack,
		Rationale:   scenarioRationale(scenario),
		Predicate:   claimPredicate(scenario.Environment),
		Enforcement: policy.Enforcement(scenario.Enforcement), Subjects: subjects, Targets: []string{},
		EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown},
	}
}

// qualityPack names the source of the rules that scenarios make in the `policy` field of a rule result.
var qualityPack = policy.PackRef{ID: "quality-scenarios", Version: "1"}

// claimPredicate is the predicate of the rules that scenarios make.
func claimPredicate(environment string) policy.Predicate {
	return policy.Predicate{
		Family:         policy.FamilyAssuranceClaim,
		AssuranceClaim: &policy.ClaimRequirement{ID: string(claims.QualityScenarioVerdict), Environment: environment},
	}
}

// scenarioRule makes the rule result and the finding of one scenario.
func scenarioRule(
	out *qualityOutcome, links []quality.Link, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration,
) (RuleResult, []Finding) {
	scenario, res := out.scenario, &out.result
	plan := scenarioPlan(scenario)

	outcome := ExternalOutcome{
		Environment: scenario.Environment, Verdict: RuleVerdict(res.Verdict), Reason: UnknownReason(res.Reason), Kind: kindQuality,
		Detail:   strings.Join(res.Details, "; "),
		Location: &Location{Path: scenario.Location.File, Line: scenario.Location.Line},
		Expected: fmt.Sprintf("scenario %s is satisfied in its %s mode: %s", scenario.ID, scenario.Mode, scenario.ExpectedResponse),
		Related:  relatedOfScenario(out, links),
	}

	outcome.Message = fmt.Sprintf("quality scenario %s (%s, %s) is %s", scenario.ID, scenario.Mode, scenario.Area, res.Verdict)
	if outcome.Detail != "" {
		outcome.Message += ": " + outcome.Detail
	}

	rule, found := outcomesResult(&plan, plan.Severity, []ExternalOutcome{outcome}, input, nodesByID)

	for i := range found {
		found[i].Area = AreaQualityEvaluation
		owner := scenario.Owner
		found[i].Owner = &owner
	}

	return rule, found
}

// relatedOfScenario lists the assessments that decided a reviewed scenario and the decisions that its links name
// but that are not active, so that the finding shows what to look at.
func relatedOfScenario(out *qualityOutcome, links []quality.Link) []RelatedRef {
	var related []RelatedRef

	for _, ev := range out.evidence {
		if ev.Kind == evidenceAssessment {
			related = append(related, RelatedRef{Kind: evidenceAssessment, ID: ev.Ref})
		}
	}

	for i := range links {
		if isInactiveDecisionLinkOf(&links[i], out.scenario.ID) {
			related = append(related, RelatedRef{Kind: "decision", ID: links[i].Canonical})
		}
	}

	return related
}

// isInactiveDecisionLinkOf says whether the link belongs to the scenario and names a decision that is not active.
func isInactiveDecisionLinkOf(link *quality.Link, scenarioID string) bool {
	if link.OwnerKind != ownerScenario || link.Owner != scenarioID {
		return false
	}

	return link.State != "" && !link.CountsAsActive()
}

// requiredAreaRule makes the violation of a required area that no scenario covers. It is mandatory: the workspace
// requires the area, and nothing that a scenario could say removes the requirement.
func requiredAreaRule(row *quality.AreaCoverage, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) (RuleResult, []Finding) {
	plan := policy.RulePlan{
		RuleID: qualityAreaPrefix + row.Area, Name: "Quality area " + row.Area, Severity: policy.SeverityError,
		Pack: qualityPack, Predicate: claimPredicate(""), Enforcement: policy.EnforcementMandatory,
		Subjects: []string{}, Targets: []string{}, EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: policy.UnknownFail},
		Rationale: "assurance.quality.requiredAreas lists the area " + row.Area,
	}

	outcome := ExternalOutcome{
		Verdict: VerdictViolation, Kind: kindQuality, Detail: row.Detail,
		Expected: "a quality scenario covers the area " + row.Area,
		Message:  "the quality area " + row.Area + " is required and no scenario covers it",
	}

	rule, found := outcomesResult(&plan, plan.Severity, []ExternalOutcome{outcome}, input, nodesByID)

	for i := range found {
		found[i].Area = AreaQualityEvaluation
	}

	return rule, found
}

// scenarioRationale is the goal of a scenario as one sentence: the stimulus and the response that the architecture
// must give, so that a failing scenario says what is at stake.
func scenarioRationale(scenario *quality.Scenario) string {
	if scenario.Stimulus == "" {
		return scenario.ExpectedResponse
	}

	return "When " + scenario.Stimulus + ", " + scenario.ExpectedResponse + "."
}
