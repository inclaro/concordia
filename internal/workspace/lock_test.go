package workspace_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

type lockSetup struct {
	root     string
	lockPath string
	abs      string
	res      workspace.ResourceRef
}

func newLockSetup(t *testing.T) *lockSetup {
	t.Helper()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{extPath: "{\"a\":1}\n"})

	abs := filepath.Join(root, "schemas", "ext.json")

	return &lockSetup{
		root:     root,
		lockPath: filepath.Join(root, ".concordia.lock"),
		abs:      abs,
		res:      workspace.ResourceRef{URI: "https://example.com/ext.json", AbsPath: abs},
	}
}

func (s *lockSetup) ctx(offline bool, cbm string, resources ...workspace.ResourceRef) *workspace.VerifyContext {
	return &workspace.VerifyContext{Root: s.root, LockPath: s.lockPath, Offline: offline, CBMVersion: cbm, Resources: resources}
}

// writeLockText writes lockfile text to a temporary file and gives its path.
func writeLockText(t *testing.T, text string) string {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"lock": text})

	return filepath.Join(dir, "lock")
}

// expectReadLockRejected reports lockfile text that ReadLock does not reject with a CO-WRK-002 error and exit 2.
func expectReadLockRejected(t *testing.T, text string) {
	t.Helper()

	_, _, err := workspace.ReadLock(writeLockText(t, text))

	var de *diag.Error
	if !errors.As(err, &de) || de.Diagnostic.Code != codeWRK002 || de.ExitCode != diag.InvalidInput {
		t.Errorf("%q: err %v", text, err)
	}
}

// resourceURIs gives the URIs of the resources of the lock, in order.
func resourceURIs(lock *workspace.Lockfile) []string {
	uris := make([]string, 0, len(lock.Resources))
	for _, r := range lock.Resources {
		uris = append(uris, r.URI)
	}

	return uris
}

func TestLockRoundTripsThroughYAML(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Resources = append(lock.Resources,
		workspace.LockResource{URI: "https://b/x", Path: "b.json", SHA256: "sha256:ab"},
		workspace.LockResource{URI: uriA, Path: "a.json", SHA256: "sha256:cd"})

	mustWriteLock(t, s.lockPath, lock)

	back, found := mustReadLock(t, s.lockPath)
	requireEqual(t, "lock is found", found, true)
	expectEqual(t, "uris", resourceURIs(back), []string{uriA, "https://b/x"})
	expectEqual(t, "cbm", back.Resolved.CBM, workspace.LockCBM{ExecutableVersion: "0.12.1", AdapterSchema: "concordia-cbm/v2"})
	expectEqual(t, "calm schema version", back.Resolved.Calm.SchemaVersion, "1.2")
	expectEqual(t, "lock version", back.LockVersion, 1)
	expectEqual(t, "structurizr is absent", back.Resolved.Structurizr == nil, true)

	_, found, err := workspace.ReadLock(s.lockPath + ".missing")
	expectEqual(t, "missing lock is found", found, false)
	expectEqual(t, "missing lock error", err, error(nil))
}

func TestLockFileTextHasTheDocumentedKeys(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "6.1.0")
	lock.Resources = append(lock.Resources, workspace.LockResource{URI: uriA, Path: "a.json", SHA256: "sha256:cd"})

	mustWriteLock(t, s.lockPath, lock)

	text := testutil.ReadFile(t, s.lockPath)
	for _, want := range []string{
		"lockVersion: 1", "resolved:", "  calm:", "    schemaVersion: \"1.2\"", "  cbm:", "    executableVersion: 0.12.1",
		"    adapterSchema: concordia-cbm/v2", "  structurizr:", "    cliVersion: 6.1.0", "resources:", "  - uri: https://a/x", "    path: a.json", "    sha256: sha256:cd",
	} {
		expectContains(t, "lock text", text, want)
	}

	back, _ := mustReadLock(t, s.lockPath)
	requireEqual(t, "structurizr is given", back.Resolved.Structurizr != nil, true)
	expectEqual(t, "structurizr cli version", back.Resolved.Structurizr.CLIVersion, "6.1.0")
}

func TestReadLockRejectsBadShapes(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"lockVersion: 2\nresolved:\n  cbm: {executableVersion: a, adapterSchema: b}\nresources: []\n",
		"lockVersion: 1\nresolved:\n  cbm: {executableVersion: a}\nresources: []\n",
		"lockVersion: 1\nresolved:\n  cbm: {executableVersion: a, adapterSchema: b}\n",
		"lockVersion: 1\nresolved:\n  cbm: {executableVersion: a, adapterSchema: b}\nresources: [1]\n",
		"- a\n",
		"",
	} {
		expectReadLockRejected(t, text)
	}

	_, _, err := workspace.ReadLock(writeLockText(t, "a: [unclosed\n"))
	if err == nil || !strings.Contains(err.Error(), codeWRK002) {
		t.Errorf("err %v", err)
	}
}

func TestSanitizeURIStripsCredentials(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ uri, want string }{
		{"https://user:pw@host/p.json", "https://host/p.json"},
		{"https://host/p.json", "https://host/p.json"},
		{"relative/path.json", "relative/path.json"},
	} {
		expectEqual(t, "sanitized "+tc.uri, workspace.SanitizeURI(tc.uri), tc.want)
	}
}

func TestLockPathFor(t *testing.T) {
	t.Parallel()

	expectEqual(t, "lock path", workspace.LockPathFor("r"), filepath.Join("r", ".concordia.lock"))
}

func TestVerifyLockAddsUnlockedResourceWhenOnlineWithoutAbsolutePaths(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	lock, warnings := mustVerifyLock(t, workspace.NewLock("0.12.1", ""), s.ctx(false, "0.12.1", s.res))
	requireEqual(t, "warning count", len(warnings), 0)
	requireEqual(t, "resource count", len(lock.Resources), 1)

	text := testutil.ReadFile(t, s.lockPath)
	expectContains(t, "lock text", text, "path: schemas/ext.json")

	if strings.Contains(text, s.root) {
		t.Errorf("lock text has the root path:\n%s", text)
	}

	expectNoTempFiles(t, s.root)
}

func TestVerifyLockWithoutLockfileMakesOneOnlyWhenResourcesNeedLocking(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	lock, _ := mustVerifyLock(t, nil, s.ctx(false, "0.12.1"))
	requireEqual(t, "lock is absent", lock == nil, true)

	lock, _ = mustVerifyLock(t, nil, s.ctx(false, "0.12.1", s.res))
	requireEqual(t, "lock is given", lock != nil, true)
	requireEqual(t, "cbm version", lock.Resolved.CBM.ExecutableVersion, "0.12.1")
}

func TestVerifyLockDigestMismatchGivesINT012Exit6(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")

	sum, err := digest.OfFile(s.abs)
	if err != nil {
		t.Fatal(err)
	}

	lock.Resources = append(lock.Resources, workspace.LockResource{URI: s.res.URI, Path: extPath, SHA256: sum})
	testutil.WriteTree(t, s.root, map[string]string{extPath: "{\"a\":2}\n"})

	_, _, err = workspace.VerifyLock(lock, s.ctx(false, "0.12.1", s.res))

	de := failure(t, err)
	requireEqual(t, "exit code", de.ExitCode, diag.Network)
	requireEqual(t, "code", de.Diagnostic.Code, diag.INT012)
}

func TestVerifyLockOfflineUnlockedResourceGivesExit6AndWritesNothing(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	_, _, err := workspace.VerifyLock(workspace.NewLock("0.12.1", ""), s.ctx(true, "0.12.1", s.res))

	requireEqual(t, "exit code", failure(t, err).ExitCode, diag.Network)
	expectNoLockFile(t, s.lockPath, "lock was written")
}

func TestVerifyLockCollectsSeveralFailuresAsCauses(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	missing := workspace.ResourceRef{URI: "https://example.com/missing.json", AbsPath: filepath.Join(s.root, "missing.json")}

	_, _, err := workspace.VerifyLock(workspace.NewLock("0.12.1", ""), s.ctx(true, "0.12.1", s.res, missing))

	de := failure(t, err)
	requireEqual(t, "exit code", de.ExitCode, diag.Network)
	requireEqual(t, "cause count", len(de.Diagnostic.Cause), 1)
}

func TestVerifyLockCBMVersionChangeGivesWRK003Warning(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	_, warnings := mustVerifyLock(t, workspace.NewLock("0.12.1", ""), s.ctx(false, "0.12.2"))

	expectWRK003Warning(t, warnings)
	expectEqual(t, "warning severity", warnings[0].Severity, diag.SeverityWarning)
}

func TestVerifyLockDoesNotChangeItsInput(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	in := workspace.NewLock("0.12.1", "")

	mustVerifyLock(t, in, s.ctx(false, "0.12.1", s.res))

	expectEqual(t, "input resource count", len(in.Resources), 0)
}
