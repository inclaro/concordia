package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

func writeOutputFile(abs, shown string, data []byte) error {
	if err := fsutil.WriteFileAtomic(abs, data); err != nil {
		return diag.Fail(diag.REP001, diag.Params{"path": shown, "detail": err.Error()})
	}

	return nil
}

// Manifest statuses. A run manifest is `incomplete` from the start of writing, and
// atomically replaced by `complete` after every run file is written.
const (
	ManifestIncomplete = "incomplete"
	ManifestComplete   = "complete"
)

type manifest struct {
	Toolchain RunToolchain `json:"toolchain"`
	// Analysis holds the counters of the review evaluation (the same object as in `results.json`). Left out when the
	// run evaluated no assessment.
	Analysis *AnalysisReport `json:"analysis,omitempty"`
	Status   string          `json:"status"`
	Inputs   RunInputs       `json:"inputs"`
	Run      RunInfo         `json:"run"`
	// AssuranceRedactions record the values that were removed from the rendered documents, the plans, and the
	// statements, by class and place and never by value. Left out when nothing was removed.
	AssuranceRedactions []redact.Record `json:"assuranceRedactions,omitempty"`
	Summary             RunSummary      `json:"summary"`
	// Observability holds the counters of the run, the same object as in `results.json`.
	Observability Observability `json:"observability"`
}

// RunManifestPath gives the workspace-relative "/" path of the manifest of a run.
func RunManifestPath(runID string) string {
	return ".concordia/runs/" + runID + "/manifest.json"
}

// RequireCompleteRun refuses a run whose manifest under root says it is not `complete`
// (CO-REP-004). A run without a manifest is accepted: its results come from elsewhere.
func RequireCompleteRun(root, runID string) error {
	if runID == "" || root == "" {
		return nil
	}

	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(RunManifestPath(runID)))) //nolint:gosec // the path is below the workspace.
	if err != nil {
		return nil //nolint:nilerr // no manifest, nothing to refuse.
	}

	var probe struct {
		Status string `json:"status"`
	}

	if json.Unmarshal(text, &probe) != nil || probe.Status != ManifestComplete {
		return diag.Fail(diag.REP004, diag.Detail(fmt.Sprintf("run %s is incomplete: its manifest status is not '%s'", runID, ManifestComplete)))
	}

	return nil
}

type evidenceRule struct {
	Coverage *Coverage              `json:"coverage,omitempty"`
	Policy   policy.PackRef         `json:"policy"`
	RuleID   string                 `json:"ruleId"`
	Name     string                 `json:"name"`
	Family   policy.PredicateFamily `json:"family"`
	Verdict  RuleVerdict            `json:"verdict"`
	Plan     policy.RulePlan        `json:"plan"`
}

type evidenceRun struct {
	SourceRevision  *string `json:"sourceRevision"`
	ID              string  `json:"id"`
	GraphGeneration string  `json:"graphGeneration"`
}

type evidenceFile struct {
	Rule          *evidenceRule `json:"rule,omitempty"`
	Finding       *Finding      `json:"finding"`
	Run           evidenceRun   `json:"run"`
	SchemaVersion string        `json:"schemaVersion"`
}

// writeRunFiles writes the incomplete run manifest, `report.outputs`, the canonical JSON, the
// evidence files, and last the `complete` run manifest. A stopped context leaves it `incomplete`.
func writeRunFiles(ctx context.Context, opts *CheckOptions, wspace *workspace.Workspace, run *Run) error {
	if opts.Deps.RenderReport == nil {
		return errors.New("no report renderer: set Deps.RenderReport or SkipReports") //nolint:err113 // a programming error of the caller.
	}

	manifestPath := RunManifestPath(run.Run.ID)
	manifestAbs := filepath.Join(wspace.Root, filepath.FromSlash(manifestPath))

	writeManifest := func(status string) error {
		text, err := canonjson.Marshal(manifest{
			Status: status, Run: run.Run, Toolchain: run.Toolchain, Inputs: run.Inputs, Summary: run.Summary, Observability: run.Observability,
			Analysis: run.Analysis, AssuranceRedactions: assuranceManifestRedactions(run),
		})
		if err != nil {
			return fmt.Errorf("encode manifest: %w", err)
		}

		return writeOutputFile(manifestAbs, manifestPath, text)
	}

	if err := writeManifest(ManifestIncomplete); err != nil {
		return err
	}

	outputs := slices.Clone(wspace.Report.Outputs)
	if !slices.ContainsFunc(outputs, func(o workspace.ReportOutput) bool { return o.Format == workspace.ReportJSON }) {
		outputs = append(outputs, workspace.DefaultReportOutput())
	}

	for _, o := range outputs {
		data, err := opts.Deps.RenderReport(o.Format, run)
		if err != nil {
			return fmt.Errorf("render %s report: %w", o.Format, err)
		}

		if err := writeOutputFile(wspace.ResolveInRoot(o.Path), o.Path, data); err != nil {
			return err
		}
	}

	if err := writeEvidenceFiles(opts, run); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("run files: %w", err)
	}

	return writeManifest(ManifestComplete)
}

func writeEvidenceFiles(opts *CheckOptions, run *Run) error {
	if opts.EvidenceDir == "" {
		return nil
	}

	dir := opts.EvidenceDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(opts.Env.Cwd, filepath.FromSlash(dir))
	}

	ruleByID := make(map[string]*RuleResult, len(run.Rules))
	for i := range run.Rules {
		ruleByID[run.Rules[i].RuleID] = &run.Rules[i]
	}

	for i := range run.Findings {
		f := &run.Findings[i]
		name := strings.TrimPrefix(f.Fingerprint, digest.Prefix) + ".json"
		body := evidenceFile{
			SchemaVersion: ResultSchemaVersion,
			Run:           evidenceRun{ID: run.Run.ID, SourceRevision: run.Run.SourceRevision, GraphGeneration: run.Run.GraphGeneration},
			Finding:       f,
		}

		if rule := ruleByID[f.RuleID]; rule != nil {
			body.Rule = &evidenceRule{
				RuleID: rule.RuleID, Name: rule.Name, Policy: rule.Policy, Family: rule.Family,
				Verdict: rule.Verdict, Coverage: rule.Coverage, Plan: rule.Plan,
			}
		}

		text, err := canonjson.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode evidence %s: %w", name, err)
		}

		if err := writeOutputFile(filepath.Join(dir, name), opts.EvidenceDir+"/"+name, text); err != nil {
			return err
		}
	}

	return nil
}
