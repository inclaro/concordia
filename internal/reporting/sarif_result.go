package reporting

import (
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
)

func resultOf(run *conformance.Run, finding *conformance.Finding, ruleIndex int) obj {
	result := obj{
		"ruleId":              finding.RuleID,
		"ruleIndex":           ruleIndex,
		keyLevel:              sarifLevelFor(finding.Severity, finding.Enforcement),
		keyMessage:            obj{keyText: finding.Message},
		"partialFingerprints": obj{"concordia/v1": finding.Fingerprint},
		"baselineState":       baselineStateOf(finding),
		keyProperties:         resultProperties(finding),
	}

	if loc := primaryLocation(run, finding); loc != nil {
		result["locations"] = []any{locationOf(loc)}
	}

	if related := relatedLocationsOf(finding); related != nil {
		result["relatedLocations"] = related
	}

	if flows := codeFlowsOf(finding); flows != nil {
		result["codeFlows"] = flows
	}

	if suppressions := suppressionsOf(run, finding); suppressions != nil {
		result["suppressions"] = suppressions
	}

	return result
}

// baselineStateOf is `unchanged` for a baselined finding, else `new`.
func baselineStateOf(finding *conformance.Finding) string {
	if finding.Disposition == exceptions.DispositionBaselined {
		return "unchanged"
	}

	return "new"
}

// resultProperties are the properties of a result: the two ends of the finding, its kind and disposition, and the
// others that it has.
func resultProperties(finding *conformance.Finding) obj {
	props := obj{
		"sourceArchitectureId": finding.Source.ArchitectureID,
		"targetArchitectureId": finding.Target.ArchitectureID,
		"findingKind":          finding.Kind,
		"disposition":          finding.Disposition,
	}

	addReferenceProperties(props, finding)
	addKindProperties(props, finding)
	addSubjectProperties(props, finding)

	return props
}

// addReferenceProperties adds the disposition reference, the owner, and the confidence of the finding edge, for each
// that the finding has.
func addReferenceProperties(props obj, finding *conformance.Finding) {
	if finding.DispositionRef != nil {
		props["dispositionRef"] = *finding.DispositionRef
	}

	if finding.Owner != nil {
		props["owner"] = *finding.Owner
	}

	if finding.Edge != nil {
		props["confidence"] = finding.Edge.Confidence
	}
}

// addKindProperties adds the next action, area, view, decision, and enforcement of the finding, for each that it has.
func addKindProperties(props obj, finding *conformance.Finding) {
	if finding.NextAction != "" {
		props["nextAction"] = finding.NextAction
	}

	if finding.Area != "" {
		props["area"] = finding.Area
	}

	if id := viewIDOf(finding); id != "" {
		props["viewId"] = id
	}

	if finding.Decision != nil {
		props["decisionId"] = finding.Decision.ID
	}

	if finding.Enforcement != "" {
		props["enforcement"] = finding.Enforcement
	}
}

// addSubjectProperties adds the obligation, subject, environment, result, and reason of the finding, for each that it
// has.
func addSubjectProperties(props obj, finding *conformance.Finding) {
	for name, value := range map[string]string{
		"obligationId": finding.ObligationID, "subject": finding.Subject, "environment": finding.Environment,
		"result": string(finding.Result), "reason": string(finding.Reason),
	} {
		if value != "" {
			props[name] = value
		}
	}
}

// relatedLocationsOf are the related locations of a finding, numbered in order. nil when it has none.
func relatedLocationsOf(finding *conformance.Finding) []any {
	if len(finding.RelatedLocations) == 0 {
		return nil
	}

	related := make([]any, 0, len(finding.RelatedLocations))

	for i := range finding.RelatedLocations {
		l := locationOf(&finding.RelatedLocations[i])
		l[keyID] = i
		related = append(related, l)
	}

	return related
}

// codeFlowsOf is the evidence path of a finding as one thread flow. nil when the finding has no evidence path.
func codeFlowsOf(finding *conformance.Finding) []any {
	if len(finding.EvidencePath) == 0 {
		return nil
	}

	locs := make([]any, 0, len(finding.EvidencePath))
	for i := range finding.EvidencePath {
		locs = append(locs, threadFlowLocation(&finding.EvidencePath[i]))
	}

	return []any{obj{"threadFlows": []any{obj{"locations": locs}}}}
}

// suppressionsOf is the suppression of a baselined or waived finding. nil for the other findings.
func suppressionsOf(run *conformance.Run, finding *conformance.Finding) []any {
	switch finding.Disposition {
	case exceptions.DispositionBaselined:
		return []any{obj{keyKind: "external", keyStatus: "accepted", "justification": "baseline"}}
	case exceptions.DispositionWaived:
		return []any{obj{keyKind: "external", keyStatus: "accepted", "justification": waiverJustification(run, finding)}}
	case exceptions.DispositionActive, exceptions.DispositionExpiredWaiver:
	}

	return nil
}

func waiverJustification(run *conformance.Run, f *conformance.Finding) string {
	ref := ""
	if f.DispositionRef != nil {
		ref = *f.DispositionRef
	}

	for i := range run.Exceptions.Waivers {
		if w := &run.Exceptions.Waivers[i]; w.ID == ref && w.Reason != "" {
			return w.Reason
		}
	}

	if ref != "" {
		return ref
	}

	return "waiver"
}
