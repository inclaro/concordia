package renderer_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/renderer"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
		ok       bool
	}{
		{"v4.3.1+gc0d08d0\n", "v4.3.1", true},
		{"v5.8.3\n", "v5.8.3", true},
		{"{Version:kustomize/v5.0.0 GitCommit:abc BuildDate:2023 GoOs:linux}", "v5.0.0", true},
		{"5.1.2", "v5.1.2", true},
		{"v3.15.0-rc.1+gabc", "v3.15.0-rc.1", true},
		{"no version here", "", false},
		{"", "", false},
	}

	for _, tc := range tests {
		got, ok := renderer.ParseVersion(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseVersion(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func fakeTool(t *testing.T, name, script string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

func envOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestProbeGivesVersionAndDigestOfTheExecutable(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fake renderer is a shell script")
	}

	helm := fakeTool(t, "helm", `echo "v4.3.1+gc0d08d0"`)
	kustomize := fakeTool(t, "kustomize", `echo "v5.8.3"`)
	getenv := envOf(map[string]string{"HELM": helm, "KUSTOMIZE": kustomize})

	sum, err := digest.OfFile(helm)
	if err != nil {
		t.Fatal(err)
	}

	found, err := renderer.Probe(context.Background(), workspace.RenderToolHelm, getenv)
	if err != nil {
		t.Fatal(err)
	}

	if want := (workspace.RendererPin{Tool: "helm", Version: "v4.3.1", SHA256: sum}); found.Pin != want || found.Path != helm {
		t.Errorf("found %+v, want %+v at %s", found, want, helm)
	}

	found, err = renderer.Probe(context.Background(), workspace.RenderToolKustomize, getenv)
	if err != nil || found.Pin.Version != "v5.8.3" || found.Pin.Tool != "kustomize" {
		t.Errorf("found %+v err %v", found, err)
	}
}

func TestProbeFailuresAreToolchainErrors(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fake renderer is a shell script")
	}

	tests := []struct {
		env  map[string]string
		name string
		tool string
	}{
		{map[string]string{"HELM": "/nonexistent/helm"}, "override does not exist", "helm"},
		{map[string]string{"HELM": fakeTool(t, "helm", "exit 3")}, "version fails", "helm"},
		{map[string]string{"KUSTOMIZE": fakeTool(t, "kustomize", "echo hello")}, "no version in the output", "kustomize"},
		{nil, "not a renderer", "plain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := renderer.Probe(context.Background(), tc.tool, envOf(tc.env))

			var de *diag.Error
			if !errors.As(err, &de) || de.Diagnostic.Code != diag.PRV003 || de.ExitCode != diag.Toolchain {
				t.Fatalf("want CO-PRV-003 exit 5, got %v", err)
			}
		})
	}
}

func TestPathUsesPATHWhenNoOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake renderer is a shell script")
	}

	dir := filepath.Dir(fakeTool(t, "kustomize", "echo v1.2.3"))
	t.Setenv("PATH", dir)

	got, err := renderer.Path(workspace.RenderToolKustomize, envOf(nil))
	if err != nil || got != filepath.Join(dir, "kustomize") {
		t.Fatalf("got %q err %v", got, err)
	}

	if _, err := renderer.Path(workspace.RenderToolHelm, envOf(nil)); err == nil {
		t.Error("helm is not on PATH, expected an error")
	}
}
