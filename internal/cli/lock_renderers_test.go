package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRenderer writes an executable shell script that prints the version text.
func fakeRenderer(t *testing.T, name, versionText string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake renderer is a shell script")
	}

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+versionText+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// renderersLockUpdate runs `lock update` in the workspace with the renderer overrides of env.
func renderersLockUpdate(t *testing.T, dir string, env map[string]string) result {
	t.Helper()

	return runWith(t, &runConfig{Dir: dir, Deps: cbmVersion("0.12.1"), Env: env}, "lock", "update")
}

// renderersStdoutHasAll reports whether the standard output of the run holds every part.
func renderersStdoutHasAll(res result, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(res.Stdout, part) {
			return false
		}
	}

	return true
}

// renderersAppendWorkspace adds the text to the workspace file of dir.
func renderersAppendWorkspace(t *testing.T, dir, extra string) {
	t.Helper()

	workspaceText := readFile(t, dir, "concordia.yaml")
	if err := os.WriteFile(filepath.Join(dir, "concordia.yaml"), []byte(workspaceText+extra), 0o600); err != nil {
		t.Fatal(err)
	}
}

// renderersLockPinsOnlyKustomize reports whether the lock pins kustomize v5.8.3 and no helm.
func renderersLockPinsOnlyKustomize(lock string) bool {
	pinsKustomize := strings.Contains(lock, "renderers:") && strings.Contains(lock, "kustomize:") && strings.Contains(lock, "version: v5.8.3")

	return pinsKustomize && !strings.Contains(lock, "helm:")
}

func TestLockUpdatePinsOnlyTheRenderersThatASourceUses(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fake renderers are shell scripts")
	}

	dir := t.TempDir()
	if res := initIn(t, dir, cbmVersion("0.12.1")); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	renderersAppendWorkspace(t, dir, `
environments:
  - {id: staging, sources: [deploy/**]}
assurance:
  rendering:
    sources:
      - {environment: staging, tool: kustomize, path: deploy/overlays/staging}
`)

	env := map[string]string{
		"KUSTOMIZE": fakeRenderer(t, "kustomize", "v5.8.3"),
		// helm is not used by a source: it must not be probed, so a broken override is no problem.
		"HELM": "/nonexistent/helm",
	}

	res := renderersLockUpdate(t, dir, env)
	if res.Code != 0 || !strings.Contains(res.Stdout, "pinned kustomize v5.8.3 ") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if lock := readFile(t, dir, ".concordia.lock"); !renderersLockPinsOnlyKustomize(lock) {
		t.Fatalf("lock:\n%s", lock)
	}

	res = renderersLockUpdate(t, dir, env)
	if res.Code != 0 || !strings.Contains(res.Stdout, "unchanged kustomize v5.8.3") {
		t.Fatalf("second update: exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	// A new build of the renderer is a new pin.
	env["KUSTOMIZE"] = fakeRenderer(t, "kustomize", "v5.9.0")

	res = renderersLockUpdate(t, dir, env)
	if res.Code != 0 || !renderersStdoutHasAll(res, "updated kustomize v5.8.3 ", "-> v5.9.0 ") {
		t.Fatalf("third update: exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestLockUpdateWithoutAnAssuranceKeyWritesNoRendererPin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if res := initIn(t, dir, cbmVersion("0.12.1")); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	res := runWith(t, &runConfig{Dir: dir, Deps: cbmVersion("0.12.1")}, "lock", "update")
	if res.Code != 0 || strings.Contains(readFile(t, dir, ".concordia.lock"), "renderers") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestLockUpdateWithAMissingRendererExitsWith5(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if res := initIn(t, dir, cbmVersion("0.12.1")); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if err := os.WriteFile(filepath.Join(dir, "concordia.yaml"), []byte(readFile(t, dir, "concordia.yaml")+`
environments:
  - {id: prod, sources: [deploy/**]}
assurance:
  rendering:
    sources: [{environment: prod, tool: helm, path: deploy/chart}]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	res := runWith(t, &runConfig{Dir: dir, Deps: cbmVersion("0.12.1"), Env: map[string]string{"HELM": "/nonexistent/helm"}}, "lock", "update")
	if res.Code != 5 || !strings.Contains(res.Stderr, "CO-PRV-003") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
