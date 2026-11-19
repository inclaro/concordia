package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/runid"
	"github.com/inclaro/concordia/internal/workspace"
)

// BuildInfo is the build data of the binary. cmd/concordia sets it from the linker variables.
type BuildInfo struct {
	Version  string
	Revision string
	Date     string
}

// GlobalOptions are the global options (the persistent flags of the root command).
// A string option that was not given is "".
type GlobalOptions struct {
	Workspace string
	Profile   string
	Format    string
	Output    string
	// FailOn is "error", "warning", "info" or "" (not given).
	FailOn  string
	Offline bool
	// NoColor is true for --no-color.
	NoColor bool
	Quiet   bool
	Verbose bool
	Trace   bool
	// Version is true for --version. Run answers --version before it parses the arguments.
	Version bool
}

// IO is everything that a command takes from the process: streams, terminal
// flags, environment and the working directory. Tests give their own.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Env is the environment. A nil map is an empty environment.
	Env map[string]string
	// Cwd is the working directory, an absolute OS path. Relative paths of the command line are relative to it.
	Cwd string
	// StdoutIsTTY is true when stdout is a terminal.
	StdoutIsTTY bool
	StdinIsTTY  bool
}

// Getenv gives an environment value, or "" when it is not set.
func (s *IO) Getenv(key string) string {
	return s.Env[key]
}

// ProbeResult is the result of running an external tool.
type ProbeResult struct {
	Stdout string
	Stderr string
	Code   int
}

// ReviewContracts are the digest contracts of the review commands: the current one and every known version with
// its equivalence marks.
type ReviewContracts struct {
	// Known are the known contract versions.
	Known impact.Registry
	// Current is the digest contract that the run uses.
	Current impact.Contract
}

// Deps are the dependencies that tests replace. The zero value gives the
// real behavior.
type Deps struct {
	// NewClient makes the CBM client. ws is nil when no workspace could be
	// loaded (only `doctor` runs then). nil gives a ProcessClient from the
	// workspace settings (see DefaultClient).
	NewClient func(ws *workspace.Workspace, c *Context) observation.Client
	// Probe runs an external tool (`doctor`). It gives nil when the tool
	// cannot be started. nil starts the process.
	Probe func(ctx context.Context, cmd string, args []string) *ProbeResult
	// Cbm replaces how `init`, `lock update`, `cbm install` and `doctor` find and pin the CBM build. nil gives the
	// real behavior (see CbmDeps).
	Cbm *CbmDeps
	// NewGit makes the git port of a workspace root. nil gives workspace.NewCliGit.
	NewGit func(root string) workspace.GitPort
	// ReviewFacts makes the fact source that supplies the current dependency items of the review commands
	// (`assessment approve`, `assessment status`). nil, or a nil result, means that no facts are available: what
	// needs the current items is then `unknown` and `assessment approve` refuses.
	ReviewFacts func(ctx context.Context, app *Context, wsp *workspace.Workspace) (impact.FactSource, error)
	// ReviewContracts sets the digest contracts of the review commands. nil: the current contract is impact.V1()
	// and it is the only known version.
	ReviewContracts *ReviewContracts
	// HTTPClient fetches a pack for `policy add <https-url>`. nil gives a client with a 30 s timeout that follows only https redirects.
	HTTPClient *http.Client
	// Open opens a path with the system viewer (`render --open`). nil starts the
	// viewer of the operating system and does not wait for it.
	Open func(path string) error
	// PollInterval is the delay between two `index_status` calls of `index`. 0 gives 1 s.
	PollInterval time.Duration
}

// Context is what a command handler works with: the global options, the
// logger, the run ID and the process IO.
type Context struct {
	Logger *logger.Logger
	Deps   Deps
	Build  BuildInfo
	// RunID is the ID of this run (`<yyyymmddThhmmssZ>-<8 hex>`).
	RunID string
	// WorkspacePath is the absolute OS path of the workspace file named by `--workspace`.
	WorkspacePath string
	IO            IO
	Opts          GlobalOptions
	// clients are the CBM clients that DefaultClient made in this run. CloseClients closes them.
	clients []openClient
	// UseColor is true when color output is allowed (no --no-color, no NO_COLOR, stdout is a terminal).
	UseColor bool
}

// NewContext makes the context of one run.
func NewContext(opts *GlobalOptions, stdio *IO, deps Deps, build BuildInfo) *Context {
	app := &Context{Opts: *opts, IO: *stdio, Deps: deps, Build: build}
	app.RunID = runid.New(app.Now())
	app.Logger = logger.New(logger.Options{
		Out:     stdio.Stderr,
		RunID:   app.RunID,
		Quiet:   opts.Quiet,
		Verbose: opts.Verbose,
		Trace:   opts.Trace,
	})
	app.UseColor = !opts.NoColor && stdio.Getenv("NO_COLOR") == "" && stdio.StdoutIsTTY
	app.WorkspacePath = app.Abs(opts.Workspace)

	return app
}

// remoteResolver makes the resolver of remote CALM resources for a workspace.
func (c *Context) remoteResolver(wsp *workspace.Workspace) *remote.Resolver {
	return &remote.Resolver{Root: wsp.Root, LockPath: workspace.LockPathFor(wsp.Root), Client: c.Deps.HTTPClient, Offline: c.Opts.Offline}
}

// openPath opens a path with Deps.Open, or with the viewer of the operating system.
// The viewer is not tied to the cancellation of ctx: it outlives the command.
func (c *Context) openPath(ctx context.Context, path string) error {
	if c.Deps.Open != nil {
		return c.Deps.Open(path)
	}

	detached := context.WithoutCancel(ctx)

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(detached, "open", path) //nolint:gosec // the path is an output path of this run.
	case "windows":
		cmd = exec.CommandContext(detached, "rundll32", "url.dll,FileProtocolHandler", path) //nolint:gosec // the path is an output path of this run.
	default:
		cmd = exec.CommandContext(detached, "xdg-open", path) //nolint:gosec // the path is an output path of this run.
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Args[0], err)
	}

	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release %s: %w", cmd.Args[0], err)
	}

	return nil
}

// Abs gives the absolute OS path of a path of the command line.
func (c *Context) Abs(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}

	return filepath.Join(c.IO.Cwd, filepath.FromSlash(p))
}

// Now gives the current time. CONCORDIA_NOW (from the IO environment, else
// the process environment) fixes it.
func (c *Context) Now() time.Time {
	if fixed := c.IO.Getenv(clock.EnvNow); fixed != "" {
		if t, err := clock.Parse(fixed); err == nil {
			return t
		}
	}

	return clock.Now()
}

// Out writes text to stdout.
func (c *Context) Out(text string) {
	_, _ = io.WriteString(c.IO.Stdout, text)
}

// Err writes text to stderr.
func (c *Context) Err(text string) {
	_, _ = io.WriteString(c.IO.Stderr, text)
}

// WriteOutput writes the primary machine output: to the `--output` file
// (atomic) when it is set, else to stdout. A non-empty pathOverride replaces
// `--output` for this call. A file that cannot be written gives CO-REP-001.
func (c *Context) WriteOutput(text, pathOverride string) error {
	target := pathOverride
	if target == "" {
		target = c.Opts.Output
	}

	if target == "" {
		c.Out(text)

		return nil
	}

	if err := fsutil.WriteFileAtomic(c.Abs(target), []byte(text)); err != nil {
		return diag.Fail(diag.REP001, diag.Params{keyPath: target, keyDetail: err.Error()})
	}

	return nil
}

// LoadWorkspace loads the workspace named by `--workspace`, with `--profile`.
func (c *Context) LoadWorkspace() (*workspace.Workspace, error) {
	return workspace.Load(c.WorkspacePath, c.Opts.Profile)
}
