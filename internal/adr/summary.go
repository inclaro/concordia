package adr

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
)

// Values of DecisionSummary.ProviderSupport.
const (
	providerSupported   = "supported"
	providerUnsupported = "unsupported"
)

// summary builds the `decisionSummary` of the run (design D8): every list is sorted and none is nil. Titles are
// redacted; the findings are the fingerprints of the findings of the selected rules.
func (e *engine) summary(
	triggers []conformance.TriggerResult, candidates []conformance.DecisionCandidate, findings []conformance.Finding,
) *conformance.DecisionSummary {
	support := providerSupported
	if !e.ev.Supported {
		support = providerUnsupported
	}

	entries := make([]conformance.DecisionEntry, 0, len(e.decisions))
	for _, d := range e.decisions {
		entries = append(entries, e.entryOf(d))
	}

	fingerprints := make([]string, 0, len(findings))
	for i := range findings {
		fingerprints = append(fingerprints, findings[i].Fingerprint)
	}

	slices.SortFunc(fingerprints, canonjson.CompareUTF16)

	if triggers == nil {
		triggers = []conformance.TriggerResult{}
	}

	return &conformance.DecisionSummary{
		ProviderSupport: support, Decisions: entries, History: conformance.DecisionHistory{Links: e.historyLinks()},
		RuleJustification: e.justificationSummary(), Triggers: triggers, Candidates: candidates, Findings: slices.Compact(fingerprints),
	}
}

// entryOf makes the entry of a registered decision with its three separate states.
func (e *engine) entryOf(dec *Decision) conformance.DecisionEntry {
	entry := conformance.DecisionEntry{
		ID: dec.Entry.ID, Project: dec.Entry.Project, Expect: string(dec.Expected()), MappedStatus: dec.Mapped,
		Path: dec.Presence.Path, Categories: sortedUnique(dec.Entry.Categories), Governs: dec.Subjects,
		Released: releasedKeys(dec.Entry), Successors: sortedUnique(neighborIDs(dec.Successors)),
		Predecessors: sortedUnique(neighborIDs(dec.Predecessors)),
		Presence:     conformance.DecisionState{State: string(dec.Presence.State), Reason: dec.Presence.Reason},
		Applicability: conformance.DecisionApplicability{
			State: string(dec.Applicability), Reason: dec.Reason, StatusLags: dec.StatusLags,
		},
	}

	if ev := dec.Presence.Decision; ev != nil {
		entry.Status, entry.StatusText, entry.Line = ev.Status, ev.StatusText, ev.Line
		entry.Title = e.red.title(ev.Path, ev.Line, ev.Title, outputSummary)

		if entry.MappedStatus == "" {
			entry.MappedStatus, _ = e.decls.Lifecycle.Classify(ev.Status, ev.StatusText)
		}
	}

	if dec.Applicability == ApplicabilitySuperseded {
		entry.Applicability.Reason = string(ReasonSuperseded) + "-by " + joinOrNone(sortedUnique(neighborIDs(dec.EffectiveSuccessors())))
	}

	state := linkState(dec, e.class)
	if !dec.Justifies() && !dec.Unresolved() {
		state = EnforcementNotApplicable
	}

	entry.Enforcement = conformance.DecisionEnforcement{
		State: string(state), Required: dec.Entry.Enforcement == EnforcementRequired, Rules: dec.RuleRefs(),
	}

	return entry
}

func releasedKeys(entry *Entry) []string {
	out := make([]string, 0, len(entry.Released))
	for i := range entry.Released {
		out = append(out, entry.Released[i].Key())
	}

	return sortedUnique(out)
}

// historyLinks gives the links of the analyzed components with their canonical IDs, sorted by from, then to.
func (e *engine) historyLinks() []conformance.HistoryLink {
	out := make([]conformance.HistoryLink, 0, len(e.hist.Links))

	for _, link := range e.hist.Links {
		out = append(out, conformance.HistoryLink{
			From: e.nameOf(link.FromPath), To: e.nameOf(link.ToPath), FromPath: link.FromPath, ToPath: link.ToPath,
			Provenance: string(link.Provenance), Effect: string(link.Effect),
		})
	}

	slices.SortStableFunc(out, func(a, b conformance.HistoryLink) int {
		if c := canonjson.CompareUTF16(a.From, b.From); c != 0 {
			return c
		}

		if c := canonjson.CompareUTF16(a.To, b.To); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.FromPath, b.FromPath)
	})

	return out
}

// justificationSummary gives the results of GOV-ADR-003 for every judged rule, sorted by rule ref.
func (e *engine) justificationSummary() []conformance.RuleJustification {
	results := EvaluateJustification(e.decisions, e.class)
	out := make([]conformance.RuleJustification, 0, len(results))

	for i := range results {
		linkedBy := make([]string, 0, len(results[i].LinkedBy))
		for _, d := range results[i].LinkedBy {
			linkedBy = append(linkedBy, d.Entry.ID)
		}

		out = append(out, conformance.RuleJustification{
			Rule: results[i].Rule.String(), Result: string(results[i].Verdict), LinkedBy: sortedUnique(linkedBy),
			Mandatory: results[i].Mandatory,
		})
	}

	return out
}
