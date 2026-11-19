package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	watchRulesAll     = "all"
	watchRulesChanged = "changed"
	minDebounceMs     = 200
	defaultDebounceMs = 1000
	fingerprintShort  = 12
	transitionVerdict = "verdict"
	transitionNew     = "new"
	transitionDone    = "resolved"
	transitionSummary = "summary"
	// refHead is the Git ref that the changed-files scope compares against by default.
	refHead = "HEAD"
)

type watchOptions struct {
	rules    string
	debounce int
	render   bool
}

// transition is one change between two check runs.
type transition struct {
	ExitCode    *int   `json:"exitCode,omitempty"`
	Time        string `json:"time,omitempty"`
	Type        string `json:"type"`
	RuleID      string `json:"ruleId,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Message     string `json:"message,omitempty"`
	Location    string `json:"location,omitempty"`

	active  int
	unknown int
}

func newWatchCmd(host *Host) *cobra.Command {
	opts := watchOptions{}

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Index and check again when the source changes; print the transitions",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return watchHandler(ctx, app, &opts)
		}),
	}

	addChoiceFlag(cmd, false, &opts.rules, "rules", "rules to report: changed|all", watchRulesAll, watchRulesChanged)
	cmd.Flags().IntVar(&opts.debounce, "debounce", defaultDebounceMs, "milliseconds between source polls (at least 200)")
	cmd.Flags().BoolVar(&opts.render, "render", false, "write the Structurizr DSL after each cycle")

	return cmd
}

// transitions lists the changes from prev to next. prev is nil on the first cycle.
func transitions(prev, next *conformance.Run) []transition {
	var out []transition

	if prev != nil {
		out = verdictTransitions(prev, next)
	}

	prevLive := liveFindings(prev)

	out = append(out, newFindingTransitions(next, prevLive)...)
	out = append(out, resolvedTransitions(prev, liveFindings(next))...)

	if prev == nil || prev.Summary.ExitCode != next.Summary.ExitCode {
		code := next.Summary.ExitCode
		out = append(out, transition{Type: transitionSummary, ExitCode: &code, active: next.Summary.Active, unknown: next.Summary.UnknownRules})
	}

	return out
}

// verdictTransitions lists the rules whose verdict differs between prev and next.
func verdictTransitions(prev, next *conformance.Run) []transition {
	before := make(map[string]conformance.RuleVerdict, len(prev.Rules))
	for i := range prev.Rules {
		before[prev.Rules[i].RuleID] = prev.Rules[i].Verdict
	}

	var out []transition

	for i := range next.Rules {
		rule := &next.Rules[i]
		if old, ok := before[rule.RuleID]; ok && old != rule.Verdict {
			out = append(out, transition{Type: transitionVerdict, RuleID: rule.RuleID, From: string(old), To: string(rule.Verdict)})
		}
	}

	return out
}

// resolvedTransitions lists the live findings of prev that nextLive no longer has, in prev order.
func resolvedTransitions(prev *conformance.Run, nextLive map[string]*conformance.Finding) []transition {
	if prev == nil {
		return nil
	}

	var out []transition

	for i := range prev.Findings {
		finding := &prev.Findings[i]
		if _, ok := nextLive[finding.Fingerprint]; conformance.IsLive(finding) && !ok {
			out = append(out, transition{Type: transitionDone, RuleID: finding.RuleID, Fingerprint: finding.Fingerprint})
		}
	}

	return out
}

// liveFindings indexes the live findings of a run by fingerprint. A nil run has none.
func liveFindings(run *conformance.Run) map[string]*conformance.Finding {
	live := map[string]*conformance.Finding{}

	if run == nil {
		return live
	}

	for i := range run.Findings {
		if conformance.IsLive(&run.Findings[i]) {
			live[run.Findings[i].Fingerprint] = &run.Findings[i]
		}
	}

	return live
}

// newFindingTransitions lists the live findings of next that prevLive does not have.
func newFindingTransitions(next *conformance.Run, prevLive map[string]*conformance.Finding) []transition {
	var out []transition

	for i := range next.Findings {
		finding := &next.Findings[i]
		if !conformance.IsLive(finding) {
			continue
		}

		if _, ok := prevLive[finding.Fingerprint]; ok {
			continue
		}

		item := transition{Type: transitionNew, RuleID: finding.RuleID, Fingerprint: finding.Fingerprint, Message: finding.Message}
		if finding.Location != nil {
			item.Location = finding.Location.Path
			if finding.Location.Line > 0 {
				item.Location += fmt.Sprintf(":%d", finding.Location.Line)
			}
		}

		out = append(out, item)
	}

	return out
}

func (t *transition) pretty() string {
	switch t.Type {
	case transitionVerdict:
		return fmt.Sprintf("%s %s %s -> %s", t.Time, t.RuleID, t.From, t.To)
	case transitionNew:
		return strings.TrimSpace(fmt.Sprintf("%s NEW %s %s %s %s", t.Time, t.RuleID, shortFingerprint(t.Fingerprint), t.Message, t.Location))
	case transitionDone:
		return fmt.Sprintf("%s RESOLVED %s %s", t.Time, t.RuleID, shortFingerprint(t.Fingerprint))
	default:
		code := 0
		if t.ExitCode != nil {
			code = *t.ExitCode
		}

		return fmt.Sprintf("%s SUMMARY exit %d: %d active, %d unknown", t.Time, code, t.active, t.unknown)
	}
}

func shortFingerprint(fp string) string {
	fp = strings.TrimPrefix(fp, "sha256:")
	if len(fp) > fingerprintShort {
		return fp[:fingerprintShort]
	}

	return fp
}

func watchHandler(ctx context.Context, app *Context, opts *watchOptions) (int, error) {
	if opts.debounce < minDebounceMs {
		return diag.InvalidInput, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("--debounce must be at least %d ms", minDebounceMs)))
	}

	format := app.Opts.Format
	if format == "" {
		format = reporting.FormatPretty
	}

	if format != reporting.FormatPretty && format != modelFormatJSON {
		return diag.InvalidInput, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported format '%s' (pretty, json)", format)))
	}

	wsp, err := app.LoadWorkspace()
	if err != nil {
		return diag.Internal, err
	}

	monitor := &watcher{app: app, wsp: wsp, opts: opts, json: format == modelFormatJSON}

	monitor.cycle(ctx, true)

	ticker := time.NewTicker(time.Duration(opts.debounce) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return diag.Interrupted, nil
		case <-ticker.C:
		}

		key := monitor.sourceKey(ctx)

		switch monitor.tick.decide(key, monitor.generations) {
		case tickIndexAndCheck:
			monitor.cycle(ctx, true)
		case tickCheckOnly:
			monitor.cycle(ctx, false)
		case tickNone:
		}
	}
}

// tickAction is what the watch loop does on one tick.
type tickAction int

const (
	tickNone tickAction = iota
	tickIndexAndCheck
	tickCheckOnly
)

// tickState is the state of the watch loop that the tick decision reads.
type tickState struct {
	lastKey        string
	pendingKey     string
	lastGeneration string
	pending        bool
}

// decide chooses the action for a tick with the current source key. A key that differs from the
// last one is pending; the same key on the next tick is stable and starts a cycle. When the source
// did not change, a new graph generation starts a check without an index. generation is read only
// when it is needed.
func (s *tickState) decide(key string, generation func() string) tickAction {
	if key == s.lastKey {
		s.pending = false

		if generation() != s.lastGeneration {
			return tickCheckOnly
		}

		return tickNone
	}

	if s.pending && key == s.pendingKey {
		s.pending = false
		s.lastKey = key

		return tickIndexAndCheck
	}

	s.pending = true
	s.pendingKey = key

	return tickNone
}

type watcher struct {
	app  *Context
	wsp  *workspace.Workspace
	opts *watchOptions
	prev *conformance.Run
	tick tickState
	json bool
}

// sourceKey identifies the source state: for each project the revision and the dirty-tree digest.
func (w *watcher) sourceKey(ctx context.Context) string {
	var key strings.Builder

	isGenerated := conformance.GeneratedPathFilter(w.wsp)

	for _, project := range w.wsp.Observation.Projects {
		git := w.app.Git(w.wsp.AbsProjectRoot(&project))
		revision, _ := git.Revision(ctx)
		digest := ""

		if dirty, err := git.DirtyFiles(ctx); err == nil {
			if w.wsp.PathPrefix(&project) == "" {
				dirty = slices.DeleteFunc(slices.Clone(dirty), isGenerated)
			}

			digest, _ = workspace.DirtyTreeDigest(w.wsp.AbsProjectRoot(&project), dirty)
		}

		key.WriteString(project.Name + "=" + revision + ":" + digest + "\n")
	}

	return key.String()
}

// generations reads the graphGeneration of each coverage file.
func (w *watcher) generations() string {
	var out strings.Builder

	for _, project := range w.wsp.Observation.Projects {
		file := conformance.CoverageFileFor(w.wsp.PathPrefix(&project) == "", project.Name)

		data, err := os.ReadFile(filepath.Join(w.wsp.Root, filepath.FromSlash(file)))
		if err != nil {
			continue
		}

		var cov struct {
			GraphGeneration string `json:"graphGeneration"`
		}

		if json.Unmarshal(data, &cov) == nil {
			out.WriteString(project.Name + "=" + cov.GraphGeneration + "\n")
		}
	}

	return out.String()
}

func (w *watcher) fail(err error) {
	var failure *diag.Error
	if errors.As(err, &failure) {
		w.app.Err(diag.FormatDiagnostic(failure.Diagnostic) + "\n")

		return
	}

	w.app.Err(err.Error() + "\n")
}

// cycle runs one index (when withIndex) and one check, then prints the transitions.
func (w *watcher) cycle(ctx context.Context, withIndex bool) {
	if withIndex {
		if _, err := indexProjects(ctx, w.app, w.wsp, &indexOptions{}); err != nil {
			w.fail(err)

			return
		}
	}

	w.tick.lastKey = w.sourceKey(ctx)
	w.tick.lastGeneration = w.generations()

	options := &conformance.CheckOptions{Env: checkEnv(w.app), Deps: checkDeps(w.app)}
	if w.opts.rules == watchRulesChanged {
		options.Changed = refHead
	}

	run, err := conformance.RunCheck(ctx, options)
	if err != nil {
		w.fail(err)

		return
	}

	if w.opts.render {
		if err = renderDSLFromRun(ctx, w.app, &renderOptions{}, run); err != nil {
			w.fail(err)
		}
	}

	now := w.app.Now().UTC().Format(time.RFC3339)
	list := transitions(w.prev, run)
	w.prev = run

	for i := range list {
		list[i].Time = now
		w.print(&list[i])
	}
}

func (w *watcher) print(t *transition) {
	if !w.json {
		w.app.Out(t.pretty() + "\n")

		return
	}

	data, err := canonjson.Marshal(t)
	if err != nil {
		w.fail(err)

		return
	}

	w.app.Out(string(data) + "\n")
}
