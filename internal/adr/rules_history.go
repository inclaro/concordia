package adr

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

const presenceTextNoCapability = "the provider capability profile has no ADR evidence"

// historyUnits are the units of GOV-ADR-005: one per anomaly of a component that holds a registered decision.
func (e *engine) historyUnits(plan *policy.RulePlan) []unit {
	if !e.ev.Supported {
		finding := buildFinding(plan, &findingSpec{
			Result: conformance.VerdictUnknown, Reason: EvidenceNoCapability, Source: plan.RuleID, Subject: plan.RuleID,
			Message:  "the provider capability profile has no ADR evidence: the history is unknown",
			Expected: "the supersession history of the decisions is consistent", Observed: presenceTextNoCapability,
			Decision: nil,
		})

		return []unit{{subject: plan.RuleID, verdict: conformance.VerdictUnknown, code: EvidenceNoCapability, message: presenceTextNoCapability, finding: &finding}}
	}

	units := make([]unit, 0, len(e.hist.Anomalies)+1)

	for i := range e.hist.Anomalies {
		a := &e.hist.Anomalies[i]
		finding := e.anomalyFinding(plan, a)
		units = append(units, unit{
			subject: strings.Join(e.anomalyIDs(a), ","), verdict: conformance.VerdictViolation, code: string(a.Kind),
			message: finding.Observed, finding: &finding,
		})
	}

	if len(units) == 0 && len(e.decisions) > 0 {
		units = append(units, unit{subject: "history", verdict: conformance.VerdictPass})
	}

	return units
}

// anomalyIDs gives the canonical IDs of the members of an anomaly, sorted and unique.
func (e *engine) anomalyIDs(anomaly *Anomaly) []string {
	return sortedUnique(e.relatedIDs(anomaly.Paths))
}

// anomalyTargets are the targets of the finding of an anomaly: the missing successor, none for a duplicate ID, else
// the members.
func anomalyTargets(anomaly *Anomaly, ids []string) []string {
	if anomaly.Kind == ReasonMissingTarget {
		return []string{anomaly.Value}
	}

	if anomaly.Kind == ReasonDuplicateID {
		return nil
	}

	return ids
}

func (e *engine) anomalyFinding(plan *policy.RulePlan, anomaly *Anomaly) conformance.Finding {
	ids := e.anomalyIDs(anomaly)
	observed := e.anomalyText(anomaly, ids)
	spec := &findingSpec{
		Result: conformance.VerdictViolation, Reason: string(anomaly.Kind), Source: ids[0], Targets: anomalyTargets(anomaly, ids), Subject: ids[0],
		Message: observed, Expected: "the supersession history of the decisions is consistent: one chain with a single head",
		Observed: observed, Related: ids,
	}

	// The decision block and the place are those of the first member with provider evidence, in the order of the IDs.
	if member := e.firstEvidenceMember(anomaly, ids); member != nil {
		spec.Location = &conformance.Location{Path: member.Path, Line: member.Decision.Line}
		spec.Decision = e.memberBlock(member)
	}

	return buildFinding(plan, spec)
}

// firstEvidenceMember gives the history node of the first member of the anomaly that has provider evidence, in the
// order of the IDs; nil when no member has any.
func (e *engine) firstEvidenceMember(anomaly *Anomaly, ids []string) *HistoryNode {
	for _, id := range ids {
		if node := e.evidenceNodeNamed(anomaly.Paths, id); node != nil {
			return node
		}
	}

	return nil
}

// evidenceNodeNamed gives the history node of the first path whose canonical name is id and that has evidence.
func (e *engine) evidenceNodeNamed(paths []string, id string) *HistoryNode {
	for _, p := range paths {
		if e.nameOf(p) == id && e.nodes[p] != nil {
			return e.nodes[p]
		}
	}

	return nil
}

// memberBlock is the decision block of a history node.
func (e *engine) memberBlock(member *HistoryNode) *conformance.FindingDecision {
	return e.red.block(&blockSpec{
		ID: e.registeredID(member.Path), Evidence: &member.Decision, Successors: e.relatedIDs(e.hist.Successors(member.Path)),
		Predecessors: e.relatedIDs(e.hist.Predecessors(member.Path)), Governs: e.governsOf(member.Path), EnforcedBy: e.rulesOf(member.Path),
	})
}

// decisionAtPath gives the registered decision whose provider evidence is at the path as written; nil when none is.
func (e *engine) decisionAtPath(path string) *Decision {
	for _, dec := range e.decisions {
		if dec.Presence.Decision != nil && dec.Presence.Path == path {
			return dec
		}
	}

	return nil
}

// registeredID gives the ID of the register entry that matched the path as written, else the canonical label.
func (e *engine) registeredID(path string) string {
	if dec := e.decisionAtPath(path); dec != nil {
		return dec.Entry.ID
	}

	return e.nameOf(path)
}

func (e *engine) governsOf(path string) []string {
	if dec := e.decisionAtPath(path); dec != nil {
		return dec.Subjects
	}

	return nil
}

func (e *engine) rulesOf(path string) []string {
	if dec := e.decisionAtPath(path); dec != nil {
		return dec.RuleRefs()
	}

	return nil
}

func (e *engine) relatedIDs(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, e.nameOf(p))
	}

	return out
}

// anomalyText says what the anomaly is, naming the decisions.
func (e *engine) anomalyText(anomaly *Anomaly, ids []string) string {
	joined := strings.Join(ids, ", ")

	switch anomaly.Kind {
	case ReasonCycle:
		return "supersession cycle between " + joined
	case ReasonMissingTarget:
		return joined + " names the superseding decision " + fmt.Sprintf("%q", anomaly.Value) + ", which is not a committed decision"
	case ReasonDuplicateID:
		return "several committed ADR files carry the ID " + anomaly.Value + ": " + strings.Join(anomaly.Paths, ", ")
	case ReasonContradictoryStatus:
		if len(anomaly.Paths) == 1 {
			return joined + " has status superseded but names no successor"
		}

		return "the status of the decisions " + joined + " is contradictory: the successor is neither active nor pending"
	case ReasonCompetingSuccessors:
		return "competing successors among " + joined
	case ReasonIndexOnly, ReasonAbsent, ReasonPathMismatch, ReasonInactive, ReasonSuperseded, ReasonHistoricalLink,
		ReasonRuleUnjustified, ReasonEnforcementGap, ReasonAdvisoryOnly, ReasonTriggerUnsatisfied:
	}

	return string(anomaly.Kind) + " among " + joined
}
