package conformance

import (
	"context"
	"slices"

	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// reviewNativeKinds are the native edge kinds that the review facts read, plus the DEFINES edges that give all
// nodes.
func reviewNativeKinds() []string {
	set := map[string]struct{}{"DEFINES": {}, "DEFINES_METHOD": {}}
	addNativeKinds(set, ReviewEvidenceKinds())

	kinds := make([]string, 0, len(set))
	for kind := range set {
		kinds = append(kinds, kind)
	}

	slices.SortFunc(kinds, compareStrings)

	return kinds
}

// LoadReviewFacts builds the impact.FactSource of the workspace without a full check: it runs the stages of
// RunCheck that produce the facts (freeze the graph, load the model and the bindings, fetch the evidence kinds that
// review dependencies read, resolve the bindings, compute the predicate coverage) and none of the rule evaluation,
// the views, the decisions, or the reports. It writes nothing.
//
// The options are those of a check run (`Env.WorkspacePath`, `Deps`, `Projects`, `Environments`). It gives a
// *diag.Error for input, adapter, toolchain, and lock problems, like RunCheck.
//
//nolint:ireturn // the fact source is the interface that the review code reads.
func LoadReviewFacts(ctx context.Context, opts *CheckOptions) (impact.FactSource, error) {
	chk := newChecker(opts)
	defer chk.closeClient()

	if err := validateFlags(opts); err != nil {
		return nil, err
	}

	wspace, err := workspace.Load(opts.Env.WorkspacePath, opts.Env.Profile)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	selected, err := SelectProjects(wspace, opts.Projects)
	if err != nil {
		return nil, err
	}

	chk.environments, err = SelectEnvironments(wspace, opts.Environments)
	if err != nil {
		return nil, err
	}

	state, err := chk.freeze(ctx, wspace, selected)
	if err != nil {
		return nil, err
	}

	if err := prepareFrozen(ctx, opts, wspace, state); err != nil {
		return nil, err
	}

	caps := &policy.ProviderCaps{SupportedCanonicalKinds: state.caps.SupportedCanonicalKinds, Profile: state.caps.Profile, Families: state.caps.Families}

	inp, err := chk.loadInputs(ctx, wspace, state, caps)
	if err != nil {
		return nil, err
	}

	evidence, err := chk.fetchEvidenceOf(ctx, wspace, state, inp, reviewNativeKinds())
	if err != nil {
		return nil, err
	}

	workspaceFiles, _, err := workspaceFilesOf(ctx, state)
	if err != nil {
		return nil, err
	}

	_, runCov := chk.buildScope(&coverageInput{wspace: wspace, state: state, evidence: evidence, bindings: &inp.bindings, workspaceFiles: workspaceFiles})

	// The same helpers as `check`: the profile ID and the item digests go into approvals and the cache key.
	profile := profileOf(&state.caps)
	declared := environmentsOfWorkspace(wspace)

	bindingSet := evidence.bindingSet

	return NewReviewFacts(&ReviewFactsInput{
		Evidence: &evidence.result, BindingSet: &bindingSet, Profile: profile, Model: inp.model, Git: chk.newGit(wspace.Root),
		Root: wspace.Root, ProviderVersion: state.caps.ExecutableVersion, AdapterSchema: state.caps.AdapterSchema,
		Coverage: runCov.Predicates, Bindings: inp.bindings.Bindings, Environments: declared, Assurance: chk.reviewAssurance(),
	}), nil
}
