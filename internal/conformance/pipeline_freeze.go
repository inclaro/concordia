package conformance

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// frozenProject is the frozen state of one selected project.
type frozenProject struct {
	git         workspace.GitPort
	ref         *observation.GraphSnapshotRef
	prefix      string // "" for the primary project, else "@<name>/".
	root        string // absolute project root.
	revision    string
	dirtyDigest string
	changedBase string // set with --changed.
	project     workspace.Project
	dirty       []string // with prefix.
}

// frozen is the state that a run fixes before any query.
type frozen struct {
	conn *observation.Connection
	// changed is set with --changed.
	changed    *RunChanged
	generation string
	caps       observation.Capabilities
	projects   []frozenProject
}

// lead gives the project whose revision and digest the run shows: the primary
// project when it is selected, else the first selected project.
func (s *frozen) lead() *frozenProject {
	for i := range s.projects {
		if s.projects[i].prefix == "" {
			return &s.projects[i]
		}
	}

	return &s.projects[0]
}

func (s *frozen) multi() bool { return len(s.projects) > 1 }

func (s *frozen) projectNames() []string {
	names := make([]string, len(s.projects))
	for i := range s.projects {
		names[i] = s.projects[i].project.Name
	}

	return names
}

// prefixed gives the workspace paths of files of the project.
func (p *frozenProject) prefixed(files []string) []string {
	if p.prefix == "" {
		return files
	}

	out := make([]string, len(files))
	for i, f := range files {
		out[i] = p.prefix + f
	}

	return out
}

func prefixPath(prefix, p string) string {
	if prefix == "" || p == "" {
		return p
	}

	return prefix + p
}

// CombineGenerations gives the graph generation of a run from the project names
// and their graph generations (same order): the generation of the one project,
// or "multi:" and 16 hex of the sha256 of the sorted "<name>=<generation>" lines.
func CombineGenerations(names, generations []string) string {
	if len(generations) == 1 {
		return generations[0]
	}

	lines := make([]string, len(generations))
	for i := range generations {
		lines[i] = names[i] + "=" + generations[i]
	}

	slices.SortFunc(lines, compareStrings)

	return "multi:" + digest.SHA256HexString(strings.Join(lines, "\n"))[:16]
}

func generationOf(projects []frozenProject) string {
	names := make([]string, len(projects))
	gens := make([]string, len(projects))

	for i := range projects {
		names[i], gens[i] = projects[i].project.Name, projects[i].ref.GraphGeneration
	}

	return CombineGenerations(names, gens)
}

// unknownProjectError is CO-WRK-010 for a project name that the workspace does not have.
func unknownProjectError(wspace *workspace.Workspace, name string) *diag.Error {
	known := make([]string, len(wspace.Observation.Projects))
	for i, p := range wspace.Observation.Projects {
		known[i] = p.Name
	}

	return usage(fmt.Sprintf("unknown project '%s'; the workspace projects are '%s'", name, strings.Join(known, "', '")))
}

// SelectProjects applies `--project` (several allowed). No name selects all
// projects. The result is in workspace order.
func SelectProjects(wspace *workspace.Workspace, names []string) ([]workspace.Project, error) {
	if len(names) == 0 {
		return slices.Clone(wspace.Observation.Projects), nil
	}

	for _, n := range names {
		if _, ok := wspace.ProjectByName(n); !ok {
			return nil, unknownProjectError(wspace, n)
		}
	}

	var out []workspace.Project

	for _, p := range wspace.Observation.Projects {
		if slices.Contains(names, p.Name) {
			out = append(out, p)
		}
	}

	return out, nil
}

// partitionEnvironments splits the declared environment ids into the selected ones (all when ids is empty) and the
// excluded ones.
func partitionEnvironments(declared, ids []string) RunEnvironments {
	out := RunEnvironments{Selected: []string{}, Excluded: []string{}}

	for _, id := range declared {
		if len(ids) == 0 || slices.Contains(ids, id) {
			out.Selected = append(out.Selected, id)
		} else {
			out.Excluded = append(out.Excluded, id)
		}
	}

	return out
}

// SelectEnvironments resolves `--environment` ids against the declared
// environments. No ids selects all of them. An undeclared id is CO-WRK-017.
func SelectEnvironments(wspace *workspace.Workspace, ids []string) (RunEnvironments, error) {
	declared := make([]string, len(wspace.Environments))
	for i, e := range wspace.Environments {
		declared[i] = e.ID
	}

	for _, id := range ids {
		if !slices.Contains(declared, id) {
			return RunEnvironments{}, diag.Fail(diag.WRK017,
				diag.Detail(fmt.Sprintf("'%s'; the declared environments are '%s'", id, strings.Join(declared, "', '"))))
		}
	}

	out := partitionEnvironments(declared, ids)

	text, err := canonjson.Marshal(wspace.Environments)
	if err != nil {
		return out, fmt.Errorf("digest environments: %w", err)
	}

	out.Digest = digest.OfBytes(text)

	return out, nil
}

//nolint:ireturn // a factory for the replaceable git port.
func (c *checker) newGit(root string) workspace.GitPort {
	switch {
	case c.opts.Deps.Git != nil:
		return c.opts.Deps.Git
	case c.opts.Deps.CreateGit != nil:
		return c.opts.Deps.CreateGit(root)
	default:
		return workspace.NewCliGit(root)
	}
}

//nolint:ireturn // a factory for the replaceable CBM client.
func (c *checker) newClient(wspace *workspace.Workspace) observation.Client {
	if c.opts.Deps.CreateClient != nil {
		return c.opts.Deps.CreateClient(wspace, c.log)
	}

	c.owned = DefaultClient(wspace, c.log)

	return c.owned
}

// closeClient ends the CBM process of the client that the run made itself.
func (c *checker) closeClient() {
	if c.owned != nil {
		_ = c.owned.Close()
		c.owned = nil
	}
}

// freezeSource fixes the git revision and the dirty tree of one project, whose `project` is set.
func (c *checker) freezeSource(ctx context.Context, wspace *workspace.Workspace, proj *frozenProject, isGenerated func(string) bool) error {
	proj.prefix = wspace.PathPrefix(&proj.project)
	proj.root = wspace.AbsProjectRoot(&proj.project)
	proj.git = c.newGit(proj.root)

	revision, err := proj.git.Revision(ctx)
	if err != nil {
		return fmt.Errorf("git revision: %w", err)
	}

	allDirty, err := proj.git.DirtyFiles(ctx)
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}

	proj.revision = revision

	dirty := slices.Clone(allDirty)
	if proj.prefix == "" {
		dirty = slices.DeleteFunc(dirty, isGenerated)
	}

	proj.dirtyDigest, err = workspace.DirtyTreeDigest(proj.root, dirty)
	if err != nil {
		return err //nolint:wrapcheck // already has context.
	}

	proj.dirty = proj.prefixed(dirty)

	if revision == "" {
		c.diags = append(c.diags, diag.New(diag.WRK013, diag.Params{"project": proj.project.Name}))
	}

	return nil
}

// freezeGraph fixes the graph generation of each project, once connected.
func freezeGraph(ctx context.Context, wspace *workspace.Workspace, state *frozen) error {
	for i := range state.projects {
		proj := &state.projects[i]

		coverageFile := CoverageFileFor(proj.prefix == "", proj.project.Name)

		ref, err := state.conn.Provider.Freeze(ctx, observation.FreezeRequest{
			Project: proj.project.Name, GitRevision: proj.revision, IndexedRevision: ReadIndexedRevision(wspace.Root, coverageFile, proj.project.Name),
		})
		if err != nil {
			return err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		proj.ref = ref
	}

	return nil
}

// freeze fixes the source revision and the graph generation of each selected
// project before any query.
func (c *checker) freeze(ctx context.Context, wspace *workspace.Workspace, selected []workspace.Project) (*frozen, error) {
	state := &frozen{projects: make([]frozenProject, len(selected))}
	isGenerated := GeneratedPathFilter(wspace)

	for i := range selected {
		state.projects[i].project = selected[i]

		if err := c.freezeSource(ctx, wspace, &state.projects[i], isGenerated); err != nil {
			return nil, err
		}
	}

	var err error

	state.conn, err = observation.Connect(ctx, c.newClient(wspace), c.log)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	c.diags = append(c.diags, state.conn.Diagnostics...)
	state.caps = state.conn.Provider.Capabilities(ctx)

	if err := freezeGraph(ctx, wspace, state); err != nil {
		return nil, err
	}

	// Compare the profile with the frozen graph schemas (CO-OBS-017).
	state.caps = state.conn.Provider.Capabilities(ctx)
	c.diags = append(c.diags, state.caps.Diagnostics...)

	state.generation = generationOf(state.projects)

	return state, nil
}
