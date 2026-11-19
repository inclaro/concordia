package assessment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// Anchor is what Concordia reads from the trust anchor revision: the authority configuration, the trust anchor
// setting, the required assessments, and the `check.enforcement` entries. A candidate change cannot edit it for
// its own verification, because every field comes from git, not from the working tree.
//
// When the anchor cannot be read, Unreadable says why and the other fields are empty: nothing may be verified
// against it.
type Anchor struct {
	Config      *workspace.Assessments
	Unreadable  *VerifyUnknown
	Diagnostic  *diag.Diagnostic
	Ref         string
	Revision    string
	Head        string
	Enforcement []workspace.EnforcementEntry
}

// Readable says whether the anchor gives configuration.
func (a *Anchor) Readable() bool { return a != nil && a.Unreadable == nil }

// Required gives the required assessments of the anchor. It is empty when the anchor is unreadable or sets none.
func (a *Anchor) Required() []workspace.RequiredAssessment {
	if !a.Readable() || a.Config == nil {
		return []workspace.RequiredAssessment{}
	}

	return a.Config.Required
}

// Authority gives the authority with the id at the anchor.
func (a *Anchor) Authority(id string) (workspace.Authority, bool) {
	if !a.Readable() || a.Config == nil {
		return workspace.Authority{}, false
	}

	for i := range a.Config.Authorities {
		if a.Config.Authorities[i].ID == id {
			return a.Config.Authorities[i], true
		}
	}

	return workspace.Authority{}, false
}

// AnchorSource says where to read the anchor.
type AnchorSource struct {
	Git workspace.GitPort
	// Ref is the configured trust anchor reference. Empty means that none is configured.
	Ref string
	// WorkspacePath is the absolute OS path of the workspace file. Its content at the anchor is the
	// authority configuration.
	WorkspacePath string
	// Profile is the workspace profile that is merged, as for the working tree.
	Profile string
}

// fetchHint says how to get a remote-tracking anchor into a shallow clone. A plain `git fetch origin main` leaves out
// `origin/main` there, because a shallow clone tracks only its own branch, so the hint names the full refspec.
func fetchHint(ref string) string {
	branch, ok := strings.CutPrefix(ref, "origin/")
	if !ok {
		return "fetch the reference"
	}

	return "fetch the reference (in a shallow clone: git fetch --depth=<n> origin " + branch + ":refs/remotes/" + ref + ")"
}

// ReadAnchor reads the trust anchor from git. It resolves Ref, checks that the anchor is an ancestor of HEAD or
// the same commit, and loads the workspace file of that revision. It never gives an error: a problem makes the
// anchor unreadable (reason `trust-anchor-unreadable`) and is shown as CO-ASM-007. Nothing leaves the machine.
func ReadAnchor(ctx context.Context, src *AnchorSource) *Anchor {
	out := &Anchor{Ref: src.Ref, Enforcement: []workspace.EnforcementEntry{}}

	if src.Ref == "" {
		return out.unreadable("no trust anchor is configured (assessments.trustAnchor.ref)",
			"set assessments.trustAnchor.ref to the protected branch, for example origin/main")
	}

	revision, err := src.Git.ResolveRef(ctx, src.Ref)
	if err != nil {
		return out.unreadable(fmt.Sprintf("the trust anchor '%s' cannot be resolved: %s", src.Ref, errText(err)),
			fetchHint(src.Ref))
	}

	out.Revision = revision

	head, err := src.Git.Revision(ctx)
	if err != nil || head == "" {
		return out.unreadable("HEAD has no commit", "make a commit, or check out the candidate revision")
	}

	out.Head = head

	ancestor, err := src.Git.IsAncestor(ctx, revision, head)
	if err != nil {
		return out.unreadable(fmt.Sprintf("the trust anchor '%s' (%s) cannot be compared with HEAD: %s",
			src.Ref, shortRevision(revision), errText(err)),
			"fetch more history so that the anchor and HEAD are both present")
	}

	if !ancestor {
		return out.unreadable(fmt.Sprintf("the trust anchor '%s' (%s) is not an ancestor of HEAD (%s)",
			src.Ref, shortRevision(revision), shortRevision(head)),
			"rebase or merge the trust anchor into the candidate, or set the correct reference (in a shallow clone: git fetch --deepen=<n>)")
	}

	rel := filepath.ToSlash(filepath.Base(src.WorkspacePath))

	data, found, err := src.Git.ShowFile(ctx, revision, rel)

	switch {
	case err != nil:
		return out.unreadable(fmt.Sprintf("cannot read %s at the trust anchor: %s", rel, errText(err)), "")
	case !found:
		return out.unreadable(fmt.Sprintf("%s does not exist at the trust anchor '%s' (%s)", rel, src.Ref, shortRevision(revision)),
			"the workspace file with the authority configuration must be on the trust anchor first")
	}

	loaded, err := workspace.LoadBytes(src.WorkspacePath, data, src.Profile)
	if err != nil {
		return out.unreadable(fmt.Sprintf("%s at the trust anchor is not valid: %s", rel, errText(err)), "")
	}

	out.Config = loaded.Assessments
	out.Enforcement = slices.Clone(loaded.Check.Enforcement)

	if out.Enforcement == nil {
		out.Enforcement = []workspace.EnforcementEntry{}
	}

	return out
}

// unreadable marks the anchor unreadable with the detail and the fix.
func (a *Anchor) unreadable(detail, fix string) *Anchor {
	a.Config = nil
	a.Enforcement = []workspace.EnforcementEntry{}
	a.Unreadable = &VerifyUnknown{Reason: ReasonTrustAnchorUnreadable, Detail: detail}

	opts := []diag.Option{}
	if fix != "" {
		opts = append(opts, diag.WithSuggestion(fix))
	}

	d := diag.New(diag.ASM007, diag.Detail(detail), opts...)
	a.Diagnostic = &d

	return a
}

func errText(err error) string {
	var de *diag.Error
	if errors.As(err, &de) {
		return de.Diagnostic.Message
	}

	return err.Error()
}

func shortRevision(sha string) string {
	const shortLen = 12
	if len(sha) > shortLen {
		return sha[:shortLen]
	}

	return sha
}

// Difference is one place where the candidate working tree differs from the trust anchor in the inputs that
// verification reads.
type Difference struct {
	// Item names the part: "trustAnchor", "authority <id>", "required <id>", or "enforcement".
	Item   string
	Detail string
}

// Differences compares the working-tree workspace with the anchor and lists what the candidate changed:
// the trust anchor setting, the authorities, the required assessments, and the `check.enforcement` entries.
// It is empty when the candidate equals the anchor, and always empty when the anchor is unreadable (nothing
// can be compared; the unreadable anchor is reported on its own). The list is sorted.
func (a *Anchor) Differences(candidate *workspace.Workspace) []Difference {
	out := []Difference{}

	if !a.Readable() {
		return out
	}

	var anchorCfg, candCfg workspace.Assessments

	if a.Config != nil {
		anchorCfg = *a.Config
	}

	if candidate.Assessments != nil {
		candCfg = *candidate.Assessments
	}

	if !equalJSON(anchorCfg.TrustAnchor, candCfg.TrustAnchor) {
		out = append(out, Difference{Item: "trustAnchor", Detail: "the trust anchor setting differs from the anchor"})
	}

	out = append(out, diffByID("authority", anchorCfg.Authorities, candCfg.Authorities,
		func(v workspace.Authority) string { return v.ID })...)
	out = append(out, diffByID("required", anchorCfg.Required, candCfg.Required,
		func(v workspace.RequiredAssessment) string { return v.ID })...)

	if !equalJSON(a.Enforcement, nilToEmpty(candidate.Check.Enforcement)) {
		out = append(out, Difference{Item: "enforcement", Detail: "check.enforcement differs from the anchor"})
	}

	slices.SortFunc(out, func(x, y Difference) int {
		if c := canonjson.CompareUTF16(x.Item, y.Item); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(x.Detail, y.Detail)
	})

	return out
}

func nilToEmpty(list []workspace.EnforcementEntry) []workspace.EnforcementEntry {
	if list == nil {
		return []workspace.EnforcementEntry{}
	}

	return list
}

// diffByID lists the entries that are added, removed, or changed between the anchor and the candidate.
func diffByID[T any](kind string, anchor, candidate []T, id func(T) string) []Difference {
	byID := func(list []T) map[string]T {
		m := make(map[string]T, len(list))
		for _, v := range list {
			m[id(v)] = v
		}

		return m
	}

	left, right := byID(anchor), byID(candidate)
	out := []Difference{}

	for key, l := range left {
		r, ok := right[key]

		switch {
		case !ok:
			out = append(out, Difference{Item: kind + " " + key, Detail: "removed by the candidate"})
		case !equalJSON(l, r):
			out = append(out, Difference{Item: kind + " " + key, Detail: "changed by the candidate"})
		}
	}

	for key := range right {
		if _, ok := left[key]; !ok {
			out = append(out, Difference{Item: kind + " " + key, Detail: "added by the candidate"})
		}
	}

	return out
}

// equalJSON compares two values by their canonical JSON.
func equalJSON(a, b any) bool {
	x, errX := canonjson.Marshal(a)
	y, errY := canonjson.Marshal(b)

	return errX == nil && errY == nil && bytes.Equal(x, y)
}

// AuthorityDigest gives AuthorityDigest of the authorities that the anchor has. It is empty when the anchor is
// unreadable or has no `assessments` key.
func (a *Anchor) AuthorityDigest() string {
	if !a.Readable() || a.Config == nil {
		return ""
	}

	return AuthorityDigest(a.Config.Authorities)
}
