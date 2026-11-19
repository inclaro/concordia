package workspace_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	cbmSHA      = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	otherCbmSHA = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	cbmPath     = "/opt/cbm/codebase-memory-mcp"
)

// cbmDigestCtx is a verify context for a CBM executable with the given digest.
func cbmDigestCtx(s *lockSetup, sum string) *workspace.VerifyContext {
	ctx := s.ctx(false, "0.12.1")
	ctx.CBMExecutable, ctx.CBMSHA256 = cbmPath, sum

	return ctx
}

func TestLockCBMDigestRoundTripsAndHasItsKey(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Resolved.CBM.SHA256 = cbmSHA

	mustWriteLock(t, s.lockPath, lock)

	expectContains(t, "lock text", testutil.ReadFile(t, s.lockPath), "    sha256: "+cbmSHA)

	back, _ := mustReadLock(t, s.lockPath)
	expectEqual(t, "cbm digest", back.Resolved.CBM.SHA256, cbmSHA)
}

func TestLockWithoutCBMDigestWritesNoKeyAndLoads(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	mustWriteLock(t, s.lockPath, workspace.NewLock("0.12.1", ""))

	if text := testutil.ReadFile(t, s.lockPath); strings.Contains(text, "sha256") {
		t.Errorf("lock text has a sha256 key:\n%s", text)
	}

	back, found := mustReadLock(t, s.lockPath)
	requireEqual(t, "lock is found", found, true)
	expectEqual(t, "cbm digest", back.Resolved.CBM.SHA256, "")
}

func TestReadLockRejectsBadCBMDigests(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"abc",
		"sha256:abc",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef",
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdefaa",
		"5",
		"[a]",
	} {
		expectReadLockRejected(t, "lockVersion: 1\nresolved:\n  cbm: {executableVersion: a, adapterSchema: b, sha256: "+bad+"}\nresources: []\n")
	}
}

func TestVerifyLockCBMDigestMismatchGivesWRK003NamingBothDigestsAndThePath(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Resolved.CBM.SHA256 = cbmSHA

	_, warnings := mustVerifyLock(t, lock, cbmDigestCtx(s, otherCbmSHA))

	expectWRK003Warning(t, warnings)

	for _, want := range []string{cbmSHA, otherCbmSHA, cbmPath, "concordia lock update"} {
		expectContains(t, "warning", warnings[0].Message, want)
	}

	expectEqual(t, "warning severity", warnings[0].Severity, diag.SeverityWarning)
}

func TestVerifyLockCBMDigestGivesNoWarningWhenEqualUnpinnedOrNotFound(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	pinned := workspace.NewLock("0.12.1", "")
	pinned.Resolved.CBM.SHA256 = cbmSHA

	_, equal := mustVerifyLock(t, pinned, cbmDigestCtx(s, cbmSHA))
	expectEqual(t, "warnings of an equal digest", len(equal), 0)

	_, unpinned := mustVerifyLock(t, workspace.NewLock("0.12.1", ""), cbmDigestCtx(s, otherCbmSHA))
	expectEqual(t, "warnings of an unpinned lock", len(unpinned), 0)

	_, noExecutable := mustVerifyLock(t, pinned, cbmDigestCtx(s, ""))
	expectEqual(t, "warnings when the client has no executable", len(noExecutable), 0)
}

func TestVerifyLockMakesNewLockWithTheCBMDigest(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	ctx := cbmDigestCtx(s, cbmSHA)
	ctx.Resources = []workspace.ResourceRef{s.res}

	lock, _ := mustVerifyLock(t, nil, ctx)
	requireEqual(t, "lock is given", lock != nil, true)
	expectEqual(t, "cbm digest", lock.Resolved.CBM.SHA256, cbmSHA)
}
