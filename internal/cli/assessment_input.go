package cli

import (
	"context"
	"path/filepath"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// assessmentEvalInput builds the input of assessment.Evaluate and assessment.Bind for the workspace: the loaded
// set, the verifier (authority configuration and records read from the trust anchor), the required assessments
// (from the trust anchor when one is configured and readable, else from the workspace), the fact source, the
// analysis limits, and the evaluation clock.
//
// A trust anchor that cannot be read is not an error here: the verifier reports every record as `unknown`.
// The only errors are a failing git or a failing fact source.
func assessmentEvalInput(ctx context.Context, app *Context, wsp *workspace.Workspace, set *assessment.Set) (*assessment.EvalInput, error) {
	cfg := wsp.Assessments
	if cfg == nil {
		cfg = &workspace.Assessments{Paths: workspace.DefaultAssessmentPaths(), Authorities: []workspace.Authority{}, Required: []workspace.RequiredAssessment{}}
	}

	git := app.Git(wsp.Root)

	ref := ""
	if cfg.TrustAnchor != nil {
		ref = cfg.TrustAnchor.Ref
	}

	anchor := assessment.ReadAnchor(ctx, &assessment.AnchorSource{
		Git: git, Ref: ref, WorkspacePath: wsp.Path, Profile: wsp.Profile,
	})

	required := cfg.Required
	if ref != "" && anchor.Readable() {
		required = anchor.Required()
	}

	// The authority digest is of the configuration that verification uses: the anchor's when one is readable.
	authorities := cfg.Authorities
	if ref != "" && anchor.Readable() && anchor.Config != nil {
		authorities = anchor.Config.Authorities
	}

	files, problems, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := policy.ThrowOnErrors(problems); err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	policyDigest := assessment.PolicyDigest(files)

	in := &assessment.EvalInput{ //nolint:varnamelen // in is the input that the function builds.
		Set: set,
		Verifier: assessment.NewAuthorities(&assessment.AuthorityOptions{
			Git: git, Anchor: anchor, Root: wsp.Root, Getenv: app.IO.Getenv, Offline: app.Opts.Offline,
		}),
		Required: required,
		Limits: impact.Limits{
			MaxDepth: cfg.AnalysisLimits.MaxDepth, MaxVisited: cfg.AnalysisLimits.MaxVisited, MaxSeconds: cfg.AnalysisLimits.MaxSeconds,
		},
		Now:             app.Now,
		PolicyDigest:    policyDigest,
		AuthorityDigest: assessment.AuthorityDigest(authorities),
	}

	if contracts := app.Deps.ReviewContracts; contracts != nil {
		in.Contract, in.Contracts = contracts.Current, contracts.Known
	}

	if app.Deps.ReviewFacts != nil {
		facts, err := app.Deps.ReviewFacts(ctx, app, wsp)
		if err != nil {
			return nil, err
		}

		in.Facts = facts
	}

	return in, nil
}

// assessmentBase gives the directory below which `assessment approve` and `revoke` write records: the first
// plain directory of `assessments.paths` (the default is `assessments`).
func assessmentBase(wsp *workspace.Workspace) string {
	paths := assessmentPaths(wsp)

	for _, p := range paths {
		if !hasGlob(p) && filepath.Ext(p) == "" {
			return p
		}
	}

	return workspace.DefaultAssessmentPaths()[0]
}

func hasGlob(p string) bool {
	for _, r := range p {
		switch r {
		case '*', '?', '[', '{':
			return true
		}
	}

	return false
}

// refuse is the CO-ASM-005 error (exit 2) of an approve or revoke that must not write.
func refuse(detail string) *diag.Error {
	return diag.Fail(diag.ASM005, diag.Detail(detail))
}
