package observation

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// UnresolvedPart is the canonical text of a part of a ScopedID that cannot
// be determined from declared sources.
const UnresolvedPart = "~"

// EnvironmentUnresolved is the value of ObservedNode.Environment and
// EvidenceEdge.Environment for a fact that no declared environment owns.
// Concordia never replaces it by a default such as `production`.
const EnvironmentUnresolved = "unresolved"

const (
	scopedIDPrefix = "k8s:"
	scopedIDParts  = 6

	// objKeyParts is the number of parts of `<group>/<Kind>/<namespace>/<name>`.
	objKeyParts = 4

	// objKeyClusterScoped is the namespace part of a cluster-scoped kind.
	objKeyClusterScoped = "_"
	// objKeyUnresolved is the namespace part of a namespaced kind whose manifest declares none.
	objKeyUnresolved = "?"

	// qualifiedNameKeySeparator separates the file part from the obj_key in a Resource qualified name.
	qualifiedNameKeySeparator = "::"
)

// ErrInvalidObjKey means a provider obj_key is not `<group>/<Kind>/<namespace>/<name>`.
var ErrInvalidObjKey = errors.New("invalid obj_key")

// ErrInvalidScopedID means a canonical identity string cannot be parsed.
var ErrInvalidScopedID = errors.New("invalid scoped identity")

// ObjKey is a parsed provider obj_key (CBM 0.12.1 observations, O2).
type ObjKey struct {
	// Group is the API group, `core` for the core group.
	Group string
	// Kind is the resource kind.
	Kind string
	// Namespace is the declared namespace. Empty when ClusterScoped or NamespaceUnresolved.
	Namespace string
	// Name is the resource name.
	Name string
	// ClusterScoped is true when the namespace part is `_`.
	ClusterScoped bool
	// NamespaceUnresolved is true when the namespace part is `?`: a namespaced
	// kind whose manifest declares no namespace (Helm template, Kustomize base).
	NamespaceUnresolved bool
}

// ParseObjKey reads `<group>/<Kind>/<namespace>/<name>`. A `_` namespace means
// a cluster-scoped kind and gives no namespace. A `?` namespace means an
// undeclared namespace and stays unresolved; it is never defaulted.
func ParseObjKey(objKey string) (ObjKey, error) {
	parts := strings.SplitN(objKey, "/", objKeyParts)
	if len(parts) != objKeyParts {
		return ObjKey{}, fmt.Errorf("%w: %q has no <group>/<Kind>/<namespace>/<name> form", ErrInvalidObjKey, objKey)
	}

	for i, part := range parts {
		if part == "" {
			return ObjKey{}, fmt.Errorf("%w: %q has an empty part %d", ErrInvalidObjKey, objKey, i+1)
		}
	}

	key := ObjKey{Group: parts[0], Kind: parts[1], Name: parts[3]}

	switch parts[2] {
	case objKeyClusterScoped:
		key.ClusterScoped = true
	case objKeyUnresolved:
		key.NamespaceUnresolved = true
	default:
		key.Namespace = parts[2]
	}

	return key, nil
}

// ObjKeyFromQualifiedName gives the obj_key part of a Resource qualified name
// `<file path with dots>::<obj_key>`. The qualified name writes both a
// cluster-scoped kind and an undeclared namespace as `-` in the namespace
// part (O2), so the result for such a name has the namespace part `?`: the
// qualified name cannot tell the two apart, and Concordia never claims a kind
// is cluster scoped from it. Use the node `obj_key` property when it exists.
func ObjKeyFromQualifiedName(qualifiedName string) (string, bool) {
	_, key, ok := strings.Cut(qualifiedName, qualifiedNameKeySeparator)
	if !ok {
		return "", false
	}

	// Drop a `#env/...` suffix of an environment-variable node.
	key, _, _ = strings.Cut(key, "#")

	parts := strings.SplitN(key, "/", objKeyParts)
	if len(parts) != objKeyParts {
		return "", false
	}

	if parts[2] == "-" {
		parts[2] = objKeyUnresolved
	}

	return strings.Join(parts, "/"), true
}

// ScopedID is the identity of an observed deployed resource: the declared
// environment, the cluster or context, the API group, the kind, the namespace
// (if namespaced), and the name. A part that cannot be determined is
// UnresolvedPart; the namespace of a cluster-scoped kind is empty.
type ScopedID struct {
	Environment string
	Cluster     string
	APIGroup    string
	Kind        string
	Namespace   string
	Name        string
}

// NewScopedID makes the identity of the resource with the given obj_key. An
// empty environment, EnvironmentUnresolved, or an empty cluster gives
// UnresolvedPart. A cluster-scoped kind gets no namespace. An undeclared
// namespace stays UnresolvedPart.
func NewScopedID(environment, cluster string, key ObjKey) ScopedID {
	scoped := ScopedID{
		Environment: unresolvedIfEmpty(environment),
		Cluster:     unresolvedIfEmpty(cluster),
		APIGroup:    key.Group,
		Kind:        key.Kind,
		Name:        key.Name,
	}

	switch {
	case key.ClusterScoped:
		scoped.Namespace = ""
	case key.NamespaceUnresolved || key.Namespace == "":
		scoped.Namespace = UnresolvedPart
	default:
		scoped.Namespace = key.Namespace
	}

	if scoped.Environment == EnvironmentUnresolved {
		scoped.Environment = UnresolvedPart
	}

	return scoped
}

// ScopedIDFromObjKey parses objKey and makes its identity (see NewScopedID).
func ScopedIDFromObjKey(environment, cluster, objKey string) (ScopedID, error) {
	key, err := ParseObjKey(objKey)
	if err != nil {
		return ScopedID{}, err
	}

	return NewScopedID(environment, cluster, key), nil
}

func unresolvedIfEmpty(s string) string {
	if s == "" {
		return UnresolvedPart
	}

	return s
}

// WithNamespace gives the identity with a namespace that rendering or a
// binding supplied. It changes nothing for a cluster-scoped kind (its namespace
// stays empty) or for a namespace that is already resolved: a namespace is
// never added to a cluster-scoped kind and never replaced.
func (id *ScopedID) WithNamespace(namespace string) ScopedID {
	out := *id
	if out.Namespace == UnresolvedPart && namespace != "" {
		out.Namespace = namespace
	}

	return out
}

// ClusterScoped says whether the identity is of a cluster-scoped kind (an empty namespace).
func (id *ScopedID) ClusterScoped() bool { return id.Namespace == "" }

// EnvironmentResolved says whether the environment is known.
func (id *ScopedID) EnvironmentResolved() bool { return id.Environment != UnresolvedPart }

// ClusterResolved says whether the cluster is known.
func (id *ScopedID) ClusterResolved() bool { return id.Cluster != UnresolvedPart }

// NamespaceResolved says whether the namespace is known or not applicable.
func (id *ScopedID) NamespaceResolved() bool { return id.Namespace != UnresolvedPart }

// Resolved says whether every part of the identity is known.
func (id *ScopedID) Resolved() bool {
	return id.EnvironmentResolved() && id.ClusterResolved() && id.NamespaceResolved()
}

// String gives the canonical form
// `k8s:<env>/<cluster>/<group>/<Kind>/<ns>/<name>`. An unknown part is `~` and
// the namespace of a cluster-scoped kind is empty. A `/` or `%` inside a part
// is written as %2F or %25, and a part that is exactly `~` as %7E.
func (id ScopedID) String() string {
	return scopedIDPrefix + strings.Join([]string{
		escapeIDPart(id.Environment), escapeIDPart(id.Cluster), escapeIDPart(id.APIGroup),
		escapeIDPart(id.Kind), escapeIDPart(id.Namespace), escapeIDPart(id.Name),
	}, "/")
}

// ParseScopedID reads the canonical form that String makes.
func ParseScopedID(s string) (ScopedID, error) {
	rest, ok := strings.CutPrefix(s, scopedIDPrefix)
	if !ok {
		return ScopedID{}, fmt.Errorf("%w: %q does not start with %q", ErrInvalidScopedID, s, scopedIDPrefix)
	}

	parts := strings.Split(rest, "/")
	if len(parts) != scopedIDParts {
		return ScopedID{}, fmt.Errorf("%w: %q has %d parts, want %d", ErrInvalidScopedID, s, len(parts), scopedIDParts)
	}

	for i := range parts {
		parts[i] = unescapeIDPart(parts[i])
	}

	return ScopedID{
		Environment: parts[0], Cluster: parts[1], APIGroup: parts[2],
		Kind: parts[3], Namespace: parts[4], Name: parts[5],
	}, nil
}

var idPartEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

func escapeIDPart(s string) string {
	if s == UnresolvedPart {
		return s
	}

	s = idPartEscaper.Replace(s)
	if s == UnresolvedPart {
		return "%7E"
	}

	return s
}

var idPartUnescaper = strings.NewReplacer("%2F", "/", "%25", "%")

func unescapeIDPart(s string) string {
	if s == "%7E" {
		return UnresolvedPart
	}

	return idPartUnescaper.Replace(s)
}

// Declaration is one manifest declaration of a resource.
type Declaration struct {
	// ID is the scoped identity of the declared resource.
	ID ScopedID
	// NodeID is the observed ID of the node.
	NodeID string
	// Path is the repository-relative file of the declaration.
	Path string
	// Line is the 1-based line, 0 when unknown.
	Line int
}

// DeclarationGroup holds the declarations that share one ScopedID.
type DeclarationGroup struct {
	ID ScopedID
	// Declarations are ordered by path, line, and node ID.
	Declarations []Declaration
}

// Competing says whether two or more declarations have the identity.
func (g *DeclarationGroup) Competing() bool { return len(g.Declarations) > 1 }

// GroupDeclarations groups declarations by ScopedID, ordered by the canonical
// identity. The provider qualified name contains the file path, so two files
// that declare one obj_key are two nodes (O2); grouping by ScopedID is what
// finds them. The same obj_key in two environments gives two groups.
func GroupDeclarations(decls []Declaration) []DeclarationGroup {
	byID := map[ScopedID]*DeclarationGroup{}

	var order []ScopedID

	for i := range decls {
		g, ok := byID[decls[i].ID]
		if !ok {
			g = &DeclarationGroup{ID: decls[i].ID}
			byID[decls[i].ID] = g
			order = append(order, decls[i].ID)
		}

		g.Declarations = append(g.Declarations, decls[i])
	}

	groups := make([]DeclarationGroup, 0, len(order))

	for _, id := range order {
		group := byID[id]
		slices.SortFunc(group.Declarations, func(a, b Declaration) int { return compareDeclarations(&a, &b) })
		groups = append(groups, *group)
	}

	slices.SortFunc(groups, func(a, b DeclarationGroup) int {
		return canonjson.CompareUTF16(a.ID.String(), b.ID.String())
	})

	return groups
}

// compareDeclarations orders declarations by path, line, and node ID.
func compareDeclarations(a, b *Declaration) int {
	if c := canonjson.CompareUTF16(a.Path, b.Path); c != 0 {
		return c
	}

	if a.Line != b.Line {
		return a.Line - b.Line
	}

	return canonjson.CompareUTF16(a.NodeID, b.NodeID)
}

// Location is a place where a declaration is.
type Location struct {
	NodeID string
	Path   string
	Line   int
}

// Conflict records competing declarations of one resource identity with every location.
// Competing declarations are not instances: they stay competing until
// rendering, ownership, or an explicit precedence rule resolves them.
type Conflict struct {
	ID        ScopedID
	Locations []Location
	// Certain is true when the environment and the namespace are known, so
	// the declarations are provably the same resource in the same environment.
	// When it is false the declarations share an identity that still has an
	// unresolved environment or namespace, and may be different resources.
	Certain bool
}

// Conflicts gives one record for each group of competing declarations, in the
// order of groups.
func Conflicts(groups []DeclarationGroup) []Conflict {
	var out []Conflict

	for i := range groups {
		g := &groups[i]
		if !g.Competing() {
			continue
		}

		c := Conflict{ID: g.ID, Certain: g.ID.EnvironmentResolved() && g.ID.NamespaceResolved()}
		for j := range g.Declarations {
			d := &g.Declarations[j]
			c.Locations = append(c.Locations, Location{NodeID: d.NodeID, Path: d.Path, Line: d.Line})
		}

		out = append(out, c)
	}

	return out
}

// Prefixes of the qualified names of project-wide nodes that carry no file
// scope in the provider graph (O3, O7, O10, O9).
const (
	imageQNPrefix  = "__image__"
	routeQNPrefix  = "__route__"
	envVarQNPrefix = "__env__"
	chartQNPrefix  = "__chart__"
)

// IsUnscopedNode says whether a node is project wide: an `Image` node (one
// per repository, no tag or digest, O3), a `Route` node (`__route__<via>__<host><path>`
// has no project file or namespace, O7), a project-wide `EnvVar` node
// (`__env__<NAME>`, O10), or an external `Chart` node. Concordia gives such a
// node no environment; a fact gets its environment from a scoped endpoint. The
// file path of a Route or Chart node is only the first declaration that the
// provider saw, so it is ignored.
func IsUnscopedNode(label, qualifiedName string) bool {
	if label == LabelImage || label == labelRoute {
		return true
	}

	for _, prefix := range []string{imageQNPrefix, routeQNPrefix, envVarQNPrefix, chartQNPrefix} {
		if strings.HasPrefix(qualifiedName, prefix) {
			return true
		}
	}

	return false
}

// Decision is an `ADR` node. Its identity is the repository-relative file
// path. AdrID and SupersededBy are kept exactly as the provider emitted them:
// `adr_id` is `ADR-1` for the file `0001-...` and `superseded_by` is the link
// target text (for example `0002-use-managed-postgres.md`), not an ADR ID. They
// are never rewritten and never used as identity.
type Decision struct {
	Path         string
	AdrID        string
	Title        string
	Status       string
	StatusText   string
	Date         string
	SupersededBy string
}

// ErrNoDecisionPath means an ADR node has no file path to identify it.
var ErrNoDecisionPath = errors.New("decision has no file path")

// DecisionOf reads the properties of an `ADR` node at the repository-relative
// file path.
func DecisionOf(filePath string, props map[string]string) (Decision, error) {
	if filePath == "" {
		return Decision{}, ErrNoDecisionPath
	}

	return Decision{
		Path:         filePath,
		AdrID:        props["adr_id"],
		Title:        props["title"],
		Status:       props["status"],
		StatusText:   props["status_text"],
		Date:         props["date"],
		SupersededBy: props["superseded_by"],
	}, nil
}

// SynthID gives the synthetic observed ID of the decision.
func (d *Decision) SynthID() string { return ADRSynthID(d.Path) }

// DiagramElementID identifies a diagram element by the diagram file, the
// section that holds the diagram block, and the author `element_id`. It does
// not use the ordinal in the diagram qualified name, so reordering the blocks
// of a file keeps the identity.
type DiagramElementID struct {
	File      string
	Section   string
	ElementID string
}

// ErrDiagramQualifiedName means a diagram qualified name has no section.
var ErrDiagramQualifiedName = errors.New("invalid diagram qualified name")

const diagramOrdinalMarker = "#diagram-"

// DiagramElementOf identifies a diagram element. diagramQualifiedName is the
// qualified name of the `Diagram` node (`parent_class` of the element),
// `<file path without extension, "/" as ".">.<section>#diagram-<ordinal>`,
// without the project prefix; file is the diagram file, repository relative.
func DiagramElementOf(file, diagramQualifiedName, elementID string) (DiagramElementID, error) {
	stem := strings.ReplaceAll(strings.TrimSuffix(file, path.Ext(file)), "/", ".") + "."

	rest, ok := strings.CutPrefix(diagramQualifiedName, stem)
	if !ok {
		return DiagramElementID{}, fmt.Errorf("%w: %q does not start with %q", ErrDiagramQualifiedName, diagramQualifiedName, stem)
	}

	section, ordinal, ok := strings.Cut(rest, diagramOrdinalMarker)
	if !ok || section == "" {
		return DiagramElementID{}, fmt.Errorf("%w: %q has no section and %s<ordinal>", ErrDiagramQualifiedName, diagramQualifiedName, diagramOrdinalMarker)
	}

	if _, err := strconv.Atoi(ordinal); err != nil {
		return DiagramElementID{}, fmt.Errorf("%w: ordinal %q: %w", ErrDiagramQualifiedName, ordinal, err)
	}

	if elementID == "" {
		return DiagramElementID{}, fmt.Errorf("%w: no element_id", ErrDiagramQualifiedName)
	}

	return DiagramElementID{File: file, Section: section, ElementID: elementID}, nil
}

// String gives `<file>#<section>#<element_id>`.
func (d DiagramElementID) String() string {
	return d.File + "#" + d.Section + "#" + d.ElementID
}

// SynthID gives the synthetic observed ID of the element.
func (d DiagramElementID) SynthID() string {
	return DiagramElementSynthID(d.File, d.Section, d.ElementID)
}

// Prefixes of synthesized supporting entities. They are observed entities with
// resolution `unbound` until a binding refers to them.
const (
	synthPrefix               = "synth:"
	synthImagePrefix          = synthPrefix + "image:"
	synthADRPrefix            = synthPrefix + "adr:"
	synthDiagramElementPrefix = synthPrefix + "diagram-element:"
)

// SynthPrefix starts the observed ID of each synthesized entity.
func SynthPrefix() string { return synthPrefix }

// ImageSynthID gives the observed ID of the image entity of one repository.
func ImageSynthID(repository string) string { return synthImagePrefix + repository }

// ADRSynthID gives the observed ID of the decision at a repository-relative file path.
func ADRSynthID(filePath string) string { return synthADRPrefix + filePath }

// DiagramElementSynthID gives the observed ID of a diagram element.
func DiagramElementSynthID(file, section, elementID string) string {
	return synthDiagramElementPrefix + file + "#" + section + "#" + elementID
}

// ObjKeyOf gives the obj_key of a `Resource` node: the property `obj_key` when
// the node has it, else the key in the qualified name (see ObjKeyFromQualifiedName).
// The bool is false for a node with no obj_key, for example a Kustomization or a Compose service.
func ObjKeyOf(n *ObservedNode) (string, bool) {
	if n.Label != labelResource {
		return "", false
	}

	if key := n.Properties["obj_key"]; key != "" {
		return key, true
	}

	return ObjKeyFromQualifiedName(n.QualifiedName)
}

// ResourceKindOf gives the Kubernetes kind of a `Resource` node: the provider
// `kind` property when the adapter read it, else the kind in the obj_key. The
// adapter reads `kind` only for nodes that are the target of a reference edge,
// so a workload that nothing references has no Kind but still has its obj_key.
// It is "" for a node with neither.
func ResourceKindOf(n *ObservedNode) string {
	if n.Kind != "" {
		return n.Kind
	}

	key, ok := ObjKeyOf(n)
	if !ok {
		return ""
	}

	parsed, err := ParseObjKey(key)
	if err != nil {
		return ""
	}

	return parsed.Kind
}

// ScopedIDOf gives the identity of a `Resource` node. The environment comes
// from the node (AttributeEnvironments sets it); the cluster is the cluster of
// that environment as the workspace declares it, empty when it declares none.
// The bool is false for a node with no obj_key. The error is not nil for an
// obj_key that is malformed.
func ScopedIDOf(n *ObservedNode, cluster string) (ScopedID, bool, error) {
	key, ok := ObjKeyOf(n)
	if !ok {
		return ScopedID{}, false, nil
	}

	id, err := ScopedIDFromObjKey(n.Environment, cluster, key)
	if err != nil {
		return ScopedID{}, false, err
	}

	return id, true, nil
}

// DeclarationsOf makes the declarations of the `Resource` nodes that have an
// obj_key, ordered by observed ID. clusterOf gives the cluster of an
// environment id (nil or an empty result means no cluster is declared).
func DeclarationsOf(nodes map[string]ObservedNode, clusterOf func(environment string) string) ([]Declaration, error) {
	return declarationsOf(nodes, clusterOf, true)
}

// DeclarationsOfValid is DeclarationsOf for a graph that a provider owns: a node with a malformed obj_key is not a
// declaration and is skipped, so that it does not hide the competing declarations of the other nodes.
func DeclarationsOfValid(nodes map[string]ObservedNode, clusterOf func(environment string) string) []Declaration {
	out, _ := declarationsOf(nodes, clusterOf, false)

	return out
}

func declarationsOf(nodes map[string]ObservedNode, clusterOf func(environment string) string, strict bool) ([]Declaration, error) {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	var out []Declaration

	for _, nodeID := range ids {
		node := nodes[nodeID]

		decl, ok, err := declarationOfNode(nodeID, &node, clusterOf)
		if err != nil && strict {
			return nil, err
		}

		if ok {
			out = append(out, decl)
		}
	}

	return out, nil
}

// declarationOfNode makes the declaration of one node. The bool is false for
// a node with no obj_key or a malformed one; the error reports the latter.
func declarationOfNode(nodeID string, node *ObservedNode, clusterOf func(environment string) string) (Declaration, bool, error) {
	scoped, ok, err := ScopedIDOf(node, clusterOfNode(node, clusterOf))
	if err != nil {
		return Declaration{}, false, fmt.Errorf("node %s: %w", nodeID, err)
	}

	if !ok {
		return Declaration{}, false, nil
	}

	return Declaration{ID: scoped, NodeID: nodeID, Path: node.FilePath, Line: node.StartLine}, true, nil
}

// DecisionOfNode reads an `ADR` node: its file path and its properties kept verbatim.
func DecisionOfNode(n *ObservedNode) (Decision, error) {
	return DecisionOf(n.FilePath, n.Properties)
}

// DiagramElementOfNode identifies a `DiagramElement` node. The qualified name of
// the node is `<diagram qualified name>.<element_id>`.
func DiagramElementOfNode(n *ObservedNode) (DiagramElementID, error) {
	elementID := n.Properties["element_id"]

	diagram, ok := strings.CutSuffix(n.QualifiedName, "."+elementID)
	if !ok || elementID == "" {
		return DiagramElementID{}, fmt.Errorf("%w: %q has no element_id %q", ErrDiagramQualifiedName, n.QualifiedName, elementID)
	}

	return DiagramElementOf(n.FilePath, diagram, elementID)
}

// Native node labels of the infrastructure nodes that identity and attribution read.
const (
	labelResource = "Resource"
	labelChart    = "Chart"
	labelRoute    = "Route"
	labelEnvVar   = "EnvVar"
)

// AnnotateScopedIDs sets ScopedID on each `Resource` node that has an obj_key.
// clusterOf gives the cluster of an environment id (nil or empty: none). A
// malformed obj_key leaves the node without a ScopedID.
func AnnotateScopedIDs(nodes map[string]ObservedNode, clusterOf func(environment string) string) {
	for nodeID := range nodes {
		node := nodes[nodeID]

		scoped, ok, err := ScopedIDOf(&node, clusterOfNode(&node, clusterOf))
		if err != nil || !ok {
			continue
		}

		node.ScopedID = scoped.String()
		nodes[nodeID] = node
	}
}

// clusterOfNode gives the cluster of the environment of a node, empty when
// the node has no resolved environment or clusterOf is nil.
func clusterOfNode(node *ObservedNode, clusterOf func(environment string) string) string {
	if clusterOf == nil || !environmentDeclared(node.Environment) {
		return ""
	}

	return clusterOf(node.Environment)
}

// environmentDeclared says whether an environment id names a declared environment.
func environmentDeclared(environment string) bool {
	return environment != "" && environment != EnvironmentUnresolved
}
