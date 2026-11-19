package cli

import (
	"context"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/workspace"
)

// loadReviewFacts is the production value of Deps.ReviewFacts (set where Main runs `Run`): the live facts of the workspace, built by the stages of
// the check pipeline that produce them (no rule evaluation and no file is written).
//
//nolint:ireturn // Deps.ReviewFacts returns the fact source interface.
func loadReviewFacts(ctx context.Context, app *Context, _ *workspace.Workspace) (impact.FactSource, error) {
	return conformance.LoadReviewFacts(ctx, &conformance.CheckOptions{Env: checkEnv(app), Deps: checkDeps(app), SkipReports: true})
}
