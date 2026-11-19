package procexec_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/procexec"
)

const streamHelperEnv = "PROCEXEC_STREAM_HELPER"

// TestStreamHelperProcess is not a test: Start runs the test binary as a program that answers each line of stdin.
func TestStreamHelperProcess(t *testing.T) {
	if os.Getenv(streamHelperEnv) == "" {
		t.Skip("helper process only")
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		_, _ = fmt.Fprintln(os.Stdout, "got "+scanner.Text())
	}

	_, _ = fmt.Fprint(os.Stderr, "stdin closed")

	os.Exit(0)
}

// streamHelper gives the command that starts the program of TestStreamHelperProcess.
func streamHelper(t *testing.T) (name string, args []string, opts procexec.Options) {
	t.Helper()

	name, args, opts = helper(t, "echo")
	args[0] = "-test.run=^TestStreamHelperProcess$"

	opts.Env = append(opts.Env, streamHelperEnv+"=1")

	return name, args, opts
}

func TestStreamTalksAndStops(t *testing.T) {
	t.Parallel()

	name, args, opts := streamHelper(t)

	s, err := procexec.Start(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fmt.Fprintln(s.Stdin, "hello"); err != nil {
		t.Fatal(err)
	}

	line, err := bufio.NewReader(s.Stdout).ReadString('\n')
	if err != nil || line != "got hello\n" {
		t.Fatalf("line %q, err %v", line, err)
	}

	if s.Exited() {
		t.Fatal("the program ended before Stop")
	}

	s.Stop(10 * time.Second)

	if !s.Exited() {
		t.Fatal("the program did not end after Stop")
	}

	if got := s.Stderr(); got != "stdin closed" {
		t.Fatalf("stderr %q, want the text after stdin closed", got)
	}
}

func TestStreamKillEndsReader(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "sleep")

	s, err := procexec.Start(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	read := make(chan error, 1)

	go func() {
		_, err := s.Stdout.Read(make([]byte, 1))
		read <- err
	}()

	s.Kill()

	select {
	case err := <-read:
		if err == nil {
			t.Fatal("read succeeded after Kill")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Kill did not end the read")
	}

	s.Stop(0)

	if !s.Exited() {
		t.Fatal("the program did not end")
	}
}

func TestStreamEndOfProgramGivesEOF(t *testing.T) {
	t.Parallel()

	name, args, opts := helper(t, "echo")

	s, err := procexec.Start(t.Context(), name, args, opts)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(s.Stdout)
	if err != nil || string(data) != "out-text" {
		t.Fatalf("stdout %q, err %v", data, err)
	}

	s.Stop(10 * time.Second)

	if got := s.Stderr(); got != "err-text" {
		t.Fatalf("stderr %q", got)
	}
}

func TestStreamMissingProgram(t *testing.T) {
	t.Parallel()

	_, err := procexec.Start(t.Context(), "/nonexistent/program", nil, procexec.Options{})

	var spawn *procexec.SpawnError
	if !errors.As(err, &spawn) || !spawn.NotFound {
		t.Fatalf("err = %v, want a SpawnError for a missing program", err)
	}
}
