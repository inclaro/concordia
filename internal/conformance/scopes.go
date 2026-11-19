package conformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// IndexResultState is the index result of one project as the run found it.
type IndexResultState struct {
	// Record is the index result (excluded directories, files not indexed, partly parsed files).
	Record  *observation.IndexRecord `json:"record,omitempty"`
	Project string                   `json:"project"`
	// Reason says why it is not available.
	Reason string `json:"reason,omitempty"`
	// Available is false when no index result is available to the run.
	Available bool `json:"available"`
}

// RunLimits are the limits of a run and the ones that it went above.
type RunLimits struct {
	// Breaches are sorted by limit. Never nil.
	Breaches   []LimitBreach    `json:"breaches"`
	Configured workspace.Limits `json:"configured"`
}

// CrossCheckSummary says what the manifest cross-check read.
type CrossCheckSummary struct {
	// Environments are the environments whose sources were cross-checked.
	Environments []string `json:"environments"`
	// FilesSkipped are the templated files that the cross-check did not read (gap `unrendered-template`).
	FilesSkipped []string `json:"filesSkipped"`
	FilesChecked int      `json:"filesChecked"`
}

// RunCoverage is the coverage section of a run: the predicate records, the scopes that failed, the index results, the facts that no evaluation read,
// the limits, and the manifest cross-check. It shows a failure on a scope that no rule uses.
type RunCoverage struct {
	Predicates    []observation.PredicateCoverage `json:"predicates"`
	ScopeFailures []observation.ScopeFailure      `json:"scopeFailures"`
	IndexResults  []IndexResultState              `json:"indexResults"`
	SkipReasons   map[string]string               `json:"skipReasons"`
	CrossCheck    CrossCheckSummary               `json:"crossCheck"`
	Limits        RunLimits                       `json:"limits"`
	// Skipped counts, over all fact kinds, the edges that no evaluation can read. Facts are never removed.
	Skipped SkipCounts `json:"skipped"`
}

// NewRunCoverage gives a coverage section with no content and no nil collection.
func NewRunCoverage() RunCoverage {
	return RunCoverage{
		Predicates: []observation.PredicateCoverage{}, ScopeFailures: []observation.ScopeFailure{}, IndexResults: []IndexResultState{},
		SkipReasons: map[string]string{}, Limits: RunLimits{Breaches: []LimitBreach{}},
		CrossCheck: CrossCheckSummary{Environments: []string{}, FilesSkipped: []string{}},
	}
}

// scopeFailureOf gives the scope failure of a query-level error in one project: a CBM error (CO-OBS-001)
// or a query whose row count does not agree (CO-OBS-010). Any other error (the executable is missing or
// crashed, the version is not supported, the graph changed) is a connection-level failure and aborts the run.
func scopeFailureOf(project string, err error) (observation.ScopeFailure, bool) {
	var failure *diag.Error
	if !errors.As(err, &failure) {
		return observation.ScopeFailure{}, false
	}

	switch failure.Diagnostic.Code {
	case diag.OBS001, diag.OBS010:
		return observation.ScopeFailure{Project: project, Reason: failure.Diagnostic.Code + ": " + failure.Diagnostic.Message}, true
	default:
		return observation.ScopeFailure{}, false
	}
}

// primaryProject gives the name of the project whose paths have no prefix.
func primaryProject(wspace *workspace.Workspace) string {
	if len(wspace.Observation.Projects) == 0 {
		return ""
	}

	return wspace.Observation.Projects[0].Name
}

// projectOfPath gives the project of a workspace path: `@<name>/` names a project, any other path belongs to the primary one.
func projectOfPath(wspace *workspace.Workspace, p string) string {
	if rest, ok := strings.CutPrefix(p, "@"); ok {
		if name, _, found := strings.Cut(rest, "/"); found {
			if _, known := wspace.ProjectByName(name); known {
				return name
			}
		}
	}

	return primaryProject(wspace)
}

// bindingProjects gives the projects that the evidence of a binding can come from. A source root names one project by
// its prefix; any other match kind can match in every project.
func bindingProjects(b *binding.Binding, wspace *workspace.Workspace, all []string) []string {
	set := map[string]struct{}{}

	for i := range b.Match {
		if b.Match[i].Kind != binding.MatchSourceRoot {
			return all
		}

		set[projectOfPath(wspace, b.Match[i].Value)] = struct{}{}
	}

	if len(set) == 0 {
		return all
	}

	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// nodeProjects gives, for each CALM node with bindings, the projects that the evidence of the node can come from.
func nodeProjects(loaded *binding.Loaded, wspace *workspace.Workspace, all []string) map[string][]string {
	sets := map[string]map[string]struct{}{}

	for i := range loaded.Bindings {
		b := &loaded.Bindings[i]
		if sets[b.ArchitectureRef] == nil {
			sets[b.ArchitectureRef] = map[string]struct{}{}
		}

		for _, p := range bindingProjects(b, wspace, all) {
			sets[b.ArchitectureRef][p] = struct{}{}
		}
	}

	out := make(map[string][]string, len(sets))

	for id, set := range sets {
		list := make([]string, 0, len(set))
		for p := range set {
			list = append(list, p)
		}

		slices.SortFunc(list, canonjson.CompareUTF16)
		out[id] = list
	}

	return out
}

// withoutFailedDependents gives the bindings without the required ones whose evidence can come from a failed project:
// such a binding that matched nothing says that the scope is unknown, not that the workspace is wrong.
func withoutFailedDependents(loaded *binding.Loaded, wspace *workspace.Workspace, all []string, failures []observation.ScopeFailure) binding.Loaded {
	if len(failures) == 0 {
		return *loaded
	}

	failed := map[string]bool{}
	for _, f := range failures {
		failed[f.Project] = true
	}

	out := *loaded
	out.Bindings = slices.DeleteFunc(slices.Clone(loaded.Bindings), func(b binding.Binding) bool {
		return b.Required && slices.ContainsFunc(bindingProjects(&b, wspace, all), func(p string) bool { return failed[p] })
	})

	return out
}

// sourcesOfProject gives the sources of an environment that lie in a project, without the project prefix.
func sourcesOfProject(wspace *workspace.Workspace, env *workspace.Environment, project string) []string {
	var out []string

	prefix := wspace.PathPrefix(&workspace.Project{Name: project})

	for _, s := range env.Sources {
		if prefix == "" {
			if !strings.HasPrefix(s, "@") {
				out = append(out, s)
			}
		} else if rest, ok := strings.CutPrefix(s, prefix); ok {
			out = append(out, rest)
		}
	}

	return out
}

// hostProjects gives the selected projects whose files the sources of an environment select.
func hostProjects(wspace *workspace.Workspace, env *workspace.Environment, projects []string) []string {
	var out []string

	for _, p := range projects {
		if len(sourcesOfProject(wspace, env, p)) > 0 {
			out = append(out, p)
		}
	}

	return out
}

// attributeSelected gives the selected environments of a workspace path.
func (c *checker) attributeSelected(wspace *workspace.Workspace, p string) []string {
	var out []string

	for _, id := range wspace.Attribute(p) {
		if slices.Contains(c.environments.Selected, id) {
			out = append(out, id)
		}
	}

	return out
}

// readWorkspaceFile reads a workspace path of a selected project, confined to the project root.
func readWorkspaceFile(state *frozen, path string) ([]byte, error) {
	for i := range state.projects {
		fp := &state.projects[i]
		if fp.prefix != "" && strings.HasPrefix(path, fp.prefix) {
			return readConfined(fp.root, strings.TrimPrefix(path, fp.prefix))
		}
	}

	for i := range state.projects {
		if state.projects[i].prefix == "" {
			return readConfined(state.projects[i].root, path)
		}
	}

	return nil, fmt.Errorf("%w: %s", os.ErrNotExist, path)
}

func readConfined(root, rel string) ([]byte, error) {
	abs, err := fsutil.Confine(root, rel)
	if err != nil {
		return nil, fmt.Errorf("confine %s: %w", rel, err)
	}

	data, err := os.ReadFile(abs) //nolint:gosec // confined to the project root by fsutil.Confine.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}

	return data, nil
}

// coverageInput is what the coverage section is built from.
type coverageInput struct {
	wspace         *workspace.Workspace
	state          *frozen
	evidence       *fetched
	bindings       *binding.Loaded
	workspaceFiles []string
}

// readIndexResults reads the index result record of each project. The records are kept by project, and the
// coverage section and the inputs of the run show them (or the reason that they are not available).
func (c *checker) readIndexResults(wspace *workspace.Workspace, state *frozen, runCov *RunCoverage) (scopes []observation.ProjectScope, records map[string]*observation.IndexRecord) {
	scopes = make([]observation.ProjectScope, len(state.projects))
	records = map[string]*observation.IndexRecord{}

	for i := range state.projects {
		fp := &state.projects[i]
		file := IndexRecordFileFor(fp.prefix == "", fp.project.Name)
		rec, reason := ReadIndexRecord(wspace.Root, file, fp.project.Name, fp.ref.GraphGeneration)
		scopes[i] = observation.ProjectScope{Name: fp.project.Name, Index: rec}
		records[fp.project.Name] = rec

		runCov.IndexResults = append(runCov.IndexResults, IndexResultState{Project: fp.project.Name, Available: rec != nil, Reason: reason, Record: rec})

		if rec == nil {
			continue
		}

		if ref, err := inputRef(wspace.Root, filepath.Join(wspace.Root, filepath.FromSlash(file))); err == nil {
			c.indexInputs = append(c.indexInputs, ref)
		}
	}

	slices.SortFunc(runCov.IndexResults, func(a, b IndexResultState) int { return canonjson.CompareUTF16(a.Project, b.Project) })

	return scopes, records
}

// indexGaps gives the gaps of the index results: default-excluded directories under the sources of the selected
// environments (with the CO-OBS-020 diagnostic and the `.cbmignore` hint), raster images, and partly parsed files.
func (c *checker) indexGaps(wspace *workspace.Workspace, names []string, records map[string]*observation.IndexRecord) []observation.CoverageGap {
	var gaps []observation.CoverageGap

	for _, envID := range c.environments.Selected {
		env := environmentByID(wspace, envID)
		if env == nil {
			continue
		}

		for _, p := range names {
			sources := sourcesOfProject(wspace, env, p)
			found := observation.IndexGaps(records[p], []observation.EnvironmentSources{{ID: envID, Sources: sources}})

			for i := range found {
				gap := &found[i]
				if gap.Reason != observation.LossDefaultExcludedDirectory {
					continue
				}

				gaps = append(gaps, *gap)
				c.diags = append(c.diags, diag.New(diag.OBS020, diag.Params{"environment": envID, "source": gap.Source, "directory": strings.TrimSuffix(gap.Path, "/"), "hint": gap.Hint}))
			}
		}
	}

	for _, p := range names {
		found := observation.IndexGaps(records[p], nil)

		for i := range found {
			gaps = append(gaps, c.attributedGaps(wspace, p, &found[i])...)
		}
	}

	return gaps
}

// manifestGaps runs the manifest cross-check and gives its gaps, with the facts that show a loss by themselves.
func (c *checker) manifestGaps(input *coverageInput) ([]observation.CoverageGap, observation.CrossCheckResult) {
	wspace, state := input.wspace, input.state

	checked := observation.CrossCheck(&observation.CrossCheckInput{
		Files:     input.workspaceFiles,
		Attribute: func(p string) []string { return c.attributeSelected(wspace, p) },
		Read:      func(p string) ([]byte, error) { return readWorkspaceFile(state, p) },
		Evidence:  &input.evidence.result,
	})

	gaps := make([]observation.CoverageGap, 0, len(checked.Gaps))

	for i := range checked.Gaps {
		gap := checked.Gaps[i]
		gap.Project = projectOfPath(wspace, gap.Path)
		gaps = append(gaps, gap)
	}

	factGaps := observation.FactGaps(&input.evidence.result)

	for i := range factGaps {
		gaps = append(gaps, c.attributedGaps(wspace, projectOfPath(wspace, factGaps[i].Path), &factGaps[i])...)
	}

	return gaps, checked
}

// buildScope reads the index results, runs the manifest cross-check, and computes the predicate coverage
// of the run: the gaps (index result, cross-check, templates, facts), the scope failures, and the limits.
// It adds the diagnostics of the gaps and of the limits to the run.
func (c *checker) buildScope(input *coverageInput) (ScopeContext, RunCoverage) {
	wspace, state := input.wspace, input.state
	names := state.projectNames()
	runCov := NewRunCoverage()

	projectScopes, records := c.readIndexResults(wspace, state, &runCov)

	gaps := c.indexGaps(wspace, names, records)
	manifest, checked := c.manifestGaps(input)
	gaps = append(gaps, manifest...)
	runCov.CrossCheck = CrossCheckSummary{Environments: checked.Environments, FilesChecked: checked.FilesChecked, FilesSkipped: checked.FilesSkipped}

	// The Dockerfile set of a project: the gaps belong to the project and to no environment, so that they never
	// limit the workload set of an environment whose sources contain the Dockerfile.
	for i := range projectScopes {
		dockerfiles, unindexed := dockerfilesOf(wspace, &projectScopes[i], input.workspaceFiles, input.evidence.cbmFiles)
		projectScopes[i].UnindexedDockerfiles = unindexed
		gaps = append(gaps, observation.DockerfileGaps(records[projectScopes[i].Name], dockerfiles)...)
	}

	environments := make([]observation.EnvironmentScope, 0, len(c.environments.Selected))

	for _, id := range c.environments.Selected {
		if env := environmentByID(wspace, id); env != nil {
			scope := observation.EnvironmentScope{ID: id, Projects: hostProjects(wspace, env, names), CrossChecked: true}
			scope.UnsupportedSources, scope.Workloads = crossCheckedOf(&checked, id)
			environments = append(environments, scope)
		}
	}

	failures := slices.Clone(input.evidence.failures)
	slices.SortFunc(failures, func(a, b observation.ScopeFailure) int { return canonjson.CompareUTF16(a.Project, b.Project) })

	var rendered []observation.RenderedRecord
	if input.evidence.assurance != nil {
		rendered = input.evidence.assurance.rendered
	}

	runCov.Predicates = observation.ComputePredicateCoverage(&observation.PredicateInput{
		Families: state.caps.Families, Projects: projectScopes, Environments: environments, Failures: failures, Gaps: gaps,
		Rendered: rendered,
	})

	if failures != nil {
		runCov.ScopeFailures = failures
	}

	// Limits: graph and evidence size. The evidence is read, never cut.
	breaches := CheckGraphLimits(wspace.Limits, &input.evidence.result)
	runCov.Limits = RunLimits{Configured: wspace.Limits, Breaches: breaches}

	if breaches == nil {
		runCov.Limits.Breaches = []LimitBreach{}
	}

	for _, b := range breaches {
		c.diags = append(c.diags, b.Diagnostic())
	}

	scope := ScopeContext{
		Predicates: runCov.Predicates, Failures: failures, Projects: names,
		NodeProjects: nodeProjects(input.bindings, wspace, names), Limits: wspace.Limits, Breaches: breaches,
		UnsupportedFacts: len(input.evidence.result.Unsupported),
	}

	return scope, runCov
}

// crossCheckedOf gives the unsupported sources and the workload documents that the manifest cross-check found in an environment.
func crossCheckedOf(checked *observation.CrossCheckResult, environment string) (unsupported []string, workloads []observation.WorkloadDocument) {
	for _, source := range checked.UnsupportedSources {
		if source.Environment == environment {
			unsupported = append(unsupported, source.Path)
		}
	}

	for i := range checked.Workloads {
		if checked.Workloads[i].Environment == environment {
			workloads = append(workloads, checked.Workloads[i])
		}
	}

	return unsupported, workloads
}

// dockerfilesOf gives the Dockerfiles of a project in the workspace files: their project-relative paths, and the
// workspace paths of those that the graph has no file for and that no default-excluded directory explains.
func dockerfilesOf(wspace *workspace.Workspace, scope *observation.ProjectScope, workspaceFiles, graphFiles []string) (relative, unindexed []string) {
	indexed := make(map[string]struct{}, len(graphFiles))
	for _, f := range graphFiles {
		indexed[f] = struct{}{}
	}

	for _, file := range workspaceFiles {
		if !observation.IsDockerfile(file) || projectOfPath(wspace, file) != scope.Name {
			continue
		}

		rel := file
		if rest, ok := strings.CutPrefix(file, "@"+scope.Name+"/"); ok {
			rel = rest
		}

		relative = append(relative, rel)

		if _, ok := indexed[file]; !ok && !underExcludedDir(scope.Index, rel) {
			unindexed = append(unindexed, file)
		}
	}

	return relative, unindexed
}

// underExcludedDir says whether a project-relative file is below a directory that the provider skipped.
func underExcludedDir(rec *observation.IndexRecord, file string) bool {
	if rec == nil {
		return false
	}

	return slices.ContainsFunc(rec.ExcludedDirs, func(dir string) bool { return strings.HasPrefix(file, dir+"/") })
}

// attributedGaps gives a gap with its project and, for a gap in a file of declared environments, one gap for each
// selected environment of the file. A gap that already has an environment keeps it.
func (c *checker) attributedGaps(wspace *workspace.Workspace, project string, gap *observation.CoverageGap) []observation.CoverageGap {
	attributed := *gap
	attributed.Project = project

	if attributed.Environment != "" || attributed.Path == "" {
		return []observation.CoverageGap{attributed}
	}

	envs := c.attributeSelected(wspace, attributed.Path)
	if len(envs) == 0 {
		return []observation.CoverageGap{attributed}
	}

	out := make([]observation.CoverageGap, len(envs))

	for i, id := range envs {
		out[i] = attributed
		out[i].Environment = id
	}

	return out
}

func environmentByID(wspace *workspace.Workspace, id string) *workspace.Environment {
	for i := range wspace.Environments {
		if wspace.Environments[i].ID == id {
			return &wspace.Environments[i]
		}
	}

	return nil
}

// finishCoverage adds the results of the evaluation to the coverage section and the diagnostics of the run:
// the skip counts (CO-CON-033 for edges with a missing endpoint) and the warnings of the evaluation.
func (c *checker) finishCoverage(runCov *RunCoverage, evaluated *EvaluateOutput) {
	runCov.Skipped = evaluated.Skipped
	runCov.SkipReasons = evaluated.Skipped.Reasons()

	if evaluated.Skipped.EndpointMissing > 0 {
		c.diags = append(c.diags, diag.New(diag.CON033, diag.Params{paramCount: evaluated.Skipped.EndpointMissing, "reason": "endpoint missing"}))
	}

	c.diags = append(c.diags, evaluated.Diagnostics...)
}

// fetchProject reads the edges and the files of one project and verifies that its graph did not change.
func fetchProject(ctx context.Context, provider observation.Provider, project *frozenProject, nativeKinds []string) (observation.EvidenceQueryResult, []string, error) {
	part, err := provider.FetchEdges(ctx, project.ref, nativeKinds)
	if err != nil {
		return part, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	files, err := provider.ListFiles(ctx, project.ref)
	if err != nil {
		return part, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := provider.VerifyFrozen(ctx, project.ref); err != nil {
		return part, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return part, files, nil
}
