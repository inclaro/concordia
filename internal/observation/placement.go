package observation

import (
	"maps"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
)

const (
	// PlacementRule is the derivation rule of a `placed-in` fact.
	PlacementRule = "placement-from-namespace"
	// PlacementRuleVersion is the version of PlacementRule.
	PlacementRuleVersion = 1

	synthNamespacePrefix = synthPrefix + "namespace:"
	kindNamespace        = "Namespace"
	templateMarker       = "{{"
)

// NamespaceSynthID gives the observed ID of the synthesized Namespace entity of
// an environment.
func NamespaceSynthID(environment, name string) string {
	return synthNamespacePrefix + environment + "/" + name
}

// DerivePlacement adds one `placed-in` fact for each workload `Resource` node
// whose namespace is a name and whose own name is not a template. The fact runs
// from the workload to its hosting target: the Namespace `Resource` node of the
// same environment when a manifest there declares it, else a synthesized
// Namespace entity (resolution `unbound`) that it appends to Entities. The fact
// has the role `parsed-source-fact`, resolution `exact`, confidence
// `syntactic`, the environment and manifest location of the workload node, and a
// derivation record (rule PlacementRule, its version, the workload node ID).
// A namespace `?` or `_`, a templated name, and a kind that is not a workload
// derive nothing. No deployment action is an input: only nodes are read.
//
// Call it after AttributeEnvironments and AnnotateScopedIDs. clusterOf gives
// the cluster of an environment (nil: none) for the identity of a synthesized
// entity. The edges and entities stay sorted. The result is the number of
// facts that it added.
func DerivePlacement(result *EvidenceQueryResult, clusterOf func(string) string) int {
	if result == nil || len(result.Nodes) == 0 {
		return 0
	}

	ids := sortedKeys(result.Nodes)

	deriver := &placementDeriver{
		nodes:      result.Nodes,
		namespaces: namespaceNodes(result.Nodes, ids),
		template:   placementTemplate(result),
		clusterOf:  clusterOf,
		entities:   map[string]ObservedNode{},
	}

	added := make([]*EvidenceEdge, 0)

	for _, nodeID := range ids {
		if edge, ok := deriver.edgeFor(nodeID); ok {
			added = append(added, edge)
		}
	}

	if len(added) == 0 {
		return 0
	}

	result.Edges = append(result.Edges, added...)
	SortEdges(result.Edges, result.Nodes)

	result.Entities = slices.AppendSeq(result.Entities, maps.Values(deriver.entities))

	slices.SortFunc(result.Entities, func(left, right ObservedNode) int {
		return canonjson.CompareUTF16(left.ObservedID, right.ObservedID)
	})

	return len(added)
}

// placementDeriver derives the placement facts of the workload nodes of a result.
type placementDeriver struct {
	nodes      map[string]ObservedNode
	namespaces map[namespaceKey]string
	clusterOf  func(string) string
	// entities are the synthesized Namespace entities, by observed ID.
	entities map[string]ObservedNode
	template EvidenceEdge
}

// edgeFor derives the placement fact of one node. The bool is false for a
// node with no placement.
func (d *placementDeriver) edgeFor(nodeID string) (*EvidenceEdge, bool) {
	node := d.nodes[nodeID]

	placement, ok := workloadPlacementOf(&node)
	if !ok {
		return nil, false
	}

	edge := d.template
	edge.ID = placementID(nodeID, placement)
	edge.From = nodeID
	edge.To = d.targetOf(&node, placement.namespace)
	edge.SourceLabel = node.Label
	edge.TargetLabel = LabelNamespace
	edge.Environment = node.Environment
	edge.Properties = map[string]string{}
	edge.Derivation = &Derivation{Rule: PlacementRule, Version: PlacementRuleVersion, Inputs: []string{nodeID}}

	if node.FilePath != "" {
		edge.SourceSpan = &SourceSpan{Path: node.FilePath, Line: node.StartLine}
	}

	return &edge, true
}

// targetOf gives the observed ID of the namespace that hosts a workload: the
// declared Namespace node, else a synthesized entity.
func (d *placementDeriver) targetOf(node *ObservedNode, namespace string) string {
	key := namespaceKey{node.Environment, namespace}
	if target, known := d.namespaces[key]; known {
		return target
	}

	target := NamespaceSynthID(key.environment, key.name)
	if _, seen := d.entities[target]; !seen {
		d.entities[target] = synthesizedNamespace(key, d.clusterOf)
	}

	return target
}

// placementTemplate gives the parts that every placement fact shares. The graph
// generation and the analyzer are those of the fetched facts.
func placementTemplate(result *EvidenceQueryResult) EvidenceEdge {
	edge := EvidenceEdge{
		Kind:                 KindPlacedIn,
		Confidence:           ConfidenceSyntactic,
		Role:                 RoleParsedSourceFact,
		Resolution:           ResolutionExact,
		ResolutionMethod:     "derived:" + PlacementRule,
		NormalizationVersion: NormalizationVersion,
	}

	if len(result.Edges) > 0 {
		edge.GraphGeneration = result.Edges[0].GraphGeneration
		edge.Analyzer = result.Edges[0].Analyzer
	}

	return edge
}

// namespaceKey is the identity of a Namespace node in an environment.
type namespaceKey struct{ environment, name string }

// namespaceKeyOf gives the key of a Namespace `Resource` node that an
// environment declares. The bool is false for any other node.
func namespaceKeyOf(node *ObservedNode) (namespaceKey, bool) {
	if node.Label != labelResource || node.Environment == EnvironmentUnresolved {
		return namespaceKey{}, false
	}

	if ResourceKindOf(node) != kindNamespace {
		return namespaceKey{}, false
	}

	key, ok := ObjKeyOf(node)
	if !ok {
		return namespaceKey{}, false
	}

	parsed, err := ParseObjKey(key)
	if err != nil || parsed.Kind != kindNamespace {
		return namespaceKey{}, false
	}

	return namespaceKey{node.Environment, parsed.Name}, true
}

// namespaceNodes finds the Namespace `Resource` nodes by environment and name.
// Of several declarations the one with the smallest observed ID is the target.
func namespaceNodes(nodes map[string]ObservedNode, sortedIDs []string) map[namespaceKey]string {
	out := map[namespaceKey]string{}

	for _, nodeID := range sortedIDs {
		node := nodes[nodeID]

		key, ok := namespaceKeyOf(&node)
		if !ok {
			continue
		}

		if _, exists := out[key]; !exists {
			out[key] = nodeID
		}
	}

	return out
}

// workloadPlacement is the name and the namespace of a workload.
type workloadPlacement struct{ name, namespace string }

// hasPlacementNamespace says whether an object key names a namespace that a placement can use.
func hasPlacementNamespace(parsed *ObjKey) bool {
	if parsed.ClusterScoped || parsed.NamespaceUnresolved {
		return false
	}

	return parsed.Namespace != ""
}

// hasTemplateMarker says whether the name or the namespace of an object key is a template.
func hasTemplateMarker(parsed *ObjKey) bool {
	return strings.Contains(parsed.Name, templateMarker) || strings.Contains(parsed.Namespace, templateMarker)
}

// workloadPlacementOf gives the name and the namespace of a workload node that
// has a placement. The bool is false for any other node.
func workloadPlacementOf(node *ObservedNode) (workloadPlacement, bool) {
	if node.Label != labelResource || !slices.Contains(workloadKinds, ResourceKindOf(node)) {
		return workloadPlacement{}, false
	}

	key, found := ObjKeyOf(node)
	if !found {
		return workloadPlacement{}, false
	}

	parsed, err := ParseObjKey(key)
	if err != nil || !hasPlacementNamespace(&parsed) || hasTemplateMarker(&parsed) {
		return workloadPlacement{}, false
	}

	return workloadPlacement{name: parsed.Name, namespace: parsed.Namespace}, true
}

// placementID gives the ID of a placement fact. It follows the workload node:
// the instance of a node in another environment (`<id>@<environment>`) gives a
// fact with the same suffix.
func placementID(nodeID string, placement workloadPlacement) string {
	base, suffix, _ := strings.Cut(nodeID, envSuffixSeparator)

	sum := digest.SHA256HexString(strings.Join([]string{string(KindPlacedIn), base, placement.namespace, placement.name}, "\x00"))
	id := "cbm-edge:" + sum[:edgeIDHexLength]

	if suffix != "" {
		id += envSuffixSeparator + suffix
	}

	return id
}

// clusterFor gives the cluster of an environment, or "" when there is none.
func clusterFor(environment string, clusterOf func(string) string) string {
	if clusterOf == nil || environment == "" {
		return ""
	}

	if environment == EnvironmentUnresolved {
		return ""
	}

	return clusterOf(environment)
}

// synthesizedNamespace makes the hosting target of a namespace that no
// manifest of the environment declares. A binding `infraResource` for
// `core/Namespace/_/<name>` of the environment addresses it.
func synthesizedNamespace(key namespaceKey, clusterOf func(string) string) ObservedNode {
	objKey := "core/" + kindNamespace + "/" + objKeyClusterScoped + "/" + key.name

	entity := ObservedNode{
		ObservedID:    NamespaceSynthID(key.environment, key.name),
		Label:         LabelNamespace,
		QualifiedName: objKey,
		Environment:   key.environment,
		Kind:          kindNamespace,
		Resolution:    ResolutionUnbound,
		Properties:    map[string]string{"obj_key": objKey},
	}

	cluster := clusterFor(key.environment, clusterOf)
	if scoped, err := ScopedIDFromObjKey(key.environment, cluster, objKey); err == nil {
		entity.ScopedID = scoped.String()
	}

	return entity
}
