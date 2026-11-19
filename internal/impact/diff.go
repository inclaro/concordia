package impact

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// ChangeKind classifies a change of one item against the approval.
type ChangeKind string

// Change kinds.
const (
	ChangeAdded    ChangeKind = "added"
	ChangeRemoved  ChangeKind = "removed"
	ChangeModified ChangeKind = "modified"
)

// Entry is an item reduced to what a comparison needs: its key and digest. The approval manifest
// records entries; the current side builds them with Sealed.Entry. A non-empty Reason means that
// the digest cannot be told (for example `reference-unresolved`), and Digest is empty.
type Entry struct {
	Kind     Kind   `json:"kind"`
	Identity string `json:"identity"`
	Digest   string `json:"digest,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Change is one difference between the approval and the current items.
type Change struct {
	Op             ChangeKind `json:"change"`
	Kind           Kind       `json:"kind"`
	Identity       string     `json:"identity"`
	BaselineDigest string     `json:"baselineDigest,omitempty"`
	CurrentDigest  string     `json:"currentDigest,omitempty"`
}

// DispositionType is how a verified approval accounts for a removed item.
type DispositionType string

// Disposition types.
const (
	DispositionRemovedIntentionally DispositionType = "removed-intentionally"
	DispositionRetired              DispositionType = "retired"
	DispositionMoved                DispositionType = "moved"
)

// Disposition accounts for a removed item. Only a disposition that a verified approval binds
// counts: the caller sets Verified after authority verification, and Diff ignores the others.
type Disposition struct {
	// Kind narrows the disposition to one dependency kind. Empty matches every kind.
	Kind Kind `json:"kind,omitempty"`
	// Identity is the identity of the removed item.
	Identity string `json:"identity"`
	// Type is the disposition type.
	Type DispositionType `json:"type"`
	// MovedTo is the new identity. Required for `moved`, empty otherwise.
	MovedTo string `json:"movedTo,omitempty"`
	// Verified says that a verified approval binds the disposition.
	Verified bool `json:"verified"`
}

// Valid says whether the type is one of the three and MovedTo agrees with it.
func (d Disposition) Valid() bool {
	switch d.Type {
	case DispositionMoved:
		return d.MovedTo != ""
	case DispositionRemovedIntentionally, DispositionRetired:
		return d.MovedTo == ""
	default:
		return false
	}
}

func (d Disposition) matches(removed *Change) bool {
	return d.Identity == removed.Identity && (d.Kind == "" || d.Kind == removed.Kind)
}

// Disposed is a removed item with the disposition that accounts for it.
type Disposed struct {
	Change      Change      `json:"change"`
	Disposition Disposition `json:"disposition"`
}

// DiffResult is the comparison of the approval manifest with the current items. Every list is
// sorted by kind and identity and never nil.
type DiffResult struct {
	// Added are items that only the current side has (everything, when there is no approval).
	Added []Change `json:"added"`
	// Removed are items that only the approval recorded, including the disposed ones.
	Removed []Change `json:"removed"`
	// Modified are items on both sides with different digests.
	Modified []Change `json:"modified"`
	// Unchanged are items on both sides with equal digests.
	Unchanged []Entry `json:"unchanged"`
	// Unknown are current items whose digest cannot be told. A comparison that needs them cannot run.
	Unknown []Entry `json:"unknown"`
	// Disposed are removed items that a verified disposition accounts for.
	Disposed []Disposed `json:"disposed"`
	// Undisposed are removed items without a verified disposition: historical subjects that block
	// (rule `REV-DSP-001`, one obligation each).
	Undisposed []Change `json:"undisposed"`
}

// Diff compares the items that the approval bound (baseline) with the current items. The compared
// set is the union of both sides, so a selector that now matches fewer items, a deleted binding, a
// removed control, or a reference whose target declaration was deleted shows as `removed`; the
// missing item is never read as no change. An empty baseline (no approval) makes every current
// item `added`.
//
// A removed item stays in Undisposed until a verified disposition for its identity exists.
func Diff(baseline, current []Entry, dispositions []Disposition) DiffResult {
	res := DiffResult{
		Added: []Change{}, Removed: []Change{}, Modified: []Change{}, Unchanged: []Entry{},
		Unknown: []Entry{}, Disposed: []Disposed{}, Undisposed: []Change{},
	}

	type key struct {
		kind     Kind
		identity string
	}

	base := make(map[key]Entry, len(baseline))
	for _, e := range baseline {
		base[key{e.Kind, e.Identity}] = e
	}

	cur := make(map[key]Entry, len(current))
	for _, e := range current {
		cur[key{e.Kind, e.Identity}] = e
	}

	for k, now := range cur {
		b, had := base[k]

		switch {
		case now.Reason != "":
			res.Unknown = append(res.Unknown, now)
		case !had:
			res.Added = append(res.Added, Change{Op: ChangeAdded, Kind: k.kind, Identity: k.identity, CurrentDigest: now.Digest})
		case b.Digest != now.Digest:
			res.Modified = append(res.Modified, Change{
				Op: ChangeModified, Kind: k.kind, Identity: k.identity, BaselineDigest: b.Digest, CurrentDigest: now.Digest,
			})
		default:
			res.Unchanged = append(res.Unchanged, now)
		}
	}

	for k, b := range base {
		if _, still := cur[k]; still {
			continue
		}

		res.Removed = append(res.Removed, Change{Op: ChangeRemoved, Kind: k.kind, Identity: k.identity, BaselineDigest: b.Digest})
	}

	byChange := func(a, b Change) int { return compareKey(a.Kind, a.Identity, b.Kind, b.Identity) }
	slices.SortFunc(res.Added, byChange)
	slices.SortFunc(res.Removed, byChange)
	slices.SortFunc(res.Modified, byChange)
	slices.SortFunc(res.Unchanged, func(a, b Entry) int { return compareKey(a.Kind, a.Identity, b.Kind, b.Identity) })
	slices.SortFunc(res.Unknown, func(a, b Entry) int { return compareKey(a.Kind, a.Identity, b.Kind, b.Identity) })

	for _, removed := range res.Removed {
		if d, ok := findDisposition(dispositions, &removed); ok {
			res.Disposed = append(res.Disposed, Disposed{Change: removed, Disposition: d})
		} else {
			res.Undisposed = append(res.Undisposed, removed)
		}
	}

	return res
}

// findDisposition gives the verified, valid disposition for a removed item. Several matches give
// the one that sorts first by type, then by moved-to identity, so the choice is stable.
func findDisposition(all []Disposition, removed *Change) (Disposition, bool) {
	var matches []Disposition

	for _, d := range all {
		if d.Verified && d.Valid() && d.matches(removed) {
			matches = append(matches, d)
		}
	}

	if len(matches) == 0 {
		return Disposition{}, false
	}

	slices.SortFunc(matches, func(a, b Disposition) int {
		if c := canonjson.CompareUTF16(string(a.Type), string(b.Type)); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.MovedTo, b.MovedTo)
	})

	return matches[0], true
}

// Triggers are the changes that make an assessment review-required: added and modified items, and
// removed items without a verified disposition. Sorted by kind and identity.
func (r *DiffResult) Triggers() []Change {
	out := make([]Change, 0, len(r.Added)+len(r.Modified)+len(r.Undisposed))
	out = append(out, r.Added...)
	out = append(out, r.Modified...)
	out = append(out, r.Undisposed...)

	slices.SortFunc(out, func(a, b Change) int { return compareKey(a.Kind, a.Identity, b.Kind, b.Identity) })

	return out
}

// Seeds gives the Triggers as the start nodes of a traversal.
func (r *DiffResult) Seeds() []Seed {
	triggers := r.Triggers()
	out := make([]Seed, 0, len(triggers))

	for _, c := range triggers {
		out = append(out, Seed{Node: ItemNodeID(c.Kind, c.Identity), Op: c.Op})
	}

	return out
}
