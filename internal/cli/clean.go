package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	cacheDir     = ".concordia"
	generatedDir = "build/concordia"
	// workspaceProject is the value of a bare `--project` (without a name).
	workspaceProject = "\x00workspace-project"
)

type cleanOptions struct {
	// project is "" (not given), workspaceProject (bare `--project`) or a CBM project name.
	project   string
	cache     bool
	generated bool
	all       bool
	yes       bool
}

func newCleanCmd(host *Host) *cobra.Command {
	var opts cleanOptions

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove caches, generated files, or the CBM project",
		// The project name can follow `--project` as a separate argument (`--project name`).
		Args: cobra.MaximumNArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return cleanHandler(ctx, app, opts, args)
		}),
	}

	cmd.Flags().BoolVar(&opts.cache, "cache", false, "remove "+cacheDir+"/")
	cmd.Flags().BoolVar(&opts.generated, "generated", false, "remove "+generatedDir+"/")
	cmd.Flags().StringVar(&opts.project, "project", "", "delete the CBM project (default: all workspace projects)")
	cmd.Flags().Lookup("project").NoOptDefVal = workspaceProject
	cmd.Flags().BoolVar(&opts.all, "all", false, "do all three actions")
	cmd.Flags().BoolVar(&opts.yes, "yes", false, "do not ask for confirmation")

	return cmd
}

// confirm asks the user on stderr and reads the answer from stdin. Only "y" confirms.
func confirm(app *Context, question string) bool {
	app.Err(question + " [y/N] ")

	if app.IO.Stdin == nil {
		return false
	}

	answer, _ := bufio.NewReader(app.IO.Stdin).ReadString('\n')

	return strings.EqualFold(strings.TrimSpace(answer), "y")
}

// cleanProjectArg resolves the project to clean from --project and the positional argument.
func cleanProjectArg(opts cleanOptions, args []string) (string, error) {
	project := opts.project

	switch {
	case len(args) == 1 && project == workspaceProject:
		project = args[0]
	case len(args) > 0:
		return "", diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unexpected argument '%s'", args[0])))
	}

	return project, nil
}

// cleanProjects lists the projects to delete: the named one, or every observed project.
func cleanProjects(wsp *workspace.Workspace, project string) []string {
	if project != "" && project != workspaceProject {
		return []string{project}
	}

	projects := make([]string, 0, len(wsp.Observation.Projects))
	for _, p := range wsp.Observation.Projects {
		projects = append(projects, p.Name)
	}

	return projects
}

// cleanPlan is what a clean run removes.
type cleanPlan struct {
	// project is "" (nothing), workspaceProject (all workspace projects) or a CBM project name.
	project string
	// projects is filled in once the workspace is loaded.
	projects  []string
	cache     bool
	generated bool
	doProject bool
}

// newCleanPlan resolves the requested actions from the options and positional arguments.
func newCleanPlan(opts cleanOptions, args []string) (cleanPlan, error) {
	project, err := cleanProjectArg(opts, args)
	if err != nil {
		return cleanPlan{}, err
	}

	plan := cleanPlan{
		project:   project,
		cache:     opts.all || opts.cache,
		generated: opts.all || opts.generated,
		doProject: opts.all || project != "",
	}

	if !plan.cache && !plan.generated && !plan.doProject {
		return cleanPlan{}, diag.Fail(diag.WRK010, diag.Detail("nothing to clean: pass --cache, --generated, --project, or --all"))
	}

	return plan, nil
}

// actions describes what a clean removes, for the prompt and the report.
func (p cleanPlan) actions() []string {
	var actions []string

	if p.cache {
		actions = append(actions, cacheDir+"/")
	}

	if p.generated {
		actions = append(actions, generatedDir+"/")
	}

	if p.doProject {
		actions = append(actions, fmt.Sprintf("CBM project '%s'", strings.Join(p.projects, "', '")))
	}

	return actions
}

// cleanConfirmed reports whether the clean may proceed, asking the user unless --yes was given.
func cleanConfirmed(app *Context, yes bool, actions []string) (bool, error) {
	if yes {
		return true, nil
	}

	if !app.IO.StdinIsTTY {
		return false, diag.Fail(diag.WRK011, diag.Detail("pass --yes (no terminal to ask)"))
	}

	return confirm(app, "Remove "+strings.Join(actions, ", ")+"?"), nil
}

func cleanHandler(ctx context.Context, app *Context, opts cleanOptions, args []string) (int, error) {
	plan, err := newCleanPlan(opts, args)
	if err != nil {
		return diag.InvalidInput, err
	}

	wsp, err := app.LoadWorkspace()
	if err != nil {
		return diag.Internal, err
	}

	plan.projects = cleanProjects(wsp, plan.project)
	actions := plan.actions()

	confirmed, err := cleanConfirmed(app, opts.yes, actions)
	if err != nil {
		return diag.InvalidInput, err
	}

	if !confirmed {
		app.Err("aborted\n")

		return diag.OK, nil
	}

	if err := removeTargets(ctx, app, wsp, plan); err != nil {
		return diag.Internal, err
	}

	app.Out("removed: " + strings.Join(actions, ", ") + "\n")

	return diag.OK, nil
}

// removeDirs deletes the selected workspace directories.
func removeDirs(root string, plan cleanPlan) error {
	for _, target := range []struct {
		dir string
		do  bool
	}{{cacheDir, plan.cache}, {generatedDir, plan.generated}} {
		if !target.do {
			continue
		}

		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(target.dir))); err != nil {
			return fmt.Errorf("remove %s: %w", target.dir, err)
		}
	}

	return nil
}

func removeTargets(ctx context.Context, app *Context, wsp *workspace.Workspace, plan cleanPlan) error {
	if err := removeDirs(wsp.Root, plan); err != nil {
		return err
	}

	if !plan.doProject {
		return nil
	}

	conn, err := app.Connect(ctx, wsp)
	if err != nil {
		return err
	}

	for _, project := range plan.projects {
		if _, err = conn.Client.Call(ctx, "delete_project", map[string]any{"project": project}); err != nil {
			return err
		}
	}

	return nil
}
