package views

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// Snapshot is the semantic state of one view in one run, for a generated and for an authored view: the model
// projection, the observed projection, and the digests. It is what the semantic digest covers (design D5).
type Snapshot struct {
	// Model is the model projection. Observed is the observed projection, nil when the view compares only
	// against the declared projection (an explicit missing value in the manifest).
	Model    *Projection
	Observed *Projection
	Digests  Digests
	// Placements and Coverage are the placement records and the predicate coverage of the run that a deployment
	// view reads. They are empty when the inputs have no observed evidence.
	Placements deployment.PlacementSet
	Coverage   []observation.PredicateCoverage
}

// SnapshotOf builds the projections and the digests of a view from the inputs of a run. It runs no tool and
// writes nothing. A view that needs the observed evidence of the run (NeedsObserved) with inputs that have none
// gives an error that wraps ErrNoObservedEvidence.
func SnapshotOf(inputs *Inputs, view *Definition) (*Snapshot, error) {
	if NeedsObserved(view) && inputs.Observed == nil {
		return nil, fmt.Errorf("view %s: %w", view.ID, ErrNoObservedEvidence)
	}

	out := &Snapshot{}

	if inputs.Observed != nil {
		out.Placements = deployment.Placements(inputs.Observed.Evidence, &inputs.Observed.Reconcile.BindingSet, inputs.Bindings, environmentsOf(inputs.Workspace), inputs.Model)
		out.Coverage = inputs.Observed.Coverage
	}

	modelInput := &ModelInput{Model: inputs.Model, Bindings: inputs.Bindings}

	if view.Kind == KindDeployment {
		placement := ProjectPlacement(inputs.Model, out.Placements, view.Environment, out.Coverage)
		modelInput.Placement = &placement
	}

	out.Model = BuildModelProjection(view, modelInput)

	if slices.Contains(view.EffectiveAgainst(), workspace.CompareObserved) {
		out.Observed = ObserveProjection(view, &ObserveInput{Reconcile: inputs.Observed.Reconcile, Bindings: inputs.Bindings})
	}

	digests, err := ComputeDigests(&DigestInput{
		View: view, Model: inputs.Model, ModelProjection: out.Model, ObservedProjection: out.Observed,
		Bindings: inputs.Bindings, Controls: inputs.Model.Controls, Policies: inputs.Policies,
	})
	if err != nil {
		return nil, fmt.Errorf("view %s: digests: %w", view.ID, err)
	}

	out.Digests = digests

	return out, nil
}
