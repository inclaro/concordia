package workspace_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// verifyLockWarnings verifies the lock and fails the test on an error. It gives the warnings only.
func verifyLockWarnings(t *testing.T, lock *workspace.Lockfile, ctx *workspace.VerifyContext) []diag.Diagnostic {
	t.Helper()

	_, warnings := mustVerifyLock(t, lock, ctx)

	return warnings
}

// expectLockProfile checks that the profile of a lock has the wanted value.
func expectLockProfile(t *testing.T, what string, got, want *workspace.LockProfile) {
	t.Helper()

	requireEqual(t, what+" is given", got != nil, true)
	requireEqual(t, what, *got, *want)
}

func TestLockRecordsTheCapabilityProfile(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Resolved.CBM.Profile = &workspace.LockProfile{ID: "cbm-0.12.1", Digest: "sha256:aa"}

	mustWriteLock(t, s.lockPath, lock)

	back, _ := mustReadLock(t, s.lockPath)

	expectLockProfile(t, "profile", back.Resolved.CBM.Profile, lock.Resolved.CBM.Profile)
}

func TestVerifyLockProfileDigestMismatchGivesWRK003Warning(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Resolved.CBM.Profile = &workspace.LockProfile{ID: "cbm-0.12.1", Digest: "sha256:aa"}

	ctx := s.ctx(false, "0.12.1")
	ctx.CBMProfile = &workspace.LockProfile{ID: "cbm-0.12.1", Digest: "sha256:aa"}

	requireEqual(t, "equal profile warning count", len(verifyLockWarnings(t, lock, ctx)), 0)

	ctx.CBMProfile = &workspace.LockProfile{ID: "cbm-0.12.1", Digest: "sha256:bb"}

	warnings := verifyLockWarnings(t, lock, ctx)
	expectWRK003Warning(t, warnings)
	expectContains(t, "digest mismatch warning", warnings[0].Message, "sha256:bb")

	ctx.CBMProfile = nil

	requireEqual(t, "no profile found warning count", len(verifyLockWarnings(t, lock, ctx)), 1)

	// A lock that records no profile promises nothing.
	requireEqual(t, "lock without profile warning count", len(verifyLockWarnings(t, workspace.NewLock("0.12.1", ""), ctx)), 0)
}

func TestVerifyLockRecordsTheCapabilityProfileOfANewLock(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	ctx := s.ctx(false, "0.12.1", s.res)
	ctx.CBMProfile = &workspace.LockProfile{ID: "cbm-0.12.1", Digest: "sha256:aa"}

	lock, _ := mustVerifyLock(t, nil, ctx)
	requireEqual(t, "lock is given", lock != nil, true)
	expectLockProfile(t, "profile", lock.Resolved.CBM.Profile, ctx.CBMProfile)

	back, _ := mustReadLock(t, s.lockPath)
	requireEqual(t, "written lock is given", back != nil, true)
	expectLockProfile(t, "written profile", back.Resolved.CBM.Profile, ctx.CBMProfile)
}
