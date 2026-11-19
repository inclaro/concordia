package render_test

import (
	"context"
	"errors"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestRequireOsSandboxWithoutBubblewrapIsAToolchainError(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{workspace.RenderToolHelm, workspace.RenderToolKustomize} {
		checkSandboxRequiredWithoutBubblewrap(t, tool)
	}
}

func checkSandboxRequiredWithoutBubblewrap(t *testing.T, tool string) {
	t.Helper()

	src := helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml")
	if tool == workspace.RenderToolKustomize {
		src = kustomizeSource("production", "kustomize-shop/overlays/staging")
	}

	run := &recorder{}
	opts := fakeOptions(t, fixtures(t), src, minimalManifest, run.run)
	opts.RequireOsSandbox = true

	res, err := render.Render(context.Background(), opts)

	de := diagError(t, err)
	if de.Diagnostic.Code != diag.PRV004 || de.ExitCode != diag.Toolchain {
		t.Errorf("%s: error = %v, want CO-PRV-004 with exit 5", tool, err)
	}

	if len(run.renders()) != 0 || res.Coverage == assurance.CoverageComplete {
		t.Errorf("%s: the renderer ran (%v) or the coverage is %s", tool, run.renders(), res.Coverage)
	}
}

func TestPlainNeedsNoSandbox(t *testing.T) {
	t.Parallel()

	res, err := render.Render(context.Background(), &render.Options{
		Root: fixtures(t), Source: *plainSource("production", "plain"), RequireOsSandbox: true,
		Run: forbidden(t), Locate: locator(nil),
	})
	if err != nil || res.Coverage != assurance.CoverageComplete {
		t.Errorf("a plain source starts no program: %v, %s", err, res.Coverage)
	}
}

func TestConfinementWithoutSandboxIsProcess(t *testing.T) {
	t.Parallel()

	res, err := render.Render(context.Background(), fakeOptions(t, fixtures(t), kustomizeSource("production", "kustomize-shop/overlays/staging"), minimalManifest, nil))
	if err != nil {
		t.Fatal(err)
	}

	if res.Confinement != render.ConfinementProcess {
		t.Errorf("confinement = %q", res.Confinement)
	}
}

// sandboxOptions gives options that require the OS sandbox and find the real bubblewrap, or skips the test when
// bubblewrap is not installed.
func sandboxOptions(t *testing.T, opts *render.Options) *render.Options {
	t.Helper()

	bwrap := realTool(t, "bwrap")
	if bwrap == "" {
		t.Skip("bubblewrap is not installed")
	}

	exe, err := opts.Locate(opts.Source.Tool)
	if err != nil {
		t.Fatal(err)
	}

	opts.Locate = locator(map[string]string{"bwrap": bwrap, opts.Source.Tool: exe})
	opts.RequireOsSandbox = true

	return opts
}

// renderInSandbox renders with the OS sandbox, and skips the test when bubblewrap cannot create its namespaces
// here (the render then stops with CO-PRV-004).
func renderInSandbox(t *testing.T, opts *render.Options) *render.Result {
	t.Helper()

	res, err := render.Render(context.Background(), opts)

	var de *diag.Error
	if errors.As(err, &de) && de.Diagnostic.Code == diag.PRV004 {
		t.Skip("bubblewrap cannot create the namespaces here")
	}

	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	return res
}

// sandboxProbe is a renderer script that reports what the sandbox exposes: the network interfaces besides lo, and
// whether /etc/hostname and /home exist.
const sandboxProbe = `n=0
while read -r line; do
  case "$line" in *:*) name="${line%%:*}"; name="${name# }"; name="${name# }"; if [ "$name" != lo ]; then n=$((n+1)); fi;; esac
done < /proc/net/dev
etc=absent; if [ -e /etc/hostname ]; then etc=present; fi
home=absent; if [ -e /home ]; then home=present; fi
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: probe\n  namespace: shop\ndata:\n  interfaces: "%s"\n  etc: "%s"\n  home: "%s"\n' "$n" "$etc" "$home"`

// TestRealSandbox runs a renderer under bubblewrap: the network is unshared and the host is not visible.
func TestRealSandbox(t *testing.T) {
	t.Parallel()

	res := renderInSandbox(t, sandboxOptions(t, fakeOptions(t, fixtures(t), helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"), sandboxProbe, nil)))

	if res.Confinement != render.ConfinementProcessOS {
		t.Fatalf("confinement = %q, gaps %+v, diagnostics %+v", res.Confinement, res.Gaps, res.Diagnostics)
	}

	if res.Coverage != assurance.CoverageComplete || len(res.Resources) != 1 {
		t.Fatalf("coverage %s, %d resources, gaps %+v, diagnostics %+v", res.Coverage, len(res.Resources), res.Gaps, res.Diagnostics)
	}

	data, ok := res.Resources[0].Document["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %v", res.Resources[0].Document["data"])
	}

	expectDeep(t, "interfaces besides lo", data["interfaces"], "0")
	expectDeep(t, "/etc/hostname", data["etc"], "absent")
	expectDeep(t, "/home", data["home"], "absent")
}

// TestRealToolsUnderSandbox renders the Helm fixture with the real helm inside bubblewrap.
func TestRealToolsUnderSandbox(t *testing.T) {
	t.Parallel()

	exe := realTool(t, "helm")
	if exe == "" {
		t.Skip("helm is not installed")
	}

	id, err := render.Identify(context.Background(), nil, "helm", exe)
	if err != nil {
		t.Fatal(err)
	}

	res := renderInSandbox(t, sandboxOptions(t, &render.Options{
		Root: fixtures(t), Source: *helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"),
		Pins:   render.Pins{"helm": {Version: id.Version, SHA256: id.SHA256}},
		Locate: locator(map[string]string{"helm": exe}),
	}))

	if res.Confinement != render.ConfinementProcessOS || res.Coverage != assurance.CoverageComplete {
		t.Fatalf("confinement %q coverage %s gaps %+v diagnostics %+v", res.Confinement, res.Coverage, res.Gaps, res.Diagnostics)
	}

	if len(res.Occurrences) != 3 {
		t.Errorf("occurrences = %d", len(res.Occurrences))
	}
}
