// Package diag holds the diagnostic catalog, diagnostics and the error type
// that carries a diagnostic with an exit code.
package diag

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/redact"
)

// Process exit codes.
const (
	OK             = 0
	Violation      = 1
	InvalidInput   = 2
	AdapterFailure = 3
	Unknown        = 4
	Toolchain      = 5
	Network        = 6
	Internal       = 7
	Interrupted    = 130
)

// Severity is the severity of a diagnostic.
type Severity string

// Severity values.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Source says where a diagnostic comes from.
type Source struct {
	URI     string `json:"uri"`
	Pointer string `json:"pointer,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// Diagnostic is one finding about the input or the run.
type Diagnostic struct {
	Source   *Source  `json:"source,omitempty"`
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	// Rule is the CALM CLI rule ID of the finding, or "" for a Concordia-only diagnostic.
	Rule       string       `json:"rule,omitempty"`
	Message    string       `json:"message"`
	Suggestion string       `json:"suggestion,omitempty"`
	Cause      []Diagnostic `json:"cause,omitempty"`
}

// Error is a diagnostic with the exit code of the process. It satisfies error.
type Error struct {
	Diagnostic Diagnostic
	ExitCode   int
}

// Error gives "<code>: <message>".
func (e *Error) Error() string {
	return e.Diagnostic.Code + ": " + e.Diagnostic.Message
}

// Params are the values for the "{name}" placeholders of a message template.
// A missing value leaves its placeholder in the text.
type Params map[string]any

// Detail gives the parameters of a message with one "{detail}" placeholder.
func Detail(text string) Params {
	return Params{"detail": text}
}

// options holds the settings of the Option values.
type options struct {
	severity   Severity
	suggestion string
	rule       string
	source     *Source
	cause      []Diagnostic
	exitCode   int
}

// Option changes the result of New or Fail.
type Option func(*options)

// WithSeverity overrides the catalog severity (for codes with dual use, for example CO-EXC-006).
func WithSeverity(s Severity) Option {
	return func(o *options) { o.severity = s }
}

// WithSuggestion overrides the catalog suggestion.
func WithSuggestion(s string) Option {
	return func(o *options) { o.suggestion = s }
}

// WithSource sets the source of the diagnostic.
func WithSource(s Source) Option {
	return func(o *options) { o.source = &s }
}

// WithCause sets the cause list. An empty list sets nothing.
func WithCause(c []Diagnostic) Option {
	return func(o *options) { o.cause = c }
}

// WithExitCode overrides the catalog exit code (Fail only; for codes with dual use, for example CO-WRK-005).
func WithExitCode(code int) Option {
	return func(o *options) { o.exitCode = code }
}

// WithRule sets the CALM CLI rule ID of a diagnostic.
func WithRule(id string) Option { return func(o *options) { o.rule = id } }

var (
	catalogOnce sync.Once
	catalog     map[string]Entry

	placeholder = regexp.MustCompile(`\{(\w+)\}`)
)

// Lookup gives the catalog entry of a code.
func Lookup(code string) (Entry, bool) {
	catalogOnce.Do(func() {
		catalog = make(map[string]Entry, len(entries))

		for _, e := range entries {
			catalog[e.Code] = e
		}
	})

	entry, ok := catalog[code]

	return entry, ok
}

// All gives a copy of the catalog in catalog order.
func All() []Entry {
	return slices.Clone(entries)
}

// ExitCodeOf gives the exit code of a catalog code. The bool is false when the
// code never stops a run by itself (or is not in the catalog).
func ExitCodeOf(code string) (int, bool) {
	e, ok := Lookup(code)
	if !ok || e.ExitCode == NoExit {
		return 0, false
	}

	return e.ExitCode, true
}

func mustLookup(code string) Entry {
	e, ok := Lookup(code)
	if !ok {
		panic("diag: unknown diagnostic code " + code)
	}

	return e
}

func fill(template string, params Params) string {
	return placeholder.ReplaceAllStringFunc(template, func(m string) string {
		v, ok := params[m[1:len(m)-1]]
		if !ok || v == nil {
			return m
		}

		return format(v)
	})
}

// format writes a value as JavaScript String() does for the types in use.
func format(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	default:
		return fmt.Sprint(v)
	}
}

func apply(opts []Option) options {
	var settings options
	for _, opt := range opts {
		opt(&settings)
	}

	return settings
}

// scrub removes secret values (token shapes, private keys, URL passwords) from the text of a
// diagnostic: a message can quote source text or a provider error. Redaction records are not kept here.
func scrub(text string) string {
	out, _ := redact.Default().RedactValue("", text)

	return out
}

// New makes a diagnostic from the catalog. It panics when the code is not in the catalog.
func New(code string, params Params, opts ...Option) Diagnostic {
	entry := mustLookup(code)
	settings := apply(opts)
	result := Diagnostic{
		Code:     code,
		Severity: entry.Severity,
		Message:  scrub(fill(entry.Message, params)),
		Source:   settings.source,
		Rule:     settings.rule,
	}

	if settings.severity != "" {
		result.Severity = settings.severity
	}

	suggestion := entry.Suggestion
	if settings.suggestion != "" {
		suggestion = settings.suggestion
	}

	if suggestion != "" {
		result.Suggestion = scrub(fill(suggestion, params))
	}

	if len(settings.cause) > 0 {
		result.Cause = settings.cause
	}

	return result
}

// Fail makes an Error from the catalog. The exit code comes from the catalog
// unless WithExitCode is set. A code with no exit code gives Internal.
func Fail(code string, params Params, opts ...Option) *Error {
	entry := mustLookup(code)
	settings := apply(opts)
	exit := entry.ExitCode

	switch {
	case settings.exitCode != 0:
		exit = settings.exitCode
	case exit == NoExit:
		exit = Internal
	}

	return &Error{Diagnostic: New(code, params, opts...), ExitCode: exit}
}

// Sort gives a copy of the list in a fixed order for stable output: by code,
// then source URI, pointer, line and message.
func Sort(list []Diagnostic) []Diagnostic {
	out := slices.Clone(list)
	slices.SortStableFunc(out, compare)

	return out
}

func sourceOf(d *Diagnostic) Source {
	if d.Source == nil {
		return Source{}
	}

	return *d.Source
}

//nolint:gocritic // slices.SortStableFunc needs this signature, and Diagnostic is a value type.
func compare(left, right Diagnostic) int {
	leftSrc, rightSrc := sourceOf(&left), sourceOf(&right)

	if c := canonjson.CompareUTF16(left.Code, right.Code); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(leftSrc.URI, rightSrc.URI); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(leftSrc.Pointer, rightSrc.Pointer); c != 0 {
		return c
	}

	if leftSrc.Line != rightSrc.Line {
		return leftSrc.Line - rightSrc.Line
	}

	return canonjson.CompareUTF16(left.Message, right.Message)
}

// FormatDiagnostic gives the text for a diagnostic. The cause list is indented below it.
//
//nolint:gocritic // Diagnostic is a value type that the code base passes by value.
func FormatDiagnostic(d Diagnostic) string {
	return formatIndented(&d, "")
}

func formatIndented(diagnostic *Diagnostic, indent string) string {
	var out strings.Builder

	out.WriteString(indent)
	out.WriteString(string(diagnostic.Severity))
	out.WriteByte(' ')
	out.WriteString(diagnostic.Code)
	out.WriteString(": ")
	out.WriteString(diagnostic.Message)

	if src := diagnostic.Source; src != nil {
		out.WriteString(" [")
		out.WriteString(src.URI)

		if src.Line != 0 {
			out.WriteByte(':')
			out.WriteString(strconv.Itoa(src.Line))
		}

		if src.Pointer != "" {
			out.WriteByte('#')
			out.WriteString(src.Pointer)
		}

		out.WriteByte(']')
	}

	if diagnostic.Suggestion != "" {
		out.WriteString("\n" + indent + "  hint: " + diagnostic.Suggestion)
	}

	for i := range diagnostic.Cause {
		out.WriteByte('\n')
		out.WriteString(formatIndented(&diagnostic.Cause[i], indent+"  "))
	}

	return out.String()
}
