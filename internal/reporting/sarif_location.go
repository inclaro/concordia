package reporting

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
)

// primaryLocation is the place of a finding: its own location (for a governance finding the ADR file and line, or
// the register entry), else the ADR file of its decision, else the first place of its evidence path (an
// edge location, else the file of a node). For an infrastructure finding that is the manifest, the Dockerfile,
// or the Terraform file, never a source file of the code that the finding does not come from.
func primaryLocation(run *conformance.Run, finding *conformance.Finding) *conformance.Location {
	if finding.Location != nil && relativePath(finding.Location.Path) {
		return finding.Location
	}

	if loc := decisionLocation(run, finding); loc != nil {
		return loc
	}

	if loc := evidenceLocation(finding); loc != nil {
		return loc
	}

	return renderedSourceLocation(run, finding)
}

// evidenceLocation is the first place of the evidence path of a finding: an edge location, else the file of a node.
func evidenceLocation(finding *conformance.Finding) *conformance.Location {
	if loc := edgeEvidenceLocation(finding); loc != nil {
		return loc
	}

	return nodeEvidenceLocation(finding)
}

// edgeEvidenceLocation is the location of the first edge of the evidence path that has one.
func edgeEvidenceLocation(finding *conformance.Finding) *conformance.Location {
	for i := range finding.EvidencePath {
		if edge := finding.EvidencePath[i].Edge; hasEdgeLocation(edge) {
			return edge.Location
		}
	}

	return nil
}

// hasEdgeLocation says whether an edge names the file that it comes from.
func hasEdgeLocation(edge *conformance.StepEdge) bool {
	return edge != nil && edge.Location != nil && edge.Location.Path != ""
}

// nodeEvidenceLocation is the file of the first node of the evidence path that has one.
func nodeEvidenceLocation(finding *conformance.Finding) *conformance.Location {
	for i := range finding.EvidencePath {
		if node := finding.EvidencePath[i].Node; node != nil && node.FilePath != "" {
			return &conformance.Location{Path: node.FilePath, Line: node.StartLine}
		}
	}

	return nil
}

// renderedSourceLocation is the declared source of the environment of a finding about rendered evidence, for a
// finding that has no file of its own (spec rendered-infrastructure-evidence, "Origin unknown"). It is the path that
// the workspace declares, never a temporary path of the renderer. A finding of any other rule (a review, a check
// result, a quality scenario) is not about the rendered documents, so it never gets this location.
func renderedSourceLocation(run *conformance.Run, finding *conformance.Finding) *conformance.Location {
	if !hasRenderedOrigin(run, finding) {
		return nil
	}

	for i := range run.Assurance.Rendered {
		if entry := &run.Assurance.Rendered[i]; entry.Environment == finding.Environment && relativePath(entry.Source) {
			return &conformance.Location{Path: strings.TrimPrefix(entry.Source, "./")}
		}
	}

	return nil
}

// hasRenderedOrigin says whether a finding is about the rendered documents of an environment of the run.
func hasRenderedOrigin(run *conformance.Run, finding *conformance.Finding) bool {
	if run.Assurance == nil || finding.Environment == "" {
		return false
	}

	return readsRenderedEvidence(run, finding.RuleID)
}

// readsRenderedEvidence says whether the rule of a finding reads rendered documents (conformance.ReadsRenderedEvidence).
func readsRenderedEvidence(run *conformance.Run, ruleID string) bool {
	for i := range run.Rules {
		if run.Rules[i].RuleID == ruleID {
			return conformance.ReadsRenderedEvidence(&run.Rules[i].Plan)
		}
	}

	return false
}

// relativePath says whether a path is relative and stays inside the repository: only such a path is a location.
func relativePath(p string) bool {
	return p != "" && !path.IsAbs(p) && !strings.Contains(p, ":") && !strings.HasPrefix(path.Clean(p), "..")
}

// uriOf is a repository-relative URI reference, percent-encoded for each segment as
// JavaScript encodeURIComponent does. It is never an absolute path.
func uriOf(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = encodeURIComponent(s)
	}

	return strings.Join(segs, "/")
}

func encodeURIComponent(s string) string {
	// QueryEscape differs from encodeURIComponent only for space and the marks below.
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")

	for _, c := range []string{"!", "'", "(", ")", "*"} {
		e = strings.ReplaceAll(e, "%"+fmt.Sprintf("%02X", c[0]), c)
	}

	return strings.ReplaceAll(e, "%7E", "~")
}

func physical(p string, line int) obj {
	o := obj{"artifactLocation": obj{"uri": uriOf(p), "uriBaseId": "SRCROOT"}}
	if line > 0 {
		o["region"] = obj{"startLine": line}
	}

	return o
}

func locationOf(l *conformance.Location) obj {
	return obj{"physicalLocation": physical(l.Path, l.Line)}
}

func threadFlowLocation(step *conformance.EvidenceStep) obj {
	if step.Node != nil {
		return nodeFlowLocation(step)
	}

	return edgeFlowLocation(step.Edge)
}

// nodeFlowLocation is the thread flow location of an observed node: its file, its qualified name, and a message.
func nodeFlowLocation(step *conformance.EvidenceStep) obj {
	node := step.Node
	loc := obj{}

	if node.FilePath != "" {
		loc["physicalLocation"] = physical(node.FilePath, node.StartLine)
	}

	if node.QualifiedName != "" {
		loc["logicalLocations"] = []any{obj{"fullyQualifiedName": node.QualifiedName, keyKind: node.Label}}
	}

	name := node.QualifiedName
	if name == "" {
		name = node.FilePath
	}

	loc[keyMessage] = obj{keyText: node.Label + " " + name}

	return obj{"location": loc}
}

// edgeFlowLocation is the thread flow location of an edge: its place when it has one, and its kind, confidence, and
// resolution method.
func edgeFlowLocation(edge *conformance.StepEdge) obj {
	loc := obj{keyMessage: obj{keyText: fmt.Sprintf("-[%s %s]->", edge.NativeKind, edge.Confidence)}}

	if edge.Location != nil {
		loc["physicalLocation"] = physical(edge.Location.Path, edge.Location.Line)
	}

	return obj{
		"location": loc,
		keyProperties: obj{
			keyKind: edge.Kind, "nativeKind": edge.NativeKind, "confidence": edge.Confidence, "resolutionMethod": edge.ResolutionMethod,
		},
	}
}
