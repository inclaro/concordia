package reporting

import (
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
)

const (
	keyText       = "text"
	keyKind       = "kind"
	keyMessage    = "message"
	keyProperties = "properties"
	keyLevel      = "level"
	keyID         = "id"
)

const sarifSchema = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"

// obj is a JSON object. canonjson sorts the keys.
type obj = map[string]any

// sanitizeTree replaces the control characters in every string of the document by a visible
// escape (redact.Sanitize): rule names, messages, and paths come from repository content and
// provider output, and a SARIF viewer may print them to a terminal.
func sanitizeTree(node any) any {
	switch val := node.(type) {
	case string:
		return redact.Sanitize(val)
	case obj:
		for key, item := range val {
			val[key] = sanitizeTree(item)
		}

		return val
	case []any:
		for i, item := range val {
			val[i] = sanitizeTree(item)
		}

		return val
	default:
		return node
	}
}

// sarifLevelFor gives the level of a result: the severity of the rule for a result that gates, `note` for a
// result of enforcement `advisory` (it is shown, and never blocks).
func sarifLevelFor(severity policy.RuleSeverity, mode policy.Enforcement) string {
	if mode == policy.EnforcementAdvisory {
		return sarifNote
	}

	return sarifLevel(severity)
}

// SARIF result levels.
const (
	sarifError   = "error"
	sarifWarning = "warning"
	sarifNote    = "note"
)

func sarifLevel(s policy.RuleSeverity) string {
	switch s {
	case policy.SeverityError:
		return sarifError
	case policy.SeverityWarning:
		return sarifWarning
	case policy.SeverityInfo:
		return sarifNote
	}

	return sarifNote
}

func descriptorOf(rule *conformance.RuleResult) obj {
	desc := obj{
		keyID:                  rule.RuleID,
		"name":                 rule.Name,
		"shortDescription":     obj{keyText: rule.Name},
		"defaultConfiguration": obj{keyLevel: sarifLevelFor(rule.Severity, rule.Enforcement)},
		keyProperties:          obj{"family": rule.Family, "policy": rule.Policy.ID + "@" + rule.Policy.Version},
	}

	if rule.Plan.Rationale != "" {
		desc["fullDescription"] = obj{keyText: rule.Plan.Rationale}
	}

	if rule.Plan.Remediation != "" {
		desc["help"] = obj{keyText: rule.Plan.Remediation}
	}

	return desc
}

// notifies says whether a rule is a tool notification: a visible rule that is unknown or errors.
func notifies(rule *conformance.RuleResult) bool {
	if !conformance.RuleVisible(rule) {
		return false
	}

	return rule.Verdict == conformance.VerdictUnknown || rule.Verdict == conformance.VerdictError
}

func notificationsOf(run *conformance.Run) []any {
	var out []any

	for i := range run.Rules {
		rule := &run.Rules[i]
		if !notifies(rule) {
			continue
		}

		level := sarifWarning
		if rule.Verdict == conformance.VerdictError {
			level = sarifError
		}

		text := strings.Join(reasonLines(rule), "; ")
		if text == "" {
			text = string(rule.Verdict)
		}

		out = append(out, obj{keyLevel: level, keyMessage: obj{keyText: text}, "descriptor": obj{keyID: rule.RuleID}})
	}

	return out
}

// ruleDescriptors are the reporting descriptors of the visible rules, and the index of the first descriptor of each
// rule ID.
func ruleDescriptors(run *conformance.Run) (descriptors []any, index map[string]int) {
	index = map[string]int{}
	descriptors = make([]any, 0, len(run.Rules))

	for i := range run.Rules {
		if !conformance.RuleVisible(&run.Rules[i]) {
			continue
		}

		if _, seen := index[run.Rules[i].RuleID]; !seen {
			index[run.Rules[i].RuleID] = len(descriptors)
		}

		descriptors = append(descriptors, descriptorOf(&run.Rules[i]))
	}

	return descriptors, index
}

// findingResults are the results of the visible findings. A finding of a rule with no descriptor has rule index -1.
func findingResults(run *conformance.Run, index map[string]int) []any {
	results := make([]any, 0, len(run.Findings))

	for i := range run.Findings {
		if !conformance.Visible(&run.Findings[i]) {
			continue
		}

		idx, ok := index[run.Findings[i].RuleID]
		if !ok {
			idx = -1
		}

		results = append(results, resultOf(run, &run.Findings[i], idx))
	}

	return results
}

// invocationOf describes the run as a tool invocation, with the unknown and erroring rules as notifications.
func invocationOf(run *conformance.Run) obj {
	invocation := obj{
		"executionSuccessful": run.Summary.ExitCode != diag.Internal,
		"exitCode":            run.Summary.ExitCode,
		"startTimeUtc":        run.Run.StartedAt,
		"endTimeUtc":          run.Run.FinishedAt,
	}

	if notes := notificationsOf(run); len(notes) > 0 {
		invocation["toolExecutionNotifications"] = notes
	}

	return invocation
}

// runPropertiesOf are the properties of the SARIF run: the project, the graph, and the summaries of the areas that
// the run has.
func runPropertiesOf(run *conformance.Run) obj {
	props := obj{
		"project":         run.Run.Project,
		"graphGeneration": run.Run.GraphGeneration,
		"queryPlanDigest": run.Inputs.QueryPlanDigest,
	}

	if run.Run.SourceRevision != nil {
		props["sourceRevision"] = *run.Run.SourceRevision
	}

	if len(run.Views) > 0 {
		props["diagramCurrency"] = viewsProperty(run)
	}

	if run.DecisionSummary != nil {
		props["adrGovernance"] = decisionsProperty(run)
	}

	if run.AssessmentCurrency != nil {
		props["assessmentCurrency"] = reviewProperty(run)
	}

	return props
}

// RenderSARIF renders SARIF 2.1.0: one reporting descriptor for each rule, one result for each
// finding. Paths are repository-relative with `uriBaseId: "SRCROOT"`; `originalUriBaseIds` is not
// written because it would need an absolute path. `tool.driver.version` is run.Toolchain.Concordia.
func RenderSARIF(run *conformance.Run) ([]byte, error) {
	descriptors, index := ruleDescriptors(run)

	doc := obj{
		"$schema": sarifSchema,
		"version": "2.1.0",
		"runs": []any{obj{
			"tool":        obj{"driver": obj{"name": "Concordia", "version": run.Toolchain.Concordia, "rules": descriptors}},
			"invocations": []any{invocationOf(run)},
			"results":     findingResults(run, index),
			keyProperties: runPropertiesOf(run),
		}},
	}

	out, err := canonjson.Marshal(sanitizeTree(doc))
	if err != nil {
		return nil, diag.Fail(diag.REP002, diag.Detail(err.Error()))
	}

	return out, nil
}
