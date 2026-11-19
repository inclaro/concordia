package render_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestLimitsFromFillsDefaults(t *testing.T) {
	t.Parallel()

	got := render.LimitsFrom(workspace.RenderingLimits{})
	if got.Timeout != 120*time.Second {
		t.Errorf("default timeout = %v", got.Timeout)
	}

	if got.MaxOutputBytes != 64<<20 || got.MaxResources != 20000 {
		t.Errorf("defaults = %+v", got)
	}

	got = render.LimitsFrom(workspace.RenderingLimits{TimeoutSeconds: 5, MaxOutputBytes: 10, MaxResources: 3})
	expectDeep(t, "limits", got, render.Limits{Timeout: 5 * time.Second, MaxOutputBytes: 10, MaxResources: 3})
}

// failedWithoutOutput checks the contract of a limit: the coverage is failed, no resource set is kept as complete
// evidence, and a warning names the limit. Nothing is cut to fit.
func failedWithoutOutput(t *testing.T, res *render.Result, gapKind string) {
	t.Helper()

	if res.Coverage != assurance.CoverageFailed || res.OutputDigest != "" {
		t.Errorf("coverage %s, digest %q: a partial set must not be used as complete evidence", res.Coverage, res.OutputDigest)
	}

	if len(res.Resources) != 0 || len(res.Occurrences) != 0 {
		t.Errorf("%d resources, %d occurrences: a partial set must not be used as complete evidence", len(res.Resources), len(res.Occurrences))
	}

	if !reflect.DeepEqual(gapKinds(res), []string{gapKind}) {
		t.Errorf("gaps = %v, want %s", gapKinds(res), gapKind)
	}

	if !hasDiag(res, diag.PRV005) {
		t.Errorf("no CO-PRV-005 warning: %+v", res.Diagnostics)
	}
}

func TestOutputLimit(t *testing.T) {
	t.Parallel()

	opts := fakeOptions(t, fixtures(t), helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"), `cat "`+fixtures(t)+`/helm-shop/recorded/production.yaml"`, nil)
	opts.Limits = render.Limits{Timeout: time.Minute, MaxOutputBytes: 100, MaxResources: 100}

	res, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	failedWithoutOutput(t, res, "output-limit")

	if !strings.Contains(res.Diagnostics[0].Message, "100 bytes") {
		t.Errorf("the warning does not name the limit: %s", res.Diagnostics[0].Message)
	}
}

func TestResourceLimit(t *testing.T) {
	t.Parallel()

	opts := fakeOptions(t, fixtures(t), helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"), `cat "`+fixtures(t)+`/helm-shop/recorded/production.yaml"`, nil)
	opts.Limits = render.Limits{Timeout: time.Minute, MaxOutputBytes: 1 << 20, MaxResources: 2}

	res, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	failedWithoutOutput(t, res, "resource-limit")
}

func TestTimeLimit(t *testing.T) {
	t.Parallel()

	opts := fakeOptions(t, fixtures(t), helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"), "exec sleep 30", nil)
	opts.Limits = render.Limits{Timeout: 300 * time.Millisecond, MaxOutputBytes: 1 << 20, MaxResources: 100}

	started := time.Now()

	res, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if time.Since(started) > 20*time.Second {
		t.Error("the renderer was not stopped at the time limit")
	}

	failedWithoutOutput(t, res, "time-limit")
}

func TestPlainLimits(t *testing.T) {
	t.Parallel()

	opts := &render.Options{Root: fixtures(t), Source: *plainSource("production", "plain"), Run: forbidden(t), Limits: render.Limits{Timeout: time.Minute, MaxOutputBytes: 200, MaxResources: 100}}

	res, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	failedWithoutOutput(t, res, "output-limit")

	opts.Limits = render.Limits{Timeout: time.Minute, MaxOutputBytes: 1 << 20, MaxResources: 3}

	res, err = render.Render(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	failedWithoutOutput(t, res, "resource-limit")
}

// TestInterruptedRender: a canceled context stops the render, gives the context error (exit 130 in the command),
// and never leaves a complete coverage behind.
func TestInterruptedRender(t *testing.T) {
	t.Parallel()

	opts := fakeOptions(t, fixtures(t), helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"), "exec sleep 30", nil)

	ctx, cancel := context.WithCancel(context.Background())

	time.AfterFunc(400*time.Millisecond, cancel)

	res, err := render.Render(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	if res.Coverage == assurance.CoverageComplete || res.OutputDigest != "" {
		t.Errorf("an interrupted render must not be complete: %s", res.Coverage)
	}

	if len(res.Resources) != 0 {
		t.Errorf("an interrupted render keeps %d resources", len(res.Resources))
	}
}

func TestRenderCanceledBeforeStart(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, src := range []*workspace.RenderSource{plainSource("production", "plain"), helmSource("production", "helm-shop/chart")} {
		res, err := render.Render(ctx, &render.Options{Root: fixtures(t), Source: *src, Run: forbidden(t)})
		if !errors.Is(err, context.Canceled) || res.Coverage != assurance.CoverageFailed {
			t.Errorf("%s: error %v coverage %s", src.Tool, err, res.Coverage)
		}
	}
}
