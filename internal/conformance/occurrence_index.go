package conformance

import (
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// indexFacts indexes the facts by kind and endpoint, and the observed entities by the CALM element that they are bound to.
func (e *ObligationEvaluator) indexFacts() {
	e.indexEdges()
	e.indexAnchors()
	e.indexConflicts()
}

// indexEdges indexes the facts by kind and each endpoint. Structural edges are no facts.
func (e *ObligationEvaluator) indexEdges() {
	for _, edge := range e.input.Evidence.Edges {
		if IsStructuralKind(edge.Kind) {
			continue
		}

		e.from[factSlot{edge.Kind, edge.From}] = append(e.from[factSlot{edge.Kind, edge.From}], edge)
		e.to[factSlot{edge.Kind, edge.To}] = append(e.to[factSlot{edge.Kind, edge.To}], edge)
	}
}

// indexAnchors indexes the observed entities by the CALM element that they are bound to, sorted by ID.
func (e *ObligationEvaluator) indexAnchors() {
	for id, calmIDs := range e.input.BindingSet.ByObserved {
		e.anchorTo(id, calmIDs)
	}

	for _, ids := range e.anchors {
		slices.SortFunc(ids, canonjson.CompareUTF16)
	}
}

// anchorTo makes the observed entity an anchor of each CALM element.
func (e *ObligationEvaluator) anchorTo(observedID string, calmIDs []string) {
	for _, calm := range calmIDs {
		e.anchors[calm] = append(e.anchors[calm], observedID)
	}
}

// indexConflicts indexes the certain conflicts by the nodes of their locations.
func (e *ObligationEvaluator) indexConflicts() {
	for i := range e.typed.Conflicts {
		if conflict := &e.typed.Conflicts[i]; conflict.Certain {
			e.indexConflict(conflict)
		}
	}
}

func (e *ObligationEvaluator) indexConflict(conflict *observation.Conflict) {
	for _, loc := range conflict.Locations {
		e.conflicted[loc.NodeID] = conflict
	}
}

// indexDeclarations indexes the CALM nodes, the bindings, the capability regressions, and the declared environments.
func (e *ObligationEvaluator) indexDeclarations() {
	e.indexModelNodes()
	e.indexBindings()

	for _, reg := range e.typed.Regressions {
		e.regressions[reg.Family] = reg
	}

	e.indexEnvironments()
}

func (e *ObligationEvaluator) indexModelNodes() {
	model := e.input.Model
	if model == nil {
		return
	}

	for i := range model.Nodes {
		e.nodesByID[model.Nodes[i].ID] = &model.Nodes[i]
	}
}

// indexBindings indexes the loaded bindings by ID and collects the subjects of the active `image` bindings.
func (e *ObligationEvaluator) indexBindings() {
	loaded := e.typed.Bindings
	if loaded == nil {
		return
	}

	for i := range loaded.Bindings {
		b := &loaded.Bindings[i]
		e.bindingsByID[b.ID] = b

		if namesImage(b) {
			e.imageSubjects[b.ArchitectureRef] = true
		}
	}
}

// namesImage says whether an `image` match makes the binding name its subject. A proposed binding takes no part in resolution.
func namesImage(b *binding.Binding) bool {
	if b.Status == binding.StatusProposed {
		return false
	}

	return slices.ContainsFunc(b.Match, func(m binding.Match) bool { return m.Kind == binding.MatchImage })
}

func (e *ObligationEvaluator) indexEnvironments() {
	ws := e.typed.Workspace
	if ws == nil {
		return
	}

	for i := range ws.Environments {
		e.declared = append(e.declared, ws.Environments[i].ID)
	}
}
