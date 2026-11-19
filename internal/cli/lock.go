package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/assurance/renderer"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fetch"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/workspace"
)

const shortDigest = 12

func newLockCmd(host *Host) *cobra.Command {
	lock := &cobra.Command{Use: "lock", Short: "Lockfile commands"}

	var uris []string

	update := &cobra.Command{
		Use:   "update",
		Short: "Download the locked remote resources again, re-pin the CALM schema release and the toolchain, and update the lockfile; report the digest impact of a toolchain change",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, lockUpdate(ctx, app, uris)
		}),
	}
	update.Flags().StringArrayVar(&uris, "uri", nil, "update only this resource (repeatable)")
	lock.AddCommand(update)

	return lock
}

func firstHex(sum string) string {
	sum = strings.TrimPrefix(sum, "sha256:")
	if len(sum) > shortDigest {
		return sum[:shortDigest]
	}

	return sum
}

// lockTarget is the workspace and the lockfile path that a lock update works on.
type lockTarget struct {
	wsp  *workspace.Workspace
	path string
}

// readLock reads the lockfile of the target again.
func (t lockTarget) readLock() (*workspace.Lockfile, error) {
	lock, _, err := workspace.ReadLock(t.path)
	if err != nil {
		return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return lock, nil
}

// cloneLockResolved copies the resolved section so that a later change to the CBM profile does not alias it.
func cloneLockResolved(lock *workspace.Lockfile) workspace.LockResolved {
	resolved := lock.Resolved
	if resolved.CBM.Profile != nil {
		profile := *resolved.CBM.Profile
		resolved.CBM.Profile = &profile
	}

	return resolved
}

// selectLockResources returns the remote resources to refresh, ordered by URI. --uri limits them to the named ones.
func selectLockResources(lock *workspace.Lockfile, uris []string) ([]*workspace.LockResource, error) {
	var selected []*workspace.LockResource

	for i := range lock.Resources {
		res := &lock.Resources[i]
		if strings.HasPrefix(res.Path, remote.CacheDir) && (len(uris) == 0 || slices.Contains(uris, res.URI)) {
			selected = append(selected, res)
		}
	}

	for _, u := range uris {
		if !slices.ContainsFunc(selected, func(res *workspace.LockResource) bool { return res.URI == u }) {
			return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("--uri %s is not a remote resource of the lockfile", workspace.SanitizeURI(u))))
		}
	}

	slices.SortFunc(selected, func(a, b *workspace.LockResource) int { return canonjson.CompareUTF16(a.URI, b.URI) })

	return selected, nil
}

func lockUpdate(ctx context.Context, app *Context, uris []string) error {
	if app.Opts.Offline {
		return diag.Fail(diag.WRK010, diag.Detail("lock update needs network access; --offline is set"))
	}

	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	target := lockTarget{wsp: wsp, path: workspace.LockPathFor(wsp.Root)}

	lock, found, err := workspace.ReadLock(target.path)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if !found {
		return diag.Fail(diag.WRK010, diag.Detail("no lockfile; run concordia check first"))
	}

	before := cloneLockResolved(lock)

	for i, u := range uris {
		uris[i] = workspace.SanitizeURI(u)
	}

	selected, err := selectLockResources(lock, uris)
	if err != nil {
		return err
	}

	if err := refreshResources(ctx, app, wsp.Root, selected); err != nil {
		return err
	}

	if err := workspace.WriteLock(target.path, lock); err != nil {
		return err
	}

	// --uri limits the update to the named resources.
	if len(uris) > 0 {
		return nil
	}

	return repinAll(ctx, app, target, &before)
}

// repinAll re-pins the CALM schema, the toolchain, and the renderers.
func repinAll(ctx context.Context, app *Context, target lockTarget, before *workspace.LockResolved) error {
	if err := repinCalmSchema(ctx, app, target); err != nil {
		return err
	}

	if err := repinToolchain(ctx, app, target, before); err != nil {
		return err
	}

	return repinRenderers(ctx, app, target)
}

// refreshResources downloads the selected lock resources again and updates their digests.
func refreshResources(ctx context.Context, app *Context, root string, selected []*workspace.LockResource) error {
	for _, res := range selected {
		data, err := fetch.Get(ctx, app.Deps.HTTPClient, res.URI)
		if err != nil {
			return diag.Fail(diag.INT013, diag.Detail(workspace.SanitizeURI(res.URI)+": "+err.Error()))
		}

		_, hexSum, err := remote.WriteCache(root, data)
		if err != nil {
			return fmt.Errorf("write the resource cache: %w", err)
		}

		// An entry without a valid digest is repaired: the old digest never names a path.
		newSum := "sha256:" + hexSum
		if newSum == res.SHA256 && remote.ValidDigest(res.SHA256) {
			app.Out("unchanged " + res.URI + "\n")

			continue
		}

		app.Out(fmt.Sprintf("updated %s %s -> %s\n", res.URI, firstHex(res.SHA256), firstHex(newSum)))

		res.SHA256 = newSum
		res.Path = remote.CacheDir + "/" + hexSum
	}

	return nil
}

// repinToolchain writes the CBM build, adapter schema, capability profile, and normalization version that this build
// of Concordia finds into the lock. When one of the pinned components changed, it writes the digest-impact report.
// The profile of a build that Concordia cannot run is kept: only a found CBM build changes it.
func repinToolchain(ctx context.Context, app *Context, target lockTarget, before *workspace.LockResolved) error {
	lock, err := target.readLock()
	if err != nil {
		return err
	}

	cbm := &lock.Resolved.CBM
	cbm.AdapterSchema = observation.AdapterSchema
	cbm.NormalizationVersion = observation.NormalizationVersion

	if found := defaultCbmBuild(ctx, app, target.wsp.Observation.Executable); found.Version != "" {
		cbm.ExecutableVersion = found.Version
		cbm.SHA256 = found.SHA256
		cbm.Profile = nil

		if profile, ok := observation.ProfileFor(found.Version, observation.AdapterSchema); ok {
			cbm.Profile = &workspace.LockProfile{ID: profile.ID, Digest: profile.Digest}
		}
	}

	if len(toolchainChanges(before, &lock.Resolved)) == 0 {
		return nil
	}

	if err := workspace.WriteLock(target.path, lock); err != nil {
		return err
	}

	return writeUpgradeImpact(ctx, app, target.wsp, before, &lock.Resolved)
}

// repinRenderers pins the renderers (helm, kustomize) that a declared rendering source uses: version and digest of the
// executable that is found now. A pin of a tool that no source uses any more is removed. A used renderer that cannot
// be found or run is a CO-PRV-003 error (exit 5): a pin cannot be written for it.
func repinRenderers(ctx context.Context, app *Context, target lockTarget) error {
	lock, err := target.readLock()
	if err != nil {
		return err
	}

	changed := false

	for _, tool := range renderer.Tools() {
		var toolChanged bool

		if target.wsp.Assurance.UsesRenderer(tool) {
			toolChanged, err = pinRenderer(ctx, app, lock, tool)
			if err != nil {
				return err
			}
		} else {
			toolChanged = dropRenderer(app, lock, tool)
		}

		changed = changed || toolChanged
	}

	if !changed {
		return nil
	}

	return workspace.WriteLock(target.path, lock)
}

// dropRenderer removes the pin of a tool that no source uses. It reports whether there was a pin.
func dropRenderer(app *Context, lock *workspace.Lockfile, tool string) bool {
	if lock.Renderer(tool) == nil {
		return false
	}

	lock.DropRenderer(tool)
	app.Out("removed " + tool + " pin: no source uses it\n")

	return true
}

// pinRenderer pins the executable of a used renderer that is found now. It reports whether the pin changed.
func pinRenderer(ctx context.Context, app *Context, lock *workspace.Lockfile, tool string) (bool, error) {
	before := lock.Renderer(tool)

	found, err := renderer.Probe(ctx, tool, app.IO.Getenv)
	if err != nil {
		return false, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	pin := found.Pin.Pin()

	switch {
	case before == nil:
		app.Out(fmt.Sprintf("pinned %s %s %s\n", tool, pin.Version, firstHex(pin.SHA256)))
	case *before == pin:
		app.Out(fmt.Sprintf("unchanged %s %s\n", tool, pin.Version))

		return false, nil
	default:
		app.Out(fmt.Sprintf("updated %s %s %s -> %s %s\n", tool, before.Version, firstHex(before.SHA256), pin.Version, firstHex(pin.SHA256)))
	}

	lock.SetRenderer(found.Pin)

	return true, nil
}

// repinCalmSchema writes the CALM release that the architecture declares into the lock.
func repinCalmSchema(ctx context.Context, app *Context, target lockTarget) error {
	wsp := target.wsp
	if wsp.Architecture.Source == "" {
		return nil
	}

	local, _, err := remote.ResolvePath(ctx, app.remoteResolver(wsp), wsp.Architecture.Source)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	archPath := local
	if !strings.HasPrefix(wsp.Architecture.Source, "https://") {
		archPath = wsp.ResolveInRoot(local)
	}

	version, err := intent.ReadSchemaVersion(archPath, wsp.Architecture.Source)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	// ResolvePath can add a remote architecture to the lock file: read it again.
	lock, err := target.readLock()
	if err != nil {
		return err
	}

	old := lock.Resolved.Calm.SchemaVersion
	if old == version {
		app.Out("unchanged calm schema " + version + "\n")

		return nil
	}

	app.Out(fmt.Sprintf("updated calm schema %s -> %s\n", old, version))
	lock.Resolved.Calm.SchemaVersion = version

	return workspace.WriteLock(target.path, lock)
}
