package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

const (
	cmdValidate = "validate"

	modelFormatPretty = "pretty"
	modelFormatJSON   = "json"
	modelFormatJUnit  = "junit"
)

type modelValidateOptions struct {
	architecture string
	timeline     string
	patterns     []string
	decorators   []string
	strict       bool
	patternOnly  bool
}

// Validation modes of `model validate` (the modes of `calm validate`).
const (
	modeArchitecture            = "architecture"
	modeArchitectureWithPattern = "architecture-with-pattern"
	modePattern                 = "pattern"
	modeTimeline                = "timeline"
)

func newModelCmd(host *Host) *cobra.Command {
	model := &cobra.Command{Use: "model", Short: "Architecture model commands"}

	var opts modelValidateOptions

	validate := &cobra.Command{
		Use:   cmdValidate,
		Short: "Validate a CALM 1.2 or 1.3 architecture (with patterns, decorators, and Concordia extensions), patterns, or a timeline",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return modelValidate(ctx, app, &opts)
		}),
	}
	validate.Flags().StringVar(&opts.architecture, "architecture", "", "CALM architecture file")
	validate.Flags().StringArrayVar(&opts.patterns, "pattern", nil, "CALM pattern (JSON Schema); repeatable")
	validate.Flags().StringArrayVar(&opts.decorators, "decorator", nil, "CALM decorator document; repeatable")
	validate.Flags().BoolVar(&opts.patternOnly, "pattern-only", false, "validate the patterns as CALM patterns, without an architecture")
	validate.Flags().StringVar(&opts.timeline, "timeline", "", "CALM timeline document")
	validate.Flags().BoolVar(&opts.strict, "strict", false, "warnings also give exit code 2")
	model.AddCommand(validate)

	return model
}

type modelValidateJSON struct {
	SchemaVer    string             `json:"schemaVersion"`
	Mode         string             `json:"mode"`
	Architecture *modelValidateArch `json:"architecture"`
	Document     *modelValidateArch `json:"document"`
	Diagnostics  []diag.Diagnostic  `json:"diagnostics"`
}

type modelValidateArch struct {
	Digest *string `json:"digest"`
	Path   string  `json:"path"`
}

// modelValidateOutcome is what `model validate` reports.
type modelValidateOutcome struct {
	mode string
	// subject is the architecture (architecture modes), or the timeline or first pattern.
	subject     modelValidateArch
	diagnostics []diag.Diagnostic
}

// namesOtherInput reports whether the options also name an architecture or patterns.
func namesOtherInput(opts *modelValidateOptions) bool {
	return opts.architecture != "" || len(opts.patterns) > 0 || opts.patternOnly
}

// modelValidateConflict says which options of `model validate` cannot be used together, or "" when none.
func modelValidateConflict(opts *modelValidateOptions) string {
	if opts.timeline != "" && namesOtherInput(opts) {
		return "--timeline cannot be used with --architecture, --pattern, or --pattern-only"
	}

	if opts.patternOnly && opts.architecture != "" {
		return "--pattern-only and --architecture cannot be used together"
	}

	return ""
}

// checkModelValidateArgs gives the output format and checks the options of `model validate`.
// It fails with an invalid-input error.
func checkModelValidateArgs(app *Context, opts *modelValidateOptions) (string, error) {
	format := app.Opts.Format
	if format == "" {
		format = modelFormatPretty
	}

	if !slices.Contains([]string{modelFormatPretty, modelFormatJSON, modelFormatJUnit}, format) {
		return "", diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported format '%s' (pretty, json, junit)", format)))
	}

	if conflict := modelValidateConflict(opts); conflict != "" {
		return "", diag.Fail(diag.WRK010, diag.Detail(conflict))
	}

	return format, nil
}

func modelValidate(ctx context.Context, app *Context, opts *modelValidateOptions) (int, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return diag.Internal, err
	}

	format, err := checkModelValidateArgs(app, opts)
	if err != nil {
		return diag.InvalidInput, err
	}

	run, err := newModelValidateRun(ctx, app, wsp, opts)
	if err != nil {
		return diag.Internal, err
	}

	outcome, err := run.outcome(ctx)
	if err != nil {
		return modelValidateFailureCode(err), err
	}

	if err := writeModelValidate(app, format, &outcome); err != nil {
		return diag.Internal, err
	}

	return modelValidateExitCode(opts.strict, outcome.diagnostics), nil
}

// modelValidateFailureCode gives the exit code of a run that failed before it had an outcome.
func modelValidateFailureCode(err error) int {
	var failure *diag.Error
	if errors.As(err, &failure) {
		return failure.ExitCode
	}

	return diag.Internal
}

// modelValidateExitCode gives the exit code of an outcome: the code of its diagnostics, and with --strict also
// the code of a warning.
func modelValidateExitCode(strict bool, diagnostics []diag.Diagnostic) int {
	if code := intent.ExitCodeForDiagnostics(diagnostics); code != diag.OK {
		return code
	}

	if strict && countDiagnostics(diagnostics, diag.SeverityWarning) > 0 {
		return diag.InvalidInput
	}

	return diag.OK
}

// countDiagnostics counts the diagnostics of one severity.
func countDiagnostics(diagnostics []diag.Diagnostic, severity diag.Severity) int {
	count := 0

	for i := range diagnostics {
		if diagnostics[i].Severity == severity {
			count++
		}
	}

	return count
}

func writeModelValidate(app *Context, format string, outcome *modelValidateOutcome) error {
	switch format {
	case modelFormatJSON:
		return writeModelValidateJSON(app, outcome)
	case modelFormatJUnit:
		return app.WriteOutput(renderModelValidateJUnit(outcome.diagnostics), "")
	default:
		return writeModelValidatePretty(app, outcome)
	}
}

func writeModelValidateJSON(app *Context, outcome *modelValidateOutcome) error {
	doc := modelValidateJSON{SchemaVer: "1.1", Mode: outcome.mode, Diagnostics: nonNilDiagnostics(outcome.diagnostics)}
	subject := outcome.subject

	if outcome.mode == modeArchitecture || outcome.mode == modeArchitectureWithPattern {
		doc.Architecture = &subject
	} else {
		doc.Document = &subject
	}

	text, err := encodeJSONIndented(doc)
	if err != nil {
		return err
	}

	return app.WriteOutput(text, "")
}

func writeModelValidatePretty(app *Context, outcome *modelValidateOutcome) error {
	for _, d := range outcome.diagnostics {
		app.Err(diag.FormatDiagnostic(d) + "\n")
	}

	errCount := countDiagnostics(outcome.diagnostics, diag.SeverityError)
	warnCount := countDiagnostics(outcome.diagnostics, diag.SeverityWarning)

	if errCount == 0 {
		return app.WriteOutput(fmt.Sprintf("model valid: %s (%d warning(s))\n", outcome.subject.Path, warnCount), "")
	}

	return app.WriteOutput(fmt.Sprintf("model not valid: %d error(s), %d warning(s)\n", errCount, warnCount), "")
}

func nonNilDiagnostics(list []diag.Diagnostic) []diag.Diagnostic {
	if list == nil {
		return []diag.Diagnostic{}
	}

	return list
}

// encodeJSONIndented gives JSON with a 2-space indent, a final newline and no HTML escaping.
func encodeJSONIndented(v any) (string, error) {
	var out strings.Builder

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode JSON: %w", err)
	}

	return out.String(), nil
}
