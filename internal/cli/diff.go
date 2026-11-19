package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/diffs"
	"github.com/inclaro/concordia/internal/intent"
)

// Kinds of --kind and of the sections of the diff.
const (
	diffKindIntent   = "intent"
	diffKindObserved = "observed"
	diffKindBindings = "bindings"
	diffKindFindings = "findings"
	diffKindAll      = "all"
)

// Kinds of a side.
const (
	sideWorktree = "worktree"
	sideGitRef   = "git-ref"
	sideResult   = "result"
	sideBundle   = "bundle"

	diffSchemaVersion = "1.0"
	diffFpLength      = 12
	percent           = 100
)

type diffOptions struct {
	from string
	to   string
	kind string
}

// diffSide is one resolved side of a diff. A nil model, bindings or run means that the side does not give that data.
type diffSide struct {
	model    *intent.ArchitectureModel
	bindings *[]binding.Binding
	run      *conformance.Run
	kind     string
	value    string
}

type diffEndpoint struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type diffSkipped struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

type diffReport struct {
	Intent        *diffs.IntentDiff   `json:"intent,omitempty"`
	Bindings      *diffs.IntentDiff   `json:"bindings,omitempty"`
	Observed      *diffs.ObservedDiff `json:"observed,omitempty"`
	Findings      *diffs.FindingDiff  `json:"findings,omitempty"`
	Coverage      *diffs.CoverageDiff `json:"coverage,omitempty"`
	Waivers       *diffs.WaiverDiff   `json:"waivers,omitempty"`
	From          diffEndpoint        `json:"from"`
	To            diffEndpoint        `json:"to"`
	SchemaVersion string              `json:"schemaVersion"`
	Skipped       []diffSkipped       `json:"skipped"`
}

func newDiffCmd(host *Host) *cobra.Command {
	opts := diffOptions{from: refHead, to: sideWorktree, kind: diffKindAll}

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare the architecture, bindings, observed state and findings of two states",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, diffHandler(ctx, app, &opts)
		}),
	}

	cmd.Flags().StringVar(&opts.from, "from", opts.from, "worktree, a git ref, a results file or a bundle (.tar.gz)")
	cmd.Flags().StringVar(&opts.to, "to", opts.to, "worktree, a git ref, a results file or a bundle (.tar.gz)")
	addChoiceFlag(cmd, false, &opts.kind, "kind", "what to compare",
		diffKindAll, diffKindIntent, diffKindObserved, diffKindBindings, diffKindFindings)

	return cmd
}

func (s *diffSide) has(kind string) bool {
	switch kind {
	case diffKindIntent:
		return s.model != nil
	case diffKindBindings:
		return s.bindings != nil
	default:
		return s.run != nil
	}
}

func diffHandler(ctx context.Context, app *Context, opts *diffOptions) error {
	format := app.Opts.Format
	if format == "" {
		format = modelFormatPretty
	}

	if format != modelFormatPretty && format != modelFormatJSON {
		return diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unsupported format '%s' (pretty, json)", format)))
	}

	loader := &diffLoader{app: app}
	defer loader.cleanup()

	report, err := buildDiffReport(ctx, loader, opts)
	if err != nil {
		return err
	}

	if format == modelFormatJSON {
		text, err := canonjson.Marshal(report)
		if err != nil {
			return fmt.Errorf("encode diff: %w", err)
		}

		app.Out(string(text) + "\n")

		return nil
	}

	app.Out(prettyDiff(report))

	return nil
}

// buildDiffReport resolves the two sides and compares what both give. A kind that a side does not give is skipped,
// or an error when the kind was asked for.
func buildDiffReport(ctx context.Context, loader *diffLoader, opts *diffOptions) (*diffReport, error) {
	from, err := loader.resolve(ctx, opts.from)
	if err != nil {
		return nil, err
	}

	dest, err := loader.resolve(ctx, opts.to)
	if err != nil {
		return nil, err
	}

	report := &diffReport{
		SchemaVersion: diffSchemaVersion, Skipped: []diffSkipped{},
		From: diffEndpoint{Kind: from.kind, Value: opts.from}, To: diffEndpoint{Kind: dest.kind, Value: opts.to},
	}

	kinds := []string{diffKindIntent, diffKindBindings, diffKindObserved, diffKindFindings}
	if opts.kind != diffKindAll {
		kinds = []string{opts.kind}
	}

	for _, kind := range kinds {
		if from.has(kind) && dest.has(kind) {
			fillDiff(report, kind, from, dest)

			continue
		}

		missing := from
		if from.has(kind) {
			missing = dest
		}

		reason := "not available for " + missing.kind
		if opts.kind != diffKindAll {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("--kind %s is %s", kind, reason)))
		}

		report.Skipped = append(report.Skipped, diffSkipped{Kind: kind, Reason: reason})
	}

	return report, nil
}

func fillDiff(rep *diffReport, kind string, from, dest *diffSide) {
	switch kind {
	case diffKindIntent:
		d := diffs.Intent(from.model, dest.model)
		rep.Intent = &d
	case diffKindBindings:
		d := diffs.Bindings(*from.bindings, *dest.bindings)
		rep.Bindings = &d
	case diffKindObserved:
		d := diffs.Observed(from.run, dest.run)
		rep.Observed = &d
	default:
		f, c, w := diffs.Findings(from.run, dest.run), diffs.Coverage(from.run, dest.run), diffs.Waivers(from.run, dest.run)
		rep.Findings, rep.Coverage, rep.Waivers = &f, &c, &w
	}
}

func prettyDiff(report *diffReport) string {
	var out strings.Builder

	fmt.Fprintf(&out, "Diff %s (%s) -> %s (%s)\n", report.From.Value, report.From.Kind, report.To.Value, report.To.Kind)

	prettyChangesSection(&out, "Intent", report.Intent)
	prettyChangesSection(&out, "Bindings", report.Bindings)

	if report.Observed != nil {
		prettyObservedSection(&out, report.Observed)
	}

	prettyFindings(&out, report)

	for _, s := range report.Skipped {
		fmt.Fprintf(&out, "\nSkipped %s: %s\n", s.Kind, s.Reason)
	}

	return out.String()
}

// prettyChangesSection writes the added, removed and changed elements of a section; a nil diff writes nothing.
func prettyChangesSection(out *strings.Builder, title string, diff *diffs.IntentDiff) {
	if diff == nil {
		return
	}

	fmt.Fprintf(out, "\n%s\n", title)

	for _, c := range diff.Added {
		fmt.Fprintf(out, "+ %s %s\n", c.Type, c.ID)
	}

	for _, c := range diff.Removed {
		fmt.Fprintf(out, "- %s %s\n", c.Type, c.ID)
	}

	for _, c := range diff.Changed {
		fmt.Fprintf(out, "~ %s %s\n", c.Type, c.ID)
	}
}

func prettyObservedSection(out *strings.Builder, observed *diffs.ObservedDiff) {
	out.WriteString("\nObserved\n")

	for _, p := range observed.Added {
		fmt.Fprintf(out, "+ pair %s -> %s (%s)\n", p.From, p.To, p.Classification)
	}

	for _, p := range observed.Removed {
		fmt.Fprintf(out, "- pair %s -> %s (%s)\n", p.From, p.To, p.Classification)
	}

	for _, p := range observed.Changed {
		fmt.Fprintf(out, "~ pair %s -> %s (%s -> %s)\n", p.From, p.To, p.FromClassification, p.ToClassification)
	}
}

func prettyFindings(out *strings.Builder, report *diffReport) {
	if report.Findings != nil {
		prettyFindingSection(out, report.Findings)
	}

	if report.Coverage != nil {
		prettyCoverageSection(out, report.Coverage)
	}

	if report.Waivers != nil {
		prettyWaiverSection(out, report.Waivers)
	}
}

func prettyFindingSection(out *strings.Builder, findings *diffs.FindingDiff) {
	out.WriteString("\nFindings\n")

	line := func(word string, refs []diffs.FindingRef) {
		for _, ref := range refs {
			where := ""
			if ref.Location != nil {
				where = fmt.Sprintf(" %s:%d", ref.Location.Path, ref.Location.Line)
			}

			fmt.Fprintf(out, "%s %s %s %s%s\n", word, ref.RuleID, fp12(ref.Fingerprint), ref.Message, where)
		}
	}

	line("NEW", findings.New)
	line("RESOLVED", findings.Resolved)
	line("REINTRODUCED", findings.Reintroduced)

	for _, c := range findings.Changed {
		fmt.Fprintf(out, "CHANGED %s %s %s -> %s\n", c.RuleID, fp12(c.Fingerprint), c.From, c.To)
	}
}

func prettyCoverageSection(out *strings.Builder, coverage *diffs.CoverageDiff) {
	out.WriteString("\nCoverage\n")

	for _, x := range coverage.Regressions {
		fmt.Fprintf(out, "REGRESSION %s %.1f%% -> %.1f%%\n", x.RuleID, x.From*percent, x.To*percent)
	}

	for _, x := range coverage.Improvements {
		fmt.Fprintf(out, "IMPROVEMENT %s %.1f%% -> %.1f%%\n", x.RuleID, x.From*percent, x.To*percent)
	}
}

func prettyWaiverSection(out *strings.Builder, waivers *diffs.WaiverDiff) {
	out.WriteString("\nWaivers\n")

	for _, id := range waivers.Added {
		fmt.Fprintf(out, "+ waiver %s\n", id)
	}

	for _, id := range waivers.Removed {
		fmt.Fprintf(out, "- waiver %s\n", id)
	}

	for _, id := range waivers.Expired {
		fmt.Fprintf(out, "EXPIRED waiver %s\n", id)
	}
}

func fp12(fingerprint string) string {
	h := hexOf(fingerprint)
	if len(h) > diffFpLength {
		h = h[:diffFpLength]
	}

	return h
}
