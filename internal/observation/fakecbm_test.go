package observation_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The fake CBM is the test binary itself. A wrapper script starts it with these variables, so that it
// answers like `codebase-memory-mcp` with no arguments: newline-delimited JSON-RPC on stdio.
const (
	fakeModeEnv    = "OBS_FAKE_CBM"
	fakeStartsEnv  = "OBS_FAKE_STARTS"
	fakeStoppedEnv = "OBS_FAKE_STOPPED"
	fakeBigBytes   = 20 << 20
	// fakeAdmissionText is the stderr of a CBM that is another build than the running daemon.
	fakeAdmissionText = "level=info msg=start\n" +
		"CBM exact-build admission failed (build 6761d038, daemon build 77745491). Close all CBM sessions and commands, then retry. " +
		"Active daemon: pid 235954, version 0.12.1; point every client at one binary (compare `which -a codebase-memory-mcp`)\n"
)

// fakeRequest is a message of the client.
type fakeRequest struct {
	ID     *int64 `json:"id"`
	Method string `json:"method"`
	Params struct {
		Arguments map[string]any `json:"arguments"`
		Name      string         `json:"name"`
	} `json:"params"`
}

// TestFakeCBM is not a test: the wrapper script of fakeExecutable starts the test binary on it.
func TestFakeCBM(t *testing.T) {
	mode := os.Getenv(fakeModeEnv)
	if mode == "" {
		t.Skip("fake CBM process only")
	}

	os.Exit(serveFake(mode))
}

// serveFake answers the requests on stdin until it closes, and gives the exit code.
func serveFake(mode string) int {
	starts := appendLine(os.Getenv(fakeStartsEnv), "start")

	if mode == "admission" {
		_, _ = fmt.Fprint(os.Stderr, fakeAdmissionText)

		return 1
	}

	in := bufio.NewReader(os.Stdin)
	initialized := false

	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			appendLine(os.Getenv(fakeStoppedEnv), "stopped")

			return 0
		}

		var req fakeRequest
		if json.Unmarshal(line, &req) != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			reply(*req.ID, map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "fake"}})
		case "notifications/initialized":
			initialized = true
		case "tools/call":
			if !initialized {
				replyError(*req.ID, "not initialized")

				continue
			}

			serveTool(*req.ID, &req, starts, in)
		default:
			if req.ID != nil {
				replyError(*req.ID, "method not found")
			}
		}
	}
}

// serveTool answers a tool call. The argument `behave` picks what the fake does.
func serveTool(id int64, req *fakeRequest, starts int, in *bufio.Reader) {
	behave, _ := req.Params.Arguments["behave"].(string)

	switch behave {
	case "error-body":
		replyText(id, `{"error":"project not found or not indexed","hint":"x"}`, false)
	case "boom":
		replyText(id, `{"error":"boom"}`, false)
	case "not-json":
		replyText(id, "hello", false)
	case "is-error":
		replyText(id, "unknown tool: x", true)
	case "rpc-error":
		replyError(id, "invalid params")
	case "noise":
		writeLine(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`)
		writeLine("")
		replyEcho(id, req)
	case "server-request":
		pingClient(in)
		replyEcho(id, req)
	case "wrong-id":
		replyEcho(id+100, req)
	case "garbage":
		writeLine("this is not json")
	case "hang":
		time.Sleep(time.Minute)
	case "hang-first":
		if starts == 1 {
			time.Sleep(time.Minute)
		}

		replyEcho(id, req)
	case "die":
		_, _ = fmt.Fprint(os.Stderr, "level=info noise\nbad cypher here\n")

		os.Exit(1)
	case "big":
		replyText(id, mustJSON(map[string]string{"data": strings.Repeat("x", fakeBigBytes)}), false)
	default:
		replyEcho(id, req)
	}
}

// pingClient sends a request of the server, as CBM can do during a call, and waits for the client's answer. It
// answers the call only after a "method not found" error for that request; otherwise the call times out.
func pingClient(in *bufio.Reader) {
	writeLine(`{"jsonrpc":"2.0","id":"server-1","method":"ping"}`)

	line, err := in.ReadBytes('\n')
	if err != nil {
		os.Exit(1)
	}

	var answer struct {
		ID    string `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}

	if json.Unmarshal(line, &answer) != nil || answer.ID != "server-1" || answer.Error.Code != -32601 {
		time.Sleep(time.Minute)
	}
}

// replyEcho answers with the process ID, the working directory, the tool, and the arguments.
func replyEcho(id int64, req *fakeRequest) {
	cwd, _ := os.Getwd()
	replyText(id, mustJSON(map[string]any{"pid": os.Getpid(), "cwd": cwd, "tool": req.Params.Name, "args": req.Params.Arguments}), false)
}

// replyText answers a tool call with one text item, as CBM does.
func replyText(id int64, text string, isError bool) {
	result := map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}
	if isError {
		result["isError"] = true
	}

	reply(id, result)
}

func reply(id int64, result any) {
	writeLine(mustJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}))
}

func replyError(id int64, message string) {
	writeLine(mustJSON(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": message}}))
}

// mustJSON encodes a value of the fake. The fake only encodes values that it built, so a failure is a bug of the test.
func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func writeLine(line string) {
	_, _ = fmt.Fprintln(os.Stdout, line)
}

// appendLine adds a line to a file, and gives the number of lines in it. An empty path does nothing.
func appendLine(path, line string) int {
	if path == "" {
		return 0
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0
	}

	_, _ = fmt.Fprintln(f, line)
	_ = f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	return strings.Count(string(data), "\n")
}
