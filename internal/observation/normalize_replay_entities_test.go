package observation_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// isHasBranchFact says whether a fact is the unsupported fact of the native type HAS_BRANCH.
func isHasBranchFact(e *observation.EvidenceEdge) bool {
	if e.Kind != observation.KindUnsupported {
		return false
	}

	return e.NativeKind == "HAS_BRANCH"
}

// requireUnmappedDiagnostic checks that the result has the CO-OBS-016 diagnostic and no ambiguity diagnostic.
func requireUnmappedDiagnostic(t *testing.T, r *infraResult) {
	t.Helper()

	var found bool

	for _, d := range r.Diagnostics {
		if d.Code == diag.OBS016 {
			found = true
		}

		if d.Code == diag.OBS019 {
			t.Errorf("ambiguity diagnostic %s", d.Message)
		}
	}

	if !found {
		t.Fatalf("no CO-OBS-016 in %v", r.Diagnostics)
	}
}

func TestReplayUnsupportedFactAndDiagnostic(t *testing.T) {
	r := fetchInfra(t)

	if len(r.Unsupported) != 1 || !isHasBranchFact(r.Unsupported[0]) {
		t.Fatalf("unsupported = %+v", r.Unsupported)
	}

	if r.UnmappedNativeEdges["HAS_BRANCH"] != 1 || len(r.UnmappedNativeEdges) != 1 {
		t.Fatalf("unmapped = %v", r.UnmappedNativeEdges)
	}

	if r.Unsupported[0].Properties["branch"] == "" {
		t.Fatalf("unsupported fact lost its properties: %v", r.Unsupported[0].Properties)
	}

	requireUnmappedDiagnostic(t, r)
}

// entitiesByObservedID indexes the entities of the result by observed ID.
func entitiesByObservedID(r *infraResult) map[string]observation.ObservedNode {
	byID := map[string]observation.ObservedNode{}
	for i := range r.Entities {
		byID[r.Entities[i].ObservedID] = r.Entities[i]
	}

	return byID
}

// diagramElementEntities gives the entities that are diagram elements.
func diagramElementEntities(r *infraResult) []observation.ObservedNode {
	var elements []observation.ObservedNode

	for i := range r.Entities {
		if r.Entities[i].Label == "DiagramElement" {
			elements = append(elements, r.Entities[i])
		}
	}

	return elements
}

// requireNewerDecision checks the entity of the decision that supersedes the other one.
func requireNewerDecision(t *testing.T, newer *observation.ObservedNode) {
	t.Helper()

	got := [3]any{newer.Label, newer.Resolution, newer.FilePath}
	if want := [3]any{"ADR", observation.ResolutionUnbound, "docs/adr/0002-use-managed-postgres.md"}; got != want {
		t.Fatalf("decision = %+v", *newer)
	}

	if newer.Properties["adr_id"] != "ADR-2" || slices.Contains([]string{newer.Properties["title"], newer.Properties["status_text"]}, "") {
		t.Fatalf("decision properties = %v", newer.Properties)
	}
}

func TestReplayKeepsDecisionAndDiagramIdentityProperties(t *testing.T) {
	r := fetchInfra(t)

	byID := entitiesByObservedID(r)

	newer := byID[observation.ADRSynthID("docs/adr/0002-use-managed-postgres.md")]
	requireNewerDecision(t, &newer)

	older := byID[observation.ADRSynthID("docs/adr/0001-use-self-hosted-postgres.md")]
	if older.Properties["adr_id"] != "ADR-1" || older.Properties["superseded_by"] != "0002-use-managed-postgres.md" {
		t.Fatalf("superseded decision properties = %v", older.Properties)
	}

	elements := diagramElementEntities(r)
	if len(elements) != 4 {
		t.Fatalf("diagram elements = %+v", elements)
	}

	for _, n := range elements {
		if n.Properties["element_id"] == "" || observation.DiagramElementSynthID(n.FilePath, "Context-diagram", n.Properties["element_id"]) != n.ObservedID {
			t.Errorf("diagram element = %+v", n)
		}
	}
}

// imageEntitiesByName reports each entity that is not unbound and gives the Image entities by qualified name.
func imageEntitiesByName(t *testing.T, r *infraResult) map[string]observation.ObservedNode {
	t.Helper()

	images := map[string]observation.ObservedNode{}

	for i := range r.Entities {
		n := &r.Entities[i]
		if n.Resolution != observation.ResolutionUnbound || n.ObservedID == "" {
			t.Errorf("entity %+v is not unbound", *n)
		}

		if n.Label == "Image" {
			images[n.QualifiedName] = *n
		}
	}

	return images
}

// isCompleteImageEntity says whether an Image entity has the synthesized ID of its name and a file.
func isCompleteImageEntity(n *observation.ObservedNode, name string) bool {
	if n.ObservedID != observation.ImageSynthID(name) {
		return false
	}

	return n.FilePath != ""
}

// countCITasks counts the CITask entities and reports each one that is not the build job of the CI workflow.
func countCITasks(t *testing.T, r *infraResult) int {
	t.Helper()

	var ci int

	for i := range r.Entities {
		n := &r.Entities[i]
		if n.Label != "CITask" {
			continue
		}

		ci++

		if n.FilePath != ".github/workflows/ci.yaml" || n.Properties["job"] != "build" {
			t.Errorf("CI task = %+v", *n)
		}
	}

	return ci
}

func TestReplayEntitiesAreUnbound(t *testing.T) {
	r := fetchInfra(t)

	images := imageEntitiesByName(t, r)

	api, ok := images["registry.example.com/payments/api"]
	if !ok || !isCompleteImageEntity(&api, "registry.example.com/payments/api") {
		t.Fatalf("image entity = %+v (all %v)", api, images)
	}

	if len(images) != 8 {
		t.Fatalf("images = %d", len(images))
	}

	if ci := countCITasks(t, r); ci != 1 {
		t.Fatalf("CI tasks = %d", ci)
	}
}

// requireDiagramBlock checks the properties, the name, and the file of the diagram block.
func requireDiagramBlock(t *testing.T, n *observation.ObservedNode) {
	t.Helper()

	got := [4]string{n.Properties["diagram_kind"], n.Properties["format"], n.QualifiedName, n.FilePath}
	if want := [4]string{"flowchart", "mermaid", "docs.architecture.Context-diagram#diagram-1", "docs/architecture.md"}; got != want {
		t.Errorf("diagram block = %+v", *n)
	}
}

// requireDiagramElement checks that a diagram element is named by its node ID and has a line.
func requireDiagramElement(t *testing.T, n *observation.ObservedNode) {
	t.Helper()

	// The diagram writes bare node IDs, so the name is the node ID (O12).
	if n.Properties["name"] != n.Properties["element_id"] || n.StartLine == 0 {
		t.Errorf("diagram element = %+v", *n)
	}
}

func TestReplayKeepsDiagramBlockAndElementNames(t *testing.T) {
	r := fetchInfra(t)

	var blocks, elements int

	for _, n := range r.Nodes {
		switch n.Label {
		case observation.LabelDiagram:
			blocks++

			requireDiagramBlock(t, &n)
		case observation.LabelDiagramElement:
			elements++

			requireDiagramElement(t, &n)
		}
	}

	if blocks != 1 || elements != 4 {
		t.Errorf("blocks = %d, elements = %d", blocks, elements)
	}
}
