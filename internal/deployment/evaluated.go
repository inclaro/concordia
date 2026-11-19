package deployment

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// The trace is built at the declared level. Rendered manifests and Terraform or OpenTofu plans give evidence of the
// level `rendered-or-evaluated` (a different level, never mixed into the declared chain): the trace lists it next to
// the chain, for the environment that has it and for the subject that a binding links to it. It never says that
// anything was deployed: a plan is what would be applied, and a render is what the committed sources produce.

// Kinds of the evaluated entities.
const (
	// KindRenderedWorkload is a workload of a rendered source that a binding maps to a subject.
	KindRenderedWorkload = "rendered-workload"
	// KindPlannedResource is a resource of a plan, by address, that an `infraResource` binding maps to a subject.
	KindPlannedResource = "planned-resource"
)

// Kinds of the evaluated sources.
const (
	SourceRender = "render"
	SourcePlan   = "plan"
)

// EvaluatedSource is one rendered source or plan of an environment.
type EvaluatedSource struct {
	// Kind is `render` or `plan`.
	Kind string `json:"kind"`
	// Tool is the renderer (`helm`, `kustomize`, `plain`) or the planner (`terraform`, `opentofu`).
	Tool string `json:"tool"`
	// Path is the declared source path or plan file.
	Path string `json:"path"`
	// Coverage is `complete`, `partial`, or `failed`. A failed source holds no evidence.
	Coverage string `json:"coverage"`
	// Digest is the output digest of a render, or the digest of a plan file; empty for a failed source.
	Digest string `json:"digest,omitempty"`
}

// EvaluatedEnvironment is the evidence of the level `rendered-or-evaluated` that one environment has.
type EvaluatedEnvironment struct {
	// Entities are the rendered workloads and the planned resources that a binding maps to a CALM element, by CALM id.
	Entities    map[string][]Entity
	Environment string
	Sources     []EvaluatedSource
}

// EvaluatedState is the state of the level `rendered-or-evaluated` in one environment.
type EvaluatedState struct {
	// Level is always `rendered-or-evaluated`.
	Level string `json:"level"`
	// Status is `evaluated` when a source holds evidence (complete or partial), else `not-available`.
	Status  string            `json:"status"`
	Sources []EvaluatedSource `json:"sources"`
}

// Evaluated is true when a source of the environment holds evidence.
func (e *EvaluatedState) Evaluated() bool { return e != nil && e.Status == levelEvaluated }

func (input *Input) evaluatedOf(environment string) *EvaluatedEnvironment {
	for i := range input.Evaluated {
		if input.Evaluated[i].Environment == environment {
			return &input.Evaluated[i]
		}
	}

	return nil
}

// evaluatedState gives the state of the evaluated level of an environment, nil when it has no source.
func (input *Input) evaluatedState(environment string) *EvaluatedState {
	env := input.evaluatedOf(environment)
	if env == nil || len(env.Sources) == 0 {
		return nil
	}

	state := &EvaluatedState{Level: LevelRenderedOrEvaluated, Status: levelNotAvailable, Sources: slices.Clone(env.Sources)}

	for i := range env.Sources {
		if env.Sources[i].Coverage != "failed" {
			state.Status = levelEvaluated
		}
	}

	slices.SortStableFunc(state.Sources, func(a, b EvaluatedSource) int {
		return canonjson.CompareUTF16(a.Kind+"\x00"+a.Path+"\x00"+a.Tool, b.Kind+"\x00"+b.Path+"\x00"+b.Tool)
	})

	return state
}

// evaluatedEntities gives the evaluated entities that a binding maps to the subject in the environment.
func (input *Input) evaluatedEntities(environment, subject string) []Entity {
	env := input.evaluatedOf(environment)
	if env == nil {
		return nil
	}

	return slices.Clone(env.Entities[subject])
}

// levelsOf gives the four levels of the trace. `rendered-or-evaluated` is `evaluated` when an environment has
// evidence of that level; the other two stay `not-available`.
func levelsOf(environments []EnvironmentTrace) []LevelState {
	levels := Levels()

	for i := range environments {
		if environments[i].Evaluated.Evaluated() {
			levels[1].Status = levelEvaluated
		}
	}

	return levels
}
