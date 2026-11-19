package observation

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
)

const repoPathKey = "repo_path"

// CallKey gives the key of a recorded response:
// `sha256(tool + "\x00" + canonicalJson(args))` as hex. For `index_repository`
// the `repo_path` argument is not part of the key, so that a recording can be
// replayed in any directory.
func CallKey(tool string, args map[string]any) string {
	if tool == indexTool {
		if _, ok := args[repoPathKey]; ok {
			args = maps.Clone(args)
			delete(args, repoPathKey)
		}
	}

	if args == nil {
		args = map[string]any{}
	}

	return digest.SHA256HexString(tool + "\x00" + canonjson.MustString(args))
}

// Recording is the content of a recording file.
type Recording struct {
	// Calls are the recorded responses by call key.
	Calls map[string]json.RawMessage `json:"calls"`
	// Version is the CBM version of the recording.
	Version string `json:"version"`
	// HeadSHA is the git head that the recorded index was made at.
	HeadSHA string `json:"headSha,omitempty"`
}

// RecordingSink receives the responses that a RecordingClient records.
type RecordingSink interface {
	Record(key string, response json.RawMessage)
	SetVersion(version string)
}

// MemorySink is a RecordingSink in memory. ToRecording gives the file content.
type MemorySink struct {
	calls   map[string]json.RawMessage
	version string
	headSHA string
	mu      sync.Mutex
}

// NewMemorySink makes a sink. headSHA is the git head of the recorded index (may be empty).
func NewMemorySink(headSHA string) *MemorySink {
	return &MemorySink{calls: map[string]json.RawMessage{}, headSHA: headSHA}
}

// Record keeps one response.
func (s *MemorySink) Record(key string, response json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls[key] = response
}

// SetVersion keeps the CBM version.
func (s *MemorySink) SetVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.version = version
}

// SetHeadSHA keeps the git head of the recorded index.
func (s *MemorySink) SetHeadSHA(headSHA string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.headSHA = headSHA
}

// ToRecording gives the content of the recording file.
func (s *MemorySink) ToRecording() Recording {
	s.mu.Lock()
	defer s.mu.Unlock()

	return Recording{Version: s.version, HeadSHA: s.headSHA, Calls: maps.Clone(s.calls)}
}

// RecordingClient passes calls to an inner client and records each response.
type RecordingClient struct {
	inner Client
	sink  RecordingSink
}

// NewRecordingClient makes a RecordingClient.
func NewRecordingClient(inner Client, sink RecordingSink) *RecordingClient {
	return &RecordingClient{inner: inner, sink: sink}
}

// Version gives the version of the inner client and records it.
func (r *RecordingClient) Version(ctx context.Context) (string, error) {
	v, err := r.inner.Version(ctx)
	if err != nil {
		return "", err
	}

	r.sink.SetVersion(v)

	return v, nil
}

// Call passes the call to the inner client and records the response.
func (r *RecordingClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	res, err := r.inner.Call(ctx, tool, args)
	if err != nil {
		return nil, err
	}

	r.sink.Record(CallKey(tool, args), res)

	return res, nil
}

// ReplayClient replays a recording. A call that is not in the recording gives
// CO-OBS-001 ("replay miss <tool>").
type ReplayClient struct {
	rec Recording
}

// NewReplayClient makes a ReplayClient from a recording.
func NewReplayClient(rec Recording) *ReplayClient {
	return &ReplayClient{rec: rec}
}

// LoadReplayClient makes a ReplayClient from a recording file.
func LoadReplayClient(path string) (*ReplayClient, error) {
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return nil, fmt.Errorf("read recording: %w", err)
	}

	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse recording %s: %w", path, err)
	}

	return NewReplayClient(rec), nil
}

// HeadSHA gives the git head that the recorded index was made at ("" when not recorded).
func (r *ReplayClient) HeadSHA() string {
	return r.rec.HeadSHA
}

// Version gives the recorded version.
func (r *ReplayClient) Version(context.Context) (string, error) {
	return r.rec.Version, nil
}

// Call gives the recorded response of a call.
func (r *ReplayClient) Call(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	res, ok := r.rec.Calls[CallKey(tool, args)]
	if !ok {
		return nil, diag.Fail(diag.OBS001, diag.Detail("replay miss "+tool))
	}

	return res, nil
}
