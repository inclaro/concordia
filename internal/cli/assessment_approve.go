package cli

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// assessmentDecisionOptions are the flags of `assessment approve` and `assessment revoke`.
type assessmentDecisionOptions struct {
	reviewer   string
	authority  string
	confirm    string
	expires    string
	path       string
	signKey    string
	reason     string
	decision   string
	conditions []string
	version    int
	sign       bool
}

// newAssessmentApproveCmd builds `assessment approve`.
func newAssessmentApproveCmd(host *Host) *cobra.Command {
	var opts assessmentDecisionOptions

	cmd := &cobra.Command{
		Use:   "approve <assessment-id>",
		Short: "Record the decision of a reviewer on one assessment version",
		Long: "Print the subject of one assessment version (its dependency items, assumptions, evidence, and the subject " +
			"digest) and, when --confirm-subject gives that digest, write one approval record. The command writes only " +
			"the record file (and its signature bundle with --sign). It refuses to run when the CI environment variable " +
			"is true, and it never approves by itself: the reviewer names themselves with --reviewer and confirms the " +
			"digest they examined.",
		Args: cobra.ExactArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return diag.OK, assessmentApprove(ctx, app, args[0], &opts)
		}),
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.reviewer, "reviewer", "", "identity of the reviewer (required)")
	flags.StringVar(&opts.authority, "authority", "", "authority the reviewer acts under (default: the authority of the required assessment)")
	flags.StringVar(&opts.confirm, "confirm-subject", "", "the subject digest that the reviewer examined (required to write)")
	flags.StringVar(&opts.expires, "expires", "", "last day of the approval (YYYY-MM-DD) or an RFC 3339 time")
	flags.StringArrayVar(&opts.conditions, "condition", nil, "an expiry condition in words (repeatable)")
	flags.StringVar(&opts.decision, "decision", string(assessment.DecisionApproved), "approved or rejected")
	flags.IntVar(&opts.version, "version", 0, "assessment version (default: the latest)")
	flags.StringVar(&opts.path, "path", "", "workspace-relative file of the record (default: <assessments>/approvals/<id>-v<N>-<seq>.yaml)")
	flags.BoolVar(&opts.sign, "sign", false, "sign the canonical record with cosign (mechanism: signed)")
	flags.StringVar(&opts.signKey, "sign-key", "", "cosign key reference (default: keyless signing)")

	return cmd
}

// newAssessmentRevokeCmd builds `assessment revoke`.
func newAssessmentRevokeCmd(host *Host) *cobra.Command {
	var opts assessmentDecisionOptions

	cmd := &cobra.Command{
		Use:   "revoke <approval-id>",
		Short: "Record the revocation of an approval",
		Long: "Print an approval record (its assessment version and digest) and, when --confirm-subject gives the digest " +
			"of the approval record, write one revocation record. The command writes only the record file (and its " +
			"signature bundle with --sign). It refuses to run when the CI environment variable is true.",
		Args: cobra.ExactArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return diag.OK, assessmentRevoke(ctx, app, args[0], &opts)
		}),
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.reviewer, "reviewer", "", "identity of the reviewer (required)")
	flags.StringVar(&opts.authority, "authority", "", "authority the reviewer acts under (default: the authority of the approval)")
	flags.StringVar(&opts.confirm, "confirm-subject", "", "the digest of the approval record that the reviewer examined (required to write)")
	flags.StringVar(&opts.reason, "reason", "", "why the approval is revoked (required)")
	flags.StringVar(&opts.path, "path", "", "workspace-relative file of the record (default: <assessments>/revocations/<approval id>.yaml)")
	flags.BoolVar(&opts.sign, "sign", false, "sign the canonical record with cosign (mechanism: signed)")
	flags.StringVar(&opts.signKey, "sign-key", "", "cosign key reference (default: keyless signing)")

	return cmd
}

// decisionGuard makes the checks that come before any read: no CI, a reviewer, and the cosign preconditions.
// It gives the cosign executable when --sign is set.
func decisionGuard(app *Context, verb string, opts *assessmentDecisionOptions) (string, error) {
	if inCI(app) {
		return "", refuse("assessment " + verb + " is refused when CI is set: a reviewer records the decision, not a pipeline")
	}

	if strings.TrimSpace(opts.reviewer) == "" {
		return "", refuse("--reviewer is required: name the reviewer who makes the decision")
	}

	if !opts.sign {
		return "", nil
	}

	// --offline decides before cosign is looked for, so a missing cosign never hides it.
	if app.Opts.Offline {
		return "", diag.Fail(diag.WRK016, diag.Detail(offlineSigning))
	}

	exe, err := cosign.Path(app.IO.Getenv)
	if err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return exe, nil
}

// loadDecisionSet loads the workspace and the assessments in it.
func loadDecisionSet(app *Context) (*workspace.Workspace, *assessment.Set, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	set, err := assessment.Load(wsp.Root, assessmentPaths(wsp))
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return wsp, set, nil
}

// confirmDecisionDigest refuses unless --confirm-subject gives the digest that the reviewer examined. The noun
// names what the digest is of.
func confirmDecisionDigest(confirm, digest, noun string) error {
	switch {
	case confirm == "":
		return refuse("confirm the " + noun + " that you examined with --confirm-subject " + digest)
	case confirm != digest:
		return refuse("the " + noun + " differs from the one that was confirmed: examined " + confirm +
			", current " + digest)
	}

	return nil
}

// parseDecision reads --decision.
func parseDecision(value string) (assessment.Decision, error) {
	decision := assessment.Decision(value)
	if decision != assessment.DecisionApproved && decision != assessment.DecisionRejected {
		return "", refuse("--decision is '" + value + "': use approved or rejected")
	}

	return decision, nil
}

// approvalTarget is the assessment version that a reviewer decides on, with the workspace that holds it.
type approvalTarget struct {
	wsp     *workspace.Workspace
	set     *assessment.Set
	doc     *assessment.Assessment
	id      string
	version int
}

// findApprovalTarget finds the version to decide on: --version, else the latest.
func findApprovalTarget(wsp *workspace.Workspace, set *assessment.Set, id string, requested int) (*approvalTarget, error) { //nolint:varnamelen // id is the assessment id, as in every command of the group.
	version := requested
	if version == 0 {
		if versions := set.Versions(id); len(versions) > 0 {
			version = versions[len(versions)-1].Version
		}
	}

	doc := set.Assessment(id, version)
	if doc == nil {
		return nil, diag.Fail(diag.ASM001, diag.Detail(fmt.Sprintf("assessment '%s' version %d does not exist", id, version)))
	}

	return &approvalTarget{wsp: wsp, set: set, doc: doc, id: id, version: version}, nil
}

// bind computes the subject of the version, prints it for the reviewer, and gives it when the reviewer confirmed
// its digest.
func (t *approvalTarget) bind(ctx context.Context, app *Context, confirm string) (*assessment.Binding, error) {
	input, err := assessmentEvalInput(ctx, app, t.wsp, t.set)
	if err != nil {
		return nil, err
	}

	binding, err := assessment.Bind(ctx, input, t.id, t.version)
	if err != nil {
		return nil, fmt.Errorf("compute the subject of %s: %w", t.doc.Key(), err)
	}

	app.Out(subjectSummary(t.doc, binding))

	if len(binding.Unknown) > 0 {
		return nil, refuse("the subject of " + t.doc.Key() + " cannot be computed: " + unknownSummary(binding.Unknown))
	}

	if err := confirmDecisionDigest(confirm, binding.Subject.Digest, "subject"); err != nil {
		return nil, err
	}

	return binding, nil
}

// nextRecord gives the id of the next approval of the version, and the sequence number in it: the first one that
// no record uses.
func (t *approvalTarget) nextRecord() (recordID string, seq int) {
	seq = len(t.set.ApprovalsFor(t.id, t.version)) + 1
	for t.set.Approval(t.recordID(seq)) != nil {
		seq++
	}

	return t.recordID(seq), seq
}

func (t *approvalTarget) recordID(seq int) string {
	return fmt.Sprintf("%s-v%d-%d", t.id, t.version, seq)
}

// approvalDecision is what the reviewer decides, apart from the subject.
type approvalDecision struct {
	decision  assessment.Decision
	authority string
	expiry    *assessment.Expiry
	decidedAt time.Time
	reviewer  string
}

// readApprovalDecision reads the flags that the decision comes from.
func readApprovalDecision(app *Context, target *approvalTarget, opts *assessmentDecisionOptions, decision assessment.Decision) (*approvalDecision, error) {
	authority := authorityFor(target.wsp, target.doc, opts.authority)
	if authority == "" {
		return nil, refuse("--authority is required: no required assessment names an authority for " + target.doc.Key())
	}

	expiry, err := parseExpiry(opts.expires, opts.conditions, app.Now())
	if err != nil {
		return nil, err
	}

	return &approvalDecision{
		decision: decision, authority: authority, expiry: expiry, reviewer: opts.reviewer,
		decidedAt: app.Now().UTC().Truncate(time.Second),
	}, nil
}

// record builds the approval of a bound subject.
func (d *approvalDecision) record(binding *assessment.Binding, recordID, file string) *assessment.Approval {
	record := &assessment.Approval{
		ID: recordID, Decision: d.decision, Assessment: binding.Assessment, Subject: binding.Subject,
		Scope: binding.Scope, Environment: binding.Environment, Assumptions: binding.Assumptions,
		Evidence: binding.Evidence, ContractVersion: binding.ContractVersion, CapabilityProfile: binding.CapabilityProfile,
		ProducedUnder: binding.ProducedUnder,
		Reviewer:      assessment.Reviewer{Identity: d.reviewer, Authority: d.authority},
		DecidedAt:     d.decidedAt, Expiry: d.expiry, Previous: binding.Previous, Delta: binding.Delta,
	}
	record.File = file

	return record
}

func assessmentApprove(ctx context.Context, app *Context, id string, opts *assessmentDecisionOptions) error { //nolint:varnamelen // id is the assessment id, as in every command of the group.
	cosignExe, err := decisionGuard(app, "approve", opts)
	if err != nil {
		return err
	}

	decision, err := parseDecision(opts.decision)
	if err != nil {
		return err
	}

	wsp, set, err := loadDecisionSet(app)
	if err != nil {
		return err
	}

	target, err := findApprovalTarget(wsp, set, id, opts.version)
	if err != nil {
		return err
	}

	dec, err := readApprovalDecision(app, target, opts, decision)
	if err != nil {
		return err
	}

	binding, err := target.bind(ctx, app, opts.confirm)
	if err != nil {
		return err
	}

	recordID, seq := target.nextRecord()

	file := opts.path
	if file == "" {
		file = assessment.ApprovalPath(assessmentBase(wsp), id, target.version, seq)
	}

	record := dec.record(binding, recordID, file)

	text, err := assessment.RenderApproval(record)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	parsed, err := assessment.ParseApproval(file, text)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	f := &decisionFile{
		kind: "approval", id: recordID, path: file, text: text, canonical: parsed.CanonicalJSON(), digest: parsed.Digest,
	}

	return writeDecisionRecord(ctx, app, wsp, f.withSigning(opts, cosignExe, mechanismHint(wsp, dec.authority)))
}

// printApproval shows the approval that a reviewer revokes.
func printApproval(app *Context, approval *assessment.Approval) {
	app.Out(fmt.Sprintf("Approval %s (%s) of %s v%d, environment %s, reviewer %s, decided %s\nApproval digest: %s\n",
		approval.ID, approval.Decision, approval.Assessment.ID, approval.Assessment.Version, approval.Environment,
		approval.Reviewer.Identity, approval.DecidedAt.UTC().Format(time.RFC3339), approval.Digest))
}

// revocationRecord builds the revocation of an approval.
func revocationRecord(approval *assessment.Approval, opts *assessmentDecisionOptions, authority, file string, decidedAt time.Time) *assessment.Revocation {
	record := &assessment.Revocation{
		ID: approval.ID, Approval: assessment.Ref{ID: approval.ID, Digest: approval.Digest},
		Reviewer:  assessment.Reviewer{Identity: opts.reviewer, Authority: authority},
		DecidedAt: decidedAt, Reason: opts.reason,
	}
	record.File = file

	return record
}

func assessmentRevoke(ctx context.Context, app *Context, approvalID string, opts *assessmentDecisionOptions) error {
	cosignExe, err := decisionGuard(app, "revoke", opts)
	if err != nil {
		return err
	}

	if strings.TrimSpace(opts.reason) == "" {
		return refuse("--reason is required: say why the approval is revoked")
	}

	wsp, set, err := loadDecisionSet(app)
	if err != nil {
		return err
	}

	approval := set.Approval(approvalID)
	if approval == nil {
		return diag.Fail(diag.ASM001, diag.Detail(fmt.Sprintf("approval '%s' does not exist", approvalID)))
	}

	printApproval(app, approval)

	if err := confirmDecisionDigest(opts.confirm, approval.Digest, "approval"); err != nil {
		return err
	}

	authority := cmp.Or(opts.authority, approval.Reviewer.Authority)
	file := cmp.Or(opts.path, assessment.RevocationPath(assessmentBase(wsp), approval.ID))
	record := revocationRecord(approval, opts, authority, file, app.Now().UTC().Truncate(time.Second))

	text, err := assessment.RenderRevocation(record)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	parsed, err := assessment.ParseRevocation(file, text)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	f := &decisionFile{
		kind: "revocation", id: approval.ID, path: file, text: text, canonical: parsed.CanonicalJSON(), digest: parsed.Digest,
	}

	return writeDecisionRecord(ctx, app, wsp, f.withSigning(opts, cosignExe, mechanismHint(wsp, authority)))
}
