package observation_test

import (
	"reflect"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

func entityByID(entities []observation.ObservedNode, id string) (observation.ObservedNode, bool) {
	for i := range entities {
		if entities[i].ObservedID == id {
			return entities[i], true
		}
	}

	return observation.ObservedNode{}, false
}

func TestSynthesizeEntitiesImageWithoutBoundNode(t *testing.T) {
	image := observation.ObservedNode{ObservedID: "cbm:p:2", Label: "Image", QualifiedName: "__image__registry.example.com/payments/api"}
	deployment := observation.ObservedNode{
		ObservedID: "cbm:p:1", Label: "Resource", QualifiedName: "k8s.d.yaml::apps/Deployment/payments/api", FilePath: "k8s/prod/deployment.yaml", StartLine: 3,
	}
	nodes := map[string]observation.ObservedNode{image.ObservedID: image, deployment.ObservedID: deployment}

	edges := []*observation.EvidenceEdge{
		{
			From: deployment.ObservedID, To: image.ObservedID, Kind: observation.KindUsesImage, NativeKind: "USAGE",
			SourceSpan: &observation.SourceSpan{Path: "k8s/prod/deployment.yaml", Line: 12},
		},
		{
			From: deployment.ObservedID, To: image.ObservedID, Kind: observation.KindUsesImage, NativeKind: "USAGE",
			SourceSpan: &observation.SourceSpan{Path: "k8s/prod/deployment.yaml", Line: 9},
		},
	}

	entities := observation.SynthesizeEntities(nodes, edges)

	entity, ok := entityByID(entities, observation.ImageSynthID("registry.example.com/payments/api"))
	if !ok {
		t.Fatalf("no image entity in %+v", entities)
	}

	if got := []any{entity.Resolution, entity.Label, entity.ObservedID[:6]}; !reflect.DeepEqual(got, []any{observation.ResolutionUnbound, "Image", "synth:"}) {
		t.Fatalf("entity = %+v", entity)
	}

	// It keeps the location of its first occurrence (smallest line).
	if entity.FilePath != "k8s/prod/deployment.yaml" || entity.StartLine != 9 {
		t.Fatalf("location = %s:%d", entity.FilePath, entity.StartLine)
	}

	if len(entities) != 1 {
		t.Fatalf("entities = %+v", entities)
	}
}

// entityHead is the label, the start line, and the resolution of an entity.
type entityHead struct {
	Label      string
	Resolution observation.Resolution
	Line       int
}

func headOfEntity(n *observation.ObservedNode) entityHead {
	return entityHead{Label: n.Label, Line: n.StartLine, Resolution: n.Resolution}
}

// mustEntityByID finds the synthesized entity and fails with its name when it is missing.
func mustEntityByID(t *testing.T, entities []observation.ObservedNode, id, name string) observation.ObservedNode {
	t.Helper()

	entity, ok := entityByID(entities, id)
	if !ok {
		t.Fatalf("%s = %+v (%v)", name, entity, ok)
	}

	return entity
}

// ciDecisionSecretDiagramGraph is a workflow that launches a script, an ADR
// that supersedes itself, a deployment that references a secret and sets an
// environment variable from it, and two diagram elements joined by an arrow.
// It gives the graph and the ADR node.
func ciDecisionSecretDiagramGraph() (map[string]observation.ObservedNode, []*observation.EvidenceEdge, observation.ObservedNode) {
	workflow := observation.ObservedNode{ObservedID: "cbm:p:1", Label: "Module", QualifiedName: "ci", FilePath: ".github/workflows/ci.yaml"}
	script := observation.ObservedNode{ObservedID: "cbm:p:2", Label: "Module", QualifiedName: "scripts.build", FilePath: "scripts/build.sh"}
	adr := observation.ObservedNode{
		ObservedID: "cbm:p:3", Label: "ADR", QualifiedName: "docs.adr.0001.__adr__", FilePath: "docs/adr/0001-x.md", StartLine: 1,
		Properties: map[string]string{"adr_id": "ADR-1", "superseded_by": "0002-y.md", "title": "X", "status": "superseded", "status_text": "Superseded", "date": "2025-01-01"},
	}
	deployment := observation.ObservedNode{ObservedID: "cbm:p:4", Label: "Resource", QualifiedName: "d", FilePath: "k8s/d.yaml", Kind: "Deployment"}
	secret := observation.ObservedNode{ObservedID: "cbm:p:5", Label: "Resource", QualifiedName: "k8s.s.yaml::core/Secret/payments/s", FilePath: "k8s/s.yaml", Kind: "Secret"}
	env := observation.ObservedNode{ObservedID: "cbm:p:6", Label: "EnvVar", QualifiedName: "d#env/app/TOKEN", FilePath: "k8s/d.yaml"}
	first := observation.ObservedNode{
		ObservedID: "cbm:p:7", Label: "DiagramElement", QualifiedName: "docs.arch.Context#diagram-1.gateway", FilePath: "docs/arch.md", StartLine: 10,
		Properties: map[string]string{"element_id": "gateway", "element_kind": "node"},
	}
	second := observation.ObservedNode{
		ObservedID: "cbm:p:8", Label: "DiagramElement", QualifiedName: "docs.arch.Context#diagram-1.api", FilePath: "docs/arch.md", StartLine: 11,
		Properties: map[string]string{"element_id": "api"},
	}

	nodes := map[string]observation.ObservedNode{}
	for _, n := range []*observation.ObservedNode{&workflow, &script, &adr, &deployment, &secret, &env, &first, &second} {
		nodes[n.ObservedID] = *n
	}

	edges := []*observation.EvidenceEdge{
		{
			From: workflow.ObservedID, To: script.ObservedID, Kind: observation.KindLaunches, NativeKind: "SPAWNS",
			Properties: map[string]string{"ci": "github-actions", "job": "build", "api": "shell"}, SourceSpan: &observation.SourceSpan{Path: ".github/workflows/ci.yaml", Line: 11},
		},
		{From: adr.ObservedID, To: adr.ObservedID, Kind: observation.KindSupersedes, NativeKind: "SUPERSEDES"},
		{
			From: deployment.ObservedID, To: secret.ObservedID, Kind: observation.KindReferencesSecret, NativeKind: "USAGE",
			Facets: map[string]string{"referenceMode": "envFrom"}, SourceSpan: &observation.SourceSpan{Path: "k8s/d.yaml", Line: 20},
		},
		{
			From: deployment.ObservedID, To: env.ObservedID, Kind: observation.KindConfiguresEnvSet, NativeKind: "CONFIGURES",
			Properties: map[string]string{"source": "secretKeyRef", "container": "app", "strategy": "env_set"}, SourceSpan: &observation.SourceSpan{Path: "k8s/d.yaml", Line: 25},
		},
		{From: first.ObservedID, To: second.ObservedID, Kind: observation.KindDiagramArrow, NativeKind: "DIAGRAM_ARROW"},
	}

	return nodes, edges, adr
}

func TestSynthesizeEntitiesCITaskDecisionSecretAndDiagram(t *testing.T) {
	nodes, edges, adr := ciDecisionSecretDiagramGraph()

	entities := observation.SynthesizeEntities(nodes, edges)

	ci := mustEntityByID(t, entities, "synth:ci-task:github-actions/build@.github/workflows/ci.yaml", "CI task")
	if headOfEntity(&ci) != (entityHead{Label: "CITask", Line: 11, Resolution: observation.ResolutionUnbound}) {
		t.Fatalf("CI task = %+v (%v)", ci, true)
	}

	decision := mustEntityByID(t, entities, observation.ADRSynthID("docs/adr/0001-x.md"), "decision")
	if !reflect.DeepEqual(decision.Properties, adr.Properties) || decision.FilePath != "docs/adr/0001-x.md" {
		t.Fatalf("decision = %+v (%v)", decision, true)
	}

	requireSecretReferences(t, entities)

	element := requireGatewayDiagramElement(t, entities)
	requireReorderedDiagramElementKeepsID(t, element.ObservedID)

	for i := 1; i < len(entities); i++ {
		if entities[i-1].ObservedID >= entities[i].ObservedID {
			t.Fatalf("entities are not sorted: %v", entities)
		}
	}
}

// requireSecretReferences fails unless the secret is referenced as a whole and by an environment variable key.
func requireSecretReferences(t *testing.T, entities []observation.ObservedNode) {
	t.Helper()

	ref := mustEntityByID(t, entities, "synth:secret:k8s.s.yaml::core/Secret/payments/s", "secret reference")
	if got := []any{ref.Label, ref.Properties["referenceMode"], ref.StartLine}; !reflect.DeepEqual(got, []any{"SecretReference", "envFrom", 20}) {
		t.Fatalf("secret reference = %+v (%v)", ref, true)
	}

	keyRef := mustEntityByID(t, entities, "synth:secret:d#env/app/TOKEN", "key reference")
	if keyRef.Properties["source"] != "secretKeyRef" || keyRef.Properties["container"] != "app" {
		t.Fatalf("key reference = %+v (%v)", keyRef, true)
	}
}

// requireGatewayDiagramElement fails unless the gateway diagram element has
// its identity from the file, the section, and the author's element id, never the ordinal.
func requireGatewayDiagramElement(t *testing.T, entities []observation.ObservedNode) observation.ObservedNode {
	t.Helper()

	element := mustEntityByID(t, entities, observation.DiagramElementSynthID("docs/arch.md", "Context", "gateway"), "diagram element")
	if got := []string{element.Label, element.Properties["element_id"], element.Properties["element_kind"]}; !reflect.DeepEqual(got, []string{"DiagramElement", "gateway", "node"}) {
		t.Fatalf("diagram element = %+v (%v)", element, true)
	}

	return element
}

// requireReorderedDiagramElementKeepsID fails unless the same diagram with
// another ordinal gives the same identity.
func requireReorderedDiagramElementKeepsID(t *testing.T, wantID string) {
	t.Helper()

	reordered := observation.ObservedNode{
		ObservedID: "cbm:p:9", Label: "DiagramElement", QualifiedName: "docs.arch.Context#diagram-3.gateway", FilePath: "docs/arch.md",
		Properties: map[string]string{"element_id": "gateway"},
	}

	again := observation.SynthesizeEntities(
		map[string]observation.ObservedNode{reordered.ObservedID: reordered},
		[]*observation.EvidenceEdge{{From: reordered.ObservedID, To: reordered.ObservedID, NativeKind: "DIAGRAM_ARROW"}})
	if len(again) != 1 || again[0].ObservedID != wantID {
		t.Fatalf("reordered diagram element = %+v, want id %s", again, wantID)
	}
}

func TestSynthesizeEntitiesSkipsNodesWithoutAnIdentity(t *testing.T) {
	adr := observation.ObservedNode{ObservedID: "cbm:p:1", Label: "ADR", QualifiedName: "x"}
	element := observation.ObservedNode{ObservedID: "cbm:p:2", Label: "DiagramElement", QualifiedName: "x", FilePath: "docs/a.md"}
	nodes := map[string]observation.ObservedNode{adr.ObservedID: adr, element.ObservedID: element}

	if got := observation.SynthesizeEntities(nodes, []*observation.EvidenceEdge{{From: adr.ObservedID, To: element.ObservedID}}); len(got) != 0 {
		t.Fatalf("entities = %+v", got)
	}
}

func TestDerivePlacementAppendsNamespaceEntitiesToTheOthersInOrder(t *testing.T) {
	deployment := observation.ObservedNode{
		ObservedID: "cbm:p:1", Label: "Resource", QualifiedName: "k8s.d.yaml::apps/Deployment/shop/orders", FilePath: "k8s/prod/d.yaml", StartLine: 2,
		Environment: "production", Properties: map[string]string{"obj_key": "apps/Deployment/shop/orders"},
	}
	result := &observation.EvidenceQueryResult{
		Nodes:    map[string]observation.ObservedNode{deployment.ObservedID: deployment},
		Entities: []observation.ObservedNode{{ObservedID: observation.ImageSynthID("registry.example.com/shop/orders"), Label: "Image", Resolution: observation.ResolutionUnbound}},
	}

	observation.DerivePlacement(result, nil)

	labels := make([]string, len(result.Entities))
	for i := range result.Entities {
		labels[i] = result.Entities[i].Label
	}

	if !reflect.DeepEqual(labels, []string{"Image", observation.LabelNamespace}) {
		t.Fatalf("entities = %+v", result.Entities)
	}

	if _, ok := entityByID(result.Entities, observation.NamespaceSynthID("production", "shop")); !ok {
		t.Fatalf("no synthesized namespace in %+v", result.Entities)
	}

	// A placement fact makes no entity by itself: SynthesizeEntities reads the endpoints of the provider facts only.
	if got := observation.SynthesizeEntities(result.Nodes, result.Edges); len(got) != 0 {
		t.Errorf("a placement fact made an entity: %+v", got)
	}
}
