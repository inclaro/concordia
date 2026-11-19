package render_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
)

// TestRendererSeesOnlyTheScrubbedEnvironment runs a renderer that writes its environment and its working directory
// to a file, with proxies, KUBECONFIG, and tokens set in the process.
func TestRendererSeesOnlyTheScrubbedEnvironment(t *testing.T) {
	// Not parallel: the test sets process environment variables.
	for _, name := range []string{"HTTP_PROXY", "https_proxy", "KUBECONFIG", "HELM_REGISTRY_CONFIG", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"} {
		t.Setenv(name, "ambient-"+name)
	}

	out := filepath.Join(t.TempDir(), "env.txt")
	exe, pin := fakeRenderer(t, `/usr/bin/env > "`+out+`"; pwd >> "`+out+`"
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n'`)

	res, err := render.Render(t.Context(), &render.Options{
		Root: fixtures(t), Source: *helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"),
		Pins: render.Pins{"helm": pin}, Locate: locator(map[string]string{"helm": exe}),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if res.Coverage != assurance.CoverageComplete {
		t.Fatalf("coverage = %s, gaps %+v", res.Coverage, res.Gaps)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	text := string(data)
	if strings.Contains(text, "ambient-") || strings.Contains(text, "PROXY") || strings.Contains(text, "KUBECONFIG") {
		t.Errorf("the child saw the ambient environment:\n%s", text)
	}

	if !strings.Contains(text, "HOME=") || !strings.Contains(text, "concordia-render-") {
		t.Errorf("HOME is not a temporary directory:\n%s", text)
	}

	// The temporary path must not reach the result.
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, "concordia-render-") {
			t.Errorf("temporary path in %s", d.Message)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
