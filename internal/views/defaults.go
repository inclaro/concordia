package views

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/workspace"
)

// productionEnvironments are the environment IDs that the workspace marks as production
// by name. The `environments` key has no other marker.
var productionEnvironments = []string{"prod", "production"}

// IsProductionEnvironment says whether an environment ID names a production environment.
func IsProductionEnvironment(id string) bool {
	return slices.Contains(productionEnvironments, id)
}

// SuggestDefaults gives one information diagnostic (CO-DGM-005) for each default view that
// the workspace does not declare:
//
//   - a system-context view for each software system;
//   - a container view for each system that contains at least one container of node type
//     `service`;
//   - a deployment view for each production environment.
//
// A view satisfies a system suggestion when its scope names the system, and a deployment
// suggestion when it names the environment. Trust and data-flow views are not suggested:
// the workspace has no designation of sensitive scopes. The suggestions are advice, not
// findings: they never change an exit code. The result is sorted and never nil. A nil model
// gives no system suggestion.
func SuggestDefaults(model *intent.ArchitectureModel, wsp *workspace.Workspace) []diag.Diagnostic {
	out := []diag.Diagnostic{}
	uri := diag.Source{URI: workspaceURI(wsp)}

	suggest := func(detail string) {
		out = append(out, diag.New(diag.DGM005, diag.Detail(detail), diag.WithSource(uri)))
	}

	if model != nil {
		systems := softwareSystems(model)

		for _, id := range systems {
			if !declares(wsp.Views, KindSystemContext, id, "") {
				suggest(fmt.Sprintf("a system-context view for the system '%s'", id))
			}
		}

		for _, id := range systems {
			if hasService(model, id) && !declares(wsp.Views, KindContainer, id, "") {
				suggest(fmt.Sprintf("a container view for the system '%s', which has services", id))
			}
		}
	}

	for _, env := range wsp.Environments {
		if IsProductionEnvironment(env.ID) && !declares(wsp.Views, KindDeployment, "", env.ID) {
			suggest(fmt.Sprintf("a deployment view for the production environment '%s'", env.ID))
		}
	}

	return out
}

// declares says whether a view of the kind covers the system (scope) or the environment.
func declares(views []Definition, kind Kind, system, environment string) bool {
	return slices.ContainsFunc(views, func(v Definition) bool {
		if v.Kind != kind {
			return false
		}

		if environment != "" {
			return v.Environment == environment
		}

		return slices.Contains(v.Scope, system)
	})
}

// softwareSystems gives the IDs of the nodes at the software-system level, sorted.
func softwareSystems(model *intent.ArchitectureModel) []string {
	var ids []string

	for i := range model.Nodes {
		n := &model.Nodes[i]

		switch {
		case n.C4Level != "":
			if n.C4Level == intent.C4SoftwareSystem {
				ids = append(ids, n.ID)
			}
		case n.NodeType == "system" || n.NodeType == "ecosystem":
			ids = append(ids, n.ID)
		}
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	return ids
}

// hasService says whether the system contains a node of node type `service`.
func hasService(model *intent.ArchitectureModel, system string) bool {
	types := make(map[string]string, len(model.Nodes))
	for i := range model.Nodes {
		types[model.Nodes[i].ID] = model.Nodes[i].NodeType
	}

	for i := range model.Relationships {
		r := &model.Relationships[i]
		if r.Kind != intent.RelComposedOf || r.Container != system {
			continue
		}

		if slices.ContainsFunc(r.Targets, func(id string) bool { return types[id] == "service" }) {
			return true
		}
	}

	return false
}
