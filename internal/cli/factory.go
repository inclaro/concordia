package cli

import (
	"context"
	"time"

	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	// defaultCbmExecutable is the CBM executable when no workspace sets one.
	defaultCbmExecutable = "codebase-memory-mcp"
	// fallbackTimeout is the timeout of CBM calls when no workspace loads.
	fallbackTimeout = 30 * time.Second
)

// DefaultClient gives the ProcessClient for the observation settings of the
// workspace. With a nil workspace (`doctor` without a loadable workspace) it
// uses the defaults: the default executable, a timeout of 30 s and no retries.
//
// A run keeps one client for each distinct setting, so a command that connects
// more than once (`watch`) uses one CBM process. The command handler closes
// the clients when it ends (see CloseClients).
func DefaultClient(wsp *workspace.Workspace, app *Context) *observation.ProcessClient {
	opts := observation.ProcessOptions{Executable: defaultCbmExecutable, Timeout: fallbackTimeout, IndexTimeout: fallbackTimeout}
	if wsp != nil {
		opts = observation.ProcessOptionsFrom(&wsp.Observation)
	}

	settings := clientSettings{executable: opts.Executable, timeout: opts.Timeout, indexTimeout: opts.IndexTimeout, retries: opts.Retries}

	for _, open := range app.clients {
		if open.settings == settings {
			return open.client
		}
	}

	opts.OnCall = func(tool string) {
		app.Logger.Trace(logger.TraceEvent{Phase: "observation", Event: "call", AdapterCommand: "tools/call " + tool})
	}

	client := observation.NewProcessClient(opts)
	app.clients = append(app.clients, openClient{settings: settings, client: client})

	return client
}

// clientSettings are the settings that tell two CBM clients apart.
type clientSettings struct {
	executable   string
	timeout      time.Duration
	indexTimeout time.Duration
	retries      int
}

// openClient is a CBM client that a run made, with its settings.
type openClient struct {
	client   *observation.ProcessClient
	settings clientSettings
}

// CloseClients ends the CBM process of each client that DefaultClient made
// in this run. The command handler runs it when it ends.
func (c *Context) CloseClients() {
	for _, open := range c.clients {
		_ = open.client.Close()
	}

	c.clients = nil
}

// NewClient makes the CBM client of this run: Deps.NewClient when a test sets
// it, else DefaultClient. wsp may be nil only for `doctor`.
func (c *Context) NewClient(wsp *workspace.Workspace) observation.Client { //nolint:ireturn // the Client port is the point: tests replace it.
	if c.Deps.NewClient != nil {
		return c.Deps.NewClient(wsp, c)
	}

	return DefaultClient(wsp, c)
}

// Connect makes the client and the provider of the workspace and checks the
// CBM version. It gives CO-OBS-003 (executable missing) or CO-OBS-004 (version
// not compatible) as an error. Version warnings go to the logger.
func (c *Context) Connect(ctx context.Context, wsp *workspace.Workspace) (*observation.Connection, error) {
	return observation.Connect(ctx, c.NewClient(wsp), c.Logger)
}
