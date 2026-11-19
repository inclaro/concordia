package deployment_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
)

func TestRenderShowsTheFiveStepsAndSaysNothingAboutRuntime(t *testing.T) {
	t.Parallel()

	_, in := a10()
	trace := deployment.BuildTrace(in)

	var out strings.Builder

	found, err := deployment.Render(&out, trace, "orders-service", deployment.RenderOptions{})
	if err != nil || !found {
		t.Fatalf("found = %v err = %v", found, err)
	}

	text := out.String()

	for _, want := range []string{
		"Evidence: declared configuration only. Not traced: deployment execution and runtime.", "Workloads of production: complete", "1. service [declared intent, exact]", "2. workload [parsed source fact, explicitly bound]",
		"3. container image", "4. build definition [inferred association, inferred]", "5. source evidence", "k8s/prod/orders.yaml:1", "build/Dockerfile:1",
		"Limits: declared-level-only, provider-may-merge-occurrences, image-provenance-unverified",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	for _, word := range []string{"running", "deployed", "succeeded"} {
		if strings.Contains(strings.ToLower(text), word) {
			t.Errorf("output says %q", word)
		}
	}
}

func TestRenderCapsListsAndShowsSource(t *testing.T) {
	t.Parallel()

	_, in := a10()
	trace := deployment.BuildTrace(in)

	var out strings.Builder

	opts := deployment.RenderOptions{Limit: 1, Environments: []string{"production"}, Source: func(l deployment.Location) []string { return []string{"line of " + l.Path} }}
	if _, err := deployment.Render(&out, trace, "orders-service", opts); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "… and ") || !strings.Contains(out.String(), "| line of k8s/prod/orders.yaml") {
		t.Errorf("output:\n%s", out.String())
	}

	out.Reset()

	found, err := deployment.Render(&out, trace, "orders-service", deployment.RenderOptions{Environments: []string{"staging"}})
	if err != nil || found || out.Len() != 0 {
		t.Errorf("found = %v err = %v out = %q", found, err, out.String())
	}
}

// With rendered evidence in an environment, the text trace says so and lists the sources and the rendered entities
// of the subject, instead of claiming that the rendered level is not available.
func TestRenderShowsTheRenderedLevelWhenTheEnvironmentHasIt(t *testing.T) {
	t.Parallel()

	_, in := a10()
	in.Evaluated = []deployment.EvaluatedEnvironment{{
		Environment: "production",
		Sources: []deployment.EvaluatedSource{
			{Kind: deployment.SourceRender, Tool: "helm", Path: "deploy/chart", Coverage: "complete", Digest: "sha256:" + strings.Repeat("a", 64)},
		},
		Entities: map[string][]deployment.Entity{"orders-service": {{
			Kind: deployment.KindWorkload, Identity: "production/apps/Deployment/shop/orders", Locations: []deployment.Location{{Path: "deploy/chart"}},
		}}},
	}}
	trace := deployment.BuildTrace(in)

	var out strings.Builder
	if _, err := deployment.Render(&out, trace, "orders-service", deployment.RenderOptions{Environments: []string{"production"}}); err != nil {
		t.Fatal(err)
	}

	text := out.String()

	for _, want := range []string{
		"rendered or evaluated from 1 source (below)", "Rendered or evaluated: evaluated",
		"render helm deploy/chart: complete", "workload production/apps/Deployment/shop/orders",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "The rendered, execution, and runtime levels are not available.") {
		t.Errorf("output still says the rendered level is not available:\n%s", text)
	}

	if got := trace.LevelsSentence(); !strings.Contains(got, "rendered or evaluated in production") {
		t.Errorf("trace level sentence = %q", got)
	}
}

// When every rendered source of the environment failed, the level is named as not available, with the failed sources.
func TestRenderSaysWhyTheRenderedLevelIsNotAvailable(t *testing.T) {
	t.Parallel()

	_, in := a10()
	in.Evaluated = []deployment.EvaluatedEnvironment{{
		Environment: "production",
		Sources:     []deployment.EvaluatedSource{{Kind: deployment.SourceRender, Tool: "kustomize", Path: "deploy/overlays/prod", Coverage: "failed"}},
	}}
	trace := deployment.BuildTrace(in)

	var out strings.Builder
	if _, err := deployment.Render(&out, trace, "orders-service", deployment.RenderOptions{Environments: []string{"production"}}); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	if !strings.Contains(text, "no rendered source or plan of production holds evidence") || !strings.Contains(text, "render kustomize deploy/overlays/prod: failed") {
		t.Errorf("output:\n%s", text)
	}

	if got := trace.LevelsSentence(); strings.Contains(got, "rendered-or-evaluated in") {
		t.Errorf("trace level sentence = %q, want the rendered level not available", got)
	}
}
