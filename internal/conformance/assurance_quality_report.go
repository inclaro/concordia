package conformance

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/canonjson"
)

// QualityEvidence is one result that evaluates a scenario (the `EVALUATED_BY` link): the rule that a structural
// scenario reads, the measurement file of a measured one, or the assessment of a reviewed one.
type QualityEvidence struct {
	// Kind is `rule`, `measurement`, or `assessment`.
	Kind string `json:"kind"`
	// Ref is the rule id, the workspace-relative path of the statement, or the assessment id.
	Ref string `json:"ref"`
}

// QualityLink is one `addressedBy` link of a scenario or a finding (the `ADDRESSES` link, design D26).
type QualityLink struct {
	// ADR is the identifier as written, Canonical the form that was compared.
	ADR       string `json:"adr"`
	Canonical string `json:"canonical"`
	// Status is `active`, `not-active`, or `unknown`. Only `active` counts as the rationale of the owner.
	Status string `json:"status"`
	// State is the applicability of the registered decision; empty when the run does not evaluate ADR governance.
	State string `json:"state,omitempty"`
	// Detail is `adr-governance-not-available`, or the state of an unknown link.
	Detail   string           `json:"detail,omitempty"`
	Location quality.Location `json:"location"`
	// ReviewRationale says that the decision is superseded or inactive: the owner needs a review of its rationale.
	ReviewRationale bool `json:"reviewRationale"`
}

// QualityEntry is one quality scenario of `assurance.quality` (spec quality-evaluation, "Quality evaluation is a
// separate report area"). The verdict is the verdict of the scenario in its own mode: the three modes are never
// merged into one score, and a result of one mode does not satisfy a scenario of another.
type QualityEntry struct {
	Scenario    string `json:"scenario"`
	Area        string `json:"area"`
	Mode        string `json:"mode"`
	Environment string `json:"environment"`
	// Enforcement is `mandatory` or `advisory`.
	Enforcement string      `json:"enforcement"`
	Verdict     RuleVerdict `json:"verdict"`
	// Reason is the closed unknown reason of an `unknown` verdict.
	Reason UnknownReason `json:"reason,omitempty"`
	// Details say why, in a fixed order.
	Details []string `json:"details"`
	// Evidence lists the results that evaluate the scenario, sorted.
	Evidence []QualityEvidence `json:"evidence"`
	// RuleID is the rule that carries the verdict of the scenario in `rules`.
	RuleID string        `json:"ruleId"`
	Links  []QualityLink `json:"links"`
	// Findings are the fingerprints of the findings of the scenario in `findings`: empty for a pass.
	Findings []string         `json:"findings"`
	Location quality.Location `json:"location"`
	// ReviewRationale says that a linked decision is superseded or inactive.
	ReviewRationale bool `json:"reviewRationale"`
}

// QualityAreaScenario is a scenario of an area with its verdict.
type QualityAreaScenario struct {
	ID      string      `json:"id"`
	Mode    string      `json:"mode"`
	Verdict RuleVerdict `json:"verdict"`
}

// QualityFinding is a sensitivity point, a risk, or a tradeoff that the report lists next to an area. A passing
// scenario never closes it: only its status does.
type QualityFinding struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	Status    string        `json:"status"`
	Statement string        `json:"statement"`
	Owner     string        `json:"owner"`
	Scenarios []string      `json:"scenarios"`
	Links     []QualityLink `json:"links"`
}

// QualityAreaEntry is the coverage of one of the six quality areas.
type QualityAreaEntry struct {
	Area string `json:"area"`
	// Verdict is `pass`, `violation`, `unknown`, `error`, or `no-scenario`. An area without a scenario is never a
	// pass; a required one is a violation.
	Verdict string `json:"verdict"`
	// Detail says why a required area without a scenario is a violation.
	Detail    string                `json:"detail,omitempty"`
	Scenarios []QualityAreaScenario `json:"scenarios"`
	Findings  []QualityFinding      `json:"findings"`
	// Reported are the fingerprints of the findings in `findings` of the run that the area has itself: the violation
	// of a required area that no scenario covers.
	Reported []string `json:"reported"`
	// Required says that `assurance.quality.requiredAreas` lists the area.
	Required bool `json:"required"`
}

// qualityEntries makes the scenario section of the report, sorted by scenario id.
func qualityEntries(outcomes []qualityOutcome, links []quality.Link, fingerprints map[string][]string) []QualityEntry {
	out := make([]QualityEntry, 0, len(outcomes))

	for i := range outcomes {
		scenario, res := outcomes[i].scenario, &outcomes[i].result

		entry := QualityEntry{
			Scenario: scenario.ID, Area: scenario.Area, Mode: string(scenario.Mode), Environment: scenario.Environment,
			Enforcement: string(scenario.Enforcement), Verdict: RuleVerdict(res.Verdict), Reason: UnknownReason(res.Reason),
			Details: nonNilStrings(res.Details), Evidence: slices.Clone(outcomes[i].evidence), RuleID: qualityRulePrefix + scenario.ID,
			Links: []QualityLink{}, Findings: sortedUniqueStrings(fingerprints[scenario.ID]),
			Location: scenario.Location,
		}

		if entry.Evidence == nil {
			entry.Evidence = []QualityEvidence{}
		}

		slices.SortStableFunc(entry.Evidence, func(a, b QualityEvidence) int {
			return canonjson.CompareUTF16(a.Kind+"\x00"+a.Ref, b.Kind+"\x00"+b.Ref)
		})

		for j := range links {
			if links[j].OwnerKind == ownerScenario && links[j].Owner == scenario.ID {
				entry.Links = append(entry.Links, qualityLink(&links[j]))
				entry.ReviewRationale = entry.ReviewRationale || links[j].ReviewRationale
			}
		}

		out = append(out, entry)
	}

	slices.SortStableFunc(out, func(a, b QualityEntry) int { return canonjson.CompareUTF16(a.Scenario, b.Scenario) })

	return out
}

// qualityLink converts a link of the quality package.
func qualityLink(link *quality.Link) QualityLink {
	return QualityLink{
		ADR: link.ADR, Canonical: link.Canonical, Status: string(link.Status), State: string(link.State), Detail: link.Detail,
		ReviewRationale: link.ReviewRationale, Location: link.Location,
	}
}

// qualityAreaEntries makes the area section of the report, sorted by area name.
func qualityAreaEntries(coverage []quality.AreaCoverage, links []quality.Link, reported map[string][]string) []QualityAreaEntry {
	out := make([]QualityAreaEntry, 0, len(coverage))

	for i := range coverage {
		row := &coverage[i]

		entry := QualityAreaEntry{
			Area: row.Area, Verdict: string(row.Verdict), Required: row.Required, Detail: row.Detail,
			Scenarios: make([]QualityAreaScenario, 0, len(row.Scenarios)), Findings: make([]QualityFinding, 0, len(row.Findings)),
			Reported: sortedUniqueStrings(reported[row.Area]),
		}

		for _, scenario := range row.Scenarios {
			entry.Scenarios = append(entry.Scenarios, QualityAreaScenario{ID: scenario.ID, Mode: string(scenario.Mode), Verdict: RuleVerdict(scenario.Verdict)})
		}

		for _, f := range row.Findings {
			finding := QualityFinding{
				ID: f.ID, Type: string(f.Type), Status: string(f.Status), Statement: f.Statement, Owner: f.Owner,
				Scenarios: sortedUniqueStrings(f.Scenarios), Links: []QualityLink{},
			}

			for j := range links {
				if links[j].OwnerKind == ownerFinding && links[j].Owner == f.ID {
					finding.Links = append(finding.Links, qualityLink(&links[j]))
				}
			}

			entry.Findings = append(entry.Findings, finding)
		}

		out = append(out, entry)
	}

	slices.SortStableFunc(out, func(a, b QualityAreaEntry) int { return canonjson.CompareUTF16(a.Area, b.Area) })

	return out
}
