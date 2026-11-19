package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// platformKubernetes is the platform facet of a placement.
const platformKubernetes = "kubernetes"

// facetEnvironment is the environment facet of a placement.
const facetEnvironment = "environment"

// labelRoute is the native label of the project-global `Route` node (O7). It is never an identity.
const labelRoute = "Route"

// reviewResource is a `Resource` node as an item needs it.
type reviewResource struct {
	// scoped is the scoped identity (`k8s:<env>/<cluster>/<group>/<Kind>/<namespace>/<name>`).
	scoped    string
	objKey    string
	kind      string
	name      string
	namespace string
}

// resourceOf reads the identity of a `Resource` node. The bool is false for a node with no obj_key.
func resourceOf(node *observation.ObservedNode) (reviewResource, bool) {
	key, ok := observation.ObjKeyOf(node)
	if !ok {
		return reviewResource{}, false
	}

	parsed, err := observation.ParseObjKey(key)
	if err != nil {
		return reviewResource{}, false
	}

	scoped := node.ScopedID
	if scoped == "" {
		id := observation.NewScopedID(node.Environment, "", parsed)
		scoped = id.String()
	}

	return reviewResource{scoped: scoped, objKey: key, kind: parsed.Kind, name: parsed.Name, namespace: parsed.Namespace}, true
}

// reviewBuilder collects the candidates of one kind and drops a second item with the same identity.
type reviewBuilder struct {
	seen map[string]struct{}
	out  []reviewCandidate
}

func (b *reviewBuilder) add(c *reviewCandidate) {
	if _, dup := b.seen[c.item.Identity]; dup {
		return
	}

	if b.seen == nil {
		b.seen = map[string]struct{}{}
	}

	b.seen[c.item.Identity] = struct{}{}
	b.out = append(b.out, *c)
}

// build makes every candidate of every kind.
func (f *ReviewFacts) build() {
	f.buildReferences()
	f.buildIdentity()
	f.buildEntryPoints()
	f.buildWorkloads()
	f.buildModel()
	f.buildExternal()
	f.buildAssurance()

	for kind, list := range f.candidates {
		slices.SortFunc(list, func(a, b reviewCandidate) int {
			return canonjson.CompareUTF16(a.item.Identity, b.item.Identity)
		})

		f.candidates[kind] = list
	}
}

func (f *ReviewFacts) put(kind impact.Kind, list []reviewCandidate) {
	f.candidates[kind] = append(f.candidates[kind], list...)
}

// ends gives the two resources of an edge. The bool is false when an endpoint is not a resource.
func (f *ReviewFacts) ends(edge *observation.EvidenceEdge) (from, to reviewResource, ok bool) {
	fromNode, okFrom := f.nodes[edge.From]
	toNode, okTo := f.nodes[edge.To]

	if !okFrom || !okTo {
		return reviewResource{}, reviewResource{}, false
	}

	from, okFrom = resourceOf(&fromNode)
	to, okTo = resourceOf(&toNode)

	return from, to, okFrom && okTo
}

func (f *ReviewFacts) calmOf(nodeID string) []string {
	return slices.Clone(f.bindingSet.ByObserved[nodeID])
}

// referenceMode is the reference mode of a reference fact: the native `ref` value.
func referenceMode(edge *observation.EvidenceEdge) string {
	if mode := edge.Facets["referenceMode"]; mode != "" {
		return mode
	}

	return edge.Properties["ref"]
}

// buildReferences makes the items of the reference kinds. The reference is the target obj_key, the role is the
// reference mode, and the consumer is the resource that holds the reference. A value never enters an item.
func (f *ReviewFacts) buildReferences() {
	for kind, edgeKind := range map[impact.Kind]observation.CanonicalEdgeKind{
		impact.KindSecretReference:        observation.KindReferencesSecret,
		impact.KindConfigurationReference: observation.KindReferencesConfiguration,
		impact.KindStorageReference:       observation.KindReferencesStorage,
		impact.KindCertificateReference:   observation.KindReferencesCertificateSecret,
	} {
		var builder reviewBuilder

		for _, edge := range f.edges[edgeKind] {
			from, to, ok := f.ends(edge)
			if !ok {
				continue
			}

			mode := referenceMode(edge)
			facets := impact.Facets{"reference": {to.objKey}, "role": {mode}, "consumer": {from.objKey}}

			builder.add(&reviewCandidate{
				item:     f.contract.NewItem(kind, from.scoped+"#"+mode+":"+to.objKey, facets, nil, nil),
				env:      edge.Environment,
				calm:     f.calmOf(edge.From),
				consumer: from.scoped,
			})
		}

		f.put(kind, builder.out)
	}
}

// roleGraph is the RoleBinding structure of one run, by the observed ID of the nodes.
type roleGraph struct {
	// roles are the role obj_keys of a RoleBinding.
	roles map[string][]string
	// subjects are the ServiceAccount obj_keys of a RoleBinding.
	subjects map[string][]string
	// accountRoles are the role obj_keys that the RoleBindings grant to a ServiceAccount.
	accountRoles map[string][]string
}

// resourceEdge is an edge between two resources, with both ends read.
type resourceEdge struct {
	edge     *observation.EvidenceEdge
	from, to reviewResource
}

// resourceEdges gives the edges of a kind whose two ends are resources, in edge order.
func (f *ReviewFacts) resourceEdges(kind observation.CanonicalEdgeKind) []*resourceEdge {
	var out []*resourceEdge

	for _, edge := range f.edges[kind] {
		if from, to, ok := f.ends(edge); ok {
			out = append(out, &resourceEdge{edge: edge, from: from, to: to})
		}
	}

	return out
}

// resourceEdgeCandidate makes the candidate of an item that is held by the `from` resource of an edge. The identity
// is `<from scoped>#<ref>:<to obj_key>`.
func (f *ReviewFacts) resourceEdgeCandidate(link *resourceEdge, kind impact.Kind, ref string, facets impact.Facets) *reviewCandidate {
	return &reviewCandidate{
		item: f.contract.NewItem(kind, link.from.scoped+"#"+ref+":"+link.to.objKey, facets, nil, nil),
		env:  link.edge.Environment, calm: f.calmOf(link.edge.From), consumer: link.from.scoped,
	}
}

func (f *ReviewFacts) roleGraph() *roleGraph {
	graph := &roleGraph{roles: map[string][]string{}, subjects: map[string][]string{}, accountRoles: map[string][]string{}}

	for _, link := range f.resourceEdges(observation.KindBindingRefersToRole) {
		graph.roles[link.edge.From] = append(graph.roles[link.edge.From], link.to.objKey)
	}

	for _, link := range f.resourceEdges(observation.KindBindingAppliesToPrincipal) {
		graph.subjects[link.edge.From] = append(graph.subjects[link.edge.From], link.to.objKey)
		graph.accountRoles[link.edge.To] = append(graph.accountRoles[link.edge.To], graph.roles[link.edge.From]...)
	}

	return graph
}

// bindingScope is the scope facet of a RoleBinding: its namespace, or `cluster`.
func bindingScope(namespace string) string {
	if namespace == "" {
		return "cluster"
	}

	return namespace
}

// buildIdentity makes the runtime-identity and role-binding items.
func (f *ReviewFacts) buildIdentity() {
	graph := f.roleGraph()

	var identity, bindings reviewBuilder

	for _, link := range f.resourceEdges(observation.KindUsesRuntimeIdentity) {
		facets := impact.Facets{"serviceAccount": {link.to.objKey}, "roleReferences": graph.accountRoles[link.edge.To]}
		identity.add(f.resourceEdgeCandidate(link, impact.KindRuntimeIdentity, refServiceAccount, facets))
	}

	for _, link := range f.resourceEdges(observation.KindBindingAppliesToPrincipal) {
		facets := impact.Facets{"serviceAccount": {link.to.objKey}, "roleReferences": graph.roles[link.edge.From]}
		identity.add(f.resourceEdgeCandidate(link, impact.KindRuntimeIdentity, refSubject, facets))
	}

	for _, link := range f.resourceEdges(observation.KindBindingRefersToRole) {
		facets := impact.Facets{"roleReference": {link.to.objKey}, "subjects": graph.subjects[link.edge.From], "scope": {bindingScope(link.from.namespace)}}
		bindings.add(f.resourceEdgeCandidate(link, impact.KindRoleBinding, refRoleRef, facets))
	}

	f.put(impact.KindRuntimeIdentity, identity.out)
	f.put(impact.KindRoleBinding, bindings.out)
}

// hostPath is the host and path of a `Route` node.
type hostPath struct{ host, path string }

// routeHostPath reads the host and path of a `Route` node from its qualified name
// `__route__<via>__<host><path>` (O7: the node has no properties on the adapter). The `*` host of an HTTPRoute with no
// hostname stays a facet value.
func routeHostPath(qualifiedName string) (hostPath, bool) {
	rest, ok := strings.CutPrefix(qualifiedName, "__route__")
	if !ok {
		return hostPath{}, false
	}

	_, rest, ok = strings.Cut(rest, "__")
	if !ok {
		return hostPath{}, false
	}

	host, path, _ := strings.Cut(rest, "/")

	return hostPath{host: host, path: "/" + path}, true
}

// routeFor gives the kind of the `Route` node edge that belongs to a resource edge kind: an Ingress is `ingress`
// on both sides, an HTTPRoute is `route` on the resource and `httproute` on the `Route` node.
func routeFor(resourceKind string) string {
	if resourceKind == "route" || resourceKind == "httproute" {
		return "httproute"
	}

	return resourceKind
}

// buildEntryPoints makes the public-entry-point items. The identity is the Ingress or HTTPRoute `Resource`; the
// `Route` nodes that route to the same Service give host and path, so two environments with equal host and path
// stay two items.
func (f *ReviewFacts) buildEntryPoints() {
	routes := f.entryRoutes()
	gateways := f.entryGateways()

	var builder reviewBuilder

	for _, draft := range f.entryPointDrafts(routes) {
		draft.facets["exposureClass"] = []string{exposureClass(draft.resource.kind, gateways[draft.nodeID])}

		builder.add(&reviewCandidate{
			item: f.contract.NewItem(impact.KindPublicEntryPoint, draft.resource.scoped, draft.facets, nil, nil),
			env:  draft.env, calm: f.calmOf(draft.nodeID), consumer: draft.resource.scoped,
		})
	}

	f.put(impact.KindPublicEntryPoint, builder.out)
}

// entryRouteKey is the Service and the edge kind that the `Route` nodes of an entry point route to.
type entryRouteKey struct{ service, kind string }

// entryRoutes gives the host and path of the `Route` nodes, by the Service they route to.
func (f *ReviewFacts) entryRoutes() map[entryRouteKey][]hostPath {
	routes := map[entryRouteKey][]hostPath{}

	for _, edge := range f.edges[observation.KindRoutesTo] {
		node, ok := f.nodes[edge.From]
		if !ok || node.Label != labelRoute {
			continue
		}

		if hp, ok := routeHostPath(node.QualifiedName); ok {
			key := entryRouteKey{service: edge.To, kind: edge.Properties["kind"]}
			routes[key] = append(routes[key], hp)
		}
	}

	return routes
}

// entryGateways gives the Gateway obj_keys that each route resource attaches to, by the observed ID of the route.
func (f *ReviewFacts) entryGateways() map[string][]string {
	gateways := map[string][]string{}

	for _, link := range f.resourceEdges(observation.KindRouteAttachesToGateway) {
		gateways[link.edge.From] = append(gateways[link.edge.From], link.to.objKey)
	}

	return gateways
}

// entryPointDraft is a public entry point while its routing edges are read.
type entryPointDraft struct {
	resource reviewResource
	facets   impact.Facets
	env      string
	nodeID   string
}

// addRoute adds the backend Service of a routing edge, with the host and path of the `Route` nodes that reach it.
func (d *entryPointDraft) addRoute(link *resourceEdge, routes map[entryRouteKey][]hostPath) {
	d.facets["backend"] = append(d.facets["backend"], link.to.objKey)

	for _, hp := range routes[entryRouteKey{service: link.edge.To, kind: routeFor(link.edge.Properties["kind"])}] {
		d.facets["host"] = append(d.facets["host"], hp.host)
		d.facets["path"] = append(d.facets["path"], hp.path)
		d.facets["route"] = append(d.facets["route"], hp.host+hp.path)
	}
}

// entryPointDrafts gives one draft per Ingress or HTTPRoute resource, in the order of its first routing edge.
func (f *ReviewFacts) entryPointDrafts(routes map[entryRouteKey][]hostPath) []*entryPointDraft {
	byScoped := map[string]*entryPointDraft{}

	var order []*entryPointDraft

	for _, link := range f.resourceEdges(observation.KindRoutesTo) {
		if node, ok := f.nodes[link.edge.From]; !ok || node.Label == labelRoute {
			continue
		}

		draft, exists := byScoped[link.from.scoped]
		if !exists {
			draft = &entryPointDraft{resource: link.from, facets: impact.Facets{}, env: link.edge.Environment, nodeID: link.edge.From}
			byScoped[link.from.scoped] = draft
			order = append(order, draft)
		}

		draft.addRoute(link, routes)
	}

	return order
}

// exposureClass says how a resource is exposed: an Ingress, or a Gateway API route with the Gateway that it attaches to.
func exposureClass(kind string, gateways []string) string {
	if kind == "Ingress" {
		return "ingress"
	}

	if len(gateways) == 0 {
		return "gateway"
	}

	sorted := slices.Clone(gateways)
	slices.SortFunc(sorted, canonjson.CompareUTF16)

	return "gateway:" + strings.Join(slices.Compact(sorted), ",")
}

// buildWorkloads makes the placement items and the image items (implementation) of the workloads.
func (f *ReviewFacts) buildWorkloads() {
	if f.evidence == nil {
		return
	}

	view := deployment.Extract(&deployment.Input{
		Model: f.model, Evidence: f.evidence, BindingSet: f.bindingSet, Bindings: f.bindings, Declared: f.envs,
	})

	regions := f.regionByNode()

	var placement, images reviewBuilder

	for i := range view.Workloads {
		workload := &view.Workloads[i]
		if workload.Templated {
			continue
		}

		placement.add(f.workloadCandidate(workload, impact.KindPlacement, workload.Identity, workloadFacets(workload, regions)))

		for j := range workload.Occurrences {
			occurrence := &workload.Occurrences[j]
			if !occurrence.Unresolved {
				images.add(f.workloadCandidate(workload, impact.KindImplementation, workload.Identity+"#image:"+occurrence.Container, imageFacets(occurrence)))
			}
		}
	}

	f.put(impact.KindPlacement, placement.out)
	f.put(impact.KindImplementation, images.out)
}

// regionByNode is the `region` of the declared trust of each endpoint node, empty without a model.
func (f *ReviewFacts) regionByNode() map[string]string {
	regions := map[string]string{}
	if f.model == nil {
		return regions
	}

	for id, decl := range endpointTrust(&trustInput{Model: f.model, Bindings: f.bindings}) {
		regions[id] = decl.Value("region")
	}

	return regions
}

// workloadCandidate makes the candidate of an item of a workload.
func (f *ReviewFacts) workloadCandidate(workload *deployment.Workload, kind impact.Kind, identity string, facets impact.Facets) *reviewCandidate {
	return &reviewCandidate{
		item: f.contract.NewItem(kind, identity, facets, nil, nil),
		env:  workload.Environment, calm: slices.Clone(workload.Subjects), consumer: workload.Identity,
	}
}

// workloadFacets are the facets of the placement of a workload.
func workloadFacets(workload *deployment.Workload, regions map[string]string) impact.Facets {
	objKey := fmt.Sprintf("%s/%s/%s/%s", workload.APIGroup, workload.Kind, namespacePart(workload.Namespace), workload.Name)

	facets := impact.Facets{
		"workload": {objKey}, facetEnvironment: {workload.Environment}, "platform": {platformKubernetes},
		"namespace": {workload.Namespace},
	}

	if workload.Cluster != "" {
		facets["platform"] = append(facets["platform"], "cluster:"+workload.Cluster)
	}

	for _, subject := range workload.Subjects {
		if region := regions[subject]; region != "" {
			facets["region"] = append(facets["region"], region)
		}
	}

	return facets
}

// namespacePart is the namespace part of an obj_key: `_` for a cluster-scoped kind.
func namespacePart(namespace string) string {
	if namespace == "" {
		return "_"
	}

	return namespace
}

// imageFacets are the facets of the image of a container occurrence. The tag or digest comes from the edge `image`
// property, never from the `Image` node (O3).
func imageFacets(occurrence *deployment.ContainerOccurrence) impact.Facets {
	facets := impact.Facets{facetSubject: {occurrence.Image.Raw}, facetRepository: {occurrence.Image.Repository}}

	if occurrence.Image.Tag != "" {
		facets[facetTag] = []string{occurrence.Image.Tag}
	}

	if occurrence.Image.Digest != "" {
		facets[facetDigest] = []string{occurrence.Image.Digest}
	}

	return facets
}

// buildModel makes the items of the CALM model and the bindings: protocol, trust-context, data-classification,
// binding, and control.
func (f *ReviewFacts) buildModel() {
	for i := range f.bindings {
		f.put(impact.KindBinding, []reviewCandidate{f.bindingItem(&f.bindings[i])})
	}

	if f.model == nil {
		return
	}

	f.buildTrustItems()

	var protocols, controls reviewBuilder

	for i := range f.model.Relationships {
		rel := &f.model.Relationships[i]
		if rel.Protocol == "" {
			continue
		}

		protocols.add(&reviewCandidate{
			item: f.contract.NewItem(impact.KindProtocol, "calm:relationship:"+rel.ID, impact.Facets{"protocol": {rel.Protocol}, "flow": {rel.ID}}, nil, nil),
			calm: relationshipNodes(rel),
		})
	}

	for i := range f.model.Controls {
		control := &f.model.Controls[i]

		controls.add(&reviewCandidate{
			item: f.contract.NewItem(impact.KindControl, controlIdentity(control), controlFacets(control), nil, nil),
			calm: f.controlNodes(control),
		})
	}

	f.put(impact.KindProtocol, protocols.out)
	f.put(impact.KindControl, controls.out)
}

func relationshipNodes(rel *intent.RelationshipDeclaration) []string {
	nodes := slices.Clone(rel.Targets)
	if rel.Source != "" {
		nodes = append(nodes, rel.Source)
	}

	return nodes
}

func controlIdentity(control *intent.ControlAttachment) string {
	return fmt.Sprintf("calm:control:%s:%s:%s", control.Scope.Level, control.Scope.ID, control.ControlID)
}

func controlFacets(control *intent.ControlAttachment) impact.Facets {
	config := []string{}

	if control.Engine != "" {
		config = append(config, "engine="+control.Engine)
	}

	if cfg := control.Config; cfg != nil {
		if cfg.PolicyRef != "" {
			config = append(config, "policyRef="+cfg.PolicyRef)
		}

		if cfg.UnknownDisposition != "" {
			config = append(config, "unknownDisposition="+cfg.UnknownDisposition)
		}

		if cfg.MinimumPolicyCoverage != nil {
			config = append(config, fmt.Sprintf("minimumPolicyCoverage=%g", *cfg.MinimumPolicyCoverage))
		}

		for _, id := range cfg.RuleIDs {
			config = append(config, "rule="+id)
		}
	}

	return impact.Facets{
		"control": {control.ControlID}, "target": {string(control.Scope.Level) + ":" + control.Scope.ID}, "configuration": config,
	}
}

// controlNodes gives the CALM nodes that a control applies to.
func (f *ReviewFacts) controlNodes(control *intent.ControlAttachment) []string {
	switch control.Scope.Level {
	case intent.ScopeNode:
		return []string{control.Scope.ID}
	case intent.ScopeRelationship:
		for i := range f.model.Relationships {
			if f.model.Relationships[i].ID == control.Scope.ID {
				return relationshipNodes(&f.model.Relationships[i])
			}
		}
	case intent.ScopeArchitecture, intent.ScopeFlow:
	}

	return nil
}

// buildTrustItems makes the trust-context and data-classification items of the nodes and relationships.
func (f *ReviewFacts) buildTrustItems() {
	var trusts, classes reviewBuilder

	add := func(element string, decl declarationView, calm []string) {
		if dims := decl.dimensions(); len(dims) > 0 {
			trusts.add(&reviewCandidate{
				item: f.contract.NewItem(impact.KindTrustContext, element+"#trust", impact.Facets{"element": {element}, "dimensions": dims}, nil, nil),
				calm: calm,
			})
		}

		if decl.classification != "" {
			classes.add(&reviewCandidate{
				item: f.contract.NewItem(impact.KindDataClassification, element+"#classification", impact.Facets{"element": {element}, "classification": {decl.classification}}, nil, nil),
				calm: calm,
			})
		}
	}

	merged := endpointTrust(&trustInput{Model: f.model, Bindings: f.bindings})

	for i := range f.model.Nodes {
		id := f.model.Nodes[i].ID
		decl := merged[id]
		add("calm:node:"+id, declarationView{values: decl.Dimensions, classification: decl.Classification}, []string{id})
	}

	for i := range f.model.Relationships {
		rel := &f.model.Relationships[i]
		if rel.Trust != nil {
			add("calm:relationship:"+rel.ID, declarationView{values: rel.Trust.Dimensions, classification: rel.Trust.Classification}, relationshipNodes(rel))
		}
	}

	f.put(impact.KindTrustContext, trusts.out)
	f.put(impact.KindDataClassification, classes.out)
}

// declarationView is a trust declaration as the items read it.
type declarationView struct {
	values         map[string]string
	classification string
}

// dimensions gives the declared dimension values as `<dimension>=<value>`, sorted.
func (d declarationView) dimensions() []string {
	out := make([]string, 0, len(d.values))

	for name, value := range d.values {
		if value != "" {
			out = append(out, name+"="+value)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// bindingItem makes the item of one binding entry: the CALM element, the selectors, and the binding mode.
func (f *ReviewFacts) bindingItem(entry *binding.Binding) reviewCandidate {
	targets := make([]string, 0, len(entry.Match))
	for i := range entry.Match {
		targets = append(targets, matchText(&entry.Match[i]))
	}

	mode := []string{
		"cardinality=" + orDefault(entry.Cardinality, binding.CardinalityMany),
		"agreement=" + orDefault(entry.Agreement, binding.AgreementAll),
		"status=" + orDefault(entry.Status, binding.StatusActive),
		fmt.Sprintf("exclusive=%t", entry.Exclusive),
	}

	return reviewCandidate{
		item: f.contract.NewItem(
			impact.KindBinding, "binding:"+entry.ID,
			impact.Facets{"source": {entry.ArchitectureRef}, "target": targets, "mode": mode}, nil, nil),
		calm: []string{entry.ArchitectureRef},
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

// matchText writes a selector of a binding as `<kind>:<field>=<value>,...` with the fields in a fixed order.
func matchText(match *binding.Match) string {
	fields := []struct{ name, value string }{
		{"value", match.Value},
		{facetEnvironment, match.Environment},
		{"cluster", match.Cluster},
		{"apiGroup", match.APIGroup},
		{"resourceKind", match.ResourceKind},
		{"namespace", match.Namespace},
		{"name", match.Name},
		{"repository", match.Repository},
		{"digest", match.Digest},
		{"address", match.Address},
		{"file", match.File},
		{"service", match.Service},
	}

	parts := make([]string, 0, len(fields))

	for _, field := range fields {
		if field.value != "" {
			parts = append(parts, field.name+"="+field.value)
		}
	}

	return string(match.Kind) + ":" + strings.Join(parts, ",")
}

// buildExternal makes the external-destination items: a remote call from a bound node to a destination that no
// binding maps. The destination is named by its scoped identity, else its qualified name.
func (f *ReviewFacts) buildExternal() {
	var builder reviewBuilder

	for _, kind := range []observation.CanonicalEdgeKind{observation.KindCallsService, observation.KindDispatchesAsync} {
		for _, edge := range f.edges[kind] {
			if candidate, ok := f.externalCandidate(kind, edge); ok {
				builder.add(candidate)
			}
		}
	}

	f.put(impact.KindExternalDestination, builder.out)
}

// externalCandidate makes the item of a call edge from a node with one binding to a node with none. The bool is
// false for any other edge.
func (f *ReviewFacts) externalCandidate(kind observation.CanonicalEdgeKind, edge *observation.EvidenceEdge) (*reviewCandidate, bool) {
	from, okFrom := f.nodes[edge.From]
	target, okTo := f.nodes[edge.To]
	callers := f.bindingSet.ByObserved[edge.From]

	if !okFrom || !okTo {
		return nil, false
	}

	if len(callers) != 1 || len(f.bindingSet.ByObserved[edge.To]) > 0 {
		return nil, false
	}

	destination := externalID(&target)
	facets := impact.Facets{"destination": {destination}, "protocol": {string(kind)}}

	return &reviewCandidate{
		item: f.contract.NewItem(impact.KindExternalDestination, callers[0]+"->"+destination, facets, nil, nil),
		calm: slices.Clone(callers), consumer: from.ObservedID,
	}, true
}
