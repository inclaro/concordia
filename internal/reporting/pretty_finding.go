package reporting

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// shortName is the name of an observed node as a reader knows it: a file for a file or module node, else the qualified
// name without the module prefix that its file already says (`ShipmentStore.reserve`, not
// `services.fulfillment.src.internal.ShipmentStore.ShipmentStore.reserve`).
func shortName(node *observation.ObservedNode) string {
	if isFileLike(node) {
		return fileOrQualifiedName(node)
	}

	if rest, ok := moduleRelativeName(node); ok {
		return rest
	}

	return node.QualifiedName
}

// isFileLike says whether a node stands for a whole file: a file or module node, or one without a qualified name.
func isFileLike(node *observation.ObservedNode) bool {
	return node.Label == "File" || node.Label == "Module" || node.QualifiedName == ""
}

// fileOrQualifiedName is the file of a node, else its qualified name.
func fileOrQualifiedName(node *observation.ObservedNode) string {
	if node.FilePath != "" {
		return node.FilePath
	}

	return node.QualifiedName
}

// moduleRelativeName is the qualified name of a node without the module prefix that its file says. It reports false
// when the node has no file or its name does not start with the module of the file.
func moduleRelativeName(node *observation.ObservedNode) (string, bool) {
	if node.FilePath == "" {
		return "", false
	}

	stem := strings.TrimSuffix(node.FilePath, path.Ext(node.FilePath))

	rest, ok := strings.CutPrefix(node.QualifiedName, strings.ReplaceAll(stem, "/", ".")+".")
	if !ok || rest == "" {
		return "", false
	}

	// A class named like its file repeats the file name once more: ShipmentStore.ShipmentStore.reserve.
	base := path.Base(stem)
	if trimmed, found := strings.CutPrefix(rest, base+"."); found && strings.HasPrefix(trimmed, base) {
		rest = trimmed
	}

	return rest, true
}

// targetText names the target of an edge: the file for a file, else the short name and the file that holds it.
func targetText(node *observation.ObservedNode) string {
	name := shortName(node)
	if node.FilePath == "" || name == node.FilePath {
		return name
	}

	return name + " in " + node.FilePath
}

// evidenceActions turns an evidence path into what the code does, one action for each edge: `imports <file>`,
// `calls ShipmentStore.reserve in <file>`. A longer path (a cycle) names the node that takes each further step.
func evidenceActions(steps []conformance.EvidenceStep) []string {
	var out []string

	var from *observation.ObservedNode

	for i := 0; i < len(steps); i++ {
		step := steps[i]
		if step.Node != nil {
			from = step.Node

			continue
		}

		if step.Edge == nil {
			continue
		}

		target, next := edgeTarget(steps, i)
		if next != nil {
			i++
		}

		out = append(out, evidenceAction(len(out) > 0, from, strings.ToLower(step.Edge.NativeKind), target))

		if next != nil {
			from = next
		}
	}

	return out
}

// edgeTarget is the target text of the edge step at index i: the node that follows it when there is one, else the
// raw target of the edge. The node is nil when the edge has none.
func edgeTarget(steps []conformance.EvidenceStep, i int) (target string, next *observation.ObservedNode) {
	if i+1 < len(steps) && steps[i+1].Node != nil {
		return targetText(steps[i+1].Node), steps[i+1].Node
	}

	return steps[i].Edge.To, nil
}

// evidenceAction is one action of an evidence path. A further action names the node that takes the step.
func evidenceAction(further bool, from *observation.ObservedNode, verb, target string) string {
	action := verb + " " + target
	if further && from != nil {
		return "then " + shortName(from) + " " + action
	}

	return action
}

// evidenceDetail is the full evidence path of a finding for --verbose: each edge with its native kind and confidence.
func evidenceDetail(steps []conformance.EvidenceStep) []string {
	var out []string

	for i := range steps {
		if edge := steps[i].Edge; edge != nil {
			out = append(out, fmt.Sprintf("%s %s %s -> %s", edge.NativeKind, edge.Confidence, edge.From, edge.To))
		}
	}

	return out
}

func dispositionText(run *conformance.Run, finding *conformance.Finding) string {
	if finding.DispositionRef == nil {
		return string(finding.Disposition)
	}

	ref := *finding.DispositionRef
	if ref == "baseline" {
		return fmt.Sprintf("%s (baseline)", finding.Disposition)
	}

	for i := range run.Exceptions.Waivers {
		w := &run.Exceptions.Waivers[i]
		if w.ID != ref {
			continue
		}

		verb := "expires"
		if w.State == "expired" {
			verb = "expired"
		}

		return fmt.Sprintf("%s (%s, %s %s)", finding.Disposition, w.ID, verb, w.Expires)
	}

	return fmt.Sprintf("%s (%s)", finding.Disposition, ref)
}

// locationText is where a finding is: `path:line`, the path alone, or a note that there is none.
func locationText(finding *conformance.Finding) string {
	switch {
	case finding.Location == nil:
		return "(no source location)"
	case finding.Location.Line > 0:
		return fmt.Sprintf("%s:%d", finding.Location.Path, finding.Location.Line)
	}

	return finding.Location.Path
}

// statusSuffix marks a finding that is not simply active: waived, an expired waiver, or baselined (shown with
// --verbose). An active finding needs no mark: it is what the block is about.
func statusSuffix(run *conformance.Run, finding *conformance.Finding) string {
	if finding.Disposition == exceptions.DispositionActive {
		return ""
	}

	return "  [" + dispositionText(run, finding) + "]"
}

// endpointsText names the two ends of a finding: CALM element IDs, or what a review or quality finding is about.
func endpointsText(rule *conformance.RuleResult, finding *conformance.Finding) string {
	switch {
	case finding.Area == conformance.AreaAssessmentCurrency:
		// A review finding is about a required assessment and the assessment that should satisfy it, not CALM nodes.
		return "required " + finding.Source.ArchitectureID + ", assessment " + finding.Target.ArchitectureID
	case hasNoEnds(finding):
		return scopeText(rule)
	case finding.Target.ArchitectureID == "":
		return finding.Source.ArchitectureID
	}

	targets := strings.Split(finding.Target.ArchitectureID, ",")
	if isCycle(finding, targets) {
		return "cycle through " + strings.Join(targets, ", ")
	}

	return finding.Source.ArchitectureID + " -> " + strings.Join(targets, ", ")
}

// hasNoEnds says whether a finding has neither a source nor a target.
func hasNoEnds(finding *conformance.Finding) bool {
	return finding.Source.ArchitectureID == "" && finding.Target.ArchitectureID == ""
}

// scopeText is the scope of a quality scenario, which has no source and target: the CALM elements that the scenario
// names.
func scopeText(rule *conformance.RuleResult) string {
	if len(rule.Subjects) == 0 {
		return ""
	}

	return "scope " + strings.Join(rule.Subjects, ", ")
}

// isCycle says whether the targets of a finding are a cycle: it lists every element on it as a target, the source
// included.
func isCycle(finding *conformance.Finding, targets []string) bool {
	return len(targets) > 1 && slices.Contains(targets, finding.Source.ArchitectureID)
}

// findingGroup is the findings of one rule with the same two ends and the same next action.
type findingGroup struct {
	ends     string
	next     string
	findings []*conformance.Finding
}

func groupFindings(rule *conformance.RuleResult, findings []*conformance.Finding) []findingGroup {
	var groups []findingGroup

	index := map[string]int{}

	for _, finding := range findings {
		key := endpointsText(rule, finding) + "\x00" + finding.NextAction

		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i

			groups = append(groups, findingGroup{ends: endpointsText(rule, finding), next: finding.NextAction})
		}

		groups[i].findings = append(groups[i].findings, finding)
	}

	return groups
}

// findingLayout is how the findings of one group are shown: the family of their rule, the width of the longest
// location of the group, and whether the output is --verbose.
type findingLayout struct {
	family  policy.PredicateFamily
	width   int
	verbose bool
}

// findingLines are the lines of one finding inside its group: the location and what the code does, aligned on the
// width of the longest location of the group, then the lines that a finding of another kind carries (an effective
// access, a view, a decision), and with --verbose the fingerprint and the full evidence.
func findingLines(run *conformance.Run, finding *conformance.Finding, layout findingLayout) []string {
	lines := locationLines(run, finding, layout.width)
	lines = append(lines, kindLines(finding, layout.family)...)

	if layout.verbose {
		lines = append(lines, verboseLines(finding)...)
	}

	return lines
}

// locationLines are the location of a finding and the actions that the code does there, one action on each line.
func locationLines(run *conformance.Run, finding *conformance.Finding, width int) []string {
	loc := locationText(finding)

	actions := evidenceActions(finding.EvidencePath)
	if len(actions) == 0 {
		return []string{"    " + loc + statusSuffix(run, finding)}
	}

	pad := strings.Repeat(" ", width-len([]rune(loc))+2)
	lines := []string{"    " + loc + pad + actions[0] + statusSuffix(run, finding)}

	for _, action := range actions[1:] {
		lines = append(lines, "    "+strings.Repeat(" ", width+2)+action)
	}

	return lines
}

// kindLines are the lines that a finding of another kind carries: what the evaluator observed for an effective access
// finding, the view of a diagram finding, the decision of a governance finding.
func kindLines(finding *conformance.Finding, family policy.PredicateFamily) []string {
	if family == policy.FamilyEffectiveAccess {
		// The observed text states the access without the pair that the heading of the group already names:
		// `open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (... rule)`.
		return []string{"      " + finding.Observed}
	}

	if id := viewIDOf(finding); id != "" {
		return []string{"      view " + id}
	}

	if finding.Decision == nil {
		return nil
	}

	block := decisionBlockLines(finding.Decision)
	lines := make([]string, 0, len(block))

	for _, line := range block {
		lines = append(lines, "      "+line)
	}

	return lines
}

// verboseLines are the fingerprint of a finding and its full evidence.
func verboseLines(finding *conformance.Finding) []string {
	details := evidenceDetail(finding.EvidencePath)
	lines := make([]string, 0, 1+len(details))
	lines = append(lines, "      fingerprint "+finding.Fingerprint)

	for _, detail := range details {
		lines = append(lines, "      evidence "+detail)
	}

	return lines
}
