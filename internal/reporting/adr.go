package reporting

import (
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
)

// Keys of the ADR governance entries in the SARIF run properties.
const (
	keyStatus   = "status"
	keyPresence = "presence"
)

// adrOpen says whether the ADR governance area of the run must not be read as a pass: it is `unknown` or
// `violation` (OUT-03). The pretty report names such an area before the summary line, so that a passing
// `conformance` section never shows one overall pass.
func adrOpen(run *conformance.Run) bool {
	switch conformance.RuleVerdict(run.Areas.ADRGovernance.Verdict) {
	case conformance.VerdictUnknown, conformance.VerdictViolation:
		return true
	case conformance.VerdictPass, conformance.VerdictNotApplicable, conformance.VerdictError:
	}

	return false
}

// stateText is a state with its reason in parentheses. A reason that only repeats the state is left out.
func stateText(state, reason string) string {
	if reason == "" || reason == state {
		return state
	}

	return state + " (" + reason + ")"
}

// decisionPlace is the ADR path of a decision, with its line when known.
func decisionPlace(d *conformance.DecisionEntry) string {
	if d.Line > 0 {
		return d.Path + ":" + strconv.Itoa(d.Line)
	}

	return d.Path
}

// decisionRow is the row of one decision in the pretty report: its place and status, then the three separate
// states with their reasons. Every state is a word; the title of the decision is not shown here.
func decisionRow(entry *conformance.DecisionEntry) []string {
	applicability := entry.Applicability.Reason
	if entry.Applicability.StatusLags {
		applicability = strings.TrimPrefix(applicability+"; status lags", "; ")
	}

	enforcement := entry.Enforcement.State

	var detail []string

	if entry.Enforcement.Required {
		detail = append(detail, "required")
	}

	if len(entry.Enforcement.Rules) > 0 {
		detail = append(detail, "rules: "+strings.Join(entry.Enforcement.Rules, ", "))
	}

	if len(detail) > 0 {
		enforcement += " (" + strings.Join(detail, "; ") + ")"
	}

	return []string{
		"  " + entry.ID + " " + entry.Status + " (" + decisionPlace(entry) + ")",
		"    presence " + stateText(entry.Presence.State, entry.Presence.Reason) +
			"; applicability " + stateText(entry.Applicability.State, applicability) +
			"; enforcement " + enforcement,
	}
}

// decisionRows are the rows of the ADR governance section: a note when the provider cannot give ADR evidence, then
// one row for each registered decision in the order of the run (sorted by canonical ID).
func decisionRows(run *conformance.Run) []string {
	summary := run.DecisionSummary
	if summary == nil {
		return nil
	}

	rows := make([]string, 0, len(summary.Decisions)+1)

	if summary.ProviderSupport == "unsupported" {
		rows = append(rows, "  Provider support: unsupported (the capability profile has no ADR evidence)")
	}

	for i := range summary.Decisions {
		rows = append(rows, decisionRow(&summary.Decisions[i])...)
	}

	return sanitized(rows)
}

// decisionBlockLines are the lines of a governance finding that replace the CALM source and target: the decision,
// its status, and what it governs. The title of the decision is not shown.
func decisionBlockLines(block *conformance.FindingDecision) []string {
	lines := []string{"Decision: " + block.ID + " (status: " + block.Status + ")"}

	if len(block.Governs) > 0 {
		lines = append(lines, "Governs: "+strings.Join(block.Governs, ", "))
	}

	if len(block.EnforcedBy) > 0 {
		lines = append(lines, "Enforced by: "+strings.Join(block.EnforcedBy, ", "))
	}

	if len(block.Successors) > 0 {
		lines = append(lines, "Successors: "+strings.Join(block.Successors, ", "))
	}

	return lines
}

// decisionsProperty is the ADR governance area in the SARIF run properties: the verdict of the area and, for each
// decision, its three states. A SARIF viewer shows it beside the results.
func decisionsProperty(run *conformance.Run) obj {
	decisions := make([]any, 0, len(run.DecisionSummary.Decisions))

	for i := range run.DecisionSummary.Decisions {
		d := &run.DecisionSummary.Decisions[i]
		decisions = append(decisions, obj{
			keyID: d.ID, "path": d.Path, keyStatus: d.Status,
			keyPresence: d.Presence.State, "applicability": d.Applicability.State, "enforcement": d.Enforcement.State,
		})
	}

	return obj{"verdict": run.Areas.ADRGovernance.Verdict, "decisions": decisions}
}

// decisionLocation is the place of a governance finding that has no location of its own: the ADR file and line of
// its decision, else the register entry that the decision summary names.
func decisionLocation(run *conformance.Run, finding *conformance.Finding) *conformance.Location {
	if finding.Decision == nil {
		return nil
	}

	if finding.Decision.Path != "" {
		return &conformance.Location{Path: finding.Decision.Path, Line: finding.Decision.Line}
	}

	if run.DecisionSummary == nil {
		return nil
	}

	for i := range run.DecisionSummary.Decisions {
		if d := &run.DecisionSummary.Decisions[i]; d.ID == finding.Decision.ID && d.Path != "" {
			return &conformance.Location{Path: d.Path, Line: d.Line}
		}
	}

	return nil
}
