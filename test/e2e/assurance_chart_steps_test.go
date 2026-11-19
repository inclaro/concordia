package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// asrChartRefusal is a hostile source and the gaps its refusal must name.
type asrChartRefusal struct {
	source string
	gaps   []string
}

// asrChartMaliciousRefusals are the sources of the malicious-content test: each is refused with its gaps.
var asrChartMaliciousRefusals = []asrChartRefusal{
	{"hostile/helm/chart", []string{"post-renderer", "remote-chart-repository", "unvendored-dependency"}},
	{"hostile/kustomize/remote-base", []string{"remote-base"}},
	{"hostile/kustomize/exec-plugin", []string{"exec-generator"}},
	{"hostile/kustomize/krm-function", []string{"krm-function"}},
	{"hostile/kustomize/helm-inflation", []string{"helm-chart-inflation"}},
	{"hostile/kustomize/escape", []string{"missing-source"}},
}

// asrChartMaliciousSources is the rendering section with the hostile helm chart and the hostile kustomize overlays.
func asrChartMaliciousSources() string {
	var sources strings.Builder

	sources.WriteString("      - {environment: production, tool: helm, path: hostile/helm/chart, valuesFiles: [hostile/helm/values/post-renderer.yaml]}\n")

	for _, dir := range []string{"remote-base", "exec-plugin", "krm-function", "helm-inflation", "escape"} {
		sources.WriteString("      - {environment: production, tool: kustomize, path: hostile/kustomize/" + dir + "}\n")
	}

	return sources.String()
}

// asrChartMaliciousFiles are the hostile helm and kustomize fixtures as workspace files.
func asrChartMaliciousFiles(t *testing.T) map[string]string {
	t.Helper()

	const fixtures = "../fixtures/assurance/"

	files := asrFixtureFiles(t, fixtures+"helm-hostile", "hostile/helm")
	for path, text := range asrFixtureFiles(t, fixtures+"kustomize-hostile", "hostile/kustomize") {
		files[path] = text
	}

	return files
}

// asrChartCheckJSON runs the JSON check under an exclusive slot and gives the parsed document and the raw output.
func asrChartCheckJSON(t *testing.T, r *asrRepo, env []string) (doc jv, raw string) {
	t.Helper()

	asrExclusive(func() {
		res := r.cli(env, "--format", "json", "check")
		asrExitInSet(t, res)

		raw = res.stdout

		var parsed map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
			t.Fatalf("check output is not JSON (exit %d): %v\n%s", res.code, err, res.stderr)
		}

		doc = jv{v: parsed}
	})

	return doc, raw
}

// asrChartRequireRefusals requires every hostile source to be refused with failed coverage, no resources and its gaps.
func asrChartRequireRefusals(t *testing.T, doc jv) {
	t.Helper()

	for _, want := range asrChartMaliciousRefusals {
		entry := doc.at("assurance", "rendered").where("source", want.source)
		if !entry.present() || entry.at("coverage").str() != "failed" || entry.at("resourceCount").text() != "0" {
			t.Errorf("rendered %s = %s, want failed coverage and no resources", want.source, entry.text())

			continue
		}

		for _, kind := range want.gaps {
			if !strings.Contains(entry.at("gaps").text(), `"`+kind+`"`) {
				t.Errorf("rendered %s has no gap %s: %s", want.source, kind, entry.at("gaps").text())
			}
		}
	}
}

// asrChartRequireNotStarted requires that none of the tripwire logs exists: no renderer started.
func asrChartRequireNotStarted(t *testing.T, helmLog, kustomizeLog string) {
	t.Helper()

	for name, log := range map[string]string{"helm": helmLog, "kustomize": kustomizeLog} {
		if data, err := os.ReadFile(log); err == nil {
			t.Errorf("%s was started:\n%s", name, data)
		}
	}
}

// asrChartRequireNotStartedByValidate requires that `assurance validate` started no renderer either.
func asrChartRequireNotStartedByValidate(t *testing.T, helmLog, kustomizeLog string) {
	t.Helper()

	if _, err := os.Stat(helmLog); err == nil {
		t.Error("helm was started by assurance validate")
	}

	if _, err := os.Stat(kustomizeLog); err == nil {
		t.Error("kustomize was started by assurance validate")
	}
}

// asrChartRequireRulesUnknown requires the rules that need the rendered environment to be unknown.
func asrChartRequireRulesUnknown(t *testing.T, doc jv, rules ...string) {
	t.Helper()

	for _, rule := range rules {
		if got := doc.rule(rule).at("verdict").str(); got != "unknown" {
			t.Errorf("%s = %s, want unknown", rule, got)
		}
	}
}

// asrChartOutputs names the outputs of a check, the results file, the SARIF log and `assurance validate`.
func asrChartOutputs(r *asrRepo, raw string, sarif, validate cliResult) map[string]string {
	return map[string]string{
		"check": raw, "results.json": r.read("build/concordia/results.json"), "sarif": sarif.output(), "assurance validate": validate.output(),
	}
}

// asrChartRequireNoCanary requires the canary of the outside file to be in none of the outputs.
func asrChartRequireNoCanary(t *testing.T, outputs map[string]string) {
	t.Helper()

	for name, text := range outputs {
		if strings.Contains(text, asrOutsideCanary) {
			t.Errorf("%s holds data from outside the declared paths", name)
		}
	}
}

// asrChartCopyHelm copies the real helm to a temporary directory and gives the copy. The OS sandbox of the render binds
// the renderer executable only, and not the home directory: the copy is the renderer itself. A wrapper that logs the
// starts cannot run in the sandbox.
func asrChartCopyHelm(t *testing.T) string {
	t.Helper()

	realHelm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm is not on the PATH: %v", err)
	}

	data, err := os.ReadFile(realHelm)
	if err != nil {
		t.Fatal(err)
	}

	helm := filepath.Join(t.TempDir(), "helm")
	if err := os.WriteFile(helm, data, 0o700); err != nil {
		t.Fatal(err)
	}

	return helm
}

// asrChartReaderRepo builds the workspace of the reader and hostile charts, with the real helm pinned by the lock. The
// first check of a workspace writes the lock file; the helm sources come after it, and `lock update` pins helm.
func asrChartReaderRepo(t *testing.T, helm string) (r *asrRepo, env []string) {
	t.Helper()

	const sources = "      - {environment: production, tool: helm, path: hostile/helm/reader, valuesFiles: [hostile/helm/values/post-renderer.yaml]}\n" +
		"      - {environment: production, tool: helm, path: hostile/helm/chart}\n"

	w := &asrWorld{t: t, extraFiles: asrFixtureFiles(t, "../fixtures/assurance/helm-hostile", "hostile/helm")}
	r = w.build()
	r.check()

	w.rendering = sources
	r.write("concordia.yaml", w.workspace())

	r.env = append(r.env, "HELM="+helm)
	env = r.env

	if res := r.cli(env, "lock", "update"); res.code != 0 {
		t.Fatalf("lock update: exit %d: %s", res.code, res.output())
	}

	return r, env
}

// asrChartReaderIsPartial reports whether the reader chart rendered one resource with the post-renderer gap and a digest.
func asrChartReaderIsPartial(reader jv) bool {
	if reader.at("coverage").str() != "partial" || reader.at("resourceCount").text() != "1" {
		return false
	}

	if !strings.Contains(reader.at("gaps").text(), `"post-renderer"`) {
		return false
	}

	return reader.at("outputDigest").str() != ""
}

// asrChartRequireReaderRendered requires the reader chart to be rendered with the post-renderer request ignored
// (partial, one resource, a recorded gap), and the hostile chart to be refused before helm: it has its gaps and no
// `render-error` gap, which is how a helm run that fails is recorded.
func asrChartRequireReaderRendered(t *testing.T, doc jv, raw string) {
	t.Helper()

	reader := doc.at("assurance", "rendered").where("source", "hostile/helm/reader")
	if !asrChartReaderIsPartial(reader) {
		t.Errorf("reader = %s, want a partial render of one resource with the post-renderer gap", reader.text())
	}

	chart := doc.at("assurance", "rendered").where("source", "hostile/helm/chart")
	if chart.at("coverage").str() != "failed" || chart.at("resourceCount").text() != "0" {
		t.Errorf("hostile chart = %s", chart.text())
	}

	// The post-renderer of the reader values is no gap of a run either: it was ignored.
	if strings.Contains(chart.at("gaps").text(), "render-error") {
		t.Errorf("helm was run for the hostile chart: %s", chart.at("gaps").text())
	}

	if strings.Contains(raw, "evil.sh") && !strings.Contains(reader.at("gaps").text(), "postRenderer") {
		t.Error("the post-renderer request reached the output")
	}
}

// asrChartBundleOutputs adds every file of the exported bundle to the outputs, keyed by "bundle <path>".
func asrChartBundleOutputs(t *testing.T, r *asrRepo, outputs map[string]string) {
	t.Helper()

	dirBundle, paths := r.bundleFiles()
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(dirBundle, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}

		outputs["bundle "+rel] = string(data)
	}
}
