package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	statusUnknown = "unknown"
	statusError   = "error"
	keyProject    = "project"

	keyName            = "name"
	keyGraphGeneration = "graphGeneration"
	keySourceRevision  = "sourceRevision"

	defaultPollInterval = time.Second
	percentFactor       = 100
)

type indexOptions struct {
	projects []string
	mode     string
	include  []string
	exclude  []string
	persist  bool
}

// Git gives the git port of a workspace root: Deps.NewGit, else the git CLI.
func (c *Context) Git(root string) workspace.GitPort { //nolint:ireturn // the GitPort is the point: tests replace it.
	if c.Deps.NewGit != nil {
		return c.Deps.NewGit(root)
	}

	return workspace.NewCliGit(root)
}

func newIndexCmd(host *Host) *cobra.Command {
	var opts indexOptions

	cmd := &cobra.Command{
		Use:   "index [repo-path]",
		Short: "Index the repository with codebase-memory-mcp and write the file coverage",
		Args:  cobra.MaximumNArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			repo := ""
			if len(args) == 1 {
				repo = args[0]
			}

			return diag.OK, indexHandler(ctx, app, repo, &opts)
		}),
	}

	cmd.Flags().StringArrayVar(&opts.projects, "project", nil, "workspace project name; repeatable (default: all projects)")
	addChoiceFlag(cmd, false, &opts.mode, "mode", "CBM index mode", "fast", "moderate", "full")
	cmd.Flags().BoolVar(&opts.persist, "persist", false, "write the CBM graph artifact to the repository")
	cmd.Flags().StringArrayVar(&opts.include, "include", nil, "coverage include glob; repeatable")
	cmd.Flags().StringArrayVar(&opts.exclude, "exclude", nil, "coverage exclude glob; repeatable")

	return cmd
}

type indexRepositoryResult struct {
	Status string `json:"status"`
	Hint   string `json:"hint"`
}

type indexStatusResult struct {
	Status string `json:"status"`
}

// indexPoller waits for the projects of a CBM client to be ready.
type indexPoller struct {
	client   observation.Client
	timeout  time.Duration
	interval time.Duration
}

// waitUntilReady polls `index_status` until the project is ready. A project
// that is not visible yet is polled again; all other errors stop the run.
func (p *indexPoller) waitUntilReady(ctx context.Context, project string) error {
	deadline := time.Now().Add(p.timeout)

	for {
		status, err := pollStatus(ctx, p.client, project)
		if err != nil {
			return err
		}

		if status == "ready" {
			return nil
		}

		if !time.Now().Before(deadline) {
			return diag.Fail(diag.OBS002, diag.Detail(fmt.Sprintf("project '%s' is not ready after %ds (status: %s)",
				project, int(math.Round(p.timeout.Seconds())), status)))
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for index: %w", ctx.Err())
		case <-time.After(p.interval):
		}
	}
}

// pollStatus reads the status of the project. A project that is not visible
// yet (CO-OBS-002) has the status "missing"; all other errors are given back.
func pollStatus(ctx context.Context, client observation.Client, project string) (string, error) {
	raw, err := client.Call(ctx, "index_status", map[string]any{keyProject: project, observation.ArgFormat: observation.FormatJSON})
	if err != nil {
		var failure *diag.Error
		if errors.As(err, &failure) && failure.Diagnostic.Code == diag.OBS002 {
			return "missing", nil
		}

		return "", err
	}

	var status indexStatusResult
	if err = json.Unmarshal(raw, &status); err != nil {
		return "", fmt.Errorf("decode index_status: %w", err)
	}

	if status.Status == "" {
		return statusUnknown, nil
	}

	return status.Status, nil
}

type coverageFiles struct {
	Languages map[string]observation.LanguageCoverage `json:"languages"`
	Ratio     float64                                 `json:"ratio"`
	Total     int                                     `json:"total"`
	Covered   int                                     `json:"covered"`
}

func coverageFileContent(project, generation, revision string, cov *observation.FileCoverage) (string, error) {
	var source any
	if revision != "" {
		source = revision
	}

	data, err := canonjson.Marshal(map[string]any{
		keyProject:         project,
		keyGraphGeneration: generation,
		keySourceRevision:  source,
		"files":            coverageFiles{Languages: cov.Languages, Ratio: cov.Ratio, Total: cov.Total, Covered: cov.Covered},
		"bytes":            cov.Bytes,
		"excluded":         cov.Excluded,
		"stale":            cov.Stale,
		"unindexed":        cov.Unindexed,
	})
	if err != nil {
		return "", fmt.Errorf("encode coverage: %w", err)
	}

	return string(data), nil
}

// indexSummary is the result of the index of one project.
type indexSummary struct {
	data map[string]any
	text string
}

func indexHandler(ctx context.Context, app *Context, repoPath string, opts *indexOptions) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	selected, err := conformance.SelectProjects(wsp, opts.projects)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if repoPath != "" && len(selected) != 1 {
		return diag.Fail(diag.WRK010, diag.Detail("the repo-path argument needs exactly one project; select it with --project"))
	}

	summaries, err := indexSelected(ctx, &indexRequest{app: app, wsp: wsp, opts: opts, repoPath: repoPath, selected: selected})
	if err != nil {
		return err
	}

	return writeIndexSummaries(app, summaries)
}

// indexProjects indexes the selected projects of the workspace and writes their coverage files.
func indexProjects(ctx context.Context, app *Context, wsp *workspace.Workspace, opts *indexOptions) ([]indexSummary, error) {
	selected, err := conformance.SelectProjects(wsp, opts.projects)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return indexSelected(ctx, &indexRequest{app: app, wsp: wsp, opts: opts, selected: selected})
}

// indexRepository calls `index_repository` and gives CO-OBS-001 when CBM reports an error. It gives the
// coverage-relevant part of the result (excluded directories, not indexed files, partly parsed files).
func indexRepository(ctx context.Context, client observation.Client, args map[string]any) (observation.IndexResult, error) {
	raw, err := client.Call(ctx, "index_repository", args)
	if err != nil {
		return observation.IndexResult{}, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	var indexed indexRepositoryResult
	if err = json.Unmarshal(raw, &indexed); err != nil {
		return observation.IndexResult{}, fmt.Errorf("decode index_repository: %w", err)
	}

	if indexed.Status == statusError {
		detail := "index_repository failed"
		if indexed.Hint != "" {
			detail += ": " + indexed.Hint
		}

		return observation.IndexResult{}, diag.Fail(diag.OBS001, diag.Detail(detail))
	}

	return observation.ParseIndexResult(raw) //nolint:wrapcheck // already has context.
}

// writeIndexSummaries writes the summaries: the text of each project, or the JSON of the one summary or of the
// list of them.
func writeIndexSummaries(app *Context, summaries []indexSummary) error {
	if app.Opts.Format != modelFormatJSON {
		return app.WriteOutput(indexSummariesText(summaries), "")
	}

	out, err := canonjson.Marshal(indexSummariesValue(summaries))
	if err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}

	return app.WriteOutput(string(out), "")
}

func indexSummariesText(summaries []indexSummary) string {
	var text strings.Builder
	for _, s := range summaries {
		text.WriteString(s.text)
	}

	return text.String()
}

// indexSummariesValue is the data of the only summary, else the list of the data of all of them.
func indexSummariesValue(summaries []indexSummary) any {
	if len(summaries) == 1 {
		return summaries[0].data
	}

	list := make([]any, len(summaries))
	for i := range summaries {
		list[i] = summaries[i].data
	}

	return list
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}
