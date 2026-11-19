package cli_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// ---------------------------------------------------------------------------------------------
// init --preset core

// pkExpectCorePresetLock checks that the lock of an initialized workspace pins the built-in core pack.
func pkExpectCorePresetLock(t *testing.T, dir string) {
	t.Helper()

	core := pkCorePack(t)
	text, _ := packs.Source("core")
	want := workspace.LockPack{ID: core.ID, Version: core.Version, Source: "builtin:core", SHA256: pkSum(string(text))}

	lock, found, err := workspace.ReadLock(workspace.LockPathFor(dir))
	if err != nil || !found || len(lock.Packs) != 1 {
		t.Fatalf("lock %+v found %v err %v", lock, found, err)
	}

	if lock.Packs[0] != want {
		t.Errorf("lock pack = %+v, want %+v", lock.Packs[0], want)
	}
}

// pkExpectNoPacksKey checks that the presets that are not core write no packs key.
func pkExpectNoPacksKey(t *testing.T) {
	t.Helper()

	for _, preset := range []string{"services", "modular-monolith", "custom"} {
		other := t.TempDir()
		if res := initIn(t, other, cbmVersion("0.12.1"), "--preset", preset); res.Code != 0 {
			t.Fatal(res.Stderr)
		}

		if strings.Contains(readFile(t, other, "concordia.yaml"), "packs") {
			t.Errorf("preset %s wrote a packs key to the workspace", preset)
		}

		if strings.Contains(readFile(t, other, ".concordia.lock"), "packs") {
			t.Errorf("preset %s wrote a packs key to the lock", preset)
		}
	}
}

func TestInitCorePresetUsesTheBuiltinPackAndLocksIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	res := initIn(t, dir, cbmVersion("0.12.1"), "--preset", "core", "--ci", "github")
	if res.Code != 0 || !strings.Contains(res.Stdout, "initialized: workspace, architecture, and policies are valid") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	wantContains(t, readFile(t, dir, "concordia.yaml"), "preset: core", "packs:", "- use: builtin:core")

	// The local policy file is the one of the `custom` preset.
	custom := t.TempDir()
	if res := initIn(t, custom, cbmVersion("0.12.1"), "--preset", "custom"); res.Code != 0 {
		t.Fatal(res.Stderr)
	}

	if got, want := readFile(t, dir, "policies/default.policy.yaml"), readFile(t, custom, "policies/default.policy.yaml"); got != want {
		t.Errorf("core policy file:\n%s\ncustom:\n%s", got, want)
	}

	pkExpectCorePresetLock(t, dir)
	pkExpectOK(t, runIn(t, dir, "policy", "verify"))
	pkExpectNoPacksKey(t)
}

// ---------------------------------------------------------------------------------------------
// check and explain with packs

// pkCheckSetup is a workspace with a vendored pack (and the demo policy file, which the glob also matches).
func pkCheckSetup(t *testing.T, packsYAML string) *chkSetup {
	t.Helper()

	return newChkSetup(t, map[string]string{
		"concordia.yaml": chkWorkspaceText("policies:\n  files: [policies/demo.policy.yaml]\n  packs:\n" + packsYAML),
		pkVendorRel:      pkPackText,
	}, nil)
}

func TestCheckRunsPackRulesAndExplainNamesThePack(t *testing.T) {
	t.Parallel()

	s := pkCheckSetup(t, pkVendored())

	res := s.run(t, "check", "--no-color")
	if res.Code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout, "FAIL ACME-BND-001 Billing does not import fulfillment")

	explained := s.run(t, "explain", "ACME-BND-001")
	if explained.Code != 0 {
		t.Fatalf("explain: exit code = %d, stderr = %q", explained.Code, explained.Stderr)
	}

	wantContains(t, explained.Stdout, "ACME-BND-001 Billing does not import fulfillment", "Policy: acme@1.2.0")
}

func TestCheckPolicyOptionReplacesPolicyFilesAndPacks(t *testing.T) {
	t.Parallel()

	// A wrong pin: a run that read the packs would exit with 2.
	s := pkCheckSetup(t, "    - use: "+pkVendorRel+"\n      sha256: sha256:"+strings.Repeat("0", 64)+"\n")

	if res := s.run(t, "check"); res.Code != 2 || !strings.Contains(res.Stderr, "CO-POL-010") {
		t.Fatalf("without --policy: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	res := s.run(t, "check", "--policy", "policies/demo.policy.yaml", "--no-color")
	if res.Code != 1 || strings.Contains(res.Stdout, "ACME-") {
		t.Fatalf("with --policy: exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	wantContains(t, res.Stdout, "FAIL CO-BND-001")
}

type pkFailingTransport struct{ t *testing.T }

func (f pkFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("network use")

	return nil, http.ErrNotSupported
}

func TestCheckWithPacksMakesNoNetworkRequest(t *testing.T) {
	prior := http.DefaultTransport
	http.DefaultTransport = pkFailingTransport{t}

	t.Cleanup(func() { http.DefaultTransport = prior })

	s := pkCheckSetup(t, "    - use: builtin:core\n"+pkVendored())

	if res := s.run(t, "--offline", "check", "--no-color"); res.Code != 1 {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if res := s.run(t, "policy", "list"); res.Code != 0 {
		t.Errorf("policy list: exit code = %d", res.Code)
	}

	if res := s.run(t, "policy", "verify"); res.Code != 0 {
		t.Errorf("policy verify: exit code = %d", res.Code)
	}
}

func TestCheckFailsWhenAPackChangedAgainstTheLock(t *testing.T) {
	t.Parallel()

	s := pkCheckSetup(t, pkVendored())

	if res := s.run(t, "check", "--no-color"); res.Code != 1 {
		t.Fatalf("first run: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, readFile(t, s.dir, ".concordia.lock"), "packs:", "id: acme", "source: "+pkVendorRel)

	changed := pkPackText + "# new\n"
	testutil.WriteTree(t, s.dir, map[string]string{
		pkVendorRel:      changed,
		"concordia.yaml": chkWorkspaceText("policies:\n  files: [policies/demo.policy.yaml]\n  packs:\n    - use: " + pkVendorRel + "\n      sha256: " + pkSum(changed) + "\n"),
	})

	pkExpectStderr(t, s.run(t, "check", "--no-color"), 6, "CO-INT-012")

	// `policy add --force` of the new file pins the digest in the lock too.
	if add := s.run(t, "policy", "add", pkVendorRel, "--force"); add.Code != 0 {
		t.Fatalf("add: exit code = %d, stderr = %q", add.Code, add.Stderr)
	}

	if res := s.run(t, "check", "--no-color"); res.Code != 1 {
		t.Errorf("after the update: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
