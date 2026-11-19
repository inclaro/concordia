package e2e_test

import (
	"slices"
	"strings"
	"testing"
)

// A38 malicious chart content: a Helm chart with dependencies that are not vendored and a values file that asks for a
// post-renderer, and Kustomize overlays with a remote base, an exec generator, a KRM function, Helm inflation, and a
// path escape. Nothing is fetched, no renderer starts (the tripwires record every start), the named gaps are reported
// with the CO-PRV-006 warning, the environment is not complete, and the claims that need it are unknown.
func TestAssuranceA38MaliciousChartContent(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	helmExe, helmLog := asrTripwire(t, "helm")
	kustomizeExe, kustomizeLog := asrTripwire(t, "kustomize")

	r := (&asrWorld{t: t, rendering: asrChartMaliciousSources(), extraFiles: asrChartMaliciousFiles(t)}).build()
	env := append(slices.Clone(r.env), "HELM="+helmExe, "KUSTOMIZE="+kustomizeExe)

	doc, raw := asrChartCheckJSON(t, r, env)

	// Every source is refused with its gap, and no resource of it is evidence.
	asrChartRequireRefusals(t, doc)

	if !doc.hasDiagnostic("CO-PRV-006") {
		t.Errorf("no CO-PRV-006 warning: %s", doc.at("diagnostics").text())
	}

	// Nothing started, nothing was read from outside the declared paths.
	asrChartRequireNotStarted(t, helmLog, kustomizeLog)

	// No false pass: the rules that need the rendered environment are unknown, and the explanation names the gap.
	asrChartRequireRulesUnknown(t, doc, "ASR-REN-001", "ASR-PRV-001", "ASR-PRV-002")
	containsAll(t, r.explain("ASR-REN-001").output(), "Verdict: unknown", "rendered-evidence-missing")

	// `assurance validate` shows the same refusals and starts nothing either.
	res := r.cli(env, "assurance", "validate")
	asrExitInSet(t, res)
	containsAll(t, res.stdout, "hostile/helm/chart", "coverage: failed")

	// The data of a file outside the declared paths is in no output.
	sarif := r.cli(env, "--format", "sarif", "check")
	asrExitInSet(t, sarif)

	asrChartRequireNoCanary(t, asrChartOutputs(r, raw, sarif, res))
	asrChartRequireNotStartedByValidate(t, helmLog, kustomizeLog)
}

// asrOutsideCanary is the content of test/fixtures/assurance/helm-hostile/outside.txt, a file next to the charts and
// outside every declared chart directory.
const asrOutsideCanary = "ASSURANCE-OUTSIDE-CANARY-1f4a"

// A38 malicious chart content, the part that renders: the chart `reader` reads `../outside.txt` and an absolute path
// with `.Files.Get` and calls `lookup`. It has no pre-scan finding that stops it (the post-renderer request in its
// values file is ignored and recorded as a gap), so the real helm renders it, confined as in production. The test shows
// that the render is real (one resource in the results and in the bundle), that the refused hostile chart never reached
// helm, and that the canary of the outside file is in no output: the results, the SARIF log, the validate output, and
// every file of the bundle.
func TestAssuranceA38ChartCannotReadOutsideFiles(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r, env := asrChartReaderRepo(t, asrChartCopyHelm(t))

	doc, raw := asrChartCheckJSON(t, r, env)

	// The reader chart rendered with the post-renderer request ignored; the hostile chart was refused before helm.
	asrChartRequireReaderRendered(t, doc, raw)

	// The canary of the outside file is in no output.
	sarif := r.cli(env, "--format", "sarif", "check")
	asrExitInSet(t, sarif)

	validate := r.cli(env, "assurance", "validate")
	asrExitInSet(t, validate)

	outputs := asrChartOutputs(r, raw, sarif, validate)
	asrChartBundleOutputs(t, r, outputs)
	asrChartRequireNoCanary(t, outputs)

	// The reader resource itself is in the bundle, so the absence of the canary is not an absence of the render.
	if render := outputs["bundle assurance/render/production.json"]; !strings.Contains(render, "reader") {
		t.Errorf("the render file does not list the reader ConfigMap:\n%s", render)
	}

	// The claims that need a complete environment are unknown.
	asrChartRequireRulesUnknown(t, doc, "ASR-REN-001")
}
