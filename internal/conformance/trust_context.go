package conformance

import (
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/trust"
)

// TrustResult is the `trust` object of results.json: the boundary crossings of the
// evaluated relationships, flow steps and observed flows. The key is left out when
// the workspace declares no trust value, so the result of such a run is the one of
// before (schemaVersion stays 1.1).
type TrustResult struct {
	// Crossings has one entry for each flow and dimension, sorted by (flow, dimension). Never nil.
	Crossings []trust.Crossing `json:"crossings"`
}

// remoteCallKinds are the observed edge kinds that cross a process boundary. Only
// they give observed flows; a plain call inside one process is not a flow between two
// trust contexts.
var remoteCallKinds = map[observation.CanonicalEdgeKind]struct{}{
	observation.KindCallsService:    {},
	observation.KindDispatchesAsync: {},
	observation.KindCallsGRPC:       {},
	observation.KindCallsGraphQL:    {},
	observation.KindCallsTRPC:       {},
}

// trustInput is what deriveTrust reads.
type trustInput struct {
	Model *intent.ArchitectureModel
	// Reconcile gives the observed flows; nil leaves them out.
	Reconcile *ReconcileInput
	Bindings  []binding.Binding
	Vocab     trust.Vocabulary
}

// deriveTrust gives the trust section of a run: one crossing per dimension for each
// `connects` and `interacts` relationship, each declared flow step, and each observed
// remote call between bound nodes that no relationship declares, or to a destination
// that no binding maps. It gives nil when no node, relationship, or binding declares
// any trust value, because then every crossing would be `unknown` and say nothing.
//
// The result depends on explicit values only: node names, namespaces, tags, and
// paths never produce a value (SEC-02).
func deriveTrust(input *trustInput) *TrustResult {
	if input.Model == nil || !declaresTrust(input) {
		return nil
	}

	derivation := &trustDerivation{input: input, ends: endpointTrust(input)}
	crossings := []trust.Crossing{}
	declared := map[[2]string]struct{}{}
	rels := make(map[string]*intent.RelationshipDeclaration, len(input.Model.Relationships))

	for i := range input.Model.Relationships {
		rel := &input.Model.Relationships[i]
		rels[rel.ID] = rel
		crossings = append(crossings, derivation.relationshipCrossings(rel, declared)...)
	}

	for i := range input.Model.Flows {
		crossings = append(crossings, derivation.flowStepCrossings(&input.Model.Flows[i], rels)...)
	}

	if input.Reconcile != nil {
		crossings = append(crossings, derivation.observedCrossings(declared)...)
	}

	trust.Sort(crossings)

	return &TrustResult{Crossings: crossings}
}

// trustDerivation derives the crossings of one run from the trust of its endpoints.
type trustDerivation struct {
	input *trustInput
	ends  map[string]trust.Declaration
}

// endpoint is the node with the trust that the model and the bindings declare for it.
func (d *trustDerivation) endpoint(id string) trust.Endpoint {
	return trust.Endpoint{ID: id, Trust: d.ends[id]}
}

// relationshipCrossings gives the crossings of the flows of a relationship and records their pairs as declared.
func (d *trustDerivation) relationshipCrossings(rel *intent.RelationshipDeclaration, declared map[[2]string]struct{}) []trust.Crossing {
	pairs := relationshipPairs(rel)
	out := make([]trust.Crossing, 0, len(pairs))

	for _, pair := range pairs {
		declared[pair] = struct{}{}
		out = append(out, trust.Crossings(d.input.Vocab, &trust.Flow{
			ID: flowID(rel, pair[1]), Kind: trust.FlowRelationship, Relationship: rel.ID,
			Source: d.endpoint(pair[0]), Destination: d.endpoint(pair[1]), Classification: relationshipClass(rel),
		})...)
	}

	return out
}

// flowStepCrossings gives the crossings of the steps of a flow whose relationship the model declares.
func (d *trustDerivation) flowStepCrossings(flow *intent.FlowDeclaration, rels map[string]*intent.RelationshipDeclaration) []trust.Crossing {
	var out []trust.Crossing

	for i := range flow.Transitions {
		step := &flow.Transitions[i]
		if rel, ok := rels[step.RelationshipID]; ok {
			out = append(out, d.stepCrossings(flow.ID, step, rel)...)
		}
	}

	return out
}

// stepCrossings gives the crossings of one step: the pairs of its relationship, reversed when the step runs backwards.
func (d *trustDerivation) stepCrossings(flowName string, step *intent.FlowTransition, rel *intent.RelationshipDeclaration) []trust.Crossing {
	pairs := relationshipPairs(rel)
	out := make([]trust.Crossing, 0, len(pairs))

	for _, pair := range pairs {
		src, dst := pair[0], pair[1]
		if step.Direction == "destination-to-source" {
			src, dst = dst, src
		}

		out = append(out, trust.Crossings(d.input.Vocab, &trust.Flow{
			ID:   flowName + "/" + strconv.Itoa(step.Sequence) + stepSuffix(rel, pair[1]),
			Kind: trust.FlowStep, Relationship: rel.ID,
			Source: d.endpoint(src), Destination: d.endpoint(dst), Classification: relationshipClass(rel),
		})...)
	}

	return out
}

func relationshipClass(rel *intent.RelationshipDeclaration) string {
	if rel.Trust == nil {
		return ""
	}

	return rel.Trust.Classification
}

// declaresTrust says whether any node, relationship, or binding assigns a trust value.
func declaresTrust(input *trustInput) bool {
	for i := range input.Model.Nodes {
		if assignsTrust(input.Model.Nodes[i].Trust) {
			return true
		}
	}

	for i := range input.Model.Relationships {
		if assignsTrust(input.Model.Relationships[i].Trust) {
			return true
		}
	}

	return slices.ContainsFunc(input.Bindings, func(b binding.Binding) bool { return assignsTrust(b.Trust) })
}

// assignsTrust says whether a declaration has at least one value.
func assignsTrust(t *trust.Declaration) bool {
	return t != nil && !t.IsZero()
}

// endpointTrust gives the trust of each CALM node: its own declaration merged with
// the binding entries that name it. Two declarations that differ on a dimension
// leave that dimension undeclared (Merge), so the crossing is `unknown`.
func endpointTrust(input *trustInput) map[string]trust.Declaration {
	byNode := map[string][]trust.Declaration{}

	collect := func(id string, t *trust.Declaration) {
		if t != nil {
			byNode[id] = append(byNode[id], *t)
		}
	}

	for i := range input.Model.Nodes {
		collect(input.Model.Nodes[i].ID, input.Model.Nodes[i].Trust)
	}

	for i := range input.Bindings {
		collect(input.Bindings[i].ArchitectureRef, input.Bindings[i].Trust)
	}

	out := make(map[string]trust.Declaration, len(byNode))

	for id, decls := range byNode {
		merged, _ := trust.Merge(decls...)
		out[id] = merged
	}

	return out
}

// relationshipPairs gives the (source, destination) node pairs of a relationship that
// carries a flow: the source and destination of `connects`, the actor and each node
// of `interacts`. The other relationship kinds are structure, not flows.
func relationshipPairs(rel *intent.RelationshipDeclaration) [][2]string {
	switch rel.Kind {
	case intent.RelConnects, intent.RelInteracts:
	case intent.RelComposedOf, intent.RelDeployedIn, intent.RelOptions:
		return nil
	}

	if rel.Source == "" {
		return nil
	}

	pairs := make([][2]string, 0, len(rel.Targets))

	for _, target := range rel.Targets {
		if target != "" && target != rel.Source {
			pairs = append(pairs, [2]string{rel.Source, target})
		}
	}

	return pairs
}

// flowID is the identity of a relationship flow: the relationship ID, with the
// destination appended when a relationship has several destinations (`interacts`).
func flowID(rel *intent.RelationshipDeclaration, destination string) string {
	return rel.ID + stepSuffix(rel, destination)
}

func stepSuffix(rel *intent.RelationshipDeclaration, destination string) string {
	if len(rel.Targets) > 1 {
		return "#" + destination
	}

	return ""
}

// externalID names an unbound observed destination: its scoped identity when it has
// one, else its qualified name, else its observed ID.
func externalID(node *observation.ObservedNode) string {
	switch {
	case node.ScopedID != "":
		return node.ScopedID
	case node.QualifiedName != "":
		return node.QualifiedName
	}

	return node.ObservedID
}
