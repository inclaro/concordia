package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// Main runs the CLI with the process streams and gives the exit code for
// os.Exit. An interrupt (Ctrl+C) gives exit code 130.
func Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, build BuildInfo) int {
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	cwd, err := os.Getwd()
	if err != nil {
		report := diag.New(diag.WRK099, diag.Detail("cannot read the working directory: "+err.Error()))
		_, _ = fmt.Fprintln(stderr, diag.FormatDiagnostic(report))

		return diag.Internal
	}

	stdio := IO{
		Stdin:       stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		Env:         environ(),
		Cwd:         cwd,
		StdoutIsTTY: isTerminal(stdout),
		StdinIsTTY:  isTerminal(stdin),
	}

	code := Run(sigCtx, args, &stdio, Deps{ReviewFacts: loadReviewFacts}, build)
	if sigCtx.Err() != nil {
		return diag.Interrupted
	}

	return code
}

// environ gives the process environment as a map.
func environ() map[string]string {
	env := map[string]string{}

	for _, entry := range os.Environ() {
		// Windows keeps entries like "=C:=C:\" that have an empty name.
		if key, value, ok := strings.Cut(entry, "="); ok && key != "" {
			env[key] = value
		}
	}

	return env
}

// isTerminal says whether a stream is a terminal: a character device that is
// not the null device.
func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}

	if null, nullErr := os.Stat(os.DevNull); nullErr == nil && os.SameFile(info, null) {
		return false
	}

	return true
}
