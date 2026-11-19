package observation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// fakeCBM is a ProcessClient on the fake CBM process, with the files that the fake writes.
type fakeCBM struct {
	client  *observation.ProcessClient
	starts  string
	stopped string
}

// newFakeCBM makes a client that starts the fake CBM in the given mode. opts sets the timeouts, the retries, and
// the trace hook; the executable is the fake.
func newFakeCBM(t *testing.T, mode string, opts observation.ProcessOptions) *fakeCBM {
	t.Helper()

	// The default working directory of the process is below the user cache: keep it out of the real one.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	dir := t.TempDir()
	fake := &fakeCBM{starts: filepath.Join(dir, "starts"), stopped: filepath.Join(dir, "stopped")}

	env := fmt.Sprintf("%s=%s %s='%s' %s='%s'", fakeModeEnv, mode, fakeStartsEnv, fake.starts, fakeStoppedEnv, fake.stopped)
	args := "-test.run=^TestFakeCBM$"

	if testing.CoverMode() != "" {
		coverDir := t.TempDir()
		env += " GOCOVERDIR='" + coverDir + "'"
		args += " -test.gocoverdir='" + coverDir + "'"
	}

	opts.Executable = script(t, env+" exec '"+os.Args[0]+"' "+args)

	if opts.Timeout == 0 {
		opts.Timeout = testTimeout
	}

	if opts.IndexTimeout == 0 {
		opts.IndexTimeout = testTimeout
	}

	fake.client = observation.NewProcessClient(opts)

	t.Cleanup(func() { _ = fake.client.Close() })

	return fake
}

// lines counts the lines of a file that the fake writes. A file that does not exist has none.
func lines(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}

	if err != nil {
		t.Fatal(err)
	}

	return strings.Count(string(data), "\n")
}

// call runs a tool on the fake with the behaviour `behave`.
func (f *fakeCBM) call(ctx context.Context, tool, behave string) (json.RawMessage, error) {
	return f.client.Call(ctx, tool, map[string]any{"behave": behave, "project": "p"})
}

// echoed is the body of an echo answer of the fake.
type echoed struct {
	Args map[string]any `json:"args"`
	Tool string         `json:"tool"`
	Cwd  string         `json:"cwd"`
	PID  int            `json:"pid"`
}

// echo runs a tool and reads the echo of the fake.
func (f *fakeCBM) echo(t *testing.T, tool string) echoed {
	t.Helper()

	raw, err := f.call(t.Context(), tool, "")
	if err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}

	var got echoed
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("body %s: %v", raw, err)
	}

	return got
}

func TestProcessClient(t *testing.T) {
	runChecks(t, []namedCheck{
		{checkSessionBody, "one process serves the calls; the body is the text of the result"},
		{checkSessionWorkDir, "the process runs in a neutral directory, or in WorkDir"},
		{checkSessionSkipsNoise, "notifications and blank lines between a request and its response are skipped"},
		{checkSessionAnswersServerRequests, "a request of the server during a call gets a 'method not found' error"},
		{checkSessionLargeBody, "a response line of 20 MiB is read"},
		{checkSessionConcurrentCalls, "calls from several goroutines share one session"},
		{checkSessionMissingExecutable, "a missing executable gives CO-OBS-003 (exit 5)"},
		{checkSessionMissingProject, "a missing project (the real CBM error body) gives CO-OBS-002"},
		{checkSessionFailures, "error bodies, errors of the server, and broken framing give CO-OBS-001 (exit 3)"},
		{checkSessionStderrTail, "a process that dies gives the end of its stderr"},
		{checkSessionRetries, "retries read-only tools only, each with a new process"},
		{checkSessionTimeout, "a timeout kills the process and the next call starts a new one"},
		{checkSessionStoppedContext, "a stopped context is not a diagnostic and kills the process"},
		{checkSessionAdmission, "a build that the CBM daemon refuses gives a CO-OBS-001 that is not retried"},
		{checkSessionClose, "Close ends the process; a later call starts a new one"},
		{checkSessionOnCall, "OnCall sees each call"},
		{checkProcessClientVersion, "reads the version"},
		{checkProcessClientUnreadableVersion, "a version that cannot be read gives CO-OBS-001"},
	})
}

func checkSessionBody(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})

	first, err := fake.client.Call(t.Context(), "index_status", map[string]any{"project": "p&<q>"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	var got echoed
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("body %s: %v", first, err)
	}

	attrRequireEqual(t, "body tool", got.Tool, "index_status")
	attrRequireEqual[any](t, "body args.project", got.Args["project"], "p&<q>")
	attrRequireEqual(t, "pid of the second call", fake.echo(t, "list_projects").PID, got.PID)
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 1)
}

func checkSessionWorkDir(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})
	cwd := fake.echo(t, "index_status").Cwd

	// The cache directory depends on the OS: XDG_CACHE_HOME on Linux, $HOME/Library/Caches on macOS.
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(filepath.Join(cache, "concordia", "cbm-session"))
	if err != nil {
		t.Fatalf("the neutral directory was not made: %v", err)
	}

	got, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}

	attrRequireEqual(t, "working directory", got, want)

	dir := t.TempDir()
	set := newFakeCBM(t, "server", observation.ProcessOptions{WorkDir: dir})

	got, err = filepath.EvalSymlinks(set.echo(t, "index_status").Cwd)
	if err != nil {
		t.Fatal(err)
	}

	want, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	attrRequireEqual(t, "configured working directory", got, want)
}

func checkSessionSkipsNoise(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})

	raw, err := fake.call(t.Context(), "query_graph", "noise")
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	attrRequireEqual(t, "valid JSON", json.Valid(raw), true)
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 1)
}

func checkSessionAnswersServerRequests(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{Timeout: 5 * time.Second})

	raw, err := fake.call(t.Context(), "query_graph", "server-request")
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	attrRequireEqual(t, "valid JSON", json.Valid(raw), true)
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 1)
}

func checkSessionLargeBody(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})

	raw, err := fake.call(t.Context(), "query_graph", "big")
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	attrRequireEqual(t, "body is at least 20 MiB", len(raw) >= fakeBigBytes, true)
	attrRequireEqual(t, "pid after the big call", fake.echo(t, "query_graph").PID > 0, true)
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 1)
}

func checkSessionConcurrentCalls(t *testing.T) {
	t.Helper()

	const callers = 8

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})
	pids := make([]int, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			raw, err := fake.client.Call(t.Context(), "index_status", map[string]any{"project": strconv.Itoa(i)})
			if err != nil {
				t.Errorf("call %d: %v", i, err)

				return
			}

			var got echoed
			if err := json.Unmarshal(raw, &got); err != nil || got.Args["project"] != strconv.Itoa(i) {
				t.Errorf("call %d: body %s (%v)", i, raw, err)

				return
			}

			pids[i] = got.PID
		}()
	}

	wg.Wait()

	for _, pid := range pids {
		attrRequireEqual(t, "pid of a call", pid, pids[0])
	}

	attrRequireEqual(t, "processes started", lines(t, fake.starts), 1)
}

func checkSessionMissingExecutable(t *testing.T) {
	t.Helper()

	c := observation.NewProcessClient(observation.ProcessOptions{Executable: "/nonexistent/cbm", Timeout: testTimeout, IndexTimeout: testTimeout})

	_, err := c.Call(t.Context(), "index_status", map[string]any{})
	wantDiag(t, err, "CO-OBS-003", diag.Toolchain)

	_, err = c.Version(t.Context())
	wantDiag(t, err, "CO-OBS-003", diag.Toolchain)
}

func checkSessionMissingProject(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})

	_, err := fake.call(t.Context(), "index_status", "error-body")

	de := diagOf(t, err, "CO-OBS-002", diag.AdapterFailure)
	requireMessageContains(t, de, "project not found or not indexed: p")
}

func checkSessionFailures(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})

	// The first four leave the session as it is; the last two break it.
	tests := []struct {
		behave      string
		wantMessage string
	}{
		{behave: "boom", wantMessage: "query_graph: boom"},
		{behave: "not-json", wantMessage: "not JSON: hello"},
		{behave: "is-error", wantMessage: "query_graph: unknown tool: x"},
		{behave: "rpc-error", wantMessage: "query_graph: invalid params"},
		{behave: "wrong-id", wantMessage: "response with id"},
		{behave: "garbage", wantMessage: "malformed response"},
	}

	for _, tt := range tests {
		_, err := fake.call(t.Context(), "query_graph", tt.behave)

		de := diagOf(t, err, "CO-OBS-001", diag.AdapterFailure)
		requireMessageContains(t, de, tt.wantMessage)
	}

	fake.echo(t, "query_graph")
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 3)
}

func checkSessionStderrTail(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})

	_, err := fake.call(t.Context(), "query_graph", "die")

	de := diagOf(t, err, "CO-OBS-001", diag.NoExit)
	requireMessageContains(t, de, "bad cypher here")
	attrRequireEqual(t, "message has the noise of stderr", strings.Contains(de.Diagnostic.Message, "level=info"), false)
}

func checkSessionRetries(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{Retries: 2})

	for _, tool := range []string{"query_graph", "index_repository", "delete_project"} {
		if _, err := fake.call(t.Context(), tool, "die"); err == nil {
			t.Fatalf("%s: no error", tool)
		}
	}

	// 3 attempts for query_graph, 1 for each write tool: each one started a process.
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 5)
}

func checkSessionTimeout(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{Timeout: time.Second})

	_, err := fake.call(t.Context(), "query_graph", "hang-first")

	de := diagOf(t, err, "CO-OBS-001", diag.AdapterFailure)
	requireMessageContains(t, de, "query_graph timed out after")

	fake.echo(t, "query_graph")
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 2)
}

func checkSessionStoppedContext(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fake.call(ctx, "query_graph", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	attrRequireEqual(t, "processes started for a stopped context", lines(t, fake.starts), 0)

	ctx, cancel = context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	_, err = fake.call(ctx, "query_graph", "hang")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}

	fake.echo(t, "query_graph")
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 2)
}

func checkSessionAdmission(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "admission", observation.ProcessOptions{Retries: 2})

	_, err := fake.call(t.Context(), "query_graph", "")

	de := diagOf(t, err, "CO-OBS-001", diag.AdapterFailure)
	requireMessageContains(t, de, "different build than the running CBM daemon")
	requireMessageContains(t, de, "point every CBM client at one binary")
	requireMessageContains(t, de, "Active daemon: pid 235954")
	attrRequireEqual(t, "message has the noise of stderr", strings.Contains(de.Diagnostic.Message, "level=info"), false)
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 1)
}

func checkSessionClose(t *testing.T) {
	t.Helper()

	fake := newFakeCBM(t, "server", observation.ProcessOptions{})
	if err := fake.client.Close(); err != nil {
		t.Fatalf("close without a process: %v", err)
	}

	first := fake.echo(t, "index_status")
	if err := fake.client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	attrRequireEqual(t, "the process saw its stdin close", lines(t, fake.stopped), 1)

	second := fake.echo(t, "index_status")
	attrRequireEqual(t, "a new process after Close", second.PID != first.PID, true)
	attrRequireEqual(t, "processes started", lines(t, fake.starts), 2)
}

func checkSessionOnCall(t *testing.T) {
	t.Helper()

	var tools []string

	fake := newFakeCBM(t, "server", observation.ProcessOptions{OnCall: func(tool string) { tools = append(tools, tool) }})

	fake.echo(t, "list_projects")
	fake.echo(t, "index_status")

	attrRequireEqual(t, "traced calls", strings.Join(tools, ","), "list_projects,index_status")
}

func checkProcessClientVersion(t *testing.T) {
	t.Helper()

	exe := script(t, `echo "codebase-memory-mcp 0.12.3"`)
	c := observation.NewProcessClient(observation.ProcessOptions{Executable: exe})

	v, err := c.Version(t.Context())
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	attrRequireEqual(t, "version", v, "0.12.3")
	attrRequireEqual(t, "executable", c.Executable(), exe)
}

func checkProcessClientUnreadableVersion(t *testing.T) {
	t.Helper()

	c := observation.NewProcessClient(observation.ProcessOptions{Executable: script(t, "echo nothing")})

	_, err := c.Version(t.Context())
	wantDiag(t, err, "CO-OBS-001", diag.AdapterFailure)
}
