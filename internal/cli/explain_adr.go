package cli

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/redact"
)

// decisionExplanation is the decision block of a governance finding as `explain` shows it: the block with its title
// redacted, the redaction records, and the lines.
type decisionExplanation struct {
	decision *conformance.FindingDecision
	lines    []string
	records  []redact.Record
}

// redactedTitle removes secret values from the title of a decision: an ADR title is untrusted repository text. The
// records say where a value was removed; they never hold the value.
func redactedTitle(d *conformance.FindingDecision) (string, []redact.Record) {
	text, records := redact.Default().Redact(d.Path, d.Title)

	for i := range records {
		records[i].File, records[i].Line, records[i].Output = d.Path, d.Line, outputExplain
	}

	return text, records
}

// noneText is the text of an empty list.
const noneText = "(none)"

// outputExplain is the output name of a redaction record of `explain`.
const outputExplain = "explain"

// explainDecision prepares the decision block of a finding: the block with the redacted title and, for the pretty
// output, the lines that state the decision, its three states, the history chain, the governed subjects, the linked
// rules, and the candidate successors (design D7 and D8 of add-adr-governance). nil for a finding without a decision.
func explainDecision(run *conformance.Run, finding *conformance.Finding) *decisionExplanation {
	if finding.Decision == nil {
		return nil
	}

	block := *finding.Decision
	title, records := redactedTitle(&block)
	block.Title = title

	lines := []string{
		"  Decision: " + block.ID + " " + block.Title,
		"    Status: " + block.Status + " (status text: " + block.StatusText + ")",
	}

	if block.Path != "" {
		lines = append(lines, "    Path: "+locationText(&conformance.Location{Path: block.Path, Line: block.Line}))
	}

	lines = append(lines, decisionStateLines(run, &block)...)
	lines = append(lines, historyLines(run, &block)...)
	lines = append(lines, listLines("Governed subjects", block.Governs)...)
	lines = append(lines, listLines("Linked rules", block.EnforcedBy)...)
	lines = append(lines, "  Successors: "+joinOrNone(block.Successors), "  Predecessors: "+joinOrNone(block.Predecessors))

	return &decisionExplanation{decision: &block, lines: lines, records: records}
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return noneText
	}

	return strings.Join(items, ", ")
}

// decisionEntryOf gives the entry of the decision summary that the finding's decision names: by ADR path, else by ID.
func decisionEntryOf(run *conformance.Run, block *conformance.FindingDecision) *conformance.DecisionEntry {
	if run.DecisionSummary == nil {
		return nil
	}

	entries := run.DecisionSummary.Decisions

	if i := slices.IndexFunc(entries, func(e conformance.DecisionEntry) bool {
		return block.Path != "" && e.Path == block.Path && e.ID == block.ID
	}); i >= 0 {
		return &entries[i]
	}

	if i := slices.IndexFunc(entries, func(e conformance.DecisionEntry) bool { return e.ID == block.ID }); i >= 0 {
		return &entries[i]
	}

	return nil
}

// decisionStateLines are the three separate states of the decision in the summary of the run.
func decisionStateLines(run *conformance.Run, d *conformance.FindingDecision) []string {
	entry := decisionEntryOf(run, d)
	if entry == nil {
		return nil
	}

	applicability := entry.Applicability.State + reasonSuffix(entry.Applicability.Reason)
	if entry.Applicability.StatusLags {
		applicability += " (the status still says active)"
	}

	enforcement := entry.Enforcement.State
	if entry.Enforcement.Required {
		enforcement += " (required)"
	}

	return []string{
		"  Presence: " + entry.Presence.State + reasonSuffix(entry.Presence.Reason),
		"  Applicability: " + applicability,
		"  Enforcement: " + enforcement,
	}
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}

	return " (" + reason + ")"
}

// historyLines is the chain of supersession links that touch the decision, with their provenance and effect. The
// links come from the decision summary of the run.
func historyLines(run *conformance.Run, d *conformance.FindingDecision) []string {
	links := historyLinksOf(run, d)
	if len(links) == 0 {
		return []string{"  History: none"}
	}

	lines := []string{"  History: " + strconv.Itoa(len(links)) + " link(s)"}

	for i := range links {
		lines = append(lines, historyLinkLines(&links[i])...)
	}

	return lines
}

// historyLinksOf are the links of the decision summary that touch the decision, by ADR path or by ID.
func historyLinksOf(run *conformance.Run, d *conformance.FindingDecision) []conformance.HistoryLink {
	if run.DecisionSummary == nil {
		return nil
	}

	var links []conformance.HistoryLink

	for i := range run.DecisionSummary.History.Links {
		if link := &run.DecisionSummary.History.Links[i]; historyLinkTouches(link, d) {
			links = append(links, *link)
		}
	}

	return links
}

// historyLinkTouches reports whether the link names the decision as either end.
func historyLinkTouches(link *conformance.HistoryLink, d *conformance.FindingDecision) bool {
	byPath := link.FromPath == d.Path || link.ToPath == d.Path
	byID := link.From == d.ID || link.To == d.ID

	return byPath || byID
}

// historyLinkLines state one supersession link, with its paths when it has any.
func historyLinkLines(link *conformance.HistoryLink) []string {
	lines := []string{"    " + link.From + " supersedes " + link.To + " (provenance: " + link.Provenance + "; effect: " + link.Effect + ")"}

	if link.FromPath != "" || link.ToPath != "" {
		lines = append(lines, "      "+cmp.Or(link.FromPath, "?")+" -> "+cmp.Or(link.ToPath, "?"))
	}

	return lines
}

// candidateLines are the successors that the history names. Governance never moves to a successor on its own: each
// is shown as `applied: false`.
func candidateLines(d *conformance.FindingDecision) []string {
	if len(d.CandidateSuccessors) == 0 {
		return []string{"  Candidate successors: 0"}
	}

	lines := []string{"  Candidate successors: " + strconv.Itoa(len(d.CandidateSuccessors))}

	for _, c := range d.CandidateSuccessors {
		lines = append(lines, "    "+c.ID+" "+c.Path+" (applied: "+strconv.FormatBool(c.Applied)+"; the register has to be edited to move to it)")
	}

	return lines
}

// endpointLine is the `Source` or `Target` line of a finding. The ends of a governance finding are decision IDs,
// rule IDs, and subject refs, not CALM node IDs: they are shown as they are. For the other findings an end is a list
// of CALM nodes.
func endpointLine(label string, finding *conformance.Finding, id string) string {
	if finding.Area == conformance.AreaADRGovernance {
		return label + ": " + cmp.Or(id, noneText)
	}

	if label == "Source" {
		return label + ": calm:node:" + id
	}

	return label + ": " + calmNodes(strings.Split(id, ","))
}
