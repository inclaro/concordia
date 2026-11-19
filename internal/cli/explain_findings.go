package cli

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/reporting"
)

const (
	infrastructureRulePrefix = "INFRA-"
	lineNumberWidth          = 4
	findingLinesCap          = 16
)

func locationText(l *conformance.Location) string {
	if l.Line > 0 {
		return l.Path + ":" + strconv.Itoa(l.Line)
	}

	return l.Path
}

func stepText(step *conformance.EvidenceStep) string {
	if step.Node != nil {
		return nodeStepText(step.Node)
	}

	if step.Edge == nil {
		return ""
	}

	e := step.Edge

	where := ""
	if e.Location != nil {
		where = " at " + locationText(e.Location)
	}

	return fmt.Sprintf("-[%s %s, %s]->%s", e.NativeKind, e.Confidence, e.ResolutionMethod, where)
}

// nodeStepText shows a node of the evidence path: label, name, file and line, and the scoped identity.
func nodeStepText(node *observation.ObservedNode) string {
	where := ""
	if node.FilePath != "" {
		where = " (" + node.FilePath
		if node.StartLine > 0 {
			where += ":" + strconv.Itoa(node.StartLine)
		}

		where += ")"
	}

	text := node.Label + " " + cmp.Or(node.QualifiedName, node.FilePath) + where
	if node.ScopedID != "" {
		text += " [" + node.ScopedID + "]"
	}

	return text
}

// identityLines shows the identity of an edge step: the edge key, the environment, the occurrence,
// the image reference, and the lossy mark with the loss mode that the profile states.
func identityLines(run *conformance.Run, step *conformance.EvidenceStep) []string {
	if step.Edge == nil || step.Edge.Identity == nil {
		return nil
	}

	identity := step.Edge.Identity
	lines := []string{"      edge key: " + identity.Key}

	for _, field := range [][2]string{
		{"environment", identity.Environment}, {"occurrence", identity.Occurrence}, {"image", identity.Image},
	} {
		if field[1] != "" {
			lines = append(lines, "      "+field[0]+": "+field[1])
		}
	}

	if identity.PossiblyLossy {
		lines = append(lines, "      "+lossyText(run, identity))
	}

	return lines
}

// lossyText says that the provider may have merged the fact, with the loss modes that the profile states.
func lossyText(run *conformance.Run, identity *observation.EdgeIdentity) string {
	limit := "possibly lossy: the provider may have merged this fact with others"
	if len(identity.LossModes) == 0 {
		return limit
	}

	limit += "; profile loss mode " + strings.Join(identity.LossModes, ", ")
	if p := run.Toolchain.Cbm.Profile; p.Applies {
		limit += " (" + p.ID + ")"
	}

	return limit
}

func dispositionRef(run *conformance.Run, f *conformance.Finding) string {
	if f.DispositionRef == nil {
		return ""
	}

	ref := *f.DispositionRef

	for i := range run.Exceptions.Waivers {
		w := &run.Exceptions.Waivers[i]
		if w.ID != ref {
			continue
		}

		verb := "expires"
		if w.State == "expired" {
			verb = "expired"
		}

		return fmt.Sprintf(" (%s: %s; %s %s; owner %s)", w.ID, w.Reason, verb, w.Expires, w.Owner)
	}

	return " (" + ref + ")"
}

// findingText gives the pretty lines of the findings of one run.
type findingText struct {
	run        *conformance.Run
	withSource bool
	allPaths   bool
}

func (t *findingText) lines(finding *conformance.Finding, decision *decisionExplanation, contexts []sourceContext) []string {
	lines := t.headLines(finding)
	lines = append(lines, t.evidenceLines(finding)...)
	lines = append(lines, crossingLines(findingCrossings(t.run, finding))...)

	if decision != nil {
		lines = append(lines, decision.lines...)
		lines = append(lines, candidateLines(decision.decision)...)
	}

	if t.withSource {
		lines = append(lines, sourceLines(contexts)...)
	}

	return lines
}

// headLines gives the fingerprint, the kind, the endpoints, the owner, the disposition, and the location.
func (t *findingText) headLines(finding *conformance.Finding) []string {
	owner := "unassigned"
	if finding.Owner != nil {
		owner = *finding.Owner
	}

	lines := make([]string, 0, findingLinesCap)
	lines = append(lines,
		"", "Finding "+reporting.ShortFingerprint(finding.Fingerprint),
		"  Fingerprint: "+finding.Fingerprint,
		"  Kind: "+string(finding.Kind),
		"  Message: "+finding.Message,
		"  "+endpointLine("Source", finding, finding.Source.ArchitectureID),
		"  "+endpointLine("Target", finding, finding.Target.ArchitectureID),
		"  Owner: "+owner,
		"  Disposition: "+string(finding.Disposition)+dispositionRef(t.run, finding),
	)

	if finding.Location != nil {
		lines = append(lines, "  Location: "+locationText(finding.Location))
	}

	return lines
}

// evidenceLines gives the evidence path and the related locations.
func (t *findingText) evidenceLines(finding *conformance.Finding) []string {
	var lines []string

	if len(finding.EvidencePath) > 0 {
		lines = append(lines, "  Evidence path:")
		for i := range finding.EvidencePath {
			lines = append(lines, "    "+stepText(&finding.EvidencePath[i]))
			lines = append(lines, identityLines(t.run, &finding.EvidencePath[i])...)
		}
	}

	if len(finding.RelatedLocations) == 0 {
		return lines
	}

	header := "  Related locations:"
	if t.allPaths {
		header = "  Related evidence:"
	}

	lines = append(lines, header)
	for i := range finding.RelatedLocations {
		lines = append(lines, "    "+locationText(&finding.RelatedLocations[i]))
	}

	return lines
}

// identityLine gives the scoped identity of the first node of the evidence path that has one. With none, it gives the
// key of the first edge that has an identity, labeled as the edge key: that key is not a scoped identity.
func identityLine(path []conformance.EvidenceStep) string {
	if id, ok := firstScopedID(path); ok {
		return "  Scoped identity: " + id
	}

	if key, ok := firstEdgeKey(path); ok {
		return "  Edge key: " + key
	}

	return ""
}

// firstScopedID gives the scoped identity of the first node of the path that has one.
func firstScopedID(path []conformance.EvidenceStep) (string, bool) {
	for i := range path {
		if node := path[i].Node; node != nil && node.ScopedID != "" {
			return node.ScopedID, true
		}
	}

	return "", false
}

// firstEdgeKey gives the key of the first edge of the path that has an identity.
func firstEdgeKey(path []conformance.EvidenceStep) (string, bool) {
	for i := range path {
		if edge := path[i].Edge; edge != nil && edge.Identity != nil {
			return edge.Identity.Key, true
		}
	}

	return "", false
}

// traceExcerptLines gives the identity lines and the deployment trace excerpt of the subject of an `infrastructure`
// finding: the same block that `concordia trace` prints for the subject and the environment of the finding.
func traceExcerptLines(run *conformance.Run, finding *conformance.Finding) []string {
	if !strings.HasPrefix(finding.RuleID, infrastructureRulePrefix) {
		return nil
	}

	lines := []string{"  Environment: " + cmp.Or(finding.Environment, "none")}

	if line := identityLine(finding.EvidencePath); line != "" {
		lines = append(lines, line)
	}

	if finding.Occurrence != "" {
		lines = append(lines, "  Occurrence: "+finding.Occurrence)
	}

	sub := run.DeploymentTrace.Subject(finding.Subject, finding.Environment)
	if sub == nil {
		return append(lines, "  Trace: none for "+cmp.Or(finding.Subject, "no subject")+" in "+cmp.Or(finding.Environment, "no environment"))
	}

	var text strings.Builder
	if err := deployment.RenderSubject(&text, traceEnvironment(run, finding.Environment), sub, deployment.RenderOptions{}); err != nil {
		return lines
	}

	lines = append(lines, "  Trace excerpt:")
	for line := range strings.SplitSeq(strings.TrimSuffix(text.String(), "\n"), "\n") {
		lines = append(lines, "    "+line)
	}

	return lines
}

// traceEnvironment gives the last environment of the deployment trace with the name, else nil.
func traceEnvironment(run *conformance.Run, name string) *deployment.EnvironmentTrace {
	var env *deployment.EnvironmentTrace

	for i := range run.DeploymentTrace.Environments {
		if run.DeploymentTrace.Environments[i].Name == name {
			env = &run.DeploymentTrace.Environments[i]
		}
	}

	return env
}

func sourceLines(contexts []sourceContext) []string {
	lines := []string{"  Source:"}
	if len(contexts) == 0 {
		lines = append(lines, "    (not available in the working tree)")
	}

	for i := range contexts {
		c := &contexts[i]
		lines = append(lines, "    "+locationText(&c.Location))

		for _, l := range c.Lines {
			marker := " "
			if l.Line == c.Location.Line {
				marker = ">"
			}

			lines = append(lines, fmt.Sprintf("    %s %*d | %s", marker, lineNumberWidth, l.Line, l.Text))
		}
	}

	return lines
}
