package render_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

// repoRoot finds the repository root (the directory with go.mod) above the test directory.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}

		dir = parent
	}
}

// fixtures is test/fixtures/assurance.
func fixtures(t *testing.T) string {
	t.Helper()

	return filepath.Join(repoRoot(t), "test", "fixtures", "assurance")
}

// realTool finds a real helm or kustomize: .bin of the repository first, then PATH. It gives "" when there is none.
func realTool(t *testing.T, name string) string {
	t.Helper()

	candidate := filepath.Join(repoRoot(t), ".bin", name)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}

	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	return ""
}

// fakeRenderer writes a renderer script. It answers `version` with 4.3.0 and runs body for every
// other call. The script runs with the scrubbed environment (PATH=/usr/bin:/bin), so it uses only shell builtins
// and tools of those directories.
func fakeRenderer(t *testing.T, body string) (string, render.Pin) {
	t.Helper()

	const version = "4.3.0"

	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh for a fake renderer")
	}

	path := filepath.Join(t.TempDir(), "fake-renderer")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"v" + version + "\"; exit 0; fi\n" + body + "\n"

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(script))

	return path, render.Pin{Version: "v" + version, SHA256: digest.Prefix + hex.EncodeToString(sum[:])}
}

// locator gives a Locator that finds the given programs and nothing else.
func locator(found map[string]string) render.Locator {
	return func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}

		return "", exec.ErrNotFound
	}
}

// recorder is a Runner that records every start and then runs the program with procexec.
type recorder struct {
	calls [][]string
	mu    sync.Mutex
}

func (r *recorder) run(ctx context.Context, name string, args []string, opts procexec.Options) (procexec.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()

	return procexec.Run(ctx, name, args, opts)
}

// renders are the starts of the renderer itself: every call that is not the version probe and not bubblewrap.
func (r *recorder) renders() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out [][]string

	for _, c := range r.calls {
		if len(c) > 1 && c[1] == "version" {
			continue
		}

		if strings.HasSuffix(c[0], "bwrap") {
			continue
		}

		out = append(out, c)
	}

	return out
}

// forbidden is a Runner that fails the test when it is called.
func forbidden(t *testing.T) render.Runner {
	t.Helper()

	return func(_ context.Context, name string, args []string, _ procexec.Options) (procexec.Result, error) {
		t.Errorf("no program should start, but %s %v was started", name, args)

		return procexec.Result{}, errors.New("forbidden")
	}
}

// diagError gives the *diag.Error in err.
func diagError(t *testing.T, err error) *diag.Error {
	t.Helper()

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("want a *diag.Error, got %v", err)
	}

	return de
}

func plainSource(env, path string) *workspace.RenderSource {
	return &workspace.RenderSource{Environment: env, Tool: workspace.RenderToolPlain, Path: path}
}

// gapKinds lists the kinds of the gaps of a result.
func gapKinds(res *render.Result) []string {
	out := make([]string, 0, len(res.Gaps))
	for _, g := range res.Gaps {
		out = append(out, g.Kind)
	}

	return out
}

func hasDiag(res *render.Result, code string) bool {
	for _, d := range res.Diagnostics {
		if d.Code == code {
			return true
		}
	}

	return false
}

func helmSource(env, chart string, values ...string) *workspace.RenderSource {
	return &workspace.RenderSource{
		Environment: env, Tool: workspace.RenderToolHelm, Path: chart, ValuesFiles: values, Release: "shop", Namespace: "shop",
	}
}

func kustomizeSource(env, path string) *workspace.RenderSource {
	return &workspace.RenderSource{Environment: env, Tool: workspace.RenderToolKustomize, Path: path}
}

// fakeOptions renders src with a fake renderer for its tool.
func fakeOptions(t *testing.T, root string, src *workspace.RenderSource, body string, run render.Runner) *render.Options {
	t.Helper()

	exe, pin := fakeRenderer(t, body)

	return &render.Options{
		Root: root, Source: *src, Run: run,
		Pins: render.Pins{src.Tool: pin}, Locate: locator(map[string]string{src.Tool: exe}),
	}
}

// minimalManifest is the output of a fake renderer: one ConfigMap.
const minimalManifest = `printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  namespace: shop\n'`
