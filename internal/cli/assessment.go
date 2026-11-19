package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// assessmentValidateOptions are the flags of `assessment validate`.
type assessmentValidateOptions struct {
	json bool
}

func newAssessmentCmd(host *Host) *cobra.Command {
	group := &cobra.Command{Use: "assessment", Short: "Validate and review security and quality assessments"}

	var validateOpts assessmentValidateOptions

	validate := &cobra.Command{
		Use:   cmdValidate,
		Short: "Validate the assessment, assumption, approval, and revocation documents offline",
		Long: "Validate the assessment documents under assessments.paths against their closed schemas, and check " +
			"references, duplicate versions, authored states, and that no approved version was edited. Reads files " +
			"only: no CBM, no git, no network, no writes. A decision record is checked for its form and its bound " +
			"digests, not for who made it: authority verification is part of `assessment status` and `check`.",
		Args: cobra.NoArgs,
		RunE: host.Action(func(_ context.Context, app *Context, _ []string) (int, error) {
			return assessmentValidate(app, validateOpts)
		}),
	}

	validate.Flags().BoolVar(&validateOpts.json, "json", false, "machine-readable output")

	group.AddCommand(validate, newAssessmentStatusCmd(host), newAssessmentApproveCmd(host), newAssessmentRevokeCmd(host))

	return group
}

// assessmentPaths gives the directories, files, and globs to load. A workspace without an `assessments` key reads
// the default directory when it exists, and nothing otherwise.
func assessmentPaths(wsp *workspace.Workspace) []string {
	if wsp.Assessments != nil {
		return wsp.Assessments.Paths
	}

	for _, p := range workspace.DefaultAssessmentPaths() {
		if info, err := os.Stat(filepath.Join(wsp.Root, filepath.FromSlash(p))); err == nil && info.IsDir() {
			return workspace.DefaultAssessmentPaths()
		}
	}

	return []string{}
}

// assessmentValidateReport is the machine-readable result of `assessment validate`.
type assessmentValidateReport struct {
	Assessments []assessmentVersionSummary `json:"assessments"`
	Assumptions []assumptionSummary        `json:"assumptions"`
	Approvals   []approvalSummary          `json:"approvals"`
	Revocations []revocationSummary        `json:"revocations"`
	Diagnostics []diag.Diagnostic          `json:"diagnostics"`
	OK          bool                       `json:"ok"`
}

type assessmentVersionSummary struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Environment   string `json:"environment"`
	Outcome       string `json:"outcome"`
	AuthoredState string `json:"authoredState"`
	Digest        string `json:"digest"`
	File          string `json:"file"`
	Version       int    `json:"version"`
}

type assumptionSummary struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	File   string `json:"file"`
}

type approvalSummary struct {
	ID          string `json:"id"`
	Decision    string `json:"decision"`
	Assessment  string `json:"assessment"`
	Environment string `json:"environment"`
	Digest      string `json:"digest"`
	File        string `json:"file"`
}

type revocationSummary struct {
	ID       string `json:"id"`
	Approval string `json:"approval"`
	Digest   string `json:"digest"`
	File     string `json:"file"`
}

func assessmentValidate(app *Context, opts assessmentValidateOptions) (int, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return diag.InvalidInput, err
	}

	set, errs := assessment.LoadAll(wsp.Root, assessmentPaths(wsp))

	report := assessmentValidateReport{
		OK:          len(errs) == 0,
		Assessments: []assessmentVersionSummary{},
		Assumptions: []assumptionSummary{},
		Approvals:   []approvalSummary{},
		Revocations: []revocationSummary{},
		Diagnostics: make([]diag.Diagnostic, 0, len(errs)),
	}

	for _, a := range set.Assessments {
		report.Assessments = append(report.Assessments, assessmentVersionSummary{
			ID: a.ID, Version: a.Version, Type: string(a.Type), Environment: a.Environment, Outcome: string(a.Outcome),
			AuthoredState: string(a.State), Digest: a.Digest, File: a.File,
		})
	}

	for _, a := range set.Assumptions {
		report.Assumptions = append(report.Assumptions, assumptionSummary{ID: a.ID, Digest: a.Digest, File: a.File})
	}

	for _, a := range set.Approvals {
		report.Approvals = append(report.Approvals, approvalSummary{
			ID: a.ID, Decision: string(a.Decision), Assessment: assessment.VersionKey(a.Assessment.ID, a.Assessment.Version),
			Environment: a.Environment, Digest: a.Digest, File: a.File,
		})
	}

	for _, r := range set.Revocations {
		report.Revocations = append(report.Revocations, revocationSummary{ID: r.ID, Approval: r.Approval.ID, Digest: r.Digest, File: r.File})
	}

	for _, e := range errs {
		report.Diagnostics = append(report.Diagnostics, e.Diagnostic)
	}

	report.Diagnostics = diag.Sort(report.Diagnostics)

	code := diag.OK
	if !report.OK {
		code = diag.InvalidInput
	}

	if opts.json || app.Opts.Format == modelFormatJSON {
		data, err := canonjson.Marshal(report)
		if err != nil {
			return diag.Internal, fmt.Errorf("encode the assessment report: %w", err)
		}

		app.Out(string(data))

		return code, nil
	}

	for _, d := range report.Diagnostics {
		app.Err(diag.FormatDiagnostic(d) + "\n")
	}

	if !report.OK {
		app.Err(fmt.Sprintf("assessment: %d problem(s)\n", len(report.Diagnostics)))

		return code, nil
	}

	app.Out(assessmentSummaryText(wsp, &report))

	return code, nil
}

// assessmentSummaryText is the pretty output of a valid set.
func assessmentSummaryText(wsp *workspace.Workspace, report *assessmentValidateReport) string {
	if len(report.Assessments)+len(report.Assumptions)+len(report.Approvals)+len(report.Revocations) == 0 {
		if wsp.Assessments == nil {
			return "assessment: no documents (the workspace has no assessments key and no assessments/ directory)\n"
		}

		return "assessment: no documents under assessments.paths\n"
	}

	var text strings.Builder

	fmt.Fprintf(&text, "assessment: %d version(s), %d assumption(s), %d approval record(s), %d revocation record(s) valid\n",
		len(report.Assessments), len(report.Assumptions), len(report.Approvals), len(report.Revocations))

	for i := range report.Assessments {
		ver := &report.Assessments[i]

		fmt.Fprintf(&text, "  %s@v%d  %s  %s  outcome %s  authored state %s\n",
			ver.ID, ver.Version, ver.Type, ver.Environment, ver.Outcome, ver.AuthoredState)
	}

	if len(report.Approvals)+len(report.Revocations) > 0 {
		text.WriteString("  decision records are checked for form and bound digests only; `assessment status` verifies their authority\n")
	}

	return text.String()
}
