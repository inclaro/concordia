package observation

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Labels of the synthesized supporting entities. The provider has no node for
// a CI task or a secret reference; for the other kinds it has a node whose id
// is not stable (an ordinal in a diagram name) or has no file (an image).
const (
	// LabelImage is the label of an image entity: one for each repository.
	LabelImage = "Image"
	// LabelCITask is the label of a CI task entity: one CI job in one file.
	LabelCITask = "CITask"
	// LabelDecision is the label of a decision entity: one `ADR` node.
	LabelDecision = "ADR"
	// LabelSecretReference is the label of a secret reference entity.
	LabelSecretReference = "SecretReference"
	// LabelDiagram is the label of a diagram block node, which has the `diagram_kind` and `format` of the block.
	LabelDiagram = "Diagram"
	// LabelDiagramElement is the label of a diagram element entity.
	LabelDiagramElement = "DiagramElement"
	// LabelNamespace is the label of a Namespace entity: the hosting target of a
	// placement (see DerivePlacement). It is the label of a synthesized
	// entity when no manifest of the environment declares the Namespace.
	LabelNamespace = "Namespace"

	synthCITaskPrefix = synthPrefix + "ci-task:"
	synthSecretPrefix = synthPrefix + "secret:"

	imageNodePrefix    = "__image__"
	diagramQNMarker    = "#diagram-"
	sourceSecretKeyRef = "secretKeyRef"
)

// SynthesizeEntities makes the supporting entities of an evidence result:
// images, CI tasks, decisions, secret references, and diagram elements. They
// come from the endpoints and properties of the edges, as ObservedNode values
// with an id that starts with `synth:` (see SynthPrefix) and resolution
// `unbound`: no binding refers to them until one does. A synthesized entity
// keeps the source location of its first occurrence (smallest path and line)
// and the provider properties of its node verbatim. Nothing in an entity is a
// secret value: a secret reference has its reference mode and key source only.
// The result is sorted by observed ID.
func SynthesizeEntities(nodes map[string]ObservedNode, edges []*EvidenceEdge) []ObservedNode {
	builder := &entityBuilder{byID: map[string]*ObservedNode{}, nodes: nodes}

	for _, edge := range edges {
		from, to := builder.endpoint(edge.From), builder.endpoint(edge.To)

		builder.imageOf(edge, from)
		builder.imageOf(edge, to)
		builder.decisionOf(from)
		builder.decisionOf(to)
		builder.diagramElementOf(from)
		builder.diagramElementOf(to)
		builder.ciTaskOf(edge)
		builder.secretReferenceOf(edge, to)
	}

	out := make([]ObservedNode, 0, len(builder.byID))
	for _, n := range builder.byID {
		out = append(out, *n)
	}

	slices.SortFunc(out, func(left, right ObservedNode) int { return canonjson.CompareUTF16(left.ObservedID, right.ObservedID) })

	return out
}

// entityBuilder collects entities by id and keeps the first occurrence of each.
type entityBuilder struct {
	byID  map[string]*ObservedNode
	nodes map[string]ObservedNode
}

func (builder *entityBuilder) endpoint(id string) *ObservedNode {
	n, ok := builder.nodes[id]
	if !ok {
		return nil
	}

	return &n
}

// add keeps the entity, or the earlier location when it exists: the entity
// with the smallest (path, line) is the first occurrence.
func (builder *entityBuilder) add(entity *ObservedNode) {
	entity.Resolution = ResolutionUnbound

	known, ok := builder.byID[entity.ObservedID]
	if !ok {
		builder.byID[entity.ObservedID] = entity

		return
	}

	if replacesLocation(known, entity) {
		known.FilePath, known.StartLine = entity.FilePath, entity.StartLine
	}
}

// replacesLocation reports whether the location of entity is the first
// occurrence, compared to the one of known.
func replacesLocation(known, entity *ObservedNode) bool {
	if known.FilePath == "" {
		return true
	}

	return entity.FilePath != "" && compareLocation(entity, known) < 0
}

func compareLocation(left, right *ObservedNode) int {
	if c := cmp.Compare(left.FilePath, right.FilePath); c != 0 {
		return c
	}

	return cmp.Compare(left.StartLine, right.StartLine)
}

// location gives the place of an edge: its source span, else the file of the
// endpoint that is not the entity itself.
func location(edge *EvidenceEdge, others ...*ObservedNode) (file string, line int) {
	if edge.SourceSpan != nil && edge.SourceSpan.Path != "" {
		return edge.SourceSpan.Path, edge.SourceSpan.Line
	}

	for _, n := range others {
		if n != nil && n.FilePath != "" {
			return n.FilePath, n.StartLine
		}
	}

	return "", 0
}

// imageOf makes the image entity of an `Image` endpoint. The node is one for each
// repository, has no tag or digest, and has no file, so the location is the
// place of the edge that refers to it (O3).
func (builder *entityBuilder) imageOf(edge *EvidenceEdge, node *ObservedNode) {
	if node == nil || node.Label != LabelImage {
		return
	}

	repository := strings.TrimPrefix(node.QualifiedName, imageNodePrefix)
	if repository == "" {
		return
	}

	other := builder.endpoint(edge.From)
	if other != nil && other.ObservedID == node.ObservedID {
		other = builder.endpoint(edge.To)
	}

	file, line := location(edge, other)

	builder.add(&ObservedNode{
		ObservedID:    ImageSynthID(repository),
		Label:         LabelImage,
		QualifiedName: repository,
		FilePath:      file,
		StartLine:     line,
		Properties:    map[string]string{"repository": repository},
	})
}

// decisionOf makes the decision entity of an `ADR` endpoint. The identity is
// the repository-relative file path; `adr_id` and `superseded_by` are kept as
// the provider emitted them and are never an identity (O12).
func (builder *entityBuilder) decisionOf(node *ObservedNode) {
	if !isFileNodeOf(node, LabelDecision) {
		return
	}

	builder.add(&ObservedNode{
		ObservedID:    ADRSynthID(node.FilePath),
		Label:         LabelDecision,
		QualifiedName: node.QualifiedName,
		FilePath:      node.FilePath,
		StartLine:     node.StartLine,
		Properties:    maps.Clone(node.Properties),
	})
}

// isFileNodeOf reports whether node is a node of label that has a file.
func isFileNodeOf(node *ObservedNode, label string) bool {
	return node != nil && node.Label == label && node.FilePath != ""
}

// diagramElementOf makes the entity of a `DiagramElement` endpoint. The
// identity is the diagram file, the section, and the author's `element_id`; the
// ordinal in the provider qualified name (`#diagram-<ordinal>`) changes when
// blocks are reordered, so it is not used.
func (builder *entityBuilder) diagramElementOf(node *ObservedNode) {
	if !isFileNodeOf(node, LabelDiagramElement) {
		return
	}

	elementID := node.Properties["element_id"]
	if elementID == "" {
		elementID = elementIDFromQualifiedName(node.QualifiedName)
	}

	if elementID == "" {
		return
	}

	section := diagramSection(node.QualifiedName, node.FilePath)
	props := maps.Clone(node.Properties)

	if props == nil {
		props = map[string]string{}
	}

	props["element_id"] = elementID

	builder.add(&ObservedNode{
		ObservedID:    DiagramElementSynthID(node.FilePath, section, elementID),
		Label:         LabelDiagramElement,
		QualifiedName: node.QualifiedName,
		FilePath:      node.FilePath,
		StartLine:     node.StartLine,
		Properties:    props,
	})
}

// diagramSection gives the section part of a diagram qualified name
// `<file path with dots>.<section>#diagram-<ordinal>.<element id>`, or "".
func diagramSection(qualifiedName, filePath string) string {
	head, _, ok := strings.Cut(qualifiedName, diagramQNMarker)
	if !ok {
		return ""
	}

	prefix := strings.ReplaceAll(strings.TrimSuffix(filePath, path.Ext(filePath)), "/", ".") + "."

	return strings.TrimPrefix(head, prefix)
}

// elementIDFromQualifiedName gives the element id after the ordinal of
// `...#diagram-<ordinal>.<element id>`, or "" when the name has no such part.
func elementIDFromQualifiedName(qualifiedName string) string {
	_, tail, ok := strings.Cut(qualifiedName, diagramQNMarker)
	if !ok {
		return ""
	}

	ordinal, id, found := strings.Cut(tail, ".")
	if _, err := strconv.Atoi(ordinal); err != nil || !found {
		return ""
	}

	return id
}

// ciTaskOf makes the entity of a CI job from a launching edge that names
// `ci` and `job`. The provider has no node for a job.
func (builder *entityBuilder) ciTaskOf(edge *EvidenceEdge) {
	if edge.NativeKind != "SPAWNS" || edge.Kind != KindLaunches {
		return
	}

	system, job := edge.Properties["ci"], edge.Properties["job"]
	if !hasBoth(system, job) || edge.SourceSpan == nil {
		return
	}

	builder.add(&ObservedNode{
		ObservedID:    synthCITaskPrefix + system + "/" + job + "@" + edge.SourceSpan.Path,
		Label:         LabelCITask,
		QualifiedName: system + "/" + job,
		FilePath:      edge.SourceSpan.Path,
		StartLine:     edge.SourceSpan.Line,
		Properties:    map[string]string{"ci": system, "job": job},
	})
}

func hasBoth(first, second string) bool {
	return first != "" && second != ""
}

// secretReferenceOf makes the entity of a reference to a secret: a workload or
// Ingress that refers to a Secret, or an environment variable that takes its
// value from a `secretKeyRef`. The entity keeps the reference mode or the key
// source and never a value.
func (builder *entityBuilder) secretReferenceOf(edge *EvidenceEdge, target *ObservedNode) {
	if target == nil {
		return
	}

	props, ok := secretReferenceProps(edge)
	if !ok {
		return
	}

	file, line := target.FilePath, target.StartLine
	if span, ok := edgeSpanLocation(edge); ok {
		file, line = span.Path, span.Line
	}

	builder.add(&ObservedNode{
		ObservedID:    synthSecretPrefix + target.QualifiedName,
		Label:         LabelSecretReference,
		QualifiedName: target.QualifiedName,
		FilePath:      file,
		StartLine:     line,
		Properties:    props,
	})
}

// edgeSpanLocation gives the source span of an edge when it names a file.
func edgeSpanLocation(edge *EvidenceEdge) (*SourceSpan, bool) {
	if edge.SourceSpan == nil || edge.SourceSpan.Path == "" {
		return nil, false
	}

	return edge.SourceSpan, true
}

// secretReferenceProps gives the properties of the secret reference an edge
// makes, or false when the edge is not a reference to a secret.
func secretReferenceProps(edge *EvidenceEdge) (map[string]string, bool) {
	if edge.Kind == KindReferencesSecret || edge.Kind == KindReferencesCertificateSecret {
		return map[string]string{"referenceMode": edge.Facets["referenceMode"]}, true
	}

	if edge.Kind == KindConfiguresEnvSet {
		return envSecretKeyRefProps(edge)
	}

	return nil, false
}

// envSecretKeyRefProps gives the properties of an environment variable that
// takes its value from a `secretKeyRef`.
func envSecretKeyRefProps(edge *EvidenceEdge) (map[string]string, bool) {
	if edge.Properties["source"] != sourceSecretKeyRef {
		return nil, false
	}

	props := map[string]string{"source": sourceSecretKeyRef}
	if c := edge.Properties["container"]; c != "" {
		props["container"] = c
	}

	return props, true
}
