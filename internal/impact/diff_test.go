package impact_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

func entry(kind impact.Kind, identity, digest string) impact.Entry {
	return impact.Entry{Kind: kind, Identity: identity, Digest: digest}
}

func workloads(names ...string) []impact.Entry {
	out := make([]impact.Entry, 0, len(names))
	for _, n := range names {
		out = append(out, entry(impact.KindRuntimeIdentity, "prod/deploy/"+n, "sha256:"+n))
	}

	return out
}

func identities(changes []impact.Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Identity)
	}

	return out
}

// diffTestSingleChange says whether there is exactly one change and its projected fields are want.
func diffTestSingleChange(changes []impact.Change, fields func(*impact.Change) []string, want ...string) bool {
	return len(changes) == 1 && slices.Equal(fields(&changes[0]), want)
}

func diffTestModificationFields(c *impact.Change) []string {
	return []string{c.Identity, c.BaselineDigest, c.CurrentDigest}
}

func diffTestRemovalFields(c *impact.Change) []string {
	return []string{string(c.Kind), c.BaselineDigest}
}

// Scenario: SEC-04 no approval.
func TestDiffEmptyBaselineIsAllAdded(t *testing.T) {
	res := impact.Diff(nil, workloads("a", "b"), nil)

	if len(res.Added) != 2 || len(res.Removed)+len(res.Modified)+len(res.Unchanged) != 0 {
		t.Fatalf("res = %+v", res)
	}

	if got := res.Triggers(); len(got) != 2 {
		t.Errorf("triggers = %v", got)
	}
}

// Scenario: SEC-04 comparison basis is the approval.
func TestDiffComparesWithApprovalNotPreviousCommit(t *testing.T) {
	baseline := []impact.Entry{entry(impact.KindPublicEntryPoint, "prod/ingress/api", "sha256:r1"), entry(impact.KindProtocol, "p", "sha256:p")}
	current := []impact.Entry{entry(impact.KindPublicEntryPoint, "prod/ingress/api", "sha256:r2"), entry(impact.KindProtocol, "p", "sha256:p")}

	res := impact.Diff(baseline, current, nil)
	if !diffTestSingleChange(res.Modified, diffTestModificationFields, "prod/ingress/api", "sha256:r1", "sha256:r2") {
		t.Fatalf("modified = %+v", res.Modified)
	}

	if len(res.Unchanged) != 1 || res.Unchanged[0].Identity != "p" {
		t.Errorf("unchanged = %+v", res.Unchanged)
	}
}

// Scenario: A23 selector becomes narrower.
func TestA23NarrowerSelectorKeepsThirdWorkloadAsRemoved(t *testing.T) {
	res := impact.Diff(workloads("a", "b", "c"), workloads("a", "b"), nil)

	if got := identities(res.Removed); !slices.Equal(got, []string{"prod/deploy/c"}) {
		t.Fatalf("removed = %v", got)
	}

	if got := identities(res.Undisposed); !slices.Equal(got, []string{"prod/deploy/c"}) {
		t.Errorf("undisposed = %v", got)
	}

	if len(res.Triggers()) != 1 || res.Triggers()[0].Op != impact.ChangeRemoved {
		t.Errorf("triggers = %+v", res.Triggers())
	}
}

// Scenario: A23 binding deleted.
func TestA23DeletedBindingIsRemoval(t *testing.T) {
	baseline := []impact.Entry{entry(impact.KindBinding, "bind/api->svc", "sha256:b"), entry(impact.KindProtocol, "p", "sha256:p")}
	current := []impact.Entry{entry(impact.KindProtocol, "p", "sha256:p")}

	res := impact.Diff(baseline, current, nil)

	if !diffTestSingleChange(res.Removed, diffTestRemovalFields, string(impact.KindBinding), "sha256:b") {
		t.Fatalf("removed = %+v", res.Removed)
	}

	if len(res.Undisposed) != 1 {
		t.Error("a deleted binding must stay in the output")
	}
}

// Scenario: Reference target declaration removed.
func TestRemovedReferenceTargetIsNotNoChange(t *testing.T) {
	baseline := []impact.Entry{entry(impact.KindSecretReference, "prod/deploy/api/env/DB", "sha256:s")}

	res := impact.Diff(baseline, nil, nil)
	if !slices.Equal([]int{len(res.Removed), len(res.Triggers()), len(res.Unchanged)}, []int{1, 1, 0}) {
		t.Fatalf("res = %+v", res)
	}
}

// Scenario: A23 mandatory control removed.
// Scenario: Disposition without authority.
func TestA23RemovedControlBlocksUntilVerifiedDisposition(t *testing.T) {
	baseline := []impact.Entry{entry(impact.KindControl, "ctl/mtls", "sha256:c")}

	d := impact.Disposition{Identity: "ctl/mtls", Type: impact.DispositionRemovedIntentionally}

	// No verified approval binds the disposition: the removed item still blocks.
	res := impact.Diff(baseline, nil, []impact.Disposition{d})
	if len(res.Undisposed) != 1 || len(res.Disposed) != 0 {
		t.Fatalf("unverified disposition counted: %+v", res)
	}

	// An invalid disposition (moved without a new identity) does not count either.
	bad := impact.Disposition{Identity: "ctl/mtls", Type: impact.DispositionMoved, Verified: true}
	if diffTestUndisposedCount(baseline, bad) != 1 {
		t.Fatal("moved without moved-to counted")
	}

	// A disposition for another identity does not count.
	other := impact.Disposition{Identity: "ctl/other", Type: impact.DispositionRetired, Verified: true}
	if diffTestUndisposedCount(baseline, other) != 1 {
		t.Fatal("disposition of another identity counted")
	}

	// Scenario: Disposition recorded.
	d.Verified = true

	res = impact.Diff(baseline, nil, []impact.Disposition{d})
	if !diffTestDisposedAs(&res, impact.DispositionRemovedIntentionally) {
		t.Fatalf("verified disposition not applied: %+v", res)
	}

	// The removal itself stays in the output and no longer triggers.
	if !slices.Equal([]int{len(res.Removed), len(res.Triggers()), len(res.Seeds())}, []int{1, 0, 0}) {
		t.Errorf("removed=%+v triggers=%+v", res.Removed, res.Triggers())
	}
}

// diffTestUndisposedCount is the number of removed items that stay undisposed after the baseline
// is compared with nothing under one disposition.
func diffTestUndisposedCount(baseline []impact.Entry, d impact.Disposition) int {
	res := impact.Diff(baseline, nil, []impact.Disposition{d})

	return len(res.Undisposed)
}

// diffTestDisposedAs says whether exactly one removed item is disposed, with the given type.
func diffTestDisposedAs(res *impact.DiffResult, want impact.DispositionType) bool {
	if !slices.Equal([]int{len(res.Undisposed), len(res.Disposed)}, []int{0, 1}) {
		return false
	}

	return res.Disposed[0].Disposition.Type == want
}

func TestDispositionTypes(t *testing.T) {
	baseline := []impact.Entry{entry(impact.KindRoleBinding, "rb/old", "sha256:1"), entry(impact.KindRoleBinding, "rb/gone", "sha256:2")}

	res := impact.Diff(baseline, nil, []impact.Disposition{
		{Kind: impact.KindRoleBinding, Identity: "rb/old", Type: impact.DispositionMoved, MovedTo: "rb/new", Verified: true},
		{Kind: impact.KindControl, Identity: "rb/gone", Type: impact.DispositionRetired, Verified: true}, // wrong kind: no match
		{Identity: "rb/gone", Type: impact.DispositionRetired, Verified: true},
	})

	if len(res.Disposed) != 2 || len(res.Undisposed) != 0 {
		t.Fatalf("res = %+v", res)
	}

	if d := res.Disposed[1].Disposition; d.Type != impact.DispositionMoved || d.MovedTo != "rb/new" {
		t.Errorf("disposed = %+v", res.Disposed)
	}

	for _, d := range []impact.Disposition{
		{Identity: "x", Type: impact.DispositionRetired, MovedTo: "y"},
		{Identity: "x", Type: "deleted"},
	} {
		if d.Valid() {
			t.Errorf("%+v must be invalid", d)
		}
	}
}

func TestDiffUnknownCurrentItemsAreNotChanges(t *testing.T) {
	baseline := []impact.Entry{entry(impact.KindImplementation, "authz", "sha256:1")}
	current := []impact.Entry{{Kind: impact.KindImplementation, Identity: "authz", Reason: impact.ReasonReferenceUnresolved}}

	res := impact.Diff(baseline, current, nil)
	if len(res.Unknown) != 1 || len(res.Removed)+len(res.Modified)+len(res.Added)+len(res.Unchanged) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestDiffIsSortedAndNeverNil(t *testing.T) {
	res := impact.Diff(nil, nil, nil)
	if diffTestHasNilList(&res) {
		t.Fatalf("nil list: %+v", res)
	}

	current := []impact.Entry{entry(impact.KindProtocol, "z", "1"), entry(impact.KindBinding, "m", "1"), entry(impact.KindProtocol, "a", "1")}
	res = impact.Diff(nil, current, nil)

	got := make([]string, 0, len(res.Added))
	for _, c := range res.Added {
		got = append(got, string(c.Kind)+"/"+c.Identity)
	}

	if want := []string{"binding/m", "protocol/a", "protocol/z"}; !slices.Equal(got, want) {
		t.Errorf("order = %v", got)
	}

	// Triggers merge the three lists in one order.
	baseline := []impact.Entry{entry(impact.KindProtocol, "r", "1"), entry(impact.KindProtocol, "q", "1")}
	res = impact.Diff(baseline, []impact.Entry{entry(impact.KindProtocol, "a", "1"), entry(impact.KindProtocol, "q", "2")}, nil)

	triggers := res.Triggers()

	ids := make([]string, 0, len(triggers))
	for _, c := range triggers {
		ids = append(ids, c.Identity+":"+string(c.Op))
	}

	if want := []string{"a:added", "q:modified", "r:removed"}; !slices.Equal(ids, want) {
		t.Errorf("triggers = %v", ids)
	}
}

// diffTestHasNilList says whether any list of the result is nil.
func diffTestHasNilList(res *impact.DiffResult) bool {
	changeLists := [][]impact.Change{res.Added, res.Removed, res.Modified, res.Undisposed}
	if slices.ContainsFunc(changeLists, func(list []impact.Change) bool { return list == nil }) {
		return true
	}

	entryLists := [][]impact.Entry{res.Unchanged, res.Unknown}
	if slices.ContainsFunc(entryLists, func(list []impact.Entry) bool { return list == nil }) {
		return true
	}

	return res.Disposed == nil
}
