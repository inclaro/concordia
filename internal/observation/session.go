package observation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/inclaro/concordia/internal/procexec"
)

const (
	// protocolVersion is the MCP version that the session asks for.
	protocolVersion = "2025-06-18"
	clientName      = "concordia"
	clientVersion   = "1"
	// scanStartBytes is the first size of the line buffer. It grows up to procexec.DefaultMaxBuffer.
	scanStartBytes = 64 * 1024
)

// initializeParams are the parameters of `initialize`.
type initializeParams struct {
	Capabilities    map[string]any `json:"capabilities"`
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      clientInfo     `json:"clientInfo"`
}

// clientInfo names this client to the server.
type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// rpcError is the `error` object of a JSON-RPC response.
type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("%s (JSON-RPC code %d)", e.Message, e.Code)
}

// toolResult is the `result` of a `tools/call` response. The `initialize`
// result decodes into it too, with no content.
type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// text gives the first text item of the result, which is the body that the
// `cli` form of the tool prints.
func (r *toolResult) text() string {
	for _, item := range r.Content {
		if item.Type == "text" {
			return item.Text
		}
	}

	return ""
}

// rpcMessage is one line from the server. A notification or a request of the
// server has a method; a response has an id and a result or an error.
type rpcMessage struct {
	Result *toolResult     `json:"result"`
	Error  *rpcError       `json:"error"`
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
}

// session is one running CBM server. It speaks newline-delimited JSON-RPC
// 2.0 (MCP) over stdin and stdout, one request at a time.
type session struct {
	stream *procexec.Stream
	lines  *bufio.Scanner
	nextID int64
	// broken is set when a call ended while its context stopped: the process is being killed.
	broken bool
}

// newSession wraps a started CBM process. It does not talk to it yet.
func newSession(stream *procexec.Stream) *session {
	lines := bufio.NewScanner(stream.Stdout)
	lines.Buffer(make([]byte, 0, scanStartBytes), procexec.DefaultMaxBuffer)

	return &session{stream: stream, lines: lines}
}

// usable reports whether the session can take another call.
func (s *session) usable() bool {
	return !s.broken && !s.stream.Exited()
}

// handshake runs `initialize` and sends `notifications/initialized`.
func (s *session) handshake(ctx context.Context) error {
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      clientInfo{Name: clientName, Version: clientVersion},
	}

	if _, err := s.request(ctx, "initialize", params); err != nil {
		return err
	}

	return s.send(map[string]any{"method": "notifications/initialized"})
}

// callTool runs `tools/call`. A *rpcError says that the server refused the
// call and the session is still good.
func (s *session) callTool(ctx context.Context, tool string, args map[string]any) (*toolResult, error) {
	if args == nil {
		args = map[string]any{}
	}

	return s.request(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
}

// request sends a request and reads its response. When ctx stops, the process
// is killed, which ends the write or the read in progress.
func (s *session) request(ctx context.Context, method string, params any) (*toolResult, error) {
	stopGuard := context.AfterFunc(ctx, s.stream.Kill)

	s.nextID++

	res, err := s.exchange(s.nextID, method, params)

	if !stopGuard() {
		s.broken = true
	}

	return res, err
}

func (s *session) exchange(id int64, method string, params any) (*toolResult, error) {
	err := s.send(map[string]any{keyID: id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}

	return s.await(id)
}

// keyID is the key of the id of a JSON-RPC message.
const keyID = "id"

// send writes one message as one line. It adds the JSON-RPC version.
func (s *session) send(message map[string]any) error {
	message["jsonrpc"] = "2.0"

	var line bytes.Buffer

	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(message); err != nil {
		return fmt.Errorf("encode the request: %w", err)
	}

	if _, err := s.stream.Stdin.Write(line.Bytes()); err != nil {
		return fmt.Errorf("write the request: %w", err)
	}

	return nil
}

// await reads lines until the response with the given id. Notifications of the
// server are skipped; a request of the server gets a "method not found" error.
func (s *session) await(id int64) (*toolResult, error) {
	want := strconv.FormatInt(id, 10)

	for s.lines.Scan() {
		line := bytes.TrimSpace(s.lines.Bytes())
		if len(line) == 0 {
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("malformed response: %w", err)
		}

		switch {
		case msg.Method != "":
			if err := s.refuseRequest(msg.ID); err != nil {
				return nil, err
			}

			continue
		case string(msg.ID) != want:
			return nil, fmt.Errorf("response with id %s, want %s", msg.ID, want)
		case msg.Error != nil:
			return nil, msg.Error
		case msg.Result == nil:
			return nil, errors.New("response without result")
		}

		return msg.Result, nil
	}

	return nil, s.readFailure()
}

// methodNotFound is the JSON-RPC error code for a method that the receiver does not have.
const methodNotFound = -32601

// refuseRequest answers a request of the server, such as a ping, with a "method not found" error, so the server
// does not wait for a response. A notification (no id) gets no answer.
func (s *session) refuseRequest(id json.RawMessage) error {
	if len(id) == 0 || string(id) == "null" {
		return nil
	}

	return s.send(map[string]any{
		keyID:   id,
		"error": map[string]any{"code": methodNotFound, "message": "method not found"},
	})
}

// readFailure says why the read of a line failed.
func (s *session) readFailure() error {
	err := s.lines.Err()

	switch {
	case err == nil:
		return io.EOF
	case errors.Is(err, bufio.ErrTooLong):
		return fmt.Errorf("%w (limit %d bytes)", procexec.ErrOutputTooLarge, procexec.DefaultMaxBuffer)
	default:
		return fmt.Errorf("read the response: %w", err)
	}
}
