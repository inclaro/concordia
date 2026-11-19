package workspace_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

var (
	helmPin      = workspace.RendererPin{Tool: workspace.RenderToolHelm, Version: "v4.3.1", SHA256: "sha256:aa"}
	kustomizePin = workspace.RendererPin{Tool: workspace.RenderToolKustomize, Version: "v5.8.3", SHA256: "sha256:bb"}
)

func rendererFailure(t *testing.T, err error) *diag.Error {
	t.Helper()

	de := failure(t, err)
	requireEqual(t, "code", de.Diagnostic.Code, diag.PRV003)
	requireEqual(t, "exit code", de.ExitCode, diag.Toolchain)

	return de
}

// expectRendererPin checks that the lock pins the tool as the wanted renderer pin does.
func expectRendererPin(t *testing.T, lock *workspace.Lockfile, tool string, want workspace.RendererPin) {
	t.Helper()

	got := lock.Renderer(tool)
	requireEqual(t, tool+" pin is given", got != nil, true)
	expectEqual(t, tool+" pin", *got, want.Pin())
}

// expectRendererVerification verifies the lock with the found renderers. It wants no error when wantErr is empty,
// and otherwise a renderer failure whose text has wantErr.
func expectRendererVerification(t *testing.T, lock *workspace.Lockfile, found []workspace.RendererPin, wantErr string) {
	t.Helper()

	s := newLockSetup(t)
	ctx := s.ctx(false, "0.12.1")
	ctx.Renderers = found

	_, _, err := workspace.VerifyLock(lock, ctx)
	if wantErr == "" {
		if err != nil {
			t.Fatal(err)
		}

		return
	}

	expectContains(t, "renderer failure", rendererFailure(t, err).Error(), wantErr)
}

func TestLockRenderersRoundTrip(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)
	lock := workspace.NewLock("0.12.1", "", helmPin, kustomizePin)

	mustWriteLock(t, s.lockPath, lock)

	text := testutil.ReadFile(t, s.lockPath)
	for _, want := range []string{"  renderers:", "    helm:", "      version: v4.3.1", "      sha256: sha256:aa", "    kustomize:", "      version: v5.8.3"} {
		expectContains(t, "lock text", text, want)
	}

	back, found := mustReadLock(t, s.lockPath)
	requireEqual(t, "lock is found", found, true)
	expectRendererPin(t, back, workspace.RenderToolHelm, helmPin)
	expectRendererPin(t, back, workspace.RenderToolKustomize, kustomizePin)
}

func TestLockWithoutRenderersLoadsAndWritesNoKey(t *testing.T) {
	t.Parallel()

	s := newLockSetup(t)

	mustWriteLock(t, s.lockPath, workspace.NewLock("0.12.1", ""))

	if strings.Contains(testutil.ReadFile(t, s.lockPath), "renderers") {
		t.Error("a lock without renderers has a renderers key")
	}

	back, found := mustReadLock(t, s.lockPath)
	requireEqual(t, "lock is found", found, true)
	requireEqual(t, "renderers are absent", back.Resolved.Renderers == nil, true)
	requireEqual(t, "helm pin is absent", back.Renderer(workspace.RenderToolHelm) == nil, true)

	// No source needs a renderer: nothing to compare, the lock verifies.
	mustVerifyLock(t, back, s.ctx(false, "0.12.1"))
}

func TestVerifyLockRendererPins(t *testing.T) {
	t.Parallel()

	changed := helmPin
	changed.SHA256 = "sha256:cc"

	tests := []struct {
		lock  *workspace.Lockfile
		name  string
		want  string
		found []workspace.RendererPin
	}{
		{name: "matching pin", lock: workspace.NewLock("0.12.1", "", helmPin), found: []workspace.RendererPin{helmPin}},
		{name: "two matching pins", lock: workspace.NewLock("0.12.1", "", helmPin, kustomizePin), found: []workspace.RendererPin{kustomizePin, helmPin}},
		{name: "pin of an unused tool is not compared", lock: workspace.NewLock("0.12.1", "", helmPin, kustomizePin), found: []workspace.RendererPin{helmPin}},
		{name: "different digest", lock: workspace.NewLock("0.12.1", "", helmPin), found: []workspace.RendererPin{changed}, want: "differs from its pin"},
		{name: "different version", lock: workspace.NewLock("0.12.1", "", helmPin), found: []workspace.RendererPin{{Tool: "helm", Version: "v3.0.0", SHA256: helmPin.SHA256}}, want: "v4.3.1"},
		{name: "not pinned", lock: workspace.NewLock("0.12.1", "", helmPin), found: []workspace.RendererPin{kustomizePin}, want: "kustomize is not pinned"},
		{name: "no renderers in the lock", lock: workspace.NewLock("0.12.1", ""), found: []workspace.RendererPin{helmPin}, want: "helm is not pinned"},
		{name: "no lockfile", lock: nil, found: []workspace.RendererPin{helmPin}, want: "helm is not pinned"},
		{name: "renderer missing", lock: workspace.NewLock("0.12.1", "", helmPin), found: []workspace.RendererPin{{Tool: "helm"}}, want: "helm was not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expectRendererVerification(t, tc.lock, tc.found, tc.want)
		})
	}
}

func TestPinMismatchNamesExpectedAndFoundIdentity(t *testing.T) {
	t.Parallel()

	changed := helmPin
	changed.SHA256 = "sha256:cc"

	de := rendererFailure(t, workspace.CheckRenderer(workspace.NewLock("1", "", helmPin), changed))
	for _, want := range []string{"sha256:aa", "sha256:cc", "v4.3.1"} {
		expectContains(t, "renderer failure", de.Error(), want)
	}
}

func TestLockRendererPinsOnlyForUsedToolsAndSurviveVerify(t *testing.T) {
	t.Parallel()

	lock := workspace.NewLock("0.12.1", "", helmPin, kustomizePin)
	lock.DropRenderer(workspace.RenderToolHelm)

	requireEqual(t, "helm pin is absent", lock.Renderer(workspace.RenderToolHelm) == nil, true)
	requireEqual(t, "kustomize pin is given", lock.Renderer(workspace.RenderToolKustomize) != nil, true)

	lock.DropRenderer(workspace.RenderToolKustomize)

	expectEqual(t, "empty renderers are dropped", lock.Resolved.Renderers == nil, true)

	// VerifyLock does not change its input and keeps the pins when it adds a resource.
	s := newLockSetup(t)
	in := workspace.NewLock("0.12.1", "", helmPin)

	out, _ := mustVerifyLock(t, in, s.ctx(false, "0.12.1", s.res))

	expectRendererPin(t, out, workspace.RenderToolHelm, helmPin)
	expectEqual(t, "output renderers are a copy", out.Resolved.Renderers != in.Resolved.Renderers, true)
}
