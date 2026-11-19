package render_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/workspace"
)

// shopCase is one environment of the chart and overlay fixtures (design task 3.8).
type shopCase struct {
	name     string
	src      *workspace.RenderSource
	recorded string
}

func shopCases() []shopCase {
	return []shopCase{
		{name: "helm-staging", src: helmSource("staging", "helm-shop/chart", "helm-shop/values/staging.yaml"), recorded: "helm-shop/recorded/staging.yaml"},
		{name: "helm-production", src: helmSource("production", "helm-shop/chart", "helm-shop/values/production.yaml"), recorded: "helm-shop/recorded/production.yaml"},
		{name: "kustomize-staging", src: kustomizeSource("staging", "kustomize-shop/overlays/staging"), recorded: "kustomize-shop/recorded/staging.yaml"},
		{name: "kustomize-production", src: kustomizeSource("production", "kustomize-shop/overlays/production"), recorded: "kustomize-shop/recorded/production.yaml"},
	}
}

type occurrenceSummary struct {
	Workload   string `json:"workload"`
	Container  string `json:"container"`
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Immutable  bool   `json:"immutable"`
}

type resourceSummary struct {
	Identity string `json:"identity"`
	Location string `json:"location"`
	Hook     string `json:"hook,omitempty"`
}

// shopSummary is what the golden stores: the evidence without digests, which depend on the renderer version, and
// without the renderer identity.
type shopSummary struct {
	Environment string              `json:"environment"`
	Tool        string              `json:"tool"`
	Coverage    string              `json:"coverage"`
	Resources   []resourceSummary   `json:"resources"`
	Occurrences []occurrenceSummary `json:"occurrences"`
}

func summarize(res *render.Result) shopSummary {
	s := shopSummary{Environment: res.Environment, Tool: res.Tool, Coverage: string(res.Coverage)}

	for i := range res.Resources {
		r := &res.Resources[i]
		s.Resources = append(s.Resources, resourceSummary{Identity: r.ID.String(), Location: r.Location, Hook: r.Hook})
	}

	for i := range res.Occurrences {
		o := &res.Occurrences[i]
		s.Occurrences = append(s.Occurrences, occurrenceSummary{
			Workload: o.Workload.Kind + "/" + o.Workload.Namespace + "/" + o.Workload.Name, Container: o.Container, Kind: o.Kind,
			Repository: o.Repository, Tag: o.Tag, Digest: o.Digest, Immutable: o.Immutable,
		})
	}

	return s
}

// checkShop checks what the specs say of the fixtures, whatever produced the output.
func checkShop(t *testing.T, tc shopCase, res *render.Result) {
	t.Helper()

	if res.Coverage != assurance.CoverageComplete {
		t.Fatalf("coverage = %s, gaps %+v, diagnostics %+v", res.Coverage, res.Gaps, res.Diagnostics)
	}

	occ := map[string]render.ContainerOccurrence{}
	for i := range res.Occurrences {
		occ[res.Occurrences[i].Workload.Name+"/"+res.Occurrences[i].Container] = res.Occurrences[i]
	}

	switch tc.name {
	case "helm-staging", "helm-production":
		checkHelmShop(t, tc, res, occ)
	default:
		checkKustomizeShop(t, tc, occ)
	}
}

func checkHelmShop(t *testing.T, tc shopCase, res *render.Result, occ map[string]render.ContainerOccurrence) {
	t.Helper()

	// A templated name and image render resolved: the identity has the resolved name and the release namespace
	// where CBM 0.12.1 records the literal template name with namespace `?` and no image edge (O11).
	api := occ["shop-api/api"]
	expectDeep(t, "api repository", api.Repository, "registry.example.com/shop/api")
	expectDeep(t, "api namespace", api.Workload.Namespace, "shop")
	expectDeep(t, "api name", api.Workload.Name, "shop-api")

	wantTag := map[string]string{"helm-staging": "1.4.1-rc1", "helm-production": "1.4.0"}[tc.name]
	if api.Tag != wantTag || api.Immutable {
		t.Errorf("api tag = %q immutable %v, want %q mutable", api.Tag, api.Immutable, wantTag)
	}

	_, hasSidecar := occ["shop-api/sidecar"]
	if hasSidecar != (tc.name == "helm-production") {
		t.Errorf("sidecar present = %v in %s", hasSidecar, tc.name)
	}

	checkHelmHook(t, tc, res)
	checkHelmSecret(t, tc, res)
}

func checkHelmHook(t *testing.T, tc shopCase, res *render.Result) {
	t.Helper()

	// The hook stays a resource with its annotation: it is never executed.
	hook := res.Resource(&render.ResourceID{Environment: tc.src.Environment, Group: "batch", Kind: "Job", Namespace: "shop", Name: "shop-migrate"})
	if hook == nil {
		t.Fatal("hook = nil")
	}

	expectDeep(t, "hook annotation", hook.Hook, "pre-install,pre-upgrade")
	expectDeep(t, "hook location", hook.Location, "helm-shop/chart/templates/hook-migrate.yaml")

	dep := res.Resource(&render.ResourceID{Environment: tc.src.Environment, Group: "apps", Kind: "Deployment", Namespace: "shop", Name: "shop-api"})
	if dep == nil {
		t.Fatal("the deployment is missing")
	}

	expectDeep(t, "the location is the chart file", dep.Location, "helm-shop/chart/templates/deployment.yaml")
}

func checkHelmSecret(t *testing.T, tc shopCase, res *render.Result) {
	t.Helper()

	secret := res.Resource(&render.ResourceID{Environment: tc.src.Environment, Kind: "Secret", Namespace: "shop", Name: "shop-credentials"})
	if secret == nil {
		t.Fatalf("secret = nil, redactions %v", res.Redactions)
	}

	expectDeep(t, "secret data keys", strings.Join(secret.DataKeys, ","), "password")

	if len(res.Redactions) == 0 {
		t.Errorf("secret = %+v, no redactions", secret)
	}

	const seed = "ASSURANCE-SEED-helm-password-6d2f"
	if strings.Contains(string(mustJSON(t, res)), seed) {
		t.Errorf("the secret value %q is in the result", seed)
	}
}

func checkKustomizeShop(t *testing.T, tc shopCase, occ map[string]render.ContainerOccurrence) {
	t.Helper()

	// A Kustomize `images:` override is in the occurrence: no code-graph edge records it (O11).
	api := occ["api/api"]
	sidecar := occ["api/sidecar"]

	if tc.name == "kustomize-production" {
		checkDigestOverride(t, &api, &sidecar)
	} else {
		checkTagOverride(t, &api, &sidecar)
	}

	expectDeep(t, "namespace", api.Workload.Namespace, "shop-"+tc.src.Environment)
}

func checkDigestOverride(t *testing.T, api, sidecar *render.ContainerOccurrence) {
	t.Helper()

	if !api.Immutable || api.Tag != "" {
		t.Errorf("api = %+v, want the digest of the override", api)
	}

	expectDeep(t, "api digest", api.Digest, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	if sidecar.Tag != "1.5.2" {
		t.Errorf("sidecar = %+v, want the tag of the override", sidecar)
	}
}

func checkTagOverride(t *testing.T, api, sidecar *render.ContainerOccurrence) {
	t.Helper()

	if api.Tag != "1.4.1-rc1" || api.Immutable {
		t.Errorf("api = %+v, want a mutable tag", api)
	}

	if sidecar.Tag != "1.5.0" {
		t.Errorf("api = %+v, sidecar = %+v", api, sidecar)
	}
}

// TestShopReplay renders the fixtures with a fake renderer that replays the output recorded from helm 4.3.0 and
// kustomize 5.6.0. It runs everywhere, so the occurrence golden is checked without the real tools.
func TestShopReplay(t *testing.T) {
	t.Parallel()

	for _, tc := range shopCases() {
		replayShop(t, tc)
	}
}

func replayShop(t *testing.T, tc shopCase) {
	t.Helper()

	run := &recorder{}
	body := `cat "` + filepath.Join(fixtures(t), filepath.FromSlash(tc.recorded)) + `"`

	res, err := render.Render(context.Background(), fakeOptions(t, fixtures(t), tc.src, body, run.run))
	if err != nil {
		t.Fatalf("%s: Render: %v", tc.name, err)
	}

	checkShop(t, tc, res)

	if res.Renderer == nil {
		t.Fatalf("%s: no renderer, confinement %s", tc.name, res.Confinement)
	}

	if res.Renderer.Name != tc.src.Tool || res.Confinement != render.ConfinementProcess {
		t.Errorf("%s: renderer %+v confinement %s", tc.name, res.Renderer, res.Confinement)
	}

	golden(t, "render-shop-"+tc.name+".json", summarize(res))

	renders := run.renders()
	if len(renders) != 1 {
		t.Fatalf("%s: renders = %v", tc.name, renders)
	}

	if tc.src.Tool == workspace.RenderToolHelm {
		want := []string{"template", "--include-crds", "--namespace", "shop", "--values", "./" + tc.src.ValuesFiles[0], "shop", "./helm-shop/chart"}
		if !reflect.DeepEqual(renders[0][1:], want) {
			t.Errorf("%s: arguments = %v, want %v", tc.name, renders[0][1:], want)
		}
	}
}

// TestShopRealRenderers runs the real helm and kustomize when they are installed (.bin/ of the repository or PATH).
func TestShopRealRenderers(t *testing.T) {
	t.Parallel()

	ran := false

	for _, tc := range shopCases() {
		exe := realTool(t, tc.src.Tool)
		if exe == "" {
			t.Logf("%s is not installed: skipped %s", tc.src.Tool, tc.name)

			continue
		}

		ran = true

		checkRealShop(t, tc, exe)
	}

	if !ran {
		t.Skip("neither helm nor kustomize is installed")
	}
}

func checkRealShop(t *testing.T, tc shopCase, exe string) {
	t.Helper()

	id, err := render.Identify(context.Background(), nil, tc.src.Tool, exe)
	if err != nil {
		t.Fatalf("%s: Identify: %v", tc.name, err)
	}

	opts := &render.Options{
		Root: fixtures(t), Source: *tc.src,
		Pins:   render.Pins{tc.src.Tool: {Version: id.Version, SHA256: id.SHA256}},
		Locate: locator(map[string]string{tc.src.Tool: exe}),
	}

	first := renderOrFail(t, tc.name+": Render", opts)
	checkShop(t, tc, first)

	if first.Renderer == nil {
		t.Fatalf("%s: renderer = nil, want %+v", tc.name, id)
	}

	if *first.Renderer != id {
		t.Errorf("%s: renderer = %+v, want %+v", tc.name, first.Renderer, id)
	}

	// The same inputs and renderer give the same digests.
	second := renderOrFail(t, tc.name+": second Render", opts)
	checkSameDigests(t, tc.name, first, second)

	golden(t, "render-shop-"+tc.name+".json", summarize(first))
}

// renderOrFail renders with opts and stops the test with the label when the render fails.
func renderOrFail(t *testing.T, label string, opts *render.Options) *render.Result {
	t.Helper()

	res, err := render.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}

	return res
}

func checkSameDigests(t *testing.T, name string, first, second *render.Result) {
	t.Helper()

	if first.InputDigest != second.InputDigest || first.OutputDigest != second.OutputDigest {
		t.Errorf("%s: digests differ between two renders", name)
	}

	if first.OutputDigest == "" {
		t.Errorf("%s: no output digest", name)
	}

	if len(render.CompareRenders(first, second)) != 0 {
		t.Errorf("%s: CompareRenders reports non-determinism", name)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
