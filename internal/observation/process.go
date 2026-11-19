package observation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	versionTimeout = 15 * time.Second
	indexTool      = "index_repository"
	// stopGrace is the time that a CBM process gets to end after its stdin closes, before it is killed.
	stopGrace = 2 * time.Second
)

// readOnlyTools are the tools that are retried.
var readOnlyTools = map[string]struct{}{
	"index_status":     {},
	"get_graph_schema": {},
	"query_graph":      {},
	"list_projects":    {},
}

// ProcessOptions are the settings of a ProcessClient.
type ProcessOptions struct {
	// OnCall is called for each started tool call (for `--trace`). It gets no
	// arguments, to keep source paths out of traces.
	OnCall func(tool string)
	// Executable is the executable name or path.
	Executable string
	// WorkDir is the working directory of the CBM process. CBM indexes the
	// working directory of a session on start, so it must not be a
	// repository. Empty gives a fixed directory in the user cache (see
	// neutralDir).
	WorkDir string
	// Timeout is the timeout of each tool call. It includes the start of the
	// CBM process when the call needs one.
	Timeout time.Duration
	// IndexTimeout is the timeout of `index_repository`.
	IndexTimeout time.Duration
	// Retries is the number of retries of read-only tools (0..3).
	Retries int
}

// ProcessOptionsFrom makes the settings of a ProcessClient from the
// observation settings of a workspace.
func ProcessOptionsFrom(o *workspace.Observation) ProcessOptions {
	return ProcessOptions{
		Executable:   o.Executable,
		Timeout:      time.Duration(o.TimeoutSeconds * float64(time.Second)),
		IndexTimeout: time.Duration(o.IndexTimeoutSeconds * float64(time.Second)),
		Retries:      o.Retries,
	}
}

// ProcessClient runs the tools of one CBM process: `<executable>` with no
// arguments is an MCP server on stdio. The process starts on the first call
// and serves every call of the client, one at a time. A call that times out
// or breaks the session kills the process; the next call starts a new one.
// Close ends the process.
type ProcessClient struct {
	sess *session
	opts ProcessOptions
	mu   sync.Mutex
}

// NewProcessClient makes a ProcessClient. It starts no process.
func NewProcessClient(opts ProcessOptions) *ProcessClient {
	return &ProcessClient{opts: opts}
}

// Executable gives the executable name or path as it is configured.
func (c *ProcessClient) Executable() string {
	return c.opts.Executable
}

// Version runs `<executable> --version` and reads the version.
func (c *ProcessClient) Version(ctx context.Context) (string, error) {
	res, err := procexec.Run(ctx, c.opts.Executable, []string{"--version"}, procexec.Options{Timeout: versionTimeout})
	if err != nil {
		return "", c.spawnFailure(err)
	}

	if v, ok := ParseVersion(res.Stdout + "\n" + res.Stderr); ok {
		return v, nil
	}

	return "", diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("cannot read the version from '%s --version'", c.opts.Executable)))
}

// Call runs one tool. Read-only tools are retried after a CO-OBS-001 failure,
// with a new process when the failure ended the session.
func (c *ProcessClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	maxRetries := 0
	if _, ok := readOnlyTools[tool]; ok {
		maxRetries = max(0, c.opts.Retries)
	}

	for attempt := 0; ; attempt++ {
		body, err := c.callOnce(ctx, tool, args)
		if err == nil {
			return body, nil
		}

		var final *finalError
		if errors.As(err, &final) {
			return nil, final.err
		}

		if !isRetryableFailure(err) || attempt >= maxRetries {
			return nil, err
		}
	}
}

// Close ends the CBM process, if one runs. The client starts a new process if
// it is called again.
func (c *ProcessClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopSession()

	return nil
}

// isRetryableFailure reports whether err is a CO-OBS-001 failure.
func isRetryableFailure(err error) bool {
	var de *diag.Error

	return errors.As(err, &de) && de.Diagnostic.Code == diag.OBS001
}

func (c *ProcessClient) spawnFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var se *procexec.SpawnError
	if errors.As(err, &se) {
		if se.NotFound || se.Permission {
			return diag.Fail(diag.OBS003, diag.Params{"executable": c.opts.Executable})
		}

		return diag.Fail(diag.OBS001, diag.Detail(se.Error()))
	}

	if errors.Is(err, procexec.ErrOutputTooLarge) {
		return diag.Fail(diag.OBS001, diag.Detail(err.Error()))
	}

	return err
}

func (c *ProcessClient) callOnce(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if c.opts.OnCall != nil {
		c.opts.OnCall(tool)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", tool, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	info := callInfo{tool: tool, timeout: c.opts.Timeout}
	if tool == indexTool {
		info.timeout = c.opts.IndexTimeout
	}

	callCtx, cancel := withTimeout(ctx, info.timeout)
	defer cancel()

	sess, err := c.ensureSession(callCtx, info)
	if err != nil {
		return nil, err
	}

	res, err := sess.callTool(callCtx, tool, args)

	var refusal *rpcError
	if errors.As(err, &refusal) {
		return nil, diag.Fail(diag.OBS001, diag.Detail(tool+": "+refusal.Error()))
	}

	if err != nil {
		return nil, c.failSession(callCtx, info, err)
	}

	return resultBody(tool, args, res)
}

// withTimeout limits ctx to timeout. A timeout of 0 or less means no limit.
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeoutCause(ctx, timeout, errCallTimeout)
}

// ensureSession gives the running session, or starts one and runs its handshake.
func (c *ProcessClient) ensureSession(callCtx context.Context, info callInfo) (*session, error) {
	if c.sess != nil && c.sess.usable() {
		return c.sess, nil
	}

	c.stopSession()

	stream, err := c.startStream(callCtx)
	if err != nil {
		return nil, err
	}

	c.sess = newSession(stream)

	if err := c.sess.handshake(callCtx); err != nil {
		return nil, c.failSession(callCtx, info, err)
	}

	return c.sess, nil
}

// failSession ends the session after a failure that left it unusable and
// gives the failure of the call.
func (c *ProcessClient) failSession(callCtx context.Context, info callInfo, cause error) error {
	stderr := c.stopSession()

	return failureOf(callCtx, info, c.opts.Executable, cause, stderr)
}

// stopSession ends the process of the session and gives its stderr text.
func (c *ProcessClient) stopSession() string {
	if c.sess == nil {
		return ""
	}

	c.sess.stream.Stop(stopGrace)

	text := c.sess.stream.Stderr()
	c.sess = nil

	return text
}
