package observation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
)

const (
	neutralDirMode = 0o700
	neutralDirName = "cbm-session"
	neutralDirApp  = "concordia"
)

// startStream starts the CBM process, with no arguments, in a working
// directory that is not a repository.
func (c *ProcessClient) startStream(ctx context.Context) (*procexec.Stream, error) {
	dir := c.opts.WorkDir
	if dir == "" {
		var err error

		if dir, err = neutralDir(); err != nil {
			return nil, diag.Fail(diag.OBS001, diag.Detail(err.Error()))
		}
	}

	executable := c.opts.Executable
	if strings.ContainsRune(executable, filepath.Separator) {
		// A relative path would be evaluated from the new working directory.
		abs, err := filepath.Abs(executable)
		if err != nil {
			return nil, diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("cannot resolve the CBM executable '%s': %s", executable, err)))
		}

		executable = abs
	}

	stream, err := procexec.Start(ctx, executable, nil, procexec.Options{Dir: dir})
	if err != nil {
		return nil, c.spawnFailure(err)
	}

	return stream, nil
}

// neutralDir gives the working directory of a CBM process that has no
// configured one: a fixed, empty directory in the user cache. CBM indexes the
// working directory of an MCP session on start when `auto_index` is on, which
// is its default. A fixed directory makes that one cheap, one-time index of an
// empty project, and never an index of the repository that Concordia runs in.
func neutralDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}

	dir := filepath.Join(base, neutralDirApp, neutralDirName)
	if err := os.MkdirAll(dir, neutralDirMode); err != nil {
		return "", fmt.Errorf("cannot make the working directory of CBM: %w", err)
	}

	return dir, nil
}
