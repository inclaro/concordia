package deployment

import (
	"cmp"
	"maps"
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// The sides of a placement gap.
const (
	// SideWorkload is the source end of a `placed-in` fact: the workload.
	SideWorkload = "workload"
	// SideHostingTarget is the target end of a `placed-in` fact: the hosting target (the namespace).
	SideHostingTarget = "hosting-target"
)

// Placement is one observed placement: a workload of a CALM node that a `placed-in` fact puts in the hosting
// target of a CALM container, in a declared environment. It is a fact of the observed graph; whether it agrees with a
// declared `deployed-in` relationship is for the reader to say (a Reverse record is the one exception, see below).
type Placement struct {
	// Node is the CALM node ID of the placed element.
	Node string
	// Container is the CALM node ID of the container that the hosting target is bound to.
	Container string
	// Environment is the ID of the declared environment of the fact.
	Environment string
	// Workload and HostingTarget are the observed IDs of the ends of the fact.
	Workload, HostingTarget string
	// Locations are the places of the `placed-in` facts (the manifest of the workload), sorted.
	Locations []Location
	// Reverse is true when the facts run from the container side to the node side of a declared `deployed-in`
	// relationship: the workload is bound to the container and the hosting target to the deployed node. Node
	// and Container name the ends of the declared relationship, not the ends of the fact.
	Reverse bool
}

// PlacementGap is a `placed-in` fact that gives no placement, and why. The entity of the gap is never attached to a
// node by its name (EVD-05).
type PlacementGap struct {
	// Environment is the ID of the declared environment of the fact. Empty for `environment-unresolved`.
	Environment string
	// Reason is `binding-gap`, `ambiguous-binding`, or `environment-unresolved`.
	Reason Reason
	// Side is SideWorkload or SideHostingTarget.
	Side string
	// Node is the CALM node when the workload end is bound, else empty. An ambiguous workload gives one gap for
	// each CALM node that a binding names.
	Node string
	// Identity is the scoped identity of the unbound or ambiguous entity.
	Identity string
	// Locations are the places of the facts, sorted.
	Locations []Location
}

// PlacementSet is the result of Placements. Both lists are sorted and never nil.
type PlacementSet struct {
	Placements []Placement
	Gaps       []PlacementGap
}

// Placements turns the `placed-in` facts of the evidence into placement records and gaps, for the facts of the
// given declared environments. The source of a fact is resolved through its `workload` binding and its target through
// its `infraResource` binding (the lookups of `hostingTargetOf`, so that the readings do not drift); both ends must be
// bound, and an end with ambiguous bindings gives no record (no binding wins by order). A fact with no declared
// environment gives the gap `environment-unresolved`; a fact of an environment that is not in environments is left
// out. model gives the `deployed-in` relationships that decide the Reverse mark; a nil model marks none.
//
// It reads no rule result, finding, disposition, waiver, or baseline and needs no pack: only the facts that
// `observation.DerivePlacement` adds. For the placement rules INFRA-PLC-001 and INFRA-PLC-002, the records and gaps
// agree with their verdicts (test/integration/deployment_placement_agreement_test.go).
func Placements(
	evidence *observation.EvidenceQueryResult, set *binding.Set, bindings []binding.Binding, environments []Environment, model *intent.ArchitectureModel,
) PlacementSet {
	if evidence == nil || set == nil {
		return PlacementSet{Placements: []Placement{}, Gaps: []PlacementGap{}}
	}

	run := &placementRun{
		evidence: evidence, set: set, environments: environments, model: model, byID: map[string]*binding.Binding{},
		placements: map[placementKey]*Placement{}, gaps: map[gapKey]*PlacementGap{},
	}

	for i := range bindings {
		run.byID[bindings[i].ID] = &bindings[i]
	}

	for _, edge := range evidence.Edges {
		if edge.Kind == observation.KindPlacedIn {
			run.fact(edge)
		}
	}

	return run.result()
}

type placementKey struct {
	environment, node, container, workload, target string
	reverse                                        bool
}

type gapKey struct {
	environment, node, identity string
	reason                      Reason
	side                        string
}

type placementRun struct {
	evidence     *observation.EvidenceQueryResult
	set          *binding.Set
	model        *intent.ArchitectureModel
	byID         map[string]*binding.Binding
	placements   map[placementKey]*Placement
	gaps         map[gapKey]*PlacementGap
	environments []Environment
}

// result sorts the records and the gaps that the run collected. Both lists are never nil.
func (run *placementRun) result() PlacementSet {
	out := PlacementSet{Placements: []Placement{}, Gaps: []PlacementGap{}}

	placements := slices.Collect(maps.Values(run.placements))
	slices.SortFunc(placements, comparePlacements)

	for _, p := range placements {
		p.Locations = sortedLocations(p.Locations)
		out.Placements = append(out.Placements, *p)
	}

	gaps := slices.Collect(maps.Values(run.gaps))
	slices.SortFunc(gaps, compareGaps)

	for _, g := range gaps {
		g.Locations = sortedLocations(g.Locations)
		out.Gaps = append(out.Gaps, *g)
	}

	return out
}

func comparePlacements(a, b *Placement) int {
	return cmp.Or(
		canonjson.CompareUTF16(a.Environment, b.Environment), canonjson.CompareUTF16(a.Node, b.Node),
		canonjson.CompareUTF16(a.Container, b.Container), canonjson.CompareUTF16(a.Workload, b.Workload),
		canonjson.CompareUTF16(a.HostingTarget, b.HostingTarget), compareBools(a.Reverse, b.Reverse),
	)
}

func compareGaps(a, b *PlacementGap) int {
	return cmp.Or(
		canonjson.CompareUTF16(a.Environment, b.Environment), canonjson.CompareUTF16(a.Node, b.Node),
		canonjson.CompareUTF16(a.Side, b.Side), canonjson.CompareUTF16(string(a.Reason), string(b.Reason)),
		canonjson.CompareUTF16(a.Identity, b.Identity),
	)
}

func compareBools(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	}

	return 1
}

// node finds an observed node of the evidence by ID: a provider node, or a synthesized entity.
func (run *placementRun) node(id string) (observation.ObservedNode, bool) {
	if node, ok := run.evidence.Nodes[id]; ok {
		return node, true
	}

	for i := range run.evidence.Entities {
		if run.evidence.Entities[i].ObservedID == id {
			return run.evidence.Entities[i], true
		}
	}

	return observation.ObservedNode{}, false
}

func (run *placementRun) clusterOf(environment string) string {
	for i := range run.environments {
		if run.environments[i].ID == environment {
			return run.environments[i].Cluster
		}
	}

	return ""
}

func (run *placementRun) declared(environment string) bool {
	return slices.ContainsFunc(run.environments, func(e Environment) bool { return e.ID == environment })
}

// identityOf gives the scoped identity of an entity of the fact, empty when the node has none.
func (run *placementRun) identityOf(id, environment string) string {
	node, ok := run.node(id)
	if !ok {
		return ""
	}

	key, ok := observation.ObjKeyOf(&node)
	if !ok {
		key = node.Properties["obj_key"]
	}

	if key != "" {
		if scoped, err := observation.ScopedIDFromObjKey(environment, run.clusterOf(environment), key); err == nil {
			return scoped.String()
		}
	}

	return node.ScopedID
}

// boundTo gives the CALM nodes that the observed entity is bound to by a binding of the match kind (sorted). When
// the set says which bindings bound the entity and one of them is known to be of another kind, it is left out; a
// binding that the caller did not pass cannot be judged and counts.
func (run *placementRun) boundTo(observed string, kind binding.MatchKind) []string {
	ids := run.set.ByObserved[observed]
	used := run.set.UsedBy[observed]

	if len(ids) == 0 || len(used) == 0 {
		return ids
	}

	var refs []string

	for _, id := range used {
		b := run.byID[id]
		if b == nil {
			return ids
		}

		if slices.ContainsFunc(b.Match, func(m binding.Match) bool { return m.Kind == kind }) {
			refs = append(refs, b.ArchitectureRef)
		}
	}

	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !slices.Contains(refs, id) })
}

// placedFact is a `placed-in` fact of a declared environment, with the CALM nodes of its workload end and the places
// that its records point to.
type placedFact struct {
	edge        *observation.EvidenceEdge
	environment string
	workloads   []string
	where       []Location
}

// environmentOf gives the environment of the fact: its own, else the one of the workload node.
func (run *placementRun) environmentOf(edge *observation.EvidenceEdge) string {
	if edge.Environment != "" {
		return edge.Environment
	}

	if from, ok := run.node(edge.From); ok {
		return from.Environment
	}

	return ""
}

// endGap adds the gaps of one end of the fact when the end is unbound or ambiguous, and says whether it did. bound
// gives the CALM nodes of the end; the gaps are of the nodes of the workload end.
func (run *placementRun) endGap(placed *placedFact, side, observed string, bound []string) bool {
	var reason Reason

	switch {
	case len(bound) == 0:
		reason = ReasonBindingGap
	case len(run.set.Ambiguous[observed]) > 0:
		reason = ReasonAmbiguousBinding
	default:
		return false
	}

	key := &gapKey{environment: placed.environment, identity: run.identityOf(observed, placed.environment), reason: reason, side: side}
	run.gapPerNode(key, placed.workloads, placed.where)

	return true
}

// fact resolves one `placed-in` fact. The checks run in the order of the placement rules: the environment, the
// workload end, the hosting-target end.
func (run *placementRun) fact(edge *observation.EvidenceEdge) {
	where := []Location{locationOf(edge)}
	environment := run.environmentOf(edge)
	workloads := run.boundTo(edge.From, binding.MatchWorkload)

	if environment == "" || environment == observation.EnvironmentUnresolved {
		key := &gapKey{identity: run.identityOf(edge.From, ""), reason: ReasonEnvironmentUnresolved, side: SideWorkload}
		run.gapPerNode(key, workloads, where)

		return
	}

	if !run.declared(environment) {
		return
	}

	placed := &placedFact{edge: edge, environment: environment, workloads: workloads, where: where}
	if run.endGap(placed, SideWorkload, edge.From, workloads) {
		return
	}

	hosts := run.boundTo(edge.To, binding.MatchInfraResource)
	if run.endGap(placed, SideHostingTarget, edge.To, hosts) {
		return
	}

	for _, workload := range workloads {
		for _, host := range hosts {
			run.place(placed, workload, host)
		}
	}
}

// place adds the records of one bound pair. A pair that the declared `deployed-in` relationships read from the
// container side to the node side is one Reverse record of the declared node; it is no placement of the container node,
// unless that node is itself a deployed node.
func (run *placementRun) place(placed *placedFact, workload, host string) {
	reverse := run.reverse(workload, host)

	if reverse {
		run.addPlacement(&Placement{
			Node: host, Container: workload, Environment: placed.environment, Workload: placed.edge.From, HostingTarget: placed.edge.To,
			Reverse: true, Locations: placed.where,
		})
	}

	if !reverse || run.deployed(workload) {
		run.addPlacement(&Placement{
			Node: workload, Container: host, Environment: placed.environment, Workload: placed.edge.From, HostingTarget: placed.edge.To,
			Locations: placed.where,
		})
	}
}

// reverse says whether a `deployed-in` relationship places the node host in the container workload.
func (run *placementRun) reverse(workload, host string) bool {
	if run.model == nil {
		return false
	}

	return slices.ContainsFunc(run.model.Relationships, func(rel intent.RelationshipDeclaration) bool {
		return rel.Kind == intent.RelDeployedIn && rel.Container == workload && slices.Contains(rel.Targets, host)
	})
}

// deployed says whether a `deployed-in` relationship lists the node as deployed.
func (run *placementRun) deployed(node string) bool {
	if run.model == nil {
		return false
	}

	return slices.ContainsFunc(run.model.Relationships, func(rel intent.RelationshipDeclaration) bool {
		return rel.Kind == intent.RelDeployedIn && rel.Container != "" && slices.Contains(rel.Targets, node)
	})
}

func (run *placementRun) addPlacement(p *Placement) {
	key := placementKey{p.Environment, p.Node, p.Container, p.Workload, p.HostingTarget, p.Reverse}

	if have, ok := run.placements[key]; ok {
		have.Locations = append(have.Locations, p.Locations...)

		return
	}

	run.placements[key] = p
}

// gapPerNode adds a gap of the key for each node, or one without a node when none is bound. It sets the node of the key.
func (run *placementRun) gapPerNode(key *gapKey, nodes []string, where []Location) {
	if len(nodes) == 0 {
		nodes = []string{""}
	}

	for _, node := range nodes {
		key.node = node
		run.addGap(key, where)
	}
}

func (run *placementRun) addGap(key *gapKey, where []Location) {
	if have, ok := run.gaps[*key]; ok {
		have.Locations = append(have.Locations, where...)

		return
	}

	run.gaps[*key] = &PlacementGap{
		Environment: key.environment, Reason: key.reason, Side: key.side, Node: key.node, Identity: key.identity, Locations: slices.Clone(where),
	}
}
