package conformance

import (
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// factDimensions are the selector dimensions that only a fact has: the subject itself has no value for them.
var factDimensions = map[policy.Dimension]bool{
	policy.DimensionProtocol: true, policy.DimensionOperation: true, policy.DimensionInterface: true,
	policy.DimensionContainer: true, policy.DimensionWorkload: true, policy.DimensionRepository: true, policy.DimensionEvidenceRole: true,
}

// contextFor gives the property values that a selector sees: those of the fact (anchor is the observed entity
// of the subject), or, with no fact (bound is nil), those of the subject itself. A property with no source here
// (trust context, data classification, governance scope) or no value on the fact is unknown, never false.
func (r *obligationRun) contextFor(anchor string, bound *BoundEdge) policy.ResolverSet {
	set := policy.ResolverSet{
		policy.DimensionArchitecture: r.architectureProperty,
		policy.DimensionObservedKind: func(property string) (string, bool) {
			return string(r.ob.Relation.Kind), property == policy.PropertyValue
		},
		policy.DimensionEnvironment: func(property string) (string, bool) {
			env := r.ob.Environment
			if bound != nil {
				env = r.factEnvironment(anchor, bound)
			}

			return env, property == policy.PropertyValue && env != "" && env != observation.EnvironmentUnresolved
		},
	}

	if bound != nil {
		r.addFactResolvers(set, anchor, bound)
	}

	return set
}

// architectureProperty reads the CALM identity and node type of the subject.
func (r *obligationRun) architectureProperty(property string) (string, bool) {
	switch property {
	case policy.PropertyID:
		return r.ob.Subject, true
	case policy.PropertyNodeType:
		if node := r.e.nodesByID[r.ob.Subject]; node != nil {
			return node.NodeType, true
		}
	}

	return "", false
}

// edgeValue gives a one-value resolver that reads the first key that the fact states, from its facets, then its properties.
func edgeValue(edge *observation.EvidenceEdge, keys ...string) func(string) (string, bool) {
	return func(property string) (string, bool) {
		if property != policy.PropertyValue {
			return "", false
		}

		for _, key := range keys {
			if v := edge.Facets[key]; v != "" {
				return v, true
			}

			if v := edge.Properties[key]; v != "" {
				return v, true
			}
		}

		return "", false
	}
}

// addFactResolvers adds the dimensions that only a fact has.
func (r *obligationRun) addFactResolvers(set policy.ResolverSet, anchor string, bound *BoundEdge) {
	edge := bound.Edge

	set[policy.DimensionProtocol] = edgeValue(edge, "protocol")
	set[policy.DimensionOperation] = edgeValue(edge, "operation")
	set[policy.DimensionInterface] = edgeValue(edge, "interface")
	set[policy.DimensionRepository] = func(property string) (string, bool) {
		if value, ok := edgeValue(edge, "repository")(property); ok {
			return value, true
		}

		anchorNode := r.e.nodes[anchor]
		repo := r.repositoryOf(&anchorNode)

		return repo, property == policy.PropertyValue && repo != ""
	}
	set[policy.DimensionEvidenceRole] = func(property string) (string, bool) {
		return string(edge.Role), property == policy.PropertyValue && edge.Role != ""
	}
	set[policy.DimensionContainer] = func(property string) (string, bool) {
		switch property {
		case policy.PropertyName:
			return edge.Occurrence, edge.Occurrence != ""
		case policy.PropertyImage:
			return edgeValue(edge, "image")(policy.PropertyValue)
		}

		return "", false
	}
	set[policy.DimensionWorkload] = func(property string) (string, bool) { return r.workloadProperty(anchor, bound, property) }
}

// factEnvironment gives the environment of a fact: its own, else that of the entity of the subject.
func (r *obligationRun) factEnvironment(anchor string, bound *BoundEdge) string {
	if env := bound.Edge.Environment; env != "" {
		return env
	}

	return r.e.nodes[anchor].Environment
}

// workloadProperty reads a property of the workload of the fact from its scoped identity: the subject-side entity
// when it is a resource, else the other endpoint. A part that the identity leaves unresolved is unknown.
func (r *obligationRun) workloadProperty(anchor string, bound *BoundEdge, property string) (string, bool) {
	candidates := []observation.ObservedNode{r.e.nodes[anchor], bound.From, bound.To}

	for i := range candidates {
		if candidates[i].ScopedID == "" || !isWorkload(&candidates[i]) {
			continue
		}

		scoped, err := observation.ParseScopedID(candidates[i].ScopedID)
		if err != nil {
			continue
		}

		var value string

		switch property {
		case policy.PropertyCluster:
			value = scoped.Cluster
		case policy.PropertyNamespace:
			value = scoped.Namespace
		case policy.PropertyKind:
			value = scoped.Kind
		case policy.PropertyName:
			value = scoped.Name
		default:
			return "", false
		}

		return value, value != "" && value != observation.UnresolvedPart
	}

	return "", false
}
