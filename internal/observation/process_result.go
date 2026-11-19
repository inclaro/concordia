package observation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
)

const (
	tailMaxChars   = 500
	notJSONChars   = 200
	admissionChars = 300
)

var (
	missingProject = regexp.MustCompile(`(?i)project not found|not indexed`)
	// admissionFailure is the stderr text of a CBM that refuses to run next to a CBM daemon of another build.
	admissionFailure = regexp.MustCompile(`(?is)exact-build admission|\bbuild\b.*\bActive daemon:`)
	// errCallTimeout is the cause of a call context that ran out of time.
	errCallTimeout = errors.New("call timed out")
)

// callInfo says which call a failure belongs to.
type callInfo struct {
	tool    string
	timeout time.Duration
}

// finalError is a failure that is not retried.
type finalError struct {
	err error
}

func (f *finalError) Error() string {
	return f.err.Error()
}

func (f *finalError) Unwrap() error {
	return f.err
}

// failureOf describes why a session could not finish a call. stderr is the
// text of the CBM process. A stop by the caller gives the error of the
// context, not a diagnostic.
func failureOf(callCtx context.Context, info callInfo, executable string, cause error, stderr string) error {
	if err := callCtx.Err(); err != nil && !errors.Is(context.Cause(callCtx), errCallTimeout) {
		return fmt.Errorf("%s: %w", info.tool, err)
	}

	if admissionFailure.MatchString(stderr) {
		text := fmt.Sprintf("%s: the CBM executable '%s' is a different build than the running CBM daemon; point every CBM client at one binary (%s)",
			info.tool, executable, stderrTail(stderr, admissionChars))

		return &finalError{err: diag.Fail(diag.OBS001, diag.Detail(text))}
	}

	tail := stderrTail(stderr, tailMaxChars)
	if tail != "" {
		tail = ": " + tail
	}

	switch {
	case errors.Is(context.Cause(callCtx), errCallTimeout):
		return diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s timed out after %ds%s", info.tool, int(math.Round(info.timeout.Seconds())), tail)))
	case errors.Is(cause, procexec.ErrOutputTooLarge):
		return diag.Fail(diag.OBS001, diag.Detail(cause.Error()))
	case errors.Is(cause, io.EOF):
		return diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s: CBM ended the session%s", info.tool, tail)))
	default:
		return diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s: %v%s", info.tool, cause, tail)))
	}
}

// resultBody reads the JSON body of a tool result, or the failure it reports.
// The body is the text that the `cli` form of the tool prints.
func resultBody(tool string, args map[string]any, res *toolResult) (json.RawMessage, error) {
	text := res.text()
	body := parseJSON(text)

	if message, ok := stringField(body, "error"); ok {
		return nil, errorBodyFailure(tool, args, message)
	}

	if res.IsError {
		return nil, diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s: %s", tool, stderrTail(text, tailMaxChars))))
	}

	if body == nil {
		return nil, diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s gave output that is not JSON: %s", tool, stderrTail(text, notJSONChars))))
	}

	return body, nil
}

func errorBodyFailure(tool string, args map[string]any, text string) error {
	if missingProject.MatchString(text) {
		project, _ := args["project"].(string)
		if project != "" {
			text = text + ": " + project
		}

		return diag.Fail(diag.OBS002, diag.Detail(text))
	}

	return diag.Fail(diag.OBS001, diag.Detail(tool+": "+text))
}

// stderrTail gives the end of the text of a CBM process, without the lines
// that are noise for error messages.
func stderrTail(text string, maxChars int) string {
	var lines []string

	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if isStderrNoise(l) {
			continue
		}

		lines = append(lines, l)
	}

	joined := strings.Join(lines, " | ")
	if runes := []rune(joined); len(runes) > maxChars {
		return "…" + string(runes[len(runes)-maxChars:])
	}

	return joined
}

// isStderrNoise reports whether a trimmed line says nothing for an error message.
func isStderrNoise(line string) bool {
	if line == "" {
		return true
	}

	return strings.HasPrefix(line, "level=info") || strings.HasPrefix(line, "warning: passing raw JSON")
}

// parseJSON gives the text as JSON, or nil when it is empty or not JSON.
func parseJSON(text string) json.RawMessage {
	body := bytes.TrimSpace([]byte(text))
	if len(body) == 0 || !json.Valid(body) {
		return nil
	}

	return body
}

// stringField gives a string field of a JSON object body.
func stringField(body json.RawMessage, key string) (string, bool) {
	if body == nil {
		return "", false
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return "", false
	}

	raw := obj[key]
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}

	return s, true
}
