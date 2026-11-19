package cli_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// pkExpectWorkspaceUnchanged reports an error when concordia.yaml is not the base workspace any more.
func pkExpectWorkspaceUnchanged(t *testing.T, dir string) {
	t.Helper()

	if got := readFile(t, dir, "concordia.yaml"); got != pkWorkspaceBase {
		t.Errorf("the workspace was changed:\n%s", got)
	}
}

func TestPolicyAddBuiltinAppendsTheEntryAndKeepsCommentsAndKeys(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, nil)

	pkExpectAdded(t, runIn(t, dir, "policy", "add", "builtin:core"), "builtin:core")

	text := readFile(t, dir, "concordia.yaml")
	wantContains(t, text, "# The workspace of the demo.", "# the CBM project", "check: {minimumCoverage: 0.5", "policies:", "packs:", "- use: builtin:core")

	if strings.Contains(text, "sha256") {
		t.Errorf("a built-in entry has a sha256:\n%s", text)
	}

	if pack := pkOnlyPack(t, dir); pack.Use != "builtin:core" {
		t.Fatalf("pack = %+v", pack)
	}

	pkExpectStderr(t, runIn(t, dir, "policy", "add", "builtin:core"), 2, "CO-WRK-010", "--force")
	pkExpectOK(t, runIn(t, dir, "policy", "add", "builtin:core", "--force"))

	if got := readFile(t, dir, "concordia.yaml"); strings.Count(got, "builtin:core") != 1 {
		t.Errorf("the entry is twice:\n%s", got)
	}
}

func TestPolicyAddAppendsToAnExistingPacksList(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase+"policies:\n  files: [rules/*.yaml]\n  # packs in use\n  packs:\n    - use: builtin:core # the base\n", map[string]string{"pack.yaml": pkPackText})

	pkExpectAdded(t, runIn(t, dir, "policy", "add", "pack.yaml"), pkVendorRel)

	text := readFile(t, dir, "concordia.yaml")
	wantContains(t, text, "# packs in use", "# the base", "files: [rules/*.yaml]", "- use: builtin:core", "- use: "+pkVendorRel, "sha256: "+pkSum(pkPackText))
}

func TestPolicyAddVendorsAFileAndPinsIt(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, map[string]string{"downloads/pack.yaml": pkPackText})

	pkExpectAdded(t, runIn(t, dir, "policy", "add", "downloads/pack.yaml"), pkVendorRel)

	if got := readFile(t, dir, pkVendorRel); got != pkPackText {
		t.Errorf("the vendored file differs:\n%s", got)
	}

	pack := pkOnlyPack(t, dir)
	pkExpectAll(t,
		pkCheck{pack.Use, pkVendorRel, "pack source"},
		pkCheck{pack.SHA256, pkSum(pkPackText), "pack digest"},
	)

	pkExpectOK(t, runIn(t, dir, "policy", "verify"))
	pkExpectStderr(t, runIn(t, dir, "policy", "add", "downloads/pack.yaml"), 2, "--force")
}

func TestPolicyAddIDOptionNamesTheVendoredFile(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, map[string]string{"pack.yaml": pkPackText})

	pkExpectAdded(t, runIn(t, dir, "policy", "add", "pack.yaml", "--id", "acme-arch"), "policies/vendor/acme-arch@1.2.0.policy.yaml")

	for _, bad := range []string{"../x", "a b", "-x"} {
		pkExpectStderr(t, runIn(t, dir, "policy", "add", "pack.yaml", "--id", bad), 2, "CO-WRK-010")
	}

	pkExpectStderr(t, runIn(t, dir, "policy", "add", "builtin:core", "--id", "x"), 2)
}

// pkSeedOverrideAndLock adds an override to the vendored entry of the workspace and writes a lock for the pack.
func pkSeedOverrideAndLock(t *testing.T, dir string) {
	t.Helper()

	wsText := readFile(t, dir, "concordia.yaml")
	wsText = strings.Replace(wsText, "- use: "+pkVendorRel, "- use: "+pkVendorRel+"\n      rules:\n        ACME-DECL-001: { enabled: false }", 1)
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": wsText})

	lock := workspace.NewLock("0.12.1", "")
	lock.SetPack(workspace.LockPack{ID: "acme", Version: "1.2.0", Source: pkVendorRel, SHA256: pkSum(pkPackText)})

	if err := workspace.WriteLock(workspace.LockPathFor(dir), lock); err != nil {
		t.Fatal(err)
	}
}

// pkExpectLockedDigest checks that the lock of dir has the pack with the digest.
func pkExpectLockedDigest(t *testing.T, dir, sum string) {
	t.Helper()

	back, _, err := workspace.ReadLock(workspace.LockPathFor(dir))
	if err != nil || len(back.Packs) != 1 {
		t.Fatalf("lock %+v err %v", back, err)
	}

	pkExpectAll(t, pkCheck{back.Packs[0].SHA256, sum, "locked digest"})
}

func TestPolicyAddForceUpdatesTheDigestAndKeepsOverridesAndTheLock(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, map[string]string{"pack.yaml": pkPackText})

	if res := runIn(t, dir, "policy", "add", "pack.yaml"); res.Code != 0 {
		t.Fatal(res.Stderr)
	}

	// The user adds an override to the entry and a lock exists.
	pkSeedOverrideAndLock(t, dir)

	// A new pack text with the same id and version (a fix at the source).
	changed := pkPackText + "# fixed\n"
	testutil.WriteTree(t, dir, map[string]string{"pack.yaml": changed})

	if res := runIn(t, dir, "policy", "add", "pack.yaml"); res.Code != 2 {
		t.Fatalf("without --force: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	res := runIn(t, dir, "policy", "add", "pack.yaml", "--force")
	pkExpectOK(t, res)
	wantContains(t, res.Stdout, "updated "+pkVendorRel, pkSum(pkPackText)+" -> "+pkSum(changed))

	entry := pkOnlyPack(t, dir)
	pkExpectAll(t, pkCheck{entry.SHA256, pkSum(changed), "entry digest"})

	if override := entry.Rules["ACME-DECL-001"]; override.Enabled == nil || *override.Enabled {
		t.Errorf("entry = %+v", entry)
	}

	if got := readFile(t, dir, pkVendorRel); got != changed {
		t.Error("the vendored file was not replaced")
	}

	pkExpectLockedDigest(t, dir, pkSum(changed))
}

func TestPolicyAddRepinsAFileEditedInPlace(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, map[string]string{"pack.yaml": pkPackText})

	if res := runIn(t, dir, "policy", "add", "pack.yaml"); res.Code != 0 {
		t.Fatal(res.Stderr)
	}

	edited := strings.Replace(pkPackText, "severity: warning", "severity: info", 1)
	testutil.WriteTree(t, dir, map[string]string{pkVendorRel: edited})

	if res := runIn(t, dir, "policy", "verify"); res.Code != 2 || !strings.Contains(res.Stderr, "CO-POL-010") {
		t.Fatalf("verify: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	pkExpectOK(t, runIn(t, dir, "policy", "add", pkVendorRel, "--force"))
	pkExpectOK(t, runIn(t, dir, "policy", "verify"))

	if got := readFile(t, dir, pkVendorRel); got != edited {
		t.Error("the edited file was overwritten")
	}
}

func TestPolicyAddRefusesToOverwriteOtherContentWithoutForce(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, map[string]string{"pack.yaml": pkPackText, pkVendorRel: "something else\n"})

	pkExpectStderr(t, runIn(t, dir, "policy", "add", "pack.yaml"), 2, "--force")
	pkExpectWorkspaceUnchanged(t, dir)
}

func TestPolicyAddRejectsInvalidSourcesAndWritesNothing(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		files  map[string]string
		source string
		text   string
		code   int
	}{
		"not a pack":      {map[string]string{"bad.yaml": "kind: Other\n"}, "bad.yaml", "CO-POL-001", 2},
		"syntax error":    {map[string]string{"bad.yaml": "a: [\n"}, "bad.yaml", "CO-POL-001", 2},
		"missing file":    {nil, "none.yaml", "CO-POL-011", 2},
		"unknown builtin": {nil, "builtin:nope", "CO-POL-012", 2},
		"views pack":      {nil, "builtin:views", "applies automatically when views are declared", 2},
		"http":            {nil, "http://example.org/p.yaml", "CO-WRK-010", 2},
		"other scheme":    {nil, "ftp://example.org/p.yaml", "CO-WRK-010", 2},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := pkDir(t, pkWorkspaceBase, tc.files)

			pkExpectStderr(t, runIn(t, dir, "policy", "add", tc.source), tc.code, tc.text)
			pkExpectWorkspaceUnchanged(t, dir)

			if exists(dir, "policies") {
				t.Error("a policies directory was made")
			}
		})
	}
}

func TestPolicyAddRefusesTheViewsPackWithCOPOL001(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, nil)

	pkExpectStderr(t, runIn(t, dir, "policy", "add", "builtin:views"), 2, "CO-POL-001", "applies automatically when views are declared")
}

func TestPolicyAddAcceptsTheADRGovernancePack(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase, nil)

	pkExpectAdded(t, runIn(t, dir, "policy", "add", "builtin:adr-governance"), "builtin:adr-governance")

	if pack := pkOnlyPack(t, dir); pack.Use != "builtin:adr-governance" {
		t.Fatalf("pack = %+v", pack)
	}
}

func TestPolicyAddNeedsAWorkspace(t *testing.T) {
	t.Parallel()

	pkExpectStderr(t, runCLI(t, "policy", "add", "builtin:core"), 2, "CO-WRK-001")
}
