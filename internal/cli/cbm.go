package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/cbmpin"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
)

const (
	envLocalAppData = "LOCALAPPDATA"
	goosWindows     = "windows"
	executableBits  = 0o111
)

func newCbmCmd(host *Host) *cobra.Command {
	cbm := &cobra.Command{Use: "cbm", Short: "Manage the codebase-memory-mcp build that Concordia is tested with"}

	var dir string

	install := &cobra.Command{
		Use:   "install",
		Short: "Install the pinned codebase-memory-mcp build; it never replaces another build and does not configure your agents",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, cbmInstall(ctx, app, dir)
		}),
	}
	install.Flags().StringVar(&dir, "dir", "", "install directory (default: ~/.local/bin; on Windows %LOCALAPPDATA%\\Programs\\codebase-memory-mcp)")
	cbm.AddCommand(install)

	return cbm
}

// CbmDeps are the CBM seams of Deps. A test replaces them; the zero value gives the real behavior.
type CbmDeps struct {
	// Find gives the CBM version and the `sha256:<hex>` of its executable file for `init` and `lock update`. An
	// empty field is not known: a test that leaves SHA256 empty never hashes the real executable. nil finds the
	// executable that the workspace names (`codebase-memory-mcp` for `init`).
	Find func(ctx context.Context) CbmBuild
	// Pin is the CBM release that `cbm install` installs and `doctor` compares the executable with. nil gives the
	// pin that is embedded in the binary (cbmpin.Default).
	Pin *cbmpin.Pin
}

// cbmPin gives the pinned CBM release: Deps.Cbm.Pin, or the pin that is embedded in the binary.
func (c *Context) cbmPin() *cbmpin.Pin {
	if c.Deps.Cbm != nil && c.Deps.Cbm.Pin != nil {
		return c.Deps.Cbm.Pin
	}

	return cbmpin.Default()
}

// markTestedBuild says in the cbm capability whether the executable is the pinned build. An executable with no
// digest, a platform that has no pinned build, and an incompatible CBM are left as they are.
func markTestedBuild(item *capability, pin *cbmpin.Pin) {
	asset, ok := pin.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok || !hasComparableBuild(item) {
		return
	}

	if item.SHA256 == asset.BinarySHA256 {
		item.Detail = joinDetail(item.Detail, "pinned build "+pin.Version()+", the build Concordia is tested with")

		return
	}

	detail := fmt.Sprintf("not the CBM build Concordia is tested with (pinned %s sha256:%s)", pin.Version(), firstHex(asset.BinarySHA256))
	if item.Status == statusAvailable {
		item.Status, item.Detail = statusDegraded, detail

		return
	}

	item.Detail = joinDetail(item.Detail, detail)
}

// hasComparableBuild says whether the capability names an executable that can be compared with the pin.
func hasComparableBuild(item *capability) bool {
	return item.SHA256 != "" && slices.Contains([]capabilityStatus{statusAvailable, statusDegraded}, item.Status)
}

// joinDetail joins two parts of a capability detail.
func joinDetail(first, second string) string {
	if first == "" {
		return second
	}

	return first + "; " + second
}

// cbmFile is a CBM executable that `cbm install` found.
type cbmFile struct {
	// Path is the path of the file, with symbolic links resolved.
	Path string
	// SHA256 is `sha256:<hex>` of the file.
	SHA256 string
	// OnPath is true when the file was found through the PATH.
	OnPath bool
}

// cbmInstall installs the pinned CBM build. It does nothing when the pinned build is already installed, and it
// refuses when another build is: one CBM build for each user is allowed.
func cbmInstall(ctx context.Context, app *Context, dirFlag string) error {
	pin := app.cbmPin()

	asset, ok := pin.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return diag.Fail(diag.OBS006, diag.Detail(fmt.Sprintf("Concordia has no pinned CBM build for %s-%s", runtime.GOOS, runtime.GOARCH)))
	}

	dir, err := cbmInstallDir(app, dirFlag, runtime.GOOS)
	if err != nil {
		return err
	}

	found, err := findCbmFile(app, dir, asset.Binary)
	if err != nil {
		return err
	}

	if found.Path != "" {
		return cbmAlreadyInstalled(app, pin, &asset, &found)
	}

	if app.Opts.Offline {
		return diag.Fail(diag.OBS006, diag.Detail("the download needs network access; --offline is set"))
	}

	path, err := cbmpin.Install(ctx, &cbmpin.Request{
		Client: app.Deps.HTTPClient,
		URL:    pin.ArchiveURL(app.IO.Getenv(cbmpin.DownloadURLEnv), &asset),
		Dir:    dir,
		Asset:  asset,
	})
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	app.Out(fmt.Sprintf("Installed codebase-memory-mcp %s to %s\nThe digest %s is the pinned digest.\n", pin.Version(), path, asset.BinarySHA256))
	warnWhenNotOnPath(app, dir)
	app.Out(cbmAgentsNote)

	return nil
}

// cbmAgentsNote says what `cbm install` does not do.
const cbmAgentsNote = "This command only installs the binary. It does not run `codebase-memory-mcp install`, so it does not configure your AI agents (MCP clients).\n"

// cbmAlreadyInstalled reports the CBM build that is already there: the pinned build is fine, any other is refused.
func cbmAlreadyInstalled(app *Context, pin *cbmpin.Pin, asset *cbmpin.Asset, found *cbmFile) error {
	if found.SHA256 != asset.BinarySHA256 {
		return diag.Fail(diag.OBS005, diag.Params{
			keyPath:   found.Path,
			"found":   found.SHA256,
			"version": pin.Version(),
			"pinned":  asset.BinarySHA256,
		})
	}

	app.Out(fmt.Sprintf("The pinned codebase-memory-mcp build %s is already installed: %s\nNothing to do.\n", pin.Version(), found.Path))

	if !found.OnPath {
		warnWhenNotOnPath(app, filepath.Dir(found.Path))
	}

	app.Out(cbmAgentsNote)

	return nil
}

// warnWhenNotOnPath writes a warning when the directory is not in the PATH.
func warnWhenNotOnPath(app *Context, dir string) {
	if containsDir(pathDirs(app), dir) {
		return
	}

	app.Err(fmt.Sprintf("Warning: %s is not on the PATH. Add it, so that Concordia and your agents find codebase-memory-mcp.\n", dir))
}

// cbmInstallDir gives the install directory: the --dir value, or the default directory of the upstream installer.
func cbmInstallDir(app *Context, dirFlag, goos string) (string, error) {
	switch {
	case dirFlag != "":
		return app.Abs(dirFlag), nil
	case goos == goosWindows:
		return windowsInstallDir(app)
	default:
		return unixInstallDir(app)
	}
}

// windowsInstallDir is `%LOCALAPPDATA%\Programs\codebase-memory-mcp`.
func windowsInstallDir(app *Context) (string, error) {
	local := app.IO.Getenv(envLocalAppData)
	if local == "" {
		return "", diag.Fail(diag.WRK010, diag.Detail("%LOCALAPPDATA% is not set; give the install directory with --dir"))
	}

	return filepath.Join(local, "Programs", "codebase-memory-mcp"), nil
}

// unixInstallDir is `~/.local/bin`.
func unixInstallDir(app *Context) (string, error) {
	home := app.IO.Getenv("HOME")
	if home != "" {
		return filepath.Join(home, ".local", "bin"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", diag.Fail(diag.WRK010, diag.Detail("the home directory is not known; give the install directory with --dir"))
	}

	return filepath.Join(home, ".local", "bin"), nil
}

// pathDirs gives the directories of the PATH of the run (not of the process), cleaned.
func pathDirs(app *Context) []string {
	value := app.IO.Getenv("PATH")
	if value == "" {
		value = app.IO.Getenv("Path")
	}

	var dirs []string

	for _, dir := range filepath.SplitList(value) {
		if dir != "" {
			dirs = append(dirs, filepath.Clean(dir))
		}
	}

	return dirs
}

// containsDir says whether the list has the directory.
func containsDir(dirs []string, dir string) bool {
	dir = filepath.Clean(dir)

	return slices.ContainsFunc(dirs, func(candidate string) bool { return sameDir(candidate, dir) })
}

// sameDir compares two clean paths. Windows paths are not case-sensitive.
func sameDir(left, right string) bool {
	if runtime.GOOS == goosWindows {
		return strings.EqualFold(left, right)
	}

	return left == right
}

// findCbmFile looks for the CBM executable in the PATH of the run, then in the install directory. It gives the
// zero value when there is none. The file is resolved through symbolic links and hashed, as
// observation.ResolveExecutable does.
func findCbmFile(app *Context, dir, name string) (cbmFile, error) {
	for _, pathDir := range pathDirs(app) {
		if candidate := filepath.Join(pathDir, name); isExecutableFile(candidate) {
			return hashCbmFile(candidate, true)
		}
	}

	if candidate := filepath.Join(dir, name); isExecutableFile(candidate) {
		return hashCbmFile(candidate, false)
	}

	return cbmFile{}, nil
}

// isExecutableFile says whether the path is a file that can run. Windows has no execute bit.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	return runtime.GOOS == goosWindows || info.Mode().Perm()&executableBits != 0
}

func hashCbmFile(path string, onPath bool) (cbmFile, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return cbmFile{}, diag.Fail(diag.OBS006, diag.Detail(fmt.Sprintf("cannot resolve the CBM executable %s: %s", path, err)))
	}

	sum, err := digest.OfFile(resolved)
	if err != nil {
		return cbmFile{}, diag.Fail(diag.OBS006, diag.Detail(fmt.Sprintf("cannot read the CBM executable %s: %s", path, err)))
	}

	return cbmFile{Path: resolved, SHA256: sum, OnPath: onPath}, nil
}
