// Package logger writes human text and trace lines to stderr. Machine output
// never goes through the logger.
package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/redact"
)

// Level is the amount of text that the logger writes.
type Level string

// Levels.
const (
	LevelQuiet   Level = "quiet"
	LevelNormal  Level = "normal"
	LevelVerbose Level = "verbose"
)

// TraceEvent is one trace line. A trace line never has source text or query file paths.
type TraceEvent struct {
	DurationMs     *int64           `json:"durationMs,omitempty"`
	Counts         map[string]int64 `json:"counts,omitempty"`
	Phase          string           `json:"phase"`
	Event          string           `json:"event"`
	AdapterCommand string           `json:"adapterCommand,omitempty"`
	QueryDigest    string           `json:"queryDigest,omitempty"`
}

// Millis gives a duration in whole milliseconds for TraceEvent.DurationMs.
func Millis(d time.Duration) *int64 {
	ms := d.Milliseconds()

	return &ms
}

// Options are the settings of New.
type Options struct {
	// Out is the text sink. nil gives os.Stderr.
	Out     io.Writer
	RunID   string
	Quiet   bool
	Verbose bool
	Trace   bool
}

// Logger writes to one sink. It is safe for concurrent use.
type Logger struct {
	out   io.Writer
	runID string
	level Level
	mu    sync.Mutex
	trace bool
}

// New makes a logger.
func New(opts Options) *Logger {
	result := &Logger{out: opts.Out, runID: opts.RunID, level: LevelNormal, trace: opts.Trace}

	switch {
	case opts.Quiet:
		result.level = LevelQuiet
	case opts.Verbose:
		result.level = LevelVerbose
	}

	if result.out == nil {
		result.out = os.Stderr
	}

	return result
}

// Level gives the level of the logger.
func (l *Logger) Level() Level { return l.level }

// TraceEnabled says whether Trace writes lines.
func (l *Logger) TraceEnabled() bool { return l.trace }

// RunID gives the run ID of the logger.
func (l *Logger) RunID() string { return l.runID }

// write redacts the text first: a log line must not hold a secret value (token shapes, private
// key blocks, URL passwords), whatever level or source it comes from.
func (l *Logger) write(text string) {
	text, _ = redact.Default().RedactValue("", text)

	l.mu.Lock()
	defer l.mu.Unlock()

	_, _ = io.WriteString(l.out, text)
}

// Error writes an error line.
func (l *Logger) Error(message string) {
	l.write("error: " + message + "\n")
}

// Warn writes a warning line, unless the level is quiet.
func (l *Logger) Warn(message string) {
	if l.level != LevelQuiet {
		l.write("warning: " + message + "\n")
	}
}

// Info writes a line, unless the level is quiet.
func (l *Logger) Info(message string) {
	if l.level != LevelQuiet {
		l.write(message + "\n")
	}
}

// Debug writes a line when the level is verbose.
func (l *Logger) Debug(message string) {
	if l.level == LevelVerbose {
		l.write("debug: " + message + "\n")
	}
}

// Diagnostic writes a diagnostic. In quiet mode only errors are written.
// Info diagnostics are written only in verbose mode.
//
//nolint:gocritic // Diagnostic is a value type that the code base passes by value.
func (l *Logger) Diagnostic(d diag.Diagnostic) {
	if l.level == LevelQuiet && d.Severity != diag.SeverityError {
		return
	}

	if d.Severity == diag.SeverityInfo && l.level != LevelVerbose {
		return
	}

	l.write(diag.FormatDiagnostic(d) + "\n")
}

// Diagnostics writes a list of diagnostics.
func (l *Logger) Diagnostics(list []diag.Diagnostic) {
	for _, d := range list {
		l.Diagnostic(d)
	}
}

type traceLine struct {
	TS    string `json:"ts"`
	RunID string `json:"runId"`
	TraceEvent
}

// Trace writes one NDJSON trace line when tracing is on.
//
//nolint:gocritic // TraceEvent is a small value type that callers build in the call.
func (l *Logger) Trace(ev TraceEvent) {
	if !l.trace {
		return
	}

	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(traceLine{TS: clock.ISO(time.Now()), RunID: l.runID, TraceEvent: ev}); err != nil {
		return
	}

	l.write(buf.String())
}
