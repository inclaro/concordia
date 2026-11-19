package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// ratioValue is a flag value that is a number from 0 to 1.
type ratioValue struct {
	target **float64
}

func (v ratioValue) String() string {
	if v.target == nil || *v.target == nil {
		return ""
	}

	return strconv.FormatFloat(**v.target, 'g', -1, 64)
}

func (v ratioValue) Set(text string) error {
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || !(number >= 0 && number <= 1) {
		return errors.New("must be a number from 0 to 1")
	}

	*v.target = &number

	return nil
}

func (v ratioValue) Type() string { return "ratio" }

type checkOptions struct {
	minimumCoverage *float64
	architecture    string
	projects        []string
	useBaseline     string
	waivers         string
	unknown         string
	evidenceDir     string
	policy          []string
	rule            []string
	control         []string
	changed         string
	rolloutReport   string
	environment     []string
	incremental     bool
}

func newCheckCmd(host *Host) *cobra.Command {
	var opts checkOptions

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run the conformance pipeline: CALM intent versus the observed code graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			if len(args) > 0 {
				if opts.changed == "" {
					return diag.InvalidInput, diag.Fail(diag.WRK010, diag.Detail("check takes no positional argument"))
				}

				opts.changed = args[0]
			}

			return checkHandler(ctx, app, &opts)
		}),
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.architecture, "architecture", "", "CALM architecture file (default: architecture.source)")
	flags.StringArrayVar(&opts.policy, "policy", nil, "policy pack file; repeatable; replaces policies.files and policies.packs")
	flags.BoolVar(&opts.incremental, "incremental", false,
		"reuse the review evaluation of an earlier run from the derived cache "+impact.CacheDir+" (the result is the one of a full evaluation)")
	flags.StringArrayVar(&opts.rule, "rule", nil, "run only this rule; repeatable")
	flags.StringArrayVar(&opts.control, "control", nil, "run only the rules of this CALM control; repeatable")
	flags.StringArrayVar(&opts.environment, "environment", nil, "declared environment id; repeatable; excluded environments have no verdict (default: all)")
	flags.StringArrayVar(&opts.projects, "project", nil, "workspace project name; repeatable (default: all projects)")
	flags.StringVar(&opts.useBaseline, "use-baseline", "", "baseline file (default: exceptions.baseline)")
	flags.StringVar(&opts.waivers, "waivers", "", "waiver register (default: exceptions.waivers)")
	flags.Var(ratioValue{target: &opts.minimumCoverage}, "minimum-coverage", "required file coverage, 0..1")
	addChoiceFlag(cmd, false, &opts.unknown, "unknown", "what an unknown rule verdict does",
		string(policy.UnknownFail), string(policy.UnknownWarn), string(policy.UnknownIgnore))
	flags.StringVar(&opts.evidenceDir, "evidence-dir", "", "write one evidence file for each finding")
	flags.StringVar(&opts.changed, "changed", "",
		"limit reporting to findings that touch files changed since the merge base of <git-ref> and HEAD (default: working-tree changes)")
	flags.Lookup("changed").NoOptDefVal = refHead
	flags.StringVar(&opts.rolloutReport, "rollout-report", "",
		"write the rollout report (canonical JSON, text summary on stderr) to this path, "+
			"default "+conformance.DefaultRolloutReportPath+"; give a path as --rollout-report=<path>")
	flags.Lookup("rollout-report").NoOptDefVal = conformance.DefaultRolloutReportPath

	return cmd
}

// checkEnv makes the environment of a pipeline run from the context.
func checkEnv(app *Context) conformance.Env {
	return conformance.Env{
		Logger:        app.Logger,
		RunID:         app.RunID,
		WorkspacePath: app.WorkspacePath,
		Cwd:           app.IO.Cwd,
		Profile:       app.Opts.Profile,
		ToolVersion:   app.Build.Version,
		FailOn:        diag.Severity(app.Opts.FailOn),
		Offline:       app.Opts.Offline,
	}
}

// checkDeps makes the pipeline dependencies from the context.
func checkDeps(app *Context) conformance.Deps {
	return conformance.Deps{
		CreateClient: func(wsp *workspace.Workspace, _ *logger.Logger) observation.Client { return app.NewClient(wsp) },
		CreateGit:    app.Deps.NewGit,
		RenderReport: reporting.Renderer(reporting.PrettyOptions{Color: app.UseColor, Verbose: app.Opts.Verbose}),
		Now:          app.Now,
		HTTPClient:   app.Deps.HTTPClient,

		ViewDiagnostics:   views.Diagnose,
		EvaluateViews:     views.Evaluate,
		EvaluateDecisions: adr.Evaluate,
		Getenv:            app.IO.Getenv,
	}
}

// runPipeline runs the check pipeline without writing any file.
func runPipeline(ctx context.Context, app *Context) (*conformance.Run, error) {
	return conformance.RunCheck(ctx, &conformance.CheckOptions{Env: checkEnv(app), Deps: checkDeps(app), SkipReports: true})
}

// writeRolloutReport writes the rollout report of a run as canonical JSON and prints its text summary on stderr.
func writeRolloutReport(app *Context, run *conformance.Run, path string) error {
	report := conformance.BuildRolloutReport(run)

	data, err := report.JSON()
	if err != nil {
		return fmt.Errorf("rollout report: %w", err)
	}

	if err := fsutil.WriteFileAtomic(app.Abs(path), data); err != nil {
		return diag.Fail(diag.REP001, diag.Params{keyPath: path, keyDetail: err.Error()})
	}

	app.Err(report.Text(path))

	return nil
}

func checkHandler(ctx context.Context, app *Context, opts *checkOptions) (int, error) {
	format := app.Opts.Format
	if format == "" {
		format = reporting.FormatPretty
	}

	if !reporting.IsReportFormat(format) {
		return diag.InvalidInput, diag.Fail(diag.WRK010,
			diag.Detail(fmt.Sprintf("unsupported format '%s' (%s)", format, strings.Join(reporting.Formats, ", "))))
	}

	run, err := conformance.RunCheck(ctx, &conformance.CheckOptions{
		MinimumCoverage: opts.minimumCoverage,
		Deps:            checkDeps(app),
		Env:             checkEnv(app),
		Architecture:    opts.architecture,
		Projects:        opts.projects,
		UseBaseline:     opts.useBaseline,
		Waivers:         opts.waivers,
		Unknown:         policy.UnknownDisposition(opts.unknown),
		EvidenceDir:     opts.evidenceDir,
		Policies:        opts.policy,
		Rules:           opts.rule,
		Controls:        opts.control,
		Environments:    opts.environment,
		Changed:         opts.changed,
		Incremental:     opts.incremental,
	})
	if err != nil {
		return diag.Internal, err
	}

	reportRedactions(app, uniqueRecords(run.DecisionRedactions))

	text, err := reporting.Render(format, run, reporting.PrettyOptions{Color: app.UseColor, Verbose: app.Opts.Verbose})
	if err != nil {
		return diag.Internal, err
	}

	if err := app.WriteOutput(string(text), ""); err != nil {
		return diag.Internal, err
	}

	if opts.rolloutReport != "" {
		if err := writeRolloutReport(app, run, opts.rolloutReport); err != nil {
			return diag.Internal, err
		}
	}

	return run.Summary.ExitCode, nil
}
