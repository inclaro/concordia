package cli

import (
	"errors"
	"maps"
	"os"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

const sourceContextLines = 2

// sourceLine is one line of source text.
type sourceLine struct {
	Text string `json:"text"`
	Line int    `json:"line"`
}

// sourceContext is the lines of source text around a location. The text is read from the working tree and never stored.
// The lines are redacted (internal/redact); Redactions are the records of the values removed from the window.
type sourceContext struct {
	Location   conformance.Location `json:"location"`
	Lines      []sourceLine         `json:"lines"`
	Redactions []redact.Record      `json:"-"`
}

// sourceRoots are the absolute roots of the workspace projects. Paths that confinement refused
// are collected in refused; report writes them as CO-REP-006 diagnostics.
type sourceRoots struct {
	projects map[string]string
	refused  map[string]string
	primary  string
}

func newSourceRoots(primary string) sourceRoots {
	return sourceRoots{primary: primary, projects: map[string]string{}, refused: map[string]string{}}
}

func sourceRootsOf(wsp *workspace.Workspace) sourceRoots {
	roots := newSourceRoots(wsp.ProjectRoot())
	for _, p := range wsp.Observation.Projects {
		roots.projects[p.Name] = wsp.AbsProjectRoot(&p)
	}

	return roots
}

// report writes a CO-REP-006 diagnostic to stderr for each path that the reads refused, once, in path order.
func (r sourceRoots) report(app *Context) {
	for _, path := range slices.Sorted(maps.Keys(r.refused)) {
		app.Err(diag.FormatDiagnostic(diag.New(diag.REP006, diag.Params{keyPath: path, keyDetail: r.refused[path]})) + "\n")
	}

	clear(r.refused)
}

// resolve gives the root and the relative path of a workspace path. A path with the
// prefix @<name>/ is below the root of the project <name>.
func (r sourceRoots) resolve(p string) (root, rel string) {
	name, rest := workspace.SplitProjectPath(p)
	if name == "" {
		return r.primary, p
	}

	projectRoot, known := r.projects[name]
	if !known {
		return r.primary, p
	}

	return projectRoot, rest
}

// readContext reads the lines around a location. It gives nil when the file is missing or the
// location has no line. A path that leaves the root (absolute, "..", or a symbolic link out of
// it) gives nil, no content, and is recorded for report.
func readContext(roots sourceRoots, location *conformance.Location) *sourceContext {
	if location.Line <= 0 {
		return nil
	}

	root, path := roots.resolve(location.Path)

	abs, err := fsutil.Confine(root, path)
	if err != nil {
		if errors.Is(err, fsutil.ErrOutsideRoot) && roots.refused != nil {
			roots.refused[location.Path] = err.Error()
		}

		return nil
	}

	data, err := os.ReadFile(abs) //nolint:gosec // Confine resolved the path below the source root.
	if err != nil {
		return nil
	}

	lines, records := redact.Default().RedactLines(path, string(data))
	from := max(1, location.Line-sourceContextLines)
	to := location.Line + sourceContextLines
	ctxt := &sourceContext{Location: *location, Lines: []sourceLine{}}

	for _, l := range lines {
		if l.Number >= from && l.Number <= to {
			ctxt.Lines = append(ctxt.Lines, sourceLine{Line: l.Number, Text: l.Text})
		}
	}

	for _, r := range records {
		if r.Line >= from && r.Line <= to {
			r.File = location.Path
			ctxt.Redactions = append(ctxt.Redactions, r)
		}
	}

	return ctxt
}

// uniqueRecords drops the records that repeat the same place: several findings can share one location.
func uniqueRecords(records []redact.Record) []redact.Record {
	seen := map[redact.Record]struct{}{}
	out := make([]redact.Record, 0, len(records))

	for _, r := range records {
		if _, dup := seen[r]; dup {
			continue
		}

		seen[r] = struct{}{}
		out = append(out, r)
	}

	return out
}

// reportRedactions writes the CO-REP-005 notice to stderr for each output that holds a redaction,
// with the count and never a value, in output order.
func reportRedactions(app *Context, records []redact.Record) {
	counts := map[string]int{}
	for _, r := range records {
		counts[r.Output]++
	}

	for _, output := range slices.Sorted(maps.Keys(counts)) {
		app.Err(diag.FormatDiagnostic(diag.New(diag.REP005, diag.Params{"count": strconv.Itoa(counts[output]), "output": output})) + "\n")
	}
}
