package reporting

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
)

// qualityOpen says whether the quality evaluation area of the run must not be read as a pass: it is `unknown` or
// `violation` (OUT-03). The pretty report names such an area before the summary line, so that a clean `conformance`
// section never shows one overall pass while a mandatory scenario has no evidence.
func qualityOpen(run *conformance.Run) bool {
	switch conformance.RuleVerdict(run.Areas.QualityEvaluation.Verdict) {
	case conformance.VerdictUnknown, conformance.VerdictViolation:
		return true
	case conformance.VerdictPass, conformance.VerdictNotApplicable, conformance.VerdictError:
	}

	return false
}

// verdictWithReason is a verdict with its closed reason in parentheses.
func verdictWithReason(verdict conformance.RuleVerdict, reason conformance.UnknownReason) string {
	if reason == "" {
		return string(verdict)
	}

	return string(verdict) + " (" + string(reason) + ")"
}

// qualityRows are the rows of the Quality evaluation section: one row for each scenario with the mode of its result
// (the modes are never merged), the coverage of each area, the findings that are still open next to their areas, and
// a note for each decision that a scenario names and that is not active.
func qualityRows(run *conformance.Run) []string {
	report := run.Assurance
	if report == nil {
		return nil
	}

	var rows []string

	for i := range report.Quality {
		entry := &report.Quality[i]

		rows = append(rows, fmt.Sprintf("  %s (%s, %s, %s): %s",
			entry.Scenario, entry.Mode, entry.Enforcement, entry.Area, verdictWithReason(entry.Verdict, entry.Reason)))

		for _, link := range entry.Links {
			if link.Status != "active" {
				rows = append(rows, fmt.Sprintf("    %s: decision %s is %s", entry.Scenario, link.Canonical, linkText(&link)))
			}
		}
	}

	var covered, uncovered, open []string

	for i := range report.QualityAreas {
		area := &report.QualityAreas[i]

		switch {
		case len(area.Scenarios) > 0:
			covered = append(covered, area.Area+" "+strings.ReplaceAll(area.Verdict, "-", " "))
		case area.Required:
			uncovered = append(uncovered, area.Area+" (required: "+strings.ReplaceAll(area.Verdict, "-", " ")+")")
		default:
			uncovered = append(uncovered, area.Area)
		}

		for i := range area.Findings {
			finding := &area.Findings[i]
			if finding.Status == "open" {
				open = append(open, fmt.Sprintf("  Open %s %s next to %s: %s", finding.Type, finding.ID, area.Area, finding.Statement))
			}
		}
	}

	if len(covered) > 0 {
		rows = append(rows, "  Areas with a scenario: "+strings.Join(covered, ", "))
	}

	if len(uncovered) > 0 {
		rows = append(rows, "  Areas without a scenario: "+strings.Join(uncovered, ", "))
	}

	return sanitized(append(rows, open...))
}

// linkText says what an addressedBy link that does not count as active tells.
func linkText(link *conformance.QualityLink) string {
	switch {
	case link.State == "superseded" || link.State == "inactive":
		return "not active (" + link.State + "); review the rationale"
	case link.Detail != "":
		return "unknown (" + link.Detail + ")"
	}

	return "unknown"
}
