package quality

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

// AreaVerdict is the verdict of one area. `no-scenario` is the state of an area that no scenario covers and that
// the workspace does not require; it is never a pass.
type AreaVerdict string

// The area verdicts.
const (
	AreaPass       AreaVerdict = "pass"
	AreaViolation  AreaVerdict = "violation"
	AreaUnknown    AreaVerdict = "unknown"
	AreaError      AreaVerdict = "error"
	AreaNoScenario AreaVerdict = "no-scenario"
)

// Text is the verdict as the specs and the terminal output write it: `no scenario`.
func (v AreaVerdict) Text() string { return strings.ReplaceAll(string(v), "-", " ") }

// DetailRequiredAreaNoScenario is the detail of the violation of a required area that no scenario covers.
const DetailRequiredAreaNoScenario = "the area is required by assurance.quality.requiredAreas and no scenario covers it"

// AreaScenario is one scenario of an area with its result.
type AreaScenario struct {
	ID      string  `json:"id"`
	Mode    Mode    `json:"mode"`
	Verdict Verdict `json:"verdict"`
}

// AreaFinding is a finding that the report lists next to an area.
type AreaFinding struct {
	ID        string        `json:"id"`
	Type      FindingType   `json:"type"`
	Status    FindingStatus `json:"status"`
	Statement string        `json:"statement"`
	Owner     string        `json:"owner"`
	Scenarios []string      `json:"scenarios"`
}

// AreaCoverage is the coverage of one area.
type AreaCoverage struct {
	Area    string      `json:"area"`
	Verdict AreaVerdict `json:"verdict"`
	// Detail says why the verdict is a violation for a missing scenario, and is empty otherwise.
	Detail    string         `json:"detail,omitempty"`
	Scenarios []AreaScenario `json:"scenarios"`
	Findings  []AreaFinding  `json:"findings"`
	Required  bool           `json:"required"`
}

// Coverage reports each of the six areas: the scenarios that cover it with their verdicts, the verdict of the
// area, and the findings that concern it.
//
// The verdict of a covered area is the worst verdict of its scenarios (`error`, `violation`, `unknown`, `pass`),
// so an area with one `pass` and one `unknown` scenario is `unknown`. A scenario with no entry in results counts
// as `unknown`. An area without a scenario is `no-scenario`, never `pass`; when `required` lists it, it is a
// `violation` instead. The rows are made from the six areas and `required`, not from the scenario set, so an
// empty set shows every required area as a violation and cannot remove them.
//
// A finding is listed next to each area in its `areas`, or next to the areas of its scenarios when it names none,
// whatever its status and whatever the scenarios of the other areas say: a passing `performance` scenario does not
// close an open tradeoff that lowers `modifiability`. Findings never change an area verdict.
func Coverage(set *Set, results map[string]Result, required []string) []AreaCoverage {
	rows := make([]AreaCoverage, len(Areas))

	for i, area := range Areas {
		rows[i] = AreaCoverage{
			Area: area, Required: slices.Contains(required, area), Scenarios: []AreaScenario{}, Findings: []AreaFinding{},
		}
	}

	rowOf := func(area string) *AreaCoverage {
		if i := slices.Index(Areas, area); i >= 0 {
			return &rows[i]
		}

		return nil
	}

	for _, scenario := range set.Scenarios {
		row := rowOf(scenario.Area)
		if row == nil {
			continue
		}

		verdict := VerdictUnknown
		if res, ok := results[scenario.ID]; ok {
			verdict = res.Verdict
		}

		row.Scenarios = append(row.Scenarios, AreaScenario{ID: scenario.ID, Mode: scenario.Mode, Verdict: verdict})
	}

	for _, f := range set.Findings {
		entry := AreaFinding{
			ID: f.ID, Type: f.Type, Status: f.Status, Statement: f.Statement, Owner: f.Owner, Scenarios: slices.Clone(f.Scenarios),
		}

		for _, area := range findingAreas(set, f) {
			if row := rowOf(area); row != nil {
				row.Findings = append(row.Findings, entry)
			}
		}
	}

	for i := range rows {
		row := &rows[i]

		slices.SortStableFunc(row.Scenarios, func(a, b AreaScenario) int { return canonjson.CompareUTF16(a.ID, b.ID) })
		slices.SortStableFunc(row.Findings, func(a, b AreaFinding) int { return canonjson.CompareUTF16(a.ID, b.ID) })

		row.Verdict, row.Detail = areaVerdict(row)
	}

	return rows
}

// areaVerdict is the worst verdict of the scenarios of an area, or the verdict of an area without a scenario.
func areaVerdict(row *AreaCoverage) (verdict AreaVerdict, detail string) {
	if len(row.Scenarios) == 0 {
		if row.Required {
			return AreaViolation, DetailRequiredAreaNoScenario
		}

		return AreaNoScenario, ""
	}

	worst := VerdictPass

	for _, scenario := range row.Scenarios {
		if scenario.Verdict.severity() > worst.severity() {
			worst = scenario.Verdict
		}
	}

	switch worst {
	case VerdictPass:
		return AreaPass, ""
	case VerdictViolation:
		return AreaViolation, ""
	case VerdictError:
		return AreaError, ""
	case VerdictUnknown, VerdictNotApplicable:
	}

	return AreaUnknown, ""
}

// findingAreas gives the areas that a finding concerns.
func findingAreas(set *Set, f *Finding) []string {
	if len(f.Areas) > 0 {
		return f.Areas
	}

	var areas []string

	for _, id := range f.Scenarios {
		if sc := set.Scenario(id); sc != nil && !slices.Contains(areas, sc.Area) {
			areas = append(areas, sc.Area)
		}
	}

	return areas
}

// ValidateRequiredAreas checks the names in `assurance.quality.requiredAreas`. An unknown name is a CO-QUA-002
// error at the workspace file, because a typo would otherwise make the requirement silently empty.
func ValidateRequiredAreas(source string, required []string) []*diag.Error {
	var errs []*diag.Error

	for _, area := range required {
		if !slices.Contains(Areas, area) {
			errs = append(errs, diag.Fail(diag.QUA002, diag.Detail(fmt.Sprintf(
				"requiredAreas names %s, which is not an area (one of %s)", area, strings.Join(Areas, ", "))),
				diag.WithSource(diag.Source{URI: source})))
		}
	}

	return errs
}
