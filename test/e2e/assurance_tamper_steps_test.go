package e2e_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
)

const (
	asrTamperDir       = "build/attestations/"
	asrTamperedPath    = asrTamperDir + asrModified
	asrTamperBadJSON   = asrTamperDir + asrMalformed
	asrTamperValidPath = asrTamperDir + asrShop
)

// asrTamperRecords are the deployment records and observations of the tamper worlds.
func asrTamperRecords() []asrRecord {
	return []asrRecord{
		{path: "deploy-1.json", digest: asrDigestA},
		{path: "deploy-2.json", container: "sidecar", digest: asrDigestA},
		{path: "observed-1.json", digest: asrDigestA},
		{path: "observed-2.json", container: "sidecar", digest: asrDigestA},
	}
}

// asrTamperBuild builds a world with the attestations and checks it.
func asrTamperBuild(t *testing.T, attestations ...string) (*asrRepo, jv, cliResult) {
	t.Helper()

	w := &asrWorld{t: t, sidecar: asrRepository + "@" + asrDigestA, attestations: attestations, records: asrTamperRecords()}
	r := w.build()
	doc, res := r.check()

	return r, doc, res
}

// asrTamperIsWarning is true for a CO-PRV-009 warning about the file.
func asrTamperIsWarning(d jv, path string) bool {
	if d.at("code").str() != "CO-PRV-009" || d.at("severity").str() != "warning" {
		return false
	}

	return d.at("source", "uri").str() == path
}

// asrTamperWarnings counts the warnings about the file.
func asrTamperWarnings(doc jv, path string) int {
	n := 0

	for _, d := range doc.at("diagnostics").list() {
		if asrTamperIsWarning(d, path) {
			n++
		}
	}

	return n
}

// asrTamperRequireBadFileWarnings wants one warning for each of the changed and the malformed file.
func asrTamperRequireBadFileWarnings(t *testing.T, doc jv) {
	t.Helper()

	for _, path := range []string{asrTamperedPath, asrTamperBadJSON} {
		if got := asrTamperWarnings(doc, path); got != 1 {
			t.Errorf("warnings for %s = %d, want 1", path, got)
		}
	}
}

// asrTamperRejectedForIntegrity is true when the one rejected statement is the changed file with an integrity failure.
func asrTamperRejectedForIntegrity(rejected []jv) bool {
	if len(rejected) != 1 || rejected[0].at("path").str() != asrTamperedPath {
		return false
	}

	return strings.Contains(rejected[0].at("details").text(), "integrity-failure")
}

// asrTamperRequireUnknownLineage wants no lineage entry that passes, and the app and sidecar entries to be unknown.
func asrTamperRequireUnknownLineage(t *testing.T, doc jv) {
	t.Helper()

	for _, container := range []string{"app", "sidecar"} {
		entry := lineageOf(t, doc, container)
		if entry.at("verdict").str() != "unknown" || entry.at("statement").present() {
			t.Errorf("%s lineage = %s", container, entry.text())
		}
	}

	app := lineageOf(t, doc, "app")
	if !asrTamperRejectedForIntegrity(app.at("rejected").list()) {
		t.Errorf("rejected = %s", app.at("rejected").text())
	}

	for _, entry := range doc.at("assurance", "lineage").list() {
		if entry.at("verdict").str() == "pass" {
			t.Errorf("a lineage pass without a good statement: %s", entry.text())
		}
	}
}

// asrTamperSummaryTrusted is true when the statement summary calls the changed statement admissible or authenticated.
func asrTamperSummaryTrusted(summary jv) bool {
	if summary.at("admissible").text() != "false" || summary.at("signature").str() != "failed" {
		return true
	}

	return summary.at("integrity").str() == "producer-authenticated"
}

// asrTamperRequireIndependentClaims wants the claims that do not read the statements to continue.
func asrTamperRequireIndependentClaims(t *testing.T, doc jv) {
	t.Helper()

	for _, rule := range []string{"ASR-REN-001", "ASR-PRV-002"} {
		if got := doc.rule(rule).at("verdict").str(); got != "pass" {
			t.Errorf("%s = %s, want pass: %s", rule, got, doc.rule(rule).text())
		}
	}

	app := lineageOf(t, doc, "app")
	if app.at("deployment", "deployedDigest", "verdict").str() != "pass" {
		t.Errorf("deployment claims stopped: %s", app.at("deployment").text())
	}
}

// asrTamperRequirePersisted wants the bundle to hold the changed statement as it was read, with no integrity field.
// What the bundle states about the file is its digest, which is the digest of the changed bytes, and nothing in it says
// that the producer was authenticated.
func asrTamperRequirePersisted(t *testing.T, r *asrRepo, summary jv) {
	t.Helper()

	dir, paths := r.bundleFiles()
	digest := summary.at("digest").str()

	if want := "sha256:" + r.sha(asrTamperedPath); digest != want {
		t.Errorf("the statement digest = %s, want the digest of the changed file %s", digest, want)
	}

	persisted := bundleStatements(t, dir, paths)
	if _, ok := persisted[digest]; !ok {
		t.Fatalf("the bundle does not hold the changed statement %s: %v", digest, paths)
	}

	data, err := os.ReadFile(filepath.Join(dir, "assurance", "statements", strings.TrimPrefix(digest, "sha256:")+".json"))
	if err != nil {
		t.Fatal(err)
	}

	if text := string(data); strings.Contains(text, "producer-authenticated") || strings.Contains(text, `"integrity"`) {
		t.Errorf("the persisted statement states an integrity level:\n%s", text)
	}
}

// asrTamperRequireNoGoodStatement checks the world where every statement is bad.
func asrTamperRequireNoGoodStatement(t *testing.T) {
	t.Helper()

	r, doc, _ := asrTamperBuild(t, asrModified, asrMalformed)

	asrTamperRequireBadFileWarnings(t, doc)

	// The dependent claim is unknown, and no lineage entry passes.
	asrTamperRequireUnknownLineage(t, doc)

	// The statements are listed with the integrity that they have: none is authenticated.
	summary := doc.at("assurance", "statements").where("path", asrTamperedPath)
	if asrTamperSummaryTrusted(summary) {
		t.Errorf("tampered statement = %s", summary.text())
	}

	// The claims that do not read the statements continue.
	asrTamperRequireIndependentClaims(t, doc)

	// The explanation says that the claim is unknown.
	containsAll(t, r.explain("ASR-PRV-001").output(), "Verdict: unknown", "no-evidence-supplied")

	asrTamperRequirePersisted(t, r, summary)
}

// asrTamperRequireBadAmongValid checks the world with a valid statement beside the bad files.
func asrTamperRequireBadAmongValid(t *testing.T) {
	t.Helper()

	_, doc, res := asrTamperBuild(t, asrShop, asrMalformed, asrModified)

	asrTamperRequireBadFileWarnings(t, doc)

	if got := asrTamperWarnings(doc, asrTamperValidPath); got != 0 {
		t.Errorf("warnings for the valid statement = %d", got)
	}

	app := lineageOf(t, doc, "app")
	if app.at("verdict").str() != "pass" || app.at("statement", "path").str() != asrTamperValidPath {
		t.Errorf("app lineage = %s", app.text())
	}

	if rejected := app.at("rejected").list(); len(rejected) != 1 || rejected[0].at("path").str() != asrTamperedPath {
		t.Errorf("rejected = %s", app.at("rejected").text())
	}

	if got := doc.rule("ASR-PRV-001").at("verdict").str(); got != "pass" || res.code != 0 {
		t.Errorf("ASR-PRV-001 = %s, exit %d", got, res.code)
	}
}

// asrTamperSecretOutputs gathers every output that must not show the secret value, by name.
func asrTamperSecretOutputs(t *testing.T, r *asrRepo, res cliResult) map[string]string {
	t.Helper()

	outputs := map[string]string{"check stdout": res.stdout, "check stderr": res.stderr, "results.json": r.read("build/concordia/results.json")}

	for _, rule := range []string{"ASR-PRV-001", "ASR-REN-001"} {
		outputs["explain "+rule] = r.explain(rule).output()
	}

	for _, sub := range [][]string{{"trace", "api-service"}, {"--format", "json", "trace", "api-service"}} {
		outputs[strings.Join(sub, " ")] = r.cli(r.env, sub...).output()
	}

	dir, paths := r.bundleFiles()

	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}

		outputs["bundle "+rel] = string(data)
	}

	return outputs
}

// asrTamperRequireSecretInventory wants the Secret to stay an inventory entry: name and key names, with the redaction
// recorded in the manifest.
func asrTamperRequireSecretInventory(t *testing.T, r *asrRepo, outputs map[string]string) {
	t.Helper()

	render := outputs["bundle assurance/render/production.json"]
	if !strings.Contains(render, "db-credentials") || !strings.Contains(render, `"password"`) {
		t.Errorf("the render file has no Secret name and key name:\n%s", render)
	}

	_, manifest := r.exportBundle()
	if !slices.ContainsFunc(manifest.Redactions, func(red bundle.Redaction) bool {
		return red.Class == "secret-data" && red.Property == "password"
	}) {
		t.Errorf("the manifest has no redaction record for the password: %+v", manifest.Redactions)
	}
}

// asrTamperRequireSecretHidden checks the world with a Secret in the rendered manifest.
func asrTamperRequireSecretHidden(t *testing.T) {
	t.Helper()

	w := asrLineageWorld(t)
	w.withSecret = true
	r := w.build()
	doc, res := r.check()

	outputs := asrTamperSecretOutputs(t, r, res)

	for name, text := range outputs {
		if strings.Contains(text, asrSecretValue) {
			t.Errorf("%s contains the secret value", name)
		}
	}

	asrTamperRequireSecretInventory(t, r, outputs)

	if doc.rule("ASR-REN-001").at("verdict").str() != "pass" {
		t.Errorf("ASR-REN-001 = %s", doc.rule("ASR-REN-001").text())
	}
}
