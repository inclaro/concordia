package workspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	packSHA   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherSHA  = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	packsBase = `
apiVersion: concordia.io/v1alpha1
kind: Workspace
observation:
  projects:
    - name: demo
`
	vendorUse = "policies/vendor/acme-arch-1.2.0.policy.yaml"
	coreUse   = "builtin:core"
)

// packsText gives the workspace text with the packs entries under `policies.packs`.
func packsText(packs string) string {
	return packsBase + "policies:\n  packs:\n    " + packs + "\n"
}

// causeMessages joins the messages of the causes of the diagnostic, one per line.
func causeMessages(de *diag.Error) string {
	var joined strings.Builder
	for _, c := range de.Diagnostic.Cause {
		joined.WriteString(c.Message + "\n")
	}

	return joined.String()
}

// expectSeverityOverride checks a rule override that sets a severity and leaves the rule enabled flag unset.
func expectSeverityOverride(t *testing.T, got workspace.RuleOverride) {
	t.Helper()

	expectEqual(t, "severity override", got.Severity, "warning")
	expectEqual(t, "severity override enabled is unset", got.Enabled == nil, true)
}

// expectDisabledOverride checks a rule override that turns the rule off.
func expectDisabledOverride(t *testing.T, got workspace.RuleOverride) {
	t.Helper()

	requireEqual(t, "enabled override is set", got.Enabled != nil, true)
	expectEqual(t, "enabled override", *got.Enabled, false)
}

// expectEvidenceOverride checks a rule override that sets an owner, a minimum coverage and an on-unknown action.
func expectEvidenceOverride(t *testing.T, cyc workspace.RuleOverride) {
	t.Helper()

	expectEqual(t, "evidence override owner", cyc.Owner, "team-platform")
	requireEqual(t, "evidence override is set", cyc.Evidence != nil, true)
	requireEqual(t, "minimum coverage is set", cyc.Evidence.MinimumCoverage != nil, true)
	expectEqual(t, "minimum coverage", *cyc.Evidence.MinimumCoverage, 0.8)
	expectEqual(t, "on unknown", cyc.Evidence.OnUnknown, "warn")
}

func TestPoliciesPacksDefaultToEmpty(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, packsBase, "")

	expectEqual(t, "packs are an empty list", w.Policies.Packs != nil && len(w.Policies.Packs) == 0, true)
}

func TestPoliciesPacksAreNormalized(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, packsBase+`
policies:
  files: [rules/**/*.policy.yaml]
  packs:
    - use: builtin:core
      rules:
        CORE-DECL-001: { severity: warning }
        CORE-SEC-001: { enabled: false }
        CORE-CYC-001: { evidence: { minimumCoverage: 0.8, onUnknown: warn }, owner: team-platform }
    - use: `+vendorUse+`
      sha256: `+packSHA+`
`, "")

	requireEqual(t, "pack count", len(w.Policies.Packs), 2)
	requireEqual(t, "first policy file", w.Policies.Files[0], "rules/**/*.policy.yaml")

	core := w.Policies.Packs[0]
	requireEqual(t, "core use", core.Use, coreUse)
	requireEqual(t, "core sha256", core.SHA256, "")
	requireEqual(t, "core rule count", len(core.Rules), 3)

	expectSeverityOverride(t, core.Rules["CORE-DECL-001"])
	expectDisabledOverride(t, core.Rules["CORE-SEC-001"])
	expectEvidenceOverride(t, core.Rules["CORE-CYC-001"])

	vendor := w.Policies.Packs[1]
	expectEqual(t, "vendor use", vendor.Use, vendorUse)
	expectEqual(t, "vendor sha256", vendor.SHA256, packSHA)
}

func TestPoliciesPacksReplaceByProfile(t *testing.T) {
	t.Parallel()

	text := packsBase + `
policies:
  packs:
    - use: builtin:core
profiles:
  strict:
    policies:
      packs:
        - use: ` + vendorUse + `
          sha256: ` + packSHA + `
`

	base := mustLoadWorkspace(t, text, "")
	strict := mustLoadWorkspace(t, text, "strict")

	requireEqual(t, "base pack count", len(base.Policies.Packs), 1)
	expectEqual(t, "base pack", base.Policies.Packs[0].Use, coreUse)
	requireEqual(t, "strict pack count (the profile list replaces the base list)", len(strict.Policies.Packs), 1)
	expectEqual(t, "strict pack (the profile list replaces the base list)", strict.Policies.Packs[0].Use, vendorUse)
}

func TestPoliciesPacksSchemaErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ text, want string }{
		"missing use":      {packsText("- sha256: " + packSHA), "use"},
		"bad digest":       {packsText("- use: " + vendorUse + "\n      sha256: abc"), "sha256"},
		"unknown key":      {packsText("- use: builtin:core\n      version: 1"), "version"},
		"bad severity":     {packsText("- use: builtin:core\n      rules:\n        CORE-A-001: { severity: fatal }"), "severity"},
		"bad rule id":      {packsText("- use: builtin:core\n      rules:\n        lower: { enabled: false }"), "lower"},
		"bad enabled":      {packsText("- use: builtin:core\n      rules:\n        CORE-A-001: { enabled: maybe }"), "enabled"},
		"unknown override": {packsText("- use: builtin:core\n      rules:\n        CORE-A-001: { name: x }"), "name"},
		"bad coverage":     {packsText("- use: builtin:core\n      rules:\n        CORE-A-001: { evidence: { minimumCoverage: 2 } }"), "minimumCoverage"},
		"not a list":       {packsBase + "policies:\n  packs: builtin:core\n", ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			de := loadFailure(t, tc.text)

			requireEqual(t, "code", de.Diagnostic.Code, codeWRK002)
			requireEqual(t, "exit code", de.ExitCode, diag.InvalidInput)
			expectContains(t, "message", de.Error(), "schema error")
			expectContains(t, "causes", causeMessages(de), tc.want)
		})
	}
}

// ---------------------------------------------------------------------------------------------
// Lockfile packs

// expectLockKeyOrder checks that the lock text starts with the lock version and the resolved block, and has the resources before the packs.
func expectLockKeyOrder(t *testing.T, text string) {
	t.Helper()

	if !strings.HasPrefix(text, "lockVersion: 1\nresolved:") {
		t.Errorf("key order:\n%s", text)
	}

	if strings.Index(text, "resources:") > strings.Index(text, "packs:") {
		t.Errorf("key order:\n%s", text)
	}
}

func TestLockWithoutPacksWritesNoPacksKey(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	mustWriteLock(t, s.lockPath, workspace.NewLock("0.12.1", ""))

	if text := testutil.ReadFile(t, s.lockPath); strings.Contains(text, "packs") {
		t.Errorf("lock text has a packs key:\n%s", text)
	}

	back, _ := mustReadLock(t, s.lockPath)
	requireEqual(t, "pack count", len(back.Packs), 0)
}

func TestLockPacksRoundTripInKeyOrder(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Packs = []workspace.LockPack{
		{ID: "acme", Version: "1.2.0", Source: vendorUse, SHA256: packSHA},
		{ID: "core", Version: "1.0.0", Source: coreUse, SHA256: otherSHA},
	}

	mustWriteLock(t, s.lockPath, lock)
	expectLockKeyOrder(t, testutil.ReadFile(t, s.lockPath))

	back, found := mustReadLock(t, s.lockPath)
	requireEqual(t, "lock is found", found, true)
	requireEqual(t, "pack count", len(back.Packs), 2)
	expectEqual(t, "first pack source (the packs are sorted by source)", back.Packs[0].Source, coreUse)
	expectEqual(t, "second pack (the packs are sorted by source)", back.Packs[1], lock.Packs[0])
}

func TestReadLockRejectsBadPacksShape(t *testing.T) {
	t.Parallel()

	for name, packs := range map[string]string{"scalar": "packs: x\n", "entry": "packs: [x]\n"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeWorkspace(t, "lockVersion: 1\nresolved:\n  cbm: {executableVersion: '0.9', adapterSchema: concordia-cbm/v1}\nresources: []\n"+packs)

			_, _, err := workspace.ReadLock(path)

			expectEqual(t, "code", failure(t, err).Diagnostic.Code, codeWRK002)
		})
	}
}

func packCtx(s *lockSetup, offline bool, packs ...workspace.PackRef) *workspace.VerifyContext {
	return &workspace.VerifyContext{Root: s.root, LockPath: s.lockPath, Offline: offline, CBMVersion: "0.12.1", Packs: packs}
}

var corePack = workspace.PackRef{ID: "core", Version: "1.0.0", Source: coreUse, SHA256: packSHA}

// expectPackAddedToNewLock verifies a run with one pack and no lock, and checks the lock that the run gives and writes.
func expectPackAddedToNewLock(t *testing.T, offline bool) {
	t.Helper()

	label := fmt.Sprintf("offline %v: ", offline)
	s := newLockSetup(t)

	lock, _ := mustVerifyLock(t, nil, packCtx(s, offline, corePack))
	requireEqual(t, label+"lock is given", lock != nil, true)
	requireEqual(t, label+"pack count", len(lock.Packs), 1)

	back, found := mustReadLock(t, s.lockPath)
	requireEqual(t, label+"written lock is found", found, true)
	requireEqual(t, label+"written pack count", len(back.Packs), 1)
	expectEqual(t, label+"written pack", back.Packs[0], workspace.LockPack{ID: "core", Version: "1.0.0", Source: coreUse, SHA256: packSHA})
}

func TestVerifyLockAddsAPackEvenOfflineAndCreatesTheLock(t *testing.T) {
	t.Parallel()

	for _, offline := range []bool{false, true} {
		expectPackAddedToNewLock(t, offline)
	}
}

func TestVerifyLockWithoutPacksDoesNotCreateALock(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	mustVerifyLock(t, nil, packCtx(s, false))

	expectNoLockFile(t, s.lockPath, "a lock was written for a run without packs")
}

func TestVerifyLockAcceptsAPackWithTheLockedDigest(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Packs = []workspace.LockPack{{ID: "core", Version: "1.0.0", Source: coreUse, SHA256: packSHA}}

	mustVerifyLock(t, lock, packCtx(s, true, corePack))

	expectNoLockFile(t, s.lockPath, "an unchanged lock was written")
}

func TestVerifyLockFailsForAChangedPackDigestLikeAResource(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Packs = []workspace.LockPack{{ID: "core", Version: "1.0.0", Source: coreUse, SHA256: otherSHA}}

	_, _, err := workspace.VerifyLock(lock, packCtx(s, false, corePack))

	de := failure(t, err)
	requireEqual(t, "exit code", de.ExitCode, diag.Network)
	requireEqual(t, "code", de.Diagnostic.Code, diag.INT012)
	expectContains(t, "message", de.Diagnostic.Message, coreUse)
	expectEqual(t, "suggestion is given", de.Diagnostic.Suggestion != "", true)
	expectNoLockFile(t, s.lockPath, "a failed run wrote the lock")
}

func TestVerifyLockKeepsPacksThatThisRunDoesNotUse(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "")
	lock.Packs = []workspace.LockPack{{ID: "acme", Version: "1.2.0", Source: vendorUse, SHA256: packSHA}}

	got, _ := mustVerifyLock(t, lock, packCtx(s, false, corePack))

	requireEqual(t, "pack count", len(got.Packs), 2)
	expectEqual(t, "input pack count (the input lock must not change)", len(lock.Packs), 1)
}

func TestSetPackAddsOrReplacesBySource(t *testing.T) {
	t.Parallel()

	lock := workspace.NewLock("0.12.1", "")
	lock.SetPack(workspace.LockPack{ID: "core", Version: "1.0.0", Source: coreUse, SHA256: packSHA})
	lock.SetPack(workspace.LockPack{ID: "core", Version: "1.1.0", Source: coreUse, SHA256: otherSHA})
	lock.SetPack(workspace.LockPack{ID: "acme", Version: "1", Source: vendorUse, SHA256: packSHA})

	requireEqual(t, "pack count", len(lock.Packs), 2)
	expectEqual(t, "first pack version", lock.Packs[0].Version, "1.1.0")
	expectEqual(t, "first pack sha256", lock.Packs[0].SHA256, otherSHA)
}
