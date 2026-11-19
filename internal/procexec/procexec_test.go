package procexec_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/procexec"
)

const helperEnv = "PROCEXEC_TEST_HELPER"

// TestHelperProcess is not a test: Run starts the test binary as a helper program.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("helper process only")
	}

	switch mode {
	case "echo":
		_, _ = fmt.Fprint(os.Stdout, "out-text")
		_, _ = fmt.Fprint(os.Stderr, "err-text")
	case "exit":
		code, _ := strconv.Atoi(os.Getenv("PROCEXEC_TEST_CODE"))
		_, _ = fmt.Fprint(os.Stdout, "partial")

		os.Exit(code)
	case "sleep":
		time.Sleep(time.Minute)
	case "big":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 1<<20))
	case "env":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("PROCEXEC_TEST_VALUE"))
	case "cwd":
		wd, _ := os.Getwd()
		_, _ = fmt.Fprint(os.Stdout, wd)
	}

	os.Exit(0)
}

// helper gives the command that starts the test binary as a helper program. Under coverage each
// helper writes its coverage data to its own directory: helpers that share one directory race on
// the coverage meta-data file, and on Windows the losing helper prints the rename error on stderr.
// A binary built without coverage rejects -test.gocoverdir, so the flag is only passed with it.
func helper(t *testing.T, mode string, extraEnv ...string) (name string, args []string, opts procexec.Options) {
	t.Helper()

	env := append(os.Environ(), helperEnv+"="+mode)
	args = []string{"-test.run=^TestHelperProcess$"}

	if testing.CoverMode() != "" {
		coverDir := t.TempDir()
		env = append(env, "GOCOVERDIR="+coverDir)
		args = append(args, "-test.gocoverdir="+coverDir)
	}

	env = append(env, extraEnv...)

	return os.Args[0], args, procexec.Options{Env: env}
}

func TestRunCapturesOutput(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "echo")

	res, err := procexec.Run(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	if res.Code != 0 || res.Stdout != "out-text" || res.Stderr != "err-text" || res.TimedOut {
		t.Fatalf("result %+v", res)
	}
}

func TestRunNonZeroExitIsNormalResult(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "exit", "PROCEXEC_TEST_CODE=3")

	res, err := procexec.Run(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	if res.Code != 3 || res.Stdout != "partial" {
		t.Fatalf("result %+v", res)
	}
}

func TestRunTimeout(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "sleep")
	opts.Timeout = 200 * time.Millisecond

	res, err := procexec.Run(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	if res.Code != -1 || !res.TimedOut {
		t.Fatalf("result %+v", res)
	}
}

func TestRunCanceledContextGivesError(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "sleep")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)

	defer cancel()

	res, err := procexec.Run(ctx, name, args, opts)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}

	if res.TimedOut || res.Code != -1 {
		t.Fatalf("result %+v", res)
	}
}

func TestRunMissingProgram(t *testing.T) {
	t.Parallel()

	_, err := procexec.Run(t.Context(), "concordia-no-such-program", nil, procexec.Options{})

	var spawn *procexec.SpawnError
	if !errors.As(err, &spawn) {
		t.Fatalf("err %v", err)
	}

	if !spawn.NotFound || spawn.Cmd != "concordia-no-such-program" {
		t.Fatalf("spawn error %+v", spawn)
	}

	if !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err does not wrap the cause: %v", err)
	}
}

func TestRunNotExecutable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "plain.txt")

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := procexec.Run(t.Context(), path, nil, procexec.Options{})

	var spawn *procexec.SpawnError
	if !errors.As(err, &spawn) {
		t.Fatalf("err %v", err)
	}

	if spawn.NotFound {
		t.Fatalf("a file that exists must not be NotFound: %+v", spawn)
	}
}

func TestRunEnvAndDir(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "env", "PROCEXEC_TEST_VALUE=v1")

	res, err := procexec.Run(t.Context(), name, args, opts)
	if err != nil || res.Stdout != "v1" {
		t.Fatalf("result %+v, err %v", res, err)
	}

	name, args, opts = helper(t, "cwd")
	opts.Dir = t.TempDir()

	res, err = procexec.Run(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.Stat(res.Stdout)
	want, _ := os.Stat(opts.Dir)

	if got == nil || want == nil || !os.SameFile(got, want) {
		t.Fatalf("cwd %q, want %q", res.Stdout, opts.Dir)
	}
}

func TestRunOutputLimit(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "big")
	opts.MaxBuffer = 1024

	res, err := procexec.Run(t.Context(), name, args, opts)
	if !errors.Is(err, procexec.ErrOutputTooLarge) {
		t.Fatalf("err %v", err)
	}

	if res.Code != -1 {
		t.Fatalf("result code %d", res.Code)
	}
}

// TestRunRetriesAProgramThatIsBusyForWriting reproduces ETXTBSY: on Linux, exec of a file
// fails while a descriptor holds it open for writing. Run retries, so the start succeeds
// once the writer closes the file.
func TestRunRetriesAProgramThatIsBusyForWriting(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY on exec is Linux behavior")
	}

	path := filepath.Join(t.TempDir(), "busy.sh")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.WriteString("#!/bin/sh\nprintf ready\n"); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(30 * time.Millisecond)

		_ = f.Close()
	}()

	res, err := procexec.Run(t.Context(), path, nil, procexec.Options{})
	if err != nil || res.Stdout != "ready" {
		t.Fatalf("result %+v, err %v", res, err)
	}
}
