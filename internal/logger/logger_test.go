package logger_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/logger"
)

const (
	testRunID  = "run-1"
	phaseCheck = "check"
)

func newLogger(opts logger.Options) (*logger.Logger, *bytes.Buffer) {
	var buf bytes.Buffer

	opts.Out = &buf
	opts.RunID = testRunID

	return logger.New(opts), &buf
}

func TestLevels(t *testing.T) {
	t.Parallel()

	run := func(opts logger.Options) string {
		l, buf := newLogger(opts)
		l.Error("e")
		l.Warn("w")
		l.Info("i")
		l.Debug("d")

		return buf.String()
	}

	if got := run(logger.Options{}); got != "error: e\nwarning: w\ni\n" {
		t.Errorf("normal: %q", got)
	}

	if got := run(logger.Options{Quiet: true}); got != "error: e\n" {
		t.Errorf("quiet: %q", got)
	}

	if got := run(logger.Options{Verbose: true}); got != "error: e\nwarning: w\ni\ndebug: d\n" {
		t.Errorf("verbose: %q", got)
	}

	if l, _ := newLogger(logger.Options{Quiet: true, Verbose: true}); l.Level() != logger.LevelQuiet {
		t.Errorf("quiet wins over verbose: %s", l.Level())
	}
}

func TestDiagnosticFiltering(t *testing.T) {
	t.Parallel()

	list := []diag.Diagnostic{
		diag.New(diag.WRK010, diag.Detail("e")),
		diag.New(diag.WRK005, diag.Params{"path": "p"}),
		diag.New(diag.CON022, nil),
	}

	count := func(opts logger.Options) int {
		l, buf := newLogger(opts)
		l.Diagnostics(list)

		return strings.Count(buf.String(), "CO-")
	}

	if got := count(logger.Options{Quiet: true}); got != 1 {
		t.Errorf("quiet: %d", got)
	}

	if got := count(logger.Options{}); got != 2 {
		t.Errorf("normal: %d", got)
	}

	if got := count(logger.Options{Verbose: true}); got != 3 {
		t.Errorf("verbose: %d", got)
	}
}

func decodeTraceLine(t *testing.T, line string) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatal(err)
	}

	return decoded
}

func TestTraceLines(t *testing.T) {
	t.Parallel()

	l, buf := newLogger(logger.Options{Trace: true, Quiet: true})
	l.Trace(logger.TraceEvent{Phase: phaseCheck, Event: "start<>", DurationMs: logger.Millis(1500 * time.Millisecond), Counts: map[string]int64{"n": 2}})
	l.Trace(logger.TraceEvent{Phase: phaseCheck, Event: "zero", DurationMs: logger.Millis(0)})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines: %q", buf.String())
	}

	first := decodeTraceLine(t, lines[0])

	want := map[string]any{"runId": testRunID, "phase": phaseCheck, "event": "start<>", "durationMs": float64(1500)}
	for key, value := range want {
		if first[key] != value {
			t.Errorf("line %v: %s = %v, want %v", first, key, first[key], value)
		}
	}

	if _, ok := first["ts"].(string); !ok {
		t.Errorf("no ts: %v", first)
	}

	if !strings.Contains(lines[0], "start<>") {
		t.Errorf("HTML characters were escaped: %s", lines[0])
	}

	if !strings.Contains(lines[1], `"durationMs":0`) {
		t.Errorf("a zero duration must stay: %s", lines[1])
	}
}

func TestTraceOffWritesNothing(t *testing.T) {
	t.Parallel()

	l, buf := newLogger(logger.Options{})
	l.Trace(logger.TraceEvent{Phase: "p", Event: "e"})

	if buf.Len() != 0 {
		t.Errorf("buf %q", buf.String())
	}

	if l.TraceEnabled() {
		t.Error("trace must be disabled")
	}

	if l.RunID() != testRunID {
		t.Errorf("run id %q", l.RunID())
	}
}
