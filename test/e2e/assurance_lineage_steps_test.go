package e2e_test

import (
	"strings"
	"testing"
)

// asrLinAllEqual says that every dotted path of the value has the string that wants gives for it.
func asrLinAllEqual(j jv, wants map[string]string) bool {
	for path, want := range wants {
		if j.at(strings.Split(path, ".")...).str() != want {
			return false
		}
	}

	return true
}

// asrLinTagOnlyUnknown says that the lineage entry is unknown with no immutable digest.
func asrLinTagOnlyUnknown(sidecar jv) bool {
	return asrLinAllEqual(sidecar, map[string]string{"verdict": "unknown", "reason": "no-evidence-supplied"})
}

// asrLinMissingDigestAndProvenance says that the entry lacks the digest and the provenance, and says why.
func asrLinMissingDigestAndProvenance(sidecar jv) bool {
	if sidecar.at("missing").text() != `["digest","provenance"]` {
		return false
	}

	return strings.Contains(sidecar.at("detail").str(), "no immutable digest")
}

// asrLinTagIsOnlyIdentity says that the occurrence of the entry has the tag and no digest.
func asrLinTagIsOnlyIdentity(sidecar jv) bool {
	if sidecar.at("occurrence", "tag").str() != "1.3.0" {
		return false
	}

	return !sidecar.at("occurrence", "digest").present()
}

// asrLinRuleUnknownAdvisory says that the rule result is unknown and advisory.
func asrLinRuleUnknownAdvisory(rule jv) bool {
	return asrLinAllEqual(rule, map[string]string{"verdict": "unknown", "enforcement": "advisory"})
}

// requireAsrLinTagNoDigestResult checks the result of the world with a tagged container and no digest.
func requireAsrLinTagNoDigestResult(t *testing.T, doc jv) jv {
	t.Helper()

	sidecar := lineageOf(t, doc, "sidecar")
	if !asrLinTagOnlyUnknown(sidecar) {
		t.Errorf("sidecar lineage = %s", sidecar.text())
	}

	if !asrLinMissingDigestAndProvenance(sidecar) {
		t.Errorf("sidecar missing = %s detail %q", sidecar.at("missing").text(), sidecar.at("detail").str())
	}

	if !asrLinTagIsOnlyIdentity(sidecar) {
		t.Errorf("the tag is the only identity of the occurrence: %s", sidecar.at("occurrence").text())
	}

	if app := lineageOf(t, doc, "app"); app.at("verdict").str() != "pass" {
		t.Errorf("app lineage = %s", app.text())
	}

	if rule := doc.rule("ASR-PRV-001"); !asrLinRuleUnknownAdvisory(rule) {
		t.Errorf("ASR-PRV-001 = %s", rule.text())
	}

	return sidecar
}

// requireAsrLinTagNoDigestProvenance checks that no statement is linked to the tag and that the bundle records the
// statements of the run.
func requireAsrLinTagNoDigestProvenance(t *testing.T, r *asrRepo, doc, sidecar jv) {
	t.Helper()

	if sidecar.at("statement").present() {
		t.Errorf("a statement is linked to the tag: %s", sidecar.at("statement").text())
	}

	dir, paths := r.bundleFiles()

	for _, summary := range doc.at("assurance", "statements").list() {
		requireStatementInBundle(t, dir, paths, summary.at("digest").str(), "")
	}
}

// asrLinUnlinkedUnknown says that the entry is unknown with no linked statement and no digest in the occurrence.
func asrLinUnlinkedUnknown(entry jv) bool {
	if entry.at("verdict").str() != "unknown" {
		return false
	}

	return !entry.at("statement").present() && !entry.at("occurrence", "digest").present()
}

// asrLinStatementAdmissible says that the statement is admissible and authenticated by its producer.
func asrLinStatementAdmissible(statement jv) bool {
	if statement.at("admissible").text() != "true" {
		return false
	}

	return statement.at("integrity").str() == "producer-authenticated"
}

// requireAsrLinTagMoved checks the result of the world with a tag that moved after the build.
func requireAsrLinTagMoved(t *testing.T, r *asrRepo, doc jv) {
	t.Helper()

	for _, container := range []string{"app", "sidecar"} {
		entry := lineageOf(t, doc, container)
		if !asrLinUnlinkedUnknown(entry) {
			t.Errorf("%s lineage = %s, want unknown with no linked statement", container, entry.text())
		}
	}

	statement := doc.at("assurance", "statements").where("kind", "build-provenance")
	if !asrLinStatementAdmissible(statement) {
		t.Errorf("attestation = %s", statement.text())
	}

	if out := r.explain("ASR-PRV-001").output(); !strings.Contains(out, "missing digest") || strings.Contains(out, "Verdict: pass") {
		t.Errorf("the explanation of a tag that moved: %s", out)
	}
}

// asrLinPassesWithContract says that the entry passes with the trust contract, the source and the statement of the
// trusted producer.
func asrLinPassesWithContract(entry jv) bool {
	if entry.at("trustContract", "digest").str() == "" {
		return false
	}

	return asrLinAllEqual(entry, map[string]string{
		"verdict":             "pass",
		"trustContract.id":    "provenance",
		"source.id":           "shop",
		"statement.integrity": "producer-authenticated",
		"statement.producer":  "ci",
	})
}

// asrLinDeploymentAgrees says that the deployed digest, the deployment and the runtime observation pass.
func asrLinDeploymentAgrees(deployment jv) bool {
	return asrLinAllEqual(deployment, map[string]string{
		"deployedDigest.level":        "deployment-execution",
		"deployedDigest.verdict":      "pass",
		"deploymentSucceeded.verdict": "pass",
		"runtimeDrift.level":          "runtime-observation",
		"runtimeDrift.verdict":        "pass",
	})
}

// requireAsrLinAgreeContainers checks the lineage entry of each container and gives the digest of the last statement.
func requireAsrLinAgreeContainers(t *testing.T, doc jv) string {
	t.Helper()

	var digest string

	for _, container := range []string{"app", "sidecar"} {
		entry := lineageOf(t, doc, container)
		if !asrLinPassesWithContract(entry) {
			t.Errorf("%s lineage = %s", container, entry.text())
		}

		if !strings.Contains(entry.at("detail").str(), "builder and source repository shop are in the trust contract") {
			t.Errorf("%s detail = %q", container, entry.at("detail").str())
		}

		if deployment := entry.at("deployment"); !asrLinDeploymentAgrees(deployment) {
			t.Errorf("%s deployment = %s", container, deployment.text())
		}

		digest = entry.at("statement", "digest").str()
	}

	return digest
}

// asrLinStatesSLSALevel says that the text names a SLSA level.
func asrLinStatesSLSALevel(text string) bool {
	lower := strings.ToLower(text)

	for _, marker := range []string{"slsalevel", "slsa-level", "slsa_level"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}

// requireAsrLinAgreeOutputs checks the exit code, the rules, the absence of a SLSA level and the explanation.
func requireAsrLinAgreeOutputs(t *testing.T, r *asrRepo, doc jv, res cliResult) {
	t.Helper()

	if res.code != 0 {
		t.Errorf("exit code = %d\n%s", res.code, res.stderr)
	}

	for _, rule := range []string{"ASR-PRV-001", "ASR-PRV-002", "ASR-REN-001"} {
		if got := doc.rule(rule).at("verdict").str(); got != "pass" {
			t.Errorf("%s = %s, want pass: %s", rule, got, doc.rule(rule).text())
		}
	}

	// No SLSA level in any output field.
	for _, text := range []string{res.stdout, r.read("build/concordia/results.json")} {
		if asrLinStatesSLSALevel(text) {
			t.Error("an output states a SLSA level")
		}
	}

	// The explanation of the rule shows the passing verdict and no finding.
	containsAll(t, r.explain("ASR-PRV-001").output(), "Verdict: pass", "No findings.")
}

// requireAsrLinAgreeTrace checks that the image-to-build step of the trace shows the verified lineage.
func requireAsrLinAgreeTrace(t *testing.T, r *asrRepo) {
	t.Helper()

	trace := r.cli(r.env, "trace", "api-service")
	if trace.code != 0 {
		t.Fatalf("trace: exit %d: %s", trace.code, trace.output())
	}

	if !strings.Contains(trace.stdout, "verified") {
		t.Errorf("the trace has no verified build step:\n%s", trace.stdout)
	}
}

// requireAsrLinDrift checks the drift of the app and the explanation of ASR-PRV-002.
func requireAsrLinDrift(t *testing.T, r *asrRepo, doc jv) {
	t.Helper()

	app := lineageOf(t, doc, "app")
	drift := app.at("deployment", "runtimeDrift")

	if drift.at("verdict").str() != "violation" || len(drift.at("findings").list()) == 0 {
		t.Fatalf("app drift = %s", drift.text())
	}

	if text := drift.text(); !strings.Contains(text, asrDigestA) || !strings.Contains(text, asrDigestB) {
		t.Errorf("the drift does not name both digests: %s", text)
	}

	// The explanation of ASR-PRV-002 shows the violation of the drifting container only.
	out := r.explain("ASR-PRV-002").output()
	containsAll(t, out, "Verdict: violation", "obligation-violation")
}
