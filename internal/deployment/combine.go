package deployment

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// PlacementStatus is the status of one placement instance: the five as-built status tags, decided for one node and
// one environment (design D17).
type PlacementStatus string

// The placement statuses, from the best to the worst.
const (
	PlacementConforming   PlacementStatus = "conforming"
	PlacementDeclaredOnly PlacementStatus = "declared-only"
	PlacementObservedOnly PlacementStatus = "observed-only"
	PlacementUnknown      PlacementStatus = "unknown"
	PlacementViolating    PlacementStatus = "violating"
)

// PlacementStatuses gives the five statuses in the order of severity: the worst first.
func PlacementStatuses() []PlacementStatus {
	return []PlacementStatus{PlacementViolating, PlacementUnknown, PlacementObservedOnly, PlacementDeclaredOnly, PlacementConforming}
}

// Severity gives the rank of the status, 0 for the worst. An unknown value ranks after all statuses.
func (s PlacementStatus) Severity() int {
	if i := slices.Index(PlacementStatuses(), s); i >= 0 {
		return i
	}

	return len(PlacementStatuses())
}

// WorstPlacementStatus gives the worst of the statuses. No status gives PlacementDeclaredOnly, the status of a
// declared structure that nothing shows.
func WorstPlacementStatus(statuses ...PlacementStatus) PlacementStatus {
	if len(statuses) == 0 {
		return PlacementDeclaredOnly
	}

	worst := statuses[0]

	for _, s := range statuses[1:] {
		if s.Severity() < worst.Severity() {
			worst = s
		}
	}

	return worst
}

// placedInPredicate is the predicate whose coverage decides declared-only against unknown.
const placedInPredicate = string(observation.KindPlacedIn)

// PlacedInstance is one instance of a node in one environment: the declared placement in a container, or one
// observed placement record.
type PlacedInstance struct {
	// Node is the CALM node ID of the placed element.
	Node string
	// Container is the CALM node ID of the container (the deployment target). Empty for an instance that no target
	// shows: a node with no declared container that only has a gap.
	Container string
	// Environment is the declared environment ID.
	Environment string
	// Workload and HostingTarget are the observed IDs of the record, empty for a declared instance.
	Workload, HostingTarget string
	// Status is the status tag of the instance.
	Status PlacementStatus
	// Gaps are the gaps of the environment that name the node. They make a declared instance `unknown`.
	Gaps []PlacementGap
	// Locations are the places of the record.
	Locations []Location
	// Declared says that CALM declares the node in Container with `deployed-in`.
	Declared bool
	// Observed says that a placement record makes the instance.
	Observed bool
	// Reverse says that the record runs from the container side to the node side.
	Reverse bool
}

// ElementPlacement is the placement of one node in one environment.
type ElementPlacement struct {
	Node string
	// Status is the worst status of the instances.
	Status PlacementStatus
	// Instances are sorted by container, workload, and declared before observed. Never nil.
	Instances []PlacedInstance
}

// EnvironmentPlacement is the combined declared and observed placement of one environment.
type EnvironmentPlacement struct {
	Environment string
	// Elements are sorted by node ID. Never nil.
	Elements []ElementPlacement
	// Gaps are all gaps of the environment, bound or not, as Placements gave them. Never nil.
	Gaps []PlacementGap
	// Declared says that CALM declares at least one placement (`deployed-in`). It applies to every environment.
	Declared bool
	// Observed says that a placement record exists for the environment.
	Observed bool
}

// Shown says whether the deployment view shows the environment: a declared placement, a placement record, or a gap
// exists for it (CO-PRJ-008 is for an environment where none exists).
func (e *EnvironmentPlacement) Shown() bool { return e.Declared || e.Observed || len(e.Gaps) > 0 }

// Instances gives the instances of a node, nil when the node has none.
func (e *EnvironmentPlacement) Instances(node string) []PlacedInstance {
	i, found := slices.BinarySearchFunc(e.Elements, node, func(el ElementPlacement, id string) int {
		return canonjson.CompareUTF16(el.Node, id)
	})
	if !found {
		return nil
	}

	return e.Elements[i].Instances
}

// Combine combines the declared placement (CALM `deployed-in`, which applies to every environment) with the observed
// placement (records and gaps of Placements) for each of the environments, sorted by ID. Records and gaps of another
// environment never count. The combination reads the model, the placement set, and the coverage of `placed-in` only:
// no rule result, finding, waiver, baseline, or expiry (design D17).
//
// Per node and environment, with D the declared containers:
//
//   - a record in a container of D is `conforming` whatever the coverage;
//   - a record in another container, and every reverse record, is `violating`;
//   - a record of a node with no D is `observed-only`;
//   - a declared container with no conforming record gives a declared instance that is `unknown` when a gap names
//     the node or the coverage of `placed-in` in the environment is not complete, else `declared-only`.
//
// The status of the node is the worst instance status. coverage holds the records of the run; a missing record of
// the environment counts as not complete.
func Combine(
	model *intent.ArchitectureModel, set PlacementSet, environments []string, coverage []observation.PredicateCoverage,
) []EnvironmentPlacement {
	declared := declaredContainers(model)
	ids := slices.Clone(environments)

	slices.SortFunc(ids, canonjson.CompareUTF16)

	ids = slices.Compact(ids)
	out := make([]EnvironmentPlacement, 0, len(ids))

	for _, id := range ids {
		out = append(out, combineEnvironment(id, declared, set, coverageComplete(coverage, id)))
	}

	return out
}

// declaredContainers gives, for each node that a `deployed-in` relationship deploys, the sorted container IDs.
func declaredContainers(model *intent.ArchitectureModel) map[string][]string {
	out := map[string][]string{}

	if model == nil {
		return out
	}

	for i := range model.Relationships {
		rel := &model.Relationships[i]
		if rel.Kind != intent.RelDeployedIn || rel.Container == "" {
			continue
		}

		for _, target := range rel.Targets {
			if !slices.Contains(out[target], rel.Container) {
				out[target] = append(out[target], rel.Container)
			}
		}
	}

	for node := range out {
		slices.SortFunc(out[node], canonjson.CompareUTF16)
	}

	return out
}

// coverageComplete says whether `placed-in` has a complete record for the environment.
func coverageComplete(coverage []observation.PredicateCoverage, environment string) bool {
	for i := range coverage {
		rec := &coverage[i]
		if rec.Predicate == placedInPredicate && rec.Scope.Project == "" && rec.Scope.Environment == environment {
			return rec.Complete()
		}
	}

	return false
}

func combineEnvironment(environment string, declared map[string][]string, set PlacementSet, complete bool) EnvironmentPlacement {
	result := EnvironmentPlacement{
		Environment: environment, Elements: []ElementPlacement{}, Gaps: []PlacementGap{}, Declared: len(declared) > 0,
	}

	records := map[string][]*Placement{}
	gapsOf := map[string][]PlacementGap{}
	nodes := map[string]struct{}{}

	for node := range declared {
		nodes[node] = struct{}{}
	}

	for i := range set.Placements {
		if p := &set.Placements[i]; p.Environment == environment {
			records[p.Node] = append(records[p.Node], p)
			nodes[p.Node] = struct{}{}
			result.Observed = true
		}
	}

	for i := range set.Gaps {
		g := &set.Gaps[i]
		if g.Environment != environment {
			continue
		}

		result.Gaps = append(result.Gaps, *g)

		if g.Node != "" {
			gapsOf[g.Node] = append(gapsOf[g.Node], *g)
			nodes[g.Node] = struct{}{}
		}
	}

	sorted := make([]string, 0, len(nodes))
	for node := range nodes {
		sorted = append(sorted, node)
	}

	slices.SortFunc(sorted, canonjson.CompareUTF16)

	for _, node := range sorted {
		instances := nodeInstances(environment, node, declared[node], records[node], gapsOf[node], complete)
		statuses := make([]PlacementStatus, len(instances))

		for i := range instances {
			statuses[i] = instances[i].Status
		}

		result.Elements = append(result.Elements, ElementPlacement{Node: node, Status: WorstPlacementStatus(statuses...), Instances: instances})
	}

	return result
}

// nodeInstances makes the instances of one node in one environment.
func nodeInstances(environment, node string, containers []string, records []*Placement, gaps []PlacementGap, complete bool) []PlacedInstance {
	instances := make([]PlacedInstance, 0, len(records)+len(containers))
	conforming := map[string]bool{}

	for _, rec := range records {
		instance := PlacedInstance{
			Node: node, Container: rec.Container, Environment: environment, Workload: rec.Workload, HostingTarget: rec.HostingTarget,
			Observed: true, Reverse: rec.Reverse, Locations: rec.Locations,
		}

		switch {
		case rec.Reverse:
			instance.Status = PlacementViolating
		case len(containers) == 0:
			instance.Status = PlacementObservedOnly
		case slices.Contains(containers, rec.Container):
			instance.Status = PlacementConforming
			instance.Declared = true
			conforming[rec.Container] = true
		default:
			instance.Status = PlacementViolating
		}

		instances = append(instances, instance)
	}

	for _, container := range containers {
		if conforming[container] {
			continue
		}

		instance := PlacedInstance{Node: node, Container: container, Environment: environment, Declared: true, Status: PlacementDeclaredOnly}
		if len(gaps) > 0 || !complete {
			instance.Status = PlacementUnknown
			instance.Gaps = gaps
		}

		instances = append(instances, instance)
	}

	if len(instances) == 0 && len(gaps) > 0 {
		instances = append(instances, PlacedInstance{Node: node, Environment: environment, Status: PlacementUnknown, Gaps: gaps})
	}

	slices.SortFunc(instances, func(a, b PlacedInstance) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Container, b.Container), canonjson.CompareUTF16(a.Workload, b.Workload),
			canonjson.CompareUTF16(a.HostingTarget, b.HostingTarget), cmp.Compare(boolRank(b.Declared && !b.Observed), boolRank(a.Declared && !a.Observed)),
		)
	})

	return instances
}

func boolRank(b bool) int {
	if b {
		return 1
	}

	return 0
}
