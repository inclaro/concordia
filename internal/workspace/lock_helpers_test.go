package workspace_test

import (
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// mustWriteLock writes the lock and fails the test when the write fails.
func mustWriteLock(t *testing.T, path string, lock *workspace.Lockfile) {
	t.Helper()

	if err := workspace.WriteLock(path, lock); err != nil {
		t.Fatal(err)
	}
}

// mustReadLock reads the lock and fails the test when the read fails. It also reports whether the lock was found.
func mustReadLock(t *testing.T, path string) (*workspace.Lockfile, bool) {
	t.Helper()

	lock, found, err := workspace.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}

	return lock, found
}

// mustVerifyLock verifies the lock and fails the test when the verification fails. It gives the lock and the warnings.
func mustVerifyLock(t *testing.T, lock *workspace.Lockfile, ctx *workspace.VerifyContext) (*workspace.Lockfile, []diag.Diagnostic) {
	t.Helper()

	verified, warnings, err := workspace.VerifyLock(lock, ctx)
	if err != nil {
		t.Fatal(err)
	}

	return verified, warnings
}

// expectNoLockFile reports a lockfile at path. The reason says why the file must not be there.
func expectNoLockFile(t *testing.T, path, reason string) {
	t.Helper()

	if _, found, _ := workspace.ReadLock(path); found {
		t.Error(reason)
	}
}

// expectNoTempFiles reports every temporary file in dir.
func expectNoTempFiles(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file %s", e.Name())
		}
	}
}

// expectWRK003Warning reports a warning list that is not exactly one CO-WRK-003 warning.
func expectWRK003Warning(t *testing.T, warnings []diag.Diagnostic) {
	t.Helper()

	requireEqual(t, "warning count", len(warnings), 1)
	expectEqual(t, "warning code", warnings[0].Code, diag.WRK003)
}
