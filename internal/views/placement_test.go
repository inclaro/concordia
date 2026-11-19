package views_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
)

// plcModel declares `orders` deployed in `cluster-a`, and `billing` with no deployed-in.
func plcModel() *intent.ArchitectureModel {
	ids := []string{"orders", "billing", "cluster-a", "cluster-b"}
	nodes := make([]intent.NodeDeclaration, 0, len(ids))

	for _, id := range ids {
		nodes = append(nodes, intent.NodeDeclaration{ID: id, NodeType: "service", Name: id, Tags: []string{}, Bindings: []intent.ExtBinding{}})
	}

	return &intent.ArchitectureModel{
		Nodes: nodes,
		Relationships: []intent.RelationshipDeclaration{{
			ID: "d1", Kind: intent.RelDeployedIn, Container: "cluster-a", Targets: []string{"orders"},
		}},
	}
}

func plcRecord(env, node, container string) deployment.Placement {
	return deployment.Placement{
		Node: node, Container: container, Environment: env, Workload: "Deployment/" + node, HostingTarget: "Namespace/" + container,
		Locations: []deployment.Location{{Path: "k8s/" + env + ".yaml", Line: 3}},
	}
}

func plcCoverage(env string, status observation.CoverageStatus) []observation.PredicateCoverage {
	return []observation.PredicateCoverage{{
		Predicate: string(observation.KindPlacedIn), Scope: observation.CoverageScope{Environment: env}, Status: status,
		LossModes: []observation.LossMode{}, Reasons: []string{},
	}}
}

func plcStatuses(p views.PlacementProjection, element string) []string {
	var out []string

	for _, e := range p.Entries {
		if e.Element == element {
			out = append(out, e.Status)
		}
	}

	slices.Sort(out)

	return out
}

func TestProjectPlacementStatusTable(t *testing.T) {
	t.Parallel()

	complete := plcCoverage("prod", observation.CoverageComplete)
	partial := plcCoverage("prod", observation.CoveragePartial)
	nodeGap := deployment.PlacementGap{
		Environment: "prod", Reason: deployment.ReasonBindingGap, Side: deployment.SideHostingTarget, Node: "orders", Identity: "Namespace/x",
	}

	tests := []struct {
		name     string
		element  string
		set      deployment.PlacementSet
		coverage []observation.PredicateCoverage
		want     []string
	}{
		{"D, contains D only, complete coverage", "orders", deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-a")}}, complete, []string{"conforming"}},
		{"D, contains D only, any coverage", "orders", deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-a")}}, nil, []string{"conforming"}},
		{"D, none, no gap, complete", "orders", deployment.PlacementSet{}, complete, []string{"declared-only"}},
		{"D, none, no gap, not complete", "orders", deployment.PlacementSet{}, partial, []string{"unknown"}},
		{"D, none, no gap, no coverage record", "orders", deployment.PlacementSet{}, nil, []string{"unknown"}},
		{"D, none, a gap for the node, complete", "orders", deployment.PlacementSet{Gaps: []deployment.PlacementGap{nodeGap}}, complete, []string{"unknown"}},
		{
			"D, another container", "orders",
			deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-b")}},
			complete,
			[]string{"declared-only", "violating"},
		},
		{
			"D, reverse triple", "orders",
			deployment.PlacementSet{Placements: []deployment.Placement{{Node: "orders", Container: "cluster-a", Environment: "prod", Workload: "Deployment/cluster-a", Reverse: true}}},
			complete,
			[]string{"declared-only", "violating"},
		},
		{
			"D, conforming and another container", "orders",
			deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-a"), plcRecord("prod", "orders", "cluster-b")}},
			complete,
			[]string{"conforming", "violating"},
		},
		{"no D, any triple", "billing", deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "billing", "cluster-b")}}, complete, []string{"observed-only"}},
		{
			"Only in another environment", "orders",
			deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("staging", "orders", "cluster-a")}},
			complete,
			[]string{"declared-only"},
		},
		{
			"Only in another environment, incomplete", "orders",
			deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("staging", "orders", "cluster-a")}},
			partial,
			[]string{"unknown"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := plcStatuses(views.ProjectPlacement(plcModel(), test.set, "prod", test.coverage), test.element)
			if !slices.Equal(got, test.want) {
				t.Errorf("statuses of %s = %v, want %v", test.element, got, test.want)
			}
		})
	}
}

func TestProjectPlacementOnlyInAnotherEnvironment(t *testing.T) {
	t.Parallel()

	set := deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("staging", "orders", "cluster-a")}}
	coverage := slices.Concat(plcCoverage("prod", observation.CoverageComplete), plcCoverage("staging", observation.CoverageComplete))

	staging := views.ProjectPlacement(plcModel(), set, "staging", coverage)
	if got := plcStatuses(staging, "orders"); !slices.Equal(got, []string{"conforming"}) {
		t.Errorf("staging = %v", got)
	}

	prod := views.ProjectPlacement(plcModel(), set, "prod", coverage)
	if got := plcStatuses(prod, "orders"); !slices.Equal(got, []string{"declared-only"}) {
		t.Errorf("prod = %v", got)
	}

	for _, e := range prod.Entries {
		if e.Environment != "prod" || e.Observed {
			t.Errorf("prod has an observed or foreign entry: %+v", e)
		}
	}
}

// plcEntrySummary describes each entry as "status/gap count".
func plcEntrySummary(entries []views.PlacementEntry) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Status+"/"+strconv.Itoa(len(entries[i].Gaps)))
	}

	return out
}

// plcGapSummary describes each gap as "reason|node|identity|locations".
func plcGapSummary(gaps []views.PlacementGapEntry) []string {
	out := make([]string, 0, len(gaps))
	for i := range gaps {
		g := &gaps[i]
		out = append(out, g.Reason+"|"+g.Node+"|"+g.Identity+"|"+strings.Join(g.Locations, ","))
	}

	return out
}

func TestProjectPlacementGapWithABoundNode(t *testing.T) {
	t.Parallel()

	gap := deployment.PlacementGap{
		Environment: "prod", Reason: deployment.ReasonBindingGap, Side: deployment.SideHostingTarget, Node: "orders", Identity: "Namespace/x",
		Locations: []deployment.Location{{Path: "k8s/prod.yaml", Line: 9}, {Path: "k8s/prod.yaml", Line: 2}},
	}

	got := views.ProjectPlacement(plcModel(), deployment.PlacementSet{Gaps: []deployment.PlacementGap{gap}}, "prod", plcCoverage("prod", observation.CoverageComplete))

	if summary := plcEntrySummary(got.Entries); !slices.Equal(summary, []string{"unknown/1"}) {
		t.Fatalf("entries = %+v", got.Entries)
	}

	if want := []string{"binding-gap|orders|Namespace/x|k8s/prod.yaml"}; !slices.Equal(plcGapSummary(got.Gaps), want) {
		t.Errorf("gaps = %+v, want %v", got.Gaps, want)
	}
}

// plcAttachesUnboundWorkload says whether an entry carries the unbound workload, a gap, or the element `mystery`.
func plcAttachesUnboundWorkload(e *views.PlacementEntry) bool {
	return e.Workload == "Deployment/mystery" || len(e.Gaps) != 0 || e.Element == "mystery"
}

func TestProjectPlacementUnboundWorkloadShowsOnlyAsAGap(t *testing.T) {
	t.Parallel()

	gap := deployment.PlacementGap{
		Environment: "prod", Reason: deployment.ReasonBindingGap, Side: deployment.SideWorkload, Identity: "Deployment/mystery",
		Locations: []deployment.Location{{Path: "k8s/prod.yaml", Line: 4}},
	}

	got := views.ProjectPlacement(plcModel(), deployment.PlacementSet{Gaps: []deployment.PlacementGap{gap}}, "prod", plcCoverage("prod", observation.CoverageComplete))

	for i := range got.Entries {
		if plcAttachesUnboundWorkload(&got.Entries[i]) {
			t.Errorf("the unbound workload is attached: %+v", got.Entries[i])
		}
	}

	if want := []string{"binding-gap||Deployment/mystery|k8s/prod.yaml"}; !slices.Equal(plcGapSummary(got.Gaps), want) {
		t.Errorf("gaps = %+v", got.Gaps)
	}
}

func TestProjectPlacementAmbiguousBindingMakesEachCandidateUnknown(t *testing.T) {
	t.Parallel()

	model := plcModel()
	model.Relationships = append(model.Relationships, intent.RelationshipDeclaration{
		ID: "d2", Kind: intent.RelDeployedIn, Container: "cluster-a", Targets: []string{"billing"},
	})

	gaps := make([]deployment.PlacementGap, 0, 2)

	for _, node := range []string{"orders", "billing"} {
		gaps = append(gaps, deployment.PlacementGap{
			Environment: "prod", Reason: deployment.ReasonAmbiguousBinding, Side: deployment.SideWorkload, Node: node, Identity: "Deployment/shared",
		})
	}

	got := views.ProjectPlacement(model, deployment.PlacementSet{Gaps: gaps}, "prod", plcCoverage("prod", observation.CoverageComplete))

	for _, node := range []string{"orders", "billing"} {
		if s := plcStatuses(got, node); !slices.Equal(s, []string{"unknown"}) {
			t.Errorf("%s = %v", node, s)
		}
	}
}

// plcSameEntries says whether two projections list the same placements, ignoring locations.
func plcSameEntries(a, b []views.PlacementEntry) bool {
	return slices.EqualFunc(a, b, func(x, y views.PlacementEntry) bool {
		return x.Element == y.Element && x.Container == y.Container && x.Status == y.Status && x.Workload == y.Workload
	})
}

func plcHasNilList(p *views.PlacementProjection) bool {
	return p.Entries == nil || p.Gaps == nil
}

func TestProjectPlacementIsCanonical(t *testing.T) {
	t.Parallel()

	a := deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-a"), plcRecord("prod", "billing", "cluster-b")}}
	b := deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "billing", "cluster-b"), plcRecord("prod", "orders", "cluster-a")}}
	// A line move in a manifest is no change of the placement.
	b.Placements[0].Locations = []deployment.Location{{Path: "k8s/prod.yaml", Line: 99}}

	first := views.ProjectPlacement(plcModel(), a, "prod", nil)
	second := views.ProjectPlacement(plcModel(), b, "prod", nil)

	if len(first.Entries) == 0 || !plcSameEntries(first.Entries, second.Entries) {
		t.Errorf("first = %+v\nsecond = %+v", first.Entries, second.Entries)
	}

	none := views.ProjectPlacement(plcModel(), deployment.PlacementSet{}, "none", nil)
	if plcHasNilList(&first) || none.Entries == nil {
		t.Error("lists are nil")
	}
}
