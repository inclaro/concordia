package procexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// stderrTailBytes is the size of the end of stderr that a Stream keeps.
const stderrTailBytes = 64 * 1024

// Stream is a running program that the caller talks to through stdin and
// stdout. It keeps the end of stderr, for error messages.
type Stream struct {
	// Stdin writes to the program. Stop closes it.
	Stdin *os.File
	// Stdout reads from the program. It gives EOF when the program ends and
	// fails when Kill or Stop closes it.
	Stdout *os.File
	proc   *os.Process
	stderr *tailBuffer
	done   chan struct{}
}

// Start starts a program without a shell, with a pipe for stdin and for
// stdout. It gives *SpawnError when the program cannot start. Options.Timeout
// and Options.MaxBuffer do not apply, and ctx does not stop the program once
// it runs: the caller stops the Stream.
func Start(ctx context.Context, name string, args []string, opts Options) (*Stream, error) {
	inRead, inWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe for %s: %w", name, err)
	}

	outRead, outWrite, err := os.Pipe()
	if err != nil {
		closeFiles(inRead, inWrite)

		return nil, fmt.Errorf("pipe for %s: %w", name, err)
	}

	tail := &tailBuffer{max: stderrTailBytes}

	cmd, err := startCommand(context.WithoutCancel(ctx), name, args, &opts, func(cmd *exec.Cmd) {
		cmd.Stdin = inRead
		cmd.Stdout = outWrite
		cmd.Stderr = tail
	})

	// The program has its own copies of these ends.
	closeFiles(inRead, outWrite)

	if err != nil {
		closeFiles(inWrite, outRead)

		return nil, newSpawnError(name, err)
	}

	s := &Stream{Stdin: inWrite, Stdout: outRead, proc: cmd.Process, stderr: tail, done: make(chan struct{})}

	go func() {
		_ = cmd.Wait()

		close(s.done)
	}()

	return s, nil
}

// Exited reports whether the program has ended.
func (s *Stream) Exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Kill stops the program at once and closes Stdout, so that a reader of
// Stdout does not wait. It is safe to call from another goroutine.
func (s *Stream) Kill() {
	_ = s.proc.Kill()
	_ = s.Stdout.Close()
}

// Stop closes Stdin and waits up to grace for the program to end, then kills
// it. It returns after the program has ended. Stderr is complete after Stop.
func (s *Stream) Stop(grace time.Duration) {
	_ = s.Stdin.Close()

	if grace > 0 {
		timer := time.NewTimer(grace)
		defer timer.Stop()

		select {
		case <-s.done:
		case <-timer.C:
		}
	}

	s.Kill()

	<-s.done
}

// Stderr gives the end of the stderr text of the program.
func (s *Stream) Stderr() string {
	return s.stderr.String()
}

// closeFiles closes files whose close errors do not matter.
func closeFiles(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
	mu  sync.Mutex
}

func (t *tailBuffer) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, data...)

	if extra := len(t.buf) - t.max; extra > 0 {
		n := copy(t.buf, t.buf[extra:])
		t.buf = t.buf[:n]
	}

	return len(data), nil
}

// String gives the kept text.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return string(t.buf)
}
