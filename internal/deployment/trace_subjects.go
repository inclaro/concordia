package deployment

import (
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
)

const tagDeployable = "deployable"

func isActive(b *binding.Binding) bool { return b.Status != binding.StatusProposed }

// hasDeploymentMatch says whether a binding names a workload or an image: a deployment binding.
func hasDeploymentMatch(b *binding.Binding) bool {
	return slices.ContainsFunc(b.Match, func(m binding.Match) bool {
		return m.Kind == binding.MatchWorkload || m.Kind == binding.MatchImage
	})
}

// traceSubjects gives the sorted CALM IDs of the trace subjects: a node tagged `deployable`, a node that a
// `deployed-in` relationship lists as deployed, and a node with an active `workload` or `image` binding.
func traceSubjects(input *Input) []string {
	if input.Model == nil {
		return []string{}
	}

	chosen := map[string]struct{}{}
	addDeployableNodes(chosen, input.Model)
	addDeployedInTargets(chosen, input.Model)
	addDeploymentBindings(chosen, input.Bindings)

	known := map[string]struct{}{}
	for i := range input.Model.Nodes {
		known[input.Model.Nodes[i].ID] = struct{}{}
	}

	out := make([]string, 0, len(chosen))

	for id := range chosen {
		if _, ok := known[id]; ok {
			out = append(out, id)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// addDeployableNodes adds the nodes of the model that carry the tag `deployable`.
func addDeployableNodes(chosen map[string]struct{}, model *intent.ArchitectureModel) {
	for i := range model.Nodes {
		if slices.Contains(model.Nodes[i].Tags, tagDeployable) {
			chosen[model.Nodes[i].ID] = struct{}{}
		}
	}
}

// addDeployedInTargets adds the nodes that a `deployed-in` relationship lists.
func addDeployedInTargets(chosen map[string]struct{}, model *intent.ArchitectureModel) {
	for i := range model.Relationships {
		rel := &model.Relationships[i]
		if rel.Kind != intent.RelDeployedIn {
			continue
		}

		for _, id := range rel.Targets {
			chosen[id] = struct{}{}
		}
	}
}

// addDeploymentBindings adds the nodes that an active deployment binding maps.
func addDeploymentBindings(chosen map[string]struct{}, bindings []binding.Binding) {
	for i := range bindings {
		b := &bindings[i]
		if isActive(b) && hasDeploymentMatch(b) {
			chosen[b.ArchitectureRef] = struct{}{}
		}
	}
}
