package adr

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

// scopeUnits are the units of GOV-ADR-006: one per configured trigger.
func (e *engine) scopeUnits(plan *policy.RulePlan, triggers []conformance.TriggerResult) []unit {
	units := make([]unit, 0, len(triggers))

	for i := range triggers {
		units = append(units, e.scopeUnit(plan, &triggers[i]))
	}

	return units
}

// scopeUnit is the unit of one trigger: a finding for a violation or an unknown result.
func (e *engine) scopeUnit(plan *policy.RulePlan, trig *conformance.TriggerResult) unit {
	verdict := conformance.RuleVerdict(trig.Result)
	if !isReasonVerdict(verdict) {
		return unit{subject: trig.ID, verdict: verdict}
	}

	reason, observed := triggerReasonText(trig, verdict)
	finding := buildFinding(plan, &findingSpec{
		Result: verdict, Reason: reason, Source: trig.ID, Subject: trig.ID, Message: observed,
		Expected: "a registered decision with the category " + trig.Category + " is added or modified in the same change set",
		Observed: observed, Related: []string{}, Location: e.raisedLocation(trig),
	})

	return unit{subject: trig.ID, verdict: verdict, code: reason, message: observed, finding: &finding}
}

// triggerReasonText gives the reason code and the observed text of a trigger that is not a pass.
func triggerReasonText(trig *conformance.TriggerResult, verdict conformance.RuleVerdict) (reason, observed string) {
	if verdict == conformance.VerdictUnknown {
		return reasonTriggerUnknown, "trigger " + trig.ID + " cannot be evaluated: " + trig.Reason
	}

	return string(ReasonTriggerUnsatisfied),
		"trigger " + trig.ID + " (" + trig.Category + ") is raised by " + strings.Join(trig.RaisedBy, ", ") + " and no such decision changed"
}

// raisedLocation is where the trigger is raised: the first raising file that is in the change set, else the
// architecture file when an architecture change raises it (the model keeps no line positions); nil otherwise.
func (e *engine) raisedLocation(trig *conformance.TriggerResult) *conformance.Location {
	if e.input.Changed == nil {
		return nil
	}

	for _, p := range trig.RaisedBy {
		if slices.Contains(e.input.Changed.Files, p) {
			return &conformance.Location{Path: p}
		}
	}

	if e.input.Model != nil && slices.ContainsFunc(trig.RaisedBy, isArchitectureChange) {
		return &conformance.Location{Path: e.input.Model.SourceURI}
	}

	return nil
}

// isArchitectureChange reports whether a raisedBy entry is an architecture change (`<kind>:<id>`).
func isArchitectureChange(raised string) bool {
	kind, _, ok := strings.Cut(raised, ":")

	return ok && slices.Contains([]string{kindNodeAdded, kindNodeRemoved, kindRelationshipAdded, kindRelationshipRemoved}, kind)
}
