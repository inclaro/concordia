package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/reporting"
)

const (
	explainListLimit = 20
	coverageLinesCap = 12
)

// Values of --path.
const (
	pathShortest = "shortest"
	pathAll      = "all"
)

type explainOptions struct {
	finding string
	results string
	path    string
	source  bool
}

func newExplainCmd(host *Host) *cobra.Command {
	opts := explainOptions{path: pathShortest}

	cmd := &cobra.Command{
		Use:   "explain <rule-id>",
		Short: "Explain a rule or a finding: rationale, subjects, plan, evidence path, coverage, disposition",
		Args:  cobra.ExactArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return diag.OK, explainHandler(ctx, app, args[0], &opts)
		}),
	}

	cmd.Flags().StringVar(&opts.finding, "finding", "", "explain one finding (full fingerprint or unique hex prefix)")
	cmd.Flags().BoolVar(&opts.source, "source", false, "show 2 lines of source before and after each location (read from the working tree)")
	cmd.Flags().StringVar(&opts.results, "results", "", "results file (default: the JSON output of the workspace)")
	addChoiceFlag(cmd, false, &opts.path, "path", "evidence paths to show", pathShortest, pathAll)

	return cmd
}

func listLines(label string, items []string) []string {
	if len(items) == 0 {
		return []string{fmt.Sprintf("  %s: 0", label)}
	}

	lines := []string{fmt.Sprintf("  %s: %d", label, len(items))}
	for _, item := range items[:min(len(items), explainListLimit)] {
		lines = append(lines, "    "+item)
	}

	if len(items) > explainListLimit {
		lines = append(lines, fmt.Sprintf("    ... and %d more", len(items)-explainListLimit))
	}

	return lines
}

func calmNodes(ids []string) string {
	if len(ids) == 0 {
		return noneText
	}

	refs := make([]string, len(ids))
	for i, id := range ids {
		refs[i] = "calm:node:" + id
	}

	return strings.Join(refs, ", ")
}

func ruleLines(rule *conformance.RuleResult) ([]string, error) {
	lines := []string{
		rule.RuleID + " " + rule.Name,
		fmt.Sprintf("Verdict: %s   Severity: %s   Family: %s   Policy: %s@%s", rule.Verdict, rule.Severity, rule.Family, rule.Policy.ID, rule.Policy.Version),
	}

	for _, field := range [][2]string{
		{"Rationale: ", rule.Plan.Rationale}, {"Remediation: ", rule.Plan.Remediation}, {"Rule owner: ", rule.Plan.Owner},
	} {
		if field[1] != "" {
			lines = append(lines, field[0]+field[1])
		}
	}

	lines = append(lines, "Subjects: "+calmNodes(rule.Subjects), "Targets: "+calmNodes(rule.Targets), "Plan:")

	plan, err := canonjson.Marshal(rule.Plan)
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}

	for l := range strings.SplitSeq(strings.TrimRight(string(plan), "\n"), "\n") {
		lines = append(lines, "  "+l)
	}

	if rule.Coverage != nil {
		lines = append(lines, coverageLines(rule)...)
	}

	return append(lines, reasonLines(rule)...), nil
}

// reasonLines gives the reasons of the verdict of a rule.
func reasonLines(rule *conformance.RuleResult) []string {
	if len(rule.Reasons) == 0 {
		return nil
	}

	lines := []string{"Reasons:"}
	for _, r := range rule.Reasons {
		lines = append(lines, fmt.Sprintf("  %s: %s", r.Code, r.Message))
	}

	return lines
}

func coverageLines(rule *conformance.RuleResult) []string {
	c := rule.Coverage
	lines := make([]string, 0, coverageLinesCap)
	lines = append(lines, "Coverage:",
		fmt.Sprintf("  files: %s (%d/%d covered, required %s)", reporting.Percent(c.Files.Ratio), c.Files.Covered, c.Files.Total, reporting.Percent(c.Required)),
	)
	lines = append(lines, listLines("excluded", c.Files.Excluded)...)
	lines = append(lines, listLines("stale", c.Files.Stale)...)
	lines = append(lines, listLines("unindexed", c.Files.Unindexed)...)
	lines = append(lines,
		fmt.Sprintf("  unbound endpoints: %d", c.UnboundEndpoints),
		fmt.Sprintf("  sub-threshold evidence: %d", c.SubThresholdEvidence),
		fmt.Sprintf("  evidence: minimum confidence %s, on unknown %s", rule.Plan.EvidenceRequirements.MinimumConfidence, rule.Plan.EvidenceRequirements.OnUnknown),
	)

	return lines
}

// explainFormat gives the output format of `explain`: pretty by default, or json.
func explainFormat(app *Context) (string, error) {
	format := app.Opts.Format
	if format == "" {
		format = reporting.FormatPretty
	}

	if format != reporting.FormatPretty && format != reporting.FormatJSON {
		return "", diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported format '%s' for explain (pretty, json)", format)))
	}

	return format, nil
}

// explainReport is what `explain` shows: a rule, its selected findings, and what the output adds to them.
type explainReport struct {
	run        *conformance.Run
	rule       *conformance.RuleResult
	decisions  map[string]*decisionExplanation
	contexts   map[string][]sourceContext
	selected   []*conformance.Finding
	withSource bool
}

func explainHandler(ctx context.Context, app *Context, ruleID string, opts *explainOptions) error {
	format, err := explainFormat(app)
	if err != nil {
		return err
	}

	run, root, err := explainRun(ctx, app, opts.results)
	if err != nil {
		return err
	}

	rule, selected, err := pickRuleAndFindings(run, ruleID, opts.finding)
	if err != nil {
		return err
	}

	decisions, contexts, records := explainParts(run, root, selected, opts.source)
	if opts.source {
		root.report(app)
	}

	reportRedactions(app, uniqueRecords(records))

	report := &explainReport{run: run, rule: rule, selected: selected, decisions: decisions, contexts: contexts, withSource: opts.source}
	if format == reporting.FormatJSON {
		return explainJSON(app, report)
	}

	lines, err := report.prettyLines(opts.path == pathAll)
	if err != nil {
		return err
	}

	// Names, messages, and source text come from repository content: no control sequence of them
	// reaches the terminal. The JSON output above keeps the text as escaped JSON strings.
	for i, line := range lines {
		lines[i] = redact.SanitizeLine(line)
	}

	return app.WriteOutput(strings.Join(lines, "\n")+"\n", "")
}

// prettyLines gives the lines of the rule, its test candidates, and each selected finding.
func (r *explainReport) prettyLines(allPaths bool) ([]string, error) {
	lines, err := ruleLines(r.rule)
	if err != nil {
		return nil, err
	}

	lines = append(lines, testCandidateLines(explainCandidates(r.run, r.rule, r.selected))...)

	if len(r.selected) == 0 {
		lines = append(lines, "", "No findings.")
	}

	text := &findingText{run: r.run, withSource: r.withSource, allPaths: allPaths}

	for _, f := range r.selected {
		lines = append(lines, text.lines(f, r.decisions[f.Fingerprint], r.contexts[f.Fingerprint])...)
		lines = append(lines, traceExcerptLines(r.run, f)...)
	}

	return lines, nil
}

// explainedFinding is a finding with the optional source context.
type explainedFinding struct {
	// SourceContext is nil (left out) without --source.
	SourceContext any `json:"sourceContext,omitempty"`
	conformance.Finding
}

// findings gives the selected findings with their decisions and, with --source, their source contexts.
func (r *explainReport) findings() []explainedFinding {
	findings := make([]explainedFinding, 0, len(r.selected))

	for _, f := range r.selected {
		item := explainedFinding{Finding: *f}
		if explained := r.decisions[f.Fingerprint]; explained != nil {
			item.Decision = explained.decision
		}

		if r.withSource {
			item.SourceContext = r.contexts[f.Fingerprint]
		}

		findings = append(findings, item)
	}

	return findings
}

// waivers gives the waivers that the selected findings refer to.
func (r *explainReport) waivers() []conformance.AppliedWaiver {
	waivers := []conformance.AppliedWaiver{}

	for i := range r.run.Exceptions.Waivers {
		waiver := &r.run.Exceptions.Waivers[i]
		if slices.ContainsFunc(r.selected, func(f *conformance.Finding) bool { return f.DispositionRef != nil && *f.DispositionRef == waiver.ID }) {
			waivers = append(waivers, *waiver)
		}
	}

	return waivers
}

func explainJSON(app *Context, report *explainReport) error {
	body := map[string]any{
		"schemaVersion": conformance.ResultSchemaVersion,
		"rule":          report.rule,
		"findings":      report.findings(),
		"waivers":       report.waivers(),
	}

	if crossings := trustJSON(report.run, report.selected); crossings != nil {
		body["trust"] = crossings
	}

	if candidates := explainCandidates(report.run, report.rule, report.selected); len(candidates) > 0 {
		body["candidates"] = candidates
	}

	text, err := canonjson.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode explanation: %w", err)
	}

	return app.WriteOutput(string(text), "")
}
