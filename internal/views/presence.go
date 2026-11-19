package views

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/inclaro/concordia/internal/workspace"
)

// The reasons of a presence result (spec view-currency, "Presence is checked in the tracked revision").
const (
	// ReasonNotCommitted: the file is in the working tree and not in the tracked revision.
	ReasonNotCommitted = "not committed"
	// ReasonIndexOnly: CBM reports a diagram for the path, and the tracked revision does not contain it (A14).
	ReasonIndexOnly = "only in the index workspace"
	// ReasonMissing: the file is nowhere.
	ReasonMissing = "missing"
	// ReasonViewIDMismatch: the committed manifest names another view.
	ReasonViewIDMismatch = "view ID mismatch"
	// ReasonScopeMismatch: the committed manifest has another scope than the definition.
	ReasonScopeMismatch = "scope mismatch"
	// ReasonManifestInvalid: the committed manifest cannot be read.
	ReasonManifestInvalid = "manifest not valid"
	// ReasonNoRevision: there is no tracked revision to list (no Git repository, no commit, or Git fails).
	ReasonNoRevision = "no tracked revision"
)

// The role of a file of a view.
const (
	RoleSource   = "source"
	RoleOutput   = "output"
	RoleExport   = "export"
	RoleManifest = "manifest"
)

// PresenceFile is the state of one file that a view needs.
type PresenceFile struct {
	Path string `json:"path"`
	Role string `json:"role"`
	// Reason is empty for a committed file with no problem.
	Reason string `json:"reason,omitempty"`
	// Detail explains the reason: for an absent file, that the repository listing of the revision is complete and
	// does not contain the path.
	Detail string `json:"detail,omitempty"`
	// Committed says that the file is in the tracked revision.
	Committed bool `json:"committed"`
}

// PresenceResult is the result of the presence check of one view (view-currency, "Four checks give independent
// results"). Reasons are sorted and have no repetition.
type PresenceResult struct {
	Status Status
	// Revision is the tracked revision that was listed. Empty when none could be.
	Revision string
	// Reasons are the plain phrases of the Reason constants.
	Reasons []string
	// Files are the files that the view needs, in declaration order.
	Files []PresenceFile
}

// PresenceInput is the input of CheckPresence.
type PresenceInput struct {
	View *Definition
	Git  workspace.GitPort
	// Root is the absolute workspace directory. It tells a file of the working tree (`not committed`) from one
	// that is nowhere.
	Root string
	// IndexedPaths are the paths that CBM reports a diagram for (DiagramPaths of the evidence). They are a
	// cross-check that only explains a violation: CBM never makes presence pass (design D4).
	IndexedPaths []string
}

// CheckPresence checks that the source file of an authored view, or the output, the exports and the manifest of a
// generated view, are in the tracked revision (`ListFilesAt` of the HEAD revision, not the working tree and not
// the index workspace of CBM) and that the committed manifest declares the view ID and the scope of the
// definition. If the revision cannot be listed the result is `unknown` with the reason and no file entry; it is
// never `pass` or `violation` then.
func CheckPresence(ctx context.Context, input *PresenceInput) PresenceResult {
	revision, err := input.Git.Revision(ctx)
	if err != nil {
		return presenceUnknown("", fmt.Sprintf("cannot read the revision: %v", err))
	}

	if revision == "" {
		return presenceUnknown("", "the workspace is not in a Git repository with a commit")
	}

	listing, err := input.Git.ListFilesAt(ctx, revision)
	if err != nil {
		return presenceUnknown(revision, fmt.Sprintf("cannot list the files of revision %s: %v", revision, err))
	}

	tree := make(map[string]bool, len(listing))
	for _, p := range listing {
		tree[p] = true
	}

	check := &presenceCheck{in: input, revision: revision, tree: tree, indexed: slices.Clone(input.IndexedPaths)}
	result := PresenceResult{Revision: revision, Files: []PresenceFile{}, Reasons: []string{}}

	for _, need := range presenceNeeds(input.View) {
		file := check.file(need.path, need.role)
		result.Files = append(result.Files, file)

		if file.Reason != "" {
			result.Reasons = append(result.Reasons, file.Reason)
		}
	}

	if input.View.Origin == OriginGenerated && input.View.Output != nil {
		reasons, unknown := check.manifest(ctx, input.View.ManifestPath())
		if unknown != "" {
			return presenceUnknown(revision, unknown)
		}

		result.Reasons = append(result.Reasons, reasons...)
	}

	slices.Sort(result.Reasons)
	result.Reasons = slices.Compact(result.Reasons)

	result.Status = StatusPass
	if len(result.Reasons) > 0 {
		result.Status = StatusViolation
	}

	return result
}

func presenceUnknown(revision, reason string) PresenceResult {
	return PresenceResult{Status: StatusUnknown, Revision: revision, Reasons: []string{ReasonNoRevision + ": " + reason}, Files: []PresenceFile{}}
}

type presenceNeed struct{ path, role string }

// presenceNeeds are the files that the presence of a view needs: the manifest is checked apart.
func presenceNeeds(view *Definition) []presenceNeed {
	if view.Origin == OriginAuthored {
		if view.Source == nil {
			return nil
		}

		return []presenceNeed{{path: view.Source.Path, role: RoleSource}}
	}

	if view.Output == nil {
		return nil
	}

	needs := []presenceNeed{{path: view.Output.Path, role: RoleOutput}}
	for _, e := range view.Output.Exports {
		needs = append(needs, presenceNeed{path: e.Path, role: RoleExport})
	}

	return append(needs, presenceNeed{path: view.ManifestPath(), role: RoleManifest})
}

type presenceCheck struct {
	in       *PresenceInput
	tree     map[string]bool
	revision string
	indexed  []string
}

// file classifies one needed file against the tracked revision.
func (c *presenceCheck) file(target, role string) PresenceFile {
	target = path.Clean(filepath.ToSlash(target))
	out := PresenceFile{Path: target, Role: role}

	if c.tree[target] {
		out.Committed = true

		return out
	}

	out.Detail = fmt.Sprintf("the repository listing of revision %s is complete and does not contain %s", c.revision, target)

	switch {
	case c.inWorkingTree(target):
		out.Reason = ReasonNotCommitted
	case slices.Contains(c.indexed, target):
		out.Reason = ReasonIndexOnly
	default:
		out.Reason = ReasonMissing
	}

	return out
}

func (c *presenceCheck) inWorkingTree(p string) bool {
	if c.in.Root == "" {
		return false
	}

	info, err := os.Stat(filepath.Join(c.in.Root, filepath.FromSlash(p)))

	return err == nil && info.Mode().IsRegular()
}

// manifest compares the committed manifest with the definition. A manifest that is not in the tree is reported by
// file(); a Git failure gives the reason that makes the whole result unknown.
func (c *presenceCheck) manifest(ctx context.Context, p string) (reasons []string, unknown string) {
	if !c.tree[p] {
		return nil, ""
	}

	data, found, err := c.in.Git.ShowFile(ctx, c.revision, p)
	if err != nil {
		return nil, fmt.Sprintf("cannot read %s at revision %s: %v", p, c.revision, err)
	}

	if !found {
		return nil, ""
	}

	manifest, err := ParseManifest(data)
	if err != nil {
		return []string{ReasonManifestInvalid}, ""
	}

	if manifest.View.ID != c.in.View.ID {
		reasons = append(reasons, ReasonViewIDMismatch)
	}

	if !slices.Equal(sortedCopy(manifest.View.Scope), sortedCopy(c.in.View.Scope)) {
		reasons = append(reasons, ReasonScopeMismatch)
	}

	return reasons, ""
}
