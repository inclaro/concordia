package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

func newAdrCmd(host *Host) *cobra.Command {
	group := &cobra.Command{Use: "adr", Short: "Validate the ADR governance declarations"}

	validate := &cobra.Command{
		Use:   cmdValidate,
		Short: "Validate the decision register, lifecycle profile, triggers, and rule links offline",
		Long: "Validate the decision register files, the lifecycle profile, and the triggers of the workspace " +
			"against the architecture model and the loaded policy packs. Reads files only: no CBM, no network, no writes.",
		Args: cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, adrValidate(ctx, app)
		}),
	}

	group.AddCommand(validate)

	return group
}

func adrValidate(ctx context.Context, app *Context) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	if wsp.ADR == nil || len(wsp.ADR.Files) == 0 {
		app.Out("adr: no register configured (the workspace has no adr.files)\n")

		return nil
	}

	model, err := loadAdrModel(ctx, app, wsp)
	if err != nil {
		return err
	}

	files, problems, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := policy.ThrowOnErrors(problems); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	caps := policy.ProviderCaps{SupportedCanonicalKinds: observation.SupportedCanonicalKinds()}

	compiled, err := policy.CompilePolicies(files, model, caps, nil)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if err := policy.ThrowOnErrors(compiled.Diagnostics); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	decl, err := adr.LoadDeclarations(wsp, model, &compiled)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if decl == nil {
		return nil
	}

	linked := 0

	for i := range decl.Register.Entries {
		linked += len(decl.Register.Entries[i].EnforcedBy)
	}

	app.Out(fmt.Sprintf("adr: %d decision(s) in %d register file(s) valid; %d rule link(s) checked; active statuses: %s\n",
		len(decl.Register.Entries), len(decl.Register.Files), linked, strings.Join(decl.Lifecycle.Active(), ", ")))

	return nil
}

// loadAdrModel loads the CALM architecture model of the workspace from local files and the pinned remote cache
// only. `adr validate` never uses the network, with or without --offline: a remote resource that is not in the
// lockfile and cache gives CO-INT-012.
func loadAdrModel(ctx context.Context, app *Context, wsp *workspace.Workspace) (*intent.ArchitectureModel, error) {
	if wsp.Architecture.Source == "" {
		return nil, diag.Fail(diag.WRK010, diag.Detail("no architecture: set architecture.source"))
	}

	res := app.remoteResolver(wsp)
	res.Offline = true

	local, _, err := remote.ResolvePath(ctx, res, wsp.Architecture.Source)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	archPath := local
	if !strings.HasPrefix(wsp.Architecture.Source, httpsPrefix) {
		archPath = wsp.ResolveInRoot(local)
	}

	urlMapping, err := intent.LoadURLMapping(wsp)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	patterns, err := resolveArchitectureFiles(ctx, res, wsp, wsp.Architecture.Patterns)
	if err != nil {
		return nil, err
	}

	decorators, err := resolveArchitectureFiles(ctx, res, wsp, wsp.Architecture.Decorators)
	if err != nil {
		return nil, err
	}

	loaded := intent.LoadArchitecture(archPath, intent.LoadContext{
		Root: wsp.Root, URLMapping: urlMapping, Patterns: patterns, Decorators: decorators, Trust: trust.FromWorkspace(wsp),
		ArchitectureURL: remoteArchitectureURL(wsp, ""),
		Remote:          func(u string) (string, string, string) { return res.Resolve(ctx, u) },
	})
	if err = policy.ThrowOnErrors(loaded.Diagnostics); err != nil {
		return nil, fmt.Errorf("load architecture: %w", err)
	}

	if loaded.Model == nil {
		return nil, diag.Fail(diag.INT001, diag.Detail("architecture not loaded"))
	}

	return loaded.Model, nil
}
