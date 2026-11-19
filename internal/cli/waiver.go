package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/workspace"
)

const resultsFile = "build/concordia/results.json"

var fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type waiverAddOptions struct {
	finding     string
	owner       string
	reason      string
	expires     string
	issue       string
	rule        string
	subject     string
	environment string
	obligation  string
	approver    string
	evidence    []string
}

type waiverValidateOptions struct {
	at                 string
	failExpiringWithin string
}

func newWaiverCmd(host *Host) *cobra.Command {
	waiver := &cobra.Command{Use: "waiver", Short: "Manage waivers"}

	var addOpts waiverAddOptions

	add := &cobra.Command{
		Use:   "add",
		Short: "Add a waiver",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(_ context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, waiverAdd(app, &addOpts)
		}),
	}
	add.Flags().StringVar(&addOpts.finding, "finding", "", "fingerprint of the finding (sha256:...); not with a scope flag")
	add.Flags().StringVar(&addOpts.owner, "owner", "", "owner of the waiver")
	add.Flags().StringVar(&addOpts.reason, "reason", "", "reason for the waiver")
	add.Flags().StringVar(&addOpts.expires, "expires", "", "expiry date (YYYY-MM-DD)")
	add.Flags().StringVar(&addOpts.issue, "issue", "", "issue reference")
	add.Flags().StringVar(&addOpts.rule, "rule", "", "rule ID (a finding waiver: default from results.json; a scoped waiver: required)")
	add.Flags().StringVar(&addOpts.subject, "subject", "", "scoped waiver: CALM ID of the subject")
	add.Flags().StringVar(&addOpts.environment, "environment", "", "scoped waiver: environment")
	add.Flags().StringVar(&addOpts.obligation, "obligation", "", "scoped waiver: full obligation ID")
	add.Flags().StringVar(&addOpts.approver, "approver", "", "scoped waiver: approver as identity@authority")
	add.Flags().StringArrayVar(&addOpts.evidence, "evidence", nil, "scoped waiver: compensating evidence file in the workspace (repeatable)")

	for _, name := range []string{"owner", "reason", "expires"} {
		_ = add.MarkFlagRequired(name)
	}

	var validateOpts waiverValidateOptions

	validate := &cobra.Command{
		Use:   cmdValidate,
		Short: "Validate waivers",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return waiverValidate(ctx, app, &validateOpts)
		}),
	}
	validate.Flags().StringVar(&validateOpts.at, "at", "", "evaluate at this date")
	validate.Flags().StringVar(&validateOpts.failExpiringWithin, "fail-expiring-within", "", "error when a waiver expires within N days")

	waiver.AddCommand(add, validate)

	return waiver
}

type resultFinding struct {
	Fingerprint  string `json:"fingerprint"`
	RuleID       string `json:"ruleId"`
	Subject      string `json:"subject"`
	Environment  string `json:"environment"`
	ObligationID string `json:"obligationId"`
}

// resultsPath gives the JSON results file that the workspace configures (report.outputs), or the default.
func resultsPath(wsp *workspace.Workspace) string { return wsp.ResolveInRoot(resultsRel(wsp)) }

// resultsRel is resultsPath as the workspace writes it: relative to the workspace root, with "/".
func resultsRel(wsp *workspace.Workspace) string {
	for _, o := range wsp.Report.Outputs {
		if o.Format == workspace.ReportJSON {
			return o.Path
		}
	}

	return workspace.DefaultReportPath
}

// readResultFindings reads the findings of the last results file. It gives ok=false when there is none.
func readResultFindings(wsp *workspace.Workspace) ([]resultFinding, bool) {
	data, err := os.ReadFile(resultsPath(wsp))
	if err != nil {
		return nil, false
	}

	var doc struct {
		Findings []resultFinding `json:"findings"`
	}

	if json.Unmarshal(data, &doc) != nil {
		return nil, false
	}

	return doc.Findings, true
}

// waiverScope gives the scope that the flags name. It is the zero scope when no scope flag is set.
func (opts *waiverAddOptions) waiverScope() exceptions.Scope {
	return exceptions.Scope{Subject: opts.subject, Environment: opts.environment, Obligation: opts.obligation}
}

// addFlagsError gives what is wrong with the flag combination of `waiver add`, or "".
func addFlagsError(opts *waiverAddOptions, scoped bool) string {
	hasFinding := opts.finding != ""

	if scoped {
		if hasFinding {
			return "--finding cannot be used with --subject, --environment, or --obligation: a waiver has a finding or a scope"
		}

		return ""
	}

	if !hasFinding {
		return "--finding, or one of --subject, --environment, and --obligation, is required"
	}

	if opts.approver != "" || len(opts.evidence) > 0 {
		return "--approver and --evidence need a scope: use --subject, --environment, or --obligation"
	}

	return ""
}

// validateAddOptions checks the flag combination of `waiver add` and the expiry date.
func validateAddOptions(app *Context, opts *waiverAddOptions, scoped bool) error {
	bad := func(detail string) error { return diag.Fail(diag.EXC002, diag.Detail(detail)) }

	if detail := addFlagsError(opts, scoped); detail != "" {
		return bad(detail)
	}

	day, ok := exceptions.ParseDay(opts.expires)
	if !ok {
		return bad("--expires must be YYYY-MM-DD")
	}

	if !day.AddDate(0, 0, 1).After(app.Now()) {
		return bad("--expires must be in the future")
	}

	return nil
}

// readExistingWaivers reads the waivers file; it gives none when the file does not exist.
func readExistingWaivers(path string) ([]exceptions.Waiver, error) {
	text, err := os.ReadFile(path) //nolint:gosec // the path comes from the workspace file.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read waivers: %w", err)
	}

	existing, err := exceptions.ParseWaivers(string(text), path)
	if err != nil {
		return nil, fmt.Errorf("read waivers: %w", err)
	}

	return existing, nil
}

// buildWaiverEntry makes the waiver that the flags describe and completes it from the workspace.
func buildWaiverEntry(wsp *workspace.Workspace, opts *waiverAddOptions, scope *exceptions.Scope, scoped bool) (exceptions.Waiver, error) {
	entry := exceptions.Waiver{Rule: opts.rule, Owner: opts.owner, Reason: opts.reason, Expires: opts.expires, Issue: opts.issue}

	if scoped {
		err := fillScoped(wsp, opts, scope, &entry)

		return entry, err
	}

	err := fillFinding(wsp, opts, &entry)

	return entry, err
}

// persistWaiver gives the entry its ID, validates a scoped one and appends it to the waivers file.
func persistWaiver(app *Context, wsp *workspace.Workspace, entry *exceptions.Waiver) error {
	path := wsp.ResolveInRoot(wsp.Exceptions.Waivers)

	existing, err := readExistingWaivers(path)
	if err != nil {
		return err
	}

	entry.ID = exceptions.NextWaiverID(existing)

	if entry.Scope != nil {
		if err = exceptions.ValidateWaiver(entry, exceptions.WithMaxExpiry(wsp.Exceptions.MaxExpiryDays, app.Now())); err != nil {
			return fmt.Errorf("new waiver: %w", err)
		}
	}

	if err = exceptions.AppendWaiver(path, entry); err != nil {
		return fmt.Errorf("add waiver: %w", err)
	}

	return nil
}

func waiverAdd(app *Context, opts *waiverAddOptions) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	scope := opts.waiverScope()
	scoped := scope != (exceptions.Scope{})

	if err := validateAddOptions(app, opts, scoped); err != nil {
		return err
	}

	entry, err := buildWaiverEntry(wsp, opts, &scope, scoped)
	if err != nil {
		return err
	}

	if err := persistWaiver(app, wsp, &entry); err != nil {
		return err
	}

	app.Err(fmt.Sprintf("waiver %s added to %s\n", entry.ID, wsp.Exceptions.Waivers))

	return nil
}

// fillFinding completes a finding waiver: it checks the fingerprint and finds the rule in the last results when
// --rule is not set.
func fillFinding(wsp *workspace.Workspace, opts *waiverAddOptions, entry *exceptions.Waiver) error {
	bad := func(detail string) error { return diag.Fail(diag.EXC002, diag.Detail(detail)) }

	if !fingerprintPattern.MatchString(opts.finding) {
		return bad("--finding must be a sha256: fingerprint")
	}

	entry.Finding = opts.finding

	if entry.Rule != "" {
		return nil
	}

	findings, found := readResultFindings(wsp)
	if !found {
		return bad("--rule is required: " + resultsFile + " not found")
	}

	for _, f := range findings {
		if f.Fingerprint == opts.finding {
			entry.Rule = f.RuleID

			break
		}
	}

	if entry.Rule == "" {
		return bad("--rule is required: fingerprint not in " + resultsFile)
	}

	return nil
}

// fillScoped completes a scoped waiver: the scope, the approver from --approver (identity@authority, split at the
// last "@" because an identity may be an e-mail address), and the compensating evidence with the digest of each file.
func fillScoped(wsp *workspace.Workspace, opts *waiverAddOptions, scope *exceptions.Scope, entry *exceptions.Waiver) error {
	bad := func(detail string) error { return diag.Fail(diag.EXC002, diag.Detail(detail)) }

	if entry.Rule == "" {
		return bad("--rule is required for a scoped waiver")
	}

	at := strings.LastIndex(opts.approver, "@")
	if at <= 0 || at == len(opts.approver)-1 {
		return bad("--approver is required for a scoped waiver and must be identity@authority")
	}

	entry.Scope = scope
	entry.Approver = &exceptions.Approver{Identity: opts.approver[:at], Authority: opts.approver[at+1:]}

	for _, ref := range opts.evidence {
		rel := filepath.ToSlash(filepath.Clean(ref))
		if !fs.ValidPath(rel) {
			return bad("--evidence " + ref + " must be a file in the workspace")
		}

		sum, err := digest.OfFile(wsp.ResolveInRoot(rel))
		if err != nil {
			return bad("--evidence " + ref + ": cannot read the file")
		}

		entry.CompensatingEvidence = append(entry.CompensatingEvidence, exceptions.Evidence{Path: rel, SHA256: sum})
	}

	return nil
}

// parseValidateAt reads --at; it is the current time when the flag is not set.
func parseValidateAt(app *Context, at string) (time.Time, error) {
	if at == "" {
		return app.Now(), nil
	}

	parsed, err := clock.Parse(at)
	if err != nil {
		return app.Now(), diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("invalid --at '%s'", at)))
	}

	return parsed, nil
}

// validDays says whether n is a number of days: not NaN and not negative.
func validDays(n float64) bool { return !math.IsNaN(n) && n >= 0 }

// parseValidateDays reads --fail-expiring-within, which is not empty.
func parseValidateDays(text string) (float64, error) {
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || !validDays(n) {
		return 0, diag.Fail(diag.WRK010, diag.Detail("--fail-expiring-within must be a number of days"))
	}

	return n, nil
}

// parseValidateOptions reads --at and --fail-expiring-within. A nil days turns the expiry error off.
func parseValidateOptions(app *Context, opts *waiverValidateOptions) (when time.Time, days *float64, err error) {
	when, err = parseValidateAt(app, opts.at)
	if err != nil {
		return when, nil, err
	}

	if opts.failExpiringWithin == "" {
		return when, nil, nil
	}

	n, err := parseValidateDays(opts.failExpiringWithin)
	if err != nil {
		return when, nil, err
	}

	return when, &n, nil
}

// staleWaiverDiagnostics gives CO-EXC-003 for each finding waiver whose finding is not in the last results, and
// CO-EXC-010 for each scoped waiver that covers none of them.
func staleWaiverDiagnostics(wsp *workspace.Workspace, uri string, waivers []exceptions.Waiver) []diag.Diagnostic {
	findings, found := readResultFindings(wsp)
	if !found {
		return nil
	}

	keys := make([]exceptions.Key, len(findings))
	for i, f := range findings {
		keys[i] = exceptions.Key{Fingerprint: f.Fingerprint, RuleID: f.RuleID, Subject: f.Subject, Environment: f.Environment, ObligationID: f.ObligationID}
	}

	var out []diag.Diagnostic

	for i := range waivers {
		covers := func(k exceptions.Key) bool { return waivers[i].Covers(&k) }

		var hit bool

		for _, k := range keys {
			if hit = covers(k); hit {
				break
			}
		}

		if !hit {
			d := exceptions.OrphanDiagnostic(&waivers[i])
			d.Source = &diag.Source{URI: uri}
			out = append(out, d)
		}
	}

	return out
}

// verifyScopedDiagnostics verifies the approver of each scoped waiver at the trust anchor and gives the warning
// CO-EXC-009 for each that is not verified, so `waiver validate` shows what a run would not apply.
func verifyScopedDiagnostics(ctx context.Context, app *Context, wsp *workspace.Workspace, waivers []exceptions.Waiver) ([]diag.Diagnostic, error) {
	path := wsp.ResolveInRoot(wsp.Exceptions.Waivers)

	results, err := conformance.VerifyScopedWaivers(ctx, &conformance.WaiverVerifyOptions{
		Workspace: wsp, Git: app.Git(wsp.Root), Getenv: app.IO.Getenv, Offline: app.Opts.Offline,
	}, path, waivers)
	if err != nil {
		return nil, fmt.Errorf("verify approvers: %w", err)
	}

	var out []diag.Diagnostic

	for i := range results {
		if results[i].Diagnostic != nil {
			out = append(out, *results[i].Diagnostic)
		}
	}

	return out, nil
}

// loadValidateWaivers reads and parses the waivers file of the workspace at the given time.
func loadValidateWaivers(wsp *workspace.Workspace, path string, when time.Time) ([]exceptions.Waiver, error) {
	text, err := os.ReadFile(path) //nolint:gosec // the path comes from the workspace file.
	if err != nil {
		return nil, fmt.Errorf("read waivers: %w", err)
	}

	waivers, err := exceptions.ParseWaivers(string(text), path, exceptions.WithMaxExpiry(wsp.Exceptions.MaxExpiryDays, when))
	if err != nil {
		return nil, fmt.Errorf("parse waivers: %w", err)
	}

	return waivers, nil
}

// isEscalatedWaiverCode says whether a diagnostic code is an error: CO-EXC-006 always, CO-EXC-007 when the expiry
// error is on.
func isEscalatedWaiverCode(code string, expiryError bool) bool {
	if code == diag.EXC006 {
		return true
	}

	return code == diag.EXC007 && expiryError
}

// reportWaiverDiagnostics prints the diagnostics and says whether one of them is an error.
func reportWaiverDiagnostics(app *Context, diags []diag.Diagnostic) bool {
	hasError := false

	for _, d := range diags {
		app.Err(diag.FormatDiagnostic(d) + "\n")

		hasError = hasError || d.Severity == diag.SeverityError
	}

	return hasError
}

func waiverValidate(ctx context.Context, app *Context, opts *waiverValidateOptions) (int, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return diag.Internal, err
	}

	when, days, err := parseValidateOptions(app, opts)
	if err != nil {
		return diag.InvalidInput, err
	}

	path := wsp.ResolveInRoot(wsp.Exceptions.Waivers)

	waivers, err := loadValidateWaivers(wsp, path, when)
	if err != nil {
		return diag.Internal, err
	}

	diags := exceptions.Diagnostics(waivers, when, wsp.Exceptions.Waivers, days)

	for i := range diags {
		if isEscalatedWaiverCode(diags[i].Code, days != nil) {
			diags[i].Severity = diag.SeverityError
		}
	}

	diags = append(diags, staleWaiverDiagnostics(wsp, wsp.Exceptions.Waivers, waivers)...)

	approvers, err := verifyScopedDiagnostics(ctx, app, wsp, waivers)
	if err != nil {
		return diag.Internal, err
	}

	hasError := reportWaiverDiagnostics(app, append(diags, approvers...))

	app.Err(fmt.Sprintf("%d waiver(s) checked\n", len(waivers)))

	if hasError {
		return diag.InvalidInput, nil
	}

	return diag.OK, nil
}
