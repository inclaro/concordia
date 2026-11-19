package adr

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

// Subject ref prefixes.
const (
	nodeRefPrefix         = "calm:node:"
	relationshipRefPrefix = "calm:relationship:"
	interfaceRefPrefix    = "calm:interface:"
	nodeSubjectPrefix     = nodeRefPrefix
)

// ResolveSubjects gives the subjects that an entry governs: the sorted unique refs `calm:node:<id>`,
// `calm:relationship:<id>`, and `calm:interface:<node-id>/<interface-id>`. A `select` expands to the matching
// `calm:node:<id>` refs (union semantics of the policy selectors). The subject refs of the `released` items must
// exist too, but are not part of the result.
//
// A ref to an unknown node, relationship, or interface (named in the error) and a `select` that matches no node
// give a CO-ADR-003 error with exit code 2.
func ResolveSubjects(model *intent.ArchitectureModel, entry *Entry) ([]string, error) {
	set := map[string]struct{}{}

	for i := range entry.Governs {
		item := &entry.Governs[i]

		if item.Select != nil {
			ids := policy.ResolveNodeSelector(model, item.Select.CalmNodes, item.Select.NodeTypes, item.Select.Tags)
			if len(ids) == 0 {
				return nil, subjectProblem(entry, item.Line, fmt.Sprintf("decision %s: select %s matches no CALM node",
					entry.ID, describeSelector(item.Select)))
			}

			for _, id := range ids {
				set[nodeSubjectPrefix+id] = struct{}{}
			}

			continue
		}

		if err := checkSubjectRef(model, entry, item.Ref, item.Line); err != nil {
			return nil, err
		}

		set[item.Ref] = struct{}{}
	}

	for i := range entry.Released {
		if rel := &entry.Released[i]; rel.Rule == nil {
			if err := checkSubjectRef(model, entry, rel.Ref, rel.Line); err != nil {
				return nil, err
			}
		}
	}

	out := make([]string, 0, len(set))
	for ref := range set {
		out = append(out, ref)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

func subjectProblem(entry *Entry, line int, detail string) *diag.Error {
	return diag.Fail(diag.ADR003, diag.Detail(detail), diag.WithSource(diag.Source{URI: entry.File, Line: line}))
}

func describeSelector(sel *Selector) string {
	var parts []string

	if len(sel.CalmNodes) > 0 {
		parts = append(parts, "calmNodes: ["+strings.Join(sel.CalmNodes, ", ")+"]")
	}

	if len(sel.NodeTypes) > 0 {
		parts = append(parts, "nodeTypes: ["+strings.Join(sel.NodeTypes, ", ")+"]")
	}

	if len(sel.Tags) > 0 {
		parts = append(parts, "tags: ["+strings.Join(sel.Tags, ", ")+"]")
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// checkSubjectRef checks that a `calm:` subject ref names a declaration of the model.
func checkSubjectRef(model *intent.ArchitectureModel, entry *Entry, ref string, line int) error {
	unknown := func(kind, id string) error {
		return subjectProblem(entry, line, fmt.Sprintf("decision %s governs unknown %s %q (%s)", entry.ID, kind, id, ref))
	}

	switch {
	case strings.HasPrefix(ref, nodeRefPrefix):
		id := strings.TrimPrefix(ref, nodeRefPrefix)
		if findNode(model, id) == nil {
			return unknown("node", id)
		}
	case strings.HasPrefix(ref, relationshipRefPrefix):
		id := strings.TrimPrefix(ref, relationshipRefPrefix)
		if !slices.ContainsFunc(model.Relationships, func(r intent.RelationshipDeclaration) bool { return r.ID == id }) {
			return unknown("relationship", id)
		}
	case strings.HasPrefix(ref, interfaceRefPrefix):
		nodeID, ifaceID, _ := strings.Cut(strings.TrimPrefix(ref, interfaceRefPrefix), "/")

		node := findNode(model, nodeID)
		if node == nil {
			return unknown("node", nodeID)
		}

		if !slices.Contains(node.Interfaces, ifaceID) {
			return unknown("interface", nodeID+"/"+ifaceID)
		}
	default:
		return subjectProblem(entry, line, fmt.Sprintf("decision %s: %q is not a calm:node, calm:relationship, or calm:interface ref", entry.ID, ref))
	}

	return nil
}

func findNode(model *intent.ArchitectureModel, id string) *intent.NodeDeclaration {
	for i := range model.Nodes {
		if model.Nodes[i].ID == id {
			return &model.Nodes[i]
		}
	}

	return nil
}
