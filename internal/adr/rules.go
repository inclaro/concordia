package adr

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

// unit is the result of one unit of a governance rule: a registered decision, a linked rule, an anomaly, or a
// trigger. A violation or an unknown unit has a finding.
type unit struct {
	finding *conformance.Finding
	subject string
	verdict conformance.RuleVerdict
	// code and message are the reason of a unit that is not a pass.
	code, message string
}

// assemble makes the rule result of a plan from its units: the worst verdict, the reasons of the units that are not
// a pass, the subjects, and the findings (without duplicates). A rule with no unit is `not-applicable` with the
// given reason.
func assemble(plan *policy.RulePlan, units []unit, emptyCode, emptyMessage string) (conformance.RuleResult, []conformance.Finding) {
	verdicts, subjects, reasons := summarizeUnits(units)
	found := uniqueUnitFindings(units)

	if len(units) == 0 {
		reasons = append(reasons, conformance.Reason{Code: emptyCode, Message: emptyMessage})
	}

	return conformance.RuleResult{
		RuleID: plan.RuleID, Name: plan.Name, Severity: plan.Severity, Policy: plan.Pack, Family: policy.FamilyDecisionGovernance,
		Verdict: conformance.WorstVerdict(verdicts), Reasons: reasons, Subjects: sortedUnique(subjects), Targets: []string{},
		Plan: *plan, FindingCount: len(found),
	}, found
}

// isSettledVerdict says that a unit with the verdict has nothing to report: a pass, or not applicable.
func isSettledVerdict(verdict conformance.RuleVerdict) bool {
	return verdict == conformance.VerdictPass || verdict == conformance.VerdictNotApplicable
}

// isReasonVerdict says that a unit with the verdict gives a reason and a finding: a violation or an unknown.
func isReasonVerdict(verdict conformance.RuleVerdict) bool {
	return verdict == conformance.VerdictViolation || verdict == conformance.VerdictUnknown
}

// summarizeUnits gives the verdicts of all units, the subjects of the units that apply, and the reasons of the units
// that are not a pass. The reasons are never nil.
func summarizeUnits(units []unit) (verdicts []conformance.RuleVerdict, subjects []string, reasons []conformance.Reason) {
	verdicts = make([]conformance.RuleVerdict, 0, len(units))
	reasons = []conformance.Reason{}

	for i := range units {
		item := &units[i]
		verdicts = append(verdicts, item.verdict)

		if item.verdict != conformance.VerdictNotApplicable {
			subjects = append(subjects, item.subject)
		}

		if isReasonVerdict(item.verdict) {
			reasons = append(reasons, conformance.Reason{Code: item.code, Message: item.message})
		}
	}

	return verdicts, subjects, reasons
}

// uniqueUnitFindings gives the findings of the units without duplicates, sorted by fingerprint.
func uniqueUnitFindings(units []unit) []conformance.Finding {
	var found []conformance.Finding

	seen := map[string]bool{}

	for i := range units {
		item := &units[i]
		if item.finding == nil || seen[item.finding.Fingerprint] {
			continue
		}

		seen[item.finding.Fingerprint] = true

		found = append(found, *item.finding)
	}

	slices.SortStableFunc(found, func(a, b conformance.Finding) int { return canonjson.CompareUTF16(a.Fingerprint, b.Fingerprint) })

	return found
}

// ruleOf evaluates one governance rule by its obligation (`decisions.obligation`).
func (e *engine) ruleOf(plan *policy.RulePlan, triggers []conformance.TriggerResult) (conformance.RuleResult, []conformance.Finding) {
	switch plan.Predicate.Decisions {
	case ObligationPresence:
		return assemble(plan, e.presenceUnits(plan), "no-decision", "no decision is registered")
	case ObligationApplicability:
		return assemble(plan, e.applicabilityUnits(plan), "no-decision", "no decision is registered")
	case ObligationJustification:
		return assemble(plan, e.justificationUnits(plan), "no-linked-rule", "no registered decision links a selected rule")
	case ObligationEnforcement:
		return assemble(plan, e.enforcementUnits(plan), "no-required-enforcement", "no registered decision requires enforcement")
	case ObligationHistory:
		return assemble(plan, e.historyUnits(plan), "no-decision", "no decision is registered")
	case ObligationScope:
		return assemble(plan, e.scopeUnits(plan, triggers), "no-trigger", "no trigger is configured")
	}

	return assemble(plan, nil, "unknown-obligation", "the obligation "+plan.Predicate.Decisions+" is not known")
}

// decisionLocation is the place of a decision: the ADR file and line when the provider reports the decision, the
// pinned file when it is tracked but not recognized, else the register file and line of the entry (design D7).
func decisionLocation(dec *Decision) *conformance.Location {
	if ev := dec.Presence.Decision; ev != nil {
		return &conformance.Location{Path: ev.Path, Line: ev.Line}
	}

	if dec.Presence.State == PresenceNotRecognized {
		return &conformance.Location{Path: dec.Presence.Path}
	}

	return &conformance.Location{Path: dec.Entry.File, Line: dec.Entry.Line}
}

// sourceOf is the source of the fingerprint of a decision: the register ID in canonical form, with the project
// when the entry is scoped to one, so that the same ID of two projects has two identities.
func sourceOf(dec *Decision) string {
	if dec.Entry.Project == "" {
		return dec.Entry.ID
	}

	return dec.Entry.ID + "@" + dec.Entry.Project
}

// ruleIDOf gives the rule ID of a rule ref `<rule>@<pack>/<version>`.
func ruleIDOf(ref string) string {
	id, _, _ := strings.Cut(ref, "@")

	return id
}

func ruleIDsOf(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ruleIDOf(ref))
	}

	return out
}

const expectedCommitted = "its ADR file is tracked in the evaluated revision and the indexed text equals the committed text"

// presenceUnits are the units of GOV-ADR-001: one per registered decision.
func (e *engine) presenceUnits(plan *policy.RulePlan) []unit {
	units := make([]unit, 0, len(e.decisions))

	for _, dec := range e.decisions {
		pres := &dec.Presence

		if pres.State == PresenceCommitted {
			units = append(units, unit{subject: dec.Entry.ID, verdict: conformance.VerdictPass})

			continue
		}

		verdict := conformance.VerdictUnknown

		switch pres.State {
		case PresenceIndexOnly, PresenceAbsent, PresencePathMismatch:
			verdict = conformance.VerdictViolation
		case PresenceCommitted, PresenceUncommittedEdit, PresenceNotRecognized, PresenceUnverifiable:
		}

		observed := presenceText(dec)
		finding := buildFinding(plan, &findingSpec{
			Result: verdict, Reason: pres.Reason, Source: sourceOf(dec), Subject: dec.Entry.ID,
			Message:  "decision " + dec.Entry.ID + ": " + observed,
			Expected: "decision " + dec.Entry.ID + " is committed: " + expectedCommitted,
			Observed: observed, Location: decisionLocation(dec), Decision: e.red.blockOf(dec, nil), Related: []string{dec.ID()},
		})
		units = append(units, unit{subject: dec.Entry.ID, verdict: verdict, code: pres.Reason, message: dec.Entry.ID + ": " + observed, finding: &finding})
	}

	return units
}

// presenceReasonText is the observed text of the presence reasons that name no path.
var presenceReasonText = map[string]string{
	EvidenceNoRevision:           "the project has no git revision, so presence cannot be verified",
	EvidenceNoCapability:         "the provider capability profile has no ADR evidence",
	EvidenceExtractionIncomplete: "ADR extraction coverage is incomplete, so the absence of the decision cannot be shown",
}

// presenceText says what the presence evidence of a decision shows.
func presenceText(dec *Decision) string {
	pres := &dec.Presence

	if text, ok := presenceReasonText[pres.Reason]; ok {
		return text
	}

	if text, ok := presencePathText(pres); ok {
		return text
	}

	return "presence is " + string(pres.State)
}

// presencePathText says what the presence evidence shows for the reasons that name paths.
func presencePathText(pres *PresenceResult) (string, bool) {
	switch pres.Reason {
	case string(ReasonIndexOnly):
		return "the ADR file " + pres.Path + " is in the index but not tracked in the evaluated revision", true
	case string(ReasonAbsent):
		return absentPresenceText(pres), true
	case string(ReasonPathMismatch):
		return "the register pins " + pres.Path + " but the committed ADR with this ID is at " + strings.Join(pres.OtherPaths, ", "), true
	case reasonUncommittedEdit:
		return "the ADR file " + pres.Path + " is tracked but edited in the working tree or stale in the index", true
	case reasonNotRecognized:
		return "the pinned file " + pres.Path + " is tracked but the provider reports no decision for it", true
	case string(ReasonDuplicateID):
		return "several ADR files carry this ID: " + strings.Join(pres.OtherPaths, ", "), true
	}

	return "", false
}

// absentPresenceText says that no committed ADR was found, at the pinned path when the entry has one.
func absentPresenceText(pres *PresenceResult) string {
	if pres.Path != "" {
		return "no committed ADR at the pinned path " + pres.Path
	}

	return "no committed ADR carries the ID"
}

// applicabilityUnits are the units of GOV-ADR-002: one per registered decision (replacement review).
func (e *engine) applicabilityUnits(plan *policy.RulePlan) []unit {
	results := EvaluateReplacement(e.decisions)
	units := make([]unit, 0, len(results))

	for i := range results {
		res := &results[i]
		dec := res.Decision

		if isSettledVerdict(res.Verdict) {
			units = append(units, unit{subject: dec.Entry.ID, verdict: res.Verdict})

			continue
		}

		observed, expected := replacementText(res)
		targets := slices.Concat(res.Subjects, ruleIDsOf(res.Rules), candidateIDs(res.Candidates))
		related := append([]string{dec.ID()}, candidateIDs(res.Candidates)...)

		if res.Reason == string(ReasonInactive) || res.Verdict == conformance.VerdictUnknown {
			targets = nil
		}

		finding := buildFinding(plan, &findingSpec{
			Result: res.Verdict, Reason: res.Reason, Source: sourceOf(dec), Targets: targets, Subject: dec.Entry.ID,
			Message: "decision " + dec.Entry.ID + ": " + observed, Expected: expected, Observed: observed,
			Location: decisionLocation(dec), Decision: e.red.blockOf(dec, res.Candidates), Related: related,
		})
		units = append(units, unit{subject: dec.Entry.ID, verdict: res.Verdict, code: res.Reason, message: dec.Entry.ID + ": " + observed, finding: &finding})
	}

	return units
}

func candidateIDs(list []conformance.CandidateSuccessor) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.ID)
	}

	return out
}

// replacementText gives the observed and the expected text of a replacement finding.
func replacementText(res *ReplacementResult) (observed, expected string) {
	dec := res.Decision
	expected = "decision " + dec.Entry.ID + " is the active decision for the subjects and rules it governs"
	status := dec.Entry.ID + " has status " + dec.statusText()

	switch res.Reason {
	case string(ReasonSuperseded):
		observed = "superseded by " + joinOrNone(candidateIDs(res.Candidates)) + " (committed, effective link): the subjects [" +
			strings.Join(res.Subjects, ", ") + "] and rules [" + strings.Join(res.Rules, ", ") +
			"] need a review; the successor does not receive them until the register names it"
	case string(ReasonInactive):
		observed = "its lifecycle is not active: " + status
	case string(ReasonHistoricalLink):
		expected = "every subject and rule of the historical decision " + dec.Entry.ID +
			" is released with a reason or declared by another active decision"
		observed = "untransferred subjects [" + strings.Join(res.Subjects, ", ") + "] and rules [" + strings.Join(res.Rules, ", ") + "]"
	default:
		observed = "applicability is unknown (" + res.Reason + ")"
	}

	return observed, expected
}

// statusText gives the status of the decision with its original text.
func (dec *Decision) statusText() string {
	ev := dec.Presence.Decision
	if ev == nil {
		return "unknown"
	}

	return fmt.Sprintf("%s (status text %q)", ev.Status, ev.StatusText)
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}

	return strings.Join(list, ", ")
}

// enforcementObservation gives the finding reason and the observed text of an enforcement unit that is not a pass.
func enforcementObservation(res *EnforcementResult) (reason, observed string) {
	if res.Reason == ReasonEnforcementGap {
		return string(res.Reason), "the decision requires enforcement and links no rule"
	}

	if res.Reason == ReasonAdvisoryOnly {
		return string(res.Reason), "the decision requires enforcement and links only advisory rules: " + strings.Join(res.Rules, ", ")
	}

	return res.Decision.Reason, "whether the decision is active is unknown (" + res.Decision.Reason + ")"
}

// enforcementUnits are the units of GOV-ADR-004: one per decision with `enforcement: required`.
func (e *engine) enforcementUnits(plan *policy.RulePlan) []unit {
	var units []unit

	for _, res := range EvaluateEnforcement(e.decisions, e.class) {
		if !res.Unit {
			continue
		}

		dec := res.Decision

		if isSettledVerdict(res.Verdict) {
			units = append(units, unit{subject: dec.Entry.ID, verdict: res.Verdict})

			continue
		}

		reason, observed := enforcementObservation(&res)

		var targets []string
		if res.Reason == ReasonAdvisoryOnly {
			targets = ruleIDsOf(res.Rules)
		}

		finding := buildFinding(plan, &findingSpec{
			Result: res.Verdict, Reason: reason, Source: sourceOf(dec), Targets: targets, Subject: dec.Entry.ID,
			Message: "decision " + dec.Entry.ID + ": " + observed, Expected: "the active decision " + dec.Entry.ID + " links at least one mandatory rule",
			Observed: observed, Location: decisionLocation(dec), Decision: e.red.blockOf(dec, nil), Related: []string{dec.ID()},
		})
		units = append(units, unit{subject: dec.Entry.ID, verdict: res.Verdict, code: reason, message: dec.Entry.ID + ": " + observed, finding: &finding})
	}

	return units
}
