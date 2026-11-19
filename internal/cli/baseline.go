package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
)

const defaultBaselineSeverities = "warning,error"

var expiresPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// baselineFinding is the part of a finding that the baseline commands use.
type baselineFinding struct {
	Owner       *string `json:"owner"`
	Fingerprint string  `json:"fingerprint"`
	RuleID      string  `json:"ruleId"`
	Severity    string  `json:"severity"`
	Disposition string  `json:"disposition"`
	// Enforcement is the mode of the finding. A `shadow` finding is recorded in the results only: it is never accepted into a baseline.
	Enforcement string `json:"enforcement"`
}

// baselineRun is the part of a check result that the baseline commands use.
type baselineRun struct {
	Run struct {
		SourceRevision  *string `json:"sourceRevision"`
		GraphGeneration string  `json:"graphGeneration"`
	} `json:"run"`
	Inputs struct {
		Policies []struct {
			Digest string `json:"digest"`
		} `json:"policies"`
	} `json:"inputs"`
	Exceptions struct {
		ResolvedBaselineEntries []string `json:"resolvedBaselineEntries"`
	} `json:"exceptions"`
	Findings []baselineFinding `json:"findings"`
}

func (r *baselineRun) createdFrom() exceptions.CreatedFrom {
	digests := make([]string, 0, len(r.Inputs.Policies))
	for _, p := range r.Inputs.Policies {
		digests = append(digests, p.Digest)
	}

	return exceptions.CreatedFrom{
		SourceRevision:  r.Run.SourceRevision,
		GraphGeneration: r.Run.GraphGeneration,
		PolicyDigest:    exceptions.PolicyDigestOf(digests),
	}
}

// loadBaselineRun reads a results file, or runs the check pipeline (without reports) when from is "".
func loadBaselineRun(ctx context.Context, app *Context, from string) (*baselineRun, error) {
	data, err := baselineResultsJSON(ctx, app, from)
	if err != nil {
		return nil, err
	}

	var out baselineRun
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("cannot read results %s: %s", from, err)))
	}

	return &out, nil
}

// baselineResultsJSON is the JSON of the results file, or of a run of the check pipeline when from is "".
func baselineResultsJSON(ctx context.Context, app *Context, from string) ([]byte, error) {
	if from != "" {
		data, err := os.ReadFile(app.Abs(from))
		if err != nil {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("cannot read results %s: %s", from, err)))
		}

		return data, nil
	}

	run, err := runPipeline(ctx, app)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(run)
	if err != nil {
		return nil, fmt.Errorf("encode run: %w", err)
	}

	return data, nil
}

func newBaselineCmd(host *Host) *cobra.Command {
	baseline := &cobra.Command{Use: "baseline", Short: "Create and update the findings baseline"}

	var create baselineCreateOptions

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Record current findings as the baseline",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, baselineCreate(ctx, app, &create)
		}),
	}
	createCmd.Flags().StringVar(&create.from, "from", "", "use an existing results file")
	createCmd.Flags().StringVar(&create.output, "output", "", "baseline file (default: exceptions.baseline)")
	createCmd.Flags().StringVar(&create.severity, "severity", defaultBaselineSeverities, "severities to include")
	createCmd.Flags().StringVar(&create.expires, "expires", "", "expiry date for entries")
	createCmd.Flags().BoolVar(&create.force, "force", false, "overwrite an existing baseline")

	var update baselineUpdateOptions

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Update the baseline",
		// `--accept a b` takes several values: the extra values are positional.
		Args: cobra.ArbitraryArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			if len(args) > 0 {
				if len(update.accept) == 0 {
					return diag.InvalidInput, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unexpected argument '%s'", args[0])))
				}

				update.accept = append(slices.Clone(update.accept), args...)
			}

			return diag.OK, baselineUpdate(ctx, app, &update)
		}),
	}
	updateCmd.Flags().StringVar(&update.from, "from", "", "use an existing results file")
	updateCmd.Flags().BoolVar(&update.pruneResolved, "prune-resolved", false, "remove resolved entries")
	updateCmd.Flags().StringArrayVar(&update.accept, "accept", nil, "accept current active findings (fingerprint; repeatable)")
	updateCmd.Flags().StringVar(&update.reason, "reason", "", "reason for the accepted entries")
	updateCmd.Flags().StringVar(&update.owner, "owner", "", "owner of the accepted entries")

	baseline.AddCommand(createCmd, updateCmd)

	return baseline
}

type baselineCreateOptions struct {
	from     string
	output   string
	severity string
	expires  string
	force    bool
}

func baselineCreate(ctx context.Context, app *Context, opts *baselineCreateOptions) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	out := wsp.ResolveInRoot(wsp.Exceptions.Baseline)
	shown := wsp.Exceptions.Baseline

	if opts.output != "" {
		out, shown = app.Abs(opts.output), opts.output
	}

	if err := checkBaselineWritable(out, shown, opts.force); err != nil {
		return err
	}

	severities, err := parseSeverities(opts.severity)
	if err != nil {
		return err
	}

	if opts.expires != "" && !expiresPattern.MatchString(opts.expires) {
		return diag.Fail(diag.WRK010, diag.Detail("--expires must be a date"))
	}

	run, err := loadBaselineRun(ctx, app, opts.from)
	if err != nil {
		return err
	}

	entries := baselineEntries(run, severities, opts.expires, clock.ISO(app.Now()))

	base := exceptions.Baseline{SchemaVersion: exceptions.BaselineVersion, CreatedFrom: run.createdFrom(), Findings: entries}
	if err := exceptions.WriteBaseline(out, &base); err != nil {
		return err //nolint:wrapcheck // a *diag.Error or a described write error.
	}

	app.Err(fmt.Sprintf("baseline written: %d finding(s) -> %s\n", len(entries), out))

	return nil
}

// checkBaselineWritable rejects an existing baseline file unless it may be overwritten.
func checkBaselineWritable(out, shown string, force bool) error {
	_, statErr := os.Stat(out)
	if statErr == nil {
		if force {
			return nil
		}

		return diag.Fail(diag.WRK005, diag.Params{keyPath: shown}, diag.WithExitCode(diag.InvalidInput), diag.WithSeverity(diag.SeverityError))
	}

	if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("stat baseline: %w", statErr)
	}

	return nil
}

// parseSeverities reads the comma-separated `--severity` list.
func parseSeverities(list string) (map[string]bool, error) {
	severities := map[string]bool{}

	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if !slices.Contains(baselineSeverityNames, name) {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("invalid severity '%s'", name)))
		}

		severities[name] = true
	}

	return severities, nil
}

// baselineSeverityNames are the severities that `--severity` accepts.
var baselineSeverityNames = []string{statusError, "warning", "info"}

// baselineEntries makes one entry for each finding with a wanted severity that is not waived.
func baselineEntries(run *baselineRun, severities map[string]bool, expires, createdAt string) []exceptions.BaselineEntry {
	byFingerprint := map[string]exceptions.BaselineEntry{}
	order := []string{}

	for i := range run.Findings {
		finding := &run.Findings[i]
		if !finding.isBaselineCandidate(severities) {
			continue
		}

		if _, seen := byFingerprint[finding.Fingerprint]; !seen {
			order = append(order, finding.Fingerprint)
		}

		byFingerprint[finding.Fingerprint] = finding.baselineEntry(expires, createdAt)
	}

	entries := make([]exceptions.BaselineEntry, 0, len(order))
	for _, fp := range order {
		entries = append(entries, byFingerprint[fp])
	}

	return entries
}

// isBaselineCandidate says whether a finding of a wanted severity may enter a baseline: it is neither waived nor shadow.
func (f *baselineFinding) isBaselineCandidate(severities map[string]bool) bool {
	return severities[f.Severity] && f.Disposition != "waived" && f.Enforcement != "shadow"
}

// baselineEntry is the baseline entry that records the finding as inherited.
func (f *baselineFinding) baselineEntry(expires, createdAt string) exceptions.BaselineEntry {
	owner := "unassigned"
	if f.Owner != nil {
		owner = *f.Owner
	}

	return exceptions.BaselineEntry{
		Fingerprint: f.Fingerprint,
		RuleID:      f.RuleID,
		Owner:       owner,
		Reason:      "Inherited before enforcement",
		CreatedAt:   createdAt,
		ExpiresAt:   expires,
	}
}

type baselineUpdateOptions struct {
	from          string
	reason        string
	owner         string
	accept        []string
	pruneResolved bool
}

// inCI says whether the CI environment variable is set to a true value.
func inCI(app *Context) bool {
	ci := app.IO.Getenv("CI")

	return ci != "" && ci != "0" && !strings.EqualFold(ci, "false")
}

func baselineUpdate(ctx context.Context, app *Context, opts *baselineUpdateOptions) error {
	if err := checkBaselineAcceptAllowed(app, opts); err != nil {
		return err
	}

	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	path := wsp.ResolveInRoot(wsp.Exceptions.Baseline)

	base, err := exceptions.ReadBaseline(path)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	run, err := loadBaselineRun(ctx, app, opts.from)
	if err != nil {
		return err
	}

	findings := slices.Clone(base.Findings)

	if opts.pruneResolved {
		findings = slices.DeleteFunc(findings, func(e exceptions.BaselineEntry) bool {
			return slices.Contains(run.Exceptions.ResolvedBaselineEntries, e.Fingerprint)
		})
	}

	findings, err = acceptBaselineFindings(findings, run, opts, clock.ISO(app.Now()))
	if err != nil {
		return err
	}

	updated := exceptions.Baseline{SchemaVersion: base.SchemaVersion, CreatedFrom: run.createdFrom(), Findings: findings}
	if err := writeUpdatedBaseline(path, &updated); err != nil {
		return err
	}

	app.Err(fmt.Sprintf("baseline updated: %d finding(s)\n", len(findings)))

	return nil
}

// checkBaselineAcceptAllowed rejects `--accept` in CI and without the reason and owner of the accepted entries.
func checkBaselineAcceptAllowed(app *Context, opts *baselineUpdateOptions) error {
	if len(opts.accept) == 0 {
		return nil
	}

	if inCI(app) {
		return diag.Fail(diag.EXC008, nil)
	}

	if opts.reason == "" || opts.owner == "" {
		return diag.Fail(diag.WRK010, diag.Detail("--accept requires --reason and --owner"))
	}

	return nil
}

// acceptBaselineFindings adds an entry for each `--accept` fingerprint that is a current active finding and not yet
// in the baseline.
func acceptBaselineFindings(findings []exceptions.BaselineEntry, run *baselineRun, opts *baselineUpdateOptions, createdAt string) ([]exceptions.BaselineEntry, error) {
	for _, fingerprint := range opts.accept {
		index := slices.IndexFunc(run.Findings, func(f baselineFinding) bool {
			return f.Fingerprint == fingerprint && f.Disposition == "active" && f.Enforcement != "shadow"
		})
		if index < 0 {
			return nil, diag.Fail(diag.EXC001, diag.Detail(fmt.Sprintf("cannot accept %s: not a current active finding", fingerprint)))
		}

		if slices.ContainsFunc(findings, func(e exceptions.BaselineEntry) bool { return e.Fingerprint == fingerprint }) {
			continue
		}

		findings = append(findings, exceptions.BaselineEntry{
			Fingerprint: fingerprint, RuleID: run.Findings[index].RuleID, Owner: opts.owner, Reason: opts.reason, CreatedAt: createdAt,
		})
	}

	return findings, nil
}

// writeUpdatedBaseline checks that the updated baseline parses, and writes it.
func writeUpdatedBaseline(path string, updated *exceptions.Baseline) error {
	text, err := json.Marshal(updated)
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}

	if _, err = exceptions.ParseBaselineJSON(text, path); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := exceptions.WriteBaseline(path, updated); err != nil {
		return err //nolint:wrapcheck // a *diag.Error or a described write error.
	}

	return nil
}
