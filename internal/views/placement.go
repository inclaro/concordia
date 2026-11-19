package views

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// PlacementEntry is one `placement` entry of the projection of a deployment view (design D5 and D17): one instance of
// an element in the environment of the view. The combination of declared and observed placement is
// deployment.Combine; this is its canonical, sorted form for the digest. It holds no source line, location, or
// display text.
type PlacementEntry struct {
	// Element is the CALM node ID.
	Element string `json:"element"`
	// Container is the CALM node ID of the deployment target. Empty when no target shows the instance.
	Container   string `json:"container"`
	Environment string `json:"environment"`
	// Workload is the observed ID of the workload of an observed instance, empty for a declared one. It keeps the
	// replicas of one element apart.
	Workload string `json:"workload"`
	// Status is one of the five status tags, without the `concordia:` prefix.
	Status string `json:"status"`
	// Gaps are the gaps that name the element in the environment and make the instance `unknown`. Never nil.
	Gaps     []PlacementGapEntry `json:"gaps"`
	Declared bool                `json:"declared"`
	Observed bool                `json:"observed"`
}

// PlacementGapEntry is one `gaps` entry of the projection: a `placed-in` fact that gives no placement, and why.
type PlacementGapEntry struct {
	Environment string `json:"environment"`
	// Reason is `binding-gap`, `ambiguous-binding`, or `environment-unresolved`.
	Reason string `json:"reason"`
	// Side is `workload` or `hosting-target`.
	Side string `json:"side"`
	// Node is the CALM node when the workload end is bound, else empty.
	Node string `json:"node"`
	// Identity is the scoped identity of the unbound or ambiguous entity.
	Identity string `json:"identity"`
	// Locations are the paths of the manifests of the facts, sorted and without lines (a line move is no change).
	Locations []string `json:"locations"`
}

// PlacementProjection is the placement part of a deployment view projection: the instances and the gaps of one
// environment. Both lists are sorted and never nil.
type PlacementProjection struct {
	Entries []PlacementEntry    `json:"placement"`
	Gaps    []PlacementGapEntry `json:"gaps"`
}

// ProjectPlacement combines the declared placement of the model with the placement records and gaps of the set for
// ONE environment, and gives the canonical entries of the projection. Records and gaps of another environment do not
// count, so a change of placement in one environment changes only the projection of that environment. The
// `environment-unresolved` gaps (no environment) belong to no environment and are not in the result.
//
// The input is the model, the output of deployment.Placements, and the coverage of the run; no rule result,
// disposition, waiver, or baseline (D17).
func ProjectPlacement(
	model *intent.ArchitectureModel, set deployment.PlacementSet, environment string, coverage []observation.PredicateCoverage,
) PlacementProjection {
	out := PlacementProjection{Entries: []PlacementEntry{}, Gaps: []PlacementGapEntry{}}

	combined := deployment.Combine(model, set, []string{environment}, coverage)
	if len(combined) != 1 {
		return out
	}

	return ProjectCombined(&combined[0])
}

// ProjectCombined gives the canonical entries of an environment that deployment.Combine already combined.
func ProjectCombined(env *deployment.EnvironmentPlacement) PlacementProjection {
	out := PlacementProjection{Entries: []PlacementEntry{}, Gaps: make([]PlacementGapEntry, 0, len(env.Gaps))}

	for i := range env.Elements {
		for j := range env.Elements[i].Instances {
			instance := &env.Elements[i].Instances[j]
			entry := PlacementEntry{
				Element: instance.Node, Container: instance.Container, Environment: env.Environment, Workload: instance.Workload,
				Declared: instance.Declared, Observed: instance.Observed, Status: string(instance.Status),
				Gaps: gapEntries(instance.Gaps),
			}
			out.Entries = append(out.Entries, entry)
		}
	}

	out.Gaps = gapEntries(env.Gaps)

	slices.SortFunc(out.Entries, func(a, b PlacementEntry) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Element, b.Element), canonjson.CompareUTF16(a.Container, b.Container),
			canonjson.CompareUTF16(a.Workload, b.Workload), cmp.Compare(boolOrder(a.Declared), boolOrder(b.Declared)),
			canonjson.CompareUTF16(a.Status, b.Status),
		)
	})

	return out
}

func gapEntries(gaps []deployment.PlacementGap) []PlacementGapEntry {
	out := make([]PlacementGapEntry, 0, len(gaps))

	for i := range gaps {
		g := &gaps[i]
		paths := make([]string, 0, len(g.Locations))

		for _, l := range g.Locations {
			paths = append(paths, l.Path)
		}

		slices.SortFunc(paths, canonjson.CompareUTF16)

		out = append(out, PlacementGapEntry{
			Environment: g.Environment, Reason: string(g.Reason), Side: g.Side, Node: g.Node, Identity: g.Identity,
			Locations: slices.Compact(paths),
		})
	}

	slices.SortFunc(out, func(a, b PlacementGapEntry) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Environment, b.Environment), canonjson.CompareUTF16(a.Reason, b.Reason),
			canonjson.CompareUTF16(a.Side, b.Side), canonjson.CompareUTF16(a.Node, b.Node), canonjson.CompareUTF16(a.Identity, b.Identity),
		)
	})

	return slices.CompactFunc(out, func(a, b PlacementGapEntry) bool {
		return a.Environment == b.Environment && a.Reason == b.Reason && a.Side == b.Side && a.Node == b.Node &&
			a.Identity == b.Identity && slices.Equal(a.Locations, b.Locations)
	})
}

func boolOrder(b bool) int {
	if b {
		return 0
	}

	return 1
}
