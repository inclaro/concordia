package cli

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fetch"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// fetchPack downloads a pack. Only `policy add` calls it.
func fetchPack(ctx context.Context, app *Context, source string) ([]byte, error) {
	shown := workspace.SanitizeURI(source)

	if app.Opts.Offline {
		return nil, diag.Fail(diag.POL016, diag.Detail("--offline is set: "+shown+" is not fetched"))
	}

	client := app.Deps.HTTPClient
	if client == nil {
		client = fetch.NewClient()
	}

	body, err := fetch.Get(ctx, client, source)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("fetch %s: %w", shown, ctx.Err())
		}

		return nil, diag.Fail(diag.POL016, diag.Detail(shown+": "+err.Error()))
	}

	if len(body) > maxPackBytes {
		return nil, diag.Fail(diag.POL016, diag.Detail(shown+": the pack is larger than 1 MiB"))
	}

	return body, nil
}

// readPackSource gives the text of the source of `policy add`: a file, or an https URL.
func readPackSource(ctx context.Context, app *Context, source string) ([]byte, error) {
	switch {
	case strings.HasPrefix(strings.ToLower(source), httpsPrefix):
		return fetchPack(ctx, app, source)
	case strings.Contains(source, "://"):
		return nil, usageError("the source " + workspace.SanitizeURI(source) + " is not supported: use builtin:<name>, a file, or an https:// URL")
	default:
		text, err := os.ReadFile(app.Abs(source))
		if err != nil {
			return nil, diag.Fail(diag.POL011, diag.Params{keyPath: fsutil.ToPosix(source)})
		}

		return text, nil
	}
}

// updateLockedPack sets the pack in the lockfile when the workspace has one. A
// new lockfile is not made here: the next `check` makes it.
func updateLockedPack(root string, pack workspace.LockPack) error {
	lockPath := workspace.LockPathFor(root)

	lock, found, err := workspace.ReadLock(lockPath)
	if err != nil || !found {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	lock.SetPack(pack)

	return workspace.WriteLock(lockPath, lock) //nolint:wrapcheck // a *diag.Error that the caller reports.
}

func policyAdd(ctx context.Context, app *Context, source string, opts *policyAddOptions) error {
	// The workspace must be valid before anything is written.
	if _, err := app.LoadWorkspace(); err != nil {
		return err
	}

	doc, err := openWorkspaceDoc(app.WorkspacePath)
	if err != nil {
		return err
	}

	adder := &packAdder{app: app, doc: doc, opts: opts}

	use, text, err := adder.resolve(ctx, source)
	if err != nil {
		return err
	}

	return adder.finish(use, text)
}

// packAdder adds a pack to the workspace file of `policy add`.
type packAdder struct {
	app  *Context
	doc  *workspaceDoc
	opts *policyAddOptions
}

// resolve gives the `use` value and the text of the pack of the source: a built-in pack,
// or a file or an https URL that it vendors.
func (a *packAdder) resolve(ctx context.Context, source string) (use string, text []byte, err error) {
	if name, builtin := strings.CutPrefix(source, packs.Prefix); builtin {
		return a.builtin(source, name)
	}

	if text, err = readPackSource(ctx, a.app, source); err != nil {
		return "", nil, err
	}

	use, err = a.vendor(source, text)

	return use, text, err
}

// builtin gives the `use` value and the text of a built-in pack.
func (a *packAdder) builtin(source, name string) (use string, text []byte, err error) {
	if name == views.PackID {
		return "", nil, diag.Fail(diag.POL001, diag.Detail(source+" is not added to policies.packs: "+viewsPackNote))
	}

	if a.opts.id != "" {
		return "", nil, usageError("--id does not apply to a built-in pack")
	}

	text, ok := packs.Source(name)
	if !ok {
		return "", nil, diag.Fail(diag.POL012, diag.Params{keyName: name})
	}

	return source, text, nil
}

// vendor validates a pack text and writes it into the vendor directory.
// It gives the `use` value: the workspace-relative path of the vendored file.
func (a *packAdder) vendor(source string, text []byte) (string, error) {
	info, problems := policy.DescribePack(policy.PolicyFile{Path: workspace.SanitizeURI(source), Text: string(text)})
	if err := policy.ThrowOnErrors(problems); err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	use, err := a.vendoredUse(&info)
	if err != nil {
		return "", err
	}

	target := a.app.Abs(use)

	if a.doc.find(use) != nil && !a.opts.force {
		return "", usageError(use + " is already in policies.packs: use --force to replace it")
	}

	unchanged, err := a.vendoredTargetUnchanged(use, target, text)
	if err != nil {
		return "", err
	}

	if unchanged {
		return use, nil
	}

	if err := fsutil.WriteFileAtomic(target, text); err != nil {
		return "", fmt.Errorf("write %s: %w", use, err)
	}

	return use, nil
}

// vendoredUse gives the workspace-relative path of the vendored file of a pack: its id (or --id) and version.
func (a *packAdder) vendoredUse(info *policy.PackInfo) (string, error) {
	name := cmp.Or(a.opts.id, info.ID)
	if !fileNamePart.MatchString(name) || !fileNamePart.MatchString(info.Version) {
		return "", usageError(fmt.Sprintf("pack %q version %q cannot be used in a file name: pass --id with letters, digits, '.', '_', '+' or '-'", name, info.Version))
	}

	return path.Join(vendorDir, name+"@"+info.Version+vendorSuffix), nil
}

// vendoredTargetUnchanged says whether the vendored file already holds the text. A file with
// other content is an error without --force.
func (a *packAdder) vendoredTargetUnchanged(use, target string, text []byte) (bool, error) {
	if existing, readErr := os.ReadFile(target); readErr == nil { //nolint:gosec // the path is inside the workspace vendor directory.
		if bytes.Equal(existing, text) {
			return true, nil
		}

		if !a.opts.force {
			return false, usageError(use + " exists with other content: use --force to replace it")
		}
	}

	return false, nil
}

// finish puts the entry in the workspace file and the lockfile, and reports it.
func (a *packAdder) finish(use string, text []byte) error {
	replaced := a.doc.find(use) != nil
	if replaced && !a.opts.force {
		return usageError(use + " is already in policies.packs: use --force to replace it")
	}

	sha := digest.OfBytes(text)

	entrySHA := sha
	if strings.HasPrefix(use, packs.Prefix) {
		entrySHA = ""
	}

	previous := a.doc.set(use, entrySHA)
	if err := a.doc.save(); err != nil {
		return err
	}

	info, _ := policy.DescribePack(policy.PolicyFile{Path: use, Text: string(text)})

	if err := updateLockedPack(filepath.Dir(a.app.WorkspacePath), workspace.LockPack{ID: info.ID, Version: info.Version, Source: use, SHA256: sha}); err != nil {
		return err
	}

	a.app.Out(addedPackMessage(use, replaced, previous, sha))

	return nil
}

// addedPackMessage says that the pack was added or updated, with the digests of an update.
func addedPackMessage(use string, replaced bool, previous, sha string) string {
	switch {
	case replaced && previous != "" && previous != sha:
		return "updated " + use + " (sha256 " + previous + " -> " + sha + ")\n"
	case replaced:
		return "updated " + use + "\n"
	}

	return "added " + use + "\n"
}
