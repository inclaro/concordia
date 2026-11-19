package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// indexRequest is what one `index` run indexes: the selected projects of the workspace. A repoPath replaces the
// root of each of them.
type indexRequest struct {
	app      *Context
	wsp      *workspace.Workspace
	opts     *indexOptions
	repoPath string
	selected []workspace.Project
}

// indexRun is an indexRequest with its connection to CBM.
type indexRun struct {
	*indexRequest

	conn *observation.Connection
	poll indexPoller
}

// indexSelected indexes the selected projects, matches them across repositories when the workspace has several,
// and writes the coverage file and the index record of each.
func indexSelected(ctx context.Context, req *indexRequest) ([]indexSummary, error) {
	conn, err := req.app.Connect(ctx, req.wsp)
	if err != nil {
		return nil, err
	}

	start := time.Now()

	run := &indexRun{
		indexRequest: req,
		conn:         conn,
		poll: indexPoller{
			client:   conn.Client,
			timeout:  time.Duration(req.wsp.Observation.IndexTimeoutSeconds * float64(time.Second)),
			interval: indexPollInterval(req.app),
		},
	}

	results, repos, err := run.indexAll(ctx)
	if err != nil {
		return nil, err
	}

	if len(req.wsp.Observation.Projects) >= 2 {
		for i := range req.selected {
			run.crossIndex(ctx, &req.selected[i], repos[i])
		}
	}

	req.app.Logger.Trace(logger.TraceEvent{Phase: "index", Event: "ready", DurationMs: logger.Millis(time.Since(start))})

	return run.finishAll(ctx, results)
}

// indexPollInterval is the time between two polls of the index status.
func indexPollInterval(app *Context) time.Duration {
	if app.Deps.PollInterval <= 0 {
		return defaultPollInterval
	}

	return app.Deps.PollInterval
}

// indexAll indexes each selected project and waits until it is ready. It gives the result of each project by name
// and the repository path that each was indexed from, in the order of the selection.
func (r *indexRun) indexAll(ctx context.Context) (results map[string]observation.IndexResult, repos []string, err error) {
	repos = make([]string, len(r.selected))
	results = make(map[string]observation.IndexResult, len(r.selected))

	for i := range r.selected {
		proj := &r.selected[i]
		repos[i] = r.repoOf(proj)

		result, err := r.indexProject(ctx, proj, repos[i])
		if err != nil {
			return nil, nil, err
		}

		results[proj.Name] = result
	}

	return results, repos, nil
}

// repoOf is the repository path that the project is indexed from.
func (r *indexRun) repoOf(proj *workspace.Project) string {
	if r.repoPath != "" {
		return r.app.Abs(r.repoPath)
	}

	return r.wsp.AbsProjectRoot(proj)
}

func (r *indexRun) indexProject(ctx context.Context, proj *workspace.Project, repo string) (observation.IndexResult, error) {
	result, err := indexRepository(ctx, r.conn.Client, map[string]any{
		"repo_path":   repo,
		"mode":        cmp.Or(r.opts.mode, string(r.wsp.Observation.IndexMode)),
		keyName:       proj.Name,
		"persistence": r.opts.persist,
	})
	if err != nil {
		return observation.IndexResult{}, err
	}

	if err := r.poll.waitUntilReady(ctx, proj.Name); err != nil {
		return observation.IndexResult{}, err
	}

	return result, nil
}

// crossIndex runs the CBM cross-repository matching of one project against the other projects of
// the workspace. A failure is a warning (CO-OBS-014): the index continues without cross edges.
func (r *indexRun) crossIndex(ctx context.Context, proj *workspace.Project, repo string) {
	targets := []string{}

	for _, other := range r.wsp.Observation.Projects {
		if other.Name != proj.Name {
			targets = append(targets, other.Name)
		}
	}

	slices.Sort(targets)

	_, err := indexRepository(ctx, r.conn.Client, map[string]any{
		"mode": "cross-repo-intelligence", keyName: proj.Name, "repo_path": repo, "target_projects": targets,
	})
	if err == nil {
		err = r.poll.waitUntilReady(ctx, proj.Name)
	}

	if err != nil {
		r.app.Err(diag.FormatDiagnostic(diag.New(diag.OBS014, diag.Params{keyProject: proj.Name, keyDetail: err.Error()})) + "\n")
	}
}

// finishAll writes the coverage file and the index record of each selected project.
func (r *indexRun) finishAll(ctx context.Context, results map[string]observation.IndexResult) ([]indexSummary, error) {
	summaries := make([]indexSummary, 0, len(r.selected))

	for i := range r.selected {
		proj := &r.selected[i]
		result := results[proj.Name]

		summary, err := r.finishIndex(ctx, proj, &result)
		if err != nil {
			return nil, err
		}

		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// coverageGlobs are the include and exclude globs of the project, unless the command line gives its own.
func (r *indexRun) coverageGlobs(proj *workspace.Project) (include, exclude []string) {
	include, exclude = proj.Include, proj.Exclude
	if len(r.opts.include) > 0 {
		include = r.opts.include
	}

	if len(r.opts.exclude) > 0 {
		exclude = r.opts.exclude
	}

	return include, exclude
}

// indexedCoverage is the file coverage of a project at the git revision of its frozen graph generation.
type indexedCoverage struct {
	revision   string
	generation string
	coverage   observation.FileCoverage
}

// measureCoverage freezes the graph of the project and compares the files of the working tree with the files of the
// graph.
func (r *indexRun) measureCoverage(ctx context.Context, proj *workspace.Project) (*indexedCoverage, error) {
	root := r.wsp.AbsProjectRoot(proj)
	git := r.app.Git(root)

	revision, err := git.Revision(ctx)
	if err != nil {
		return nil, fmt.Errorf("git revision: %w", err)
	}

	ref, err := r.conn.Provider.Freeze(ctx, observation.FreezeRequest{Project: proj.Name, GitRevision: revision})
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	workspaceFiles, err := git.ListFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("git list files: %w", err)
	}

	cbmFiles, err := r.conn.Provider.ListFiles(ctx, ref)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	dirty, err := git.DirtyFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("git dirty files: %w", err)
	}

	include, exclude := r.coverageGlobs(proj)

	return &indexedCoverage{
		revision:   revision,
		generation: ref.GraphGeneration,
		coverage: observation.ComputeFileCoverage(&observation.FileCoverageInput{
			WorkspaceFiles: workspaceFiles,
			Include:        include,
			Exclude:        exclude,
			CbmFiles:       cbmFiles,
			DirtyFiles:     dirty,
			Sizes:          observation.FileSizes(root, workspaceFiles),
		}),
	}, nil
}

// finishIndex writes the coverage file and the index record of one project.
func (r *indexRun) finishIndex(ctx context.Context, proj *workspace.Project, indexResult *observation.IndexResult) (indexSummary, error) {
	project := proj.Name
	atRoot := r.wsp.PathPrefix(proj) == ""
	coverageFile := conformance.CoverageFileFor(atRoot, project)

	measured, err := r.measureCoverage(ctx, proj)
	if err != nil {
		return indexSummary{}, err
	}

	content, err := coverageFileContent(project, measured.generation, measured.revision, &measured.coverage)
	if err != nil {
		return indexSummary{}, err
	}

	if err = fsutil.WriteFileAtomic(r.wsp.ResolveInRoot(coverageFile), []byte(content)); err != nil {
		return indexSummary{}, diag.Fail(diag.REP001, diag.Params{keyPath: coverageFile, keyDetail: err.Error()})
	}

	// `check` reads the index result (excluded directories, files not indexed, partly parsed files) as coverage evidence.
	record := indexResult.Record(project, measured.generation, measured.revision)
	if writeErr := conformance.WriteIndexRecord(r.wsp.Root, conformance.IndexRecordFileFor(atRoot, project), &record); writeErr != nil {
		return indexSummary{}, writeErr //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return measured.summary(project, coverageFile), nil
}

// summary is the result of the index of the project, as data and as text.
func (c *indexedCoverage) summary(project, coverageFile string) indexSummary {
	cov := &c.coverage

	return indexSummary{
		data: map[string]any{
			keyProject:        project,
			"graphGeneration": c.generation,
			"sourceRevision":  nilIfEmpty(c.revision),
			"files":           map[string]any{"total": cov.Total, "covered": cov.Covered, "ratio": cov.Ratio},
			"coverageFile":    coverageFile,
		},
		text: fmt.Sprintf("project: %s\ngeneration: %s\nfiles: %d/%d covered (%.1f%%)\nexcluded: %d, stale: %d, unindexed: %d\ncoverage file: %s\n",
			project, c.generation, cov.Covered, cov.Total, cov.Ratio*percentFactor,
			len(cov.Excluded), len(cov.Stale), len(cov.Unindexed), coverageFile),
	}
}
