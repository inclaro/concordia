package render_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

func synthetic(digest string, cov assurance.Coverage, docs ...string) *render.Result {
	res := &render.Result{Environment: "production", Path: "deploy/chart", Coverage: cov, OutputDigest: digest}

	for _, name := range docs {
		d, _, _ := strings.Cut(name, "=")
		_, dig, _ := strings.Cut(name, "=")
		res.Resources = append(res.Resources, render.Resource{ID: render.ResourceID{Environment: "production", Kind: "ConfigMap", Namespace: "n", Name: d}, Digest: dig})
	}

	return res
}

func TestCompareRenders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b         *render.Result
		name         string
		wantContains []string
		wantWarning  bool
	}{
		{name: "equal", a: synthetic("aaaa", assurance.CoverageComplete, "x=1"), b: synthetic("aaaa", assurance.CoverageComplete, "x=1")},
		{name: "both failed", a: synthetic("", assurance.CoverageFailed), b: synthetic("", assurance.CoverageFailed)},
		{name: "nil", a: nil, b: synthetic("aaaa", assurance.CoverageComplete)},
		{
			name: "different digest", a: synthetic("aaaa11112222", assurance.CoverageComplete, "x=1", "y=1"), b: synthetic("bbbb33334444", assurance.CoverageComplete, "x=1", "y=2"),
			wantWarning: true, wantContains: []string{"environment production", "aaaa11112222", "bbbb33334444", "ConfigMap/n/y"},
		},
		{
			name: "different coverage", a: synthetic("aaaa", assurance.CoverageComplete, "x=1"), b: synthetic("", assurance.CoverageFailed),
			wantWarning: true, wantContains: []string{"coverage complete, then failed"},
		},
		{
			name: "resource only in the second", a: synthetic("aaaa", assurance.CoverageComplete, "x=1"), b: synthetic("bbbb", assurance.CoverageComplete, "x=1", "z=1"),
			wantWarning: true, wantContains: []string{"ConfigMap/n/z"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := render.CompareRenders(tc.a, tc.b)
			if !tc.wantWarning {
				if len(got) != 0 {
					t.Errorf("diagnostics = %+v", got)
				}

				return
			}

			if len(got) != 1 || got[0].Code != diag.PRV014 || got[0].Severity != diag.SeverityWarning {
				t.Fatalf("diagnostics = %+v, want one CO-PRV-014 warning", got)
			}

			for _, w := range tc.wantContains {
				if !strings.Contains(got[0].Message, w) {
					t.Errorf("message %q does not contain %q", got[0].Message, w)
				}
			}
		})
	}
}

// TestRandomTemplateIsNotDeterministic uses a fake renderer whose output changes with every start (its process id
// is in a name), like a chart that calls randAlphaNum.
func TestRandomTemplateIsNotDeterministic(t *testing.T) {
	t.Parallel()

	opts := fakeOptions(t, fixtures(t), helmSource("production", "helm-random/chart"),
		`printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: random-%s\n  namespace: default\n' "$$"`, nil)

	first, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	second, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	if first.InputDigest != second.InputDigest {
		t.Error("the inputs are the same, so the input digests must be")
	}

	if got := render.CompareRenders(first, second); len(got) != 1 || got[0].Code != diag.PRV014 {
		t.Errorf("diagnostics = %+v", got)
	}
}

// TestRealRandomTemplate renders the chart with `randAlphaNum` twice with the real helm.
func TestRealRandomTemplate(t *testing.T) {
	t.Parallel()

	exe := realTool(t, "helm")
	if exe == "" {
		t.Skip("helm is not installed")
	}

	id, err := render.Identify(context.Background(), nil, "helm", exe)
	if err != nil {
		t.Fatal(err)
	}

	opts := &render.Options{
		Root:   filepath.Join(fixtures(t), "helm-random"),
		Source: workspace.RenderSource{Environment: "production", Tool: "helm", Path: "chart", Release: "random"},
		Pins:   render.Pins{"helm": {Version: id.Version, SHA256: id.SHA256}}, Locate: locator(map[string]string{"helm": exe}),
	}

	first, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	second, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	if first.Coverage != assurance.CoverageComplete || second.Coverage != assurance.CoverageComplete {
		t.Fatalf("coverage %s, %s: %+v", first.Coverage, second.Coverage, first.Gaps)
	}

	got := render.CompareRenders(first, second)
	if len(got) != 1 || got[0].Code != diag.PRV014 {
		t.Errorf("diagnostics = %+v, want the non-determinism warning", got)
	}
}
