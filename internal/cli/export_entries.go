package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/workspace"
)

const resultsBundlePath = "results/results.json"

// readConfined reads the workspace-relative "/" path of a bundle input. A path that leaves the
// workspace root (absolute, "..", or a symbolic link out of it) is refused: it gives no content
// and a CO-REP-006 diagnostic on stderr, and ok is false. A path that is not a file of the
// workspace (a built-in pack) is skipped silently.
func readConfined(app *Context, wsp *workspace.Workspace, rel, what string) (data []byte, ok bool, err error) {
	if rel == "" || strings.HasPrefix(rel, "builtin:") {
		return nil, false, nil
	}

	abs, err := fsutil.Confine(wsp.Root, rel)
	if errors.Is(err, fsutil.ErrOutsideRoot) {
		app.Err(diag.FormatDiagnostic(diag.New(diag.REP006, diag.Params{keyPath: rel, keyDetail: err.Error()})) + "\n")

		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("read %s %s: %w", what, rel, err)
	}

	data, err = os.ReadFile(abs) //nolint:gosec // Confine resolved the path below the workspace root.
	if err != nil {
		return nil, false, fmt.Errorf("read %s %s: %w", what, rel, err)
	}

	return data, true, nil
}

// addConfined adds the confined file of a bundle input as `inputs/<path>`, unless it is refused or skipped.
func addConfined(app *Context, wsp *workspace.Workspace, rel, what string, byPath map[string][]byte) error {
	data, ok, err := readConfined(app, wsp, rel, what)
	if err != nil {
		return err
	}

	if ok {
		byPath["inputs/"+rel] = data
	}

	return nil
}

// addInputs adds the inputs of the run, the URL mapping of the architecture with its mapped files, and the locked
// cache files as `inputs/<path>` entries. A path that leaves the workspace root is refused (CO-REP-006) and left out.
func addInputs(app *Context, wsp *workspace.Workspace, run *conformance.Run, byPath map[string][]byte) error {
	for _, ref := range inputRefsOf(&run.Inputs) {
		if err := addConfined(app, wsp, ref.Path, "input", byPath); err != nil {
			return err
		}
	}

	if err := addURLMapping(app, wsp, byPath); err != nil {
		return err
	}

	return addLockedResources(app, wsp, byPath)
}

// addURLMapping adds the URL mapping file (architecture.urlMapping) and each file that it maps inside the workspace as
// `inputs/<path>`, so that `diff` loads the architecture of the bundle with the same mapping. An entry resolves
// against the directory of the mapping file, as intent.LoadURLMapping does. An absolute entry, an entry that leaves
// the workspace, and a mapped file that does not exist are left out.
func addURLMapping(app *Context, wsp *workspace.Workspace, byPath map[string][]byte) error {
	name := wsp.Architecture.URLMapping
	if name == "" {
		return nil
	}

	name = path.Clean(filepath.ToSlash(name))

	data, ok, err := readConfined(app, wsp, name, "url mapping")
	if err != nil || !ok {
		return err
	}

	byPath["inputs/"+name] = data

	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse url mapping %s: %w", name, err)
	}

	for _, entry := range entries {
		rel, ok := mappedPath(path.Dir(name), entry)
		if !ok {
			continue
		}

		if err := addMappedFile(app, wsp, rel, byPath); err != nil {
			return err
		}
	}

	return nil
}

// mappedPath gives the workspace-relative path of a mapping entry, resolved against the directory of the mapping
// file. An absolute entry (checked before the join, which would make it relative) or one that leaves the workspace
// gives false.
func mappedPath(dir, entry string) (string, bool) {
	slashed := filepath.ToSlash(entry)
	if filepath.IsAbs(entry) || path.IsAbs(slashed) {
		return "", false
	}

	rel := path.Join(dir, slashed)

	return rel, filepath.IsLocal(filepath.FromSlash(rel))
}

// addMappedFile adds one mapped file of the URL mapping; a file that does not exist is left out.
func addMappedFile(app *Context, wsp *workspace.Workspace, rel string, byPath map[string][]byte) error {
	err := addConfined(app, wsp, rel, "mapped file", byPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}

// addLockedResources adds the cache files that the lock lists as `inputs/<path>` entries.
func addLockedResources(app *Context, wsp *workspace.Workspace, byPath map[string][]byte) error {
	lock, found, err := workspace.ReadLock(workspace.LockPathFor(wsp.Root))
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if !found {
		return nil
	}

	for _, res := range lock.Resources {
		if !strings.HasPrefix(res.Path, remote.CacheDir) {
			continue
		}

		if err := addConfined(app, wsp, res.Path, "locked resource", byPath); err != nil {
			return err
		}
	}

	return nil
}

// bundleFiles collects the files of a bundle by their path in it.
type bundleFiles struct {
	app    *Context
	wsp    *workspace.Workspace
	run    *conformance.Run
	byPath map[string][]byte
}

// bundleEntries gives the files of the bundle of the job, the records of the values that were removed from them, and
// the views of the bundle.
func bundleEntries(ctx context.Context, job *exportJob) ([]bundle.Entry, []redact.Record, []bundle.View, error) {
	files := &bundleFiles{app: job.app, wsp: job.wsp, run: job.run, byPath: map[string][]byte{}}

	if err := files.addResultFiles(); err != nil {
		return nil, nil, nil, err
	}

	viewEntries, err := addViews(ctx, job.app, job.wsp, job.run, files.byPath)
	if err != nil {
		return nil, nil, nil, err
	}

	// The titles of the decisions in results.json and results.sarif were redacted by the governance evaluation.
	records, err := files.addEvaluationFiles()
	if err != nil {
		return nil, nil, nil, err
	}

	optional, err := files.addOptionalFiles(job.opts)
	if err != nil {
		return nil, nil, nil, err
	}

	return files.entries(), append(records, optional...), viewEntries, nil
}

// addResultFiles adds the results, the coverage files, the inputs, and the scoped waivers of the run.
func (f *bundleFiles) addResultFiles() error {
	results, err := conformance.CanonicalJSON(f.run)
	if err != nil {
		return fmt.Errorf("encode results: %w", err)
	}

	f.byPath[resultsBundlePath] = results

	sarif, err := reporting.Render(reporting.FormatSARIF, f.run, reporting.PrettyOptions{})
	if err != nil {
		return fmt.Errorf("encode sarif: %w", err)
	}

	f.byPath["results/results.sarif"] = sarif

	if err := f.addCoverage(); err != nil {
		return err
	}

	if err := addInputs(f.app, f.wsp, f.run, f.byPath); err != nil {
		return err
	}

	return f.addScopedWaivers()
}

// addCoverage adds the coverage files that `index` wrote as `coverage/<name>`.
func (f *bundleFiles) addCoverage() error {
	coverage, err := filepath.Glob(filepath.Join(f.wsp.Root, "build", "concordia", "coverage*.json"))
	if err != nil {
		return fmt.Errorf("find coverage files: %w", err)
	}

	for _, p := range coverage {
		data, err := os.ReadFile(p) //nolint:gosec // a file that `index` wrote.
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}

		f.byPath["coverage/"+filepath.Base(p)] = data
	}

	return nil
}

// addScopedWaivers adds the scoped waivers of the run, when it has any.
func (f *bundleFiles) addScopedWaivers() error {
	if len(f.run.Exceptions.ScopedWaivers) == 0 {
		return nil
	}

	scopedWaivers, err := conformance.ScopedWaiversJSON(f.run)
	if err != nil {
		return err //nolint:wrapcheck // already has context.
	}

	f.byPath[bundle.ScopedWaiversPath] = scopedWaivers

	return nil
}

// addEvaluationFiles adds the review and assurance files of the run and gives the records of the values that were
// removed from them, after those of the decision titles.
func (f *bundleFiles) addEvaluationFiles() ([]redact.Record, error) {
	reviewRecords, err := addReview(f.app, f.wsp, f.run, f.byPath)
	if err != nil {
		return nil, err
	}

	assuranceFiles, assuranceRecords, err := conformance.AssuranceBundleFiles(f.run)
	if err != nil {
		return nil, err //nolint:wrapcheck // already has context.
	}

	for _, file := range assuranceFiles {
		f.byPath[file.Path] = file.Data
	}

	return append(append(decisionRedactions(f.run), reviewRecords...), assuranceRecords...), nil
}

// addOptionalFiles adds the evidence graph and the source snippets that the options ask for.
func (f *bundleFiles) addOptionalFiles(opts *exportBundleOptions) ([]redact.Record, error) {
	var records []redact.Record

	if opts.includeGraph {
		data, recs, err := graphJSON(f.run)
		if err != nil {
			return nil, err
		}

		f.byPath[graphEvidencePath] = data
		records = recs
	}

	if opts.snippets == snippetsViolations {
		roots := sourceRootsOf(f.wsp)
		records = append(records, addSnippets(f.byPath, roots, f.run)...)
		roots.report(f.app)
	}

	return records, nil
}

// entries gives the files as bundle entries.
func (f *bundleFiles) entries() []bundle.Entry {
	entries := make([]bundle.Entry, 0, len(f.byPath))
	for p, data := range f.byPath {
		entries = append(entries, bundle.Entry{Path: p, Data: data})
	}

	return entries
}

// addSnippets adds the redacted snippet of each live violation as `snippets/<fingerprint>.txt` and gives
// the records of the values that it removed (with the output set).
func addSnippets(byPath map[string][]byte, roots sourceRoots, run *conformance.Run) []redact.Record {
	violating := violatingRuleIDs(run)

	var records []redact.Record

	for i := range run.Findings {
		if fnd := &run.Findings[i]; hasLiveViolation(violating, fnd) {
			records = append(records, addSnippet(byPath, roots, fnd)...)
		}
	}

	return records
}

// addSnippet adds the snippet of a finding with a location, when its source can be read, and gives the records of
// the values that it removed (with the output set).
func addSnippet(byPath map[string][]byte, roots sourceRoots, fnd *conformance.Finding) []redact.Record {
	snip := readContext(roots, fnd.Location)
	if snip == nil {
		return nil
	}

	output := "snippets/" + hexOf(fnd.Fingerprint) + ".txt"
	byPath[output] = []byte(snippetText(fnd.Location, snip))

	records := make([]redact.Record, 0, len(snip.Redactions))

	for _, r := range snip.Redactions {
		r.Output = output
		records = append(records, r)
	}

	return records
}

// violatingRuleIDs gives the IDs of the rules whose verdict is a violation.
func violatingRuleIDs(run *conformance.Run) map[string]struct{} {
	violating := map[string]struct{}{}

	for i := range run.Rules {
		if run.Rules[i].Verdict == conformance.VerdictViolation {
			violating[run.Rules[i].RuleID] = struct{}{}
		}
	}

	return violating
}

// hasLiveViolation says whether a finding is live, has a location, and belongs to a rule that violates.
func hasLiveViolation(violating map[string]struct{}, fnd *conformance.Finding) bool {
	_, ok := violating[fnd.RuleID]

	return ok && conformance.IsLive(fnd) && fnd.Location != nil
}

// snippetText renders the numbered source lines of a snippet below the text of its location.
func snippetText(loc *conformance.Location, snip *sourceContext) string {
	var b strings.Builder

	b.WriteString(locationText(loc) + "\n")

	for _, l := range snip.Lines {
		fmt.Fprintf(&b, "%*d  %s\n", snippetNumberWidth, l.Line, l.Text)
	}

	return b.String()
}

// decisionRedactions gives the redactions of the ADR titles in the findings and the decision summary, which the
// bundle holds in results/results.json: the output of each record is that bundle path.
func decisionRedactions(run *conformance.Run) []redact.Record {
	out := make([]redact.Record, 0, len(run.DecisionRedactions))

	for _, r := range run.DecisionRedactions {
		r.Output = resultsBundlePath
		out = append(out, r)
	}

	return out
}
