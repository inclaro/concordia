package render_test

import (
	"context"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
)

func TestNormalizeVersion(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"v4.3.0":          "4.3.0",
		"4.3.0":           "4.3.0",
		"v4.3.0+gbec5b06": "4.3.0",
		"v5.6.0\n":        "5.6.0",
		"v3.1.0-rc.1":     "3.1.0-rc.1",
	} {
		if got := render.NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRendererWithMatchingPin(t *testing.T) {
	t.Parallel()

	exe, pin := fakeRenderer(t, "echo unused")
	run := &recorder{}

	got, id, err := render.ResolveRenderer(context.Background(), run.run, locator(map[string]string{"helm": exe}), "helm", render.Pins{"helm": pin})
	if err != nil {
		t.Fatalf("ResolveRenderer: %v", err)
	}

	if got != exe && !strings.HasSuffix(got, "fake-renderer") {
		t.Errorf("path = %s", got)
	}

	// The run manifest records name, version, and digest.
	if want := (render.Identity{Name: "helm", Version: "v4.3.0", SHA256: pin.SHA256}); id != want {
		t.Errorf("identity = %+v, want %+v", id, want)
	}

	// A pin without the `v` and without the `sha256:` prefix, in upper case, still matches.
	pin.Version = "4.3.0"
	pin.SHA256 = strings.ToUpper(strings.TrimPrefix(pin.SHA256, "sha256:"))

	if _, _, err := render.ResolveRenderer(context.Background(), run.run, locator(map[string]string{"helm": exe}), "helm", render.Pins{"helm": pin}); err != nil {
		t.Errorf("a pin in another spelling: %v", err)
	}
}

func TestResolveRendererToolchainErrors(t *testing.T) {
	t.Parallel()

	exe, pin := fakeRenderer(t, "echo unused")
	other, _ := fakeRenderer(t, "echo different")

	tests := []struct {
		name   string
		locate render.Locator
		pins   render.Pins
		want   []string
	}{
		{name: "renderer is missing", locate: locator(nil), pins: render.Pins{"helm": pin}, want: []string{"helm was not found"}},
		{name: "renderer is not pinned", locate: locator(map[string]string{"helm": exe}), pins: nil, want: []string{"not pinned", pin.SHA256}},
		{name: "pin without digest", locate: locator(map[string]string{"helm": exe}), pins: render.Pins{"helm": {Version: "4.3.0"}}, want: []string{"not pinned"}},
		{
			name: "different digest", locate: locator(map[string]string{"helm": other}), pins: render.Pins{"helm": pin},
			want: []string{"expected helm v4.3.0 (" + pin.SHA256 + ")", "found helm v4.3.0 (sha256:"},
		},
		{
			name: "different version", locate: locator(map[string]string{"helm": exe}), pins: render.Pins{"helm": {Version: "4.2.0", SHA256: pin.SHA256}},
			want: []string{"expected helm 4.2.0", "found helm v4.3.0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := render.ResolveRenderer(context.Background(), nil, tc.locate, "helm", tc.pins)

			de := diagError(t, err)
			if de.Diagnostic.Code != diag.PRV003 || de.ExitCode != diag.Toolchain {
				t.Fatalf("got %s exit %d, want CO-PRV-003 exit 5", de.Diagnostic.Code, de.ExitCode)
			}

			for _, w := range tc.want {
				if !strings.Contains(de.Diagnostic.Message, w) {
					t.Errorf("message %q does not contain %q", de.Diagnostic.Message, w)
				}
			}
		})
	}
}

func TestIdentifyReadsRealToolVersion(t *testing.T) {
	t.Parallel()

	exe := realTool(t, "helm")
	if exe == "" {
		t.Skip("helm is not installed")
	}

	id, err := render.Identify(context.Background(), nil, "helm", exe)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	if !strings.HasPrefix(id.Version, "v") || !strings.HasPrefix(id.SHA256, "sha256:") || len(id.SHA256) != len("sha256:")+64 {
		t.Errorf("identity = %+v", id)
	}
}

func TestRenderStopsWithoutPin(t *testing.T) {
	t.Parallel()

	exe, _ := fakeRenderer(t, "echo unused")
	run := &recorder{}

	res, err := render.Render(context.Background(), &render.Options{
		Root: fixtures(t), Source: *helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"),
		Run: run.run, Locate: locator(map[string]string{"helm": exe}),
	})

	if de := diagError(t, err); de.Diagnostic.Code != diag.PRV003 || de.ExitCode != diag.Toolchain {
		t.Errorf("error = %v", err)
	}

	if res.Renderer != nil || len(res.Resources) != 0 || res.OutputDigest != "" {
		t.Errorf("no rendered evidence from a renderer that failed its pin: %+v", res)
	}

	if len(run.renders()) != 0 {
		t.Errorf("the renderer rendered: %v", run.renders())
	}
}
