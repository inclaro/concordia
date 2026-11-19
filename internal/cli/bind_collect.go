package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// projectGraph is what one frozen project gives to the bind commands.
type projectGraph struct {
	generation string
	nodes      []observation.ObservedNode
	files      []string
	links      []fileLink
}

// projectCollector freezes the selected projects and reads what the bind commands need.
type projectCollector struct {
	app        *Context
	wsp        *workspace.Workspace
	provider   observation.Provider
	strategies map[string]bool
}

// projectSnapshot is a frozen project with the prefix of its paths in the workspace.
type projectSnapshot struct {
	provider observation.Provider
	ref      *observation.GraphSnapshotRef
	wsp      *workspace.Workspace
	prefix   string
}

// collectAll freezes each selected project and gives its observed nodes and indexed
// files. Paths of other projects than the primary one get the prefix @<name>/.
func (c *projectCollector) collectAll(ctx context.Context, selected []workspace.Project) (*projectGraph, error) {
	merged := &projectGraph{}
	names := make([]string, 0, len(selected))
	generations := make([]string, 0, len(selected))

	for _, project := range selected {
		data, err := c.collectProject(ctx, &project)
		if err != nil {
			return nil, err
		}

		merged.nodes = append(merged.nodes, data.nodes...)
		merged.files = append(merged.files, data.files...)
		merged.links = append(merged.links, data.links...)
		names = append(names, project.Name)
		generations = append(generations, data.generation)
	}

	slices.SortFunc(merged.nodes, func(a, b observation.ObservedNode) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })
	slices.SortFunc(merged.files, canonjson.CompareUTF16)

	merged.generation = conformance.CombineGenerations(names, generations)

	return merged, nil
}

// collectProject freezes one project and gives its observed nodes (with the nodes that the
// selected strategies need), indexed files and import links. Paths get the project prefix.
func (c *projectCollector) collectProject(ctx context.Context, project *workspace.Project) (*projectGraph, error) {
	prefix := c.wsp.PathPrefix(project)

	ref, err := c.freeze(ctx, project, prefix)
	if err != nil {
		return nil, err
	}

	nodes, indexed, err := c.readFrozen(ctx, ref)
	if err != nil {
		return nil, err
	}

	for i := range nodes {
		nodes[i].FilePath = prefixWorkspacePath(prefix, nodes[i].FilePath)
	}

	graph := &projectGraph{generation: ref.GraphGeneration}

	for _, file := range indexed {
		graph.files = append(graph.files, prefix+file)
	}

	snap := &projectSnapshot{provider: c.provider, ref: ref, wsp: c.wsp, prefix: prefix}

	if graph.nodes, err = snap.strategyNodes(ctx, nodes, c.strategies); err != nil {
		return nil, err
	}

	if c.strategies[strategyCluster] {
		if graph.links, err = snap.importLinks(ctx); err != nil {
			return nil, err
		}
	}

	return graph, nil
}

// freeze freezes the graph of a project at its Git revision.
func (c *projectCollector) freeze(ctx context.Context, project *workspace.Project, prefix string) (*observation.GraphSnapshotRef, error) {
	revision, err := c.app.Git(c.wsp.AbsProjectRoot(project)).Revision(ctx)
	if err != nil {
		return nil, fmt.Errorf("git revision: %w", err)
	}

	ref, err := c.provider.Freeze(ctx, observation.FreezeRequest{
		Project: project.Name, GitRevision: revision,
		IndexedRevision: conformance.ReadIndexedRevision(c.wsp.Root, conformance.CoverageFileFor(prefix == "", project.Name), project.Name),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return ref, nil
}

// readFrozen gives the observed nodes and the indexed files of a frozen project, and checks the freeze held.
func (c *projectCollector) readFrozen(ctx context.Context, ref *observation.GraphSnapshotRef) (nodes []observation.ObservedNode, indexed []string, err error) {
	nodes, err = binding.CollectObservedNodes(ctx, c.provider, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("collect observed nodes: %w", err)
	}

	indexed, err = c.provider.ListFiles(ctx, ref)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := c.provider.VerifyFrozen(ctx, ref); err != nil {
		return nil, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return nodes, indexed, nil
}

// strategyNodes adds to the nodes of a project the nodes that the selected strategies need.
func (s *projectSnapshot) strategyNodes(ctx context.Context, nodes []observation.ObservedNode, strategies map[string]bool) ([]observation.ObservedNode, error) {
	if strategies[strategyRoute] {
		var err error
		if nodes, err = s.routeNodes(ctx, nodes); err != nil {
			return nil, err
		}
	}

	if strategies[strategyDeployment] {
		return s.withDeployedNodes(ctx, nodes)
	}

	return nodes, nil
}

// withDeployedNodes merges the deployed nodes of the project into its nodes.
func (s *projectSnapshot) withDeployedNodes(ctx context.Context, nodes []observation.ObservedNode) ([]observation.ObservedNode, error) {
	deployed, err := s.deployedNodes(ctx)
	if err != nil {
		return nil, err
	}

	return mergeDeployedNodes(nodes, deployed), nil
}

// routeNodes adds the Route nodes that the HANDLES edges give, when the project has no Route node.
func (s *projectSnapshot) routeNodes(ctx context.Context, nodes []observation.ObservedNode) ([]observation.ObservedNode, error) {
	if slices.ContainsFunc(nodes, func(node observation.ObservedNode) bool { return node.Label == nodeLabelRoute }) {
		return nodes, nil
	}

	handles, err := s.provider.FetchEdges(ctx, s.ref, []string{"HANDLES"})
	if err != nil {
		return nil, fmt.Errorf("fetch route edges: %w", err)
	}

	for id := range handles.Nodes {
		node := handles.Nodes[id]
		if node.Label != nodeLabelRoute || hasObservedID(nodes, id) {
			continue
		}

		node.FilePath = prefixWorkspacePath(s.prefix, node.FilePath)
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// hasObservedID says whether one of the nodes has the observed ID.
func hasObservedID(nodes []observation.ObservedNode, id string) bool {
	return slices.ContainsFunc(nodes, func(node observation.ObservedNode) bool { return node.ObservedID == id })
}

// mergeDeployedNodes adds the deployed nodes. A deployed node replaces the DEFINES node of the same ID:
// it has the attributed environment.
func mergeDeployedNodes(nodes, deployed []observation.ObservedNode) []observation.ObservedNode {
	byID := map[string]int{}
	for i := range nodes {
		byID[nodes[i].ObservedID] = i
	}

	for i := range deployed {
		if at, ok := byID[deployed[i].ObservedID]; ok {
			nodes[at] = deployed[i]
		} else {
			nodes = append(nodes, deployed[i])
		}
	}

	return nodes
}

// importLinks gives the file-to-file IMPORTS edges of a frozen project.
func (s *projectSnapshot) importLinks(ctx context.Context) ([]fileLink, error) {
	imports, err := s.provider.FetchEdges(ctx, s.ref, []string{edgeKindImports})
	if err != nil {
		return nil, fmt.Errorf("fetch import edges: %w", err)
	}

	var links []fileLink

	for _, e := range imports.Edges {
		from, to := imports.Nodes[e.From].FilePath, imports.Nodes[e.To].FilePath
		if from != "" && to != "" {
			links = append(links, fileLink{From: s.prefix + from, To: s.prefix + to})
		}
	}

	return links, nil
}

func prefixWorkspacePath(prefix, p string) string {
	if p == "" {
		return p
	}

	return prefix + p
}
