package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// assessmentStatusOptions are the flags of `assessment status`.
type assessmentStatusOptions struct {
	at         string
	assessment string
	json       bool
}

// newAssessmentStatusCmd builds `assessment status`.
func newAssessmentStatusCmd(host *Host) *cobra.Command {
	var opts assessmentStatusOptions

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the currency of every assessment version and required assessment",
		Long: "Evaluate the currency of the assessments exactly as `check` does: the state (current, review-required, " +
			"expired, revoked, or unknown), the reasons, the triggers with their explanation paths, the approval that " +
			"the result rests on, the records that were not applied, and the digest-impact report of the current digest " +
			"contract. Expiry and revocation are judged at the evaluation time, which the output states; --at sets " +
			"another time. The command is read-only: it writes no file and it never approves.",
		Args: cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return assessmentStatus(ctx, app, &opts)
		}),
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.json, "json", false, "machine-readable output")
	flags.StringVar(&opts.at, "at", "", "evaluation time (YYYY-MM-DD or RFC 3339; default: now, or CONCORDIA_NOW)")
	flags.StringVar(&opts.assessment, "assessment", "", "show only this assessment id")

	return cmd
}

// assessmentStatusReport is the machine-readable result of `assessment status`.
type assessmentStatusReport struct {
	// EvaluatedAt is the time that expiry was judged at.
	EvaluatedAt       string                      `json:"evaluatedAt"`
	ContractVersion   string                      `json:"contractVersion"`
	CapabilityProfile string                      `json:"capabilityProfile"`
	DigestImpact      impact.DigestImpactReport   `json:"digestImpact"`
	Assessments       []assessment.Result         `json:"assessments"`
	Required          []assessment.RequiredResult `json:"required"`
	Verification      []statusVerification        `json:"verification"`
	Cycles            []impact.Cycle              `json:"cycles"`
	Diagnostics       []diag.Diagnostic           `json:"diagnostics"`
	Visited           int                         `json:"visited"`
}

// statusVerification is the outcome of the authority verification of one decision record.
type statusVerification struct {
	RecordID  string `json:"recordId"`
	Kind      string `json:"kind"`
	Mechanism string `json:"mechanism"`
	Authority string `json:"authority"`
	Signer    string `json:"signer"`
	// Reason is empty for an applied record; otherwise the reason that it is not applied or could not be judged.
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
	Applied bool   `json:"applied"`
}

func assessmentStatus(ctx context.Context, app *Context, opts *assessmentStatusOptions) (int, error) {
	report, code, err := evaluateAssessmentStatus(ctx, app, opts)
	if err != nil {
		return code, err
	}

	if opts.json || app.Opts.Format == modelFormatJSON {
		data, err := canonjson.Marshal(report)
		if err != nil {
			return diag.Internal, fmt.Errorf("encode the assessment status: %w", err)
		}

		app.Out(string(data))

		return diag.OK, nil
	}

	for _, d := range report.Diagnostics {
		app.Err(diag.FormatDiagnostic(d) + "\n")
	}

	app.Out(statusText(&report))

	return diag.OK, nil
}

// evaluateAssessmentStatus loads the assessments, evaluates their currency, and shapes the report. The exit code is
// meaningful only with an error.
func evaluateAssessmentStatus(ctx context.Context, app *Context, opts *assessmentStatusOptions) (assessmentStatusReport, int, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return assessmentStatusReport{}, diag.InvalidInput, err
	}

	set, err := assessment.Load(wsp.Root, assessmentPaths(wsp))
	if err != nil {
		return assessmentStatusReport{}, diag.InvalidInput, err //nolint:wrapcheck // the load error is a diagnostic with its exit code.
	}

	if opts.assessment != "" && len(set.Versions(opts.assessment)) == 0 {
		return assessmentStatusReport{}, diag.InvalidInput, usageError("--assessment " + opts.assessment + " names no assessment under assessments.paths")
	}

	input, err := assessmentEvalInput(ctx, app, wsp, set)
	if err != nil {
		return assessmentStatusReport{}, diag.Internal, err
	}

	if opts.at != "" {
		at, parseErr := clock.Parse(opts.at)
		if parseErr != nil {
			return assessmentStatusReport{}, diag.InvalidInput, usageError("--at " + opts.at + " is not a date or an RFC 3339 time")
		}

		input.Now = func() time.Time { return at }
	}

	evaluation, err := assessment.Evaluate(ctx, input)
	if err != nil {
		return assessmentStatusReport{}, diag.Internal, err //nolint:wrapcheck // Evaluate wraps the verifier and fact source errors.
	}

	return statusReportOf(evaluation, input, opts.assessment), diag.OK, nil
}

// statusReportOf shapes the evaluation for output. The digest-impact report lists the verified approvals that the
// current digest contract reaches, so a contract change is seen before it makes assessments review-required.
func statusReportOf(evaluation *assessment.Evaluation, input *assessment.EvalInput, only string) assessmentStatusReport {
	report := assessmentStatusReport{
		EvaluatedAt: clock.ISO(evaluation.At), ContractVersion: evaluation.ContractVersion, CapabilityProfile: evaluation.CapabilityProfile,
		Assessments: []assessment.Result{}, Required: []assessment.RequiredResult{}, Verification: statusVerifications(evaluation.Verification),
		Cycles: evaluation.Cycles, Diagnostics: evaluation.Diagnostics, Visited: evaluation.Visited,
	}

	var bound []impact.Bound

	for i := range evaluation.Assessments {
		row := &evaluation.Assessments[i]
		if only != "" && row.ID != only {
			continue
		}

		report.Assessments = append(report.Assessments, *row)

		if b, ok := approvedBinding(row); ok {
			bound = append(bound, b)
		}
	}

	for i := range evaluation.Required {
		if only == "" || evaluation.Required[i].Assessment == only {
			report.Required = append(report.Required, evaluation.Required[i])
		}
	}

	report.DigestImpact = statusRegistry(input).DigestImpact(evaluation.ContractVersion, bound)

	if report.Diagnostics == nil {
		report.Diagnostics = []diag.Diagnostic{}
	}

	if report.Cycles == nil {
		report.Cycles = []impact.Cycle{}
	}

	return report
}

// statusRegistry is the contract registry of the input, or one holding only the input's contract.
func statusRegistry(input *assessment.EvalInput) impact.Registry {
	if len(input.Contracts.Versions()) > 0 {
		return input.Contracts
	}

	contract := input.Contract
	if contract.Version == "" {
		contract = impact.V1()
	}

	return impact.NewRegistry(contract)
}

// approvedBinding is the digest-contract binding of the approval that a version rests on, if it is an approved one.
func approvedBinding(row *assessment.Result) (impact.Bound, bool) {
	ap := row.Currency.Approval
	if ap == nil || ap.Decision != assessment.DecisionApproved {
		return impact.Bound{}, false
	}

	return impact.Bound{Assessment: row.ID + "@" + strconv.Itoa(row.Version), Approval: ap.ID, Contract: ap.ContractVersion}, true
}

// statusVerifications shapes the authority verification outcomes for output.
func statusVerifications(verification []assessment.Verification) []statusVerification {
	rows := make([]statusVerification, 0, len(verification))

	for _, v := range verification {
		rows = append(rows, statusVerification{
			RecordID: v.RecordID, Kind: v.Kind, Mechanism: v.Mechanism, Authority: v.Authority, Signer: v.Signer,
			Reason: v.Reason, Detail: v.Detail, Applied: v.Applied,
		})
	}

	return rows
}

// statusText is the pretty output: one block per assessment version, the required entries, and the digest impact.
func statusText(report *assessmentStatusReport) string {
	var text strings.Builder

	fmt.Fprintf(&text, "Assessment currency at %s (digest contract %s, capability profile %s)\n", report.EvaluatedAt,
		report.ContractVersion, orNone(report.CapabilityProfile))

	if len(report.Assessments) == 0 {
		text.WriteString("\nNo assessment documents.\n")
	}

	for i := range report.Assessments {
		assessmentText(&text, &report.Assessments[i])
	}

	if len(report.Required) > 0 {
		text.WriteString("\nRequired assessments\n")
	}

	for i := range report.Required {
		requiredText(&text, &report.Required[i])
	}

	impactText(&text, &report.DigestImpact)

	return text.String()
}

// assessmentText writes the block of one assessment version: the currency on the heading, then what it is and where,
// the approval it rests on, why it is not current, what changed, and the next action.
func assessmentText(text *strings.Builder, row *assessment.Result) {
	cur := &row.Currency

	state := string(cur.State)
	if cur.UnknownReason != "" {
		state += " (" + cur.UnknownReason + ")"
	}

	fmt.Fprintf(text, "\n%s v%d: %s\n", row.ID, row.Version, state)
	fmt.Fprintf(text, "  %s, %s, outcome %s, review state %s, %s:%d\n", row.Type, row.Environment, row.Outcome, row.State, row.File, row.Line)

	approvalText(text, cur)
	currencyReasonText(text, cur)

	actions := triggerText(text, cur.Triggers)

	for _, u := range cur.Undisposed {
		fmt.Fprintf(text, "  removed without a disposition: %s %s\n", u.Kind, u.Identity)
	}

	for i := range cur.Regressions {
		fmt.Fprintf(text, "  capability regression: %s\n", cur.Regressions[i].Detail)
	}

	if len(actions) > 0 {
		fmt.Fprintf(text, "  next action: %s\n", strings.Join(actions, ", "))
	}
}

// approvalText writes the approval the currency rests on, or what an unapproved version was compared with.
func approvalText(text *strings.Builder, cur *assessment.Currency) {
	switch {
	case cur.Approval != nil:
		ap := cur.Approval
		fmt.Fprintf(text, "  approval %s by %s (%s, %s), decided %s%s\n", ap.ID, ap.Reviewer, ap.Authority, orNone(ap.Mechanism),
			clock.ISO(ap.DecidedAt), expiryText(ap))
	case cur.Basis != "":
		fmt.Fprintf(text, "  no approval of this version; compared with %s\n", cur.Basis)
	}
}

// currencyReasonText writes why a version is not current: the reasons, details, causes, and the records not applied.
func currencyReasonText(text *strings.Builder, cur *assessment.Currency) {
	for _, reason := range cur.Reasons {
		fmt.Fprintf(text, "  why: %s\n", reason)
	}

	for _, detail := range cur.Details {
		fmt.Fprintf(text, "    %s\n", detail)
	}

	for _, c := range cur.Causes {
		fmt.Fprintf(text, "  cannot tell: %s (%s)\n", c.Detail, c.Reason)
	}

	for _, n := range cur.NotApplied {
		fmt.Fprintf(text, "  not applied: %s %s: %s %s\n", n.Kind, n.Record, n.Reason, n.Detail)
	}
}

// triggerText writes what changed and returns the distinct next actions in the order of the triggers.
func triggerText(text *strings.Builder, triggers []impact.ReviewTrigger) []string {
	var actions []string

	for i := range triggers {
		trigger := &triggers[i]
		fmt.Fprintf(text, "  changed: %s\n", trigger.ChangeText())

		// A direct dependency needs no path; a longer one says how the change reaches the assessment.
		if len(trigger.Path) > 2 {
			fmt.Fprintf(text, "    path: %s\n", strings.Join(trigger.Path, " -> "))
		}

		if action := trigger.Action; action != "" && !slices.Contains(actions, action) {
			actions = append(actions, action)
		}
	}

	return actions
}

// requiredText writes the line of one required entry: which assessment satisfies it, and in which state.
func requiredText(text *strings.Builder, row *assessment.RequiredResult) {
	target := row.Assessment
	if row.Version > 0 {
		target = fmt.Sprintf("%s v%d", row.Assessment, row.Version)
	}

	fmt.Fprintf(text, "  %s: %s is %s", row.ID, target, row.Currency.State)

	if len(row.Currency.Reasons) > 0 {
		fmt.Fprintf(text, " (%s)", strings.Join(row.Currency.Reasons, "; "))
	}

	if row.Currency.UnknownReason != "" {
		fmt.Fprintf(text, " (%s)", row.Currency.UnknownReason)
	}

	text.WriteString("\n")
}

func impactText(text *strings.Builder, report *impact.DigestImpactReport) {
	if len(report.Affected)+len(report.Equivalent)+len(report.Invalid) == 0 {
		return
	}

	fmt.Fprintf(text, "\nDigest impact of %s\n", report.Current)

	for _, affected := range report.Affected {
		fmt.Fprintf(text, "  %s: approval %s was bound under %s: %s\n", affected.Assessment, affected.Approval, affected.BoundContract, affected.Detail)
	}

	for _, same := range report.Equivalent {
		fmt.Fprintf(text, "  %s: approval %s bound under %s stays valid (equivalent)\n", same.Assessment, same.Approval, same.Contract)
	}

	for _, mark := range report.Invalid {
		fmt.Fprintf(text, "  invalid equivalence mark: %s\n", mark)
	}
}

func expiryText(approval *assessment.ApprovalInfo) string {
	if approval.ExpiresAt == nil {
		return "; no expiry time"
	}

	return "; expires " + clock.ISO(*approval.ExpiresAt)
}

func orNone(value string) string {
	if value == "" {
		return "none"
	}

	return value
}
