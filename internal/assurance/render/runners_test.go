package render_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestReleaseNameThatLooksLikeAFlagIsRefused(t *testing.T) {
	t.Parallel()

	bad := helmSource("production", "helm-shop/chart")
	bad.Release = "--post-renderer=evil"

	res, err := render.Render(context.Background(), fakeOptions(t, fixtures(t), bad, minimalManifest, forbidden(t)))
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage != assurance.CoverageFailed || !reflect.DeepEqual(gapKinds(res), []string{render.GapInvalidSource}) {
		t.Errorf("coverage %s, gaps %v", res.Coverage, gapKinds(res))
	}
}

// TestValuesSymlinkIsRefusedByRender: the values file is a link to a file outside the declared paths. The render
// of that entry fails with a gap, no program starts, and the content is never read.
func TestValuesSymlinkIsRefusedByRender(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "chart", "Chart.yaml"), "apiVersion: v2\nname: x\nversion: 0.1.0\n")

	if err := os.Symlink("/etc/hostname", filepath.Join(root, "values.yaml")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	src := &workspace.RenderSource{Environment: "production", Tool: "helm", Path: "chart", ValuesFiles: []string{"values.yaml"}}

	res, err := render.Render(context.Background(), fakeOptions(t, root, src, minimalManifest, forbidden(t)))
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage != assurance.CoverageFailed || !reflect.DeepEqual(gapKinds(res), []string{render.GapSymlink}) {
		t.Errorf("coverage %s, gaps %+v", res.Coverage, res.Gaps)
	}

	if res.InputDigest != "" && strings.Contains(res.InputDigest, "hostname") {
		t.Error("the input digest was computed from the linked file")
	}
}

// TestRealHelmReadsNothingOutsideTheChart is the A38 case: templates that read a file outside the chart, an absolute
// file, and the cluster. The chart is copied alone, and the canary file beside it is not in the copy.
func TestRealHelmReadsNothingOutsideTheChart(t *testing.T) {
	t.Parallel()

	exe := realTool(t, "helm")
	if exe == "" {
		t.Skip("helm is not installed")
	}

	id, err := render.Identify(context.Background(), nil, "helm", exe)
	if err != nil {
		t.Fatal(err)
	}

	run := &recorder{}

	res, err := render.Render(context.Background(), &render.Options{
		Root:   filepath.Join(fixtures(t), "helm-hostile"),
		Source: workspace.RenderSource{Environment: "production", Tool: "helm", Path: "reader", Release: "reader"},
		Pins:   render.Pins{"helm": {Version: id.Version, SHA256: id.SHA256}},
		Locate: locator(map[string]string{"helm": exe}), Run: run.run,
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(fixtures(t), "helm-hostile", "outside.txt"))
	if err != nil {
		t.Fatal(err)
	}

	canary := strings.TrimSpace(string(data))

	serialized := string(mustJSON(t, res.Resources)) + string(mustJSON(t, res.Diagnostics))
	if strings.Contains(serialized, canary) {
		t.Errorf("data from outside the chart is in the result")
	}

	hostname, _ := os.ReadFile("/etc/hostname")
	if h := strings.TrimSpace(string(hostname)); h != "" && strings.Contains(serialized, h) {
		t.Errorf("the host name is in the result")
	}

	if res.Coverage == assurance.CoverageComplete && len(res.Gaps) > 0 {
		t.Errorf("a gap with complete coverage: %+v", res.Gaps)
	}

	if len(run.renders()) != 1 {
		t.Errorf("renders = %v", run.renders())
	}
}
