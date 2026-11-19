package binding

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

const (
	labelResource = "Resource"
	// imageNodePrefix starts the qualified name of a provider `Image` node.
	imageNodePrefix = "__image__"
	// labelFile is the label of a source file node.
	labelFile = "File"
	// The match fields of a deployed resource, as the binding files and the image properties name them.
	fieldEnvironment  = "environment"
	fieldCluster      = "cluster"
	fieldAPIGroup     = "apiGroup"
	fieldResourceKind = "resourceKind"
	fieldNamespace    = "namespace"
	fieldName         = "name"
	fieldRepository   = "repository"
	fieldDigest       = "digest"
	fieldAddress      = "address"
	fieldFile         = "file"
	fieldService      = "service"
)

// routeKinds are the resource kinds that a `route` match accepts when it names no resourceKind.
var routeKinds = []string{"Ingress", "HTTPRoute", "GRPCRoute", "TCPRoute", "TLSRoute", "UDPRoute", "Route", "VirtualService"}

// AgreementOf gives the agreement of the deployed-resource bindings of a CALM
// element for typed obligations: AgreementAny when every binding that bound a
// deployed resource to it says `any` (one bound entity that satisfies the
// requirement is enough), else AgreementAll (the default: every bound entity must).
func (s *Set) AgreementOf(calmID string) string {
	if a, ok := s.Agreements[calmID]; ok {
		return a
	}

	return AgreementAll
}

// newDeployedMatcher builds the matcher of a deployed-resource match. The bool is false for the other kinds.
func newDeployedMatcher(sel *Match, cfg *resolveConfig) (matcher, bool) {
	switch sel.Kind {
	case MatchWorkload:
		return scopedMatcher(sel, cfg, tierWorkload, func(kind string) bool { return kind == sel.ResourceKind }), true
	case MatchRoute:
		return scopedMatcher(sel, cfg, tierRoute, func(kind string) bool {
			if sel.ResourceKind != "" {
				return kind == sel.ResourceKind
			}

			return slices.Contains(routeKinds, kind)
		}), true
	case MatchInfraResource:
		return infraMatcher(sel, cfg), true
	case MatchComposeService:
		return composeMatcher(sel), true
	case MatchImage:
		return imageMatcher(sel), true
	case MatchSourceRoot, MatchPackage, MatchQualifiedName, MatchBuildTarget, MatchOtelService:
		return matcher{}, false
	}

	return matcher{}, false
}

// environmentOK says whether n is in the environment that the match names. A
// match with an environment never matches an entity with no environment or the
// environment "unresolved".
func environmentOK(sel *Match, n *observation.ObservedNode) bool {
	return sel.Environment == "" || n.Environment == sel.Environment
}

// scopedOf gives the scoped identity of a `Resource` node with an obj_key.
func scopedOf(n *observation.ObservedNode, cfg *resolveConfig) (observation.ScopedID, bool) {
	if n.Label != labelResource {
		return observation.ScopedID{}, false
	}

	id, ok, err := observation.ScopedIDOf(n, cfg.clusterFor(n.Environment))

	return id, ok && err == nil
}

func clusterOK(sel *Match, id *observation.ScopedID) bool {
	return sel.Cluster == "" || id.Cluster == sel.Cluster
}

func countSet(values ...string) int {
	count := 0

	for _, v := range values {
		if v != "" {
			count++
		}
	}

	return count
}

// scopedMatcher matches a Kubernetes resource by its scoped identity: the
// environment, the optional cluster, API group and namespace, the kind, and the name.
func scopedMatcher(sel *Match, cfg *resolveConfig, tier int, kindOK func(kind string) bool) matcher {
	return matcher{
		spec: specificity{tier, countSet(sel.Cluster, sel.APIGroup, sel.Namespace, sel.ResourceKind)},
		test: func(n *observation.ObservedNode, _ []string) bool {
			if !environmentOK(sel, n) {
				return false
			}

			id, ok := scopedOf(n, cfg)

			return ok && scopedIDOK(sel, &id, kindOK)
		},
	}
}

// scopedIDOK says whether a scoped identity has the cluster, API group,
// kind, name, and namespace that the match names; an unnamed optional field matches any.
func scopedIDOK(sel *Match, id *observation.ScopedID, kindOK func(kind string) bool) bool {
	return clusterOK(sel, id) &&
		(sel.APIGroup == "" || id.APIGroup == sel.APIGroup) &&
		kindOK(id.Kind) && id.Name == sel.Name &&
		(sel.Namespace == "" || id.Namespace == sel.Namespace)
}

// infraMatcher matches an infrastructure resource, including a Kubernetes
// namespace used as a hosting target, by its address: the provider obj_key
// `<group>/<Kind>/<namespace>/<name>` (for example `core/Namespace/_/payments`),
// the `address` property, or the qualified name.
func infraMatcher(sel *Match, cfg *resolveConfig) matcher {
	return matcher{
		spec: specificity{tierInfraResource, countSet(sel.Cluster)},
		test: func(node *observation.ObservedNode, _ []string) bool {
			return infraCandidateOK(sel, cfg, node) && infraAddressOK(sel, node)
		},
	}
}

// infraCandidateOK says whether a node may be an infrastructure resource of
// the environment and cluster that the match names.
func infraCandidateOK(sel *Match, cfg *resolveConfig, node *observation.ObservedNode) bool {
	if !environmentOK(sel, node) || isFileOrImage(node) {
		return false
	}

	return sel.Cluster == "" || cfg.clusterFor(node.Environment) == sel.Cluster
}

func isFileOrImage(node *observation.ObservedNode) bool {
	return node.Label == labelFile || node.Label == observation.LabelImage
}

// infraAddressOK says whether the obj_key, the `address` property, or the
// qualified name of a node is the address that the match names.
func infraAddressOK(sel *Match, node *observation.ObservedNode) bool {
	if key, ok := observation.ObjKeyOf(node); ok && key == sel.Address {
		return true
	}

	return node.Properties[fieldAddress] == sel.Address || node.QualifiedName == sel.Address
}

// composeMatcher matches a Compose service of a Compose file.
func composeMatcher(sel *Match) matcher {
	return matcher{
		spec: specificity{tierComposeService, countSet(sel.Environment)},
		test: func(n *observation.ObservedNode, _ []string) bool {
			if n.Label != labelResource || n.FilePath != sel.File || !environmentOK(sel, n) {
				return false
			}

			return composeServiceOK(n, sel.Service)
		},
	}
}

// composeServiceOK says whether a resource node is the Compose service
// service: by its `service` property, else by the name of its `compose/Service` obj_key.
func composeServiceOK(n *observation.ObservedNode, service string) bool {
	if declared := n.Properties[fieldService]; declared != "" {
		return declared == service
	}

	key, ok := observation.ObjKeyOf(n)
	if !ok {
		return false
	}

	parsed, err := observation.ParseObjKey(key)

	return err == nil && parsed.Group == "compose" && parsed.Kind == "Service" && parsed.Name == service
}

// imageRepositoryOf gives the repository of an `Image` node or entity.
func imageRepositoryOf(n *observation.ObservedNode) (string, bool) {
	if n.Label != observation.LabelImage {
		return "", false
	}

	if repo := n.Properties["repository"]; repo != "" {
		return repo, true
	}

	repo := strings.TrimPrefix(n.QualifiedName, imageNodePrefix)

	return repo, repo != ""
}

// imageMatcher matches an `Image` node or entity by repository, and by digest
// when the match names one. The repository may be written with a tag or digest;
// only its repository part is used. The tier is higher with a digest.
func imageMatcher(sel *Match) matcher {
	repository := sel.Repository
	if ref, err := observation.ParseImageRef(repository); err == nil {
		repository = ref.Repository
	}

	tier := tierImageRepo
	if sel.Digest != "" {
		tier = tierImageDigest
	}

	return matcher{
		spec: specificity{tier, countSet(sel.Environment)},
		test: func(n *observation.ObservedNode, _ []string) bool {
			repo, ok := imageRepositoryOf(n)
			if !ok || repo != repository || !environmentOK(sel, n) {
				return false
			}

			return sel.Digest == "" || n.Properties[fieldDigest] == sel.Digest
		},
	}
}

// ImageDigestNodes makes one `Image` entity for each repository and immutable
// digest that an occurrence edge (`uses-image` and the like) references, so
// that an `image` binding with a digest has an entity to match. The tag and the
// digest come from the `image` property of the edge, never from the node (O3).
// An entity has the environment and the first source location of its edges, the
// synthetic ID `synth:image:<repository>@<digest>` (with `#<environment>` when the edges have one, so that
// one digest in two environments gives two entities that bind and report apart), and resolution `unbound`.
// The result is sorted by observed ID.
func ImageDigestNodes(edges []*observation.EvidenceEdge) []observation.ObservedNode {
	byID := map[string]observation.ObservedNode{}

	for _, edge := range edges {
		node, ok := imageDigestNodeOf(edge)
		if !ok {
			continue
		}

		// One entity for each repository, digest, and environment: the ID tells them apart.
		if known, found := byID[node.ObservedID]; found && !locationBefore(&node, &known) {
			continue
		}

		byID[node.ObservedID] = node
	}

	out := make([]observation.ObservedNode, 0, len(byID))
	for id := range byID {
		out = append(out, byID[id])
	}

	slices.SortFunc(out, func(a, b observation.ObservedNode) int {
		return canonjson.CompareUTF16(a.ObservedID, b.ObservedID)
	})

	return out
}

// imageDigestNodeOf makes the `Image` entity of one occurrence edge. The bool
// is false when the edge is no occurrence or its image has no digest.
func imageDigestNodeOf(edge *observation.EvidenceEdge) (observation.ObservedNode, bool) {
	occurrence, ok, err := observation.ContainerOccurrenceOf(edge)
	if err != nil || !ok || occurrence.Image.Digest == "" {
		return observation.ObservedNode{}, false
	}

	ref := occurrence.Image

	nodeID := observation.ImageSynthID(ref.Repository) + "@" + ref.Digest
	if edge.Environment != "" {
		nodeID += "#" + edge.Environment
	}

	node := observation.ObservedNode{
		ObservedID: nodeID, Label: observation.LabelImage, QualifiedName: ref.Repository, Environment: edge.Environment,
		Resolution: observation.ResolutionUnbound,
		Properties: map[string]string{fieldRepository: ref.Repository, fieldDigest: ref.Digest},
	}

	if edge.SourceSpan != nil {
		node.FilePath, node.StartLine = edge.SourceSpan.Path, edge.SourceSpan.Line
	}

	return node, true
}

// locationBefore says whether first has a smaller (path, line) than other; a node with a path is before one without.
func locationBefore(first, other *observation.ObservedNode) bool {
	if first.FilePath == "" {
		return false
	}

	if other.FilePath == "" {
		return true
	}

	if first.FilePath != other.FilePath {
		return first.FilePath < other.FilePath
	}

	return first.StartLine < other.StartLine
}

// agreementTracker folds the agreement of the bindings that bound deployed resources, by CALM ID.
type agreementTracker struct {
	// any is true for a CALM ID while every binding seen says `any`.
	any map[string]bool
}

func (t *agreementTracker) add(b *Binding) {
	if b.Agreement == AgreementAny {
		if _, seen := t.any[b.ArchitectureRef]; !seen {
			t.any[b.ArchitectureRef] = true
		}

		return
	}

	t.any[b.ArchitectureRef] = false
}

func (t *agreementTracker) result() map[string]string {
	out := make(map[string]string, len(t.any))

	for calmID, isAny := range t.any {
		if isAny {
			out[calmID] = AgreementAny
		} else {
			out[calmID] = AgreementAll
		}
	}

	return out
}

// CardinalityBreach is a binding with cardinality `one` that matched more than
// one entity in one environment.
type CardinalityBreach struct {
	BindingID string
	// Environment is the environment of the entities, empty when they have none.
	Environment string
	// Entities are the identities of the matched entities (sorted), for example the scoped workload identities.
	Entities []string
	// ObservedIDs are the observed IDs of the nodes of the entities (sorted).
	ObservedIDs []string
}

func (b *CardinalityBreach) detail() diag.Params {
	scope := ""
	if b.Environment != "" {
		scope = " in environment " + b.Environment
	}

	return diag.Detail(fmt.Sprintf("binding %s has cardinality one and matched %d entities%s: %s",
		b.BindingID, len(b.Entities), scope, strings.Join(b.Entities, ", ")))
}

// cardinalityTracker collects the entities that each binding with cardinality
// `one` matches, by environment. An entity is a scoped resource identity, an
// image repository, or the observed node: two declarations of one resource,
// or the provider node and the synthesized entity of one image, are one entity.
type cardinalityTracker struct {
	cfg *resolveConfig
	// one marks the bindings that have cardinality `one`.
	one []bool
	// found: binding index → environment → entity → observed IDs.
	found map[int]map[string]map[string][]string
	ids   []string
}

func newCardinalityTracker(bindings []Binding, cfg *resolveConfig) *cardinalityTracker {
	t := &cardinalityTracker{cfg: cfg, one: make([]bool, len(bindings)), found: map[int]map[string]map[string][]string{}}
	t.ids = make([]string, len(bindings))

	for i := range bindings {
		t.one[i] = bindings[i].Cardinality == CardinalityOne && bindings[i].Status != StatusProposed
		t.ids[i] = bindings[i].ID
	}

	return t
}

func (t *cardinalityTracker) record(i int, node *observation.ObservedNode) {
	if !t.one[i] {
		return
	}

	byEnv := t.found[i]
	if byEnv == nil {
		byEnv = map[string]map[string][]string{}
		t.found[i] = byEnv
	}

	entities := byEnv[node.Environment]
	if entities == nil {
		entities = map[string][]string{}
		byEnv[node.Environment] = entities
	}

	key := entityKey(node, t.cfg)
	entities[key] = append(entities[key], node.ObservedID)
}

func (t *cardinalityTracker) breaches() []CardinalityBreach {
	out := []CardinalityBreach{}

	for i, byEnv := range t.found {
		for env, entities := range byEnv {
			if len(entities) < 2 {
				continue
			}

			breach := CardinalityBreach{BindingID: t.ids[i], Environment: env}

			for key, observed := range entities {
				breach.Entities = append(breach.Entities, key)
				breach.ObservedIDs = append(breach.ObservedIDs, observed...)
			}

			slices.SortFunc(breach.Entities, canonjson.CompareUTF16)
			slices.SortFunc(breach.ObservedIDs, canonjson.CompareUTF16)
			out = append(out, breach)
		}
	}

	slices.SortFunc(out, func(a, b CardinalityBreach) int {
		if c := canonjson.CompareUTF16(a.BindingID, b.BindingID); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Environment, b.Environment)
	})

	return out
}

// entityKey gives the identity of the entity that a node stands for.
func entityKey(n *observation.ObservedNode, cfg *resolveConfig) string {
	if repo, ok := imageRepositoryOf(n); ok {
		return repo
	}

	if id, ok := scopedOf(n, cfg); ok {
		return id.String()
	}

	return n.ObservedID
}

func findBinding(loaded *Loaded, id string) *Binding {
	for i := range loaded.Bindings {
		if loaded.Bindings[i].ID == id {
			return &loaded.Bindings[i]
		}
	}

	return nil
}
