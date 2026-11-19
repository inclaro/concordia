package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// DefaultFakeVersion is the version that a FakeClient gives when no version is set.
const DefaultFakeVersion = "0.12.1"

// FakeHandler gives the response of each call of a FakeClient. The response
// is encoded as JSON. The handler can give an error (for example a *diag.Error).
type FakeHandler func(tool string, args map[string]any) (any, error)

// FakeCall is one call that a FakeClient received.
type FakeCall struct {
	Args map[string]any
	Tool string
}

// FakeClient is a test double for Client.
type FakeClient struct {
	handler FakeHandler
	version string
	calls   []FakeCall
	mu      sync.Mutex
}

// NewFakeClient makes a FakeClient. An empty version gives DefaultFakeVersion.
func NewFakeClient(handler FakeHandler, version string) *FakeClient {
	if version == "" {
		version = DefaultFakeVersion
	}

	return &FakeClient{handler: handler, version: version}
}

// Version gives the version of the fake.
func (f *FakeClient) Version(context.Context) (string, error) {
	return f.version, nil
}

// Call keeps the call and gives the response of the handler.
func (f *FakeClient) Call(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{Tool: tool, Args: args})
	f.mu.Unlock()

	res, err := f.handler(tool, args)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encode the fake response of %s: %w", tool, err)
	}

	return data, nil
}

// Calls gives the calls that the fake received, in order.
func (f *FakeClient) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]FakeCall(nil), f.calls...)
}
