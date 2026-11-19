package render_test

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
)

// TestHostileHelmChartIsRefusedBeforeAnyProgram: a chart with dependencies that are not vendored and a values file
// that asks for a post-renderer. Nothing is fetched and no program starts, not even the version probe (A38).
func TestHostileHelmChartIsRefusedBeforeAnyProgram(t *testing.T) {
	t.Parallel()

	root := filepath.Join(fixtures(t), "helm-hostile")

	opts := fakeOptions(t, root, helmSource("production", "chart", "values/post-renderer.yaml"), minimalManifest, forbidden(t))

	res, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	kinds := gapKinds(res)
	slices.Sort(kinds)

	if want := []string{render.GapPostRenderer, render.GapRemoteChartRepo, render.GapUnvendoredDependency}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("gaps = %v, want %v", kinds, want)
	}

	if res.Coverage != assurance.CoverageFailed || len(res.Resources) != 0 || res.OutputDigest != "" {
		t.Errorf("coverage %s with %d resources", res.Coverage, len(res.Resources))
	}

	for _, g := range res.Gaps {
		switch g.Kind {
		case render.GapRemoteChartRepo:
			if !strings.Contains(g.Detail, "redis") || !strings.Contains(g.Detail, "https://charts.example.invalid/stable") {
				t.Errorf("gap %+v does not name the dependency and the repository", g)
			}
		case render.GapUnvendoredDependency:
			if !strings.Contains(g.Detail, "common") {
				t.Errorf("gap %+v does not name the dependency", g)
			}
		}
	}

	if !hasDiag(res, diag.PRV006) {
		t.Errorf("no CO-PRV-006 warning: %+v", res.Diagnostics)
	}
}

func TestPostRendererRequestIsIgnoredAndPartial(t *testing.T) {
	t.Parallel()

	root := filepath.Join(fixtures(t), "helm-hostile")
	run := &recorder{}

	res, err := render.Render(context.Background(), fakeOptions(t, root, helmSource("production", "reader", "values/post-renderer.yaml"), minimalManifest, run.run))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if res.Coverage != assurance.CoveragePartial || !reflect.DeepEqual(gapKinds(res), []string{render.GapPostRenderer}) {
		t.Errorf("coverage %s, gaps %v", res.Coverage, res.Gaps)
	}

	if len(res.Resources) != 1 {
		t.Errorf("the render proceeds without the post-renderer: %d resources", len(res.Resources))
	}

	renders := run.renders()
	if len(renders) != 1 {
		t.Fatalf("renders = %v", renders)
	}

	for _, arg := range renders[0] {
		if arg == "--post-renderer" || strings.Contains(arg, "evil.sh") {
			t.Errorf("the post-renderer reached the command line: %v", renders[0])
		}
	}

	if !hasDiag(res, diag.PRV006) {
		t.Errorf("no warning that the request was ignored: %+v", res.Diagnostics)
	}
}

func TestHostileKustomizeOverlaysAreRefused(t *testing.T) {
	t.Parallel()

	root := filepath.Join(fixtures(t), "kustomize-hostile")

	tests := []struct {
		dir  string
		want []string
	}{
		{dir: "remote-base", want: []string{render.GapRemoteBase, render.GapRemoteBase, render.GapRemoteBase}},
		{dir: "exec-plugin", want: []string{render.GapExecGenerator}},
		{dir: "krm-function", want: []string{render.GapKRMFunction}},
		{dir: "helm-inflation", want: []string{render.GapHelmInflation}},
		{dir: "escape", want: []string{render.GapPathEscape}},
		{dir: "plugin-escape", want: []string{render.GapPathEscape}},
	}

	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()

			res, err := render.Render(context.Background(), fakeOptions(t, root, kustomizeSource("production", tc.dir), minimalManifest, forbidden(t)))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			if got := gapKinds(res); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("gaps = %v, want %v", got, tc.want)
			}

			if res.Coverage != assurance.CoverageFailed || len(res.Resources) != 0 {
				t.Errorf("coverage = %s, resources %d", res.Coverage, len(res.Resources))
			}

			if !strings.HasSuffix(tc.dir, "escape") && !hasDiag(res, diag.PRV006) {
				t.Errorf("no CO-PRV-006 warning naming the construct: %+v", res.Diagnostics)
			}
		})
	}
}
