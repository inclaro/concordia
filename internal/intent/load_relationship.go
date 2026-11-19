package intent

import (
	"strconv"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/observation"
)

func (l *loader) collectRelationship(rawRel obj, pointer string) {
	uniqueID, hasID := rawRel["unique-id"].(string)
	if !hasID {
		return
	}

	relType, hasType := asObj(rawRel["relationship-type"])
	if !hasType {
		return
	}

	rel, ok := buildRelationship(uniqueID, relType, rawRel, pointer)
	if !ok {
		return
	}

	if raw, present := rawRel["concordia"]; present {
		l.applyRelationshipExtension(&rel, raw, pointer)
	}

	l.rels = append(l.rels, rel)
	l.collectControls(rawRel["controls"], ControlScope{Level: ScopeRelationship, ID: uniqueID}, pointer+"/controls")
}

// applyRelationshipExtension reads the `concordia` extension of a relationship
// declaration into rel. An extension that is not valid adds diagnostics and changes nothing.
func (l *loader) applyRelationshipExtension(rel *RelationshipDeclaration, raw any, pointer string) {
	ext, isObj := asObj(raw)
	if !isObj {
		l.add(diag.INT007, diag.Detail("concordia must be an object"), pointer+"/concordia")

		return
	}

	if !l.extErrors(l.set.relationship, ext, pointer+"/concordia") {
		return
	}

	if o, hasObs := asObj(ext["observation"]); hasObs {
		rel.Observation = l.observationOf(o, pointer+"/concordia/observation")
	}

	rel.Trust = l.trustOf(ext["trust"], pointer+"/concordia/trust")
}

func (l *loader) observationOf(obsRaw obj, pointer string) *RelationshipObservation {
	obs := &RelationshipObservation{Required: obsRaw["required"] == true}

	if rawKinds, present := obsRaw["allowed-edge-kinds"]; present {
		if kinds, ok := l.edgeKindsOf(rawKinds, pointer+"/allowed-edge-kinds"); ok {
			obs.AllowedEdgeKinds = kinds
		}
	}

	if c, ok := obsRaw["minimum-confidence"].(string); ok {
		obs.MinimumConfidence = observation.Confidence(c)
	}

	return obs
}

// edgeKindsOf resolves the `allowed-edge-kinds` entries. Each unknown entry adds
// CO-INT-007; ok is false when there is one.
func (l *loader) edgeKindsOf(raw any, pointer string) ([]observation.CanonicalEdgeKind, bool) {
	kinds := []observation.CanonicalEdgeKind{}
	allKnown := true

	for i, item := range asArr(raw) {
		resolved, known := resolveEdgeKind(item)
		if !known {
			allKnown = false

			l.add(diag.INT007, diag.Detail("unknown edge kind '"+edgeKindText(item)+"'"), pointer+"/"+strconv.Itoa(i))

			continue
		}

		kinds = append(kinds, resolved...)
	}

	return kinds, allKnown
}

func resolveEdgeKind(item any) ([]observation.CanonicalEdgeKind, bool) {
	text, isStr := item.(string)
	if !isStr {
		return nil, false
	}

	return edgekind.Resolve(text)
}

func edgeKindText(item any) string {
	if text, isStr := item.(string); isStr {
		return text
	}

	return canonjson.MustString(item)
}

func buildRelationship(uniqueID string, relType, raw obj, pointer string) (RelationshipDeclaration, bool) {
	rel := RelationshipDeclaration{ID: uniqueID, Pointer: pointer, Targets: []string{}}

	if !fillRelationshipKind(&rel, relType) {
		return rel, false
	}

	rel.Protocol = asStr(raw["protocol"])
	rel.Description = asStr(raw["description"])

	return rel, true
}

// fillRelationshipKind sets the kind, source and targets of rel from the
// relationship-type. It gives false when the relationship-type has no known kind.
func fillRelationshipKind(rel *RelationshipDeclaration, relType obj) bool {
	switch {
	case isObjKey(relType, "connects"):
		fillConnects(rel, relType)
	case isObjKey(relType, "interacts"):
		fillInteracts(rel, relType)
	case isObjKey(relType, "composed-of"):
		fillContainer(rel, RelComposedOf, relType)
	case isObjKey(relType, "deployed-in"):
		fillContainer(rel, RelDeployedIn, relType)
	default:
		if _, ok := relType["options"]; !ok {
			return false
		}

		rel.Kind = RelOptions
	}

	return true
}

func fillConnects(rel *RelationshipDeclaration, relType obj) {
	c, _ := asObj(relType["connects"])
	rel.Kind = RelConnects

	if s, ok := asObj(c["source"]); ok {
		rel.Source = asStr(s["node"])
	}

	if d, ok := asObj(c["destination"]); ok {
		if node := asStr(d["node"]); node != "" {
			rel.Targets = []string{node}
		}
	}
}

func fillInteracts(rel *RelationshipDeclaration, relType obj) {
	v, _ := asObj(relType["interacts"])
	rel.Kind = RelInteracts
	rel.Targets = strList(v["nodes"])
	rel.Source = asStr(v["actor"])
}

func fillContainer(rel *RelationshipDeclaration, kind RelationshipKind, relType obj) {
	v, _ := asObj(relType[string(kind)])
	rel.Kind = kind
	rel.Targets = strList(v["nodes"])
	rel.Container = asStr(v["container"])
}
