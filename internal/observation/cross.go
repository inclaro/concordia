package observation

import (
	"slices"
	"strings"
)

// CrossNativePrefix starts the native kind of each cross-project edge.
const CrossNativePrefix = "CROSS_"

// idProject gives the project of an observed ID `cbm:<project>:<native id>`.
func idProject(observedID string) string {
	rest, ok := strings.CutPrefix(observedID, "cbm:")
	if !ok {
		return ""
	}

	project, _, _ := strings.Cut(rest, ":")

	return project
}

// ResolveCrossProject points each cross-project edge (native kind CROSS_*) to a
// node of the project that the target belongs to. projects are the names of
// the selected projects, in workspace order. result holds the merged evidence
// of all projects.
//
// A target whose qualified name starts with `<P>.` for a project P is matched
// with the one node of P with the same qualified name without that prefix.
// Else it is matched with the one File node of P for the same file. An edge
// without a match is removed. A target with no project prefix keeps its edge.
// The result is the number of removed edges.
func ResolveCrossProject(result *EvidenceQueryResult, projects []string) int {
	byProject := map[string]*projectIndex{}

	for i := range projects {
		byProject[projects[i]] = &projectIndex{name: projects[i], byQN: map[string][]string{}, files: map[string][]string{}}
	}

	for id := range result.Nodes {
		idx := byProject[idProject(id)]
		if idx == nil {
			continue
		}

		n := result.Nodes[id]
		idx.byQN[n.QualifiedName] = append(idx.byQN[n.QualifiedName], id)

		if n.Label == "File" && n.FilePath != "" {
			idx.files[n.FilePath] = append(idx.files[n.FilePath], id)
		}
	}

	dropped := 0
	kept := result.Edges[:0]

	for _, edge := range result.Edges {
		if !strings.HasPrefix(edge.NativeKind, CrossNativePrefix) {
			kept = append(kept, edge)

			continue
		}

		target, ok := result.Nodes[edge.To]
		if !ok {
			kept = append(kept, edge)

			continue
		}

		matched, resolved := crossTarget(&target, projects, byProject)
		switch {
		case !matched:
			kept = append(kept, edge)
		case resolved == "":
			dropped++
		default:
			edge.To = resolved
			kept = append(kept, edge)
		}
	}

	clear(result.Edges[len(kept):])
	result.Edges = kept

	return dropped
}

type projectIndex struct {
	byQN  map[string][]string
	files map[string][]string
	name  string
}

// crossTarget gives matched=false when no project prefix matches the target,
// else the observed ID of the resolved node ("" when there is none).
func crossTarget(target *ObservedNode, projects []string, byProject map[string]*projectIndex) (matched bool, resolved string) {
	own := idProject(target.ObservedID)

	for _, proj := range projects {
		if proj == own {
			continue
		}

		rest, ok := strings.CutPrefix(target.QualifiedName, proj+".")
		if !ok {
			continue
		}

		idx := byProject[proj]
		if ids := idx.byQN[rest]; len(ids) == 1 {
			return true, ids[0]
		}

		if target.FilePath != "" {
			raw := strings.TrimPrefix(target.FilePath, "@"+own+"/")

			ids := slices.Concat(idx.files[raw], idx.files["@"+proj+"/"+raw])
			if len(ids) == 1 {
				return true, ids[0]
			}
		}

		return true, ""
	}

	return false, ""
}
