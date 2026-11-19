package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/diag"
)

const (
	rootDescription = "Check architecture conformance: CALM intent versus the observed code graph"
	argsSeparator   = "--"
	versionFlag     = "--version"
	workspaceFlag   = "--workspace"
	// DefaultWorkspaceFile is the default value of `--workspace`.
	DefaultWorkspaceFile = "concordia.yaml"
)

// Handler is the function of a command. It gets the context of the run and
// the positional arguments (the local options are read in the closure of the
// command constructor). It gives the exit code and an error:
//
//   - a *diag.Error is written to stderr and its exit code is used;
//   - any other error gives CO-WRK-099 and exit code 7.
//
// A handler gives (0, nil) for success. When it gives an error, the exit code is ignored.
type Handler func(ctx context.Context, c *Context, args []string) (int, error)

// Host connects the cobra commands to one run. A command constructor gets it
// and uses Action for the RunE of each runnable command.
type Host struct {
	err      error
	deps     Deps
	build    BuildInfo
	stdio    IO
	opts     GlobalOptions
	exitCode int
}

// Action makes the RunE function of a command. It builds the Context from
// the global options, runs the handler and keeps its result. A handler error
// is not given to cobra: Run maps it, so that only usage errors come from cobra.
func (host *Host) Action(handler Handler) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c := NewContext(&host.opts, &host.stdio, host.deps, host.build)
		defer c.CloseClients()

		host.exitCode, host.err = handler(cmd.Context(), c, args)

		return nil
	}
}

// newRootCmd builds the root command with the global options and all commands.
func newRootCmd(host *Host) *cobra.Command {
	root := &cobra.Command{
		Use:           "concordia",
		Short:         rootDescription,
		Long:          rootDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(host.stdio.Stdout)
	root.SetErr(host.stdio.Stderr)
	root.SetIn(host.stdio.Stdin)

	flags := root.PersistentFlags()
	flags.StringVar(&host.opts.Workspace, "workspace", DefaultWorkspaceFile, "workspace configuration")
	flags.StringVar(&host.opts.Profile, "profile", "", "named environment/profile")
	flags.StringVar(&host.opts.Format, "format", "", "output format where supported (pretty|json|junit|sarif ...)")
	flags.StringVar(&host.opts.Output, "output", "", "write primary output to a file")
	flags.BoolVar(&host.opts.Offline, "offline", false, "prohibit network access and require locked local resources")
	flags.BoolVar(&host.opts.NoColor, "no-color", false, "disable terminal color")
	flags.BoolVar(&host.opts.Quiet, "quiet", false, "emit result only")
	flags.BoolVar(&host.opts.Verbose, "verbose", false, "emit diagnostics to stderr")
	flags.BoolVar(&host.opts.Trace, "trace", false, "emit adapter and evaluation trace without source content")
	addChoiceFlag(root, true, &host.opts.FailOn, "fail-on", "minimum active severity that yields failure",
		string(diag.SeverityError), string(diag.SeverityWarning), string(diag.SeverityInfo))
	flags.BoolVar(&host.opts.Version, "version", false, "print Concordia and adapter versions")

	for _, construct := range commandConstructors() {
		root.AddCommand(construct(host))
	}

	return root
}

// optionValue reads the value of `--name value` or `--name=value`.
func optionValue(args []string, name string) (string, bool) {
	for i, arg := range args {
		if arg == name {
			if i+1 < len(args) {
				return args[i+1], true
			}

			return "", false
		}

		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value, true
		}
	}

	return "", false
}

// Run runs the CLI with the given arguments (without the program name) and
// gives the exit code. It never calls os.Exit. Main is Run with the process
// streams.
func Run(ctx context.Context, args []string, stdio *IO, deps Deps, build BuildInfo) (code int) {
	report := func(d diag.Diagnostic) {
		_, _ = fmt.Fprintln(stdio.Stderr, diag.FormatDiagnostic(d))
	}

	defer func() {
		if r := recover(); r != nil {
			report(diag.New(diag.WRK099, diag.Detail(fmt.Sprintf("panic: %v", r))))

			code = diag.Internal
		}
	}()

	// --version is answered before the arguments are parsed, so that it needs no subcommand.
	flagArgs := args
	if i := slices.Index(args, argsSeparator); i >= 0 {
		flagArgs = args[:i]
	}

	if slices.Contains(flagArgs, versionFlag) {
		file, ok := optionValue(flagArgs, workspaceFlag)
		if !ok {
			file = DefaultWorkspaceFile
		}

		_, _ = fmt.Fprint(stdio.Stdout, VersionText(ctx, stdio.Cwd, file, build))

		return diag.OK
	}

	host := &Host{stdio: *stdio, deps: deps, build: build}
	root := newRootCmd(host)

	if len(args) == 0 {
		_ = root.Help()

		return diag.OK
	}

	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		// Handlers never return their error to cobra, so this is a usage error:
		// unknown command, bad flag or bad flag value, wrong number of arguments.
		report(diag.New(diag.WRK010, diag.Detail(err.Error())))

		return diag.InvalidInput
	}

	if host.err == nil {
		return host.exitCode
	}

	if ctx.Err() != nil && errors.Is(host.err, ctx.Err()) {
		return diag.Interrupted
	}

	var failure *diag.Error
	if errors.As(host.err, &failure) {
		report(failure.Diagnostic)

		return failure.ExitCode
	}

	report(diag.New(diag.WRK099, diag.Detail(host.err.Error())))

	return diag.Internal
}
