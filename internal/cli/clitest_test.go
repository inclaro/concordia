package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
)

// This file is the test harness of package cli. The tests of every command
// use it:
//
//	res := runCLI(t, "doctor", "--json")               // empty temp dir as cwd, no TTY, empty env
//	res := runIn(t, dir, "check", "--format", "json")  // fixed cwd
//	res := runWith(t, &runConfig{Dir: dir, Deps: cli.Deps{NewClient: ...}}, "index")
//
// A result has the exit code and the text of stdout and stderr.

// result is the outcome of one CLI run.
type result struct {
	Stdout string
	Stderr string
	Code   int
}

// runConfig is everything that a test can set for a run.
type runConfig struct {
	// Deps replaces the dependencies (CBM client, probes, CBM version).
	Deps cli.Deps
	// Ctx is the context of the run. nil gives the context of the test.
	Ctx context.Context //nolint:containedctx // a test option: the run needs this context, not a struct that lives long.
	// Env is the environment. nil is empty.
	Env map[string]string
	// Dir is the working directory. "" gives a new empty temporary directory.
	Dir string
	// Stdin is the text of stdin.
	Stdin string
	// StdinTTY and StdoutTTY say that the stream is a terminal.
	StdinTTY  bool
	StdoutTTY bool
}

// runWith runs the CLI with the arguments (without the program name).
func runWith(t *testing.T, cfg *runConfig, args ...string) result {
	t.Helper()

	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}

	if cfg.Ctx == nil {
		cfg.Ctx = t.Context()
	}

	var stdout, stderr bytes.Buffer

	code := cli.Run(cfg.Ctx, args, &cli.IO{
		Stdin:       strings.NewReader(cfg.Stdin),
		Stdout:      &stdout,
		Stderr:      &stderr,
		Env:         cfg.Env,
		Cwd:         cfg.Dir,
		StdoutIsTTY: cfg.StdoutTTY,
		StdinIsTTY:  cfg.StdinTTY,
	}, cfg.Deps, cli.BuildInfo{Version: "dev", Revision: "test", Date: "test"})

	return result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// runCLI runs the CLI in a new empty temporary directory.
func runCLI(t *testing.T, args ...string) result {
	t.Helper()

	return runWith(t, &runConfig{}, args...)
}

// runIn runs the CLI in a directory.
func runIn(t *testing.T, dir string, args ...string) result {
	t.Helper()

	return runWith(t, &runConfig{Dir: dir}, args...)
}
