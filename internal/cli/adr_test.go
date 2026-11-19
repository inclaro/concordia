package cli_test

import (
	"crypto/sha256"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// adrFixture copies test/fixtures/adr into a new temporary workspace.
func adrFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "test", "fixtures", "adr"))); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}

	return dir
}

// treeDigests gives the digest of every file below dir.
func treeDigests(t *testing.T, dir string) map[string][32]byte {
	t.Helper()

	out := map[string][32]byte{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}

		out[p] = sha256.Sum256(data)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// Scenario: Valid register is loaded.
func TestAdrValidateAcceptsAValidRegisterOfflineWithoutCBM(t *testing.T) {
	t.Parallel()

	dir := adrFixture(t)
	before := treeDigests(t, dir)

	// The environment is empty: there is no PATH, so no CBM executable can be found or started.
	res := runWith(t, &runConfig{Dir: dir}, "adr", "validate", "--offline")
	if res.Code != 0 || !strings.Contains(res.Stdout, "valid") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if after := treeDigests(t, dir); !maps.Equal(before, after) {
		t.Error("adr validate changed the workspace files")
	}
}

func TestAdrValidateRejectsAnInvalidLink(t *testing.T) {
	t.Parallel()

	dir := adrFixture(t)
	path := filepath.Join(dir, "governance", "decisions.yaml")

	text := readFile(t, dir, "governance/decisions.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(text, "version: 1.0.0", "version: 9.9.9", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	before := treeDigests(t, dir)

	res := runWith(t, &runConfig{Dir: dir}, "adr", "validate", "--offline")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-ADR-004") || !strings.Contains(res.Stderr, "CORE-DB-001") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if after := treeDigests(t, dir); !maps.Equal(before, after) {
		t.Error("adr validate changed the workspace files")
	}
}

// Scenario: Invalid declaration.
func TestAdrValidateRejectsAnInvalidRegister(t *testing.T) {
	t.Parallel()

	dir := adrFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "governance", "decisions.yaml"),
		[]byte("apiVersion: concordia.io/v1alpha1\nkind: DecisionRegister\ndecisions:\n  - id: ADR-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := runWith(t, &runConfig{Dir: dir}, "adr", "validate")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-ADR-001") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestAdrValidateWithoutAnAdrKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "concordia.yaml"), []byte(waiverWorkspaceYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	res := runWith(t, &runConfig{Dir: dir}, "adr", "validate")
	if res.Code != 0 || !strings.Contains(res.Stdout, "no register configured") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

// Scenario: Offline.
// Without --offline too, a remote architecture that is not pinned in the lockfile is never fetched: the command
// stops with CO-INT-012 (its exit code 6) and makes no network call.
func TestAdrValidateNeverUsesTheNetwork(t *testing.T) {
	t.Parallel()

	dir := adrFixture(t)
	path := filepath.Join(dir, "concordia.yaml")

	text := readFile(t, dir, "concordia.yaml")
	remoteSource := "source: https://example.com/orders.calm.json"

	if err := os.WriteFile(path, []byte(strings.Replace(text, "source: architecture/orders.calm.json", remoteSource, 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32

	cfg := &runConfig{Dir: dir}
	cfg.Deps.HTTPClient = &http.Client{Transport: failingTransport{t: t, hits: &calls}}

	res := runWith(t, cfg, "adr", "validate")
	if res.Code != 6 || !strings.Contains(res.Stderr, "CO-INT-012") || calls.Load() != 0 {
		t.Errorf("exit code = %d, calls = %d, stderr = %q", res.Code, calls.Load(), res.Stderr)
	}
}
