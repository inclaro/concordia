package cli

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/reporting"
)

type traceOptions struct {
	results     string
	environment []string
	source      bool
}

func newTraceCmd(host *Host) *cobra.Command {
	var opts traceOptions

	cmd := &cobra.Command{
		Use:   "trace <architecture-id>",
		Short: "Show the declared-level deployment trace of a CALM node: workload, image, build definition, source evidence",
		Args:  cobra.ExactArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return diag.OK, traceHandler(ctx, app, args[0], &opts)
		}),
	}

	cmd.Flags().StringArrayVar(&opts.environment, "environment", nil, "declared environment id; repeatable (default: all declared environments)")
	cmd.Flags().BoolVar(&opts.source, "source", false, "show the source line of each location with 2 lines around it (read from the working tree, redacted)")
	cmd.Flags().StringVar(&opts.results, "results", "", "results file (default: the JSON output of the workspace)")

	return cmd
}

// traceSelection checks the environments and the subject against the trace. It gives CO-WRK-017 for no declared
// environment or an undeclared one, and CO-WRK-010 for a node that has no trace.
func traceSelection(trace *deployment.Trace, subject string, wanted []string) ([]string, error) {
	if !trace.Configured() {
		return nil, diag.Fail(diag.WRK017, diag.Detail("no environment is declared; environment-specific assurance needs a declared environment"))
	}

	declared := make([]string, len(trace.Environments))
	for i := range trace.Environments {
		declared[i] = trace.Environments[i].Name
	}

	if err := traceCheckDeclared(declared, wanted); err != nil {
		return nil, err
	}

	selected := wanted
	if len(selected) == 0 {
		selected = declared
	}

	if !traceHasSubject(trace, subject, selected) {
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("'%s' is not a trace subject (a node tagged `deployable`, a deployed node of `deployed-in`, or a node with a deployment binding)", subject)))
	}

	return selected, nil
}

// traceCheckDeclared gives CO-WRK-017 for the first wanted environment that the results do not declare.
func traceCheckDeclared(declared, wanted []string) error {
	for _, name := range wanted {
		if !slices.Contains(declared, name) {
			return diag.Fail(diag.WRK017, diag.Detail(fmt.Sprintf("'%s' (declared in the results: %s)", name, strings.Join(declared, ", "))))
		}
	}

	return nil
}

// traceHasSubject reports whether the subject has a trace in any of the environments.
func traceHasSubject(trace *deployment.Trace, subject string, environments []string) bool {
	return slices.ContainsFunc(environments, func(name string) bool {
		return trace.Subject(subject, name) != nil
	})
}

// traceFormat is the output format of trace: pretty unless the options name another, which must be json.
func traceFormat(app *Context) (string, error) {
	format := app.Opts.Format
	if format == "" {
		return reporting.FormatPretty, nil
	}

	if format != reporting.FormatPretty && format != reporting.FormatJSON {
		return "", diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported format '%s' for trace (pretty, json)", format)))
	}

	return format, nil
}

func traceHandler(ctx context.Context, app *Context, subject string, opts *traceOptions) error {
	format, err := traceFormat(app)
	if err != nil {
		return err
	}

	run, roots, err := explainRun(ctx, app, opts.results)
	if err != nil {
		return err
	}

	selected, err := traceSelection(&run.DeploymentTrace, subject, opts.environment)
	if err != nil {
		return err
	}

	req := traceRequest{
		trace:      &run.DeploymentTrace,
		subject:    subject,
		selected:   selected,
		source:     &traceSource{roots: roots, contexts: map[string]*sourceContext{}},
		withSource: opts.source,
		verbose:    app.Opts.Verbose,
	}
	out, err := req.output(format)

	roots.report(app)
	reportRedactions(app, uniqueRecords(req.source.records))

	if err != nil {
		return err
	}

	return app.WriteOutput(out, "")
}

// traceRequest is what one run of trace renders: the subject in the selected environments, with the source lines of
// its locations only when withSource is set.
type traceRequest struct {
	trace      *deployment.Trace
	source     *traceSource
	subject    string
	selected   []string
	withSource bool
	verbose    bool
}

// output renders the trace in the format: the JSON document, or the pretty text.
func (r *traceRequest) output(format string) (string, error) {
	if format == reporting.FormatJSON {
		return traceJSON(r.trace, r.subject, r.selected, r.jsonSource())
	}

	render := deployment.RenderOptions{Environments: r.selected, Verbose: r.verbose}
	if r.withSource {
		render.Source = r.source.lines
	}

	return tracePretty(r.trace, r.subject, &render)
}

// jsonSource is the source of the JSON output: nil leaves the source contexts out.
func (r *traceRequest) jsonSource() *traceSource {
	if !r.withSource {
		return nil
	}

	return r.source
}

// traceSource reads the lines around the locations of a trace through readContext: confined to the workspace
// roots and redacted. It keeps each context once and the redaction records of the reads.
type traceSource struct {
	contexts map[string]*sourceContext
	roots    sourceRoots
	records  []redact.Record
}

func (s *traceSource) lines(loc deployment.Location) []string {
	key := loc.Path + ":" + strconv.Itoa(loc.Line)

	read, seen := s.contexts[key]
	if !seen {
		read = readContext(s.roots, &conformance.Location{Path: loc.Path, Line: loc.Line})
		s.contexts[key] = read

		if read != nil {
			for _, r := range read.Redactions {
				r.Output = "trace"
				s.records = append(s.records, r)
			}
		}
	}

	if read == nil {
		return nil
	}

	lines := make([]string, len(read.Lines))
	for i, line := range read.Lines {
		lines[i] = fmt.Sprintf("%*d | %s", lineNumberWidth, line.Line, line.Text)
	}

	return lines
}

func tracePretty(trace *deployment.Trace, subject string, render *deployment.RenderOptions) (string, error) {
	var buf bytes.Buffer

	if _, err := deployment.Render(&buf, trace, subject, *render); err != nil {
		return "", fmt.Errorf("render trace: %w", err)
	}

	// Names and source text come from repository content: no control sequence reaches the terminal.
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	for i, line := range lines {
		lines[i] = redact.SanitizeLine(line)
	}

	return strings.Join(lines, "\n") + "\n", nil
}

// tracedSubject is one trace of the JSON output with the cluster of its environment.
type tracedSubject struct {
	Cluster string `json:"cluster,omitempty"`
	deployment.SubjectTrace
}

// traceSubjects are the traces of the subject in the selected environments, each with the cluster of its
// environment.
func traceSubjects(trace *deployment.Trace, subject string, selected []string) []tracedSubject {
	traces := []tracedSubject{}

	for i := range trace.Environments {
		env := &trace.Environments[i]
		if !slices.Contains(selected, env.Name) {
			continue
		}

		for j := range env.Subjects {
			if env.Subjects[j].ArchitectureID == subject {
				traces = append(traces, tracedSubject{SubjectTrace: env.Subjects[j], Cluster: env.Cluster})
			}
		}
	}

	return traces
}

// traceSourceContexts reads the source lines of every location of the traces and gives the contexts that were
// read, in key order.
func traceSourceContexts(traces []tracedSubject, source *traceSource) []sourceContext {
	for i := range traces {
		for _, loc := range traceLocations(&traces[i].SubjectTrace) {
			source.lines(loc)
		}
	}

	contexts := []sourceContext{}

	for _, key := range slices.Sorted(maps.Keys(source.contexts)) {
		if read := source.contexts[key]; read != nil {
			contexts = append(contexts, *read)
		}
	}

	return contexts
}

// traceJSON is the JSON document of the trace. A nil source leaves the source contexts out.
func traceJSON(trace *deployment.Trace, subject string, selected []string, source *traceSource) (string, error) {
	traces := traceSubjects(trace, subject, selected)

	doc := map[string]any{
		"schemaVersion":  conformance.ResultSchemaVersion,
		"architectureId": subject,
		"levels":         trace.Levels,
		"traces":         traces,
	}

	if source != nil {
		doc["sourceContext"] = traceSourceContexts(traces, source)
	}

	text, err := canonjson.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("encode trace: %w", err)
	}

	return string(text), nil
}

// traceLocations lists the locations with a line of the entities of a trace.
func traceLocations(sub *deployment.SubjectTrace) []deployment.Location {
	var locations []deployment.Location

	for i := range sub.Steps {
		for j := range sub.Steps[i].Entities {
			locations = append(locations, traceLinedLocations(sub.Steps[i].Entities[j].Locations)...)
		}
	}

	return locations
}

// traceLinedLocations keeps the locations that have a line.
func traceLinedLocations(all []deployment.Location) []deployment.Location {
	var lined []deployment.Location

	for _, loc := range all {
		if loc.Line > 0 {
			lined = append(lined, loc)
		}
	}

	return lined
}
