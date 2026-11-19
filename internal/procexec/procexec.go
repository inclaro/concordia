// Package procexec starts external programs without a shell.
package procexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"syscall"
	"time"
)

const (
	// DefaultMaxBuffer is the default limit for each of stdout and stderr (512 MiB).
	DefaultMaxBuffer = 512 * 1024 * 1024

	// waitDelay is the time that Run waits for the output pipes after it kills a process.
	waitDelay = 5 * time.Second

	// busyRetries and busyBackoff bound the retries of a start that fails with
	// ETXTBSY (see startCommand).
	busyRetries = 5
	busyBackoff = 20 * time.Millisecond
)

// ErrOutputTooLarge is given (wrapped) when stdout or stderr exceeds Options.MaxBuffer.
var ErrOutputTooLarge = errors.New("process output too large")

// Options are the settings of Run.
type Options struct {
	// Dir is the working directory (OS path). Empty is the current directory.
	Dir string
	// Env is the environment of the process as "KEY=value" entries. nil gives
	// the environment of the current process. A non-nil value replaces it.
	Env []string
	// Timeout stops the process after this time. Zero or less means no timeout.
	Timeout time.Duration
	// MaxBuffer limits each of stdout and stderr in bytes. Zero gives DefaultMaxBuffer.
	MaxBuffer int
}

// Result is the result of a process that started.
type Result struct {
	Stdout string
	Stderr string
	// Code is the exit code. -1 when the process was killed (timeout or signal).
	Code     int
	TimedOut bool
}

// SpawnError says that the program could not start.
type SpawnError struct {
	Err error
	Cmd string
	// NotFound is true when the program does not exist.
	NotFound bool
	// Permission is true when the program cannot be run for a permission reason.
	Permission bool
}

// Error gives the text of the error.
func (e *SpawnError) Error() string {
	return fmt.Sprintf("cannot start %s: %v", e.Cmd, e.Err)
}

// Unwrap gives the cause.
func (e *SpawnError) Unwrap() error {
	return e.Err
}

// limitedBuffer is a bytes.Buffer with a size limit.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		l.overflow = true

		return 0, fmt.Errorf("%w (limit %d bytes)", ErrOutputTooLarge, l.max)
	}

	return l.buf.Write(p) //nolint:wrapcheck // bytes.Buffer.Write never fails.
}

// Run runs a program without a shell. A non-zero exit code is a normal result.
// It gives *SpawnError when the program cannot start. A timeout gives
// Result{Code: -1, TimedOut: true} and no error. When ctx stops first, Run
// gives the result so far and the error of ctx.
func Run(ctx context.Context, name string, args []string, opts Options) (Result, error) {
	runCtx := ctx

	if opts.Timeout > 0 {
		var cancel context.CancelFunc

		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	limit := opts.MaxBuffer
	if limit <= 0 {
		limit = DefaultMaxBuffer
	}

	stdout := &limitedBuffer{max: limit}
	stderr := &limitedBuffer{max: limit}

	cmd, err := startCommand(runCtx, name, args, &opts, func(cmd *exec.Cmd) {
		cmd.Stdout = stdout
		cmd.Stderr = stderr
	})
	if err != nil {
		return Result{}, newSpawnError(name, err)
	}

	waitErr := cmd.Wait()
	res := Result{Stdout: stdout.buf.String(), Stderr: stderr.buf.String()}

	if stdout.overflow || stderr.overflow {
		res.Code = -1

		return res, fmt.Errorf("run %s: %w (limit %d bytes)", name, ErrOutputTooLarge, limit)
	}

	if waitErr == nil {
		return res, nil
	}

	if ctx.Err() != nil {
		res.Code = -1

		return res, fmt.Errorf("run %s: %w", name, ctx.Err())
	}

	if runCtx.Err() != nil {
		res.Code = -1
		res.TimedOut = true

		return res, nil
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		res.Code = exitErr.ExitCode()

		return res, nil
	}

	res.Code = -1

	return res, fmt.Errorf("run %s: %w", name, waitErr)
}

// newSpawnError describes why a program did not start.
func newSpawnError(name string, err error) *SpawnError {
	return &SpawnError{
		Cmd:        name,
		Err:        err,
		NotFound:   errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist),
		Permission: errors.Is(err, fs.ErrPermission),
	}
}

// startCommand starts the program. wire connects the streams of each new command. On Linux, exec of a file
// fails with ETXTBSY while any process holds it open for writing; a child of another goroutine's fork can hold
// such a descriptor until its own exec, for a just-written program. The condition
// clears within milliseconds, so the start is retried a few times with a new command
// (an exec.Cmd whose Start failed must not be started again).
func startCommand(ctx context.Context, name string, args []string, opts *Options, wire func(*exec.Cmd)) (*exec.Cmd, error) {
	for attempt := 0; ; attempt++ {
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // starting the given program without a shell is the function.
		cmd.Dir = opts.Dir
		cmd.Env = opts.Env
		cmd.WaitDelay = waitDelay

		wire(cmd)

		err := cmd.Start()
		if err == nil {
			return cmd, nil
		}

		if !errors.Is(err, syscall.ETXTBSY) || attempt >= busyRetries {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(busyBackoff * time.Duration(attempt+1)):
		}
	}
}
