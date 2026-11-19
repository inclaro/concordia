package views_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
)

// plcDeploymentModel is a system `sys` of one service `orders`, deployed in `cluster-a`; `cluster-b` is another target.
func plcDeploymentModel() *intent.ArchitectureModel {
	model := plcModel()
	model.Nodes = append(model.Nodes, intent.NodeDeclaration{
		ID: "sys", NodeType: "system", Name: "sys", Tags: []string{}, Bindings: []intent.ExtBinding{},
	})
	model.Relationships = append(model.Relationships, intent.RelationshipDeclaration{
		ID: "c1", Kind: intent.RelComposedOf, Container: "sys", Targets: []string{"orders", "billing"},
	})

	return model
}

// plcSemanticDigest is the semantic digest of the deployment view of one environment.
func plcSemanticDigest(t *testing.T, environment string, set deployment.PlacementSet, coverage []observation.PredicateCoverage) string {
	t.Helper()

	model := plcDeploymentModel()
	view := views.Definition{
		ID: "orders-" + environment, Kind: views.KindDeployment, Origin: views.OriginGenerated, Owner: "team", Scope: []string{"sys"},
		Abstraction: views.AbstractionDeployment, Environment: environment,
		Output: &views.Output{Path: "docs/architecture/views/orders-" + environment + ".dsl"},
	}

	placement := views.ProjectPlacement(model, set, environment, coverage)
	projection := views.BuildModelProjection(&view, &views.ModelInput{Model: model, Placement: &placement})

	digests, err := views.ComputeDigests(&views.DigestInput{View: &view, Model: model, ModelProjection: projection})
	if err != nil {
		t.Fatal(err)
	}

	return digests.Semantic
}

// TestPlacementMovesInStagingChangesOnlyTheStagingDigest: scenario "Placement moves in staging".
func TestPlacementMovesInStagingChangesOnlyTheStagingDigest(t *testing.T) {
	t.Parallel()

	coverage := slices.Concat(plcCoverage("prod", observation.CoverageComplete), plcCoverage("staging", observation.CoverageComplete))
	before := deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-a"), plcRecord("staging", "orders", "cluster-a")}}
	after := deployment.PlacementSet{Placements: []deployment.Placement{plcRecord("prod", "orders", "cluster-a"), plcRecord("staging", "orders", "cluster-b")}}

	prodBefore, prodAfter := plcSemanticDigest(t, "prod", before, coverage), plcSemanticDigest(t, "prod", after, coverage)
	stagingBefore, stagingAfter := plcSemanticDigest(t, "staging", before, coverage), plcSemanticDigest(t, "staging", after, coverage)

	if prodBefore != prodAfter {
		t.Errorf("the prod digest changed with the staging placement: %s -> %s", prodBefore, prodAfter)
	}

	if stagingBefore == stagingAfter {
		t.Error("the staging digest did not change when the placement moved in staging")
	}
}
