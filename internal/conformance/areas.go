package conformance

import "github.com/inclaro/concordia/internal/policy"

// Area names a report area (PRD OUT-03). `results.json` shows every area separately, so one overall
// pass cannot hide an `unknown` area.
type Area string

// The six report areas, in the order of the report. Each later change fills its own area.
const (
	AreaConformance        Area = "conformance"
	AreaDeploymentTrace    Area = "deploymentTrace"
	AreaADRGovernance      Area = "adrGovernance"
	AreaDiagramCurrency    Area = "diagramCurrency"
	AreaAssessmentCurrency Area = "assessmentCurrency"
	AreaQualityEvaluation  Area = "qualityEvaluation"
)

// AreaOrder lists the areas in report order.
func AreaOrder() []Area {
	return []Area{AreaConformance, AreaDeploymentTrace, AreaADRGovernance, AreaDiagramCurrency, AreaAssessmentCurrency, AreaQualityEvaluation}
}

// areaTitles holds the terminal headings of the areas.
var areaTitles = map[Area]string{
	AreaConformance:        "Conformance",
	AreaDeploymentTrace:    "Deployment trace",
	AreaADRGovernance:      "ADR governance",
	AreaDiagramCurrency:    "Diagram currency",
	AreaAssessmentCurrency: "Assessment currency",
	AreaQualityEvaluation:  "Quality evaluation",
}

// Title gives the heading of the area in the terminal output.
func (a Area) Title() string {
	if title, ok := areaTitles[a]; ok {
		return title
	}

	return string(a)
}

// AreaVerdict is the verdict of one area: a rule verdict, or `not-configured` when the run does not use the area.
type AreaVerdict string

// AreaNotConfigured is the verdict of an area that the workspace does not configure. It is never `pass`.
const AreaNotConfigured AreaVerdict = "not-configured"

// Configured says whether the run evaluated the area. An area with no verdict (a hand-built run) is not configured.
func (v AreaVerdict) Configured() bool { return v != "" && v != AreaNotConfigured }

// AreaResult is the result of one area: its own verdict and counts. The detail of an area stays in its own
// section of the result; the counts here agree with that section.
type AreaResult struct {
	// Counts are plain integers with fixed names. The conformance area counts rules by verdict (`pass`, `violation`,
	// `unknown`, `notApplicable`, `error`) and `findings`. Never nil; empty when the area is not configured.
	Counts  map[string]int `json:"counts"`
	Verdict AreaVerdict    `json:"verdict"`
}

// Areas holds the six report areas. Only `conformance` is configured by default; the others show
// `not-configured` until the change that owns them fills them.
type Areas struct {
	Conformance        AreaResult `json:"conformance"`
	DeploymentTrace    AreaResult `json:"deploymentTrace"`
	ADRGovernance      AreaResult `json:"adrGovernance"`
	DiagramCurrency    AreaResult `json:"diagramCurrency"`
	AssessmentCurrency AreaResult `json:"assessmentCurrency"`
	QualityEvaluation  AreaResult `json:"qualityEvaluation"`
}

// Get gives the result of an area, or nil for an unknown area name.
func (a *Areas) Get(area Area) *AreaResult {
	switch area {
	case AreaConformance:
		return &a.Conformance
	case AreaDeploymentTrace:
		return &a.DeploymentTrace
	case AreaADRGovernance:
		return &a.ADRGovernance
	case AreaDiagramCurrency:
		return &a.DiagramCurrency
	case AreaAssessmentCurrency:
		return &a.AssessmentCurrency
	case AreaQualityEvaluation:
		return &a.QualityEvaluation
	}

	return nil
}

// AreaOfFamily gives the report area of the rules of a predicate family. The rules of the views pack report in
// `diagramCurrency`, the rules of the adr-governance pack in `adrGovernance`, the rules of the review-currency pack
// in `assessmentCurrency`; the other families report in `conformance`. The three assurance families
// (`effective-access`, `check-result`, `assurance-claim`) also report in `conformance`, like the typed rules of the
// infrastructure pack: `deploymentTrace` is filled from the deployment trace and not from rules. The rules that
// quality scenarios make are of the family `assurance-claim` and report in `qualityEvaluation` (see AreaOfRule).
func AreaOfFamily(family policy.PredicateFamily) Area {
	switch family { //nolint:exhaustive // every other family reports in conformance.
	case policy.FamilyView:
		return AreaDiagramCurrency
	case policy.FamilyDecisionGovernance:
		return AreaADRGovernance
	case policy.FamilyAssessmentCurrency:
		return AreaAssessmentCurrency
	default:
		return AreaConformance
	}
}

// AreaOfRule gives the area of a rule result: `qualityEvaluation` for the rules that quality scenarios and required
// areas make (their ids start with `quality:`), else the area of the family of the rule.
func AreaOfRule(rule *RuleResult) Area {
	if isQualityRule(rule.RuleID) {
		return AreaQualityEvaluation
	}

	return AreaOfFamily(rule.Family)
}

// SettleAreas fills the areas that the run did not fill, and makes the summary verdict show every configured area.
//
// An area that no rule or later change reported is `not-configured`; `conformance` is always configured (its
// verdict is `not-applicable` when no rule exists). An area that the owning change filled before the call keeps
// its result, and its verdict joins the summary verdict: a mandatory `unknown` area is `unknown` in the summary
// even when `conformance` passes (OUT-03).
func (r *Run) SettleAreas() {
	filled := []RuleVerdict{}

	for _, area := range AreaOrder() {
		res := r.Areas.Get(area)
		if res.Verdict == "" {
			*res = r.areaFromRules(area)

			continue
		}

		if res.Verdict.Configured() {
			filled = append(filled, RuleVerdict(res.Verdict))
		}

		if res.Counts == nil {
			res.Counts = map[string]int{}
		}
	}

	if len(filled) > 0 {
		r.Summary.Verdict = WorstVerdict(append(filled, r.Summary.Verdict))
	}
}

// areaFromRules builds the area result from the rules and findings that belong to the area.
func (r *Run) areaFromRules(area Area) AreaResult {
	verdicts, counts := r.areaRuleVerdicts(area)

	if len(verdicts) == 0 && area != AreaConformance {
		return AreaResult{Verdict: AreaNotConfigured, Counts: map[string]int{}}
	}

	return AreaResult{
		Verdict: AreaVerdict(WorstVerdict(verdicts)),
		Counts: map[string]int{
			"pass": counts.Pass, "violation": counts.Violation, string(VerdictUnknown): counts.Unknown,
			"notApplicable": counts.NotApplicable, "error": counts.Error, "findings": r.areaFindingCount(area),
		},
	}
}

// areaRuleVerdicts collects the verdicts and per-verdict counts of the rules that report in the area.
func (r *Run) areaRuleVerdicts(area Area) ([]RuleVerdict, RuleCounts) {
	var (
		verdicts []RuleVerdict
		counts   RuleCounts
	)

	for i := range r.Rules {
		rule := &r.Rules[i]
		if AreaOfRule(rule) != area {
			continue
		}

		verdicts = append(verdicts, rule.Verdict)
		countAreaRuleVerdict(&counts, rule.Verdict)
	}

	return verdicts, counts
}

func countAreaRuleVerdict(counts *RuleCounts, verdict RuleVerdict) {
	switch verdict {
	case VerdictPass:
		counts.Pass++
	case VerdictViolation:
		counts.Violation++
	case VerdictUnknown:
		counts.Unknown++
	case VerdictNotApplicable:
		counts.NotApplicable++
	case VerdictError:
		counts.Error++
	}
}

// areaFindingCount counts the findings of the area; a finding without an area belongs to `conformance`.
func (r *Run) areaFindingCount(area Area) int {
	findings := 0

	for i := range r.Findings {
		if findingReportsInArea(r.Findings[i].Area, area) {
			findings++
		}
	}

	return findings
}

func findingReportsInArea(findingArea, area Area) bool {
	if findingArea == "" {
		return area == AreaConformance
	}

	return findingArea == area
}

// ConfiguredAreas gives the areas that the run evaluated, in report order.
func (r *Run) ConfiguredAreas() []Area {
	var out []Area

	for _, area := range AreaOrder() {
		if r.Areas.Get(area).Verdict.Configured() {
			out = append(out, area)
		}
	}

	return out
}
